## 网站 AI 聊天 + 联网搜索组件

在任何网站右下角加一个聊天按钮：用户提问 → 服务端调用大模型，必要时先上网搜索 → 回答里用 [1][2] 标注来源，末尾列出来源链接。

- 模型：任何 **OpenAI 兼容接口**（OpenAI、DeepSeek、通义/豆包等平台、Ollama 的 `/v1`）。
- 搜索：默认免费的 DuckDuckGo；被限流或没结果时，自动改用你配置了 Key 的 Tavily / 博查。
- 前端：一个 `chat-widget.js`，页面里加一行 `<script>` 即可，不依赖 React / Vue，自动适配手机和深色模式。
- Key 只放在服务端环境变量里，前端和日志里都不会出现。

两种接入方式，任选一种：

| 方式 | 适合 | 你要做的 |
| --- | --- | --- |
| **A. 独立服务** | 网站后端是任何语言（PHP、Java、Node、Python……）或纯静态网站 | 运行一个程序（或 Docker 容器），页面里加一行 `<script>` |
| **B. Go 包** | 网站后端是 Go | 复制 `searchchat/` 目录，路由里加一行，页面里加一行 `<script>` |

## 3 分钟接入（方式 A）

需要：一台能访问外网的服务器，以及一个模型 Key（用 Ollama 本地模型可以不要 Key）。

**第 1 步：启动服务**（二选一）

用 Docker：

```bash
cd widget
cp .env.example .env        # 然后编辑 .env，至少填 LLM_BASE_URL、LLM_API_KEY、LLM_MODEL、ALLOWED_ORIGINS
docker build -t search-chat .
docker run -d --name search-chat -p 8080:8080 --env-file .env --restart unless-stopped search-chat
```

不用 Docker（需要 Go 1.26+，编译一次即可，生成的程序可以复制到同系统的服务器上运行）：

```bash
cd widget
go build -o search-chat-server ./cmd/search-chat-server
# Linux 服务器：GOOS=linux GOARCH=amd64 go build -o search-chat-server ./cmd/search-chat-server

export LLM_BASE_URL=https://api.deepseek.com/v1
export LLM_API_KEY=你的Key
export LLM_MODEL=deepseek-chat
export ALLOWED_ORIGINS=https://www.你的网站.com
./search-chat-server
```

**第 2 步：确认服务正常**

```bash
curl http://服务器地址:8080/healthz
# {"model":"deepseek-chat","ok":true,"search":"auto"}
```

**第 3 步：在网站页面 `</body>` 前加一行**

```html
<script src="https://聊天服务地址/chat-widget.js" data-endpoint="https://聊天服务地址/api/chat"></script>
```

刷新页面，右下角出现聊天按钮就接好了。

> 正式上线时请在聊天服务前面加 HTTPS（Nginx、Caddy 或云厂商的负载均衡）。网站是 https 时，聊天服务也必须是 https，否则浏览器会拦截。用了反向代理时，设置 `TRUST_PROXY=1`，限流才能按用户真实 IP 计算。

## 环境变量一览

**模型（必填前三项）**

| 变量 | 说明 | 示例 / 默认 |
| --- | --- | --- |
| `LLM_BASE_URL` | OpenAI 兼容接口地址，写到 `/v1` 为止 | `https://api.openai.com/v1`、`https://api.deepseek.com/v1`、`http://localhost:11434/v1` |
| `LLM_API_KEY` | 模型 Key；Ollama 不用填 | — |
| `LLM_MODEL` | 模型名，**必须支持工具调用（tools）** | `gpt-4o-mini`、`deepseek-chat`、`qwen3:8b` |
| `LLM_TIMEOUT` | 模型单次调用最多等多久 | 默认 `120s` |
| `LLM_REASONING_EFFORT` | 可选，原样作为 `reasoning_effort` 发给模型。推理模型（qwen3、o 系列）填 `none` / `low` 会快很多；普通模型不要填 | 空 |

**搜索**

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `SEARCH_ENGINE` | `auto`：先用免费的 DuckDuckGo，被限流/出错/没结果时依次改用配置了 Key 的收费引擎（中文问题先博查、英文先 Tavily）；`duckduckgo` / `tavily` / `bocha`：只用这一个 | `auto` |
| `TAVILY_API_KEY` | Tavily 的 Key（https://tavily.com），可选 | 空 |
| `BOCHA_API_KEY` | 博查的 Key（https://open.bochaai.com），可选 | 空 |
| `SEARCH_RESULTS` | 每次搜索取几条结果（1–10） | `5` |
| `SEARCH_CACHE_TTL` | 相同查询多久内直接用缓存；`0` 关闭 | `5m` |

每轮提问最多搜索 2 次（写在代码里：`searchchat/chat.go` 的 `maxSearchesPerTurn`）。

