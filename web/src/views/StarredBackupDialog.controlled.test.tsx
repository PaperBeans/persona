// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { StarredBackup } from "../lib/starred-backup";
import type { Star } from "../lib/starred-rows";

const globals = vi.hoisted(() => ({
  api: vi.fn(),
  getToken: vi.fn(() => "default-token"),
  credentialOwner: vi.fn(() => "default-owner"),
  ownerListeners: new Set<() => void>(),
  subscribeCredentialOwner: vi.fn((listener: () => void) => {
    globals.ownerListeners.add(listener);
    return () => { globals.ownerListeners.delete(listener); };
  }),
  shownManager: vi.fn(() => "default-manager"),
  stampedHeaders: vi.fn((manager: string) => ({ "X-Persona-Manager": manager })),
  managerListeners: new Set<() => void>(),
  defaultManagerListeners: new Set<() => void>(),
  onManagerChoice: vi.fn((listener: () => void) => {
    globals.managerListeners.add(listener);
    return () => { globals.managerListeners.delete(listener); };
  }),
  onDefaultManager: vi.fn((listener: () => void) => {
    globals.defaultManagerListeners.add(listener);
    return () => { globals.defaultManagerListeners.delete(listener); };
  }),
  toast: vi.fn<(message: string, tone?: string) => void>(),
}));

vi.mock("../lib/api", async (actual) => {
  const real = await actual<typeof import("../lib/api")>();
  return {
    ...real,
    api: (...args: unknown[]) => globals.api(...args),
    getToken: () => globals.getToken(),
    credentialOwner: () => globals.credentialOwner(),
    subscribeCredentialOwner: (listener: () => void) => globals.subscribeCredentialOwner(listener),
  };
});

vi.mock("../lib/manager-choice", async (actual) => {
  const real = await actual<typeof import("../lib/manager-choice")>();
  return {
    ...real,
    shownManager: () => globals.shownManager(),
    stampedHeaders: (manager: string) => globals.stampedHeaders(manager),
    onManagerChoice: (listener: () => void) => globals.onManagerChoice(listener),
    onDefaultManager: (listener: () => void) => globals.onDefaultManager(listener),
  };
});

vi.mock("../ui/atoms", async (actual) => {
  const real = await actual<typeof import("../ui/atoms")>();
  return {
    ...real,
    toast: (message: string, tone?: string) => globals.toast(message, tone),
  };
});

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});

import { initI18n, t } from "../i18n";
import { ApiError } from "../lib/api";
import {
  StarredBackupDialog,
  StarredBackupDialogView,
  type StarredBackupDialogDeps,
  type StarredBackupDownload,
  type StarredBackupRequestContext,
  type StarredImportPreview,
  type StarredImportResult,
} from "./StarredBackupDialog";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const sampleStar: Star = {
  id: "sm" + "b".repeat(32),
  machine_id: "m1",
  session_id: "s1",
  item_id: "i1",
  conversation: "assistant",
  role: "assistant",
  title: "Morning note",
  speaker: "Persona",
  text: "Remember to check the backup.",
  starred_at: "2026-11-21T03:04:00Z",
  omitted_chars: 12,
};

