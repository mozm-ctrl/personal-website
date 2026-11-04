// search-chat-local：完全免费的「本地大模型 + 联网搜索」聊天程序（Go 版）。
//
// 功能和 ../search_chat_local.py 一致：
//   - 大模型：Ollama 在本机运行的开源模型（默认 qwen3:8b），不花钱、不需要 Key。
//   - 搜索：默认用 DuckDuckGo（ddg.go，不用注册、不用 Key）；
//     如果设置了环境变量 TAVILY_API_KEY，就改用 Tavily。
//
// 用法：
//
//	go build -o search-chat-local .     # 编译（只需要做一次）
//	./search-chat-local                 # 默认模型 qwen3:8b
//	./search-chat-local --model qwen3:4b
//	./search-chat-local --think         # 让 qwen3 先“思考”再回答（更慢，有时更准）
//	./search-chat-local --web :8080     # 网页模式：浏览器打开 http://localhost:8080
//
// 工作流程：
//  1. 把 web_search 作为“工具”告诉模型；
//  2. 模型自己决定要不要搜、搜什么；
//  3. 程序去搜索，并抓取前几个网页的正文（截断），带编号交回给模型；
//  4. 模型根据资料回答，并用 [1][2] 标注来源；
//  5. 程序在最后打印来源链接。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/peterh/liner"
)

const (
	defaultModel      = "qwen3:8b"
	defaultMaxResults = 5
)

func main() {
	envModel := os.Getenv("OLLAMA_MODEL")
	if envModel == "" {
		envModel = defaultModel
	}
	model := flag.String("model", envModel, "Ollama 模型名，默认读取环境变量 OLLAMA_MODEL，否则为 "+defaultModel)
	maxResults := flag.Int("max-results", defaultMaxResults, fmt.Sprintf("每次搜索最多取多少条结果（1-10，默认 %d）", defaultMaxResults))
	think := flag.Bool("think", false, "开启 qwen3 的“思考模式”（更慢，复杂问题可能更准）")
	web := flag.String("web", "", "改为网页模式，在这个地址提供聊天页面和接口，例如 :8080（不填则在终端里聊天）")
	allowOrigin := flag.String("allow-origin", "", "网页模式下允许哪个网站直接调用接口，例如 https://你的网站.com（用 iframe 嵌入时不需要）")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "完全免费的本地联网搜索聊天（Ollama + DuckDuckGo）")
		fmt.Fprintln(os.Stderr, "\n用法：./search-chat-local [--model 模型名] [--max-results 数量] [--think] [--web :8080]\n\n参数：")
		flag.PrintDefaults()
	}
	flag.Parse()
	n := max(1, min(10, *maxResults))

	if err := checkOllama(*model); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if *web != "" {
		if err := serveWeb(*web, *model, n, *think, *allowOrigin); err != nil {
			fmt.Println("❌ 网页模式启动失败：", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("🔎 本地联网搜索聊天已启动（模型：%s，搜索：%s）\n", *model, searchEngineName())
	fmt.Print("   完全在本机运行，不花钱。直接输入问题并回车；输入 exit 退出。\n\n")

	// liner 提供行编辑：中文退格、左右方向键、上下键翻历史（相当于 Python 的 readline）
	line := liner.NewLiner()
	defer line.Close()
	line.SetCtrlCAborts(true)

	messages := []Message{{Role: "system", Content: systemPrompt}}

	for {
		input, err := line.Prompt("你：")
		if err != nil {
			if errors.Is(err, liner.ErrPromptAborted) || errors.Is(err, io.EOF) {
				fmt.Println("\n再见！")
				return
			}
			fmt.Println("读取输入失败：", err)
			return
		}
		question := strings.TrimSpace(input)
		if question == "" {
			continue
		}
		line.AppendHistory(question)
		if q := strings.ToLower(question); q == "exit" || q == "quit" {
			fmt.Println("再见！")
			return
		}

		historyLen := len(messages)
		messages = append(messages, Message{Role: "user", Content: question})
		fmt.Println("（本地模型思考中，第一次可能要等十几秒……）")
		start := time.Now()
		answer, sources, err := ask(*model, messages, n, *think)
		if err != nil {
			fmt.Print(err, "\n\n")
			messages = messages[:historyLen] // 这一轮失败了，不计入对话历史
			continue
		}
		elapsed := time.Since(start).Seconds()
		answer = cleanCitations(answer, sources)

		// 历史里只保留问题和最终回答，网页资料不留，避免对话越来越长、越来越慢
		messages = append(messages[:historyLen+1], Message{Role: "assistant", Content: answer})

		if answer == "" {
			answer = "（模型没有返回文字）"
		}
		fmt.Println("\n助手：" + answer)
		if len(sources) > 0 {
			fmt.Println("\n📚 来源：")
			for _, n := range citedSources(answer, sources) {
				s := sources[n-1]
				fmt.Printf("  [%d] %s\n      %s\n", n, s.Title, s.URL)
			}
		} else {
			fmt.Println("\n📚 来源：这次没有联网搜索（模型判断不需要）。")
		}
		fmt.Printf("   （用时 %.1f 秒）\n\n", elapsed)
	}
}
