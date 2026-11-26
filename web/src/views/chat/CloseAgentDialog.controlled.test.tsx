// @vitest-environment jsdom
// Controlled panels supply explicit session/feature/worktree state and close/archive/cleanup/notice callbacks; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { AGENT_ARCHIVE_CLOSE_FEATURE, AGENT_ARCHIVES_FEATURE } from "../../lib/agent-archives";
import type { Session } from "../../lib/types";
import type { WorktreeStatus } from "../../lib/worktree-cleanup";
import type { CloseAgentDialogProps } from "./CloseAgentDialog";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(),
  getSession: vi.fn(),
  getState: vi.fn(),
  archiveAgent: vi.fn(),
  api: vi.fn(),
  navigate: vi.fn(),
  parse: vi.fn(),
  showsSession: vi.fn(),
  toast: vi.fn(),
  useAgentWorktree: vi.fn(),
  runCleanup: vi.fn(),
}));

vi.mock("../../lib/store", () => ({
  useStore: mocks.useStore,
  getSession: mocks.getSession,
  getState: mocks.getState,
}));
vi.mock("../../lib/agent-archives", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/agent-archives")>(),
  archiveAgent: mocks.archiveAgent,
}));
vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  api: mocks.api,
}));
vi.mock("../../lib/router", () => ({
  navigate: mocks.navigate,
  parse: mocks.parse,
  showsSession: mocks.showsSession,
}));
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: mocks.toast,
}));
vi.mock("./SessionWorktreeCleanup", async (importOriginal) => ({
  ...await importOriginal<typeof import("./SessionWorktreeCleanup")>(),
  useAgentWorktree: mocks.useAgentWorktree,
  runCleanup: mocks.runCleanup,
}));

const { appArchiveDeps } = await import("../../lib/agent-archives");
const { CloseAgentDialog } = await import("./CloseAgentDialog");
const { initI18n, t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const archiveFeatures = [AGENT_ARCHIVES_FEATURE, AGENT_ARCHIVE_CLOSE_FEATURE] as const;

const idleSession: Session = {
  machine_id: "example-machine",
  session_id: "example-session",
  title: "Controlled agent",
  cwd: "/repo/worktrees/example",
  status: "idle",
  state: "idle",
  agent: "claude",
  pane_id: "w1:p1",
};

const cleanWorktree: WorktreeStatus = {
  worktree: true,
  ours: true,
  path: "/repo/worktrees/example",
  branch: "persona/example",
  dirty: 0,
  unmerged: 0,
};

let host: HTMLDivElement;
let root: Root;
let onClose: Mock<() => void>;
let onClosed: Mock<() => void>;

const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const mount = async (props: Partial<CloseAgentDialogProps> = {}) => {
  await act(async () => root.render(
    <CloseAgentDialog open session={idleSession} machineName="Example computer" onClose={onClose} {...props} />,
  ));
  await flush();
};

const dialog = () => document.querySelector("[role=dialog]")!;
const dialogButton = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("[role=dialog] button")].find((button) => button.textContent === label);
const removeRadio = () => [...document.querySelectorAll<HTMLButtonElement>("[role=radio]")].find((button) => button.textContent === t("chat-sessiondialogs.wt_remove"));

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.getSession).not.toHaveBeenCalled();
  expect(mocks.getState).not.toHaveBeenCalled();
  expect(mocks.useAgentWorktree).not.toHaveBeenCalled();
  expect(mocks.archiveAgent).not.toHaveBeenCalled();
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.runCleanup).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
  expect(mocks.parse).not.toHaveBeenCalled();
  expect(mocks.showsSession).not.toHaveBeenCalled();
};