const bundle: StarredBackup = {
  format: "persona.starred",
  version: 1,
  messages: [{
    key: "sm" + "a".repeat(32),
    conversation: "assistant",
    role: "assistant",
    title: "Reply",
    speaker: "Persona",
    text: "A saved reply.",
    starred_at: "2026-11-21T03:04:00Z",
  }],
};
const rawBundle = JSON.stringify(bundle, null, 2) + "\n";
const toBytes = (text: string) => new TextEncoder().encode(text).buffer;
const makePreview = (revision: number, overrides: Partial<StarredImportPreview> = {}): StarredImportPreview => ({
  revision,
  total: 1,
  to_add: 1,
  already_saved: 0,
  available: 5,
  ...overrides,
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

let container: HTMLDivElement;
let root: Root;

const button = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent === label);
const fileInput = () => document.querySelector<HTMLInputElement>('input[type="file"]');
const alertText = () => [...document.querySelectorAll('[role="dialog"] [role="alert"]')].map((n) => n.textContent).join("\n");
const statusText = () => [...document.querySelectorAll('[role="dialog"] [role="status"]')].map((n) => n.textContent).join("\n");

async function clickButton(label: string) {
  await act(async () => {
    button(label)!.click();
  });
}

async function pickFile(size = 500, read = vi.fn(async () => toBytes(rawBundle)), name = "backup.json") {
  const file = new File([""], name, { type: "application/json" });
  Object.defineProperties(file, { size: { value: size }, arrayBuffer: { value: read } });
  Object.defineProperty(fileInput()!, "files", { configurable: true, value: [file] });
  await act(async () => {
    fileInput()!.dispatchEvent(new Event("change", { bubbles: true }));
  });
  return read;
}

function expectNoGlobalCalls() {
  expect(globals.api).not.toHaveBeenCalled();
  expect(globals.getToken).not.toHaveBeenCalled();
  expect(globals.credentialOwner).not.toHaveBeenCalled();
  expect(globals.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(globals.shownManager).not.toHaveBeenCalled();
  expect(globals.stampedHeaders).not.toHaveBeenCalled();
  expect(globals.onManagerChoice).not.toHaveBeenCalled();
  expect(globals.onDefaultManager).not.toHaveBeenCalled();
  expect(globals.toast).not.toHaveBeenCalled();
}

beforeEach(async () => {
  await initI18n("en");
  globals.api.mockReset();
  globals.getToken.mockReset().mockReturnValue("default-token");
  globals.credentialOwner.mockReset().mockReturnValue("default-owner");
  globals.subscribeCredentialOwner.mockClear();
  globals.shownManager.mockReset().mockReturnValue("default-manager");
  globals.stampedHeaders.mockClear();
  globals.onManagerChoice.mockClear();
  globals.onDefaultManager.mockClear();
  globals.toast.mockReset();
  globals.ownerListeners.clear();
  globals.managerListeners.clear();
  globals.defaultManagerListeners.clear();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

describe("StarredBackupDialog controlled mode", () => {
  it("runs export, capability check, preview, and restore through injected deps without touching globals", async () => {
    const createObjectURL = vi.spyOn(URL, "createObjectURL");
    const checkImport = vi.fn(async (_ctx: StarredBackupRequestContext) => ({ can_import: true }));
    const previewImport = vi.fn(async (_raw: string, _ctx: StarredBackupRequestContext) => makePreview(4));
    const restoreImport = vi.fn(async (_b: StarredBackup, _rev: number, _ctx: StarredBackupRequestContext): Promise<StarredImportResult> => ({
      revision: 5,
      added: 1,
      already_saved: 0,
    }));
    const exportFile = vi.fn((_file: StarredBackupDownload) => {});
    const notify = vi.fn();
    const onClose = vi.fn();
    const onRestored = vi.fn();

    const deps: StarredBackupDialogDeps = {
      manager: "mgr-alpha",
      credentialOwner: "acct-1",
      checkImport,
      previewImport,
      restoreImport,
      exportFile,
      notify,
    };

    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[sampleStar]}
          canWrite
          speaker={(star) => star.speaker}
          onClose={onClose}
          onRestored={onRestored}
          deps={deps}
        />,
      );
    });

    expect(checkImport).toHaveBeenCalledOnce();
    const checkCtx = checkImport.mock.calls[0][0];
    expect(checkCtx.manager).toBe("mgr-alpha");
    expect(checkCtx.credentialOwner).toBe("acct-1");
    expect(checkCtx.signal.aborted).toBe(false);
    expect(checkCtx.isCurrent()).toBe(true);

    await clickButton(t("starred.backup_json"));
    await clickButton(t("starred.backup_markdown"));
    expect(exportFile).toHaveBeenCalledTimes(2);
    expect(exportFile.mock.calls[0][0]).toMatchObject({
      format: "json",
      mimeType: "application/json;charset=utf-8",
    });
    expect(exportFile.mock.calls[0][0].filename).toMatch(/^persona-starred-\d{4}-\d{2}-\d{2}\.json$/);
    expect(JSON.parse(exportFile.mock.calls[0][0].content).messages).toHaveLength(1);
    expect(exportFile.mock.calls[1][0]).toMatchObject({
      format: "md",
      mimeType: "text/markdown;charset=utf-8",
    });
    expect(exportFile.mock.calls[1][0].content).toContain("Morning note");
    expect(exportFile.mock.calls[1][0].content).toContain("Persona");
    expect(createObjectURL).not.toHaveBeenCalled();

    await pickFile(500);
    await clickButton(t("starred.backup_preview"));
    expect(previewImport).toHaveBeenCalledWith(rawBundle, expect.objectContaining({ manager: "mgr-alpha", credentialOwner: "acct-1" }));
    expect(button(t("starred.backup_restore"))?.disabled).toBe(false);

    await clickButton(t("starred.backup_restore"));
    expect(restoreImport).toHaveBeenCalledWith(bundle, 4, expect.objectContaining({ manager: "mgr-alpha", credentialOwner: "acct-1" }));
    expect(onRestored).toHaveBeenCalledOnce();
    expect(notify).toHaveBeenCalledWith(t("starred.backup_restored", { count: 1 }), "ok");
    expect(statusText()).toContain(t("starred.backup_result", { added: 1, already: 0 }));
    expect(button(t("starred.backup_restore"))).toBeUndefined();

    await clickButton(t("starred.backup_done"));
    expect(onClose).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
    createObjectURL.mockRestore();
  });

  it("echoes capability, preview, restore, conflict, capacity, and export errors cleanly and supports retry", async () => {
    const checkImport = vi.fn()
      .mockRejectedValueOnce(new Error("Capability timeout"))
      .mockResolvedValueOnce({ can_import: true });
    const previewImport = vi.fn()
      .mockRejectedValueOnce(new Error("Preview rejected"))
      .mockResolvedValueOnce(makePreview(1, { to_add: 0, already_saved: 1, available: 5 }))
      .mockResolvedValueOnce(makePreview(2, { to_add: 4, already_saved: 0, available: 2 }))
      .mockResolvedValueOnce(makePreview(3));
    const restoreImport = vi.fn()
      .mockRejectedValueOnce(new ApiError(409, "Conflict", {}))
      .mockRejectedValueOnce(new Error("Write failed"));
    const exportFile = vi.fn()
      .mockImplementationOnce(() => { throw new Error("Export sync failed"); })
      .mockRejectedValueOnce(new Error("Export async failed"));
    const notify = vi.fn();
    const onRestored = vi.fn();

    const deps: StarredBackupDialogDeps = {
      manager: "mgr-err",
      credentialOwner: "acct-err",
      checkImport,
      previewImport,
      restoreImport,
      exportFile,
      notify,
    };

    await act(async () => {
      root.render(
        <StarredBackupDialog stars={[sampleStar]} canWrite speaker={(s) => s.speaker} onClose={vi.fn()} onRestored={onRestored} deps={deps} />,
      );
    });

    expect(alertText()).toContain("Capability timeout");
    await clickButton(t("starred.backup_json"));
    await clickButton(t("starred.backup_markdown"));
    await act(async () => { await Promise.resolve(); });
    expect(notify).toHaveBeenNthCalledWith(1, "Export sync failed", "error");
    expect(notify).toHaveBeenNthCalledWith(2, "Export async failed", "error");

    await clickButton(t("starred.retry"));
    expect(checkImport).toHaveBeenCalledTimes(2);
    expect(fileInput()).not.toBeNull();

    await pickFile(500);
    await clickButton(t("starred.backup_preview"));
    expect(alertText()).toContain("Preview rejected");

    await clickButton(t("starred.backup_preview"));
    expect(button(t("starred.backup_restore"))?.disabled).toBe(true);
    expect(statusText()).toContain(t("starred.backup_no_changes"));

    await clickButton(t("starred.backup_preview"));
    expect(button(t("starred.backup_restore"))?.disabled).toBe(true);
    expect(statusText()).toContain(t("starred.backup_capacity"));

    await clickButton(t("starred.backup_preview"));
    expect(button(t("starred.backup_restore"))?.disabled).toBe(false);
    await clickButton(t("starred.backup_restore"));
    expect(alertText()).toContain(t("starred.backup_conflict"));
    expect(button(t("starred.backup_restore"))).toBeUndefined();

    previewImport.mockResolvedValueOnce(makePreview(4));
    await clickButton(t("starred.backup_preview"));
    await clickButton(t("starred.backup_restore"));
    expect(alertText()).toContain("Write failed");
    expect(onRestored).not.toHaveBeenCalled();
    expectNoGlobalCalls();
  });

  it("treats HTTP 403/404/405, can_import=false, and canWrite=false as unsupported", async () => {
    const checkImport = vi.fn()
      .mockRejectedValueOnce(new ApiError(403, "Forbidden", {}))
      .mockResolvedValueOnce({ can_import: false });
    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[]}
          canWrite
          speaker={(s) => s.speaker}
          onClose={vi.fn()}
          onRestored={vi.fn()}
          deps={{ manager: "m1", credentialOwner: "u1", checkImport }}
        />,
      );
    });
    expect(fileInput()).toBeNull();
    expect(alertText()).toBe("");

    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[]}
          canWrite
          speaker={(s) => s.speaker}
          onClose={vi.fn()}
          onRestored={vi.fn()}
          deps={{ manager: "m2", credentialOwner: "u1", checkImport }}
        />,
      );
    });
    expect(checkImport).toHaveBeenCalledTimes(2);
    expect(fileInput()).toBeNull();
    expect(alertText()).toBe("");

    checkImport.mockReset();
    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[]}
          canWrite={false}
          speaker={(s) => s.speaker}
          onClose={vi.fn()}
          onRestored={vi.fn()}
          deps={{ manager: "m2", credentialOwner: "u1", checkImport }}
        />,
      );
    });
    expect(checkImport).not.toHaveBeenCalled();
    expect(fileInput()).toBeNull();
    expectNoGlobalCalls();
  });

  it("resets state and fences stale ABA callbacks when controlled manager or credentialOwner changes", async () => {
    const check1 = deferred<{ can_import: boolean }>();
    const check2 = deferred<{ can_import: boolean }>();
    const check3 = deferred<{ can_import: boolean }>();
    const restoreA1 = deferred<StarredImportResult>();
    const previewA2 = deferred<StarredImportPreview>();

    const checkImport = vi.fn()
      .mockResolvedValueOnce({ can_import: true })
      .mockReturnValueOnce(check1.promise)
      .mockReturnValueOnce(check2.promise)
      .mockReturnValueOnce(check3.promise);
    const previewImport = vi.fn()
      .mockResolvedValueOnce(makePreview(1))
      .mockReturnValueOnce(previewA2.promise);
    const restoreImport = vi.fn().mockReturnValueOnce(restoreA1.promise);
    const notify = vi.fn();
    const onRestored = vi.fn();
    const onClose = vi.fn();

    const renderWith = (manager: string, owner: string) =>
      act(async () => {
        root.render(
          <StarredBackupDialog
            stars={[sampleStar]}
            canWrite
            speaker={(s) => s.speaker}
            onClose={onClose}
            onRestored={onRestored}
            deps={{ manager, credentialOwner: owner, checkImport, previewImport, restoreImport, notify }}
          />,
        );
      });

    await renderWith("mgr-a", "owner-1");
    await pickFile(500);
    // Re-rendering with a fresh inline deps object for the SAME scope must not reset state or re-check.
    await renderWith("mgr-a", "owner-1");
    expect(checkImport).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain("backup.json");

    await clickButton(t("starred.backup_preview"));
    expect(button(t("starred.backup_restore"))).toBeDefined();

    // Start restore in mgr-a/owner-1; close is blocked while pending.
    await clickButton(t("starred.backup_restore"));
    const restoreCtx = restoreImport.mock.calls[0][2];
    expect(restoreCtx.signal.aborted).toBe(false);
    expect(button(t("starred.backup_done"))?.disabled).toBe(true);

    // Switch manager mgr-a -> mgr-b: aborts in-flight restore, clears file/preview/working, re-checks mgr-b.
    await renderWith("mgr-b", "owner-1");
    expect(restoreCtx.signal.aborted).toBe(true);
    expect(restoreCtx.isCurrent()).toBe(false);
    expect(statusText()).toContain(t("common.loading"));
    expect(button(t("starred.backup_restore"))).toBeUndefined();
    expect(button(t("starred.backup_done"))?.disabled).toBe(false);

    // Switch back mgr-b -> mgr-a (ABA) while mgr-b's check1 is still pending, resolve mgr-a's check2.
    await renderWith("mgr-a", "owner-1");
    await act(async () => { check2.resolve({ can_import: true }); });
    expect(fileInput()).not.toBeNull();
    expect(document.body.textContent).not.toContain("backup.json");

    // Start a fresh preview in the new mgr-a scope, then settle stale mgr-b check1 and stale mgr-a restoreA1.
    await pickFile(500, undefined, "fresh.json");
    await clickButton(t("starred.backup_preview"));
    expect(button(t("starred.backup_done"))?.disabled).toBe(true);

    await act(async () => {
      check1.reject(new Error("stale mgr-b check"));
      restoreA1.resolve({ revision: 9, added: 1, already_saved: 0 });
    });
    expect(onRestored).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();
    expect(alertText()).toBe("");
    // Stale restoreA1 finally block must not clear the active previewA2 working state.
    expect(button(t("starred.backup_done"))?.disabled).toBe(true);

    // Now switch credentialOwner owner-1 -> owner-2 while previewA2 is pending.
    await renderWith("mgr-a", "owner-2");
    await act(async () => {
      previewA2.reject(new Error("stale owner-1 preview"));
      check3.resolve({ can_import: true });
    });
    expect(alertText()).toBe("");
    expect(button(t("starred.backup_done"))?.disabled).toBe(false);
    expect(document.body.textContent).not.toContain("fresh.json");
    expectNoGlobalCalls();
  });

  it("rebuilds scope, resets working state, and rechecks when getter-only identity changes with stable getter refs", async () => {
    let selectedManager = "mgr-a";
    let selectedOwner = "owner-1";
    const getManager = () => selectedManager;
    const getCredentialOwner = () => selectedOwner;

    const previewA = deferred<StarredImportPreview>();
    const previewB = deferred<StarredImportPreview>();
    const checkB = deferred<{ can_import: boolean }>();
    const checkOwner2 = deferred<{ can_import: boolean }>();

    const checkImport = vi.fn()
      .mockResolvedValueOnce({ can_import: true })
      .mockReturnValueOnce(checkB.promise)
      .mockReturnValueOnce(checkOwner2.promise);
    const previewImport = vi.fn()
      .mockReturnValueOnce(previewA.promise)
      .mockReturnValueOnce(previewB.promise);
    const restoreImport = vi.fn();

    const renderDialog = () =>
      act(async () => {
        root.render(
          <StarredBackupDialog
            stars={[sampleStar]}
            canWrite
            speaker={(s) => s.speaker}
            onClose={vi.fn()}
            onRestored={vi.fn()}
            deps={{ getManager, getCredentialOwner, checkImport, previewImport, restoreImport }}
          />,
        );
      });

    await renderDialog();
    expect(checkImport).toHaveBeenCalledTimes(1);
    expect(checkImport.mock.calls[0][0].manager).toBe("mgr-a");
    expect(checkImport.mock.calls[0][0].credentialOwner).toBe("owner-1");

    // Start preview under mgr-a; dialog enters working="preview".
    await pickFile(500);
    await clickButton(t("starred.backup_preview"));
    const previewACtx = previewImport.mock.calls[0][1];
    expect(previewACtx.signal.aborted).toBe(false);
    expect(button(t("starred.backup_done"))?.disabled).toBe(true);

    // Switch getter return value to mgr-b (same getManager function reference, no subscribeManager) and re-render.
    selectedManager = "mgr-b";
    await renderDialog();
    expect(previewACtx.signal.aborted).toBe(true);
    expect(previewACtx.isCurrent()).toBe(false);
    expect(checkImport).toHaveBeenCalledTimes(2);
    expect(checkImport.mock.calls[1][0].manager).toBe("mgr-b");
    expect(button(t("starred.backup_done"))?.disabled).toBe(false);

    // Resolve mgr-b capability check and settle stale previewA; controls must not stay stuck in working="preview".
    await act(async () => {
      checkB.resolve({ can_import: true });
      previewA.resolve(makePreview(1));
    });
    expect(button(t("starred.backup_done"))?.disabled).toBe(false);
    expect(button(t("starred.backup_choose_file"))?.disabled).toBe(false);
    expect(button(t("starred.backup_restore"))).toBeUndefined();

    // Start a preview on mgr-b, then switch getCredentialOwner return value (same function ref, no subscribeCredentialOwner).
    await pickFile(500, undefined, "mgr-b.json");
    await clickButton(t("starred.backup_preview"));
    const previewBCtx = previewImport.mock.calls[1][1];
    expect(button(t("starred.backup_done"))?.disabled).toBe(true);

    selectedOwner = "owner-2";
    await renderDialog();
    expect(previewBCtx.signal.aborted).toBe(true);
    expect(checkImport).toHaveBeenCalledTimes(3);
    expect(checkImport.mock.calls[2][0]).toMatchObject({ manager: "mgr-b", credentialOwner: "owner-2" });

    await act(async () => {
      checkOwner2.resolve({ can_import: true });
      previewB.reject(new Error("stale owner-1 preview"));
    });
    expect(alertText()).toBe("");
    expect(button(t("starred.backup_done"))?.disabled).toBe(false);
    expect(button(t("starred.backup_choose_file"))?.disabled).toBe(false);
    expectNoGlobalCalls();
  });

  it("retires scope, resets state, and closes on injected subscription change even after ABA switch back", async () => {
    let activeManager = "mgr-1";
    let activeOwner = "owner-1";
    const managerSubs = new Set<() => void>();
    const ownerSubs = new Set<() => void>();
    const fileRead = deferred<ArrayBuffer>();
    const exportFile = vi.fn();
    const previewImport = vi.fn();
    const onClose = vi.fn();

    const subscribeManager = (listener: () => void) => {
      managerSubs.add(listener);
      return () => { managerSubs.delete(listener); };
    };
    const subscribeCredentialOwner = (listener: () => void) => {
      ownerSubs.add(listener);
      return () => { ownerSubs.delete(listener); };
    };

    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[sampleStar]}
          canWrite
          speaker={(s) => s.speaker}
          onClose={onClose}
          onRestored={vi.fn()}
          deps={{
            getManager: () => activeManager,
            subscribeManager,
            getCredentialOwner: () => activeOwner,
            subscribeCredentialOwner,
            checkImport: async () => ({ can_import: true }),
            previewImport,
            restoreImport: vi.fn(),
            exportFile,
          }}
        />,
      );
    });

    await pickFile(500, vi.fn(() => fileRead.promise));
    await clickButton(t("starred.backup_preview"));

    // Flip credential owner and flip right back (ABA) while notifying subscriber.
    await act(async () => {
      activeOwner = "owner-2";
      ownerSubs.forEach((fn) => fn());
      activeOwner = "owner-1";
      fileRead.resolve(toBytes(rawBundle));
    });

    expect(onClose).toHaveBeenCalledOnce();
    expect(previewImport).not.toHaveBeenCalled();

    // Even if parent has not unmounted yet, retired scope refuses downloads.
    await clickButton(t("starred.backup_json"));
    expect(exportFile).not.toHaveBeenCalled();

    // Verify manager subscription also cleans up cleanly on unmount.
    expect(managerSubs.size).toBe(1);
    expect(ownerSubs.size).toBe(1);
    await act(async () => root.unmount());
    expect(managerSubs.size).toBe(0);
    expect(ownerSubs.size).toBe(0);
    expectNoGlobalCalls();
    activeManager = "mgr-2";
    expect(activeManager).toBe("mgr-2");
  });

  it("ignores out-of-order capability checks when retry overtakes an earlier failure", async () => {
    const first = deferred<{ can_import: boolean }>();
    const second = deferred<{ can_import: boolean }>();
    const checkImport = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[]}
          canWrite
          speaker={(s) => s.speaker}
          onClose={vi.fn()}
          onRestored={vi.fn()}
          deps={{ manager: "m1", credentialOwner: "u1", checkImport }}
        />,
      );
    });
    expect(statusText()).toContain(t("common.loading"));

    // Trigger a scope transition to m2 while m1's check is still in flight.
    await act(async () => {
      root.render(
        <StarredBackupDialog
          stars={[]}
          canWrite
          speaker={(s) => s.speaker}
          onClose={vi.fn()}
          onRestored={vi.fn()}
          deps={{ manager: "m2", credentialOwner: "u1", checkImport }}
        />,
      );
    });
    await act(async () => { second.resolve({ can_import: true }); });
    expect(fileInput()).not.toBeNull();

    await act(async () => { first.reject(new Error("late failure")); });
    expect(fileInput()).not.toBeNull();
    expect(alertText()).toBe("");
    expectNoGlobalCalls();
  });

  it("renders StarredBackupDialogView as a pure controlled view", async () => {
    const onClose = vi.fn();
    const onDownload = vi.fn();
    const onRetryCheck = vi.fn();
    const onPickFile = vi.fn();
    const onPreview = vi.fn();
    const onRestore = vi.fn();

    await act(async () => {
      root.render(
        <StarredBackupDialogView
          support="available"
          capabilityError=""
          file={new File(["{}"], "saved.json", { type: "application/json" })}
          reviewed={{ bundle, preview: makePreview(7) }}
          restored={{ revision: 8, added: 2, already_saved: 1 }}
          error=""
          onClose={onClose}
          onDownload={onDownload}
          onRetryCheck={onRetryCheck}
          onPickFile={onPickFile}
          onPreview={onPreview}
          onRestore={onRestore}
        />,
      );
    });

    expect(document.body.textContent).toContain("saved.json");
    expect(statusText()).toContain(t("starred.backup_result", { added: 2, already: 1 }));
    await clickButton(t("starred.backup_json"));
    await clickButton(t("starred.backup_markdown"));
    await clickButton(t("starred.backup_preview"));
    await clickButton(t("starred.backup_restore"));
    await clickButton(t("starred.backup_done"));
    expect(onDownload).toHaveBeenNthCalledWith(1, "json");
    expect(onDownload).toHaveBeenNthCalledWith(2, "md");
    expect(onPreview).toHaveBeenCalledOnce();
    expect(onRestore).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
  });
});

