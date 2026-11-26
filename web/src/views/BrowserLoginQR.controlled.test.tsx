// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const globals = vi.hoisted(() => ({
  setPairedToken: vi.fn<(token: string, id: string) => void>(),
  errorText: vi.fn<(err: unknown) => string>((e) => (e instanceof Error ? e.message : String(e))),
  deviceName: vi.fn<() => string>(() => "Default Browser"),
  forgetUnrevoked: vi.fn<() => void>(),
  unrevokedTokenId: vi.fn<() => string>(() => ""),
  storedCredentials: vi.fn<() => { token: string; tokenId: string }>(() => ({ token: "", tokenId: "" })),
  qrRendered: vi.fn<(props: { rows: string[]; label?: string }) => void>(),
}));

vi.mock("../lib/api", () => ({
  setPairedToken: (token: string, id: string) => globals.setPairedToken(token, id),
  errorText: (err: unknown) => globals.errorText(err),
}));

vi.mock("../lib/auth", () => ({
  deviceName: () => globals.deviceName(),
  forgetUnrevoked: () => globals.forgetUnrevoked(),
  unrevokedTokenId: () => globals.unrevokedTokenId(),
}));

vi.mock("../lib/compatibility", () => ({
  storedCredentials: () => globals.storedCredentials(),
}));

vi.mock("./settings/LoginSteps", () => ({
  QRCode: (props: { rows: string[]; label?: string }) => {
    globals.qrRendered(props);
    return <svg data-default-qr aria-label={props.label ?? "QR"} />;
  },
}));

import { initI18n, t } from "../i18n";
import {
  BrowserLoginQR,
  BrowserLoginRequestError,
  BrowserLoginQRView,
  type BrowserLoginChallenge,
  type BrowserLoginCredential,
  type BrowserLoginQRDeps,
} from "./BrowserLoginQR";

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

function makeChallenge(id: string): BrowserLoginChallenge {
  return { id, secret: `sec-${id}`, expires_at: "2099-01-01T00:00:00Z", qr: { rows: ["10", "01"] } };
}

let host: HTMLDivElement;
let root: Root;
let fetchMock: ReturnType<typeof vi.fn>;

function expectNoGlobalCalls() {
  expect(fetchMock).not.toHaveBeenCalled();
  expect(globals.setPairedToken).not.toHaveBeenCalled();
  expect(globals.errorText).not.toHaveBeenCalled();
  expect(globals.deviceName).not.toHaveBeenCalled();
  expect(globals.forgetUnrevoked).not.toHaveBeenCalled();
  expect(globals.unrevokedTokenId).not.toHaveBeenCalled();
  expect(globals.storedCredentials).not.toHaveBeenCalled();
  expect(globals.qrRendered).not.toHaveBeenCalled();
}

function numberInput(): HTMLInputElement | null {
  return host.querySelector<HTMLInputElement>("#login-qr-number");
}

function refreshButton(): HTMLButtonElement {
  const buttons = [...host.querySelectorAll<HTMLButtonElement>("button")];
  return buttons[buttons.length - 1]!;
}

