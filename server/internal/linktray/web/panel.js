// The panel: what the link is doing and what can be done about it. All text comes from the tray (linktray.View),
// so this only draws it.
"use strict";

(() => {
  const { el, icon, line, infobar, send, tell } = lt;
  const L = lt.labels;
  const $ = (id) => document.getElementById(id);
  let noticeTimer = 0;
  let busy = "";
  let view = lt.boot.view;
  // rename is the name field while it is open: what is typed, and whether the hub is being asked.
  let rename = null;

  $("mark").appendChild(el("img", { class: "helper", src: lt.boot.helper, alt: "" }));

  function render(v) {
    view = v;
    const pill = $("pill");
    pill.className = "pill" + (v.tone ? " tone-" + v.tone : "");
    pill.textContent = v.pill;

    $("lines").replaceChildren(...v.lines.map(line));

    const rows = [];
    const tools = [];
    const canRename = v.actions.some((a) => a.id === "rename");
    if (!canRename && rename && !rename.saving) rename = null; // disconnected meanwhile: nothing to ask
    for (const a of v.actions) {
      if (a.id === "open_logs" || a.id === "quit") {
        tools.push(el("button", {
          class: "button subtle icon-only", title: a.hint ? a.label + " · " + a.hint : a.label, "aria-label": a.label,
          onclick: () => run(a),
        }, icon(a.icon)));
        continue;
      }
      if (a.checked !== undefined && a.checked !== null) {
        rows.push(el("button", {
          class: "row", role: "menuitemcheckbox", "aria-checked": String(a.checked), onclick: () => run(a),
        }, icon(a.icon), el("span", { class: "label", text: a.label }), el("span", { class: "switch" })));
        continue;
      }
      if (a.id === "rename" && rename) {
        rows.push(renameField());
        continue;
      }
      rows.push(el("button", {
        class: "row", role: "menuitem", disabled: a.disabled || busy === a.id, onclick: () => run(a),
      }, icon(a.icon), el("span", { class: "label", text: a.label })));
    }
    $("actions").replaceChildren(...rows);
    $("tools").replaceChildren(...tools);
    if (rename && rename.focus) {
      rename.focus = false;
      const input = $("rename-input");
      if (input) {
        input.focus();
        input.select();
      }
    }
  }

  // renameField is the open name field in place of its row: what the hub calls this computer, Save and Cancel.
  function renameField() {
    const save = () => saveName();
    // Save is for a name that differs from the one the hub has (typing does not draw the field again).
    const unchanged = () => rename.saving || !rename.name.trim() || rename.name.trim() === view.name;
    const saveButton = el("button", {
      class: "button accent", disabled: unchanged(), onclick: save, text: rename.saving ? L.rename_saving : L.rename_save,
    });
    return el("div", { class: "card stack", id: "rename" },
      el("label", { class: "caption secondary", for: "rename-input", text: L.rename_label }),
      el("input", {
        class: "textbox", id: "rename-input", type: "text", value: rename.name, maxlength: "40", spellcheck: "false",
        autocomplete: "off", disabled: rename.saving,
        oninput: (e) => {
          rename.name = e.target.value;
          saveButton.disabled = unchanged();
        },
        onkeydown: (e) => {
          if (e.key === "Enter") save();
          if (e.key === "Escape") {
            e.stopPropagation(); // closes the field, not the panel
            closeRename();
          }
        },
      }),
      el("div", { class: "caption tertiary", text: L.rename_hint }),
      el("div", { class: "end" },
        el("button", { class: "button subtle", disabled: rename.saving, onclick: closeRename, text: L.rename_cancel }),
        saveButton));
  }

  function openRename() {
    if (!view.actions.some((a) => a.id === "rename")) return;
    rename = { name: view.name || "", saving: false, focus: true };
    notice(null);
    render(view);
  }

  function closeRename() {
    rename = null;
    render(view);
  }

  function saveName() {
    if (!rename || rename.saving) return;
    const name = rename.name.trim();
    if (!name || name === view.name) return;
    rename.saving = true;
    render(view);
    send("rename", { name }).then(() => { rename = null; }, (err) => {
      rename.saving = false;
      rename.focus = true;
      notice({ tone: "danger", text: err.message });
    }).finally(() => render(view));
  }

  // run sends an action; the tray answers with a new view, and maybe a notice.
  function run(a) {
    if (a.id === "rename") {
      openRename();
      return;
    }
    if (a.id === "autostart") {
      tell("action", { id: a.id, on: !a.checked });
      return;
    }
    busy = a.id;
    send("action", { id: a.id }).catch((err) => notice({ tone: "danger", text: err.message }))
      .finally(() => { busy = ""; });
  }

  function notice(n) {
    clearTimeout(noticeTimer);
    if (!n || !n.text) {
      $("notice").replaceChildren();
      return;
    }
    const icons = { positive: "check", danger: "octagon", caution: "warning", info: "info" };
    $("notice").replaceChildren(infobar(n.tone, icons[n.tone] || "info", el("span", { class: "selectable", text: n.text })));
    if (n.tone === "positive") noticeTimer = setTimeout(() => notice(null), 6000);
  }

  lt.on("view", (m) => render(m.view));
  lt.on("notice", (m) => notice(m.notice));
  lt.on("rename", () => openRename()); // the right-click menu's Rename
  lt.on("show", () => {
    // Replay the rise each time the panel opens.
    const main = $("main");
    main.classList.remove("rising");
    void main.offsetWidth;
    main.classList.add("rising");
  });

  $("version").textContent = "Persona Link " + lt.boot.version;
  render(view);
  lt.watchSize($("panel"));
})();
