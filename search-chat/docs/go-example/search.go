package main

import (
	"context"
	"fmt"
	"log"
)

// Search 先调 SearXNG；出错或结果为空时改用 DuckDuckGo。
// lang 为 "zh" 时搜中文网页，其他值（如 "en"）搜英文网页。
// 第二个返回值是实际提供结果的引擎："searxng" 或 "duckduckgo"。
func Search(ctx context.Context, query, lang string, maxResults int, searxngURL string) ([]Result, string, error) {
	sxLang, ddgRegion := "en", "us-en"
	if lang == "zh" {
		sxLang, ddgRegion = "zh-CN", "cn-zh"
	}

	results, sxErr := SearXNGSearch(ctx, query, SearXNGOptions{BaseURL: searxngURL, Language: sxLang, MaxResults: maxResults})
	if sxErr == nil && len(results) > 0 {
		return results, "searxng", nil
	}
	if sxErr != nil {
		log.Printf("SearXNG 搜索失败，改用 DuckDuckGo：%v", sxErr)
	} else {
		log.Printf("SearXNG 无结果，改用 DuckDuckGo")
	}

	results, err := DDGSearch(ctx, query, DDGOptions{Region: ddgRegion, MaxResults: maxResults})
	if err != nil {
		return []Result{}, "duckduckgo", fmt.Errorf("两个搜索引擎都失败（SearXNG: %v）: %w", sxErr, err)
	}
	return results, "duckduckgo", nil
}