describe("StarredBackupDialog default self-wired mode", () => {
  it("falls back to global api, manager-choice, credentialOwner, Blob download, and toast when deps is omitted", async () => {
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:starred-test");
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    const onRestored = vi.fn();
    globals.api
      .mockResolvedValueOnce({ can_import: true })
      .mockResolvedValueOnce(makePreview(3))
      .mockResolvedValueOnce({ revision: 4, added: 1, already_saved: 0 });

    await act(async () => {
      root.render(
        <StarredBackupDialog stars={[sampleStar]} canWrite speaker={(s) => s.speaker} onClose={onClose} onRestored={onRestored} />,
      );
    });

    expect(globals.shownManager).toHaveBeenCalled();
    expect(globals.credentialOwner).toHaveBeenCalled();
    expect(globals.getToken).toHaveBeenCalled();
    expect(globals.onManagerChoice).toHaveBeenCalledOnce();
    expect(globals.onDefaultManager).toHaveBeenCalledOnce();
    expect(globals.subscribeCredentialOwner).toHaveBeenCalledOnce();

    await clickButton(t("starred.backup_json"));
    expect(createObjectURL).toHaveBeenCalledOnce();
    expect(anchorClick).toHaveBeenCalledOnce();

    await pickFile(500);
    await clickButton(t("starred.backup_preview"));
    await clickButton(t("starred.backup_restore"));
    expect(onRestored).toHaveBeenCalledOnce();
    expect(globals.toast).toHaveBeenCalledWith(t("starred.backup_restored", { count: 1 }), "ok");

    // Global credential owner change closes the dialog cleanly.
    globals.credentialOwner.mockReturnValue("next-owner");
    await act(async () => {
      globals.ownerListeners.forEach((fn) => fn());
    });
    expect(onClose).toHaveBeenCalledOnce();

    anchorClick.mockRestore();
    createObjectURL.mockRestore();
    revokeObjectURL.mockRestore();
  });
});
