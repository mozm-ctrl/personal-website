// 示例：接入网站自己的账号系统，按用户扣费。
//
// 用内存里的用户表和余额表模拟「你的网站」：登录后拿到 Token，聊天时从这个用户的积分里扣费。
// 换成真实网站时，只需要把 authenticate 和 memoryBilling 换成查你自己的数据库（参考 README）。
//
//	cd widget
//	LLM_BASE_URL=http://localhost:11434/v1 LLM_MODEL=qwen3:8b LLM_REASONING_EFFORT=none \
//	  go run ./examples/billing-demo
//	浏览器打开 http://localhost:3001（端口可用 PORT 修改）
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"

	"search-chat-widget/searchchat"
)

// ===== 模拟的账号系统：用户、登录 Token =====

var users = map[string]string{"alice": "123456", "bob": "123456"} // 用户名 → 密码（演示用）

type sessions struct {
	mu     sync.Mutex
	tokens map[string]string // Token → 用户 ID
}

func (s *sessions) login(user string) string {
	b := make([]byte, 16)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = user
	s.mu.Unlock()
	return tok
}

func (s *sessions) user(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.tokens[tok]
	return u, ok
}

// ===== 要实现的函数 1：Authenticate =====

func authenticate(s *sessions) func(r *http.Request) (string, error) {
	return func(r *http.Request) (string, error) {
		// 真实网站：校验 JWT 签名和过期时间，或者查 session 表
		if u, ok := s.user(searchchat.BearerToken(r)); ok {
			return u, nil
		}
		return "", errors.New("未登录或 Token 无效")
	}
}

// ===== 要实现的函数 2：Billing（Reserve / Settle / Refund）=====

type hold struct {
	amount int64
	state  string // reserved / settled / refunded
}

// memoryBilling 是内存版余额表。真实网站换成数据库事务，见 README 的 SQL 写法。
type memoryBilling struct {
	mu      sync.Mutex
	balance map[string]int64
	holds   map[string]*hold // 相当于数据库里的 chat_charges 表，主键 (user_id, request_id)
}

func (b *memoryBilling) Reserve(ctx context.Context, user, reqID string, amount int64) error {
	b.mu.Lock() // 检查和扣减在同一把锁里完成，相当于 UPDATE ... WHERE balance >= ?
	defer b.mu.Unlock()
	key := user + "/" + reqID
	if b.holds[key] != nil {
		return searchchat.ErrDuplicateRequest
	}
	if b.balance[user] < amount {
		return searchchat.ErrInsufficientBalance
	}
	b.balance[user] -= amount
	b.holds[key] = &hold{amount: amount, state: "reserved"}
	return nil
}

func (b *memoryBilling) Settle(ctx context.Context, c searchchat.Charge) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.holds[c.UserID+"/"+c.RequestID]
	if h == nil || h.state != "reserved" { // 已经结算或退款过：什么也不做（幂等）
		return nil
	}
	h.state = "settled"
	b.balance[c.UserID] += h.amount - c.Cost // 多冻结的退回
	log.Printf("[billing] %s 结算 request=%s 输入 %d 输出 %d token 搜索 %d 次 扣 %d 积分，余额 %d",
		c.UserID, c.RequestID, c.PromptTokens, c.CompletionTokens, c.Searches, c.Cost, b.balance[c.UserID])
	return nil
}

func (b *memoryBilling) Refund(ctx context.Context, user, reqID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.holds[user+"/"+reqID]
	if h == nil || h.state != "reserved" {
		return nil
	}
	h.state = "refunded"
	b.balance[user] += h.amount
	return nil
}

func (b *memoryBilling) get(user string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.balance[user]
}

func (b *memoryBilling) add(user string, n int64) {
	b.mu.Lock()
	b.balance[user] += n
	b.mu.Unlock()
}

// ===== 把聊天服务挂进网站 =====

