#!/usr/bin/env python3
"""终端里的「大模型 + 联网搜索」聊天程序（通过 OpenRouter）。

用法：
    export OPENROUTER_API_KEY="你的 Key"
    python3 search_chat.py                     # 用默认模型
    python3 search_chat.py --model openai/gpt-5.2
    OPENROUTER_MODEL=google/gemini-3-flash-preview python3 search_chat.py

联网搜索使用 OpenRouter 的服务端工具 `openrouter:web_search`
（官方文档：https://openrouter.ai/docs/guides/features/server-tools/web-search ）。
搜索和读网页由 OpenRouter 在服务器端完成，本程序只需发一次请求就能拿到带引用的回答。
"""

import argparse
import os
import sys

try:
    import requests
except ImportError:
    print("找不到 requests 库。请先激活虚拟环境并运行：pip install -r requirements.txt")
    sys.exit(1)

API_URL = "https://openrouter.ai/api/v1/chat/completions"
DEFAULT_MODEL = "openai/gpt-5-mini"
DEFAULT_MAX_RESULTS = 5
TIMEOUT_SECONDS = 120

SYSTEM_PROMPT = (
    "你是一个会联网搜索的助手。需要最新信息或事实核查时，请使用网络搜索工具。"
    "默认用中文回答，内容简洁准确，并在正文中用 Markdown 链接标注引用来源。"
)


def parse_args():
    parser = argparse.ArgumentParser(description="终端联网搜索聊天（OpenRouter）")
    parser.add_argument(
        "--model",
        default=os.environ.get("OPENROUTER_MODEL", DEFAULT_MODEL),
        help="模型名，默认读取环境变量 OPENROUTER_MODEL，否则为 %s" % DEFAULT_MODEL,
    )
    parser.add_argument(
        "--max-results",
        type=int,
        default=DEFAULT_MAX_RESULTS,
        help="每次搜索最多返回多少条结果（1-25，默认 %d）" % DEFAULT_MAX_RESULTS,
    )
    return parser.parse_args()


def get_api_key():
    key = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if not key:
        print("❌ 没有找到 OpenRouter API Key。")
        print("")
        print("请先在终端里设置环境变量（把引号里的内容换成你自己的 Key）：")
        print('    export OPENROUTER_API_KEY="sk-or-v1-..."')
        print("然后再运行：")
        print("    python3 search_chat.py")
        print("")
        print("还没有 Key？请看 README.md 里「注册 OpenRouter 并生成 API Key」一节。")
        sys.exit(1)
    return key


def explain_http_error(status, body_text):
    """把常见的 HTTP 错误翻译成中文提示。"""
    hints = {
        400: "请求参数有误（可能是模型名写错，或该模型不支持联网工具）。",
        401: "API Key 无效。请检查 OPENROUTER_API_KEY 是否复制完整、没有多余空格，或在 openrouter.ai 重新生成一个。",
        402: "账户余额不足。请到 https://openrouter.ai/settings/credits 充值后再试。",
        403: "请求被拒绝（可能是内容审核或 Key 权限限制）。",
        404: "找不到该模型。请检查 --model 或 OPENROUTER_MODEL 的拼写。",
        408: "请求超时，请稍后重试。",
        429: "请求太频繁，被限流了。请等一会儿再试。",
        502: "模型服务暂时不可用，请稍后重试或换一个模型。",
        503: "暂时没有可用的模型服务商，请稍后重试或换一个模型。",
    }
    hint = hints.get(status, "服务器返回了错误。")
    detail = body_text[:300] if body_text else ""
    return "❌ 出错了（HTTP %d）：%s\n   服务器原文：%s" % (status, hint, detail)


def ask(api_key, model, max_results, messages):
    """发送一次请求，返回 (回答文本, 来源列表)。出错时抛出 RuntimeError。"""
    headers = {
        "Authorization": "Bearer " + api_key,
        "Content-Type": "application/json",
        # 以下两个是可选的，用于在 OpenRouter 后台显示应用名
        "HTTP-Referer": "https://github.com/mozm-ctrl/personal-website",
        "X-Title": "search-chat",
    }
    payload = {
        "model": model,
        "messages": messages,
        "tools": [
            {
                "type": "openrouter:web_search",
                "parameters": {"max_results": max_results},
            }
        ],
    }

    try:
        resp = requests.post(API_URL, headers=headers, json=payload, timeout=TIMEOUT_SECONDS)
    except requests.exceptions.ConnectionError:
        raise RuntimeError(
            "❌ 连不上 openrouter.ai。请检查网络（是否需要代理？），"
            "可以先在浏览器里打开 https://openrouter.ai 试试。"
        )
    except requests.exceptions.Timeout:
        raise RuntimeError("❌ 等了 %d 秒还没有回应，请稍后重试。" % TIMEOUT_SECONDS)
    except requests.exceptions.RequestException as e:
        raise RuntimeError("❌ 网络请求失败：%s" % e)

    if resp.status_code != 200:
        raise RuntimeError(explain_http_error(resp.status_code, resp.text))

    try:
        data = resp.json()
    except ValueError:
        raise RuntimeError("❌ 服务器返回的内容不是 JSON：%s" % resp.text[:300])

    # 有时 200 也会带 error 字段
    if "error" in data:
        err = data["error"] or {}
        code = err.get("code") if isinstance(err, dict) else None
        msg = err.get("message") if isinstance(err, dict) else str(err)
        if isinstance(code, int):
            raise RuntimeError(explain_http_error(code, msg or ""))
        raise RuntimeError("❌ 出错了：%s" % msg)

    choices = data.get("choices") or []
    if not choices:
        raise RuntimeError("❌ 服务器没有返回回答。")
    message = choices[0].get("message") or {}
    content = message.get("content") or ""

    sources = []
    seen = set()
    for ann in message.get("annotations") or []:
        if ann.get("type") != "url_citation":
            continue
        cite = ann.get("url_citation") or {}
        url = cite.get("url")
        if url and url not in seen:
            seen.add(url)
            sources.append((cite.get("title") or url, url))

    return content, sources


def main():
    args = parse_args()
    api_key = get_api_key()
    max_results = max(1, min(25, args.max_results))

    print("🔎 联网搜索聊天已启动（模型：%s）" % args.model)
    print("   直接输入问题并回车；输入 exit 退出。\n")

    messages = [{"role": "system", "content": SYSTEM_PROMPT}]

    while True:
        try:
            question = input("你：").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n再见！")
            break

        if not question:
            continue
        if question.lower() in ("exit", "quit"):
            print("再见！")
            break

        messages.append({"role": "user", "content": question})
        print("（正在搜索并思考，请稍等……）")

        try:
            answer, sources = ask(api_key, args.model, max_results, messages)
        except RuntimeError as e:
            print(str(e) + "\n")
            messages.pop()  # 这一轮失败了，不计入对话历史
            continue

        messages.append({"role": "assistant", "content": answer})

        print("\n助手：" + (answer.strip() or "（模型没有返回文字）"))
        if sources:
            print("\n📚 来源：")
            for i, (title, url) in enumerate(sources, 1):
                print("  [%d] %s\n      %s" % (i, title, url))
        else:
            print("\n📚 来源：这次回答没有附带来源链接（模型可能判断不需要搜索）。")
        print("")


if __name__ == "__main__":
    main()
