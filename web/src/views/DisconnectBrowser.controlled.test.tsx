// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Revoke } from "../lib/disconnect";

const globals = vi.hoisted(() => ({
  revoke: vi.fn<() => Promise<Revoke>>(),
  signOut: vi.fn<(unrevoked?: string) => void>(),
  unauthorizedHandler: (() => {}) as () => void,
  handleUnauthorized: vi.fn((fn: () => void) => {
    const before = globals.unauthorizedHandler;
    globals.unauthorizedHandler = fn;
    return before;
  }),
  toast: vi.fn<(message: string, tone?: string) => void>(),
  chunkFails: false,
  confirmChunkLoaded: vi.fn(),
}));

vi.mock("../lib/disconnect", () => ({
  revokeThisBrowser: () => globals.revoke(),
  signOutHere: (unrevoked?: string) => globals.signOut(unrevoked),
}));

vi.mock("../lib/api", async (actual) => {
  const real = await actual<typeof import("../lib/api")>();
  return {
    ...real,
    handleUnauthorized: (fn: () => void) => globals.handleUnauthorized(fn),
  };
});

vi.mock("../ui/atoms", async (actual) => {
  const real = await actual<typeof import("../ui/atoms")>();
  return {
    ...real,
    toast: (message: string, tone?: string) => globals.toast(message, tone),
  };
});

vi.mock("./DisconnectConfirm", async (actual) => {
  const real = await actual<typeof import("./DisconnectConfirm")>();
  return {
    get DisconnectConfirm() {
      globals.confirmChunkLoaded();
      if (globals.chunkFails) throw new TypeError("Failed to fetch dynamically imported module: /src/views/DisconnectConfirm.tsx");
      return real.DisconnectConfirm;
    },
  };
});

import { initI18n, t } from "../i18n";
import {
  DisconnectBrowserDialog,
  useDisconnect,
  type DisconnectBrowserDeps,
  type DisconnectDialogViewProps,
  type DisconnectProblem,
} from "./DisconnectBrowser";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

let host: HTMLDivElement;
let root: Root;
const initialGlobalUnauthorized = () => {};

function expectNoGlobalCalls() {
  expect(globals.revoke).not.toHaveBeenCalled();
  expect(globals.signOut).not.toHaveBeenCalled();
  expect(globals.handleUnauthorized).not.toHaveBeenCalled();
  expect(globals.toast).not.toHaveBeenCalled();
  expect(globals.confirmChunkLoaded).not.toHaveBeenCalled();
}

function renderCustomDialog(props: DisconnectDialogViewProps) {
  return (
    <div
      data-custom-dialog
      data-open={String(props.open)}
      data-busy={String(props.busy)}
      data-problem={props.problem ? (props.problem.nothing ? "nothing" : props.problem.error) : ""}
    >
      <button type="button" data-cancel onClick={props.onCancel}>cancel</button>
      <button type="button" data-confirm onClick={props.onConfirm}>confirm</button>
      <button type="button" data-close-problem onClick={props.onProblemClose}>close-problem</button>
      <button type="button" data-sign-out onClick={props.onSignOut}>sign-out</button>
    </div>
  );
}

