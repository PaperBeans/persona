// @vitest-environment jsdom
// Controlled delegated views avoid default store, hub request and toast singletons; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DelegatedAnswer, Reanswer, ReanswerResult } from "../../lib/delegate";
import type { Interaction } from "../../lib/types";
import type { DelegatedCardProps, DelegatedHistoryProps } from "./Delegated";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(), getSession: vi.fn(), dropDelegated: vi.fn(), noteReanswer: vi.fn(),
  loadDelegated: vi.fn(), reanswer: vi.fn(), toast: vi.fn(),
}));
vi.mock("../../lib/store", () => ({ useStore: mocks.useStore, getSession: mocks.getSession, dropDelegated: mocks.dropDelegated, noteReanswer: mocks.noteReanswer }));
vi.mock("../../lib/delegate", async (orig) => ({ ...await orig<typeof import("../../lib/delegate")>(), loadDelegated: mocks.loadDelegated, reanswer: mocks.reanswer }));
vi.mock("../../ui/atoms", async (orig) => ({ ...await orig<typeof import("../../ui/atoms")>(), toast: mocks.toast }));
vi.mock("../../ui/icons", () => ({ Icon: ({ name }: { name: string }) => <span data-icon={name} /> }));

const { DelegatedCard, DelegatedHistory } = await import("./Delegated");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const Q1 = "Which driver?";
const Q2 = "Enable pooling?";
const ix: Interaction = {
  id: "ix-1", machine_id: "m1", session_id: "s1", kind: "question", manager: "mgr-2", created_at: "2026-11-07T10:00:00Z",
  questions: [
    { question: Q1, header: "Driver", multi_select: false, options: [{ label: "pgx" }, { label: "lib/pq" }] },
    { question: Q2, header: "Pool", multi_select: false, options: [{ label: "yes" }, { label: "no" }] },
  ],
};
const answered: DelegatedAnswer = { ix, answers: { [Q1]: "pgx", [Q2]: "yes" }, reason: "Used across repo", at: Date.parse("2026-11-07T10:01:00Z") };
const session = { machine_id: "m1", session_id: "s1", manager: "mgr-2" };

