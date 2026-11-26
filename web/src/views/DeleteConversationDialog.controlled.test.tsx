// @vitest-environment jsdom
// Controlled DeleteConversationDialog bypasses store/action singletons and fences cross-session and ABA async races; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CONVERSATION_DELETE_FEATURE } from "../lib/conversation-delete";
import type { Session } from "../lib/types";
import type { DeleteConversationDialogDeps } from "./DeleteConversationDialog";

const mocks = vi.hoisted(() => ({
  deleteConversation: vi.fn(),
  errorText: vi.fn((e: unknown) => (e instanceof Error ? e.message : String(e))),
  getSession: vi.fn(),
  getState: vi.fn(),
  useStore: vi.fn(),
}));

vi.mock("../lib/conversation-delete-action", () => ({
  deleteConversation: mocks.deleteConversation,
}));
vi.mock("../lib/api", () => ({
  errorText: mocks.errorText,
}));
vi.mock("../lib/store", () => ({
  getSession: mocks.getSession,
  getState: mocks.getState,
  useStore: mocks.useStore,
}));

const { DeleteConversationDialog, DeleteConversationDialogView } = await import("./DeleteConversationDialog");
const { initI18n, t } = await import("../i18n");

const makeChat = (patch: Partial<Session> = {}): Session => ({
  machine_id: "hub",
  session_id: "chat-a",
  title: "Chat A",
  cwd: "",
  status: "idle",
  assistant: true,
  manager: "assistant",
  ...patch,
});

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const dialogButton = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === label);
const alertText = () => document.querySelector('[role="dialog"] [role="alert"]')?.textContent ?? "";

