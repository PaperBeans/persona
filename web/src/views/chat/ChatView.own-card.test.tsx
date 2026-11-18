// @vitest-environment jsdom
// A Persona conversation's own approval card is in the chat, above the composer (#4883): its first Bash waited for
// approval while the card sat only in the closed Needs you tray, and the owner thought the assistant had hung. The
// card says it waits for the user's approval, Allow ends it, and Needs you still counts it with a row that leads to it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Interaction, Item, Session } from "../../lib/types";
import zh from "../../i18n/locales/zh-Hans/chat-interactions.json";
import en from "../../i18n/locales/en/chat-interactions.json";
import ja from "../../i18n/locales/ja/chat-interactions.json";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/router")>(), useRoute: () => ({ name: "persona", sid: "c1" }) }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));

const { ChatView } = await import("./ChatView");
const { getState, reset, upsertSession } = await import("../../lib/store");
const { t } = await import("../../i18n");
const { loadPages } = await import("../../lib/lazy-page");
await loadPages();
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const bash = {
  id: "ix_bash", machine_id: "hub", session_id: "c1", session_title: "Persona", kind: "permission", tool_name: "Bash",
  tool_input: { command: "ssh vps 'cat /etc/example/config.json'", description: "Read the server config" },
  created_at: "2020-01-01T00:00:00Z",
} as Interaction;

let host: HTMLDivElement;
let root: Root;
let fetcher: ReturnType<typeof vi.fn>;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const card = () => host.querySelector(`[data-card="${bash.id}"]`);
const tray = () => host.querySelector("[data-assistant-actionbar]");
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const resolves = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith(`/interactions/${bash.id}/resolve`));

beforeEach(() => {
  localStorage.clear();
  transcript.items = [];
  upsertSession({ machine_id: "hub", session_id: "c1", title: "Persona", status: "working", state: "needs_you", assistant: true, agent: "claude" } as Session);
  Object.assign(getState(), { interactions: [bash], answered: {} });
  fetcher = vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetcher);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("an assistant-owned approval question", () => {
  const prepared = {
    ...bash,
    id: "ix_prepared",
    tool_name: "persona_settings",
    can_clarify: true,
    fields: [{ key: "password", label: "Password", secret: true }],
  } as Interaction;
  const area = () => host.querySelector<HTMLTextAreaElement>("[data-approval-question] textarea");
  const type = (text: string) => act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area(), text);
    area()!.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const show = async () => {
    Object.assign(getState(), { interactions: [prepared], answered: {} });
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant onToggleSidebar={() => {}} />));
    await flush();
    await act(async () => {
      button(t("chat-interactions.have_question"))!.click();
      await import("./ApprovalQuestion");
    });
    await flush();
  };

  it("sends only verbatim text and a stable message ID, then allows the same card once", async () => {
    await show();
    const question = "  Why does this change need my password?\nPlease be specific.  ";
    await type(question);
    act(() => button(t("approval-question.send"))!.click());
    await flush();
    const calls = fetcher.mock.calls.filter(([url]) => String(url).endsWith(`/interactions/${prepared.id}/clarify`));
    expect(calls.length).toBe(1);
    const body = JSON.parse(String((calls[0][1] as RequestInit).body));
    expect(body.text).toBe(question);
    expect(body.client_msg_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(Object.keys(body).sort()).toEqual(["client_msg_id", "text"]);
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith(`/interactions/${prepared.id}/resolve`))).toBe(false);
    expect(area()).toBeNull();
    expect(getState().interactions.some((i) => i.id === prepared.id)).toBe(true);
    act(() => button(t("chat-interactions.allow"))!.click());
    await flush();
    const allows = fetcher.mock.calls.filter(([url]) => String(url).endsWith(`/interactions/${prepared.id}/resolve`));
    expect(allows.length).toBe(1);
    expect(JSON.parse(String((allows[0][1] as RequestInit).body))).toMatchObject({ allow: true });
  });

  it("preserves a failed draft and retries it under the same delivery ID", async () => {
    await show();
    await type("  keep every surrounding character  ");
    fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ error: "offline" }), { status: 400, headers: { "Content-Type": "application/json" } }));
    act(() => button(t("approval-question.send"))!.click());
    await flush();
    expect(area()?.value).toBe("  keep every surrounding character  ");
    expect(button(t("chat-interactions.allow"))!.disabled).toBe(false);
    act(() => button(t("approval-question.send"))!.click());
    await flush();
    const calls = fetcher.mock.calls.filter(([url]) => String(url).endsWith(`/interactions/${prepared.id}/clarify`));
    expect(calls.length).toBe(2);
    const first = JSON.parse(String((calls[0][1] as RequestInit).body));
    const second = JSON.parse(String((calls[1][1] as RequestInit).body));
    expect(second).toEqual(first);
  });

  it("does not turn Enter in the question editor into Allow", async () => {
    await show();
    await type("Why this change?");
    act(() => area()!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    await flush();
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith(`/interactions/${prepared.id}/resolve`))).toBe(false);
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith(`/interactions/${prepared.id}/clarify`))).toBe(true);
  });

  it("ships the action in all three languages and leaves old cards unchanged", async () => {
    expect(en.have_question).toBe("Have a question");
    expect(zh.have_question).toBe("有疑问");
    expect(ja.have_question).toBe("質問する");
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant onToggleSidebar={() => {}} />));
    await flush();
    expect(button(t("chat-interactions.have_question"))).toBeUndefined();
    expect(button(t("chat-interactions.allow"))).toBeDefined();
    expect(button(t("chat-interactions.deny"))).toBeDefined();
  });
});

