package searchchat

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEnginesOrder(t *testing.T) {
	cases := []struct {
		cfg   Config
		query string
		want  []string
	}{
		{Config{SearchEngine: "auto"}, "news", []string{"duckduckgo"}},
		{Config{SearchEngine: "auto", TavilyAPIKey: "t"}, "news", []string{"duckduckgo", "tavily"}},
		{Config{SearchEngine: "auto", TavilyAPIKey: "t", BochaAPIKey: "b"}, "news", []string{"duckduckgo", "tavily", "bocha"}},
		{Config{SearchEngine: "auto", TavilyAPIKey: "t", BochaAPIKey: "b"}, "科技新闻", []string{"duckduckgo", "bocha", "tavily"}},
		{Config{SearchEngine: "bocha", BochaAPIKey: "b"}, "news", []string{"bocha"}},
	}
	for _, c := range cases {
		if got := newSearcher(c.cfg).engines(c.query); !reflect.DeepEqual(got, c.want) {
			t.Errorf("engines(%+v, %q) = %v, want %v", c.cfg, c.query, got, c.want)
		}
	}
}

func TestSearchCache(t *testing.T) {
	s := newSearcher(Config{SearchCacheTTL: time.Minute})
	s.store("ai news", []result{{Title: "t", URL: "https://a.com"}}, "tavily")
	res, engine, err := s.search(t.Context(), "  AI   News ")
	if err != nil || len(res) != 1 || engine != "tavily（缓存）" {
		t.Fatalf("cache miss: %v %q %v", res, engine, err)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2)
	if !l.allow("a") || !l.allow("a") || l.allow("a") || !l.allow("b") {
		t.Fatal("limiter should allow 2 per IP per minute")
	}
}

func TestCORSAndToken(t *testing.T) {
	h := Handler(Config{LLMBaseURL: "http://127.0.0.1:1/v1", LLMModel: "m", AllowedOrigins: []string{"https://ok.com/"}, AccessToken: "secret"})
	body := `{"messages":[{"role":"system","content":"x"}]}`
	do := func(origin, token string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Chat-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("https://evil.com", "secret"); c != http.StatusForbidden {
		t.Errorf("evil origin: got %d", c)
	}
	if c := do("https://ok.com", "wrong"); c != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d", c)
	}
	// 口令和网站都对，但伪造 system 消息 → 400
	if c := do("https://ok.com", "secret"); c != http.StatusBadRequest {
		t.Errorf("forged role: got %d", c)
	}
}

func TestCitations(t *testing.T) {
	src := []source{{N: 1, URL: "a"}, {N: 2, URL: "b"}, {N: 3, URL: "c"}}
	answer := cleanCitations("A [3]，B [9]。", src)
	if answer != "A [3]，B。" {
		t.Errorf("cleanCitations = %q", answer)
	}
	if got := citedSources(answer, src); len(got) != 1 || got[0].N != 3 {
		t.Errorf("citedSources = %v", got)
	}
}

func TestRedact(t *testing.T) {
	if got := redactKeys("bad key sk-abc123 here", "sk-abc123"); strings.Contains(got, "sk-abc123") {
		t.Errorf("key leaked: %q", got)
	}
}