func main() {
	s := &sessions{tokens: map[string]string{}}
	billing := &memoryBilling{balance: map[string]int64{"alice": 10, "bob": 1}, holds: map[string]*hold{}}

	cfg := searchchat.ConfigFromEnv() // 模型、搜索、计费规则（BILLING_MODE 等）都可以用环境变量配置
	cfg.Authenticate = authenticate(s)
	cfg.Billing = billing
	if cfg.Pricing.Mode == "" && cfg.Pricing.PerRequest == 0 {
		cfg.Pricing = searchchat.Pricing{Mode: searchchat.BillingPerRequest, PerRequest: 2, PerSearch: 1}
	}
	if problems := cfg.Problems(); len(problems) > 0 {
		for _, p := range problems {
			log.Println("❌", p)
		}
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/ai/", http.StripPrefix("/ai", searchchat.Handler(cfg)))

	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ User, Password string }
		json.NewDecoder(r.Body).Decode(&in)
		if pw, ok := users[in.User]; !ok || pw != in.Password {
			http.Error(w, "用户名或密码不对", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"token": s.login(in.User)})
	})
	// 查询余额 / 充值：用同一个 authenticate 校验 Token
	me := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		u, err := cfg.Authenticate(r)
		if err != nil {
			http.Error(w, "请先登录", http.StatusUnauthorized)
			return "", false
		}
		return u, true
	}
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := me(w, r); ok {
			json.NewEncoder(w).Encode(map[string]any{"user": u, "balance": billing.get(u)})
		}
	})
	mux.HandleFunc("POST /recharge", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := me(w, r); ok {
			billing.add(u, 10)
			json.NewEncoder(w).Encode(map[string]any{"user": u, "balance": billing.get(u)})
		}
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}
	log.Printf("打开 http://localhost:%s（用户 alice / bob，密码 123456；alice 有 10 积分，bob 有 1 积分）", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}

const page = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>按用户扣费示例</title>
<style>body{font:15px/1.6 -apple-system,"PingFang SC",sans-serif;max-width:640px;margin:40px auto;padding:0 16px}
button{margin:2px 4px 2px 0;padding:4px 12px}code{background:#f3f4f6;padding:1px 4px;border-radius:4px}</style></head>
<body>
<h2>我的网站（演示账号系统）</h2>
<p id="who">未登录</p>
<p>
  <button onclick="login('alice')">以 alice 登录（10 积分）</button>
  <button onclick="login('bob')">以 bob 登录（1 积分）</button>
  <button onclick="logout()">退出登录</button>
  <button id="recharge" onclick="recharge()">充值 10 积分</button>
</p>
<p>当前计费：按次，每次提问 2 积分，每次联网搜索加 1 积分（提问前预扣 4 积分，回答后多退少补）。</p>
<ol>
  <li>不登录直接提问 → 提示「请先登录」</li>
  <li>以 alice 登录后问「1+1」→ 回答下方显示「本次消耗 2 积分」，余额减少</li>
  <li>问「今天的科技新闻」→ 会联网搜索，消耗更多</li>
  <li>以 bob 登录后提问 → 提示「余额不足」和充值链接</li>
</ol>
<script>
// 网站自己的登录逻辑：登录后把 Token 存进 localStorage（键名和 data-auth-storage 一致）
async function login(user) {
  const r = await fetch("/login", {method: "POST", body: JSON.stringify({user, password: "123456"})});
  const {token} = await r.json();
  localStorage.setItem("demo_token", token);
  refresh();
}
function logout() { localStorage.removeItem("demo_token"); refresh(); }
async function recharge() {
  await fetch("/recharge", {method: "POST", headers: auth()});
  refresh();
}
function auth() { const t = localStorage.getItem("demo_token"); return t ? {Authorization: "Bearer " + t} : {}; }
async function refresh() {
  const r = await fetch("/me", {headers: auth()});
  document.getElementById("who").textContent = r.ok
    ? (d => "已登录：" + d.user + "，余额 " + d.balance + " 积分")(await r.json())
    : "未登录";
}
refresh();
setInterval(refresh, 3000);
</script>
<!-- 聊天组件：从 localStorage 的 demo_token 读取登录 Token -->
<script src="/ai/chat-widget.js" data-auth-storage="demo_token" data-login-url="/#login" data-recharge-url="/#recharge"></script>
</body></html>
`
