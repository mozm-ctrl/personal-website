// 演示三种调用：DuckDuckGo、SearXNG、自动切换。
//
//	go run .                                   # 依次演示三种调用（中文 + 英文）
//	go run . -only ddg -q "golang"             # 只跑一种：ddg / news / searxng / search
//	go run . -only search -searxng http://localhost:9999   # SearXNG 不可用时的自动切换
package main

import (
	"context"
	"flag"
	"fmt"
	"time"
)

func main() {
	only := flag.String("only", "", "只运行一种：ddg / news / searxng / search，留空则演示全部")
	query := flag.String("q", "", "搜索词，留空则用中文和英文各一个示例")
	region := flag.String("region", "cn-zh", "DuckDuckGo 地区，如 cn-zh、us-en、wt-wt")
	timeLimit := flag.String("t", "", "DuckDuckGo 时间范围：d / w / m / y")
	page := flag.Int("page", 1, "DuckDuckGo 页码")
	n := flag.Int("n", 5, "返回条数")
	searxngURL := flag.String("searxng", "http://localhost:8888", "SearXNG 地址")
	flag.Parse()

	queries := []string{"大模型 联网搜索", "golang http client timeout"}
	if *query != "" {
		queries = []string{*query}
	}
	ctx := context.Background()

	for _, q := range queries {
		if *only == "" || *only == "ddg" {
			start := time.Now()
			results, err := DDGSearch(ctx, q, DDGOptions{Region: *region, TimeLimit: *timeLimit, MaxResults: *n, Page: *page})
			report("DuckDuckGo", q, start, results, err)
			time.Sleep(2 * time.Second)
		}
		if *only == "news" {
			start := time.Now()
			news, err := DDGNews(ctx, q, DDGOptions{Region: *region, TimeLimit: *timeLimit, MaxResults: *n})
			fmt.Printf("== DuckDuckGo 新闻 | %s | %.2fs | %d 条 | err=%v\n", q, time.Since(start).Seconds(), len(news), err)
			for i, r := range news {
				fmt.Printf("%d. [%s %s] %s\n   %s\n", i+1, r.Source, r.Date.Format("2006-01-02 15:04"), r.Title, r.URL)
			}
		}
		if *only == "" || *only == "searxng" {
			start := time.Now()
			results, err := SearXNGSearch(ctx, q, SearXNGOptions{BaseURL: *searxngURL, MaxResults: *n})
			report("SearXNG", q, start, results, err)
		}
		if *only == "" || *only == "search" {
			start := time.Now()
			results, source, err := Search(ctx, q, *n, *searxngURL)
			report("Search（自动切换，来源 "+source+"）", q, start, results, err)
		}
	}
}

func report(name, query string, start time.Time, results []Result, err error) {
	fmt.Printf("== %s | %s | %.2fs | %d 条 | err=%v\n", name, query, time.Since(start).Seconds(), len(results), err)
	for i, r := range results {
		fmt.Printf("%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, truncate(r.Snippet, 80))
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
