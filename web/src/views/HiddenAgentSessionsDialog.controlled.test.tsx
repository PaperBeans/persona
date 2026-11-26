// @vitest-environment jsdom
// Controlled HiddenAgentSessionsDialog uses opt-in deps and pure view separation without touching default singletons; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../lib/types";
import type { HiddenAgentSessionsSnapshot, HiddenAgentSessionsSource } from "./HiddenAgentSessionsDialog";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  useScoped: vi.fn(<T,>(items: T[]) => items),
  onHubEvent: vi.fn(),
  href: vi.fn(),
  onLinkClick: vi.fn(),
}));

vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: mocks.api,
}));
vi.mock("../lib/current-assistant", () => ({
  useScoped: mocks.useScoped,
}));
vi.mock("../lib/store", () => ({
  onHubEvent: mocks.onHubEvent,
}));
vi.mock("../lib/router", () => ({
  href: mocks.href,
  onLinkClick: mocks.onLinkClick,
}));

const { HiddenAgentSessionsDialog, HiddenAgentSessionsDialogView } = await import("./HiddenAgentSessionsDialog");
const { initI18n, t } = await import("../i18n");

const makeSession = (patch: Partial<Session> = {}): Session => ({
  machine_id: "demo",
  session_id: "s1",
  title: "Hidden agent",
  cwd: "/demo/work",
  status: "idle",
  state: "idle",
  hidden_by_directory: true,
  ...patch,
});

function createFakeSource(initial: Partial<HiddenAgentSessionsSnapshot> = {}) {
  let snapshot: HiddenAgentSessionsSnapshot = {
    rows: [],
    loading: false,
    error: null,
    hasMore: false,
    ...initial,
  };
  const listeners = new Set<() => void>();
  const emit = (next: Partial<HiddenAgentSessionsSnapshot>) => {
    snapshot = { ...snapshot, ...next };
    listeners.forEach((l) => l());
  };
  const source: HiddenAgentSessionsSource = {
    subscribe: vi.fn((l: () => void) => {
      listeners.add(l);
      return () => { listeners.delete(l); };
    }),
    getSnapshot: vi.fn(() => snapshot),
    load: vi.fn(async () => {}),
    retry: vi.fn(async () => {}),
    more: vi.fn(async () => {}),
    invalidate: vi.fn(),
    reset: vi.fn(),
  };
  return { source, emit, listenerCount: () => listeners.size };
}

let host: HTMLDivElement;
let root: Root;
const unhandled: unknown[] = [];
const onUnhandled = (reason: unknown) => { unhandled.push(reason); };

const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const button = (label: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(label));

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  mocks.api.mockReset();
  mocks.useScoped.mockClear();
  mocks.useScoped.mockImplementation(<T,>(items: T[]) => items);
  mocks.onHubEvent.mockReset().mockReturnValue(() => {});
  mocks.href.mockReset().mockImplementation((r: { mid: string; sid: string }) => `/sessions/${r.mid}/${r.sid}`);
  mocks.onLinkClick.mockReset().mockImplementation((e: { preventDefault: () => void }) => e.preventDefault());
  unhandled.length = 0;
  process.on("unhandledRejection", onUnhandled);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  process.off("unhandledRejection", onUnhandled);
});