describe("a Persona conversation's own approval card (#4883)", () => {
  it("is in the chat, not only in the closed Needs you tray", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant onToggleSidebar={() => {}} />));
    await flush();
    // the tray starts closed, and the card is there anyway
    expect(host.querySelector("[data-tray-list]")).toBeNull();
    expect(card()).not.toBeNull();
    expect(tray()?.contains(card())).toBe(false);
    expect(card()?.textContent).toContain(t("chat-interactions.permission_kicker"));
    expect(card()?.textContent).toContain("ssh vps");
    expect(button(t("chat-interactions.allow"))).toBeDefined();
    expect(button(t("chat-interactions.deny"))).toBeDefined();
  });

  it("is still counted in Needs you, whose row leads to the card instead of drawing it twice", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant onToggleSidebar={() => {}} />));
    await flush();
    const needs = host.querySelector<HTMLButtonElement>('[data-tray-entry="needs"]')!;
    expect(needs.textContent).toContain("1");
    act(() => needs.click());
    const row = host.querySelector<HTMLButtonElement>(`[data-own-ask="${bash.id}"]`);
    expect(row).not.toBeNull();
    expect(host.querySelectorAll(`[data-card="${bash.id}"]`).length).toBe(1);
    act(() => row!.click());
    expect(host.querySelector("[data-tray-list]")).toBeNull();
    expect(card()).not.toBeNull();
  });

  it("ends once allowed, and the chat goes on", async () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant onToggleSidebar={() => {}} />));
    await flush();
    act(() => button(t("chat-interactions.allow"))!.click());
    await flush();
    expect(resolves().length).toBe(1);
    expect(JSON.parse(String((resolves()[0][1] as RequestInit).body))).toMatchObject({ allow: true });
    expect(getState().interactions.some((i) => i.id === bash.id)).toBe(false);
    // no card waits any more: no answer buttons, no "waiting for your approval", the composer is there to go on
    expect(button(t("chat-interactions.allow"))).toBeUndefined();
    expect(host.textContent).not.toContain(t("chat-interactions.permission_kicker"));
    expect(host.querySelector("textarea")).not.toBeNull();
  });

  it("says it waits for the user's approval, in every language", () => {
    expect(zh.permission_kicker).toBe("在等你批准");
    expect(en.permission_kicker).toBe("Waiting for your approval");
    expect(ja.permission_kicker).toBe("承認を待っています");
  });
});
