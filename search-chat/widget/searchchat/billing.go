package searchchat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Billing 由接入方实现，接上自己的余额（积分）表。金额单位是积分，整数。
//
// 每次提问的调用顺序：
//
//	Reserve（提问前预扣最高费用）→ 调用模型 → Settle（成功，按实际用量结算，多退少补）
//	                                        → Refund（失败/超时/用户断开，全额退回）
//
// 同一个 (userID, requestID) 只会被 Reserve 一次，之后只会被 Settle 或 Refund 一次。
// 建议在数据库里给 (user_id, request_id) 建唯一索引，Settle / Refund 只处理状态还是「已冻结」的记录，
// 这样即使被重复调用也不会重复扣费或重复退款。参考写法见 README「接入账号系统和按用户扣费」。
type Billing interface {
	// Reserve 检查余额并冻结 amount 积分。
	// 余额不足返回 ErrInsufficientBalance（→ HTTP 402，不调用模型）；
	// requestID 已经用过返回 ErrDuplicateRequest（→ HTTP 409）；其他错误 → HTTP 503。
	// 必须是原子操作（例如 UPDATE ... WHERE balance >= ?），否则同一用户并发提问会把余额扣成负数。
	Reserve(ctx context.Context, userID, requestID string, amount int64) error
	// Settle 回答成功后调用：实际扣 c.Cost 积分（不会超过冻结的 c.Reserved），把 c.Reserved-c.Cost 退回余额。
	Settle(ctx context.Context, c Charge) error
	// Refund 失败时调用：把这次冻结的积分全部退回。
	Refund(ctx context.Context, userID, requestID string) error
}

var (
	ErrInsufficientBalance = errors.New("余额不足")
	ErrDuplicateRequest    = errors.New("请求 ID 重复")
)

// Charge 是一次提问的结算信息，传给 Billing.Settle。
type Charge struct {
	UserID           string
	RequestID        string // 每次提问唯一，可用来做幂等
	PromptTokens     int    // 输入 token 数（一轮里多次调用模型的总和）
	CompletionTokens int    // 输出 token 数
	Searches         int    // 成功的联网搜索次数
	Reserved         int64  // 提问前冻结的积分
	Cost             int64  // 应扣积分（≤ Reserved）
}

// 计费方式
const (
	BillingPerRequest = "per_request" // 按次（默认）
	BillingPerToken   = "per_token"   // 按用量
)

// Pricing 是计费规则。只在设置了 Config.Billing 时生效。
//
// 按次：每次提问扣 PerRequest，每次联网搜索再加 PerSearch。预扣 = PerRequest + 最多搜索次数 × PerSearch。
// 按用量：ceil((输入token × InputPer1K + 输出token × OutputPer1K) / 1000) + 搜索次数 × PerSearch，
// 预扣 MaxCharge，实际费用超过 MaxCharge 时按 MaxCharge 收。
type Pricing struct {
	Mode        string  // BILLING_MODE：per_request（默认）/ per_token
	PerRequest  int64   // PRICE_PER_REQUEST：按次，每次提问扣几积分，默认 1
	PerSearch   int64   // PRICE_PER_SEARCH：每次联网搜索额外加几积分，默认 0（两种方式都生效）
	InputPer1K  float64 // PRICE_INPUT_PER_1K：按用量，每 1000 个输入 token 多少积分
	OutputPer1K float64 // PRICE_OUTPUT_PER_1K：按用量，每 1000 个输出 token 多少积分
	MaxCharge   int64   // MAX_CHARGE：按用量，每次提问最多扣几积分（也是预扣金额），默认 100
}

func (p Pricing) withDefaults() Pricing {
	if p.Mode == "" {
		p.Mode = BillingPerRequest
	}
	if p.Mode == BillingPerRequest && p.PerRequest <= 0 {
		p.PerRequest = 1
	}
	if p.Mode == BillingPerToken && p.MaxCharge <= 0 {
		p.MaxCharge = 100
	}
	return p
}

func (p Pricing) problems() []string {
	switch p.Mode {
	case BillingPerRequest:
		if p.PerSearch < 0 {
			return []string{"PRICE_PER_SEARCH 不能是负数"}
		}
	case BillingPerToken:
		if p.InputPer1K < 0 || p.OutputPer1K < 0 || p.PerSearch < 0 || p.InputPer1K+p.OutputPer1K == 0 {
			return []string{"BILLING_MODE=per_token 时要设置 PRICE_INPUT_PER_1K / PRICE_OUTPUT_PER_1K（不能是负数）"}
		}
	default:
		return []string{"BILLING_MODE 只能是 per_request 或 per_token"}
	}
	return nil
}

// reserve 是提问前要冻结的最高费用。
func (p Pricing) reserve() int64 {
	if p.Mode == BillingPerToken {
		return p.MaxCharge
	}
	return p.PerRequest + maxSearchesPerTurn*p.PerSearch
}

// cost 按实际用量算应扣金额，不会超过 reserve()。
func (p Pricing) cost(promptTokens, completionTokens, searches int) int64 {
	c := p.PerRequest + int64(searches)*p.PerSearch
	if p.Mode == BillingPerToken {
		c = int64(math.Ceil((float64(promptTokens)*p.InputPer1K+float64(completionTokens)*p.OutputPer1K)/1000)) +
			int64(searches)*p.PerSearch
	}
	return max(0, min(c, p.reserve()))
}

// BearerToken 取出请求头 Authorization: Bearer <token> 里的 token，没有时返回空字符串。
// 方便在 Config.Authenticate 里使用。
func BearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// requestID 优先用前端传来的 X-Request-ID（同一次提问重试时不变），格式不对就由服务端生成。
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); requestIDRe.MatchString(id) {
		return id
	}
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// dailyLimiter 限制每个用户每天（服务器本地时间）最多提问几次。
type dailyLimiter struct {
	perDay int
	now    func() time.Time
	mu     sync.Mutex
	day    string
	counts map[string]int
}

func newDailyLimiter(perDay int) *dailyLimiter {
	return &dailyLimiter{perDay: perDay, now: time.Now, counts: map[string]int{}}
}

func (d *dailyLimiter) take(user string) bool {
	if d.perDay <= 0 {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := d.now().Format(time.DateOnly); day != d.day { // 过了零点，全部清零
		d.day, d.counts = day, map[string]int{}
	}
	if d.counts[user] >= d.perDay {
		return false
	}
	d.counts[user]++
	return true
}

// release 把没成功的提问还回当天额度。
func (d *dailyLimiter) release(user string) {
	if d.perDay <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counts[user] > 0 {
		d.counts[user]--
	}
}
