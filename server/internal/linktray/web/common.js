// What both pages share: talking to the tray (Go) over WebView2's message channel, the theme, icons and building
// elements. The tray puts BOOT (labels, glyphs, theme, first data) into the page before it loads.
"use strict";

const lt = (() => {
  const boot = JSON.parse(document.getElementById("boot").textContent);
  const handlers = {};
  const pending = new Map();
  let nextID = 1;

  // send asks the tray to do something; the promise settles with its reply.
  function send(cmd, args) {
    const id = nextID++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      if (window.chrome && window.chrome.webview) {
        window.chrome.webview.postMessage(JSON.stringify({ id, cmd, args: args || {} }));
      } else {
        // Opened outside the tray (a browser, a test): nothing answers.
        pending.delete(id);
        reject(new Error("no tray"));
      }
    });
  }

  // tell is send without waiting for an answer.
  function tell(cmd, args) {
    send(cmd, args).catch(() => {});
  }

  // receive is what the tray calls with each message.
  function receive(msg) {
    if (msg.type === "reply") {
      const p = pending.get(msg.id);
      if (!p) return;
      pending.delete(msg.id);
      if (msg.error) p.reject(new Error(msg.error));
      else p.resolve(msg.data);
      return;
    }
    if (msg.type === "theme") {
      applyTheme(msg.theme);
      return;
    }
    const h = handlers[msg.type];
    if (h) h(msg);
  }

  function on(type, fn) {
    handlers[type] = fn;
  }

  function applyTheme(t) {
    const root = document.documentElement;
    root.dataset.theme = t.dark ? "dark" : "light";
    document.body.classList.toggle("solid", !t.backdrop);
    if (t.accent) {
      root.style.setProperty("--accent", t.accent);
      root.style.setProperty("--accent-hover", t.accent_hover);
      root.style.setProperty("--accent-text", t.accent_text);
      root.style.setProperty("--on-accent", t.on_accent);
    }
  }

  const svgNS = "http://www.w3.org/2000/svg";

  // icon is a glyph from the tray's set (linktray.Glyphs).
  function icon(name, extra) {
    const svg = document.createElementNS(svgNS, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("class", "icon" + (extra ? " " + extra : ""));
    const path = document.createElementNS(svgNS, "path");
    path.setAttribute("d", boot.glyphs[name] || boot.glyphs.info);
    svg.appendChild(path);
    return svg;
  }

  // el builds an element: el("div", {class: "x", onclick: f}, child, "text", …).
  function el(tag, attrs, ...children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
      else if (k === "class") e.className = v;
      else if (k === "text") e.textContent = v;
      else e.setAttribute(k, v === true ? "" : v);
    }
    for (const c of children.flat()) {
      if (c === null || c === undefined || c === false) continue;
      e.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    }
    return e;
  }

  // line is a status line from linktray.Line.
  function line(l) {
    const tone = l.tone ? " tone-" + l.tone : "";
    return el("div", { class: "line" + (l.primary ? " primary" : "") + tone },
      icon(l.icon), el("div", { class: "text selectable", text: l.text }));
  }

  // infobar is a notice: tone, icon, then the body's nodes.
  function infobar(tone, iconName, ...body) {
    return el("div", { class: "infobar tone-" + tone, role: tone === "danger" ? "alert" : "status" },
      icon(iconName), el("div", { class: "body" }, ...body));
  }

  // watchSize tells the tray how tall the content is, so the window fits it.
  function watchSize(node) {
    let last = 0;
    new ResizeObserver(() => {
      const h = Math.ceil(node.getBoundingClientRect().height);
      if (h !== last) {
        last = h;
        tell("size", { height: h });
      }
    }).observe(node);
  }

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") tell("escape");
  });
  // No browser context menu, no zoom, no dragging pictures out: this is a window, not a page.
  document.addEventListener("contextmenu", (e) => {
    if (!e.target.closest("input, textarea, .selectable")) e.preventDefault();
  });
  document.addEventListener("wheel", (e) => { if (e.ctrlKey) e.preventDefault(); }, { passive: false });
  document.addEventListener("dragstart", (e) => e.preventDefault());

  applyTheme(boot.theme);
  window.__lt = receive;
  return { boot, send, tell, on, icon, el, line, infobar, watchSize, labels: boot.labels };
})();
