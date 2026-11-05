package searchchat

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是聊天服务的全部配置。一般用 ConfigFromEnv() 从环境变量读取，
// 也可以在代码里直接填写（Key 请仍然从环境变量或密钥管理里取，不要写死在代码里）。
type Config struct {
	LLMBaseURL string        // LLM_BASE_URL：OpenAI 兼容接口地址，如 https://api.openai.com/v1、http://localhost:11434/v1
	LLMAPIKey  string        // LLM_API_KEY：模型 Key（Ollama 可以不填）
	LLMModel   string        // LLM_MODEL：模型名，如 gpt-4o-mini、deepseek-chat、qwen3:8b
	LLMTimeout time.Duration // LLM_TIMEOUT：模型单次调用最多等多久，默认 120s
	// LLM_REASONING_EFFORT：可选，原样作为 reasoning_effort 发给模型。
	// 推理模型（qwen3、o 系列等）填 none / low 可以明显变快；普通模型不要填。
	LLMReasoningEffort string

	SearchEngine   string        // SEARCH_ENGINE：auto（默认）/ duckduckgo / tavily / bocha
	TavilyAPIKey   string        // TAVILY_API_KEY
	BochaAPIKey    string        // BOCHA_API_KEY
	SearchResults  int           // SEARCH_RESULTS：每次搜索取几条结果，默认 5
	SearchCacheTTL time.Duration // SEARCH_CACHE_TTL：相同查询的缓存时间，默认 5m，0 表示不缓存

	AllowedOrigins     []string // ALLOWED_ORIGINS：允许接入的网站，逗号分隔，如 https://a.com,https://www.a.com；* 表示全部允许
	AccessToken        string   // ACCESS_TOKEN：访问口令（可选），前端用 data-token 传入
	RateLimitPerMinute int      // RATE_LIMIT_PER_MIN：每个 IP 每分钟最多提问几次，默认 10，0 表示不限
	TrustProxy         bool     // TRUST_PROXY=1：服务在 Nginx 等反向代理后面时，按 X-Forwarded-For 识别真实 IP
	MaxConcurrent      int      // MAX_CONCURRENT：同时最多处理几个提问，默认 4（本地 Ollama 建议 1）

	// 以下用于接入网站自己的账号系统，只能在代码里设置（方式 B），不设置时和以前完全一样。

	// Authenticate 可选：校验网站自己的登录 Token，返回用户 ID。
	// 设置后，返回错误或空 ID 的请求一律 401「请先登录」；限流改为按用户 ID 计算。
	// Token 一般用 BearerToken(r) 取（前端以 Authorization: Bearer <token> 发送）。
	Authenticate func(r *http.Request) (userID string, err error)
	// Billing 可选：从用户余额里扣费，必须同时设置 Authenticate。
	Billing Billing
	// Pricing 计费规则，默认按次每次 1 积分。见 Pricing 的说明。
	Pricing Pricing
	// DAILY_LIMIT_PER_USER：设置了 Authenticate 时，每个用户每天最多问几次，0 表示不限
	DailyLimitPerUser int
}

// ConfigFromEnv 从环境变量读取配置，没填的项用默认值。
func ConfigFromEnv() Config {
	return Config{
		LLMBaseURL:         env("LLM_BASE_URL", ""),
		LLMAPIKey:          env("LLM_API_KEY", ""),
		LLMModel:           env("LLM_MODEL", ""),
		LLMTimeout:         envDuration("LLM_TIMEOUT", 120*time.Second),
		LLMReasoningEffort: env("LLM_REASONING_EFFORT", ""),
		SearchEngine:       strings.ToLower(env("SEARCH_ENGINE", "auto")),
		TavilyAPIKey:       env("TAVILY_API_KEY", ""),
		BochaAPIKey:        env("BOCHA_API_KEY", ""),
		SearchResults:      envInt("SEARCH_RESULTS", 5),
		SearchCacheTTL:     envDuration("SEARCH_CACHE_TTL", 5*time.Minute),
		AllowedOrigins:     splitList(env("ALLOWED_ORIGINS", "")),
		AccessToken:        env("ACCESS_TOKEN", ""),
		RateLimitPerMinute: envInt("RATE_LIMIT_PER_MIN", 10),
		TrustProxy:         envBool("TRUST_PROXY"),
		MaxConcurrent:      envInt("MAX_CONCURRENT", 4),
		Pricing: Pricing{
			Mode:        strings.ToLower(env("BILLING_MODE", "")),
			PerRequest:  int64(envInt("PRICE_PER_REQUEST", 0)),
			PerSearch:   int64(envInt("PRICE_PER_SEARCH", 0)),
			InputPer1K:  envFloat("PRICE_INPUT_PER_1K", 0),
			OutputPer1K: envFloat("PRICE_OUTPUT_PER_1K", 0),
			MaxCharge:   int64(envInt("MAX_CHARGE", 0)),
		},
		DailyLimitPerUser: envInt("DAILY_LIMIT_PER_USER", 0),
	}
}

// Problems 返回配置里的问题（中文），为空表示可以启动。只检查有没有填，不会打印任何 Key。
func (c Config) Problems() []string {
	var p []string
	if c.LLMBaseURL == "" {
		p = append(p, "缺少 LLM_BASE_URL（模型接口地址，例如 https://api.openai.com/v1）")
	}
	if c.LLMModel == "" {
		p = append(p, "缺少 LLM_MODEL（模型名，例如 gpt-4o-mini）")
	}
	switch c.SearchEngine {
	case "auto", "duckduckgo":
	case "tavily":
		if c.TavilyAPIKey == "" {
			p = append(p, "SEARCH_ENGINE=tavily 但没有设置 TAVILY_API_KEY")
		}
	case "bocha":
		if c.BochaAPIKey == "" {
			p = append(p, "SEARCH_ENGINE=bocha 但没有设置 BOCHA_API_KEY")
		}
	default:
		p = append(p, "SEARCH_ENGINE 只能是 auto / duckduckgo / tavily / bocha")
	}
	if c.Billing != nil {
		if c.Authenticate == nil {
			p = append(p, "设置了 Billing 但没有设置 Authenticate（不知道该扣谁的钱）")
		}
		p = append(p, c.Pricing.withDefaults().problems()...)
	}
	return p
}

// withDefaults 补齐在代码里直接构造 Config 时没填的项。
func (c Config) withDefaults() Config {
	c.LLMBaseURL = strings.TrimRight(c.LLMBaseURL, "/")
	if c.LLMTimeout <= 0 {
		c.LLMTimeout = 120 * time.Second
	}
	if c.SearchEngine == "" {
		c.SearchEngine = "auto"
	}
	if c.SearchResults <= 0 {
		c.SearchResults = 5
	}
	c.SearchResults = min(10, c.SearchResults)
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 4
	}
	c.Pricing = c.Pricing.withDefaults()
	for i, o := range c.AllowedOrigins {
		c.AllowedOrigins[i] = strings.TrimRight(strings.TrimSpace(o), "/")
	}
	return c
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(env(name, "")); err == nil {
		return n
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if f, err := strconv.ParseFloat(env(name, ""), 64); err == nil {
		return f
	}
	return def
}

func envBool(name string) bool {
	b, _ := strconv.ParseBool(env(name, "false"))
	return b
}

// envDuration 接受 90s、5m 这样的写法，也接受纯数字（按秒算）。
func envDuration(name string, def time.Duration) time.Duration {
	v := env(name, "")
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
