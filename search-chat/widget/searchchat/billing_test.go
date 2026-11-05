package searchchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBilling 是内存里的余额表，和 examples/billing-demo 的实现思路一样。
type fakeBilling struct {
	mu       sync.Mutex
	balance  map[string]int64
	holds    map[string]*fakeHold // key: userID + "/" + requestID
	settles  int
	refunds  int
	minSeen  int64 // 观察到的最低余额，用来检查有没有扣成负数
	reserveN atomic.Int32
}

type fakeHold struct {
	amount int64
	state  string // reserved / settled / refunded
}

func newFakeBilling(balance map[string]int64) *fakeBilling {
	return &fakeBilling{balance: balance, holds: map[string]*fakeHold{}}
}

func (b *fakeBilling) Reserve(ctx context.Context, user, reqID string, amount int64) error {
	b.reserveN.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	key := user + "/" + reqID
	if b.holds[key] != nil {
		return ErrDuplicateRequest
	}
	if b.balance[user] < amount {
		return ErrInsufficientBalance
	}
	b.balance[user] -= amount
	b.minSeen = min(b.minSeen, b.balance[user])
	b.holds[key] = &fakeHold{amount: amount, state: "reserved"}
	return nil
}

func (b *fakeBilling) Settle(ctx context.Context, c Charge) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.holds[c.UserID+"/"+c.RequestID]
	if h == nil || h.state != "reserved" { // 幂等：只处理还在冻结中的记录
		return nil
	}
	h.state = "settled"
	b.balance[c.UserID] += h.amount - c.Cost
	b.settles++
	return nil
}

func (b *fakeBilling) Refund(ctx context.Context, user, reqID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.holds[user+"/"+reqID]
	if h == nil || h.state != "reserved" {
		return nil
	}
	h.state = "refunded"
	b.balance[user] += h.amount
	b.refunds++
	return nil
}

func (b *fakeBilling) get(user string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.balance[user]
}

// fakeLLM 模拟 OpenAI 兼容接口，直接回答，不调用工具。
type fakeLLM struct {
	calls atomic.Int32
	fail  bool
	delay time.Duration
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-r.Context().Done():
			return
		}
	}
	if f.fail {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"2"}}],"usage":{"prompt_tokens":1200,"completion_tokens":300}}`))
}

// testAuth：Authorization: Bearer tok-<用户名>
func testAuth(r *http.Request) (string, error) {
	tok := BearerToken(r)
	if !strings.HasPrefix(tok, "tok-") {
		return "", errors.New("invalid token")
	}
	return strings.TrimPrefix(tok, "tok-"), nil
}

func newBillingHandler(t *testing.T, llm *fakeLLM, b Billing, mutate func(*Config)) http.Handler {
	t.Helper()
	srv := httptest.NewServer(llm)
	t.Cleanup(srv.Close)
	cfg := Config{
		LLMBaseURL: srv.URL + "/v1", LLMModel: "m", MaxConcurrent: 64,
		Authenticate: testAuth, Billing: b,
		Pricing: Pricing{Mode: BillingPerRequest, PerRequest: 2, PerSearch: 1},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return Handler(cfg)
}

type chatResult struct {
	code int
	body map[string]any
}

func ask(h http.Handler, token, reqID string) chatResult {
	r := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"1+1"}]}`))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if reqID != "" {
		r.Header.Set("X-Request-ID", reqID)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	return chatResult{w.Code, body}
}

func TestAuthRequired(t *testing.T) {
	llm := &fakeLLM{}
	h := newBillingHandler(t, llm, newFakeBilling(map[string]int64{"alice": 10}), nil)
	for _, tok := range []string{"", "bad"} {
		if res := ask(h, tok, ""); res.code != http.StatusUnauthorized || res.body["code"] != "login_required" {
			t.Errorf("token %q: got %d %v", tok, res.code, res.body)
		}
	}
	if llm.calls.Load() != 0 {
		t.Error("model must not be called without login")
	}
}

func TestInsufficientBalance(t *testing.T) {
	llm := &fakeLLM{}
	b := newFakeBilling(map[string]int64{"bob": 3}) // 预扣要 2+2×1=4
	h := newBillingHandler(t, llm, b, nil)
	res := ask(h, "tok-bob", "")
	if res.code != http.StatusPaymentRequired || res.body["code"] != "insufficient_balance" {
		t.Fatalf("got %d %v", res.code, res.body)
	}
	if llm.calls.Load() != 0 {
		t.Error("model must not be called when balance is insufficient")
	}
	if b.get("bob") != 3 {
		t.Errorf("balance changed: %d", b.get("bob"))
	}
}

