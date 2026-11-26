// @vitest-environment jsdom
import { act, startTransition, Suspense, useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine, Session } from "../lib/types";

const spies = vi.hoisted(() => ({
  onOpenCloseAgent: vi.fn(),
  useStore: vi.fn(),
  machineWithHub: vi.fn(),
}));

vi.mock("../lib/close-agent", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/close-agent")>();
  return {
    ...actual,
    onOpenCloseAgent: (listener: (s: Session) => void) => {
      spies.onOpenCloseAgent(listener);
      return actual.onOpenCloseAgent(listener);
    },
  };
});

vi.mock("../lib/store", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/store")>();
  return {
    ...actual,
    useStore: ((selector: (st: ReturnType<typeof actual.getState>) => unknown) => {
      spies.useStore(selector);
      return actual.useStore(selector as never);
    }) as typeof actual.useStore,
  };
});

vi.mock("../lib/mesh", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/mesh")>();
  return {
    ...actual,
    machineWithHub: (machineId: string, machines: Machine[]) => {
      spies.machineWithHub(machineId, machines);
      return actual.machineWithHub(machineId, machines);
    },
  };
});

vi.mock("./chat/CloseAgentDialog", () => ({
  CloseAgentDialog: ({
    session,
    machineName,
    onClose,
    open,
  }: {
    open: boolean;
    session: Session;
    machineName: string;
    onClose: () => void;
  }) => (
    <div data-testid="default-close-dialog" data-open={String(open)} data-machine={machineName}>
      <span>{session.machine_id}/{session.session_id}</span>
      <button type="button" data-testid="default-close-btn" onClick={onClose}>Close</button>
    </div>
  ),
}));

import { openCloseAgent } from "../lib/close-agent";
import { getState, reset } from "../lib/store";
import { CloseAgentHost, CloseAgentHostView, type CloseAgentHostRenderProps, type CloseAgentHostViewProps } from "./CloseAgentHost";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function makeSession(patch: Partial<Session> = {}): Session {
  return {
    machine_id: "m1",
    session_id: "s1",
    status: "idle",
    title: "Build task",
    cwd: "/work/repo",
    updated_at: "2026-11-26T00:00:00Z",
    ...patch,
  };
}

function makeMachine(patch: Partial<Machine> = {}): Machine {
  return {
    id: "m1",
    name: "Studio",
    online: true,
    ...patch,
  };
}

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createExternalBox<T>(initial: T) {
  let current = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => current,
    set: (next: T) => {
      current = next;
      listeners.forEach((l) => l());
    },
    setSilent: (next: T) => {
      current = next;
    },
    notify: () => {
      listeners.forEach((l) => l());
    },
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    listenerCount: () => listeners.size,
  };
}

function createEventBus<T>() {
  const listeners = new Set<(value: T) => void>();
  const subscribe = vi.fn((listener: (value: T) => void) => {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  });
  return {
    subscribe,
    emit: (value: T) => { listeners.forEach((l) => l(value)); },
    listenerCount: () => listeners.size,
    rawListeners: () => [...listeners],
  };
}

let host: HTMLDivElement;
let root: Root;

const until = async (ok: () => boolean) => {
  for (let i = 0; i < 200 && !ok(); i++) {
    await act(async () => { await new Promise((r) => setTimeout(r, 10)); });
  }
};

function expectNoDefaultSingletons() {
  expect(spies.onOpenCloseAgent).not.toHaveBeenCalled();
  expect(spies.useStore).not.toHaveBeenCalled();
}

beforeEach(() => {
  reset();
  spies.onOpenCloseAgent.mockClear();
  spies.useStore.mockClear();
  spies.machineWithHub.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.restoreAllMocks();
});

describe("CloseAgentHostView (pure presentational)", () => {
  it("renders null when session is null and forwards props to renderDialog when open", () => {
    const onClose = vi.fn();
    act(() => root.render(<CloseAgentHostView session={null} machineName="Studio" onClose={onClose} />));
    expect(host.innerHTML).toBe("");

    let captured: CloseAgentHostRenderProps | null = null;
    const s = makeSession();
    act(() => root.render(
      <CloseAgentHostView
        session={s}
        machineName="Home · Studio"
        dialogKey="custom-key"
        onClose={onClose}
        dialogProps={{ features: ["archive"] }}
        renderDialog={(props) => {
          captured = props;
          return <button type="button" data-testid="custom-dialog" onClick={props.onClose}>{props.machineName}</button>;
        }}
      />,
    ));
    expect(captured).not.toBeNull();
    expect(captured!.open).toBe(true);
    expect(captured!.session).toBe(s);
    expect(captured!.machineName).toBe("Home · Studio");
    expect(captured!.dialogKey).toBe("custom-key");
    expect(captured!.dialogProps).toEqual({ features: ["archive"] });
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="custom-dialog"]')!.click());
    expect(onClose).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });
});