**服务与安全**

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `PORT` | 监听端口（仅方式 A） | `8080` |
| `LISTEN_ADDR` | 监听地址，填 `127.0.0.1` 则只允许本机访问（仅方式 A） | 所有网卡 |
| `ALLOWED_ORIGINS` | 允许接入的网站，逗号分隔，要写完整的协议和域名（含端口），如 `https://a.com,https://www.a.com`。`*` 表示全部允许（不建议）。不填时只允许和聊天服务同域名的页面 | 空 |
| `ACCESS_TOKEN` | 访问口令，可选。设置后前端要加 `data-token="口令"`，否则返回 401 | 空 |
| `RATE_LIMIT_PER_MIN` | 每个 IP 每分钟最多提问几次；`0` 不限 | `10` |
| `TRUST_PROXY` | 服务在 Nginx 等反向代理后面时设为 `1`，按 `X-Forwarded-For` 识别真实 IP。**直接对外时不要开**，否则用户可以伪造 IP 绕过限流 | 关 |
| `MAX_CONCURRENT` | 同时最多处理几个提问，多出来的排队；本地 Ollama 建议 `1` | `4` |

> 访问口令会出现在网页源码里，只能挡住随手的滥用，不能代替限流和 CORS 白名单。真正的 Key 永远只在服务端。

## 前端组件 `chat-widget.js`

```html
<script src="https://聊天服务地址/chat-widget.js"
        data-endpoint="https://聊天服务地址/api/chat"
        data-token="口令（没设 ACCESS_TOKEN 就不写）"
        data-title="AI 助手"
        data-greeting="你好，有什么可以帮你？"
        data-color="#2563eb"
        data-position="right"></script>
```

除 `src` 外都可以省略。`data-endpoint` 不写时，默认是脚本所在目录下的 `api/chat`。组件用 Shadow DOM 隔离样式，不会影响网站原有样式；屏幕宽度小于 480px 时聊天框自动全屏；跟随系统深色模式。

`chat-widget.js` 由聊天服务自己提供（`/chat-widget.js`），也可以把 `searchchat/chat-widget.js` 复制到你网站的静态文件目录里引用，这时 `data-endpoint` 必须写。

## 接口说明

### `POST /api/chat`

请求头：`Content-Type: application/json`；设置了口令时加 `X-Chat-Token: 口令`。

请求体：对话历史，只能有 `user` / `assistant` 两种角色，最后一条必须是这次的问题（最多保留最近 20 条，每条最多 4000 字）：

```json
{"messages": [
  {"role": "user", "content": "今天有什么科技新闻？"},
  {"role": "assistant", "content": "……"},
  {"role": "user", "content": "第一条再详细说说"}
]}
```

成功（HTTP 200）：

```json
{
  "answer": "今天是 2026年11月4日。世界互联网大会乌镇峰会于11月3日开幕 [1]。",
  "sources": [{"n": 1, "title": "科技新闻_央视网", "url": "https://news.cctv.com/tech/"}],
  "searches": [{"query": "今天 科技新闻", "engine": "duckduckgo"}],
  "seconds": 14.5
}
```

- `sources` 只列回答里实际引用的来源；`n` 对应回答里的 `[n]`。没有联网时为空数组。
- `searches` 是本轮的搜索记录，`engine` 带「（缓存）」表示命中缓存。

失败：`{"error": "给用户看的中文说明"}`，HTTP 状态码见下方「常见报错」。

### `GET /chat-widget.js`

前端组件脚本。

### `GET /healthz`

健康检查，返回 `{"ok":true,"model":"...","search":"auto"}`。

## 方式 A 示例（独立服务）

已有网站不管是什么语言，只需在模板里加那一行 `<script>`。常见的部署方式：

```nginx
# Nginx：把聊天服务挂到网站自己的域名下，就不用配置跨域
location /ai/ {
    proxy_pass http://127.0.0.1:8080/;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_read_timeout 180s;
}
```

```html
<!-- 页面里 -->
<script src="/ai/chat-widget.js"></script>
```

这时启动服务加上 `TRUST_PROXY=1` 和 `LISTEN_ADDR=127.0.0.1`。

`examples/blank-page/index.html` 是一个只有这一行 `<script>` 的空白页，可以用来测试。

## 方式 B 示例（Go 包）

1. 把 `searchchat/` 整个目录复制进你的项目，例如 `你的项目/internal/searchchat/`。
2. 安装依赖：`go get github.com/PuerkitoBio/goquery golang.org/x/net`
3. 路由里加一行：

```go
import "你的模块名/internal/searchchat"

mux.Handle("/ai/", http.StripPrefix("/ai", searchchat.Handler(searchchat.ConfigFromEnv())))
```

4. 页面里加：`<script src="/ai/chat-widget.js"></script>`
5. 启动你的程序前设置上面表格里的环境变量（`PORT`、`LISTEN_ADDR` 除外，端口由你的程序决定）。

