// The pairing window: active mode takes the link from the phone or the web, passive mode makes one for the hub's
// admin page. The tray checks links and does the pairing (linktray.ParseLink, Host.Pair); this draws and asks.
"use strict";

(() => {
  const { el, icon, line, infobar, send, labels: L } = lt;
  const $ = (id) => document.getElementById(id);
  let state = lt.boot.pair;
  let tab = lt.boot.tab || (state.mode === "passive" ? "passive" : "active");

  // What the user typed survives a redraw (the tray pushes new state when link.json changes).
  const initPreview = lt.boot.preview || null;
  const initTarget = state.ssh ? state.ssh.target : (initPreview && initPreview.default_ssh_target ? initPreview.default_ssh_target : "");
  const initAlways = state.ssh ? Boolean(state.ssh.always) : Boolean(initPreview && initPreview.default_ssh_always);
  const form = { link: lt.boot.prefill || "", machine: "", advanced: false, preview: initPreview, result: null, busy: false,
    sshOpen: Boolean(state.ssh), sshServer: state.ssh ? state.ssh.server : "", sshTarget: initTarget,
    sshTargetTouched: Boolean(state.ssh), sshAlways: initAlways, sshAlwaysTouched: Boolean(state.ssh),
    sshDirtyUser: false, sshTesting: false, sshSaving: false, sshNotice: null,
    listen: state.listen, invitation: null, passiveError: "", generating: false, newAddress: "", addressError: "",
    copied: "" };

  function tabs() {
    const make = (id, text) => el("button", {
      class: "tab", role: "tab", "aria-selected": String(tab === id), "aria-controls": id,
      onclick: () => { tab = id; draw(); },
    }, text);
    $("tabs").replaceChildren(make("active", L.mode_active), make("passive", L.mode_passive));
    $("active").hidden = tab !== "active";
    $("passive").hidden = tab !== "passive";
  }

  function switchBack(text, mode) {
    if (!text) return null;
    return infobar("info", "info", el("span", { text }),
      el("div", {}, el("button", { class: "button", onclick: () => setMode(mode) }, L.switch_button)));
  }

  async function setMode(mode) {
    try {
      await send("set_mode", { mode });
    } catch (err) {
      form.result = { ok: false, text: err.message };
      draw();
    }
  }

  let parseTimer = 0;
  function onLink(value) {
    form.link = value;
    form.result = null;
    clearTimeout(parseTimer);
    parseTimer = setTimeout(async () => {
      form.preview = value.trim() ? await send("parse_link", { raw: value }).catch(() => null) : null;
      if (form.preview && form.preview.kind === "ok" && !state.ssh) {
        if (!form.sshTargetTouched) form.sshTarget = form.preview.default_ssh_target || "";
        if (!form.sshAlwaysTouched) form.sshAlways = Boolean(form.preview.default_ssh_always);
      }
      drawActive();
    }, 120);
  }

  function currentSSH() {
    const server = form.sshServer.trim();
    if (!server) return null;
    return { server, target: form.sshTarget.trim(), always: Boolean(form.sshAlways) };
  }

  function defaultTargetPlaceholder() {
    if (form.preview && form.preview.default_ssh_target) return form.preview.default_ssh_target;
    if (state.default_ssh_target) return state.default_ssh_target;
    return L.ssh_target_placeholder;
  }

  function sshChanged() {
    const cur = currentSSH();
    const saved = state.ssh || null;
    if (!cur && !saved) return false;
    if (!cur || !saved) return Boolean(cur);
    const curTarget = cur.target || defaultTargetPlaceholder();
    return cur.server !== saved.server || curTarget !== saved.target || Boolean(cur.always) !== Boolean(saved.always);
  }

  async function pair() {
    form.busy = true;
    form.result = null;
    drawActive();
    try {
      const r = await send("pair", { raw: form.link, machine: form.machine, ssh: currentSSH() });
      form.result = { ok: true, text: r.text };
      form.link = "";
      form.preview = null;
      form.sshDirtyUser = false;
    } catch (err) {
      form.result = { ok: false, text: err.message };
    }
    form.busy = false;
    drawActive();
  }

  async function testSSH() {
    const ssh = currentSSH();
    if (!ssh || form.sshTesting || form.sshSaving) return;
    form.sshTesting = true;
    form.sshNotice = null;
    drawActive();
    try {
      const r = await send("ssh_test", { ssh, raw: form.link });
      if (r && r.ssh && !form.sshTarget.trim()) form.sshTarget = r.ssh.target;
      form.sshNotice = { ok: true, text: r.text };
    } catch (err) {
      form.sshNotice = { ok: false, text: err.message };
    }
    form.sshTesting = false;
    drawActive();
  }

  async function saveSSH() {
    const ssh = currentSSH();
    if (!ssh || form.sshSaving || form.sshTesting) return;
    form.sshSaving = true;
    form.sshNotice = null;
    drawActive();
    try {
      const r = await send("ssh_set", { ssh });
      if (r && r.ssh) {
        form.sshServer = r.ssh.server;
        form.sshTarget = r.ssh.target;
        form.sshAlways = Boolean(r.ssh.always);
        form.sshTargetTouched = true;
        form.sshAlwaysTouched = true;
      }
      form.sshDirtyUser = false;
      form.sshNotice = { ok: true, text: r.text };
    } catch (err) {
      form.sshNotice = { ok: false, text: err.message };
    }
    form.sshSaving = false;
    drawActive();
  }

  async function removeSSH() {
    if (form.sshSaving || form.sshTesting) return;
    form.sshSaving = true;
    form.sshNotice = null;
    drawActive();
    try {
      const r = await send("ssh_set", { ssh: null });
      form.sshServer = "";
      form.sshTarget = defaultTargetPlaceholder();
      form.sshTargetTouched = false;
      form.sshAlways = false;
      form.sshAlwaysTouched = false;
      form.sshDirtyUser = false;
      form.sshNotice = { ok: true, text: r.text };
    } catch (err) {
      form.sshNotice = { ok: false, text: err.message };
    }
    form.sshSaving = false;
    drawActive();
  }

  function sshSection() {
    const serverInput = el("input", {
      class: "textbox code", id: "ssh-server-input", type: "text", placeholder: L.ssh_server_placeholder,
      spellcheck: "false", autocomplete: "off", value: form.sshServer,
      oninput: (e) => { form.sshServer = e.target.value; form.sshDirtyUser = true; form.sshNotice = null; updateSSHButtons(); },
    });
    const targetInput = el("input", {
      class: "textbox code", id: "ssh-target-input", type: "text", placeholder: defaultTargetPlaceholder(),
      spellcheck: "false", autocomplete: "off", value: form.sshTarget,
      oninput: (e) => { form.sshTarget = e.target.value; form.sshTargetTouched = true; form.sshDirtyUser = true; form.sshNotice = null; updateSSHButtons(); },
    });
    const testBtn = el("button", {
      class: "button", id: "ssh-test-btn",
      disabled: !form.sshServer.trim() || form.sshTesting || form.sshSaving,
      onclick: testSSH,
    }, form.sshTesting ? L.ssh_testing : L.ssh_test);
    const removeBtn = state.ssh ? el("button", {
      class: "button subtle", disabled: form.sshSaving || form.sshTesting, onclick: removeSSH,
    }, L.ssh_remove) : null;
    const saveBtn = state.active_paired ? el("button", {
      class: "button accent", id: "ssh-save-btn",
      disabled: !sshChanged() || !form.sshServer.trim() || form.sshSaving || form.sshTesting,
      onclick: saveSSH,
    }, form.sshSaving ? L.ssh_saving : L.ssh_save) : null;
    function updateSSHButtons() {
      testBtn.disabled = !form.sshServer.trim() || form.sshTesting || form.sshSaving;
      if (saveBtn) saveBtn.disabled = !sshChanged() || !form.sshServer.trim() || form.sshSaving || form.sshTesting;
    }
    const switchBtn = el("button", {
      class: "row", role: "switch", "aria-checked": String(form.sshAlways),
      onclick: () => {
        form.sshAlways = !form.sshAlways;
        form.sshAlwaysTouched = true;
        form.sshDirtyUser = true;
        form.sshNotice = null;
        drawActive();
      },
    }, el("span", { class: "label", text: L.ssh_always_label }), el("span", { class: "switch" }));

    return el("details", { open: form.sshOpen, ontoggle: (e) => { form.sshOpen = e.target.open; } },
      el("summary", {}, icon("chevron"), el("span", { text: L.ssh_title })),
      el("div", { class: "card stack ssh-body" },
        el("div", { class: "caption secondary", text: L.ssh_intro }),
        el("label", { class: "caption secondary", for: "ssh-server-input", text: L.ssh_server_label }),
        serverInput,
        el("label", { class: "caption secondary", for: "ssh-target-input", text: L.ssh_target_label }),
        targetInput,
        switchBtn,
        el("div", { class: "caption tertiary", text: L.ssh_always_hint }),
        el("div", { class: "ssh-actions" }, testBtn, el("span", { class: "spacer" }), removeBtn, saveBtn),
        form.sshNotice
          ? (form.sshNotice.ok
              ? infobar("positive", "check_filled", el("span", { class: "selectable", text: form.sshNotice.text }))
              : infobar("danger", "octagon", el("span", { class: "selectable", text: form.sshNotice.text })))
          : null));
  }

  function drawActive() {
    const p = form.preview;
    const nodes = [
      el("h1", { class: "subtitle", text: state.active_paired ? L.pair_title_again : L.pair_title_new }),
      switchBack(state.switch_to_active, "active"),
    ];
    if (p && p.notice) nodes.push(infobar("caution", "warning", el("span", { text: p.notice })));
    nodes.push(el("div", { class: "secondary", text: L.active_intro }));
    const input = el("input", {
      class: "textbox code", type: "text", placeholder: "persona://link?…", spellcheck: "false", autocomplete: "off",
      value: form.link, oninput: (e) => onLink(e.target.value),
      onkeydown: (e) => { if (e.key === "Enter" && canPair()) pair(); },
    });
    nodes.push(input);
    if (p && p.kind === "ok") nodes.push(el("div", { class: "card lines" }, p.lines.map(line)));
    if (!(p && p.kind === "ok" && p.machine)) {
      const details = el("details", { open: form.advanced, ontoggle: (e) => { form.advanced = e.target.open; } },
        el("summary", {}, icon("chevron"), el("span", { text: L.advanced })),
        el("input", { class: "textbox", type: "text", placeholder: L.machine_placeholder, spellcheck: "false",
          value: form.machine, oninput: (e) => { form.machine = e.target.value; } }));
      nodes.push(details);
    }
    nodes.push(sshSection());
    nodes.push(el("div", { class: "end" }, el("button", {
      class: "button accent", disabled: !canPair(), onclick: pair,
    }, form.busy ? L.pairing : L.pair_button)));
    if (form.result) {
      nodes.push(form.result.ok
        ? infobar("positive", "check_filled", el("span", { class: "selectable", text: form.result.text }))
        : infobar("danger", "octagon", el("span", { class: "selectable", text: form.result.text })));
    }
    nodes.push(addresses());
    const focusedId = document.activeElement ? document.activeElement.id : "";
    const focusedLink = document.activeElement && document.activeElement.classList.contains("code") && !focusedId;
    $("active").replaceChildren(...nodes.filter(Boolean));
    if (focusedLink) {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    } else if (focusedId) {
      const next = $(focusedId);
      if (next && typeof next.focus === "function") {
        next.focus();
        if (typeof next.setSelectionRange === "function") next.setSelectionRange(next.value.length, next.value.length);
      }
    }
  }

  function canPair() {
    return !form.busy && form.link.trim() !== "" && !(form.preview && (form.preview.kind === "other_app" || form.preview.kind === "incomplete"));
  }

  function addresses() {
    const rows = state.addresses.map((a) => el("div", { class: "address" },
      icon("network"),
      el("span", { class: "text selectable", text: a.text }),
      a.in_use ? el("span", { class: "in-use caption", text: L.address_in_use }) : null,
      a.manual ? el("button", { class: "button subtle", onclick: () => removeAddress(a.url) }, L.address_remove) : null));
    const add = el("div", { class: "field-row" },
      el("input", { class: "textbox", type: "text", placeholder: L.address_add_placeholder, spellcheck: "false",
        value: form.newAddress, disabled: !state.active_paired,
        oninput: (e) => { form.newAddress = e.target.value; },
        onkeydown: (e) => { if (e.key === "Enter") addAddress(); } }),
      el("button", { class: "button", disabled: !state.active_paired, onclick: addAddress }, L.address_add));
    return el("div", { class: "group" },
      el("div", { class: "strong", text: L.addresses_title }),
      el("div", { class: "caption secondary", text: L.addresses_hint }),
      state.system_trust ? el("div", { class: "line" }, icon("shield"), el("div", { class: "text", text: L.addresses_trust })) : null,
      rows.length ? rows : el("div", { class: "secondary", text: L.addresses_empty }),
      add,
      form.addressError ? infobar("danger", "octagon", el("span", { text: form.addressError })) : null);
  }

  async function addAddress() {
    const raw = form.newAddress.trim();
    if (!raw) return;
    try {
      await send("address_add", { raw });
      form.newAddress = "";
      form.addressError = "";
    } catch (err) {
      form.addressError = err.message;
    }
    drawActive();
  }

  async function removeAddress(url) {
    try {
      await send("address_remove", { url });
      form.addressError = "";
    } catch (err) {
      form.addressError = err.message;
    }
    drawActive();
  }

  async function generate() {
    form.generating = true;
    form.passiveError = "";
    drawPassive();
    try {
      form.invitation = await send("pair_listen", { listen: form.listen });
    } catch (err) {
      form.passiveError = err.message;
    }
    form.generating = false;
    drawPassive();
  }

  function copy(text, which) {
    // In the click itself, so the page may write the clipboard.
    const area = el("textarea", { class: "offscreen" });
    area.value = text;
    document.body.appendChild(area);
    area.select();
    document.execCommand("copy");
    area.remove();
    form.copied = which;
    drawPassive();
    setTimeout(() => { form.copied = ""; drawPassive(); }, 2000);
  }

  function copyButton(text, which) {
    const done = form.copied === which;
    return el("button", { class: "button", onclick: () => copy(text, which) }, icon(done ? "check" : "copy"), done ? L.copied : L.copy);
  }

  function drawPassive() {
    const inv = form.invitation;
    const nodes = [
      el("h1", { class: "subtitle", text: L.passive_title }),
      el("div", { class: "secondary", text: L.passive_intro }),
      state.passive_text ? infobar("info", "info", el("span", { text: state.passive_text })) : null,
      switchBack(state.switch_to_passive, "passive"),
      el("div", { class: "field-row" },
        el("span", { class: "secondary", text: L.listen_label }),
        el("input", { class: "textbox code", type: "text", value: form.listen, spellcheck: "false",
          oninput: (e) => { form.listen = e.target.value; } }),
        el("button", { class: "button accent", disabled: form.generating, onclick: generate },
          form.generating ? L.generating : L.generate)),
      form.passiveError ? infobar("danger", "octagon", el("span", { class: "selectable", text: form.passiveError })) : null,
    ];
    if (inv) {
      nodes.push(el("div", { class: "card" }, el("div", { class: "stack" },
        el("div", { text: L.code_intro }),
        el("div", { class: "field-row" },
          el("input", { class: "textbox code", type: "text", readonly: true, value: inv.link, onfocus: (e) => e.target.select() }),
          copyButton(inv.link, "link")),
        el("div", { class: "caption secondary selectable", text: inv.addr_text }))));
      nodes.push(el("div", { class: "group" },
        el("div", { class: "caption secondary", text: L.firewall_hint }),
        el("div", { class: "field-row" }, el("div", { class: "code-block selectable grow", text: inv.firewall }),
          copyButton(inv.firewall, "firewall")),
        el("div", { class: "caption secondary", text: L.firewall_proxy })));
    }
    $("passive").replaceChildren(...nodes.filter(Boolean));
  }

  function draw() {
    tabs();
    drawActive();
    drawPassive();
  }

  lt.on("pair", (m) => {
    state = m.pair;
    if (!form.listen) form.listen = state.listen;
    if (!form.sshDirtyUser) {
      if (state.ssh) {
        form.sshOpen = true;
        form.sshServer = state.ssh.server;
        form.sshTarget = state.ssh.target;
        form.sshAlways = Boolean(state.ssh.always);
        form.sshTargetTouched = true;
        form.sshAlwaysTouched = true;
      } else if (!form.link.trim()) {
        form.sshServer = "";
        form.sshTarget = state.default_ssh_target || "";
        form.sshAlways = false;
        form.sshTargetTouched = false;
        form.sshAlwaysTouched = false;
      }
    }
    draw();
  });
  lt.on("prefill", (m) => {
    tab = "active";
    onLink(m.raw);
    draw();
  });
  lt.on("tab", (m) => { tab = m.tab; draw(); });

  draw();
})();
