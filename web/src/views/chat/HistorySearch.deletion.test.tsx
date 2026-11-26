// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { HistorySearch } from "./HistorySearch";
import { recordSent } from "../../lib/send-history";
import { writeInlineDraft } from "../../lib/inline-mentions";
import { skillDraft } from "../../lib/skill-mentions";
import { markConversationDeleted, reset } from "../../lib/store";
import { setToken } from "../../lib/api";

vi.hoisted(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) }); });
let host: HTMLDivElement, root: Root;
const pick = vi.fn(), close = vi.fn();
const secret = skillDraft([{ id: "skill-private", kind: "skill", title: "Private skill", text: "Private metadata", url: "", attrs: { ref: "opaque-private-skill" } }], "Private sent text");
const mount = () => act(() => root.render(<HistorySearch onPick={pick} onClose={close} />));
const option = (text: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="option"]')].find((row) => row.textContent?.includes(text))!;
beforeEach(() => {
  reset(); localStorage.clear(); setToken("history-owner"); pick.mockClear(); close.mockClear();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { value: vi.fn(), configurable: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); reset(); });

it("shows readable inline references and recalls the original rich draft", () => {
  const rich = writeInlineDraft(["Use ", { id: "fixture", kind: "skill", title: "Review", text: "Review the change.", url: "" }, "."]);
  recordSent("history-hub/chat", rich);
  mount();
  const row = option("Use Review.");
  expect(row.title).toBe("Use Review.");
  expect(host.textContent).not.toContain("persona-inline-v1");
  act(() => row.click());
  expect(pick).toHaveBeenCalledExactlyOnceWith(rich);
});

it("filters by displayed @file text and hides raw JSON/XML metadata tokens (#15797)", () => {
  const richFile = writeInlineDraft([
    "Check ",
    { id: "file:src/App.tsx", kind: "file", title: "App.tsx", url: "", text: "src/App.tsx", attrs: { path: "src/App.tsx", cwd: "/example/repo" } },
  ]);
  recordSent("history-hub/chat", richFile, undefined, 2);
  recordSent("history-hub/chat", secret, undefined, 1);
  mount();

  const input = host.querySelector<HTMLInputElement>('input[role="combobox"]')!;
  const setQuery = (q: string) => {
    const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    act(() => {
      setVal.call(input, q);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };

  setQuery("@src/App");
  expect(host.querySelectorAll('[role="option"]')).toHaveLength(1);
  expect(option("Check @src/App.tsx")).toBeDefined();

  setQuery("persona-inline-v1");
  expect(host.querySelectorAll('[role="option"]')).toHaveLength(0);

  setQuery("opaque-private-skill");
  expect(host.querySelectorAll('[role="option"]')).toHaveLength(0);

  setQuery("@src/App.tsx");
  act(() => option("Check @src/App.tsx").click());
  expect(pick).toHaveBeenCalledExactlyOnceWith(richFile);
});

it("rejects a stale click on a deduplicated inline-ref prompt after its newer chat is deleted", () => {
  const newer = writeInlineDraft(["Open ", { id: "uuid-newer", kind: "workspace", title: "Main box", url: "/m1/w1", text: "box" }]);
  const older = writeInlineDraft(["Open ", { id: "uuid-older", kind: "workspace", title: "Main box", url: "/m1/w1", text: "box" }]);
  recordSent("history-hub/private", newer, undefined, 2);
  recordSent("history-hub/kept", older, undefined, 1);
  mount();
  expect(host.querySelectorAll('[role="option"]')).toHaveLength(1);
  const stale = option("Open Main box");
  act(() => { markConversationDeleted("history-hub", "private"); stale.click(); });
  expect(pick).not.toHaveBeenCalled();
  act(() => option("Open Main box").click());
  expect(pick).toHaveBeenCalledExactlyOnceWith(older);
});

it("removes another chat's text and selected skill from an already open search", () => {
  recordSent("history-hub/private", secret, undefined, 2);
  recordSent("history-hub/kept", "Keep this message", undefined, 1);
  mount(); expect(host.textContent).toContain("Private skill"); expect(host.textContent).toContain("Private sent text");
  act(() => markConversationDeleted("history-hub", "private"));
  expect(host.textContent).not.toContain("Private skill"); expect(host.textContent).not.toContain("Private sent text");
  act(() => option("Keep this message").click());
  expect(pick).toHaveBeenCalledExactlyOnceWith("Keep this message");
});

it("rejects a stale click by source identity before rendering, even when another chat has identical text", () => {
  recordSent("history-hub/private", secret, undefined, 2);
  recordSent("history-hub/kept", secret, undefined, 1);
  mount(); const stale = option("Private sent text");
  act(() => { markConversationDeleted("history-hub", "private"); stale.click(); });
  expect(pick).not.toHaveBeenCalled();
  // A newly rendered result may still belong to the retained chat; it is a new, explicit selection.
  act(() => option("Private sent text").click());
  expect(pick).toHaveBeenCalledExactlyOnceWith(secret);
});

it.each(["credential", "reset"])("clears and refuses the old panel when its %s changes", (kind) => {
  recordSent("history-hub/private", secret); mount(); const stale = option("Private sent text");
  act(() => { if (kind === "credential") setToken("replacement-history-owner"); else reset(); stale.click(); });
  expect(pick).not.toHaveBeenCalled();
  expect(close).toHaveBeenCalled();
  expect(host.textContent).not.toContain("Private sent text");
  expect(host.textContent).not.toContain("Private skill");
});

it("clears an open panel on another tab's credential change without a click or unrelated render", () => {
  recordSent("history-hub/private", secret); mount(); const stale = option("Private sent text");
  act(() => {
    localStorage.setItem("persona.token", "owner-from-another-tab");
    window.dispatchEvent(new StorageEvent("storage", { key: "persona.token", newValue: "owner-from-another-tab" }));
  });
  expect(close).toHaveBeenCalled();
  expect(host.textContent).not.toContain("Private sent text");
  expect(host.textContent).not.toContain("Private skill");
  act(() => stale.click());
  expect(pick).not.toHaveBeenCalled();
});
