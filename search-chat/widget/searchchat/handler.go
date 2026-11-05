package searchchat

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxHistory      = 20   // 前端传来的对话历史最多保留几条（太长会变慢）
	maxMessageChars = 4000 // 每条消息最多多少字
)

//go:embed chat-widget.js
var widgetJS []byte

type chatRequest struct {
	Messages []message `json:"messages"` // 只含 user / assistant，最后一条是这次的问题
}

type chatResponse struct {
	Answer   string      `json:"answer,omitempty"`
	Sources  []source    `json:"sources"`
	Searches []searchLog `json:"searches"`
	Seconds  float64     `json:"seconds"`
	// 以下只在设置了 Billing 时返回
	RequestID string `json:"request_id,omitempty"`
	Cost      *int64 `json:"cost,omitempty"` // 本次消耗的积分
}

// Handler 返回聊天服务的 http.Handler，提供：
//
//	GET  /chat-widget.js  前端组件（页面里用 <script> 引入）
//	POST /api/chat        聊天接口
//	GET  /healthz         健康检查
//
// 挂在子路径下时用 http.StripPrefix，例如：
//
//	mux.Handle("/ai/", http.StripPrefix("/ai", searchchat.Handler(searchchat.ConfigFromEnv())))
func Handler(cfg Config) http.Handler {
	cfg = cfg.withDefaults()
	mux := http.NewServeMux()
	if problems := cfg.Problems(); len(problems) > 0 {
		for _, p := range problems {
			logf("配置错误：%s", p)
		}
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusInternalServerError, "聊天服务还没配置好，请联系网站管理员（详见服务端日志）。")
		})
		return mux
	}

	h := &handler{
		cfg:     cfg,
		engine:  &engine{cfg: cfg, llm: &llmClient{cfg: cfg, http: &http.Client{Timeout: cfg.LLMTimeout}}, searcher: newSearcher(cfg)},
		limiter: newLimiter(cfg.RateLimitPerMinute),
		daily:   newDailyLimiter(cfg.DailyLimitPerUser),
		slots:   make(chan struct{}, cfg.MaxConcurrent),
	}
	mux.HandleFunc("GET /chat-widget.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(widgetJS)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": cfg.LLMModel, "search": cfg.SearchEngine})
	})
	mux.HandleFunc("OPTIONS /api/chat", h.preflight)
	mux.HandleFunc("POST /api/chat", h.chat)
	logf("已启动：模型 %s（%s），搜索 %s，允许网站 %v，口令 %v，限流 %d 次/分钟/%s",
		cfg.LLMModel, hostOnly(cfg.LLMBaseURL), cfg.SearchEngine, cfg.AllowedOrigins, cfg.AccessToken != "", cfg.RateLimitPerMinute,
		map[bool]string{true: "用户", false: "IP"}[cfg.Authenticate != nil])
	if cfg.Billing != nil {
		logf("按用户扣费：%s，预扣 %d 积分/次，每个用户每天最多 %d 次（0 为不限）", cfg.Pricing.Mode, cfg.Pricing.reserve(), cfg.DailyLimitPerUser)
	}
	return mux
}

type handler struct {
	cfg     Config
	engine  *engine
	limiter *limiter
	daily   *dailyLimiter
	slots   chan struct{} // 同时处理的提问数上限
}