const expectNoDefaultSingletons = () => {
  expect(mocks.deleteConversation).not.toHaveBeenCalled();
  expect(mocks.errorText).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  Object.assign(globalThis, { matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) });
  await initI18n("en");
  mocks.deleteConversation.mockReset();
  mocks.errorText.mockClear();
  mocks.getSession.mockReset();
  mocks.getState.mockReset();
  mocks.useStore.mockReset().mockReturnValue(false);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("DeleteConversationDialog controlled deps, view separation, and race guards", () => {
  it("runs fully controlled via deps without touching store or delete-action singletons", async () => {
    const s = makeChat();
    const fresher = makeChat({ title: "Chat A (fresh)" });
    const req = deferred();
    const onClose = vi.fn();
    const deps: DeleteConversationDialogDeps = {
      isDeleted: false,
      getSession: vi.fn(() => fresher),
      canDelete: vi.fn(() => true),
      deleteConversation: vi.fn(() => req.promise),
      formatError: vi.fn(() => "custom formatted error"),
    };

    await act(async () => root.render(<DeleteConversationDialog s={s} onClose={onClose} deps={deps} />));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Chat A");

    // Rapid double-click admits a single delete call with the fresher session.
    await act(async () => {
      dialogButton(t("conversation-delete.delete_confirm"))!.click();
      dialogButton(t("conversation-delete.delete_confirm"))!.click();
    });
    expect(deps.getSession).toHaveBeenCalledWith("hub", "chat-a");
    expect(deps.canDelete).toHaveBeenCalledWith(fresher);
    expect(deps.deleteConversation).toHaveBeenCalledTimes(1);
    expect(deps.deleteConversation).toHaveBeenCalledWith(fresher);
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

    // Reject renders custom formatted error; retry succeeds and calls onClose.
    await act(async () => req.reject(new Error("network down")));
    await flush();
    expect(alertText()).toBe("custom formatted error");
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(false);
    expect(onClose).not.toHaveBeenCalled();

    const retryReq = deferred();
    (deps.deleteConversation as ReturnType<typeof vi.fn>).mockImplementationOnce(() => retryReq.promise);
    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    expect(alertText()).toBe("");
    await act(async () => retryReq.resolve());
    await flush();
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("closes via controlled subscribe when external state marks the conversation deleted", async () => {
    const s = makeChat();
    let deleted = false;
    const listeners = new Set<() => void>();
    const onClose = vi.fn();
    const deps: DeleteConversationDialogDeps = {
      getSession: () => (deleted ? { ...s, deleted: true } : s),
      subscribe: (l) => {
        listeners.add(l);
        return () => { listeners.delete(l); };
      },
      canDelete: () => false,
      deleteConversation: vi.fn(async () => {}),
      formatError: () => "unused",
    };

    await act(async () => root.render(<DeleteConversationDialog s={s} onClose={onClose} deps={deps} />));
    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    expect(alertText()).toBe(t("conversation-delete.delete_unavailable"));
    expect(deps.deleteConversation).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    deleted = true;
    await act(async () => listeners.forEach((l) => l()));
    await flush();
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("clears error and busy state on cross-session switch (A -> B) and discards A's stale resolve/reject", async () => {
    const chatA = makeChat({ session_id: "chat-a", title: "Chat A" });
    const chatB = makeChat({ session_id: "chat-b", title: "Chat B" });
    const reqA = deferred();
    const reqB = deferred();
    const onClose = vi.fn();
    const deleteFn = vi.fn((cur: Session) => (cur.session_id === "chat-a" ? reqA.promise : reqB.promise));
    const deps: DeleteConversationDialogDeps = {
      isDeleted: false,
      getSession: (_m, sid) => (sid === "chat-a" ? chatA : chatB),
      canDelete: () => true,
      deleteConversation: deleteFn,
      formatError: (e) => (e instanceof Error ? e.message : String(e)),
    };

    // Start delete on A so busy=true.
    await act(async () => root.render(<DeleteConversationDialog s={chatA} onClose={onClose} deps={deps} />));
    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

    // Switch A -> B: busy resets immediately so B can start its own delete.
    await act(async () => root.render(<DeleteConversationDialog s={chatB} onClose={onClose} deps={deps} />));
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(false);
    expect(alertText()).toBe("");

    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

    // Late rejection from A must not show an error on B or clear B's busy state.
    await act(async () => reqA.reject(new Error("stale A failure")));
    await flush();
    expect(alertText()).toBe("");
    expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);
    expect(onClose).not.toHaveBeenCalled();

    // B succeeds cleanly.
    await act(async () => reqB.resolve());
    await flush();
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it.each(["resolve", "reject"] as const)(
    "fences ABA out-of-order %s when switching A -> B -> A while A's first request is still pending",
    async (staleOutcome) => {
      const chatA = makeChat({ session_id: "chat-a", title: "Chat A" });
      const chatB = makeChat({ session_id: "chat-b", title: "Chat B" });
      const reqA1 = deferred();
      const reqA2 = deferred();
      let aCalls = 0;
      const onClose = vi.fn();
      const deps: DeleteConversationDialogDeps = {
        isDeleted: false,
        getSession: (_m, sid) => (sid === "chat-a" ? chatA : chatB),
        canDelete: () => true,
        deleteConversation: vi.fn(() => (++aCalls === 1 ? reqA1.promise : reqA2.promise)),
        formatError: (e) => (e instanceof Error ? e.message : String(e)),
      };

      // Visit 1 of A: start reqA1.
      await act(async () => root.render(<DeleteConversationDialog s={chatA} onClose={onClose} deps={deps} />));
      await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
      expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

      // Switch to B, then back to A (ABA): busy is cleared and visit 2 can start reqA2.
      await act(async () => root.render(<DeleteConversationDialog s={chatB} onClose={onClose} deps={deps} />));
      await act(async () => root.render(<DeleteConversationDialog s={chatA} onClose={onClose} deps={deps} />));
      expect(dialogButton(t("common.cancel"))!.disabled).toBe(false);

      await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
      expect(aCalls).toBe(2);
      expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

      // Stale reqA1 settles out of order before reqA2: must not close, set error, or clear busy.
      await act(async () => {
        if (staleOutcome === "resolve") reqA1.resolve();
        else reqA1.reject(new Error("stale ABA error"));
      });
      await flush();
      expect(onClose).not.toHaveBeenCalled();
      expect(alertText()).toBe("");
      expect(dialogButton(t("common.cancel"))!.disabled).toBe(true);

      // Current reqA2 resolves and closes once.
      await act(async () => reqA2.resolve());
      await flush();
      expect(onClose).toHaveBeenCalledTimes(1);
    },
  );

  it("clears error across manager changes and ignores late settlement after unmount", async () => {
    const s1 = makeChat({ session_id: "same-id", manager: "mgr-a" });
    const s2 = makeChat({ session_id: "same-id", manager: "mgr-b" });
    const req = deferred();
    const onClose = vi.fn();
    let allow = false;
    const deps: DeleteConversationDialogDeps = {
      isDeleted: false,
      getSession: () => undefined,
      canDelete: () => allow,
      deleteConversation: () => req.promise,
      formatError: () => "boom",
    };

    await act(async () => root.render(<DeleteConversationDialog s={s1} onClose={onClose} deps={deps} />));
    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    expect(alertText()).toBe(t("conversation-delete.delete_unavailable"));

    // Switching manager clears the old unavailable error.
    allow = true;
    await act(async () => root.render(<DeleteConversationDialog s={s2} onClose={onClose} deps={deps} />));
    expect(alertText()).toBe("");

    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    await act(async () => root.render(null));
    await act(async () => req.resolve());
    await flush();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("renders pure DeleteConversationDialogView without touching default singletons", async () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn();
    await act(async () => root.render(
      <DeleteConversationDialogView s={makeChat({ title: "Pure view chat" })} busy={false} error="View alert" onClose={onClose} onConfirm={onConfirm} />,
    ));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Pure view chat");
    expect(alertText()).toBe("View alert");
    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    await act(async () => dialogButton(t("common.cancel"))!.click());
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("preserves default store and deleteConversation fallbacks when deps are omitted", async () => {
    const s = makeChat({ session_id: "default-chat", title: "Default chat" });
    mocks.getSession.mockReturnValue(s);
    mocks.getState.mockReturnValue({
      assistant: { machine_id: "hub", main: "main", conversations: [s] },
      features: [CONVERSATION_DELETE_FEATURE],
      interactions: [],
    });
    mocks.deleteConversation.mockResolvedValue(undefined);
    const onClose = vi.fn();

    await act(async () => root.render(<DeleteConversationDialog s={s} onClose={onClose} />));
    expect(mocks.useStore).toHaveBeenCalled();

    await act(async () => dialogButton(t("conversation-delete.delete_confirm"))!.click());
    await flush();
    expect(mocks.getState).toHaveBeenCalled();
    expect(mocks.deleteConversation).toHaveBeenCalledWith(s);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