beforeEach(async () => {
  await initI18n("en");
  globals.revoke.mockReset();
  globals.signOut.mockReset();
  globals.unauthorizedHandler = initialGlobalUnauthorized;
  globals.handleUnauthorized.mockClear();
  globals.toast.mockReset();
  globals.chunkFails = false;
  globals.confirmChunkLoaded.mockReset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("useDisconnect controlled mode", () => {
  it("supports fully controlled dialog state, deps, and renderDialog with zero global singleton or chunk calls", async () => {
    let activeUnauth = () => {};
    const originalUnauth = activeUnauth;
    const revokeDeferred = deferred<Revoke>();
    const revoke = vi.fn(() => revokeDeferred.promise);
    const signOut = vi.fn();
    const setUnauth = vi.fn((fn: () => void) => {
      const before = activeUnauth;
      activeUnauth = fn;
      return before;
    });
    const notify = vi.fn();
    const onAskingChange = vi.fn();
    const onBusyChange = vi.fn();
    const onProblemChange = vi.fn();

    function ControlledHost() {
      const [asking, setAsking] = useState(false);
      const [busy, setBusy] = useState(false);
      const [problem, setProblem] = useState<DisconnectProblem | null>(null);
      const disconnect = useDisconnect({
        revoke,
        signOut,
        handleUnauthorized: setUnauth,
        notify,
        asking,
        onAskingChange: (next) => { onAskingChange(next); setAsking(next); },
        busy,
        onBusyChange: (next) => { onBusyChange(next); setBusy(next); },
        problem,
        onProblemChange: (next) => { onProblemChange(next); setProblem(next); },
        renderDialog: renderCustomDialog,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>{disconnect.busy ? "busy" : "idle"}</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<ControlledHost />));
    const dialog = () => host.querySelector<HTMLDivElement>("[data-custom-dialog]")!;
    expect(dialog().dataset.open).toBe("false");
    expect(dialog().dataset.busy).toBe("false");

    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    expect(onAskingChange).toHaveBeenCalledWith(true);
    expect(dialog().dataset.open).toBe("true");

    // Double-clicking confirm synchronously must invoke revoke only once.
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[data-confirm]")!.click();
      host.querySelector<HTMLButtonElement>("[data-confirm]")!.click();
    });
    expect(revoke).toHaveBeenCalledOnce();
    expect(onBusyChange).toHaveBeenCalledWith(true);
    expect(activeUnauth).not.toBe(originalUnauth);
    expect(host.querySelector<HTMLButtonElement>("[data-start]")!.textContent).toBe("busy");

    // Actions while busy are ignored.
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[data-cancel]")!.click();
      host.querySelector<HTMLButtonElement>("[data-sign-out]")!.click();
    });
    expect(onAskingChange).toHaveBeenCalledTimes(1);
    expect(signOut).not.toHaveBeenCalled();

    await act(async () => revokeDeferred.resolve({ ok: true }));
    expect(signOut).toHaveBeenCalledWith();
    expect(onProblemChange).not.toHaveBeenCalled();
    expect(activeUnauth).not.toBe(originalUnauth);
    expectNoGlobalCalls();
  });

  it("handles rejected revoke promises, failed revokes, retry, cancel, and nothing-to-revoke in controlled mode", async () => {
    let activeUnauth = () => {};
    const originalUnauth = activeUnauth;
    const setUnauth = vi.fn((fn: () => void) => {
      const before = activeUnauth;
      activeUnauth = fn;
      return before;
    });
    const revoke = vi.fn<() => Promise<Revoke>>()
      .mockRejectedValueOnce(new Error("hub unreachable"))
      .mockResolvedValueOnce({ ok: false, error: "HTTP 503", id: "dev_42" })
      .mockResolvedValueOnce({ ok: false, nothing: true });
    const signOut = vi.fn();

    function Host() {
      const disconnect = useDisconnect({
        revoke,
        signOut,
        handleUnauthorized: setUnauth,
        renderDialog: renderCustomDialog,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>{disconnect.busy ? "busy" : "idle"}</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<Host />));
    const dialog = () => host.querySelector<HTMLDivElement>("[data-custom-dialog]")!;

    // 1. Rejected promise restores unauthorized handler, clears busy, and surfaces errorText.
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(activeUnauth).toBe(originalUnauth);
    expect(dialog().dataset.busy).toBe("false");
    expect(dialog().dataset.open).toBe("false");
    expect(dialog().dataset.problem).toBe("hub unreachable");

    // 2. Retry returns structured failure with device id; signing out carries id.
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(dialog().dataset.problem).toBe("HTTP 503");
    expect(activeUnauth).toBe(originalUnauth);
    await act(async () => host.querySelector<HTMLButtonElement>("[data-sign-out]")!.click());
    expect(signOut).toHaveBeenCalledWith("dev_42");
    expect(activeUnauth).not.toBe(originalUnauth);

    // 3. Close problem, reopen, and get nothing-to-revoke.
    activeUnauth = originalUnauth;
    await act(async () => host.querySelector<HTMLButtonElement>("[data-close-problem]")!.click());
    expect(dialog().dataset.problem).toBe("");
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(dialog().dataset.problem).toBe("nothing");
    await act(async () => host.querySelector<HTMLButtonElement>("[data-sign-out]")!.click());
    expect(signOut).toHaveBeenLastCalledWith("");
    expectNoGlobalCalls();
  });

  it("fences stale revokes across scopeKey ABA switches and unmount, restoring unauthorized handler cleanly", async () => {
    let activeUnauth = () => {};
    const hubAUnauth = activeUnauth;
    const setUnauth = vi.fn((fn: () => void) => {
      const before = activeUnauth;
      activeUnauth = fn;
      return before;
    });
    const firstRevoke = deferred<Revoke>();
    const secondRevoke = deferred<Revoke>();
    const revoke = vi.fn()
      .mockReturnValueOnce(firstRevoke.promise)
      .mockReturnValueOnce(secondRevoke.promise);
    const signOut = vi.fn();

    function ScopeHost({ scopeKey }: { scopeKey: string }) {
      const disconnect = useDisconnect({
        scopeKey,
        revoke,
        signOut,
        handleUnauthorized: setUnauth,
        renderDialog: renderCustomDialog,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>{disconnect.busy ? "busy" : "idle"}</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<ScopeHost scopeKey="hub-a" />));
    const dialog = () => host.querySelector<HTMLDivElement>("[data-custom-dialog]")!;

    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(dialog().dataset.busy).toBe("true");
    expect(activeUnauth).not.toBe(hubAUnauth);

    // Switch scopeKey hub-a -> hub-b: restores hub-a unauthorized handler and resets state.
    await act(async () => root.render(<ScopeHost scopeKey="hub-b" />));
    expect(activeUnauth).toBe(hubAUnauth);
    expect(dialog().dataset.busy).toBe("false");
    expect(dialog().dataset.open).toBe("false");

    // Switch back hub-b -> hub-a (ABA) and start a new revoke.
    await act(async () => root.render(<ScopeHost scopeKey="hub-a" />));
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    const unauthDuringSecond = activeUnauth;
    expect(dialog().dataset.busy).toBe("true");

    // Stale firstRevoke resolving must NOT sign out or reset busy or touch unauthorized handler.
    await act(async () => firstRevoke.resolve({ ok: true }));
    expect(signOut).not.toHaveBeenCalled();
    expect(dialog().dataset.busy).toBe("true");
    expect(activeUnauth).toBe(unauthDuringSecond);

    // Unmounting while secondRevoke is pending restores unauthorized handler and ignores late failure.
    await act(async () => root.unmount());
    expect(activeUnauth).toBe(hubAUnauth);
    await act(async () => secondRevoke.resolve({ ok: false, error: "late error", id: "dev_x" }));
    expect(signOut).not.toHaveBeenCalled();
    expectNoGlobalCalls();
  });

  it("clears controlled asking and problem state when scopeKey switches after a failed revoke", async () => {
    const revokeA = vi.fn<() => Promise<Revoke>>().mockResolvedValueOnce({ ok: false, error: "HTTP 503", id: "dev_A" });
    const revokeB = vi.fn<() => Promise<Revoke>>();
    const signOutA = vi.fn();
    const signOutB = vi.fn();
    const onAskingChange = vi.fn();
    const onBusyChange = vi.fn();
    const onProblemChange = vi.fn();
    let activeUnauth = () => {};

    function FullyControlledSwitchHost({ account }: { account: "A" | "B" }) {
      const [asking, setAsking] = useState(false);
      const [busy, setBusy] = useState(false);
      const [problem, setProblem] = useState<DisconnectProblem | null>(null);
      const disconnect = useDisconnect({
        scopeKey: `account-${account}`,
        revoke: account === "A" ? revokeA : revokeB,
        signOut: account === "A" ? signOutA : signOutB,
        handleUnauthorized: (fn) => {
          const before = activeUnauth;
          activeUnauth = fn;
          return before;
        },
        asking,
        onAskingChange: (next) => { onAskingChange(next); setAsking(next); },
        busy,
        onBusyChange: (next) => { onBusyChange(next); setBusy(next); },
        problem,
        onProblemChange: (next) => { onProblemChange(next); setProblem(next); },
        renderDialog: renderCustomDialog,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>{disconnect.busy ? "busy" : "idle"}</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<FullyControlledSwitchHost account="A" />));
    const dialog = () => host.querySelector<HTMLDivElement>("[data-custom-dialog]")!;

    // Account A fails disconnect, leaving problem with dev_A.
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(dialog().dataset.problem).toBe("HTTP 503");
    expect(onProblemChange).toHaveBeenLastCalledWith({ ok: false, error: "HTTP 503", id: "dev_A" });

    // Switching scopeKey and deps to account B must notify onAskingChange(false) and onProblemChange(null).
    await act(async () => root.render(<FullyControlledSwitchHost account="B" />));
    expect(onAskingChange).toHaveBeenLastCalledWith(false);
    expect(onProblemChange).toHaveBeenLastCalledWith(null);
    expect(dialog().dataset.open).toBe("false");
    expect(dialog().dataset.problem).toBe("");
    expect(signOutA).not.toHaveBeenCalled();
    expect(signOutB).not.toHaveBeenCalled();

    // Opening confirmation on account B and switching back to account A also clears controlled asking.
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    expect(dialog().dataset.open).toBe("true");
    await act(async () => root.render(<FullyControlledSwitchHost account="A" />));
    expect(onAskingChange).toHaveBeenLastCalledWith(false);
    expect(dialog().dataset.open).toBe("false");
    expect(signOutB).not.toHaveBeenCalled();
    expectNoGlobalCalls();
  });

  it("releases controlled busy on scopeKey switch when a revoke was in flight", async () => {
    const inFlight = deferred<Revoke>();
    const onBusyChange = vi.fn();
    let activeUnauth = () => {};
    const originalUnauth = activeUnauth;

    function ControlledBusyHost({ scopeKey, busy }: { scopeKey: string; busy: boolean }) {
      const disconnect = useDisconnect({
        scopeKey,
        busy,
        onBusyChange,
        revoke: () => inFlight.promise,
        signOut: vi.fn(),
        handleUnauthorized: (fn) => {
          const before = activeUnauth;
          activeUnauth = fn;
          return before;
        },
        renderDialog: renderCustomDialog,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>start</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<ControlledBusyHost scopeKey="s1" busy={false} />));
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(onBusyChange).toHaveBeenCalledWith(true);

    await act(async () => root.render(<ControlledBusyHost scopeKey="s2" busy />));
    expect(onBusyChange).toHaveBeenCalledWith(false);
    expect(activeUnauth).toBe(originalUnauth);
    expectNoGlobalCalls();
  });

  it("routes lazy DisconnectConfirm chunk failures through injected deps.notify without calling global toast", async () => {
    globals.chunkFails = true;
    const notify = vi.fn();
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});

    function ChunkHost() {
      const disconnect = useDisconnect({
        revoke: vi.fn(),
        signOut: vi.fn(),
        handleUnauthorized: () => () => {},
        notify,
      });
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>start</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<ChunkHost />));
    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    await vi.waitFor(async () => {
      await act(async () => { await new Promise((done) => setTimeout(done, 0)); });
      expect(notify).toHaveBeenCalledWith(t("pageboundary.chunk_body"), "error");
    }, { timeout: 3000 });
    expect(globals.toast).not.toHaveBeenCalled();
    errors.mockRestore();
  });

  it("renders DisconnectBrowserDialog cleanly as a standalone view", async () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    const onProblemClose = vi.fn();
    const onSignOut = vi.fn();
    const onChunkError = vi.fn();

    await act(async () => {
      root.render(
        <DisconnectBrowserDialog
          mounted={false}
          open={false}
          busy={false}
          problem={null}
          onCancel={onCancel}
          onConfirm={onConfirm}
          onProblemClose={onProblemClose}
          onSignOut={onSignOut}
          onChunkError={onChunkError}
        />,
      );
    });
    expect(host.innerHTML).toBe("");
    expect(globals.confirmChunkLoaded).not.toHaveBeenCalled();

    await act(async () => {
      root.render(
        <DisconnectBrowserDialog
          open
          busy={false}
          problem={null}
          onCancel={onCancel}
          onConfirm={onConfirm}
          onProblemClose={onProblemClose}
          onSignOut={onSignOut}
          onChunkError={onChunkError}
          renderDialog={renderCustomDialog}
        />,
      );
    });
    await act(async () => host.querySelector<HTMLButtonElement>("[data-confirm]")!.click());
    expect(onConfirm).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
  });
});