async function setNumberAndSubmit(value: string) {
  await act(async () => {
    const input = numberInput()!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => {
    host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}

async function tick(ms = 1500) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(async () => {
  await initI18n("en");
  vi.useFakeTimers();
  globals.setPairedToken.mockReset();
  globals.errorText.mockReset().mockImplementation((e) => (e instanceof Error ? e.message : String(e)));
  globals.deviceName.mockReset().mockReturnValue("Default Browser");
  globals.forgetUnrevoked.mockReset();
  globals.unrevokedTokenId.mockReset().mockReturnValue("");
  globals.storedCredentials.mockReset().mockReturnValue({ token: "", tokenId: "" });
  globals.qrRendered.mockReset();
  fetchMock = vi.fn(() => {
    throw new Error("unexpected default fetch");
  });
  vi.stubGlobal("fetch", fetchMock);
  Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("BrowserLoginQRView", () => {
  it("renders loading, QR, confirm, and ended states purely with zero default singleton calls", async () => {
    const onNumberChange = vi.fn();
    const onSubmit = vi.fn((e: React.FormEvent) => e.preventDefault());
    const onRefresh = vi.fn();
    const renderQRCode = vi.fn(({ rows, label }: { rows: string[]; label: string }) => (
      <div data-custom-qr data-rows={rows.join(",")} aria-label={label} />
    ));

    await act(async () => {
      root.render(
        <BrowserLoginQRView
          state="pending"
          error=""
          challenge={makeChallenge("view-1")}
          number=""
          sending={false}
          onNumberChange={onNumberChange}
          onSubmit={onSubmit}
          onRefresh={onRefresh}
          renderQRCode={renderQRCode}
        />,
      );
    });
    expect(host.querySelector("[data-custom-qr]")?.getAttribute("data-rows")).toBe("10,01");
    expect(host.textContent).toContain(t("login.qr_hint"));

    await act(async () => {
      root.render(
        <BrowserLoginQRView
          state="confirm"
          error=""
          challenge={makeChallenge("view-1")}
          number="47"
          sending={false}
          onNumberChange={onNumberChange}
          onSubmit={onSubmit}
          onRefresh={onRefresh}
          renderQRCode={renderQRCode}
        />,
      );
    });
    await act(async () => {
      const input = numberInput()!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "9x8a1");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(onNumberChange).toHaveBeenCalledWith("98");
    await act(async () => {
      host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(onSubmit).toHaveBeenCalledOnce();

    await act(async () => {
      root.render(
        <BrowserLoginQRView
          state="failed"
          error="custom failure"
          number=""
          sending={false}
          onNumberChange={onNumberChange}
          onSubmit={onSubmit}
          onRefresh={onRefresh}
        />,
      );
    });
    expect(host.textContent).toContain("custom failure");
    await act(async () => refreshButton().click());
    expect(onRefresh).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
  });
});

describe("BrowserLoginQR controlled mode", () => {
  it("runs start, poll, confirm, token persistence, and forgetUnrevoked with zero global singleton calls", async () => {
    let pollState = "pending";
    const startSignals: AbortSignal[] = [];
    const onDone = vi.fn();
    const deps: BrowserLoginQRDeps = {
      deviceName: vi.fn(() => "Controlled Browser"),
      replacesTokenId: vi.fn(() => "dev_prev"),
      start: vi.fn(async (_body, signal) => {
        startSignals.push(signal);
        return makeChallenge("ctrl-1");
      }),
      poll: vi.fn(async () => ({ state: pollState })),
      confirm: vi.fn(async () => ({ state: "approved", token: "tok_ctrl", id: "dev_ctrl" })),
      setPairedToken: vi.fn(),
      forgetUnrevoked: vi.fn(),
      formatError: vi.fn((e) => `fmt:${String(e)}`),
      isVisible: vi.fn(() => true),
      renderQRCode: ({ rows }) => <div data-custom-qr>{rows.join("")}</div>,
    };

    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} deps={deps} />);
    });
    expect(deps.start).toHaveBeenCalledWith({ name: "Controlled Browser", replaces: "dev_prev" }, startSignals[0]);
    expect(host.querySelector("[data-custom-qr]")?.textContent).toBe("1001");

    pollState = "scanned";
    await tick();
    expect(host.textContent).toContain(t("login.qr_scanned"));

    pollState = "confirm";
    await tick();
    expect(numberInput()).not.toBeNull();

    await setNumberAndSubmit("47");
    expect(deps.confirm).toHaveBeenCalledWith(
      { id: "ctrl-1", secret: "sec-ctrl-1", number: "47" },
      startSignals[0],
    );
    expect(deps.setPairedToken).toHaveBeenCalledExactlyOnceWith("tok_ctrl", "dev_ctrl");
    expect(deps.forgetUnrevoked).toHaveBeenCalledOnce();
    expect(onDone).toHaveBeenCalledOnce();

    const pollsAfterDone = vi.mocked(deps.poll!).mock.calls.length;
    await tick(6000);
    expect(deps.poll).toHaveBeenCalledTimes(pollsAfterDone);
    expectNoGlobalCalls();
  });

  it("handles 400 replaces fallback, visibility pause, retryable errors, 409/410 confirm endings, and start errors", async () => {
    let visible = false;
    let pollCalls = 0;
    const onDone = vi.fn();
    const deps: BrowserLoginQRDeps = {
      deviceName: () => "Controlled Browser",
      replacesTokenId: () => "dev_old",
      start: vi.fn(async (body) => {
        if (body.replaces) throw new BrowserLoginRequestError(400);
        return makeChallenge("fallback-1");
      }),
      poll: vi.fn(async () => {
        pollCalls += 1;
        if (pollCalls === 1) throw new BrowserLoginRequestError(503);
        if (pollCalls === 2) return { state: "confirm" };
        return { state: "confirm" };
      }),
      confirm: vi.fn(async () => {
        throw new BrowserLoginRequestError(410);
      }),
      setPairedToken: vi.fn(),
      forgetUnrevoked: vi.fn(),
      formatError: (err) => (err instanceof Error ? `formatted:${err.message}` : "formatted"),
      isVisible: () => visible,
      renderQRCode: () => <div data-custom-qr />,
    };

    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} deps={deps} />);
    });
    expect(deps.start).toHaveBeenCalledTimes(2);
    expect(deps.start).toHaveBeenNthCalledWith(1, { name: "Controlled Browser", replaces: "dev_old" }, expect.any(AbortSignal));
    expect(deps.start).toHaveBeenNthCalledWith(2, { name: "Controlled Browser" }, expect.any(AbortSignal));
    expect(deps.poll).not.toHaveBeenCalled();

    visible = true;
    await tick(1500);
    expect(pollCalls).toBe(1);
    expect(host.textContent).toContain(t("login.qr_retry"));

    await tick(3000);
    expect(pollCalls).toBe(2);
    expect(numberInput()).not.toBeNull();

    await setNumberAndSubmit("47");
    expect(host.textContent).toContain(t("login.qr_expired"));
    const pollsAfterExpire = pollCalls;
    await tick(6000);
    expect(pollCalls).toBe(pollsAfterExpire);
    expect(deps.forgetUnrevoked).not.toHaveBeenCalled();

    // Lost reply (TypeError) on confirm keeps polling and picks up approved credential
    let lostReplyStep = 0;
    const lostDone = vi.fn();
    const lostPaired = vi.fn();
    await act(async () => {
      root.render(
        <BrowserLoginQR
          onDone={lostDone}
          scopeKey="lost-reply"
          deps={{
            ...deps,
            replacesTokenId: () => "",
            start: vi.fn(async () => makeChallenge("lost-1")),
            poll: vi.fn(async () => {
              lostReplyStep += 1;
              return lostReplyStep === 1 ? { state: "confirm" } : { state: "approved", token: "tok_lost", id: "dev_lost" };
            }),
            confirm: vi.fn(async () => {
              throw new TypeError("network dropped");
            }),
            setPairedToken: lostPaired,
          }}
        />,
      );
    });
    await setNumberAndSubmit("47");
    expect(lostPaired).not.toHaveBeenCalled();
    await tick(1500);
    expect(lostPaired).toHaveBeenCalledExactlyOnceWith("tok_lost", "dev_lost");
    expect(lostDone).toHaveBeenCalledOnce();

    // 409 number mismatch voids challenge and stops polling
    let voidedPolls = 0;
    await act(async () => {
      root.render(
        <BrowserLoginQR
          onDone={onDone}
          scopeKey="voided-409"
          deps={{
            ...deps,
            replacesTokenId: () => "",
            start: vi.fn(async () => makeChallenge("void-1")),
            poll: vi.fn(async () => {
              voidedPolls += 1;
              return { state: "confirm" };
            }),
            confirm: vi.fn(async () => {
              throw new BrowserLoginRequestError(409, "number_mismatch", "voided");
            }),
          }}
        />,
      );
    });
    await setNumberAndSubmit("12");
    expect(host.textContent).toContain(t("login.qr_voided"));
    const pollsAtVoid = voidedPolls;
    await tick(6000);
    expect(voidedPolls).toBe(pollsAtVoid);

    // Synchronous throw from start on refresh is caught cleanly and formatted via deps.formatError
    deps.start = vi.fn(() => {
      throw new Error("sync-boom");
    });
    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} scopeKey="sync-throw" deps={deps} />);
    });
    expect(host.textContent).toContain("formatted:sync-boom");
    expectNoGlobalCalls();
  });

  it("aborts AbortSignal, cancels timers, and ignores late start/poll/confirm responses on unmount", async () => {
    const start1 = deferred<BrowserLoginChallenge>();
    const signals: AbortSignal[] = [];
    const onDone = vi.fn();
    const setPaired = vi.fn();
    const forget = vi.fn();
    const pollSpy = vi.fn();

    await act(async () => {
      root.render(
        <BrowserLoginQR
          onDone={onDone}
          deps={{
            deviceName: () => "Controlled",
            replacesTokenId: () => "dev_old",
            start: (_body, signal) => {
              signals.push(signal);
              return start1.promise;
            },
            poll: pollSpy,
            setPairedToken: setPaired,
            forgetUnrevoked: forget,
            renderQRCode: () => <div data-custom-qr />,
          }}
        />,
      );
    });
    expect(signals[0]!.aborted).toBe(false);

    // Unmount while start is pending
    await act(async () => root.unmount());
    expect(signals[0]!.aborted).toBe(true);
    await act(async () => {
      start1.resolve(makeChallenge("late-start"));
      await Promise.resolve();
    });
    expect(pollSpy).not.toHaveBeenCalled();

    // Remount and unmount while poll and confirm are both in flight
    root = createRoot(host);
    const poll1 = deferred<BrowserLoginCredential>();
    const confirm1 = deferred<BrowserLoginCredential>();
    let pollStep = 0;
    let run2Signal!: AbortSignal;

    await act(async () => {
      root.render(
        <BrowserLoginQR
          onDone={onDone}
          deps={{
            deviceName: () => "Controlled",
            replacesTokenId: () => "dev_old",
            start: async (_body, signal) => {
              run2Signal = signal;
              return makeChallenge("run-2");
            },
            poll: () => {
              pollStep += 1;
              if (pollStep === 1) return Promise.resolve({ state: "confirm" });
              return poll1.promise;
            },
            confirm: () => confirm1.promise,
            setPairedToken: setPaired,
            forgetUnrevoked: forget,
            renderQRCode: () => <div data-custom-qr />,
          }}
        />,
      );
    });
    await tick(1500); // triggers poll #2 (deferred)
    await setNumberAndSubmit("47"); // triggers confirm (deferred)
    expect(run2Signal.aborted).toBe(false);

    await act(async () => root.unmount());
    expect(run2Signal.aborted).toBe(true);

    // Late resolutions ignoring AbortSignal must have zero effect
    await act(async () => {
      poll1.resolve({ state: "approved", token: "stale-poll", id: "stale-id" });
      confirm1.resolve({ state: "approved", token: "stale-confirm", id: "stale-id" });
      await Promise.resolve();
    });
    await tick(6000);
    expect(pollStep).toBe(2);
    expect(setPaired).not.toHaveBeenCalled();
    expect(forget).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
    expectNoGlobalCalls();
  });

  it("guards refresh and scopeKey ABA races so stale confirm/poll cannot clear sending or finish a newer run", async () => {
    const confirm1 = deferred<BrowserLoginCredential>();
    const confirm2 = deferred<BrowserLoginCredential>();
    const pollById = new Map<string, ReturnType<typeof deferred<BrowserLoginCredential>>>();
    const signalsById = new Map<string, AbortSignal>();
    let startCount = 0;
    const onDone = vi.fn();
    const setPaired = vi.fn();
    const confirmCalls: string[] = [];

    const deps: BrowserLoginQRDeps = {
      deviceName: () => "Controlled",
      replacesTokenId: () => "",
      start: async (_body, signal) => {
        const id = `run-${++startCount}`;
        signalsById.set(id, signal);
        return makeChallenge(id);
      },
      poll: ({ id }) => {
        if (!pollById.has(id)) {
          pollById.set(id, deferred<BrowserLoginCredential>());
          return Promise.resolve({ state: "confirm" });
        }
        return pollById.get(id)!.promise;
      },
      confirm: ({ id }) => {
        confirmCalls.push(id);
        return id === "run-1" ? confirm1.promise : confirm2.promise;
      },
      setPairedToken: setPaired,
      forgetUnrevoked: vi.fn(),
      renderQRCode: () => <div data-custom-qr />,
    };

    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    expect(numberInput()).not.toBeNull();

    // Start confirm on run-1
    await setNumberAndSubmit("47");
    expect(confirmCalls).toEqual(["run-1"]);
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true);

    // Refresh mid-confirm -> aborts run-1 and starts run-2
    await act(async () => refreshButton().click());
    expect(signalsById.get("run-1")!.aborted).toBe(true);
    expect(numberInput()).not.toBeNull();
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true); // empty number

    // Submit on run-2
    await setNumberAndSubmit("89");
    expect(confirmCalls).toEqual(["run-1", "run-2"]);
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true);

    // Stale confirm-1 settles while confirm-2 is still in flight: must NOT clear sending or finish
    await act(async () => {
      confirm1.resolve({ state: "approved", token: "stale-tok", id: "stale-id" });
      await Promise.resolve();
    });
    expect(setPaired).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true);

    // Scope ABA switch: hub-a -> hub-b (run-3) -> hub-a (run-4) while run-2 confirm is pending
    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} scopeKey="hub-b" deps={deps} />);
    });
    expect(signalsById.get("run-2")!.aborted).toBe(true);
    await tick(1500); // start poll on run-3 (deferred)

    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    expect(signalsById.get("run-3")!.aborted).toBe(true);

    // Stale confirm-2 and stale poll from run-3 both resolve approved: must be discarded!
    await act(async () => {
      confirm2.resolve({ state: "approved", token: "stale-2", id: "stale-2" });
      pollById.get("run-3")!.resolve({ state: "approved", token: "stale-3", id: "stale-3" });
      await Promise.resolve();
    });
    expect(setPaired).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();

    // Only run-4's poll completes sign-in
    await tick(1500);
    await act(async () => {
      pollById.get("run-4")!.resolve({ state: "approved", token: "fresh-4", id: "cred-4" });
      await Promise.resolve();
    });
    expect(setPaired).toHaveBeenCalledExactlyOnceWith("fresh-4", "cred-4");
    expect(onDone).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
  });

  it("prevents duplicate finish on concurrent poll+confirm approval, blocks sync double-submit, and keeps challenge across inline prop updates", async () => {
    const confirmPending = deferred<BrowserLoginCredential>();
    const pollPending = deferred<BrowserLoginCredential>();
    let starts = 0;
    let pollCalls = 0;
    const confirmSpy = vi.fn(() => confirmPending.promise);
    const setPaired = vi.fn();
    const firstOnDone = vi.fn();
    const latestOnDone = vi.fn();

    const makeDeps = (): BrowserLoginQRDeps => ({
      deviceName: () => "Controlled",
      replacesTokenId: () => "",
      start: async () => makeChallenge(`inline-${++starts}`),
      poll: () => {
        pollCalls += 1;
        if (pollCalls === 1) return Promise.resolve({ state: "confirm" });
        return pollPending.promise;
      },
      confirm: confirmSpy,
      setPairedToken: setPaired,
      forgetUnrevoked: vi.fn(),
      renderQRCode: () => <div data-custom-qr />,
    });

    await act(async () => {
      root.render(<BrowserLoginQR onDone={firstOnDone} scopeKey="stable" deps={makeDeps()} />);
    });
    // Re-render with fresh inline onDone and deps references (same scopeKey): must NOT restart
    await act(async () => {
      root.render(<BrowserLoginQR onDone={latestOnDone} scopeKey="stable" deps={makeDeps()} />);
    });
    expect(starts).toBe(1);

    await tick(1500); // triggers poll #2 (deferred)
    await act(async () => {
      const input = numberInput()!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "47");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    // Synchronous double submit before React re-renders
    await act(async () => {
      const form = host.querySelector("form")!;
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(confirmSpy).toHaveBeenCalledOnce();

    // Both poll and confirm resolve approved in the same turn: finish must run only once, calling latestOnDone
    await act(async () => {
      pollPending.resolve({ state: "approved", token: "tok-once", id: "cred-once" });
      confirmPending.resolve({ state: "approved", token: "tok-once", id: "cred-once" });
      await Promise.resolve();
    });
    expect(setPaired).toHaveBeenCalledExactlyOnceWith("tok-once", "cred-once");
    expect(firstOnDone).not.toHaveBeenCalled();
    expect(latestOnDone).toHaveBeenCalledOnce();
    expectNoGlobalCalls();
  });
});

describe("BrowserLoginQR default fallback compatibility", () => {
  it("uses default fetch, deviceName, storage, setPairedToken, forgetUnrevoked, and lazy QRCode when deps are omitted", async () => {
    globals.unrevokedTokenId.mockReturnValue("dev_legacy");
    let pollStep = 0;
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/start")) {
        return Promise.resolve({ ok: true, json: () => Promise.resolve(makeChallenge("def-1")) });
      }
      pollStep += 1;
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(pollStep === 1 ? { state: "pending" } : { state: "approved", token: "tok_def", id: "dev_def" }),
      });
    });

    const onDone = vi.fn();
    await act(async () => {
      root.render(<BrowserLoginQR onDone={onDone} />);
    });
    // Await the lazy LoginSteps.QRCode dynamic import inside act so Suspense commits cleanly
    await act(async () => {
      await import("./settings/LoginSteps");
      await Promise.resolve();
    });
    expect(globals.deviceName).toHaveBeenCalledOnce();
    expect(globals.storedCredentials).toHaveBeenCalledOnce();
    expect(globals.unrevokedTokenId).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/auth/browser-login/start"),
      expect.objectContaining({ method: "POST" }),
    );
    expect(globals.qrRendered).toHaveBeenCalled();

    await tick(1500);
    expect(globals.setPairedToken).toHaveBeenCalledExactlyOnceWith("tok_def", "dev_def");
    expect(globals.forgetUnrevoked).toHaveBeenCalledOnce();
    expect(onDone).toHaveBeenCalledOnce();
  });
});
