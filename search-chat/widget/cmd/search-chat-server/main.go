// search-chat-server：独立运行的「AI 聊天 + 联网搜索」服务。
// 所有配置都来自环境变量，见 ../../README.md。
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"search-chat-widget/searchchat"
)

func main() {
	cfg := searchchat.ConfigFromEnv()
	if problems := cfg.Problems(); len(problems) > 0 {
		for _, p := range problems {
			log.Println("❌", p)
		}
		os.Exit(1)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := os.Getenv("LISTEN_ADDR") // 例如 127.0.0.1（只允许本机访问）；默认所有网卡
	srv := &http.Server{
		Addr:              addr + ":" + port,
		Handler:           searchchat.Handler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("聊天服务监听 %s（前端脚本：/chat-widget.js，接口：/api/chat）", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