func (h *handler) preflight(w http.ResponseWriter, r *http.Request) {
	if !h.cors(w, r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Chat-Token, Authorization, X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) chat(w http.ResponseWriter, r *http.Request) {
	ip := h.clientIP(r)
	if !h.cors(w, r) {
		logf("拒绝 ip=%s：网站 %q 不在 ALLOWED_ORIGINS 里", ip, r.Header.Get("Origin"))
		writeError(w, http.StatusForbidden, "这个网站没有被允许接入聊天服务（ALLOWED_ORIGINS）。")
		return
	}
	if h.cfg.AccessToken != "" &&
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Chat-Token")), []byte(h.cfg.AccessToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "访问口令不对（data-token）。")
		return
	}
	who, userID := "ip="+ip, "" // who 用于限流和日志
	if h.cfg.Authenticate != nil {
		id, err := h.cfg.Authenticate(r)
		if err != nil || strings.TrimSpace(id) == "" {
			writeErrorCode(w, http.StatusUnauthorized, "login_required", "请先登录。")
			return
		}
		userID, who = id, "user="+id
	}
	if !h.limiter.allow(who) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "提问太频繁了，请一分钟后再试。")
		return
	}
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, `请求格式不对，需要 JSON：{"messages":[...]}`)
		return
	}
	messages, err := cleanHistory(req.Messages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if userID != "" && !h.daily.take(userID) {
		writeErrorCode(w, http.StatusTooManyRequests, "daily_limit", "今天的提问次数已用完，明天再来吧。")
		return
	}

	// 预扣：余额不足时直接返回 402，不调用模型
	var reqID string
	var reserved int64
	if h.cfg.Billing != nil {
		reqID, reserved = requestID(r), h.cfg.Pricing.reserve()
		if err := h.cfg.Billing.Reserve(r.Context(), userID, reqID, reserved); err != nil {
			h.daily.release(userID)
			switch {
			case errors.Is(err, ErrInsufficientBalance):
				writeErrorCode(w, http.StatusPaymentRequired, "insufficient_balance", "余额不足。")
			case errors.Is(err, ErrDuplicateRequest):
				writeErrorCode(w, http.StatusConflict, "duplicate_request", "这个问题已经提交过了，请不要重复提交。")
			default:
				logf("预扣失败 %s request=%s：%s", who, reqID, err)
				writeError(w, http.StatusServiceUnavailable, "扣费服务暂时不可用，请稍后再试。")
			}
			return
		}
	}
	// 后面任何一步失败都要全额退回。用户断开连接后 r.Context() 已取消，退款不能再用它。
	fail := func(reason string) {
		h.daily.release(userID)
		if h.cfg.Billing == nil {
			return
		}
		if err := h.cfg.Billing.Refund(context.WithoutCancel(r.Context()), userID, reqID); err != nil {
			logf("退款失败 %s request=%s（%s）：%s", who, reqID, reason, err)
			return
		}
		logf("已退回 %s request=%s %d 积分（%s）", who, reqID, reserved, reason)
	}

	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	case <-r.Context().Done():
		fail("排队时用户断开")
		return
	}
	start := time.Now()
	res, err := h.engine.ask(r.Context(), messages)
	if err == nil && r.Context().Err() != nil {
		err = r.Context().Err() // 回答好了但用户已经断开，看不到回答，不收费
	}
	if err != nil {
		status, public := http.StatusBadGateway, "出错了，请稍后再试。"
		var le *llmError
		if errors.As(err, &le) {
			status, public = le.Status, le.Public
			logf("模型调用失败 %s：%s", who, le.Detail)
		} else {
			logf("处理失败 %s：%s", who, redactKeys(err.Error(), h.cfg.LLMAPIKey, h.cfg.TavilyAPIKey, h.cfg.BochaAPIKey))
		}
		fail("回答失败")
		writeError(w, status, public)
		return
	}
	answer := cleanCitations(res.Answer, res.Sources)
	if answer == "" {
		answer = "（模型没有返回文字）"
	}
	resp := chatResponse{Answer: answer, Sources: []source{}, Searches: res.Searches, Seconds: time.Since(start).Seconds()}
	if len(res.Sources) > 0 {
		resp.Sources = citedSources(answer, res.Sources)
	}
	if resp.Searches == nil {
		resp.Searches = []searchLog{}
	}
	if h.cfg.Billing != nil {
		c := Charge{
			UserID: userID, RequestID: reqID,
			PromptTokens: res.Usage.PromptTokens, CompletionTokens: res.Usage.CompletionTokens,
			Searches: res.okSearches(), Reserved: reserved,
		}
		c.Cost = h.cfg.Pricing.cost(c.PromptTokens, c.CompletionTokens, c.Searches)
		if err := h.cfg.Billing.Settle(context.WithoutCancel(r.Context()), c); err != nil {
			// 回答已经生成，照常返回；冻结的积分由接入方按 request_id 对账处理
			logf("结算失败 %s request=%s cost=%d：%s", who, reqID, c.Cost, err)
		}
		resp.RequestID, resp.Cost = reqID, &c.Cost
		logf("扣费 %s request=%s 输入 %d 输出 %d token 搜索 %d 次 扣 %d 积分（预扣 %d）",
			who, reqID, c.PromptTokens, c.CompletionTokens, c.Searches, c.Cost, reserved)
	}
	// 日志里不记录提问内容和 Key，只记录耗时和搜索情况
	logf("回答 %s 用时 %.1fs 搜索 %d 次 来源 %d 条", who, resp.Seconds, len(res.Searches), len(resp.Sources))
	writeJSON(w, http.StatusOK, resp)
}

// cors 检查请求来自哪个网站，允许时写上 CORS 头。
// 没有 Origin（如 curl、服务端调用）或与服务同源时总是允许。
func (h *handler) cors(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimRight(r.Header.Get("Origin"), "/")
	if origin == "" {
		return true
	}
	w.Header().Add("Vary", "Origin")
	allowed := false
	for _, o := range h.cfg.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			allowed = true
			break
		}
	}
	if !allowed {
		if u, err := url.Parse(origin); err == nil && strings.EqualFold(u.Host, r.Host) {
			allowed = true // 同源
		}
	}
	if allowed {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	return allowed
}

func (h *handler) clientIP(r *http.Request) string {
	if h.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// cleanHistory 检查前端传来的对话历史，并在最前面加上系统提示。
// 只接受 user / assistant 两种角色，防止前端伪造 system 或 tool 消息。
func cleanHistory(in []message) ([]message, error) {
	if len(in) == 0 || in[len(in)-1].Role != "user" || strings.TrimSpace(in[len(in)-1].Content) == "" {
		return nil, errors.New("最后一条消息必须是用户的问题")
	}
	if len(in) > maxHistory {
		in = in[len(in)-maxHistory:]
	}
	out := []message{{Role: "system", Content: systemPrompt}}
	for _, m := range in {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, errors.New("消息角色只能是 user 或 assistant")
		}
		out = append(out, message{Role: m.Role, Content: truncateRunes(strings.TrimSpace(m.Content), maxMessageChars)})
	}
	return out, nil
}

// limiter 按 IP（设置了 Authenticate 时按用户）限流：每个 key 在最近 1 分钟内最多 perMinute 次。
type limiter struct {
	perMinute int
	mu        sync.Mutex
	hits      map[string][]time.Time
	lastClean time.Time
}

func newLimiter(perMinute int) *limiter {
	return &limiter{perMinute: perMinute, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(ip string) bool {
	if l.perMinute <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	if now.Sub(l.lastClean) > time.Minute { // 定期清掉不活跃的 IP
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(l.hits, k)
			}
		}
		l.lastClean = now
	}
	recent := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.perMinute {
		l.hits[ip] = recent
		return false
	}
	l.hits[ip] = append(recent, now)
	return true
}

// writeError 返回 {"error":"..."}，前端直接显示这句话。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeErrorCode 额外带上 code，方便前端区分 login_required / insufficient_balance 等情况。
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func logf(format string, a ...any) { log.Printf("[searchchat] "+format, a...) }

// hostOnly 只保留地址里的主机名，日志里不出现路径和参数。
func hostOnly(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return "?"
}