describe("HiddenAgentSessionsDialog controlled deps and view separation", () => {
  it("runs fully controlled via deps without invoking default singletons", async () => {
    const fake = createFakeSource({
      rows: [
        makeSession({ session_id: "s-hidden", title: "Scratch run", hidden_by_directory: true, manager: "m1" }),
        makeSession({ session_id: "s-other", title: "Other manager", hidden_by_directory: false, manager: "m2" }),
        makeSession({ session_id: "s-assistant", title: "Assistant chat", assistant: true, manager: "m1" }),
        makeSession({ session_id: "s-untitled", title: "", hidden_by_directory: false, manager: "m1" }),
      ],
      hasMore: true,
    });
    let sessionsListener: (() => void) | undefined;
    const unsubscribe = vi.fn();
    const onSessionsList = vi.fn((l: () => void) => {
      sessionsListener = l;
      return unsubscribe;
    });
    const createList = vi.fn(() => fake.source);
    const scopeRows = vi.fn((rows: readonly Session[]) => rows.filter((r) => r.manager === "m1"));
    const sessionHref = vi.fn((s: Session) => `#custom/${s.machine_id}/${s.session_id}`);
    const onSessionClick = vi.fn((e: { preventDefault: () => void }) => e.preventDefault());
    const onClose = vi.fn();

    // Closed dialog mounts neither the list nor any subscriptions.
    await act(async () => root.render(
      <HiddenAgentSessionsDialog
        open={false}
        onClose={onClose}
        deps={{ createList, onSessionsList, scopeRows, sessionHref, onSessionClick }}
      />,
    ));
    expect(createList).not.toHaveBeenCalled();
    expect(onSessionsList).not.toHaveBeenCalled();
    expect(scopeRows).not.toHaveBeenCalled();

    await act(async () => root.render(
      <HiddenAgentSessionsDialog
        open
        onClose={onClose}
        deps={{ createList, onSessionsList, scopeRows, sessionHref, onSessionClick }}
      />,
    ));
    await flush();

    expect(createList).toHaveBeenCalledTimes(1);
    expect(fake.source.load).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain("Scratch run");
    expect(document.body.textContent).toContain(t("lib-format.untitled_session"));
    expect(document.body.textContent).not.toContain("Other manager");
    expect(document.body.textContent).not.toContain("Assistant chat");

    // Hub event proxy triggers invalidate; error + retry + more stay controlled.
    sessionsListener?.();
    expect(fake.source.invalidate).toHaveBeenCalledTimes(1);

    await act(async () => fake.emit({ error: "controlled fail" }));
    expect(document.body.textContent).toContain("controlled fail");
    (fake.source.retry as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("retry rejected"));
    await act(async () => button(t("hidden-agent-dirs.retry"))!.click());
    await flush();
    expect(fake.source.retry).toHaveBeenCalledTimes(1);

    (fake.source.more as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("more rejected"));
    await act(async () => button(t("hidden-agent-dirs.more"))!.click());
    await flush();
    expect(fake.source.more).toHaveBeenCalledTimes(1);

    const link = document.body.querySelector<HTMLAnchorElement>('a[href="#custom/demo/s-hidden"]');
    expect(link).not.toBeNull();
    await act(async () => link!.click());
    expect(onSessionClick).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);

    // Closing cleans up subscriptions and resets the source; late listener calls are fenced.
    await act(async () => root.render(
      <HiddenAgentSessionsDialog
        open={false}
        onClose={onClose}
        deps={{ createList, onSessionsList, scopeRows, sessionHref, onSessionClick }}
      />,
    ));
    expect(unsubscribe).toHaveBeenCalledTimes(1);
    expect(fake.source.reset).toHaveBeenCalledTimes(1);
    sessionsListener?.();
    expect(fake.source.invalidate).toHaveBeenCalledTimes(1);

    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.useScoped).not.toHaveBeenCalled();
    expect(mocks.onHubEvent).not.toHaveBeenCalled();
    expect(mocks.href).not.toHaveBeenCalled();
    expect(mocks.onLinkClick).not.toHaveBeenCalled();
    expect(unhandled).toEqual([]);
  });

  it("resets stale list and fences late emissions/events across A -> B -> A scope switches", async () => {
    const listA1 = createFakeSource({ rows: [makeSession({ session_id: "a1", title: "Scope A visit 1" })] });
    const listB = createFakeSource({ rows: [makeSession({ session_id: "b1", title: "Scope B row" })] });
    const listA2 = createFakeSource({ rows: [makeSession({ session_id: "a2", title: "Scope A visit 2" })] });
    let aCalls = 0;
    const createA = () => (++aCalls === 1 ? listA1.source : listA2.source);
    const createB = () => listB.source;

    const callbacks: (() => void)[] = [];
    const onSessionsList = vi.fn((l: () => void) => {
      callbacks.push(l);
      return () => {};
    });
    const scopeRows = (rows: readonly Session[]) => rows;
    const onClose = vi.fn();

    let rejectA1!: (e: Error) => void;
    (listA1.source.load as ReturnType<typeof vi.fn>).mockImplementationOnce(
      () => new Promise<void>((_, reject) => { rejectA1 = reject; }),
    );

    await act(async () => root.render(
      <HiddenAgentSessionsDialog open onClose={onClose} deps={{ createList: createA, onSessionsList, scopeRows }} />,
    ));
    expect(document.body.textContent).toContain("Scope A visit 1");

    // Switch A -> B: A1 is reset and unsubscribed.
    await act(async () => root.render(
      <HiddenAgentSessionsDialog open onClose={onClose} deps={{ createList: createB, onSessionsList, scopeRows }} />,
    ));
    expect(listA1.source.reset).toHaveBeenCalledTimes(1);
    expect(listA1.listenerCount()).toBe(0);
    expect(document.body.textContent).toContain("Scope B row");
    expect(document.body.textContent).not.toContain("Scope A visit 1");

    // Switch B -> A (ABA): B is reset and a fresh A2 source is mounted.
    await act(async () => root.render(
      <HiddenAgentSessionsDialog open onClose={onClose} deps={{ createList: createA, onSessionsList, scopeRows }} />,
    ));
    expect(listB.source.reset).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain("Scope A visit 2");

    // Late rejection, snapshot emit, and stale callback from A1 do not leak into A2.
    await act(async () => {
      rejectA1(new Error("stale A1 load"));
      listA1.emit({ rows: [makeSession({ session_id: "stale", title: "Leaked A1" })], error: "Leaked error" });
      callbacks[0]?.();
    });
    await flush();
    expect(listA1.source.invalidate).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("Scope A visit 2");
    expect(document.body.textContent).not.toContain("Leaked A1");
    expect(document.body.textContent).not.toContain("Leaked error");
    expect(unhandled).toEqual([]);
  });

  it("renders pure HiddenAgentSessionsDialogView without touching default singletons", async () => {
    const onClose = vi.fn();
    const onRetry = vi.fn();
    const onMore = vi.fn();
    const onSessionClick = vi.fn((e: { preventDefault: () => void }) => e.preventDefault());

    await act(async () => root.render(
      <HiddenAgentSessionsDialogView
        open
        onClose={onClose}
        rows={[makeSession({ session_id: "view-1", title: "View row" })]}
        loading={false}
        error="view error"
        hasMore
        onRetry={onRetry}
        onMore={onMore}
        sessionHref={(s) => `#view/${s.session_id}`}
        onSessionClick={onSessionClick}
      />,
    ));

    expect(document.body.textContent).toContain("View row");
    expect(document.body.textContent).toContain("view error");
    await act(async () => button(t("hidden-agent-dirs.retry"))!.click());
    await act(async () => button(t("hidden-agent-dirs.more"))!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onMore).toHaveBeenCalledTimes(1);
    await act(async () => document.body.querySelector<HTMLAnchorElement>('a[href="#view/view-1"]')!.click());
    expect(onSessionClick).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);

    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.useScoped).not.toHaveBeenCalled();
    expect(mocks.onHubEvent).not.toHaveBeenCalled();
    expect(mocks.href).not.toHaveBeenCalled();
    expect(mocks.onLinkClick).not.toHaveBeenCalled();
  });

  it("preserves default SessionList, onHubEvent, useScoped, and router fallbacks when deps are omitted", async () => {
    let hubHandler: (() => void) | undefined;
    mocks.onHubEvent.mockImplementation((event: string, listener: () => void) => {
      if (event === "sessions_list") hubHandler = listener;
      return () => {};
    });
    const session = makeSession({ session_id: "default-1", title: "Default hidden" });
    mocks.api.mockResolvedValue({
      sessions: [session],
      remove: [],
      total: 1,
      has_more: false,
      revision: "r1",
    });
    const onClose = vi.fn();

    await act(async () => root.render(<HiddenAgentSessionsDialog open onClose={onClose} />));
    await flush();

    expect(mocks.api).toHaveBeenCalledWith(expect.stringContaining("/sessions?include_hidden=1"), expect.anything());
    expect(mocks.onHubEvent).toHaveBeenCalledWith("sessions_list", expect.any(Function));
    expect(mocks.useScoped).toHaveBeenCalled();
    expect(mocks.href).toHaveBeenCalledWith({ name: "session", mid: "demo", sid: "default-1" });
    expect(document.body.textContent).toContain("Default hidden");

    // Hub invalidation triggers another default list read.
    await act(async () => hubHandler?.());
    await flush();
    expect(mocks.api.mock.calls.length).toBeGreaterThanOrEqual(2);

    const link = document.body.querySelector<HTMLAnchorElement>('a[href="/sessions/demo/default-1"]');
    await act(async () => link!.click());
    expect(mocks.onLinkClick).toHaveBeenCalledWith(expect.anything(), { name: "session", mid: "demo", sid: "default-1" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