let host: HTMLDivElement;
let root: Root;
const flush = async () => { for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); }); };
const mountCard = (props: Partial<DelegatedCardProps> = {}) => act(async () => root.render(<DelegatedCard d={answered} {...props} />));
const mountHist = (props: Partial<DelegatedHistoryProps> = {}) => act(async () => root.render(<DelegatedHistory session={session} {...props} />));
const btn = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const pick = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button[aria-pressed]")].find((b) => b.textContent?.startsWith(label))!;
const collapse = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-interactions.collapse")}"]`)!;
const typeIn = (el: HTMLInputElement, v: string) => act(async () => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, v);
  el.dispatchEvent(new Event("input", { bubbles: true }));
});
const expectNoDefaults = () => {
  for (const fn of Object.values(mocks)) expect(fn).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

describe("DelegatedCard controlled mode", () => {
  it("dismisses and respects canChange without touching default singletons", async () => {
    const onDismiss = vi.fn();
    await mountCard({ reanswered: null, canChange: false, onDismiss });
    expect(btn(t("chat-delegate.change"))!.disabled).toBe(true);
    expect(btn(t("chat-delegate.change"))!.title).toBe(t("agents-agentsview.cannot_send"));
    act(() => collapse().click());
    expect(onDismiss).toHaveBeenCalledWith(ix.id);
    expectNoDefaults();
  });

  it("submits changed answers via onReanswer, guards duplicates and renders controlled reanswered state", async () => {
    let done!: (r: ReanswerResult) => void;
    const onReanswer = vi.fn(() => new Promise<ReanswerResult>((r) => { done = r; }));
    const onSuccess = vi.fn();
    const base = { reanswered: null as Reanswer | null, canChange: true, onDismiss: vi.fn(), onReanswer, onSuccess };

    await mountCard(base);
    await act(async () => btn(t("chat-delegate.change"))!.click());
    await act(async () => pick("lib/pq").click());
    act(() => btn(t("chat-delegate.send"))!.click());
    expect(onReanswer).toHaveBeenCalledWith(ix.id, { [Q1]: "lib/pq" }, "mgr-2");
    expect(btn(t("chat-delegate.send"))!.disabled).toBe(true);
    act(() => btn(t("chat-delegate.send"))!.click());
    expect(onReanswer).toHaveBeenCalledTimes(1);

    await act(async () => { done({ ok: true }); await Promise.resolve(); });
    await flush();
    expect(onSuccess).toHaveBeenCalledWith(t("chat-delegate.sent"));
    expect(btn(t("chat-delegate.send"))).toBeUndefined();

    await mountCard({ ...base, reanswered: { answers: { [Q1]: "lib/pq" }, at: Date.parse("2026-11-07T10:02:00Z") } });
    expect(host.textContent).toContain(t("chat-delegate.reanswered"));
    expect(host.textContent).toContain(t("chat-delegate.was", { answer: "pgx" }));
    expectNoDefaults();
  });

  it("preserves input on returned and thrown controlled reanswer failures", async () => {
    const onReanswer = vi.fn<NonNullable<DelegatedCardProps["onReanswer"]>>()
      .mockResolvedValueOnce({ ok: false, error: "panel offline" })
      .mockRejectedValueOnce(new Error("transport reset"));
    const onSuccess = vi.fn();
    await mountCard({ reanswered: null, canChange: true, onDismiss: vi.fn(), onReanswer, onSuccess });
    await act(async () => btn(t("chat-delegate.change"))!.click());
    const input = host.querySelector<HTMLInputElement>(`input[aria-label="${t("chat-interactions.custom_answer")}"]`)!;
    await typeIn(input, "wait for benchmark");

    act(() => btn(t("chat-delegate.send"))!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("panel offline");
    expect(input.value).toBe("wait for benchmark");

    act(() => btn(t("chat-delegate.send"))!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("transport reset");
    expect(btn(t("chat-delegate.send"))!.disabled).toBe(false);
    expect(onSuccess).not.toHaveBeenCalled();
    expectNoDefaults();
  });
});

describe("DelegatedHistory controlled mode", () => {
  it("renders controlled rows, errors and custom loadHistory without default singletons", async () => {
    const onReanswer = vi.fn<NonNullable<DelegatedHistoryProps["onReanswer"]>>().mockResolvedValue({ ok: true });
    const onSuccess = vi.fn();
    const onRetry = vi.fn();

    await mountHist({ rows: [], reanswered: {}, canChange: true, onReanswer, onSuccess });
    expect(host.textContent).toBe("");

    await mountHist({ rows: [answered], reanswered: {}, canChange: true, onReanswer, onSuccess });
    await act(async () => btn(t("chat-delegate.change"))!.click());
    await act(async () => pick("lib/pq").click());
    act(() => btn(t("chat-delegate.send"))!.click());
    await flush();
    expect(onReanswer).toHaveBeenCalledWith(ix.id, { [Q1]: "lib/pq" }, "mgr-2");
    expect(onSuccess).toHaveBeenCalledWith(t("chat-delegate.sent"));

    await mountHist({ rows: null, error: "history offline", onRetry, reanswered: {}, canChange: true });
    act(() => btn(t("chat-delegate.history_retry"))!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);

    await mountHist({ rows: null, error: "read-only", reanswered: {}, canChange: true });
    expect(btn(t("chat-delegate.history_retry"))).toBeUndefined();
    expectNoDefaults();
  });

  it("loads and retries via loadHistory without calling loadDelegated", async () => {
    const loadHistory = vi.fn<NonNullable<DelegatedHistoryProps["loadHistory"]>>()
      .mockRejectedValueOnce(new Error("fetch failed"))
      .mockResolvedValueOnce([answered]);
    const onRetry = vi.fn();

    await mountHist({ loadHistory, onRetry, reanswered: {}, canChange: false });
    await flush();
    expect(loadHistory).toHaveBeenCalledWith("m1", "s1", "mgr-2");
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("fetch failed");

    act(() => btn(t("chat-delegate.history_retry"))!.click());
    await flush();
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(loadHistory).toHaveBeenCalledTimes(2);
    expect(host.querySelectorAll("[data-delegated-row]")).toHaveLength(1);
    expect(btn(t("chat-delegate.change"))!.disabled).toBe(true);
    expectNoDefaults();
  });
});

describe("Delegated default compatibility", () => {
  const wireStore = () => {
    mocks.getSession.mockReturnValue({ machine_id: "m1", session_id: "s1" });
    mocks.useStore.mockImplementation((sel: (s: { reanswered: Record<string, Reanswer> }) => unknown) => sel({ reanswered: {} }));
  };

  it("keeps default store, dismissal, loadDelegated, reanswer and toast fallbacks", async () => {
    wireStore();
    mocks.reanswer.mockResolvedValue({ ok: true });
    await mountCard();
    expect(mocks.getSession).toHaveBeenCalledWith("m1", "s1");
    act(() => collapse().click());
    expect(mocks.dropDelegated).toHaveBeenCalledWith(ix.id);

    await act(async () => btn(t("chat-delegate.change"))!.click());
    await act(async () => pick("lib/pq").click());
    act(() => btn(t("chat-delegate.send"))!.click());
    await flush();
    expect(mocks.reanswer).toHaveBeenCalledWith(ix.id, { [Q1]: "lib/pq" }, "mgr-2");
    expect(mocks.noteReanswer).toHaveBeenCalledWith(ix.id, { [Q1]: "lib/pq" });
    expect(mocks.toast).toHaveBeenCalledWith(t("chat-delegate.sent"), "ok");

    mocks.loadDelegated.mockRejectedValueOnce(new Error("hub offline")).mockResolvedValueOnce([answered]);
    await mountHist();
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("hub offline");
    act(() => btn(t("chat-delegate.history_retry"))!.click());
    await flush();
    expect(mocks.loadDelegated).toHaveBeenCalledTimes(2);
    expect(host.querySelectorAll("[data-delegated-row]")).toHaveLength(1);
  });
});