func TestChargePerRequest(t *testing.T) {
	b := newFakeBilling(map[string]int64{"alice": 10})
	h := newBillingHandler(t, &fakeLLM{}, b, nil)
	res := ask(h, "tok-alice", "")
	if res.code != http.StatusOK || res.body["cost"] != float64(2) || res.body["request_id"] == "" {
		t.Fatalf("got %d %v", res.code, res.body)
	}
	if got := b.get("alice"); got != 8 { // 预扣 4，没搜索，结算 2，退回 2
		t.Errorf("balance = %d, want 8", got)
	}
}

func TestChargePerToken(t *testing.T) {
	b := newFakeBilling(map[string]int64{"alice": 100})
	h := newBillingHandler(t, &fakeLLM{}, b, func(c *Config) {
		c.Pricing = Pricing{Mode: BillingPerToken, InputPer1K: 1, OutputPer1K: 4, MaxCharge: 20}
	})
	res := ask(h, "tok-alice", "")
	// ceil((1200×1 + 300×4) / 1000) = ceil(2.4) = 3
	if res.code != http.StatusOK || res.body["cost"] != float64(3) {
		t.Fatalf("got %d %v", res.code, res.body)
	}
	if got := b.get("alice"); got != 97 {
		t.Errorf("balance = %d, want 97", got)
	}
}

func TestRefundOnModelError(t *testing.T) {
	b := newFakeBilling(map[string]int64{"alice": 10})
	h := newBillingHandler(t, &fakeLLM{fail: true}, b, nil)
	if res := ask(h, "tok-alice", ""); res.code != http.StatusBadGateway {
		t.Fatalf("got %d %v", res.code, res.body)
	}
	if got := b.get("alice"); got != 10 || b.refunds != 1 || b.settles != 0 {
		t.Errorf("balance=%d refunds=%d settles=%d, want full refund", got, b.refunds, b.settles)
	}
}

func TestRefundOnTimeoutAndDisconnect(t *testing.T) {
	b := newFakeBilling(map[string]int64{"alice": 10})
	h := newBillingHandler(t, &fakeLLM{delay: 2 * time.Second}, b, func(c *Config) { c.LLMTimeout = 100 * time.Millisecond })
	if res := ask(h, "tok-alice", ""); res.code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: got %d %v", res.code, res.body)
	}
	if got := b.get("alice"); got != 10 {
		t.Errorf("after timeout balance = %d, want 10", got)
	}

	// 用户断开连接
	h = newBillingHandler(t, &fakeLLM{delay: 2 * time.Second}, b, nil)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer tok-alice")
	time.AfterFunc(100*time.Millisecond, cancel)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got := b.get("alice"); got != 10 || b.settles != 0 {
		t.Errorf("after disconnect balance=%d settles=%d, want 10 / 0", got, b.settles)
	}
}

func TestConcurrentNoNegativeBalance(t *testing.T) {
	llm := &fakeLLM{delay: 50 * time.Millisecond}
	b := newFakeBilling(map[string]int64{"alice": 20}) // 预扣 4/次 → 最多同时 5 个
	h := newBillingHandler(t, llm, b, nil)
	const n = 30
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { codes[i] = ask(h, "tok-alice", "").code })
	}
	wg.Wait()
	ok, poor := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusPaymentRequired:
			poor++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 5 || poor != n-5 {
		t.Errorf("ok=%d 402=%d, want 5 / %d", ok, poor, n-5)
	}
	if b.minSeen < 0 {
		t.Errorf("balance went negative: %d", b.minSeen)
	}
	if got := b.get("alice"); got != 20-5*2 {
		t.Errorf("final balance = %d, want 10", got)
	}
	if int(llm.calls.Load()) != ok {
		t.Errorf("model called %d times, want %d", llm.calls.Load(), ok)
	}
}

func TestRequestIDNoDoubleCharge(t *testing.T) {
	llm := &fakeLLM{}
	b := newFakeBilling(map[string]int64{"alice": 10})
	h := newBillingHandler(t, llm, b, nil)
	if res := ask(h, "tok-alice", "req-0001-abcd"); res.code != http.StatusOK || res.body["request_id"] != "req-0001-abcd" {
		t.Fatalf("first: %d %v", res.code, res.body)
	}
	if res := ask(h, "tok-alice", "req-0001-abcd"); res.code != http.StatusConflict {
		t.Fatalf("duplicate: got %d %v", res.code, res.body)
	}
	if got := b.get("alice"); got != 8 || llm.calls.Load() != 1 {
		t.Errorf("balance=%d calls=%d, want 8 / 1", got, llm.calls.Load())
	}
	// 结算被重复调用也只扣一次
	b.Settle(t.Context(), Charge{UserID: "alice", RequestID: "req-0001-abcd", Cost: 2})
	if got := b.get("alice"); got != 8 {
		t.Errorf("after repeated settle balance = %d, want 8", got)
	}
	// 格式不对的 ID 由服务端重新生成，不会和别的请求撞
	if res := ask(h, "tok-alice", "x"); res.code != http.StatusOK || res.body["request_id"] == "x" {
		t.Errorf("bad id: %d %v", res.code, res.body)
	}
}

