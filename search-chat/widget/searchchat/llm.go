package searchchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// message 是 OpenAI 兼容接口 /v1/chat/completions 的一条消息。
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// query 取出工具参数里的搜索词。arguments 按规范是 JSON 字符串，个别模型会直接给纯文本。
func (c toolCall) query() string {
	var args struct {
		Query any `json:"query"`
	}
	if json.Unmarshal([]byte(c.Function.Arguments), &args) != nil {
		return strings.TrimSpace(c.Function.Arguments)
	}
	if args.Query == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(args.Query))
}

var webSearchTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name": "web_search",
		"description": "在互联网上搜索最新信息，返回带编号的网页资料，并告诉你今天的日期。" +
			"凡是新闻、今天/最近的事、日期、价格、天气或你不确定的事实，都要调用。" +
			"常识、计算、写作、闲聊不需要调用。",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词，简洁明确，例如：2026年11月 科技新闻",
				},
			},
			"required": []string{"query"},
		},
	},
}

// llmError 是调用模型失败的原因。Public 给网页用户看，Detail 只写进服务端日志（已去掉 Key）。
type llmError struct {
	Status int
	Public string
	Detail string
}

func (e *llmError) Error() string { return e.Public }

type llmClient struct {
	cfg  Config
	http *http.Client
}

// complete 调用一次 /chat/completions。tools 为 false 时不提供搜索工具，模型只能直接回答。
func (c *llmClient) complete(ctx context.Context, messages []message, tools bool) (message, error) {
	req := map[string]any{
		"model":    c.cfg.LLMModel,
		"messages": messages,
		"stream":   false,
	}
	if tools {
		req["tools"] = []any{webSearchTool}
	}
	if c.cfg.LLMReasoningEffort != "" {
		req["reasoning_effort"] = c.cfg.LLMReasoningEffort
	}
	payload, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.LLMBaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return message{}, &llmError{Status: 500, Public: "模型接口地址 LLM_BASE_URL 写得不对。", Detail: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.LLMAPIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.LLMAPIKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return message{}, &llmError{Status: 504, Public: "模型回答超时了，请稍后再试。", Detail: c.redact(err.Error())}
		}
		return message{}, &llmError{Status: 502, Public: "连不上模型服务，请稍后再试。", Detail: c.redact(err.Error())}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return message{}, &llmError{Status: 502, Public: "读取模型回答失败，请重试。", Detail: c.redact(err.Error())}
	}
	if resp.StatusCode != 200 {
		detail := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, c.redact(truncate(string(body), 300)))
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return message{}, &llmError{Status: 502, Public: "模型服务拒绝了请求（服务端的 LLM_API_KEY 无效或没有权限）。", Detail: detail}
		case resp.StatusCode == 404:
			return message{}, &llmError{Status: 502, Public: "找不到模型（检查服务端的 LLM_BASE_URL 和 LLM_MODEL）。", Detail: detail}
		case resp.StatusCode == 429:
			return message{}, &llmError{Status: 503, Public: "模型服务繁忙或额度用完了，请稍后再试。", Detail: detail}
		case bytes.Contains(body, []byte("does not support tools")):
			return message{}, &llmError{Status: 502, Public: "当前模型不支持工具调用，没法联网搜索，请换一个模型。", Detail: detail}
		default:
			return message{}, &llmError{Status: 502, Public: fmt.Sprintf("模型服务返回错误（HTTP %d）。", resp.StatusCode), Detail: detail}
		}
	}
	var data struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &data); err != nil || len(data.Choices) == 0 {
		return message{}, &llmError{Status: 502, Public: "模型返回的内容看不懂。", Detail: c.redact(truncate(string(body), 300))}
	}
	return data.Choices[0].Message, nil
}

// redact 防止上游把 Key 原样带回错误信息里，进而被写进日志。
func (c *llmClient) redact(s string) string {
	return redactKeys(s, c.cfg.LLMAPIKey, c.cfg.TavilyAPIKey, c.cfg.BochaAPIKey)
}

func redactKeys(s string, keys ...string) string {
	for _, k := range keys {
		if len(k) >= 4 {
			s = strings.ReplaceAll(s, k, "***")
		}
	}
	return s
}
