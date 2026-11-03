## search-chat：终端里的联网搜索聊天程序

在终端里输入问题，大模型会先上网搜索，再给你一个带**来源链接**的回答。搜索和读网页都由 OpenRouter 在服务器端完成，你只需要一个 OpenRouter 的 API Key。

适用环境：macOS（Apple Silicon / Intel 都可以），已自带 `python3`。

---

## 一、注册 OpenRouter 并生成 API Key

1. 打开 <https://openrouter.ai>，点右上角 **Sign in**，用 Google / GitHub 账号或邮箱注册登录。
2. 充值：打开 <https://openrouter.ai/settings/credits>，点 **Add Credits**，用信用卡充值。先充 **5 美元**就够试很久。
3. 生成 Key：打开 <https://openrouter.ai/settings/keys>，点 **Create Key**，起个名字（比如 `search-chat`），可以设置一个额度上限（比如 2 美元）防止意外花超。
4. 页面会显示一串以 `sk-or-v1-` 开头的 Key。**它只显示一次**，马上复制保存到密码管理器或备忘录里。
   - ⚠️ 不要把 Key 发给别人，不要提交到 GitHub，也不要写进代码里。

### 费用大致怎么算

每问一次，费用 = **模型费** + **搜索费**：

- **模型费**：按 token（大约等于字数）计费。默认模型 `openai/gpt-5-mini` 大约是输入每百万 token 0.25 美元、输出每百万 token 2 美元。搜索到的网页内容也会算进输入 token。
- **搜索费**：每次搜索单独收费。OpenAI、Google 等模型会用它们自己的原生搜索（按服务商价格）；其它模型默认用 Exa，大约 **0.007 美元 / 次**。
- 粗略估计：一次普通提问通常在 **几美分以内**。具体价格以 <https://openrouter.ai/models> 和你账户里的 **Activity** 页面为准。

---

## 二、安装并运行（每一步一条命令）

打开「终端」（Terminal）App，按顺序一条一条复制、粘贴、回车。

**1. 进入程序所在的文件夹**（把路径换成你电脑上仓库的实际位置）

```bash
cd ~/personal-website/search-chat
```

**2. 创建虚拟环境**（只需要做一次）

```bash
python3 -m venv .venv
```

**3. 激活虚拟环境**（每次新开终端都要做一次，成功后命令行前面会出现 `(.venv)`）

```bash
source .venv/bin/activate
```

**4. 安装依赖**（只需要做一次）

```bash
pip install -r requirements.txt
```

**5. 设置 API Key**（把引号里换成你自己的 Key；每次新开终端都要设置一次）

```bash
export OPENROUTER_API_KEY="sk-or-v1-你的Key"
```

**6. 运行**

```bash
python3 search_chat.py
```

看到 `你：` 就可以输入问题了，比如「今天有什么科技新闻？」。输入 `exit` 回车即可退出。

### 换模型

默认模型是 `openai/gpt-5-mini`（便宜、支持联网）。想换别的模型，有两种方法：

```bash
python3 search_chat.py --model google/gemini-3-flash-preview
```

或者：

```bash
export OPENROUTER_MODEL="openai/gpt-5.2"
```

模型名可以在 <https://openrouter.ai/models> 查到。还可以用 `--max-results 3` 调整每次搜索返回的结果数（1–25）。

---

## 三、常见报错及处理