func TestDailyLimitPerUser(t *testing.T) {
	b := newFakeBilling(map[string]int64{"alice": 100, "bob": 100})
	h := newBillingHandler(t, &fakeLLM{}, b, func(c *Config) { c.DailyLimitPerUser = 2 })
	for i := range 2 {
		if res := ask(h, "tok-alice", ""); res.code != http.StatusOK {
			t.Fatalf("alice #%d: %d", i+1, res.code)
		}
	}
	if res := ask(h, "tok-alice", ""); res.code != http.StatusTooManyRequests || res.body["code"] != "daily_limit" {
		t.Errorf("alice #3: %d %v", res.code, res.body)
	}
	if res := ask(h, "tok-bob", ""); res.code != http.StatusOK {
		t.Errorf("bob should not share alice's quota: %d", res.code)
	}
	if got := b.get("alice"); got != 96 {
		t.Errorf("alice balance = %d, want 96 (3rd question not charged)", got)
	}

	// 第二天清零；失败的提问不占额度
	d := newDailyLimiter(1)
	day := time.Date(2026, 11, 5, 23, 0, 0, 0, time.Local)
	d.now = func() time.Time { return day }
	if !d.take("u") || d.take("u") {
		t.Fatal("limit 1 per day")
	}
	d.release("u")
	if !d.take("u") {
		t.Error("released quota should be usable again")
	}
	day = day.Add(2 * time.Hour)
	if !d.take("u") {
		t.Error("quota should reset the next day")
	}
}

func TestRateLimitByUser(t *testing.T) {
	h := newBillingHandler(t, &fakeLLM{}, newFakeBilling(map[string]int64{"alice": 100, "bob": 100}), func(c *Config) { c.RateLimitPerMinute = 1 })
	// 同一个 IP（httptest 默认 192.0.2.1），不同用户各自计数
	if ask(h, "tok-alice", "").code != http.StatusOK || ask(h, "tok-bob", "").code != http.StatusOK {
		t.Fatal("different users should have separate limits")
	}
	if c := ask(h, "tok-alice", "").code; c != http.StatusTooManyRequests {
		t.Errorf("alice 2nd: got %d", c)
	}
}

func TestPricing(t *testing.T) {
	p := Pricing{}.withDefaults()
	if p.Mode != BillingPerRequest || p.reserve() != 1 || p.cost(9999, 9999, 0) != 1 {
		t.Errorf("default pricing: %+v reserve=%d", p, p.reserve())
	}
	p = Pricing{PerRequest: 2, PerSearch: 3}.withDefaults()
	if p.reserve() != 8 || p.cost(0, 0, 1) != 5 || p.cost(0, 0, 2) != 8 {
		t.Errorf("per request: reserve=%d cost1=%d", p.reserve(), p.cost(0, 0, 1))
	}
	p = Pricing{Mode: BillingPerToken, InputPer1K: 0.5, OutputPer1K: 2, PerSearch: 1, MaxCharge: 10}.withDefaults()
	if p.cost(1000, 1000, 1) != 4 || p.cost(100000, 0, 0) != 10 {
		t.Errorf("per token: %d %d", p.cost(1000, 1000, 1), p.cost(100000, 0, 0))
	}
	if len((Config{LLMBaseURL: "x", LLMModel: "m", SearchEngine: "auto", Billing: newFakeBilling(nil)}).Problems()) == 0 {
		t.Error("Billing without Authenticate should be a config problem")
	}
}

// 没有 Authenticate / Billing 的老用法：不需要登录，响应里没有 cost。
func TestNoBillingUnchanged(t *testing.T) {
	llm := &fakeLLM{}
	h := newBillingHandler(t, llm, nil, func(c *Config) { c.Authenticate = nil; c.Billing = nil })
	res := ask(h, "", "")
	if res.code != http.StatusOK || res.body["answer"] != "2" {
		t.Fatalf("got %d %v", res.code, res.body)
	}
	if _, ok := res.body["cost"]; ok {
		t.Error("cost should be absent without Billing")
	}
}
