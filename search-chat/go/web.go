package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxHistory      = 20   // 网页传来的对话历史最多保留几条（太长会变慢）
	maxMessageChars = 4000 // 每条消息最多多少字
)

//go:embed chat.html
var chatPage []byte

type chatRequest struct {
	Messages []Message `json:"messages"` // 只含 user / assistant，最后一条是这次的问题
}

type chatSource struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type chatResponse struct {
	Answer  string       `json:"answer,omitempty"`
	Sources []chatSource `json:"sources"`
	Seconds float64      `json:"seconds"`
	Error   string       `json:"error,omitempty"`
}

// serveWeb 启动网页模式：
//
//	GET  /          聊天页面（可以直接打开，也可以用 <iframe> 嵌进自己的网站）
//	POST /api/chat  聊天接口，请求 {"messages":[{"role":"user","content":"..."}]}
//	                返回 {"answer":"...","sources":[{"n":1,"title":"...","url":"..."}],"seconds":3.2}
func serveWeb(addr, model string, maxResults int, think bool, allowOrigin string) error {
	// 只写端口（如 :8080）时只监听本机，别人连不上；要给局域网用请写 0.0.0.0:8080
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	var mu sync.Mutex // 本地模型一次只答一个问题，其余请求排队

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(chatPage)
	})
	mux.HandleFunc("OPTIONS /api/chat", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, allowOrigin)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, allowOrigin)
		var req chatRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, chatResponse{Error: "请求格式不对，需要 JSON：{\"messages\":[...]}"})
			return
		}
		messages, err := cleanHistory(req.Messages)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, chatResponse{Error: err.Error()})
			return
		}

		mu.Lock()
		defer mu.Unlock()
		fmt.Println("你：" + messages[len(messages)-1].Content)
		start := time.Now()
		answer, sources, err := ask(model, messages, maxResults, think)
		if err != nil {
			fmt.Println(err)
			writeJSON(w, http.StatusBadGateway, chatResponse{Error: strings.TrimPrefix(err.Error(), "❌ ")})
			return
		}
		answer = cleanCitations(answer, sources)
		if answer == "" {
			answer = "（模型没有返回文字）"
		}
		resp := chatResponse{Answer: answer, Sources: []chatSource{}, Seconds: time.Since(start).Seconds()}
		if len(sources) > 0 {
			for _, n := range citedSources(answer, sources) {
				resp.Sources = append(resp.Sources, chatSource{N: n, Title: sources[n-1].Title, URL: sources[n-1].URL})
			}
		}
		fmt.Printf("   （用时 %.1f 秒）\n", resp.Seconds)
		writeJSON(w, http.StatusOK, resp)
	})

	fmt.Printf("🌐 网页模式已启动（模型：%s，搜索：%s）\n", model, searchEngineName())
	fmt.Printf("   浏览器打开：http://%s\n", strings.Replace(addr, "0.0.0.0", "localhost", 1))
	fmt.Println("   按 Ctrl+C 停止。")
	return http.ListenAndServe(addr, mux)
}

// cleanHistory 检查网页传来的对话历史，并在最前面加上系统提示。
// 只接受 user / assistant 两种角色，防止网页伪造 system 或 tool 消息。
func cleanHistory(in []Message) ([]Message, error) {
	if len(in) == 0 || in[len(in)-1].Role != "user" || strings.TrimSpace(in[len(in)-1].Content) == "" {
		return nil, fmt.Errorf("最后一条消息必须是用户的问题")
	}
	if len(in) > maxHistory {
		in = in[len(in)-maxHistory:]
	}
	out := []Message{{Role: "system", Content: systemPrompt}}
	for _, m := range in {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, fmt.Errorf("不支持的角色：%q（只能是 user 或 assistant）", m.Role)
		}
		out = append(out, Message{Role: m.Role, Content: truncateRunes(strings.TrimSpace(m.Content), maxMessageChars)})
	}
	return out, nil
}

// setCORS 允许指定的网站（--allow-origin）从浏览器里直接调用接口。
// 用 <iframe> 嵌入时不需要设置。
func setCORS(w http.ResponseWriter, allowOrigin string) {
	if allowOrigin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
