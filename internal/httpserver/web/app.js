"use strict";

// 主播录播归档 · 前端 SPA(纯 JS,hash 路由)。
// 鉴权门:加载时查 /api/whoami,未登录显示登录视图;受保护接口返回 401 时回到登录。

const appEl = document.getElementById("app");
let nav = 0; // 导航代次,用于取消旧的轮询/异步渲染

// ---- 工具 ----
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
function fmtDate(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d)) return "—";
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function fmtSize(b) {
  if (!b) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, n = b;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}
function fmtDur(s) {
  if (!s) return "";
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  const p = (n) => String(n).padStart(2, "0");
  return h ? `${h}:${p(m)}:${p(sec)}` : `${m}:${p(sec)}`;
}

class AuthError extends Error {}

async function api(path, opts) {
  const res = await fetch(path, Object.assign({ credentials: "same-origin" }, opts || {}));
  if (res.status === 401) { renderLogin(); throw new AuthError(); }
  return res;
}

// ---- 顶栏 ----
function topbar(crumbHtml) {
  return `<header class="topbar">
    <h1><a href="#/">主播录播归档</a></h1>
    <span class="crumb">${crumbHtml || ""}</span>
    <span class="spacer"></span>
    <a class="topnav" href="#/admin">Telegram 登录</a>
    <button id="logoutBtn">退出登录</button>
  </header>`;
}
function wireTopbar() {
  const b = document.getElementById("logoutBtn");
  if (b) b.onclick = async () => {
    try { await api("/api/logout", { method: "POST" }); } catch (e) {}
    renderLogin();
  };
}

// ---- 登录视图 ----
function renderLogin() {
  nav++;
  appEl.innerHTML = `<div class="login-wrap"><div class="card">
    <h1>访问密码</h1>
    <form id="lf">
      <input id="pw" type="password" autocomplete="current-password" autofocus required>
      <button class="primary" id="lb" type="submit">登录</button>
      <div id="lm" class="msg err"></div>
    </form>
  </div></div>`;
  const f = document.getElementById("lf"), pw = document.getElementById("pw");
  const b = document.getElementById("lb"), m = document.getElementById("lm");
  f.onsubmit = async (e) => {
    e.preventDefault();
    b.disabled = true; m.textContent = ""; m.className = "msg err";
    try {
      const res = await fetch("/api/login", {
        method: "POST", headers: { "Content-Type": "application/json" },
        credentials: "same-origin", body: JSON.stringify({ password: pw.value }),
      });
      if (res.ok) { m.className = "msg ok"; m.textContent = "登录成功"; route(true); return; }
      if (res.status === 429) {
        const d = await res.json().catch(() => ({}));
        m.textContent = `失败次数过多,请 ${Math.ceil((d.retry_after_ms || 0) / 1000)} 秒后再试`;
      } else { m.textContent = "密码错误"; }
    } catch (err) { m.textContent = "网络错误"; }
    finally { b.disabled = false; }
  };
}

// ---- 主播网格 ----
async function renderGrid() {
  const gen = ++nav;
  appEl.innerHTML = topbar("") + `<main><div class="loading"><span class="spin"></span>加载中…</div></main>`;
  wireTopbar();
  let data;
  try { data = await (await api("/api/streamers")).json(); }
  catch (e) { if (e instanceof AuthError) return; appEl.innerHTML = topbar("") + `<main><div class="empty">加载失败</div></main>`; wireTopbar(); return; }
  if (gen !== nav) return;
  const list = (data && data.streamers) || [];
  const main = document.querySelector("main");
  if (!list.length) { main.innerHTML = `<div class="empty">还没有任何录播。同步入库后会出现在这里。</div>`; return; }
  main.innerHTML = `<div class="grid">` + list.map((s) => `
    <div class="tile" data-s="${esc(s.streamer)}">
      <div class="thumb">${s.has_thumb ? "" : "🎬"}</div>
      <div class="meta">
        <div class="name">${esc(s.streamer)}</div>
        <div class="sub">${s.count} 个录播 · ${fmtDate(s.latest_at)}</div>
      </div>
    </div>`).join("") + `</div>`;
  main.querySelectorAll(".tile").forEach((el) => {
    el.onclick = () => { location.hash = "#/s/" + encodeURIComponent(el.dataset.s); };
  });
}