| 你看到的 | 原因 | 怎么办 |
|---|---|---|
| `没有找到 OpenRouter API Key` | 当前终端没设置 Key | 重新执行第 5 步的 `export` 命令。新开的终端窗口需要重新设置 |
| `HTTP 401` / `API Key 无效` | Key 复制错了、少了字符、多了空格，或已被删除 | 到 <https://openrouter.ai/settings/keys> 重新生成一个，重新 `export` |
| `HTTP 402` / `余额不足` | 账户没钱了，或者 Key 的额度上限用完了 | 到 <https://openrouter.ai/settings/credits> 充值；或在 Key 设置里调高上限 |
| `连不上 openrouter.ai` | 网络不通，或需要代理 | 先用浏览器打开 <https://openrouter.ai> 看能不能访问。需要代理的话，在运行前设置：`export HTTPS_PROXY=http://127.0.0.1:7890`（端口换成你代理软件的端口） |
| `command not found: python3` | 电脑没装 Python 命令行工具 | 运行 `xcode-select --install` 安装，装好后重开终端 |
| `command not found: pip` | 没激活虚拟环境 | 先执行第 3 步 `source .venv/bin/activate`，或者用 `python3 -m pip install -r requirements.txt` |
| `找不到 requests 库` | 没安装依赖，或没激活虚拟环境 | 执行第 3 步和第 4 步 |
| `HTTP 400` / `HTTP 404` | 模型名写错，或该模型不支持联网工具 | 去 <https://openrouter.ai/models> 核对模型名，或换回默认模型 |
| `HTTP 429` | 请求太频繁 | 等一分钟再试 |

---

## 四、升级到方案 ③：自建搜索 + 国内模型