describe("useDisconnect default self-wired mode", () => {
  it("falls back to global revokeThisBrowser, signOutHere, handleUnauthorized, and lazy DisconnectConfirm when deps is omitted", async () => {
    globals.revoke.mockResolvedValueOnce({ ok: false, error: "HTTP 502", id: "dev_default" });

    function DefaultHost({ deps }: { deps?: DisconnectBrowserDeps }) {
      const disconnect = useDisconnect(deps);
      return (
        <>
          <button type="button" data-start onClick={disconnect.start}>{disconnect.busy ? "busy" : "idle"}</button>
          {disconnect.dialog}
        </>
      );
    }

    await act(async () => root.render(<DefaultHost />));
    expect(globals.confirmChunkLoaded).not.toHaveBeenCalled();

    await act(async () => host.querySelector<HTMLButtonElement>("[data-start]")!.click());
    const findDialogButton = (label: string) =>
      [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.includes(label));

    await vi.waitFor(async () => {
      await act(async () => { await new Promise((done) => setTimeout(done, 0)); });
      expect(findDialogButton(t("disconnect-confirm.confirm"))).toBeDefined();
    }, { timeout: 3000 });

    await act(async () => findDialogButton(t("disconnect-confirm.confirm"))!.click());
    expect(globals.revoke).toHaveBeenCalledOnce();
    expect(globals.handleUnauthorized).toHaveBeenCalledTimes(2);
    expect(globals.unauthorizedHandler).toBe(initialGlobalUnauthorized);
    expect(document.body.textContent).toContain("HTTP 502");

    await act(async () => findDialogButton(t("disconnect.sign_out_anyway"))!.click());
    expect(globals.signOut).toHaveBeenCalledWith("dev_default");
  });
});
