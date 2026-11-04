package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// searxngResponse 对应 GET /search?format=json 的返回字段。
type searxngResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title         string   `json:"title"`
		URL           string   `json:"url"`
		Content       string   `json:"content"`
		Engine        string   `json:"engine"`
		Engines       []string `json:"engines"`
		Score         float64  `json:"score"`
		Category      string   `json:"category"`
		PublishedDate *string  `json:"publishedDate"`
	} `json:"results"`
	Suggestions         []string   `json:"suggestions"`
	UnresponsiveEngines [][]string `json:"unresponsive_engines"` // [引擎名, 原因]
	Error               string     `json:"error"`                // 400 时返回
}

// SearXNGOptions 是 SearXNG 搜索的参数。
type SearXNGOptions struct {
	BaseURL    string // 默认 http://localhost:8888
	Engines    string // 默认 google cse,bing,duckduckgo,wikipedia
	Language   string // 默认 zh-CN
	MaxResults int    // 默认 5
}

// SearXNGSearch 只用标准库调用 SearXNG；出错时返回空切片和 error。
func SearXNGSearch(ctx context.Context, query string, opt SearXNGOptions) ([]Result, error) {
	if opt.BaseURL == "" {
		opt.BaseURL = "http://localhost:8888"
	}
	if opt.Engines == "" {
		opt.Engines = "google cse,bing,duckduckgo,wikipedia"
	}
	if opt.Language == "" {
		opt.Language = "zh-CN"
	}
	if opt.MaxResults <= 0 {
		opt.MaxResults = 5
	}

	params := url.Values{
		"q":        {query},
		"format":   {"json"},
		"language": {opt.Language},
		"engines":  {opt.Engines},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opt.BaseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return []Result{}, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return []Result{}, fmt.Errorf("searxng: 请求失败: %w", err) // 连接失败、超时
	}
	defer resp.Body.Close()

	var data searxngResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&data)
	if resp.StatusCode != http.StatusOK {
		return []Result{}, fmt.Errorf("searxng: HTTP %d %s", resp.StatusCode, data.Error)
	}
	if decodeErr != nil {
		return []Result{}, fmt.Errorf("searxng: 返回的不是 JSON: %w", decodeErr)
	}

	results := []Result{}
	for _, r := range data.Results {
		if len(results) >= opt.MaxResults {
			break
		}
		results = append(results, Result{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}
