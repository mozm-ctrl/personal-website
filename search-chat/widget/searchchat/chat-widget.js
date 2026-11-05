/*
 * 联网搜索聊天组件（不依赖任何前端框架）
 *
 * 用法：在页面 </body> 前加一行
 *   <script src="https://聊天服务地址/chat-widget.js" data-endpoint="https://聊天服务地址/api/chat"></script>
 *
 * 可选属性：
 *   data-endpoint  聊天接口地址，默认 = 本脚本所在目录 + api/chat
 *   data-token     访问口令（服务端设置了 ACCESS_TOKEN 时必填）
 *   data-title     聊天框标题，默认「AI 助手」
 *   data-greeting  第一句欢迎语
 *   data-color     主题色，默认 #2563eb
 *   data-position  right（默认）或 left
 *
 * 接入网站账号系统（服务端设置了 Authenticate / Billing 时）：
 *   data-auth-storage  从 localStorage 的这个键读取用户登录 Token，以 Authorization: Bearer <token> 发送
 *   data-login-url     未登录（401）时提示里附上的登录链接
 *   data-recharge-url  余额不足（402）时提示里附上的充值链接
 *   也可以由网站自己的 JS 在登录后调用 window.SearchChat.setToken(token)，退出登录时 setToken(null)。
 */
