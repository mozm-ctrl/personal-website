package main

import (
	"context"
	"fmt"
	"log"
)

// Search 先调 SearXNG；出错或结果为空时改用 DuckDuckGo。
// 第二个返回值是实际提供结果的引擎："searxng" 或 "duckduckgo"。
func Search(ctx context.Context, query string, maxResults int, searxngURL string) ([]Result, string, error) {
	results, err := SearXNGSearch(ctx, query, SearXNGOptions{BaseURL: searxngURL, MaxResults: maxResults})
	if err == nil && len(results) > 0 {
		return results, "searxng", nil
	}
	if err != nil {
		log.Printf("SearXNG 搜索失败，改用 DuckDuckGo：%v", err)
	} else {
		log.Printf("SearXNG 无结果，改用 DuckDuckGo")
	}

	results, err = DDGSearch(ctx, query, DDGOptions{Region: "cn-zh", MaxResults: maxResults})
	if err != nil {
		return []Result{}, "duckduckgo", fmt.Errorf("两个搜索引擎都失败: %w", err)
	}
	return results, "duckduckgo", nil
}
