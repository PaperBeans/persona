// @vitest-environment jsdom
// Knowledge-base chats are Core data, not a lark-module-owned kbKind. The list, search and detail therefore stay
// available with the module on, switched off, or absent (the core-only case).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectModuleHub, registerModule, resetModulesForTest, type ModuleDescriptor } from "../../core/module-host";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../../core/module-switch";
import { initI18n, t } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBDetailResponse, KBEntry, KBResult } from "../../lib/kb";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { getState, reset, subscribe } from "../../lib/store";
import { lists, searches } from "./cache";
import { EntryDetail } from "./KBDetail";
import { ListPane } from "./KBList";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const requests: string[] = [];
const entry: KBEntry = { kind: "chats", id: "c1", name: "Release group", relevance: 0 };
const detail: KBDetailResponse = {
  detail: { kind: "chats", id: "c1", path: "", meta: { name: "Release group", connector: "larkx" }, summary: "# Release group", recent: "## 2026-11-20\n- 10:15 Alice: Historical note\n" },
};
vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  api: (path: string) => {
    requests.push(path);
    if (path === "/kb/chats") return Promise.resolve({ items: [entry], tags: [] });
    if (path === "/kb/chats/c1") return Promise.resolve(detail);
    if (path.startsWith("/kb-search")) {
      const results: KBResult[] = [{ entry, count: 1 }];
      return Promise.resolve({ results });
    }
    return Promise.resolve({});
  },
}));

const descriptor: ModuleDescriptor = {
  id: "lark",
  feature: "module.lark",
  title: () => "Lark",
  description: () => "Lark module",
  load: async () => ({}),
};
let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 12; i++) await act(async () => { await Promise.resolve(); });
};
const search = async (value: string) => {
  const input = host.querySelector<HTMLInputElement>(`input[aria-label="${t("knowledge-kblist.search_label")}"]`)!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => vi.advanceTimersByTime(300));
  await flush();
};

beforeEach(async () => {
  await initI18n("en");
  vi.useFakeTimers();
  localStorage.clear();
  resetModulesForTest();
  forgetModuleSwitches();
  reset();
  connectModuleHub(getState, subscribe);
  resetManagerChoice();
  setRosterDefault("assistant");
  setToken("synthetic-owner-a");
  lists.clear();
  searches.clear();
  requests.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.useRealTimers();
  lists.clear();
  searches.clear();
  resetManagerChoice();
});

describe.each(["on", "switched off", "absent"] as const)("knowledge chats with the lark module %s", (mode) => {
  beforeEach(() => {
    if (mode !== "absent") registerModule(descriptor);
    if (mode === "switched off") setModuleSwitchedOff("lark", true);
  });

  it("keeps the list, search and detail readable", async () => {
    act(() => root.render(<ListPane kind="chats" detailOpen={false} reloadKey={0} onToggleSidebar={() => {}} onOpen={() => {}} />));
    await flush();
    expect(host.textContent).toContain("Release group");
    expect(requests).toContain("/kb/chats");

    await search("Historical");
    expect(host.textContent).toContain("Release group");
    expect(requests.some((p) => p.startsWith("/kb-search?q=Historical&results=1"))).toBe(true);

    await act(async () => root.render(
      <EntryDetail kind="chats" id="c1" reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={() => {}} />,
    ));
    await flush();
    expect(requests).toContain("/kb/chats/c1");
    expect(host.textContent).toContain("Release group");
    expect(host.textContent).not.toContain(t("not-found.kb_title"));
  });
});