describe("CloseAgentHost (controlled & default)", () => {
  it("bypasses default singletons, caches fresh-array/object getters without render loops, and preserves identity across bridge switches", () => {
    const sessionBox = createExternalBox<Session | null>(makeSession({ machine_id: "m1", session_id: "s1", status: "working" }));
    const machinesBox = createExternalBox<Machine[]>([makeMachine({ id: "m1", name: "Mac Studio" })]);
    const onClose = vi.fn();
    let mounts = 0;
    let renderCount = 0;

    function TrackedDialog({ session, machineName, onClose: dismiss }: CloseAgentHostRenderProps) {
      useEffect(() => {
        mounts += 1;
      }, []);
      return (
        <div data-testid="tracked" data-status={session.status} data-machine={machineName}>
          <button type="button" data-testid="close" onClick={dismiss}>Close</button>
        </div>
      );
    }

    const renderDialog = (props: CloseAgentHostRenderProps) => {
      renderCount += 1;
      return <TrackedDialog key={props.dialogKey} {...props} />;
    };

    // Start with fresh-object / fresh-array getters + subscribe.
    act(() => root.render(
      <CloseAgentHost
        deps={{
          session: () => (sessionBox.get() ? { ...sessionBox.get()! } : null),
          subscribeSession: sessionBox.subscribe,
          machines: () => machinesBox.get().map((m) => ({ ...m })),
          subscribeMachines: machinesBox.subscribe,
          onClose,
          renderDialog,
        }}
      />,
    ));
    expect(mounts).toBe(1);
    expect(renderCount).toBeLessThan(6);
    expect(host.querySelector('[data-testid="tracked"]')?.getAttribute("data-machine")).toBe("Mac Studio");
    expectNoDefaultSingletons();

    // Update mutable progress (status + machine rename) on the same session: updates live without remounting dialog.
    act(() => {
      sessionBox.set(makeSession({ machine_id: "m1", session_id: "s1", status: "idle" }));
      machinesBox.set([makeMachine({ id: "m1", name: "Mac Studio Pro" })]);
    });
    expect(mounts).toBe(1);
    expect(host.querySelector('[data-testid="tracked"]')?.getAttribute("data-status")).toBe("idle");
    expect(host.querySelector('[data-testid="tracked"]')?.getAttribute("data-machine")).toBe("Mac Studio Pro");

    // Switch both session and machines from getter+subscribe to direct values: dialog must NOT remount.
    act(() => root.render(
      <CloseAgentHost
        deps={{
          session: makeSession({ machine_id: "m1", session_id: "s1", status: "idle" }),
          machines: [makeMachine({ id: "m1", name: "Mac Studio Direct" })],
          onClose,
          renderDialog,
        }}
      />,
    ));
    expect(mounts).toBe(1);
    expect(sessionBox.listenerCount()).toBe(0);
    expect(machinesBox.listenerCount()).toBe(0);
    expect(host.querySelector('[data-testid="tracked"]')?.getAttribute("data-machine")).toBe("Mac Studio Direct");

    // Switch back from direct values to getter+subscribe: still zero remounts.
    act(() => root.render(
      <CloseAgentHost
        deps={{
          session: () => (sessionBox.get() ? { ...sessionBox.get()! } : null),
          subscribeSession: sessionBox.subscribe,
          machines: () => machinesBox.get().map((m) => ({ ...m })),
          subscribeMachines: machinesBox.subscribe,
          onClose,
          renderDialog,
        }}
      />,
    ));
    expect(mounts).toBe(1);
    expectNoDefaultSingletons();
  });

  it("invalidates stale local session fail-closed on source switches and fences stale bus events", () => {
    const busA = createEventBus<Session>();
    const busB = createEventBus<Session>();
    const latestView: { current: CloseAgentHostViewProps | null } = { current: null };
    const renderView = (props: CloseAgentHostViewProps) => {
      latestView.current = props;
      return <CloseAgentHostView {...props} />;
    };
    const renderDialog = (props: CloseAgentHostRenderProps) => (
      <div data-testid="dialog">{props.session.session_id}</div>
    );

    act(() => root.render(
      <CloseAgentHost deps={{ onOpenRequest: busA.subscribe, machineName: "Studio", renderDialog, renderView }} />,
    ));
    expect(busA.listenerCount()).toBe(1);
    const busACallback = busA.rawListeners()[0];
    act(() => busA.emit(makeSession({ session_id: "s-a" })));
    expect(host.querySelector('[data-testid="dialog"]')?.textContent).toBe("s-a");

    // Switching onOpenRequest from busA -> busB immediately clears busA's session fail-closed.
    act(() => root.render(
      <CloseAgentHost deps={{ onOpenRequest: busB.subscribe, machineName: "Studio", renderDialog, renderView }} />,
    ));
    expect(host.querySelector('[data-testid="dialog"]')).toBeNull();
    expect(latestView.current?.session).toBeNull();
    expect(busA.listenerCount()).toBe(0);
    expect(busB.listenerCount()).toBe(1);

    // A late stray event delivered through busA's old listener is ignored.
    act(() => busACallback(makeSession({ session_id: "s-stale" })));
    expect(host.querySelector('[data-testid="dialog"]')).toBeNull();

    // Events from busB open normally.
    act(() => busB.emit(makeSession({ session_id: "s-b" })));
    expect(host.querySelector('[data-testid="dialog"]')?.textContent).toBe("s-b");

    // Switching to controlled session and then back to event-driven stays fail-closed (never resurrects s-b).
    act(() => root.render(
      <CloseAgentHost deps={{ session: makeSession({ session_id: "s-ctrl" }), machineName: "Studio", renderDialog, renderView }} />,
    ));
    expect(host.querySelector('[data-testid="dialog"]')?.textContent).toBe("s-ctrl");
    expect(busB.listenerCount()).toBe(0);

    act(() => root.render(
      <CloseAgentHost deps={{ onOpenRequest: busB.subscribe, machineName: "Studio", renderDialog, renderView }} />,
    ));
    expect(host.querySelector('[data-testid="dialog"]')).toBeNull();
    expectNoDefaultSingletons();
  });

  it("resets on scopeKey ABA transitions, fences stale onClose callbacks, and recovers via try/finally when onClose throws", () => {
    const scopeBox = createExternalBox("acct-1");
    const bus = createEventBus<Session>();
    const onClose = vi.fn();
    let capturedClose: (() => void) | null = null;
    let mounts = 0;

    function DialogProbe(props: CloseAgentHostRenderProps) {
      capturedClose = props.onClose;
      useEffect(() => { mounts += 1; }, []);
      return <div data-testid="probe">{props.dialogKey}</div>;
    }

    act(() => root.render(
      <CloseAgentHost
        deps={{
          scopeKey: scopeBox.get,
          subscribeScopeKey: scopeBox.subscribe,
          onOpenRequest: bus.subscribe,
          machineName: "Studio",
          onClose,
          renderDialog: (props) => <DialogProbe key={props.dialogKey} {...props} />,
        }}
      />,
    ));

    act(() => bus.emit(makeSession({ session_id: "s1" })));
    expect(mounts).toBe(1);
    const staleCloseFromA1 = capturedClose!;

    // ABA burst: acct-1 -> acct-2 -> acct-1 immediately invalidates local session and fences stale close.
    act(() => {
      scopeBox.set("acct-2");
      scopeBox.set("acct-1");
    });
    expect(host.querySelector('[data-testid="probe"]')).toBeNull();
    act(() => staleCloseFromA1());
    expect(onClose).not.toHaveBeenCalled();

    // Open again in the new acct-1 epoch, and throw inside onClose: local session must STILL clear in finally.
    act(() => bus.emit(makeSession({ session_id: "s1" })));
    expect(host.querySelector('[data-testid="probe"]')).not.toBeNull();
    onClose.mockImplementationOnce(() => { throw new Error("observer boom"); });
    let caught: unknown;
    act(() => {
      try {
        capturedClose!();
      } catch (err) {
        caught = err;
      }
    });
    expect((caught as Error)?.message).toBe("observer boom");
    expect(host.querySelector('[data-testid="probe"]')).toBeNull();

    // Live drift check in controlled getter mode: if external session changed to s2 before commit, s1's onClose is fenced.
    const controlledBox = createExternalBox<Session | null>(makeSession({ session_id: "s1" }));
    onClose.mockReset();
    act(() => root.render(
      <CloseAgentHost
        deps={{
          scopeKey: "acct-1",
          session: controlledBox.get,
          subscribeSession: controlledBox.subscribe,
          machineName: "Studio",
          onClose,
          renderDialog: (props) => <DialogProbe key={props.dialogKey} {...props} />,
        }}
      />,
    ));
    const closeS1 = capturedClose!;
    // Mutate underlying getter without waiting for React commit.
    controlledBox.setSilent(makeSession({ session_id: "s2" }));
    act(() => closeS1());
    expect(onClose).not.toHaveBeenCalled();
    act(() => controlledBox.notify());
    expect(host.querySelector('[data-testid="probe"]')?.textContent).toBe("2:m1/s2");
    expectNoDefaultSingletons();
  });

  it("keeps committed subscription and open dialog intact when a concurrent transition render is abandoned", async () => {
    const busA = createEventBus<Session>();
    const busB = createEventBus<Session>();
    const onClose = vi.fn();
    const suspendGate = deferred<void>();
    const renderedScopes: string[] = [];
    let latestClose: (() => void) | null = null;

    function SuspendIf({ block }: { block: boolean }) {
      if (block) throw suspendGate.promise;
      return null;
    }

    let setHarness!: (next: { scope: string; useB: boolean; block: boolean }) => void;
    function Harness() {
      const [state, setState] = useState({ scope: "acct-1", useB: false, block: false });
      setHarness = setState;
      renderedScopes.push(state.scope);
      return (
        <Suspense fallback={<div data-fallback />}>
          <CloseAgentHost
            deps={{
              scopeKey: state.scope,
              onOpenRequest: state.useB ? busB.subscribe : busA.subscribe,
              machineName: "Studio",
              onClose,
              renderDialog: (props) => {
                latestClose = props.onClose;
                return <div data-testid="active-dialog">{props.session.session_id}</div>;
              },
            }}
          />
          <SuspendIf block={state.block} />
        </Suspense>
      );
    }

    await act(async () => root.render(<Harness />));
    expect(busA.listenerCount()).toBe(1);
    expect(busB.listenerCount()).toBe(0);
    await act(async () => busA.emit(makeSession({ session_id: "s1" })));
    expect(host.querySelector('[data-testid="active-dialog"]')?.textContent).toBe("s1");

    // Start a concurrent transition switching scope and bus that suspends before commit.
    await act(async () => {
      startTransition(() => {
        setHarness({ scope: "acct-2", useB: true, block: true });
      });
    });
    expect(renderedScopes).toContain("acct-2");
    // Committed busA subscription and active dialog must be completely untouched.
    expect(busA.listenerCount()).toBe(1);
    expect(busB.listenerCount()).toBe(0);
    expect(host.querySelector('[data-testid="active-dialog"]')?.textContent).toBe("s1");

    // Abandon the transition back to acct-1; existing dialog and close handler still work.
    await act(async () => setHarness({ scope: "acct-1", useB: false, block: false }));
    expect(busA.listenerCount()).toBe(1);
    expect(busB.listenerCount()).toBe(0);
    await act(async () => latestClose!());
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[data-testid="active-dialog"]')).toBeNull();

    // Reopen on busA, then actually commit transition to acct-2 + busB -> busA unsubscribes and resets.
    await act(async () => busA.emit(makeSession({ session_id: "s2" })));
    expect(host.querySelector('[data-testid="active-dialog"]')?.textContent).toBe("s2");
    await act(async () => setHarness({ scope: "acct-2", useB: true, block: false }));
    expect(busA.listenerCount()).toBe(0);
    expect(busB.listenerCount()).toBe(1);
    expect(host.querySelector('[data-testid="active-dialog"]')).toBeNull();
    expectNoDefaultSingletons();
  });

  it("preserves default fallback behavior when deps are omitted", async () => {
    getState().machines = [
      makeMachine({ id: "m1", name: "Alpha", hub_id: "h1", hub_name: "Home" }),
      makeMachine({ id: "m2", name: "Beta", hub_id: "h2", hub_name: "Office" }),
    ];
    act(() => root.render(<CloseAgentHost />));
    expect(spies.onOpenCloseAgent).toHaveBeenCalledTimes(1);
    expect(spies.useStore).toHaveBeenCalled();

    await act(async () => {
      openCloseAgent(makeSession({ machine_id: "m1", session_id: "s-def" }));
    });
    await until(() => Boolean(host.querySelector('[data-testid="default-close-dialog"]')));
    const dlg = host.querySelector('[data-testid="default-close-dialog"]');
    expect(dlg?.getAttribute("data-machine")).toBe("Home · Alpha");
    expect(dlg?.textContent).toContain("m1/s-def");

    act(() => host.querySelector<HTMLButtonElement>('[data-testid="default-close-btn"]')!.click());
    expect(host.querySelector('[data-testid="default-close-dialog"]')).toBeNull();
  });
});
