package searchchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

const (
	pagesToFetch = 2               // 每次搜索抓取前几个网页的正文
	pageChars    = 1500            // 每个网页正文最多保留多少字
	snippetChars = 300             // 搜索摘要最多保留多少字
	fetchTimeout = 8 * time.Second // 抓一个网页最多等多少秒
)

type result struct {
	Title   string
	URL     string
	Snippet string
	Body    string // 抓到的网页正文（可能为空）
}

// searcher 负责按策略选择搜索引擎、抓取正文，并缓存相同查询的结果。
type searcher struct {
	cfg  Config
	api  *http.Client // 调用搜索 API
	page *http.Client // 抓网页正文：不允许访问内网地址

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	results []result
	engine  string
	expires time.Time
}

func newSearcher(cfg Config) *searcher {
	return &searcher{
		cfg:   cfg,
		api:   &http.Client{Timeout: 20 * time.Second},
		page:  &http.Client{Timeout: fetchTimeout, Transport: publicOnlyTransport()},
		cache: map[string]cacheEntry{},
	}
}

// engines 返回本次要依次尝试的引擎。auto：先用免费的 DuckDuckGo，失败或没结果时再用配置了 Key 的收费引擎。
func (s *searcher) engines(query string) []string {
	switch s.cfg.SearchEngine {
	case "duckduckgo", "tavily", "bocha":
		return []string{s.cfg.SearchEngine}
	}
	list := []string{"duckduckgo"}
	paid := []string{}
	if s.cfg.TavilyAPIKey != "" {
		paid = append(paid, "tavily")
	}
	if s.cfg.BochaAPIKey != "" {
		paid = append(paid, "bocha")
	}
	// 中文查询优先用博查（国内引擎，中文结果更好）
	if hasHan(query) && len(paid) == 2 {
		paid[0], paid[1] = paid[1], paid[0]
	}
	return append(list, paid...)
}

// search 返回结果和实际使用的引擎名。所有引擎都失败时返回最后一个错误。
func (s *searcher) search(ctx context.Context, query string) ([]result, string, error) {
	key := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if s.cfg.SearchCacheTTL > 0 {
		s.mu.Lock()
		e, ok := s.cache[key]
		s.mu.Unlock()
		if ok && time.Now().Before(e.expires) {
			return e.results, e.engine + "（缓存）", nil
		}
	}

	var lastErr error
	for _, engine := range s.engines(query) {
		var results []result
		var err error
		switch engine {
		case "duckduckgo":
			results, err = searchDuckDuckGo(ctx, s.api, query, s.cfg.SearchResults)
		case "tavily":
			results, err = s.searchTavily(ctx, query)
		case "bocha":
			results, err = s.searchBocha(ctx, query)
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %s", engine, redactKeys(err.Error(), s.cfg.TavilyAPIKey, s.cfg.BochaAPIKey))
			continue
		}
		if len(results) == 0 {
			continue
		}
		s.fetchBodies(ctx, results)
		s.store(key, results, engine)
		return results, engine, nil
	}
	if lastErr != nil {
		return nil, "", lastErr
	}
	s.store(key, nil, "none")
	return nil, "none", nil
}

func (s *searcher) store(key string, results []result, engine string) {
	if s.cfg.SearchCacheTTL <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.cache { // 顺手清理过期项，缓存不会无限增长
		if now.After(e.expires) {
			delete(s.cache, k)
		}
	}
	s.cache[key] = cacheEntry{results: results, engine: engine, expires: now.Add(s.cfg.SearchCacheTTL)}
}

// fetchBodies 并行抓取前几个网页的正文。
func (s *searcher) fetchBodies(ctx context.Context, results []result) {
	var wg sync.WaitGroup
	for i := range results[:min(pagesToFetch, len(results))] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].Body = s.fetchPageText(ctx, results[i].URL)
		}()
	}
	wg.Wait()
}

func (s *searcher) searchTavily(ctx context.Context, query string) ([]result, error) {
	var data struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	err := s.postJSON(ctx, "https://api.tavily.com/search", s.cfg.TavilyAPIKey,
		map[string]any{"query": query, "max_results": s.cfg.SearchResults}, &data)
	if err != nil {
		return nil, err
	}
	var results []result
	for _, r := range data.Results {
		if r.URL != "" {
			results = append(results, result{Title: r.Title, URL: r.URL, Snippet: r.Content})
		}
	}
	return results, nil
}

// searchBocha 调用博查 Web Search API：https://open.bochaai.com
func (s *searcher) searchBocha(ctx context.Context, query string) ([]result, error) {
	var data struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			WebPages struct {
				Value []struct {
					Name    string `json:"name"`
					URL     string `json:"url"`
					Snippet string `json:"snippet"`
					Summary string `json:"summary"`
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	err := s.postJSON(ctx, "https://api.bochaai.com/v1/web-search", s.cfg.BochaAPIKey,
		map[string]any{"query": query, "count": s.cfg.SearchResults, "summary": true, "freshness": "noLimit"}, &data)
	if err != nil {
		return nil, err
	}
	if data.Code != 0 && data.Code != 200 {
		return nil, fmt.Errorf("code %d: %s", data.Code, truncate(data.Msg, 200))
	}
	var results []result
	for _, r := range data.Data.WebPages.Value {
		if r.URL == "" {
			continue
		}
		snippet := r.Summary
		if snippet == "" {
			snippet = r.Snippet
		}
		results = append(results, result{Title: r.Name, URL: r.URL, Snippet: snippet})
	}
	return results, nil
}

func (s *searcher) postJSON(ctx context.Context, url, key string, body any, out any) error {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.api.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return errors.New("Key 无效或没有权限")
	case resp.StatusCode == 429 || resp.StatusCode == 432:
		return errors.New("被限流或额度用完")
	case resp.StatusCode != 200:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

var spaceRe = regexp.MustCompile(`\s+`)

// fetchPageText 抓取网页正文（纯文本，已截断）。失败返回空字符串。
func (s *searcher) fetchPageText(ctx context.Context, url string) string {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := s.page.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode != 200 || !strings.Contains(contentType, "html") {
		return ""
	}
	// 按网页声明的编码转成 UTF-8（有些中文网站还在用 GBK）
	body, err := charset.NewReader(http.MaxBytesReader(nil, resp.Body, 2<<20), contentType)
	if err != nil {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return ""
	}
	doc.Find("script, style, noscript, nav, header, footer, form, aside").Remove()
	text := strings.TrimSpace(spaceRe.ReplaceAllString(doc.Text(), " "))
	return truncateRunes(text, pageChars)
}

// publicOnlyTransport 拒绝连接内网、本机地址，防止有人借搜索结果让服务去访问内部系统（SSRF）。
func publicOnlyTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
				ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
				return fmt.Errorf("不允许访问内网地址 %s", host)
			}
			return nil
		},
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = dialer.DialContext
	return t
}

func todayText() string {
	now := time.Now()
	weekday := []string{"日", "一", "二", "三", "四", "五", "六"}[now.Weekday()]
	return fmt.Sprintf("%s（星期%s）", now.Format("2006-01-02"), weekday)
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// truncateRunes 按“字”截断（不是按字节，避免把汉字切成乱码）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return truncateRunes(s, n) + "…"
}