不想用环境变量也可以直接填配置（Key 仍然建议从环境变量或密钥管理里取）：

```go
searchchat.Handler(searchchat.Config{
    LLMBaseURL:         "https://api.deepseek.com/v1",
    LLMAPIKey:          os.Getenv("DEEPSEEK_API_KEY"),
    LLMModel:           "deepseek-chat",
    AllowedOrigins:     []string{"https://www.example.com"},
    RateLimitPerMinute: 10,
})
```

注意：直接填 `Config` 时，`RateLimitPerMinute` 和 `SearchCacheTTL` 不写就是「不限流」「不缓存」；`ConfigFromEnv()` 才会套用上面表格里的默认值（10 次/分钟、5 分钟）。

完整可运行的例子：`examples/go-embed/main.go`（`go run ./examples/go-embed` 后打开 http://localhost:3000）。

## 常见报错和处理办法

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 启动时报「缺少 LLM_BASE_URL / LLM_MODEL」 | 环境变量没设置 | 按上表设置；Docker 检查 `--env-file` 路径 |
| 聊天框显示「连不上聊天服务（网络或跨域限制）」，浏览器控制台有 `blocked by CORS policy` | 网站域名不在 `ALLOWED_ORIGINS` 里 | 把控制台里 `from origin '...'` 的地址原样加进 `ALLOWED_ORIGINS`（注意 `www`、`https`、端口），重启服务 |
| 同上，但控制台是 `Mixed Content` | 网站是 https，聊天服务是 http | 给聊天服务加 HTTPS，或用 Nginx 挂到网站同域名下 |
| 403「这个网站没有被允许接入」 | 同 CORS，请求是直接发来的 | 同上 |
| 401「访问口令不对」 | 设置了 `ACCESS_TOKEN`，但前端没写或写错 `data-token` | 两边改成一致 |
| 429「提问太频繁了」 | 超过 `RATE_LIMIT_PER_MIN` | 等一分钟；或调大限制。用了反向代理却没开 `TRUST_PROXY`，所有人会共用一个 IP 的额度 |
| 「模型服务拒绝了请求」 | `LLM_API_KEY` 无效或没权限 | 检查 Key 和余额 |
| 「找不到模型」 | `LLM_BASE_URL` 少了 `/v1`，或 `LLM_MODEL` 写错 | 地址写到 `/v1` 为止；Ollama 先 `ollama pull 模型名` |
| 「当前模型不支持工具调用」 | 模型不支持 tools | 换支持 function calling 的模型（gpt-4o-mini、deepseek-chat、qwen3 等） |
| 「模型回答超时了」 | 模型太慢 | 调大 `LLM_TIMEOUT`，推理模型设 `LLM_REASONING_EFFORT=none` |
| 回答说「这次没能联网」 | 所有搜索引擎都失败（常见是 DuckDuckGo 限流且没配收费引擎） | 配一个 `TAVILY_API_KEY` 或 `BOCHA_API_KEY` 作后备；服务端日志有具体原因 |
| 页面没出现聊天按钮 | 脚本没加载成功 | 浏览器打开 `src` 里的地址，确认能看到 JS 代码 |

服务端日志只记录 IP、用时、搜索次数和错误原因，不记录提问内容，也不会出现任何 Key。

## 安全设计

- Key 只从服务端环境变量读取；返回给前端的错误信息是固定的中文提示，上游错误详情写日志前会把 Key 替换成 `***`。
- `ALLOWED_ORIGINS` 白名单 + 可选口令 + 按 IP 限流 + 并发上限。
- 前端只能传 `user` / `assistant` 消息，传 `system` / `tool` 会被拒绝。
- 搜到的网页内容放在 `<资料>` 标签里交给模型，系统提示明确要求只当参考资料、不执行其中的指令。
- 抓取网页正文时不允许访问内网和本机地址（防 SSRF），单页最多读 2MB。
- 前端用 DOM 节点渲染回答，不拼 HTML；来源链接只允许 http/https。

## 文件说明

```
widget/
├── searchchat/            # 核心代码（Go 包），方式 B 复制这个目录
│   ├── handler.go         # Handler(cfg)：路由、CORS、口令、限流
│   ├── config.go          # 配置和环境变量
│   ├── chat.go            # 一轮对话：调用模型、搜索、整理引用
│   ├── llm.go             # OpenAI 兼容接口客户端
│   ├── search.go          # 搜索策略、缓存、Tavily、博查、抓网页正文
│   ├── ddg.go             # DuckDuckGo
│   └── chat-widget.js     # 前端组件（编译时打包进程序）
├── cmd/search-chat-server # 方式 A 的程序入口
├── examples/              # 空白测试页、Go 接入示例
├── Dockerfile
├── .env.example           # 环境变量模板
└── go.mod / go.sum
```
