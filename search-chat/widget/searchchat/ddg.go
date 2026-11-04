package searchchat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	errDDGRateLimited = errors.New("DuckDuckGo 被限流")
	errDDGCaptcha     = errors.New("DuckDuckGo 要求验证码（等同限流）")
)

const (
	ddgHTMLURL = "https://html.duckduckgo.com/html/"
	// User-Agent 要像浏览器，否则很快会被要求验证码。
	browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
)

// searchDuckDuckGo 请求 html.duckduckgo.com 并解析结果页（免费，不需要 Key）。
func searchDuckDuckGo(ctx context.Context, client *http.Client, query string, maxResults int) ([]result, error) {
	region := "wt-wt"
	if hasHan(query) {
		region = "cn-zh"
	}
	form := url.Values{"q": {query}, "b": {""}, "kl": {region}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgHTMLURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	// 缺少 Sec-Fetch-* 和 Referer 时，实测每次都返回验证码页。
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", ddgHTMLURL)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.AddCookie(&http.Cookie{Name: "kl", Value: region})

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DuckDuckGo 请求失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", errDDGRateLimited, resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("DuckDuckGo HTTP %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("DuckDuckGo 结果页解析失败: %w", err)
	}
	// 验证码页返回 HTTP 202，页面里有 id="challenge-form" 的表单。
	if doc.Find("#challenge-form").Length() > 0 {
		return nil, errDDGCaptcha
	}
	var results []result
	doc.Find("#links div.web-result").Each(func(_ int, s *goquery.Selection) {
		if s.HasClass("result--ad") || len(results) >= maxResults {
			return
		}
		a := s.Find("h2 a.result__a").First()
		href, _ := a.Attr("href")
		if href == "" {
			return
		}
		results = append(results, result{
			Title:   strings.TrimSpace(a.Text()),
			URL:     href,
			Snippet: strings.TrimSpace(s.Find("a.result__snippet").First().Text()),
		})
	})
	return results, nil
}
