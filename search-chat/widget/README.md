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

网站有自己的账号系统、想让每个用户用自己的积分付费？用方式 B，见「[接入账号系统和按用户扣费](#接入账号系统和按用户扣费方式-b)」。

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
| `RATE_LIMIT_PER_MIN` | 每个 IP（设置了 `Authenticate` 时为每个用户）每分钟最多提问几次；`0` 不限 | `10` |
| `TRUST_PROXY` | 服务在 Nginx 等反向代理后面时设为 `1`，按 `X-Forwarded-For` 识别真实 IP。**直接对外时不要开**，否则用户可以伪造 IP 绕过限流 | 关 |
| `MAX_CONCURRENT` | 同时最多处理几个提问，多出来的排队；本地 Ollama 建议 `1` | `4` |

**按用户扣费**（只在方式 B 设置了 `Authenticate` / `Billing` 时生效，见「接入账号系统和按用户扣费」）

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `BILLING_MODE` | `per_request` 按次 / `per_token` 按用量 | `per_request` |
| `PRICE_PER_REQUEST` | 按次：每次提问扣几积分 | `1` |
| `PRICE_PER_SEARCH` | 每次联网搜索额外扣几积分（两种方式都生效） | `0` |
| `PRICE_INPUT_PER_1K` / `PRICE_OUTPUT_PER_1K` | 按用量：每 1000 个输入 / 输出 token 多少积分，可以是小数 | — |
| `MAX_CHARGE` | 按用量：单次提问最多扣几积分（也是预扣金额） | `100` |
| `DAILY_LIMIT_PER_USER` | 每个用户每天最多问几次；`0` 不限 | `0` |

> 访问口令会出现在网页源码里，只能挡住随手的滥用，不能代替限流和 CORS 白名单。真正的 Key 永远只在服务端。

## 前端组件 `chat-widget.js`

```html
<script src="https://聊天服务地址/chat-widget.js"
        data-endpoint="https://聊天服务地址/api/chat"
        data-token="口令（没设 ACCESS_TOKEN 就不写）"
        data-title="AI 助手"
        data-greeting="你好，有什么可以帮你？"
        data-color="#2563eb"
        data-position="right"
        data-auth-storage="localStorage 里存登录 Token 的键名（接入账号系统时用）"
        data-login-url="/login"
        data-recharge-url="/recharge"></script>
```

除 `src` 外都可以省略。`data-endpoint` 不写时，默认是脚本所在目录下的 `api/chat`。组件用 Shadow DOM 隔离样式，不会影响网站原有样式；屏幕宽度小于 480px 时聊天框自动全屏；跟随系统深色模式。

`chat-widget.js` 由聊天服务自己提供（`/chat-widget.js`），也可以把 `searchchat/chat-widget.js` 复制到你网站的静态文件目录里引用，这时 `data-endpoint` 必须写。

## 接口说明

### `POST /api/chat`

请求头：`Content-Type: application/json`；设置了口令时加 `X-Chat-Token: 口令`；接入账号系统时加 `Authorization: Bearer 用户Token`，可选 `X-Request-ID: 每个问题唯一的 ID`（8–64 位字母、数字、`-`、`_`）。

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

接入账号系统扣费时还会多 `cost`（本次消耗的积分）和 `request_id`。

失败：`{"error": "给用户看的中文说明"}`，HTTP 状态码见下方「常见报错」；401 / 402 等还会带 `code`，见「接入账号系统和按用户扣费」的状态码表。

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

## 接入账号系统和按用户扣费（方式 B）

默认情况下所有访客共用网站主人的模型 Key，费用全记在网站主人账上。如果你的网站有自己的账号系统（Go 后端、登录状态用 Token 保存），可以改成：**只有登录用户能提问，每次提问从这个用户自己的积分里扣费。**

我们拿不到你的用户表和余额表，所以做成了接口：你实现 4 个函数，接上自己的数据库就能用。

| 你要实现的 | 作用 |
| --- | --- |
| `Authenticate(r *http.Request) (userID string, err error)` | 校验请求里的登录 Token，返回用户 ID |
| `Billing.Reserve(ctx, userID, requestID, amount)` | 提问前：检查余额并冻结（预扣）最高费用 |
| `Billing.Settle(ctx, charge)` | 回答成功后：按实际用量结算，多冻结的退回 |
| `Billing.Refund(ctx, userID, requestID)` | 失败时（模型出错、超时、用户断开）：全额退回冻结的钱 |

> **只在登录后的页面才加 `<script>` 是不够的。** 聊天接口是公开的地址，任何人都能直接用 curl 调用。必须在服务端设置 `Authenticate` 校验 Token，否则谁都能免费用你的模型。

### 接入步骤

**第 1 步：实现 `Authenticate`**，校验你自己的 Token（JWT、session 表都行）：

```go
cfg := searchchat.ConfigFromEnv()
cfg.Authenticate = func(r *http.Request) (string, error) {
    token := searchchat.BearerToken(r) // 取 Authorization: Bearer <token>
    claims, err := myauth.ParseJWT(token) // 换成你自己的校验
    if err != nil {
        return "", err // → 401「请先登录」，不会调用模型
    }
    return claims.UserID, nil
}
```

**第 2 步：实现 `Billing`**，接上你的余额表（SQL 参考写法见下文），然后：

```go
cfg.Billing = &SQLBilling{DB: db}
cfg.Pricing = searchchat.Pricing{Mode: searchchat.BillingPerRequest, PerRequest: 2, PerSearch: 1}
cfg.DailyLimitPerUser = 50 // 可选：每个用户每天最多问 50 次
mux.Handle("/ai/", http.StripPrefix("/ai", searchchat.Handler(cfg)))
```

**第 3 步：前端把登录 Token 交给聊天组件**，二选一：

```html
<!-- 方式一：你的网站登录后把 Token 存在 localStorage 里，告诉组件键名 -->
<script src="/ai/chat-widget.js"
        data-auth-storage="token"
        data-login-url="/login"
        data-recharge-url="/recharge"></script>
```

```js
// 方式二：你自己的 JS 在登录后设置（退出登录时 setToken(null)）
window.SearchChat.setToken(token);
```

组件会把 Token 以 `Authorization: Bearer <token>` 发给聊天接口。`data-login-url` / `data-recharge-url` 可选，设置后「请先登录」「余额不足」的提示里会带上对应链接。回答成功后，回答下方会用小字显示「本次消耗 X 积分」。

> 聊天服务和网站不在同一个域名时，要把网站加进 `ALLOWED_ORIGINS`。Token 存在 Cookie 里的网站，建议用方式二把 Token 交给组件，或者把聊天服务挂在网站同域名下，在 `Authenticate` 里直接读 Cookie。

### 完整示例

`examples/billing-demo/main.go`：用内存里的用户表和余额表模拟账号系统，演示 `Authenticate` 和 `Billing` 的写法，页面上可以登录、提问、看余额变化、遇到余额不足再充值。

```bash
cd widget
LLM_BASE_URL=http://localhost:11434/v1 LLM_MODEL=qwen3:8b LLM_REASONING_EFFORT=none \
  go run ./examples/billing-demo
# 打开 http://localhost:3001，用户 alice（10 积分）/ bob（1 积分），密码 123456
```

### 计费方式

两种方式，用 `cfg.Pricing` 或环境变量配置，默认**按次、每次 1 积分**。积分是整数。

| 方式 | 怎么算 | 提问前预扣 | 配置 |
| --- | --- | --- | --- |
| **按次**（默认） | 每次提问 `PerRequest`，每次联网搜索再加 `PerSearch` | `PerRequest + 2 × PerSearch`（每轮最多搜 2 次） | `BILLING_MODE=per_request` `PRICE_PER_REQUEST=2` `PRICE_PER_SEARCH=1` |
| **按用量** | `ceil((输入token × InputPer1K + 输出token × OutputPer1K) / 1000) + 搜索次数 × PerSearch` | `MaxCharge` | `BILLING_MODE=per_token` `PRICE_INPUT_PER_1K=2` `PRICE_OUTPUT_PER_1K=8` `MAX_CHARGE=20` |

- token 数取自模型返回的 `usage`（一轮里多次调用模型的总和）；失败的搜索不收费。
- 按用量时实际费用超过 `MaxCharge` 就按 `MaxCharge` 收，所以 `MaxCharge` 也是「单次提问最多花多少」。
- 例：按次 `PerRequest=2, PerSearch=1` → 预扣 4；没搜索的问题扣 2（退回 2），搜索 1 次扣 3（退回 1）。

### 接口说明

```go
type Billing interface {
    Reserve(ctx context.Context, userID, requestID string, amount int64) error
    Settle(ctx context.Context, c searchchat.Charge) error
    Refund(ctx context.Context, userID, requestID string) error
}

type Charge struct {
    UserID           string
    RequestID        string // 每次提问唯一，用来做幂等
    PromptTokens     int    // 输入 token 数
    CompletionTokens int    // 输出 token 数
    Searches         int    // 成功的联网搜索次数
    Reserved         int64  // 提问前冻结的积分
    Cost             int64  // 应扣积分（≤ Reserved）
}
```

- `Reserve` 余额不足时返回 `searchchat.ErrInsufficientBalance`；`requestID` 已经用过时返回 `searchchat.ErrDuplicateRequest`；其他错误（如数据库连不上）返回 503，都不会调用模型。
- **必须是原子操作**：「检查余额」和「扣减」要在一条 SQL 或一个事务里完成（`UPDATE ... WHERE balance >= ?`），否则同一个用户同时发多个问题时，余额会被扣成负数。
- `requestID` 由前端为每个问题生成（`X-Request-ID`，格式不对时服务端重新生成）。用 `(user_id, request_id)` 做唯一索引，同一个问题被重复提交时只扣一次；`Settle` / `Refund` 只处理状态还是「已冻结」的记录，重复调用也不会多扣或多退。
- 用户断开连接、模型出错或超时，都会调用 `Refund`（用的 context 不会因为用户断开而取消）。
- `Settle` 失败时回答仍然返回给用户，服务端日志会记下 `request_id`，冻结的积分请按 `request_id` 对账处理。

### 接 SQL 数据库的参考写法

表结构（MySQL 示例）：

```sql
CREATE TABLE chat_charges (
  user_id    VARCHAR(64) NOT NULL,
  request_id VARCHAR(64) NOT NULL,
  reserved   BIGINT      NOT NULL,
  cost       BIGINT      NULL,
  status     VARCHAR(16) NOT NULL,  -- reserved / settled / refunded
  created_at TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, request_id)
);
-- 假设你已有 users 表，余额字段是 balance（BIGINT）
```

```go
type SQLBilling struct{ DB *sql.DB }

func (b *SQLBilling) Reserve(ctx context.Context, userID, reqID string, amount int64) error {
    tx, err := b.DB.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    // 主键冲突 = 这个问题已经提交过
    if _, err := tx.ExecContext(ctx,
        `INSERT INTO chat_charges (user_id, request_id, reserved, status) VALUES (?, ?, ?, 'reserved')`,
        userID, reqID, amount); err != nil {
        if isDuplicateKey(err) { // MySQL 错误码 1062；PostgreSQL 是 23505
            return searchchat.ErrDuplicateRequest
        }
        return err
    }
    // 检查余额和扣减在同一条 SQL 里完成，并发时也不会扣成负数
    res, err := tx.ExecContext(ctx,
        `UPDATE users SET balance = balance - ? WHERE id = ? AND balance >= ?`, amount, userID, amount)
    if err != nil {
        return err
    }
    if n, _ := res.RowsAffected(); n == 0 {
        return searchchat.ErrInsufficientBalance // defer 里回滚，chat_charges 记录也不会留下
    }
    return tx.Commit()
}

func (b *SQLBilling) Settle(ctx context.Context, c searchchat.Charge) error {
    return b.finish(ctx, c.UserID, c.RequestID, "settled", c.Cost)
}

func (b *SQLBilling) Refund(ctx context.Context, userID, reqID string) error {
    return b.finish(ctx, userID, reqID, "refunded", 0)
}

// finish 把冻结记录改成 settled / refunded，并把 reserved - cost 退回余额。
// WHERE status = 'reserved' 保证同一个 request_id 只会处理一次。
func (b *SQLBilling) finish(ctx context.Context, userID, reqID, status string, cost int64) error {
    tx, err := b.DB.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    var reserved int64
    err = tx.QueryRowContext(ctx,
        `SELECT reserved FROM chat_charges WHERE user_id = ? AND request_id = ? AND status = 'reserved' FOR UPDATE`,
        userID, reqID).Scan(&reserved)
    if errors.Is(err, sql.ErrNoRows) {
        return nil // 已经处理过
    }
    if err != nil {
        return err
    }
    if _, err := tx.ExecContext(ctx,
        `UPDATE chat_charges SET status = ?, cost = ? WHERE user_id = ? AND request_id = ?`,
        status, cost, userID, reqID); err != nil {
        return err
    }
    if _, err := tx.ExecContext(ctx,
        `UPDATE users SET balance = balance + ? WHERE id = ?`, reserved-cost, userID); err != nil {
        return err
    }
    return tx.Commit()
}
```

PostgreSQL 把 `?` 换成 `$1, $2…` 即可。服务意外重启时可能留下 `status = 'reserved'` 的记录，可以定时把超过 10 分钟还没结算的记录按 `Refund` 的逻辑退回。

### 限流

设置了 `Authenticate` 后，`RATE_LIMIT_PER_MIN` 改为按用户 ID 计算（同一个公司 / 网吧共用 IP 的用户互不影响）；`DAILY_LIMIT_PER_USER`（或 `cfg.DailyLimitPerUser`）限制每个用户每天最多问几次（服务器本地时间零点清零，失败和余额不足的提问不占次数）。没有设置 `Authenticate` 时仍按 IP 限流，和以前一样。

### 状态码

| 状态码 | `code` | 含义 | 前端显示 |
| --- | --- | --- | --- |
| 401 | `login_required` | 没带 Token，或 `Authenticate` 返回了错误 | 「请先登录」+ `data-login-url` 链接 |
| 402 | `insufficient_balance` | 余额不够这次预扣，没有调用模型 | 「余额不足」+ `data-recharge-url` 链接 |
| 409 | `duplicate_request` | 同一个 `X-Request-ID` 提交了两次，没有重复扣费 | 错误提示 |
| 429 | `daily_limit` | 超过每个用户每天的次数 | 错误提示 |
| 503 | — | `Reserve` 返回了其他错误（如数据库故障） | 错误提示 |

成功的响应里多两个字段：`"cost": 2`（本次消耗的积分）和 `"request_id": "..."`。

### 常见问题

- **没设置 `Authenticate` / `Billing` 会怎样？** 和以前完全一样：不用登录，不扣费，按 IP 限流。只设 `Authenticate` 不设 `Billing` 也可以：只允许登录用户使用、按用户限流，但不扣费。
- **Token 放在 Cookie 里怎么办？** 聊天服务挂在网站同域名下（例如 `/ai/`），在 `Authenticate` 里用 `r.Cookie("session")` 校验即可，前端不用设置 `data-auth-storage`。
- **为什么要预扣？** 回答要几秒到几十秒，期间同一个用户可能又发了几个问题。先冻结最高费用，余额就不可能被并发请求扣成负数；回答后多退少补。
- **积分不是整数怎么办？** 把单位换小一点，例如 1 元 = 100 积分。
- **用户中途关掉页面会扣钱吗？** 不会。模型调用会被取消，冻结的积分全部退回。
- **余额不足时会调用模型吗？** 不会，直接返回 402。
- **能用环境变量运行独立服务（方式 A）并扣费吗？** 不能，`Authenticate` 和 `Billing` 要写 Go 代码接你的数据库，只能用方式 B。

## 常见报错和处理办法

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 启动时报「缺少 LLM_BASE_URL / LLM_MODEL」 | 环境变量没设置 | 按上表设置；Docker 检查 `--env-file` 路径 |
| 聊天框显示「连不上聊天服务（网络或跨域限制）」，浏览器控制台有 `blocked by CORS policy` | 网站域名不在 `ALLOWED_ORIGINS` 里 | 把控制台里 `from origin '...'` 的地址原样加进 `ALLOWED_ORIGINS`（注意 `www`、`https`、端口），重启服务 |
| 同上，但控制台是 `Mixed Content` | 网站是 https，聊天服务是 http | 给聊天服务加 HTTPS，或用 Nginx 挂到网站同域名下 |
| 403「这个网站没有被允许接入」 | 同 CORS，请求是直接发来的 | 同上 |
| 401「访问口令不对」 | 设置了 `ACCESS_TOKEN`，但前端没写或写错 `data-token` | 两边改成一致 |
| 聊天框显示「请先登录」（401） | 设置了 `Authenticate`，但请求没带 Token 或 Token 无效 | 确认 `data-auth-storage` 键名正确或调用了 `SearchChat.setToken`；检查 `Authenticate` 的校验逻辑 |
| 聊天框显示「余额不足」（402） | 用户余额不够这次预扣 | 充值；或调低 `PRICE_*` / `MAX_CHARGE` |
| 429「提问太频繁了」 | 超过 `RATE_LIMIT_PER_MIN` | 等一分钟；或调大限制。用了反向代理却没开 `TRUST_PROXY`，所有人会共用一个 IP 的额度 |
| 「模型服务拒绝了请求」 | `LLM_API_KEY` 无效或没权限 | 检查 Key 和余额 |
| 「找不到模型」 | `LLM_BASE_URL` 少了 `/v1`，或 `LLM_MODEL` 写错 | 地址写到 `/v1` 为止；Ollama 先 `ollama pull 模型名` |
| 「当前模型不支持工具调用」 | 模型不支持 tools | 换支持 function calling 的模型（gpt-4o-mini、deepseek-chat、qwen3 等） |
| 「模型回答超时了」 | 模型太慢 | 调大 `LLM_TIMEOUT`，推理模型设 `LLM_REASONING_EFFORT=none` |
| 回答说「这次没能联网」 | 所有搜索引擎都失败（常见是 DuckDuckGo 限流且没配收费引擎） | 配一个 `TAVILY_API_KEY` 或 `BOCHA_API_KEY` 作后备；服务端日志有具体原因 |
| 页面没出现聊天按钮 | 脚本没加载成功 | 浏览器打开 `src` 里的地址，确认能看到 JS 代码 |

服务端日志只记录 IP（或用户 ID）、用时、搜索次数、扣费金额和错误原因，不记录提问内容和用户 Token，也不会出现任何 Key。

## 安全设计

- Key 只从服务端环境变量读取；返回给前端的错误信息是固定的中文提示，上游错误详情写日志前会把 Key 替换成 `***`。
- `ALLOWED_ORIGINS` 白名单 + 可选口令 + 按 IP 限流 + 并发上限。
- 接入账号系统时由服务端 `Authenticate` 校验每个请求的 Token；扣费先预扣再结算，余额不足不调用模型。
- 前端只能传 `user` / `assistant` 消息，传 `system` / `tool` 会被拒绝。
- 搜到的网页内容放在 `<资料>` 标签里交给模型，系统提示明确要求只当参考资料、不执行其中的指令。
- 抓取网页正文时不允许访问内网和本机地址（防 SSRF），单页最多读 2MB。
- 前端用 DOM 节点渲染回答，不拼 HTML；来源链接只允许 http/https。

## 文件说明

```
widget/
├── searchchat/            # 核心代码（Go 包），方式 B 复制这个目录
│   ├── handler.go         # Handler(cfg)：路由、CORS、口令、登录校验、限流、扣费流程
│   ├── billing.go         # Billing 接口、计费规则、每日限额
│   ├── config.go          # 配置和环境变量
│   ├── chat.go            # 一轮对话：调用模型、搜索、整理引用
│   ├── llm.go             # OpenAI 兼容接口客户端
│   ├── search.go          # 搜索策略、缓存、Tavily、博查、抓网页正文
│   ├── ddg.go             # DuckDuckGo
│   └── chat-widget.js     # 前端组件（编译时打包进程序）
├── cmd/search-chat-server # 方式 A 的程序入口
├── examples/              # 空白测试页、Go 接入示例、按用户扣费示例（billing-demo）
├── Dockerfile
├── .env.example           # 环境变量模板
└── go.mod / go.sum
```
