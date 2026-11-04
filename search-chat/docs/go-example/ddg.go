package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Result 是 DuckDuckGo 和 SearXNG 统一使用的结果结构体。
type Result struct {
	Title   string
	URL     string
	Snippet string
}

var (
	ErrDDGRateLimited = errors.New("duckduckgo: 被限流")
	ErrDDGCaptcha     = errors.New("duckduckgo: 要求验证码")
	ErrNoResults      = errors.New("无结果")
)

const (
	ddgHTMLURL = "https://html.duckduckgo.com/html/"
	// User-Agent 要像浏览器，并且同一进程内保持不变（翻页用的 vqd 和它绑定）。
	ddgUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
)

// DDGOptions 是 DuckDuckGo 网页搜索的参数。
type DDGOptions struct {
	Region     string        // 地区/语言，如 cn-zh、us-en、wt-wt（不限），默认 wt-wt
	TimeLimit  string        // 时间范围：d 天 / w 周 / m 月 / y 年，默认不限
	MaxResults int           // 返回条数上限，默认 10
	Page       int           // 页码，从 1 开始，默认 1
	Timeout    time.Duration // 单次请求超时，默认 10 秒
}

// DDGSearch 请求 html.duckduckgo.com 并解析结果页。
func DDGSearch(ctx context.Context, query string, opt DDGOptions) ([]Result, error) {
	if opt.Region == "" {
		opt.Region = "wt-wt"
	}
	if opt.MaxResults <= 0 {
		opt.MaxResults = 10
	}
	if opt.Page <= 0 {
		opt.Page = 1
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: opt.Timeout}

	form := url.Values{"q": {query}, "b": {""}, "kl": {opt.Region}}
	if opt.TimeLimit != "" {
		form.Set("df", opt.TimeLimit)
	}

	var results []Result
	for page := 1; ; page++ {
		doc, err := ddgPost(ctx, client, form, opt)
		if err != nil {
			return []Result{}, err
		}
		if page == opt.Page {
			doc.Find("#links div.web-result").Each(func(_ int, s *goquery.Selection) {
				if s.HasClass("result--ad") || len(results) >= opt.MaxResults {
					return
				}
				a := s.Find("h2 a.result__a").First()
				href, _ := a.Attr("href")
				results = append(results, Result{
					Title:   strings.TrimSpace(a.Text()),
					URL:     href,
					Snippet: strings.TrimSpace(s.Find("a.result__snippet").First().Text()),
				})
			})
			break
		}
		// 第 2 页起要提交上一页「Next」表单里的隐藏字段（含 vqd），不能直接构造。
		next := doc.Find("div.nav-link form").Last()
		if next.Find(`input[name="vqd"]`).Length() == 0 {
			return []Result{}, fmt.Errorf("duckduckgo: 第 %d 页之后没有下一页（部分地区如 cn-zh 不支持翻页）", page)
		}
		form = url.Values{}
		next.Find("input[type=hidden]").Each(func(_ int, in *goquery.Selection) {
			name, _ := in.Attr("name")
			value, _ := in.Attr("value")
			form.Set(name, value)
		})
		time.Sleep(time.Second) // 连续请求间隔，降低被限流的概率
	}
	if len(results) == 0 {
		return []Result{}, ErrNoResults
	}
	return results, nil
}

func ddgPost(ctx context.Context, client *http.Client, form url.Values, opt DDGOptions) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgHTMLURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	// 缺少 Sec-Fetch-* 和 Referer 时，实测每次都返回验证码页。
	req.Header.Set("User-Agent", ddgUserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", ddgHTMLURL)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.AddCookie(&http.Cookie{Name: "kl", Value: opt.Region})
	if opt.TimeLimit != "" {
		req.AddCookie(&http.Cookie{Name: "df", Value: opt.TimeLimit})
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: 请求失败: %w", err) // 含超时
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrDDGRateLimited, resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("duckduckgo: HTTP %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: 解析 HTML 失败: %w", err)
	}
	// 验证码页返回 HTTP 202，页面里有 id="challenge-form" 的表单。
	if doc.Find("#challenge-form").Length() > 0 {
		return nil, ErrDDGCaptcha
	}
	return doc, nil
}

// NewsResult 是 DuckDuckGo 新闻搜索的结果。
type NewsResult struct {
	Title   string
	URL     string
	Snippet string
	Source  string
	Date    time.Time
}

var vqdRe = regexp.MustCompile(`vqd=["']?([0-9-]+)`)

// DDGNews 调用 duckduckgo.com/news.js：先请求搜索页拿 vqd，再取新闻 JSON。
func DDGNews(ctx context.Context, query string, opt DDGOptions) ([]NewsResult, error) {
	if opt.Region == "" {
		opt.Region = "wt-wt"
	}
	if opt.MaxResults <= 0 {
		opt.MaxResults = 10
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: opt.Timeout}

	body, err := ddgGet(ctx, client, "https://duckduckgo.com/?"+url.Values{"q": {query}, "iar": {"news"}, "ia": {"news"}}.Encode())
	if err != nil {
		return []NewsResult{}, err
	}
	m := vqdRe.FindSubmatch(body)
	if m == nil {
		return []NewsResult{}, errors.New("duckduckgo: 没有拿到 vqd")
	}

	// news.js 带 df 参数时实测总是返回 0 条，所以时间范围改为在本地按 Date 过滤。
	params := url.Values{"l": {opt.Region}, "o": {"json"}, "noamp": {"1"}, "q": {query}, "vqd": {string(m[1])}}
	body, err = ddgGet(ctx, client, "https://duckduckgo.com/news.js?"+params.Encode())
	if err != nil {
		return []NewsResult{}, err
	}
	var data struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Excerpt string `json:"excerpt"`
			Source  string `json:"source"`
			Date    int64  `json:"date"` // Unix 秒
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return []NewsResult{}, fmt.Errorf("duckduckgo: 新闻接口返回的不是 JSON: %w", err)
	}
	var since time.Time
	if d, ok := map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "m": 30 * 24 * time.Hour, "y": 365 * 24 * time.Hour}[opt.TimeLimit]; ok {
		since = time.Now().Add(-d)
	}
	news := []NewsResult{}
	for _, r := range data.Results {
		if len(news) >= opt.MaxResults {
			break
		}
		if time.Unix(r.Date, 0).Before(since) {
			continue
		}
		news = append(news, NewsResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: stripTags(r.Excerpt),
			Source:  r.Source,
			Date:    time.Unix(r.Date, 0),
		})
	}
	if len(news) == 0 {
		return news, ErrNoResults
	}
	return news, nil
}

func ddgGet(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ddgUserAgent)
	req.Header.Set("Referer", "https://duckduckgo.com/")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w (HTTP %d)", ErrDDGRateLimited, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("duckduckgo: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return strings.TrimSpace(tagRe.ReplaceAllString(s, "")) }