beforeEach(async () => {
  await initI18n("en");
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }));
  mocks.useAgentWorktree.mockReturnValue(null);
  mocks.runCleanup.mockResolvedValue(undefined);
  mocks.parse.mockReturnValue({ name: "session", mid: idleSession.machine_id, sid: idleSession.session_id });
  mocks.showsSession.mockReturnValue(false);
  onClose = vi.fn();
  onClosed = vi.fn();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("CloseAgentDialog with controlled data and callbacks", () => {
  it("archives an idle agent and cleans up its worktree without touching default singletons", async () => {
    const onArchiveClose = vi.fn().mockResolvedValue({ ok: true });
    const onCloseSession = vi.fn().mockResolvedValue(undefined);
    const onCleanupWorktree = vi.fn().mockResolvedValue(undefined);
    const onNotice = vi.fn();

    await mount({
      liveSession: idleSession,
      features: archiveFeatures,
      worktree: cleanWorktree,
      onClosed,
      onArchiveClose,
      onCloseSession,
      onCleanupWorktree,
      onNotice,
    });

    expect(dialog().textContent).toContain("persona/example");
    await act(async () => removeRadio()!.click());
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();

    expect(onArchiveClose).toHaveBeenCalledWith(idleSession);
    expect(onCloseSession).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClosed).not.toHaveBeenCalled();
    expect(onCleanupWorktree).toHaveBeenCalledWith(cleanWorktree, { remove: true, deleteBranch: false, confirmed: false });
    expectNoDefaultSingletons();
  });

  it("switches to close-anyway after a busy archive refusal and uses onCloseSession, onNotice and onClosed", async () => {
    const onArchiveClose = vi.fn().mockResolvedValue({ ok: false, busy: true });
    const onCloseSession = vi.fn().mockResolvedValue(undefined);
    const onCleanupWorktree = vi.fn();
    const onNotice = vi.fn();

    await mount({
      liveSession: idleSession,
      features: archiveFeatures,
      worktree: null,
      onClosed,
      onArchiveClose,
      onCloseSession,
      onCleanupWorktree,
      onNotice,
    });

    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();
    expect(onArchiveClose).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
    expect(dialog().querySelector("[data-close-busy]")?.getAttribute("data-close-busy")).toBe("working");
    expect(dialogButton(t("close-agent.close_anyway"))).toBeDefined();

    await act(async () => dialogButton(t("close-agent.close_anyway"))!.click());
    await flush();
    expect(onCloseSession).toHaveBeenCalledWith(idleSession);
    expect(onNotice).toHaveBeenCalledWith(t("close-agent.closed"), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClosed).toHaveBeenCalledTimes(1);
    expect(onCleanupWorktree).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("follows liveSession updates and dismisses without acting when liveSession becomes null", async () => {
    const onArchiveClose = vi.fn().mockResolvedValue({ ok: true });
    const onCloseSession = vi.fn().mockResolvedValue(undefined);
    const onNotice = vi.fn();

    await mount({
      liveSession: idleSession,
      features: archiveFeatures,
      worktree: null,
      onClosed,
      onArchiveClose,
      onCloseSession,
      onNotice,
    });
    expect(dialogButton(t("close-agent.archive_close"))).toBeDefined();

    const workingSession: Session = { ...idleSession, status: "working", state: "working" };
    await mount({
      liveSession: workingSession,
      features: archiveFeatures,
      worktree: null,
      onClosed,
      onArchiveClose,
      onCloseSession,
      onNotice,
    });
    expect(dialog().querySelector("[data-close-busy]")?.getAttribute("data-close-busy")).toBe("working");
    expect(dialogButton(t("close-agent.close_anyway"))).toBeDefined();

    await mount({
      liveSession: null,
      features: archiveFeatures,
      worktree: null,
      onClosed,
      onArchiveClose,
      onCloseSession,
      onNotice,
    });
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onArchiveClose).not.toHaveBeenCalled();
    expect(onCloseSession).not.toHaveBeenCalled();
    expect(onClosed).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("reports controlled close failures and post-close worktree cleanup failures through onNotice", async () => {
    const onCloseSession = vi.fn().mockRejectedValueOnce(new Error("node unreachable")).mockResolvedValueOnce(undefined);
    const onCleanupWorktree = vi.fn().mockRejectedValue(new Error("worktree busy"));
    const onNotice = vi.fn();

    await mount({
      liveSession: idleSession,
      features: [],
      worktree: cleanWorktree,
      onClosed,
      onCloseSession,
      onCleanupWorktree,
      onNotice,
    });

    await act(async () => removeRadio()!.click());
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();
    expect(onNotice).toHaveBeenCalledWith(t("close-agent.close_failed", { error: "node unreachable" }), "error");
    expect(onClose).not.toHaveBeenCalled();
    expect(onClosed).not.toHaveBeenCalled();
    expect(onCleanupWorktree).not.toHaveBeenCalled();

    onNotice.mockClear();
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onClosed).toHaveBeenCalledTimes(1);
    expect(onNotice).toHaveBeenNthCalledWith(1, t("close-agent.closed"), "ok");
    expect(onNotice).toHaveBeenNthCalledWith(2, t("chat-sessiondialogs.wt_remove_failed", { error: "worktree busy" }), "error");
    expectNoDefaultSingletons();
  });

  it("resets worktree cleanup and loss confirmation to KEEP when switching sessions while open", async () => {
    const sessionA = idleSession;
    const worktreeA: WorktreeStatus = {
      worktree: true,
      ours: true,
      path: "/repo/worktrees/session-a",
      branch: "persona/session-a",
      main_branch: "main",
      dirty: 2,
      unmerged: 1,
    };
    const sessionB: Session = {
      ...idleSession,
      session_id: "other-session",
      title: "Other controlled agent",
      cwd: "/repo/worktrees/session-b",
    };
    const worktreeB: WorktreeStatus = {
      worktree: true,
      ours: true,
      path: "/repo/worktrees/session-b",
      branch: "persona/session-b",
      main_branch: "main",
      dirty: 3,
      unmerged: 2,
    };
    const onArchiveClose = vi.fn().mockResolvedValue({ ok: true });
    const onCleanupWorktree = vi.fn().mockResolvedValue(undefined);
    const onNotice = vi.fn();

    await mount({
      session: sessionA,
      liveSession: sessionA,
      features: archiveFeatures,
      worktree: worktreeA,
      onClosed,
      onArchiveClose,
      onCleanupWorktree,
      onNotice,
    });

    await act(async () => removeRadio()!.click());
    const deleteBranchA = document.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t("chat-sessiondialogs.wt_delete_branch", { branch: "persona/session-a" })}"]`);
    const confirmLoss = () => document.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t("chat-sessiondialogs.wt_confirm_loss")}"]`);
    expect(deleteBranchA).not.toBeNull();
    await act(async () => deleteBranchA!.click());
    expect(dialogButton(t("close-agent.archive_close"))?.disabled).toBe(true);
    await act(async () => confirmLoss()!.click());
    expect(dialogButton(t("close-agent.archive_close"))?.disabled).toBe(false);

    // Switch to session B while keeping the same dialog open.
    await mount({
      session: sessionB,
      liveSession: sessionB,
      features: archiveFeatures,
      worktree: worktreeB,
      onClosed,
      onArchiveClose,
      onCleanupWorktree,
      onNotice,
    });

    const keepRadio = [...document.querySelectorAll<HTMLButtonElement>("[role=radio]")].find((button) => button.textContent === t("chat-sessiondialogs.wt_keep"));
    expect(keepRadio?.getAttribute("aria-checked")).toBe("true");
    expect(removeRadio()?.getAttribute("aria-checked")).toBe("false");

    // Selecting Remove on B requires fresh loss confirmation and disables the confirm button until confirmed.
    await act(async () => removeRadio()!.click());
    expect(dialogButton(t("close-agent.archive_close"))?.disabled).toBe(true);
    expect(confirmLoss()?.getAttribute("aria-checked")).toBe("false");

    // Switching back to KEEP and confirming B must not remove B's worktree.
    await act(async () => keepRadio!.click());
    expect(dialogButton(t("close-agent.archive_close"))?.disabled).toBe(false);
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();

    expect(onArchiveClose).toHaveBeenCalledWith(sessionB);
    expect(onCleanupWorktree).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("skips worktree cleanup when archive?close=1 leaves the agent still open, but cleans up when closed or not_running", async () => {
    const onCleanupWorktree = vi.fn().mockResolvedValue(undefined);
    const onNotice = vi.fn();
    for (const close of [
      { closed: false, outcome: "refused", reason: "agent busy" },
      { closed: false, outcome: "failed", detail: "process error" },
      { closed: false, outcome: "offline" },
    ]) {
      onClose.mockClear();
      onCleanupWorktree.mockClear();
      const archive = vi.fn().mockResolvedValue({ ok: true, close });
      await mount({
        liveSession: idleSession,
        features: archiveFeatures,
        worktree: cleanWorktree,
        onClosed,
        onArchiveClose: archive,
        onCleanupWorktree,
        onNotice,
      });
      if (removeRadio()?.getAttribute("aria-checked") !== "true") await act(async () => removeRadio()!.click());
      await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
      await flush();

      expect(archive).toHaveBeenCalledWith(idleSession);
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(onCleanupWorktree).not.toHaveBeenCalled();
    }

    for (const close of [
      { closed: true, outcome: "closed" },
      { closed: false, outcome: "not_running" },
    ]) {
      onClose.mockClear();
      onCleanupWorktree.mockClear();
      const archive = vi.fn().mockResolvedValue({ ok: true, close });
      await mount({
        liveSession: idleSession,
        features: archiveFeatures,
        worktree: cleanWorktree,
        onClosed,
        onArchiveClose: archive,
        onCleanupWorktree,
        onNotice,
      });
      await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
      await flush();

      expect(archive).toHaveBeenCalledWith(idleSession);
      expect(onCleanupWorktree).toHaveBeenCalledWith(cleanWorktree, { remove: true, deleteBranch: false, confirmed: false });
    }
    expectNoDefaultSingletons();
  });
});

describe("CloseAgentDialog default compatibility", () => {
  it("closes off-page and archived-but-open sessions not present in the store, while still dismissing a live session that vanished", async () => {
    mocks.getState.mockReturnValue({ features: [...archiveFeatures] });
    mocks.useStore.mockImplementation(<T,>(selector: (state: { features: readonly string[] }) => T) => selector({ features: [...archiveFeatures] }));
    mocks.getSession.mockReturnValue(undefined);
    mocks.archiveAgent.mockResolvedValue({ ok: true, close: { closed: true, outcome: "closed" } });
    mocks.api.mockResolvedValue({});

    // Off-page / filtered / grouped agent never in store: confirming archives & closes using the opened snapshot.
    await mount();
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();
    expect(mocks.archiveAgent).toHaveBeenCalledWith(idleSession, true, appArchiveDeps);
    expect(onClose).toHaveBeenCalledTimes(1);

    // Archived-but-still-open agent dropped from store: confirming sends plain POST .../close.
    onClose.mockClear();
    mocks.archiveAgent.mockClear();
    const archivedOpen: Session = { ...idleSession, session_id: "archived-open", archived: true };
    await mount({ session: archivedOpen });
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();
    expect(mocks.api).toHaveBeenCalledWith("/sessions/example-machine/archived-open/close", { method: "POST" });
    expect(mocks.archiveAgent).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);

    // Live session present in store when opened, then vanishes before confirm: still dismisses without acting.
    onClose.mockClear();
    mocks.api.mockClear();
    mocks.archiveAgent.mockClear();
    const vanishing: Session = { ...idleSession, session_id: "vanishing-session" };
    mocks.getSession.mockReturnValue(vanishing);
    await mount({ session: vanishing });
    mocks.getSession.mockReturnValue(undefined);
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();
    expect(mocks.archiveAgent).not.toHaveBeenCalled();
    expect(mocks.api).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);

    // Reopening the same dialog (same machine_id/session_id, open: true -> false -> true) after the session left
    // the store clears stale `hadLive` so closing the off-page/archived snapshot still works.
    onClose.mockClear();
    await mount({ open: false, session: vanishing });
    await mount({ open: true, session: { ...vanishing, archived: true } });
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();
    expect(mocks.api).toHaveBeenCalledWith("/sessions/example-machine/vanishing-session/close", { method: "POST" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("fails closed on default archiveAgent when close outcome is omitted (archived_close_unknown)", async () => {
    mocks.getSession.mockReturnValue(idleSession);
    mocks.getState.mockReturnValue({ features: [...archiveFeatures] });
    mocks.useStore.mockImplementation(<T,>(selector: (state: { features: readonly string[] }) => T) => selector({ features: [...archiveFeatures] }));
    mocks.useAgentWorktree.mockReturnValue(cleanWorktree);
    mocks.archiveAgent.mockResolvedValue({ ok: true });

    await mount();
    await act(async () => removeRadio()!.click());
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();

    expect(mocks.archiveAgent).toHaveBeenCalledWith(idleSession, true, appArchiveDeps);
    expect(mocks.runCleanup).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("uses the default store, worktree hook, archiveAgent and runCleanup when optional props are omitted", async () => {
    mocks.getSession.mockReturnValue(idleSession);
    mocks.getState.mockReturnValue({ features: [...archiveFeatures] });
    mocks.useStore.mockImplementation(<T,>(selector: (state: { features: readonly string[] }) => T) => selector({ features: [...archiveFeatures] }));
    mocks.useAgentWorktree.mockReturnValue(cleanWorktree);
    mocks.archiveAgent.mockResolvedValue({ ok: true, close: { closed: true, outcome: "closed" } });

    await mount();
    await act(async () => removeRadio()!.click());
    await act(async () => dialogButton(t("close-agent.archive_close"))!.click());
    await flush();

    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.getSession).toHaveBeenCalledWith(idleSession.machine_id, idleSession.session_id);
    expect(mocks.getState).toHaveBeenCalled();
    expect(mocks.useAgentWorktree).toHaveBeenCalledWith(idleSession, true);
    expect(mocks.archiveAgent).toHaveBeenCalledWith(idleSession, true, appArchiveDeps);
    expect(mocks.runCleanup).toHaveBeenCalledWith(idleSession.machine_id, cleanWorktree, { remove: true, deleteBranch: false, confirmed: false }, undefined);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("uses default api close, toast and home navigation when optional props are omitted, and toasts close errors", async () => {
    mocks.getSession.mockReturnValue(idleSession);
    mocks.getState.mockReturnValue({ features: [] });
    mocks.useStore.mockImplementation(<T,>(selector: (state: { features: readonly string[] }) => T) => selector({ features: [] }));
    mocks.showsSession.mockReturnValue(true);
    mocks.api.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error("hub offline"));

    await mount();
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();

    expect(mocks.api).toHaveBeenCalledWith("/sessions/example-machine/example-session/close", { method: "POST" });
    expect(mocks.toast).toHaveBeenCalledWith(t("close-agent.closed"), "ok");
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "home" });
    expect(onClose).toHaveBeenCalledTimes(1);

    onClose.mockClear();
    mocks.toast.mockClear();
    mocks.navigate.mockClear();
    await act(async () => dialogButton(t("close-agent.close_agent"))!.click());
    await flush();

    expect(mocks.toast).toHaveBeenCalledWith(t("close-agent.close_failed", { error: "hub offline" }), "error");
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});