// ---- 时间线 ----
async function renderTimeline(streamer) {
  const gen = ++nav;
  appEl.innerHTML = topbar(`<a href="#/">主播</a> / ${esc(streamer)}`) +
    `<main><div class="loading"><span class="spin"></span>加载中…</div></main>`;
  wireTopbar();
  let data;
  try { data = await (await api("/api/timeline?streamer=" + encodeURIComponent(streamer))).json(); }
  catch (e) { if (e instanceof AuthError) return; document.querySelector("main").innerHTML = `<div class="empty">加载失败</div>`; return; }
  if (gen !== nav) return;
  const items = (data && data.items) || [];
  const main = document.querySelector("main");
  if (!items.length) { main.innerHTML = `<div class="empty">该主播暂无录播</div>`; return; }
  main.innerHTML = `<ul class="timeline">` + items.map((m) => {
    const pass = m.play_mode === "passthrough";
    return `<li data-t="${esc(m.stream_token)}">
      <span class="when">${fmtDate(m.recorded_at)}</span>
      <span class="fn">${esc(m.file_name)}</span>
      ${m.duration_sec ? `<span class="badge">${fmtDur(m.duration_sec)}</span>` : ""}
      <span class="badge">${fmtSize(m.file_size)}</span>
      <span class="badge ${pass ? "pass" : ""}">${esc(m.play_mode || m.status)}</span>
    </li>`;
  }).join("") + `</ul>`;
  main.querySelectorAll("li").forEach((el) => {
    el.onclick = () => { location.hash = "#/play/" + encodeURIComponent(el.dataset.t); };
  });
}

// ---- 播放页 ----
async function renderPlay(token) {
  const gen = ++nav;
  appEl.innerHTML = topbar(`<a href="#/">主播</a> / 播放`) +
    `<main><div class="loading"><span class="spin"></span>加载中…</div></main>`;
  wireTopbar();
  let m;
  try { m = await (await api("/api/media/" + encodeURIComponent(token))).json(); }
  catch (e) { if (e instanceof AuthError) return; document.querySelector("main").innerHTML = `<div class="empty">未找到该录播</div>`; return; }
  if (gen !== nav) return;

  const main = document.querySelector("main");
  main.innerHTML = `<div class="player">
    <video id="v" controls playsinline></video>
    <div class="info">
      <div>${esc(m.file_name)}</div>
      <div>录制时间:${fmtDate(m.recorded_at)} · 大小:${fmtSize(m.file_size)}${m.duration_sec ? " · 时长:" + fmtDur(m.duration_sec) : ""}</div>
      <div>播放模式:${esc(m.play_mode || "未探测")} · 缓存:${esc(m.cache_state)}</div>
    </div>
    <div id="pn"></div>
  </div>`;
  preparePlayback(token, gen);
}

// 按 §13.4 换签契约:换签 -> ready 直接播;202 则轮询 status 后重试。
async function preparePlayback(token, gen) {
  const v = document.getElementById("v");
  const pn = document.getElementById("pn");
  const notice = (cls, html) => { if (pn) pn.innerHTML = `<div class="notice ${cls}">${html}</div>`; };
  try {
    const res = await api("/api/media/" + encodeURIComponent(token) + "/play-url");
    if (gen !== nav) return;
    if (res.status === 200) {
      const d = await res.json();
      if (d.ready && d.url) { v.src = d.url; notice("", "就绪,点击播放。"); return; }
    }
    if (res.status === 202) {
      notice("", `<span class="spin"></span>正在准备(remux/转码),自动重试中…`);
      setTimeout(() => { if (gen === nav) pollStatus(token, gen); }, 2000);
      return;
    }
    if (res.status === 404) {
      notice("warn", "播放换签接口尚未实现(Phase 4)。当前已能浏览目录与元数据;接入 broker 与缓存播放后即可在线播放。");
      return;
    }
    notice("warn", "无法获取播放地址(HTTP " + res.status + ")。");
  } catch (e) {
    if (e instanceof AuthError) return;
    notice("warn", "播放链路尚未就绪(Phase 4)。");
  }
}

async function pollStatus(token, gen) {
  try {
    const d = await (await api("/api/media/" + encodeURIComponent(token) + "/status")).json();
    if (gen !== nav) return;
    if (d.cache_state === "ready") { preparePlayback(token, gen); return; }
    if (d.cache_state === "failed") {
      document.getElementById("pn").innerHTML = `<div class="notice warn">准备失败:${esc(d.last_error || "未知错误")}</div>`;
      return;
    }
    setTimeout(() => { if (gen === nav) pollStatus(token, gen); }, 2000);
  } catch (e) { if (!(e instanceof AuthError)) setTimeout(() => { if (gen === nav) pollStatus(token, gen); }, 4000); }
}

