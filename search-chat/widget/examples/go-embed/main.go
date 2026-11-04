// 示例：把聊天服务挂进已有的 Go 网站（接入方式 B）。
//
//	cd widget && go run ./examples/go-embed
//	浏览器打开 http://localhost:3000
package main

import (
	"log"
	"net/http"

	"search-chat-widget/searchchat"
)

func main() {
	mux := http.NewServeMux()

	// 你原来的页面
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><h1>我的网站</h1>
<script src="/ai/chat-widget.js"></script>`))
	})

	// 加这一行：聊天服务挂在 /ai/ 下（前端脚本 /ai/chat-widget.js，接口 /ai/api/chat）
	mux.Handle("/ai/", http.StripPrefix("/ai", searchchat.Handler(searchchat.ConfigFromEnv())))

	log.Println("打开 http://localhost:3000")
	log.Fatal(http.ListenAndServe(":3000", mux))
}