(function () {
  "use strict";
  var script = document.currentScript;
  if (!script || window.__searchChatWidget) return;
  window.__searchChatWidget = true;

  function attr(name, def) {
    var v = script.getAttribute("data-" + name);
    return v == null || v === "" ? def : v;
  }
  var endpoint = attr("endpoint", new URL("api/chat", script.src).href);
  var token = attr("token", "");
  var title = attr("title", "AI 助手");
  var greeting = attr("greeting", "你好！我会先上网搜索再回答，并在最后列出来源链接。");
  var color = attr("color", "#2563eb");
  var side = attr("position", "right") === "left" ? "left" : "right";
  var authStorage = attr("auth-storage", "");
  var loginURL = attr("login-url", "");
  var rechargeURL = attr("recharge-url", "");

  // window.SearchChat.setToken(token)：网站登录后设置；在本脚本加载前设置 window.SearchChat = {token: "..."} 也可以
  var api = window.SearchChat || {};
  var userToken = api.token || null;
  api.setToken = function (t) { userToken = t || null; };
  window.SearchChat = api;

  function currentToken() {
    if (userToken) return userToken;
    if (!authStorage) return null;
    try {
      var v = window.localStorage.getItem(authStorage);
      if (v && v.charAt(0) === '"') v = JSON.parse(v); // 有的网站用 JSON.stringify 存
      return v || null;
    } catch (e) { return null; }
  }

  function newRequestID() {
    if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
    return "r" + Date.now().toString(36) + Math.random().toString(36).slice(2, 12);
  }

  var css =
    ":host{all:initial}" +
    "*{box-sizing:border-box;font-family:-apple-system,BlinkMacSystemFont,'PingFang SC','Microsoft YaHei','Helvetica Neue',sans-serif}" +
    ".btn{position:fixed;bottom:20px;" + side + ":20px;width:56px;height:56px;border-radius:50%;border:0;background:var(--c);color:#fff;" +
    "cursor:pointer;box-shadow:0 4px 16px rgba(0,0,0,.25);display:flex;align-items:center;justify-content:center;z-index:2147483646}" +
    ".btn svg{width:26px;height:26px}" +
    ".panel{position:fixed;bottom:88px;" + side + ":20px;width:380px;height:560px;max-height:calc(100vh - 110px);display:none;flex-direction:column;" +
    "background:var(--bg);color:var(--fg);border:1px solid var(--bd);border-radius:14px;overflow:hidden;box-shadow:0 12px 40px rgba(0,0,0,.25);z-index:2147483647}" +
    ".panel.open{display:flex}" +
    ".head{display:flex;align-items:center;justify-content:space-between;padding:12px 14px;background:var(--c);color:#fff;font-size:15px;font-weight:600}" +
    ".head button{background:none;border:0;color:#fff;font-size:22px;line-height:1;cursor:pointer;padding:0 4px}" +
    ".log{flex:1;overflow-y:auto;padding:14px;font-size:14px;line-height:1.65}" +
    ".msg{margin-bottom:12px;display:flex}" +
    ".msg .b{max-width:88%;padding:8px 12px;border-radius:12px;white-space:pre-wrap;word-break:break-word;background:var(--bot)}" +
    ".msg.user{justify-content:flex-end}.msg.user .b{background:var(--c);color:#fff}" +
    ".msg.err .b{color:#dc2626}" +
    ".msg.err .b a{color:var(--link);margin-left:6px}" +
    ".cost{margin-top:6px;font-size:11px;color:var(--mut)}" +
    ".msg.wait .b{color:var(--mut)}" +
    "sup a{color:var(--link);text-decoration:none;font-size:11px;margin:0 1px}" +
    ".src{margin-top:8px;padding-top:6px;border-top:1px solid var(--bd);font-size:12px;white-space:normal;color:var(--mut)}" +
    ".src ol{margin:4px 0 0;padding-left:20px}.src li{margin:2px 0}" +
    ".src a{color:var(--link);text-decoration:none;word-break:break-all}.src a:hover{text-decoration:underline}" +
    "form{display:flex;gap:8px;padding:10px;border-top:1px solid var(--bd);background:var(--bg)}" +
    "textarea{flex:1;resize:none;height:40px;max-height:120px;font-size:14px;padding:9px 12px;border:1px solid var(--bd);border-radius:10px;background:var(--bg);color:var(--fg);outline:none}" +
    "textarea:focus{border-color:var(--c)}" +
    "form button{border:0;border-radius:10px;padding:0 16px;background:var(--c);color:#fff;font-size:14px;cursor:pointer}" +
    "form button:disabled{opacity:.5;cursor:default}" +
    ":host{--c:" + color + ";--bg:#fff;--fg:#1f2328;--bd:#e5e7eb;--bot:#f3f4f6;--mut:#6b7280;--link:#2563eb}" +
    "@media (prefers-color-scheme:dark){:host{--bg:#111827;--fg:#e5e7eb;--bd:#374151;--bot:#1f2937;--mut:#9ca3af;--link:#60a5fa}}" +
    "@media (max-width:480px){.panel{left:0;right:0;bottom:0;top:0;width:100%;height:100%;max-height:none;border-radius:0;border:0}" +
    ".panel.open~.btn{display:none}}";

  var host = document.createElement("div");
  host.setAttribute("data-search-chat", "");
  var root = host.attachShadow({ mode: "open" });
  root.innerHTML =
    "<style>" + css + "</style>" +
    '<div class="panel" role="dialog" aria-label="聊天">' +
    '<div class="head"><span class="t"></span><button type="button" class="x" aria-label="关闭">×</button></div>' +
    '<div class="log" aria-live="polite"></div>' +
    '<form><textarea rows="1" placeholder="输入问题，回车发送" aria-label="输入问题"></textarea><button type="submit">发送</button></form>' +
    "</div>" +
    '<button type="button" class="btn" aria-label="打开聊天">' +
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg></button>';

  var panel = root.querySelector(".panel");
  var log = root.querySelector(".log");
  var form = root.querySelector("form");
  var input = root.querySelector("textarea");
  var send = root.querySelector("form button");
  root.querySelector(".t").textContent = title;

  var history = []; // 只存问题和最终回答，每次发给服务器

  function toggle(open) {
    panel.classList.toggle("open", open);
    if (open) input.focus();
  }
  root.querySelector(".btn").addEventListener("click", function () { toggle(!panel.classList.contains("open")); });
  root.querySelector(".x").addEventListener("click", function () { toggle(false); });

  function bubble(cls) {
    var row = document.createElement("div");
    row.className = "msg " + (cls || "");
    var b = document.createElement("div");
    b.className = "b";
    row.appendChild(b);
    log.appendChild(row);
    log.scrollTop = log.scrollHeight;
    return row;
  }

  function safeURL(u) {
    try {
      var p = new URL(u, location.href); // 允许 /login 这样的相对地址
      return p.protocol === "http:" || p.protocol === "https:" ? p.href : null;
    } catch (e) { return null; }
  }

  // 把回答里的 [1][2] 变成可点击的上标链接。全部用 DOM 节点生成，不拼 HTML，防止 XSS。
  function renderAnswer(b, answer, sources) {
    var byN = {};
    sources.forEach(function (s) { byN[s.n] = s; });
    var re = /\[(\d+)\]/g, last = 0, m;
    while ((m = re.exec(answer))) {
      b.appendChild(document.createTextNode(answer.slice(last, m.index)));
      var s = byN[m[1]], href = s && safeURL(s.url);
      if (href) {
        var sup = document.createElement("sup"), a = document.createElement("a");
        a.href = href; a.target = "_blank"; a.rel = "noopener noreferrer nofollow";
        a.title = s.title; a.textContent = "[" + m[1] + "]";
        sup.appendChild(a); b.appendChild(sup);
      } else {
        b.appendChild(document.createTextNode(m[0]));
      }
      last = re.lastIndex;
    }
    b.appendChild(document.createTextNode(answer.slice(last)));

    if (sources.length) {
      var box = document.createElement("div");
      box.className = "src";
      box.appendChild(document.createTextNode("来源："));
      var ol = document.createElement("ol");
      sources.forEach(function (s) {
        var href = safeURL(s.url);
        if (!href) return;
        var li = document.createElement("li"), a = document.createElement("a");
        li.value = s.n;
        a.href = href; a.target = "_blank"; a.rel = "noopener noreferrer nofollow";
        a.textContent = s.title || href;
        li.appendChild(a); ol.appendChild(li);
      });
      box.appendChild(ol);
      b.appendChild(box);
    }
  }

  function linkError(msg, href, linkText) {
    var e = new Error(msg);
    e.plain = true; e.href = href; e.linkText = linkText;
    return e;
  }

  bubble().firstChild.textContent = greeting;

  input.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      form.requestSubmit ? form.requestSubmit() : form.dispatchEvent(new Event("submit", { cancelable: true }));
    }
  });

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var q = input.value.trim();
    if (!q || send.disabled) return;
    input.value = "";
    send.disabled = true;
    bubble("user").firstChild.textContent = q;
    var wait = bubble("wait");
    wait.firstChild.textContent = "搜索、思考中……";

    var headers = { "Content-Type": "application/json", "X-Request-ID": newRequestID() };
    if (token) headers["X-Chat-Token"] = token;
    var bearer = currentToken();
    if (bearer) headers["Authorization"] = "Bearer " + bearer;
    fetch(endpoint, {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ messages: history.concat([{ role: "user", content: q }]) }),
    })
      .then(function (resp) {
        return resp.json().catch(function () { return {}; }).then(function (data) {
          // 访问口令（data-token）错误也是 401，靠 code 区分
          if (resp.status === 401 && (data.code === "login_required" || !data.error)) throw linkError("请先登录", loginURL, "去登录");
          if (resp.status === 402) throw linkError("余额不足", rechargeURL, "去充值");
          if (!resp.ok) throw new Error(data.error || "HTTP " + resp.status);
          return data;
        });
      })
      .then(function (data) {
        wait.className = "msg";
        wait.firstChild.textContent = "";
        renderAnswer(wait.firstChild, data.answer || "", data.sources || []);
        if (typeof data.cost === "number") {
          var cost = document.createElement("div");
          cost.className = "cost";
          cost.textContent = "本次消耗 " + data.cost + " 积分";
          wait.firstChild.appendChild(cost);
        }
        history.push({ role: "user", content: q }, { role: "assistant", content: data.answer || "" });
        if (history.length > 20) history = history.slice(-20);
      })
      .catch(function (err) {
        wait.className = "msg err";
        if (err.plain) { // 401 / 402：不加「出错了」，有链接就附上
          wait.firstChild.textContent = err.message;
          var href = err.href && safeURL(err.href);
          if (href) {
            var a = document.createElement("a");
            a.href = href; a.textContent = err.linkText;
            wait.firstChild.appendChild(a);
          }
          return;
        }
        // 浏览器因 CORS 拦截时只会给出 Failed to fetch
        wait.firstChild.textContent = "出错了：" + (err.message === "Failed to fetch" ? "连不上聊天服务（网络或跨域限制）" : err.message);
      })
      .then(function () {
        send.disabled = false;
        input.focus();
        log.scrollTop = log.scrollHeight;
      });
  });

  function mount() { document.body.appendChild(host); }
  document.body ? mount() : document.addEventListener("DOMContentLoaded", mount);
})();