// ---- 后台:Telegram 登录引导(§14)----
async function renderAdmin() {
  const gen = ++nav;
  appEl.innerHTML = topbar("Telegram 登录") +
    `<main><div class="loading"><span class="spin"></span>读取状态…</div></main>`;
  wireTopbar();
  let st;
  try {
    const res = await api("/admin/tdl-status");
    if (res.status === 503) { adminMain(`<div class="notice warn">broker 未配置或未运行,无法管理 Telegram 登录。</div>`); return; }
    st = await res.json();
  } catch (e) {
    if (e instanceof AuthError) return;
    adminMain(`<div class="notice warn">读取状态失败。</div>`); return;
  }
  if (gen !== nav) return;
  if (st.logged_in) return adminLoggedIn(st.phone);
  adminWizard();
}

function adminMain(html) {
  const m = document.querySelector("main");
  if (m) m.innerHTML = `<div class="card admincard">${html}</div>`;
}

function adminLoggedIn(phone) {
  adminMain(`<h1>Telegram 已登录</h1>
    <div class="info">账号:${esc(phone || "(未知)")}</div>
    <button class="primary" id="lo">注销 Telegram</button>
    <div id="am" class="msg"></div>`);
  document.getElementById("lo").onclick = async () => {
    if (!confirm("确定注销 Telegram?注销后需重新登录才能同步/播放。")) return;
    try {
      const res = await api("/admin/tdl-logout", { method: "POST" });
      if (res.ok) renderAdmin();
    } catch (e) {}
  };
}

function adminWizard() {
  // step: phone -> code -> (password) -> done
  let stepToken = "";
  adminMain(`<h1>登录 Telegram</h1>
    <div id="aw"></div>
    <div id="am" class="msg err"></div>`);
  const aw = document.getElementById("aw"), am = document.getElementById("am");
  const setMsg = (t, ok) => { am.className = "msg " + (ok ? "ok" : "err"); am.textContent = t || ""; };

  const phoneStep = () => {
    aw.innerHTML = `<input id="ph" placeholder="手机号(含国家码,如 +8613...)" autofocus>
      <button class="primary" id="go">获取验证码</button>`;
    document.getElementById("go").onclick = async () => {
      const phone = document.getElementById("ph").value.trim();
      if (!phone) return setMsg("请输入手机号");
      setMsg("");
      try {
        const res = await api("/admin/tdl-send-code", { method: "POST",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ phone }) });
        const d = await res.json();
        if (!res.ok) return setMsg(d.error || "发送失败");
        stepToken = d.step_token; codeStep();
      } catch (e) { if (!(e instanceof AuthError)) setMsg("网络错误"); }
    };
  };
  const codeStep = () => {
    aw.innerHTML = `<input id="cd" placeholder="短信验证码" autofocus>
      <button class="primary" id="go">登录</button>`;
    setMsg("验证码已发送", true);
    document.getElementById("go").onclick = async () => {
      const code = document.getElementById("cd").value.trim();
      if (!code) return setMsg("请输入验证码");
      setMsg("");
      try {
        const res = await api("/admin/tdl-sign-in", { method: "POST",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ step_token: stepToken, code }) });
        const d = await res.json();
        if (!res.ok) return setMsg(d.error || "登录失败");
        if (d.need_password) return pwdStep();
        renderAdmin();
      } catch (e) { if (!(e instanceof AuthError)) setMsg("网络错误"); }
    };
  };
  const pwdStep = () => {
    aw.innerHTML = `<input id="pwd" type="password" placeholder="两步验证密码(2FA)" autofocus>
      <button class="primary" id="go">完成登录</button>`;
    setMsg("该账号开启了两步验证,请输入密码", true);
    document.getElementById("go").onclick = async () => {
      const password = document.getElementById("pwd").value;
      if (!password) return setMsg("请输入 2FA 密码");
      setMsg("");
      try {
        const res = await api("/admin/tdl-check-password", { method: "POST",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ step_token: stepToken, password }) });
        const d = await res.json();
        if (!res.ok) return setMsg(d.error || "密码错误");
        renderAdmin();
      } catch (e) { if (!(e instanceof AuthError)) setMsg("网络错误"); }
    };
  };
  phoneStep();
}

// ---- 路由 ----
function route(authed) {
  const h = location.hash || "#/";
  if (h.startsWith("#/admin")) return renderAdmin();
  if (h.startsWith("#/s/")) return renderTimeline(decodeURIComponent(h.slice(4)));
  if (h.startsWith("#/play/")) return renderPlay(decodeURIComponent(h.slice(7)));
  return renderGrid();
}

async function boot() {
  try {
    const res = await fetch("/api/whoami", { credentials: "same-origin" });
    if (res.status === 401) { renderLogin(); return; }
  } catch (e) { /* 网络错误也先尝试登录视图 */ renderLogin(); return; }
  route(true);
}

window.addEventListener("hashchange", () => route(true));
boot();