现在的方案 ② 是「OpenRouter 一站式」：模型和搜索都由 OpenRouter 提供。方案 ③ 是自己调用搜索 API（如[博查](https://open.bochaai.com)、[Tavily](https://tavily.com)），再把搜索结果交给国内模型（如 [DeepSeek](https://platform.deepseek.com)）。好处是国内网络更稳、价格可能更低；代价是要自己写「搜索 → 拼提示词」这一步。

需要改 `search_chat.py` 里的这些地方：

1. **接口地址 `API_URL`**：换成模型厂商的地址，例如 DeepSeek 是 `https://api.deepseek.com/chat/completions`（同样是 OpenAI 兼容格式）。
2. **API Key**：`get_api_key()` 里的 `OPENROUTER_API_KEY` 换成 `DEEPSEEK_API_KEY`；再新增一个搜索服务的 Key，比如 `BOCHA_API_KEY` 或 `TAVILY_API_KEY`。
3. **默认模型 `DEFAULT_MODEL`**：换成厂商自己的模型名，例如 `deepseek-chat`。
4. **去掉 `tools` 里的 `openrouter:web_search`**：这是 OpenRouter 独有的功能，别家不认识。
5. **新增一个 `search(question)` 函数**：调用博查或 Tavily 的搜索接口，拿到若干条 `标题 / 链接 / 摘要`。
6. **修改 `ask()`**：先调用 `search()`，把搜索结果整理成一段文字（编号 + 标题 + 链接 + 摘要），放进发给模型的消息里，并要求模型「按编号引用来源」。
7. **来源列表**：不再从返回的 `annotations` 里取，直接用第 5 步拿到的搜索结果来打印。
8. **请求头**：去掉 `HTTP-Referer` 和 `X-Title`（OpenRouter 专用）。
9. **README 和费用**：费用变成「搜索服务按次计费 + 模型按 token 计费」，两个账户分别充值。

需要的话可以再开一个任务，让我按这个思路把方案 ③ 写出来。

---

## 五、完全免费的本地版（Ollama + DuckDuckGo）

不想花钱、不想注册任何账号？用 `search_chat_local.py`：

- **大模型**在你自己的 Mac 上运行（[Ollama](https://ollama.com) + 开源模型 `qwen3:8b`），不联网也能聊天。
- **搜索**用 DuckDuckGo，不需要注册、不需要 Key。
- 全程 **0 元**。原来的 `search_chat.py`（OpenRouter 版）不受影响，两个可以随便换着用。

电脑要求：Apple 芯片（M1 及以上）、内存 16GB 更顺畅（8GB 请用小模型 `qwen3:4b`）、硬盘剩余 6GB 以上。

### 1. 安装 Ollama（只需要做一次）

1. 用浏览器打开 <https://ollama.com/download>，点 **Download for macOS**，下载到 `Ollama-darwin.zip`。
2. 双击解压，把 **Ollama.app** 拖进「应用程序」文件夹。
3. 打开「应用程序」里的 Ollama。第一次会问是否安装命令行工具，点同意（可能要输电脑开机密码；不输也没关系，见下面常见问题）。
4. 屏幕右上角菜单栏出现一个**小羊驼图标**，说明 Ollama 在运行了。

检查是否装好（打开「终端」输入）：

```bash
ollama --version
```

### 2. 下载模型（只需要做一次，约 5.2GB）

```bash
ollama pull qwen3:8b
```

下载完成后，用 `ollama list` 能看到 `qwen3:8b`。

### 3. 安装依赖（只需要做一次）

```bash
cd ~/search-chat/search-chat
source .venv/bin/activate
pip install -r requirements.txt
```

### 4. 运行

```bash
cd ~/search-chat/search-chat && .venv/bin/python search_chat_local.py
```

看到 `你：` 就可以提问了。问「今天有什么科技新闻？」它会先搜索，再回答并在末尾列出来源链接；问「1+1 等于几」这种常识就不会搜索，直接回答。输入 `exit` 退出。

其他选项：

```bash
.venv/bin/python search_chat_local.py --model qwen3:4b   # 换模型（要先 ollama pull qwen3:4b）
.venv/bin/python search_chat_local.py --think            # 让模型先“思考”再回答，更慢，复杂问题可能更准
.venv/bin/python search_chat_local.py --max-results 3    # 每次搜索取几条结果（1-10）
```

想用 [Tavily](https://tavily.com) 搜索（每月有免费额度，结果通常比 DuckDuckGo 干净）：注册拿到 Key 后，在运行前执行 `export TAVILY_API_KEY="tvly-你的Key"`。没设置这个变量时自动用 DuckDuckGo。

### 5. 常见问题

| 你看到的 | 原因 | 怎么办 |
|---|---|---|
| `连不上 Ollama` | Ollama 没在运行 | 打开「应用程序」里的 Ollama，等菜单栏出现小羊驼图标再运行 |
| `模型 xxx 还没有下载` | 没执行第 2 步，或模型名写错 | 运行提示里给出的 `ollama pull ...` 命令；`ollama list` 查看已下载的模型 |
| `command not found: ollama` | 命令行工具没装上（第一次打开时没输密码） | 用完整路径代替：`/Applications/Ollama.app/Contents/Resources/ollama pull qwen3:8b` |
| `DuckDuckGo 暂时限流了` | 短时间内搜索太多次 | 等一两分钟再问；或者改用 Tavily |
| `不支持工具调用` | 换的模型不会用工具，没法联网 | 换回 `qwen3:8b`，或在 <https://ollama.com/search?c=tools> 找带 tools 标签的模型 |
| 回答很慢 / 电脑变卡 | 本地模型很吃内存 | 关掉一些大程序；或用小模型 `qwen3:4b` |
| 回答里有明显错误 | 本地小模型能力有限，有时会理解错资料 | 点开来源链接核对；重要的事用 OpenRouter 版再问一次 |

不用的时候，点菜单栏小羊驼图标 → **Quit Ollama**，可以释放约 6GB 内存。

### 6. 和 OpenRouter 版对比

| | 本地版 `search_chat_local.py` | OpenRouter 版 `search_chat.py` |
|---|---|---|
| 费用 | **完全免费** | 按次付费，一次通常几美分以内 |
| 需要注册 | 不需要 | 需要 OpenRouter 账号、充值、API Key |
| 速度（M4 / 16GB 实测） | 不搜索约 1–2 秒，联网搜索约 20–25 秒（第一次启动多等十几秒加载模型） | 通常 5–20 秒，取决于模型 |
| 回答质量 | 8B 小模型：日常问答够用，但总结资料时偶尔挑错重点、遗漏细节 | 大模型 + 专业搜索，质量明显更好 |
| 搜索 | DuckDuckGo（免费，偶尔限流）或 Tavily | OpenRouter 服务端搜索（Exa 或模型原生搜索） |
| 内存占用 | 运行时约 6GB | 几乎不占 |
| 隐私 | 对话只在本机，只有搜索关键词会发给搜索引擎 | 对话会发给 OpenRouter 和模型厂商 |
