// @vitest-environment jsdom
/// <reference types="node" />
// attention-signals.ts stays an ultra-thin first-screen shell (document.title, favicon badge, desktop Dock badge,
// synchronous gesture-driven AudioContext unlock, and lightweight cold-window `ws_notify` / baseline continuity)
// while the heavy background notification, audio synthesis, Web Push, desktop lifecycle, and Service Worker reply
// runtime loads dynamically in attention-signals-runtime.ts without entering IDLE_TIER_BUDGET or startup-namespaces.json.
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import startup from "../i18n/startup-namespaces.json";
import { startupMatcher } from "../i18n/startup-keys";
import notifyMessages from "../i18n/locales/en/notify.json";
import { idleImports } from "../startup-preload";
import type { AgentState, Interaction, Session } from "./types";

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");

describe("attention-signals first-screen boundary", () => {
  it("keeps heavy notification/audio/SW dependencies and notify i18n out of the first-screen shell and idle tier", () => {
    const shell = read("./attention-signals.ts");
    // No static runtime imports of deferred notification/SW/Web Push/i18n modules or the runtime chunk itself.
    for (const mod of [
      "./notify",
      "./hub-notify",
      "./notification-answer",
      "./web-push-flag",
      "./web-push",
      "./attention-signals-runtime",
      "../i18n",
      "./router",
      "./card-focus",
      "./quick-answers",
    ]) {
      const escaped = mod.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      expect(shell, `unexpected static import of ${mod}`).not.toMatch(new RegExp(`^import\\s+(?!type\\b)[^;]*from\\s+["']${escaped}["']`, "m"));
    }
    expect(shell).toContain('import("./attention-signals-runtime")');
    expect(idleImports(shell)).toEqual([]);

    // App.tsx and look-icon.ts keep their existing imports unchanged.
    const app = read("../App.tsx");
    expect(app).toContain('import { AttentionSignals } from "./lib/attention-signals"');
    expect(app).not.toMatch(/^import\s+(?!type\b)[^;]*from\s+["']\.\/lib\/(?:notify|hub-notify|notification-answer|web-push-flag|attention-signals-runtime)["']/m);
    expect(read("./look-icon.ts")).toContain('import { setFaviconBase } from "./attention-signals"');

    // notify namespace is not in startup-namespaces.json.
    expect(startup.filter((entry) => entry === "notify" || entry.startsWith("notify."))).toEqual([]);
    const inStartup = startupMatcher(startup);
    for (const key of Object.keys(notifyMessages)) expect(inStartup(`notify.${key}`), key).toBe(false);
  });
});

describe("attention-signals lazy shell runtime loading", () => {
  let host: HTMLDivElement;
  let root: Root;
  let badgeCount: number;
  let loaded: boolean;
  let sessions: Session[];
  let interactions: Interaction[];
  let helloListeners: Set<() => void>;
  let hubEventListeners: Map<string, Set<(msg: Record<string, unknown>) => void>>;
  let helloExtrasFn: () => string[];
  let sayHelloMock: ReturnType<typeof vi.fn>;
  let sendMock: ReturnType<typeof vi.fn>;
  let answerCardMock: ReturnType<typeof vi.fn<(ix: Interaction, body: unknown) => Promise<{ ok: true; already: false }>>>;
  let posted: { title: string; body?: string; tag?: string }[];

  const session = (id: string, state: AgentState, extra: Partial<Session> = {}): Session => ({
    machine_id: "m",
    session_id: id,
    title: id,
    cwd: "/p",
    status: state,
    state,
    last_text: `${id} last line`,
    ...extra,
  });

  const emitHubEvent = (type: string, msg: Record<string, unknown>) => {
    hubEventListeners.get(type)?.forEach((fn) => fn(msg));
  };

  function stubCanvasAndImage() {
    vi.stubGlobal(
      "Image",
      class {
        onload: (() => void) | null = null;
        onerror: (() => void) | null = null;
        set src(_: string) {
          queueMicrotask(() => this.onload?.());
        }
      },
    );
    const ctx2d = {
      drawImage: vi.fn(),
      beginPath: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
      stroke: vi.fn(),
      fillText: vi.fn(),
      fillStyle: "",
      strokeStyle: "",
      lineWidth: 0,
      font: "",
      textAlign: "",
      textBaseline: "",
    };
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx2d as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue("data:image/png;base64,BADGED");
  }

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.resetModules();
    localStorage.clear();
    document.title = "";
    document.head.innerHTML = "";
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    badgeCount = 0;
    loaded = true;
    sessions = [];
    interactions = [];
    helloListeners = new Set();
    hubEventListeners = new Map();
    helloExtrasFn = () => [];
    sayHelloMock = vi.fn();
    sendMock = vi.fn();
    answerCardMock = vi.fn(async (_ix: Interaction, _body: unknown) => ({ ok: true as const, already: false }));
    posted = [];
    class FakeNotification {
      static permission = "granted";
      static requestPermission = async () => "granted";
      onclick: (() => void) | null = null;
      onclose: (() => void) | null = null;
      constructor(title: string, opts?: { body?: string; tag?: string }) {
        posted.push({ title, body: opts?.body, tag: opts?.tag });
      }
      close() {}
    }
    vi.stubGlobal("Notification", FakeNotification);
    vi.spyOn(document, "hasFocus").mockReturnValue(false);
    vi.doMock("./store", () => ({
      useStore: (select: (s: { loaded: boolean; interactions: Interaction[]; pushGrading: boolean }) => unknown) =>
        select({ loaded, interactions, pushGrading: false }),
      getState: () => ({
        conn: "connecting",
        mutes: { spaces: [], sessions: [], allowed: [] },
        interactions,
        pushGrading: false,
        features: ["desktop_unregister"],
        sessions: {},
      }),
      onHello: (fn: () => void) => {
        helloListeners.add(fn);
        return () => void helloListeners.delete(fn);
      },
      onHubEvent: (type: string, fn: (msg: Record<string, unknown>) => void) => {
        let set = hubEventListeners.get(type);
        if (!set) {
          set = new Set();
          hubEventListeners.set(type, set);
        }
        set.add(fn);
        return () => void set?.delete(fn);
      },
      send: sendMock,
      sayHello: sayHelloMock,
      setHelloExtras: (fn: () => string[]) => {
        helloExtrasFn = fn;
      },
      appSocketReady: () => true,
      connect: () => {},
      disconnect: () => {},
      DESKTOP_UNREGISTER_FEATURE: "desktop_unregister",
    }));
    vi.doMock("./sessions", () => ({
      useCounts: () => ({ badge: badgeCount }),
      useAgentSessions: () => sessions,
    }));
    vi.doMock("./api", async (orig) => ({
      ...(await orig<typeof import("./api")>()),
      api: async () => ({ input: true, done: "long", long_turn_seconds: 60 }),
      enc: encodeURIComponent,
    }));
    vi.doMock("./card-answer", () => ({
      answerCard: (ix: Interaction, body: unknown) => answerCardMock(ix, body),
    }));
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    delete (window as { personaDesktop?: unknown }).personaDesktop;
    vi.doUnmock("./attention-signals-runtime");
    vi.doUnmock("./store");
    vi.doUnmock("./sessions");
    vi.doUnmock("./api");
    vi.doUnmock("./card-answer");
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("renders null and syncs document.title, favicon base/badge, and desktop badge immediately before the lazy runtime resolves", async () => {
    stubCanvasAndImage();
    const link = document.createElement("link");
    link.setAttribute("rel", "icon");
    link.setAttribute("href", "/icon-light.png");
    document.head.append(link);

    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const setBadge = vi.fn();
    Object.defineProperty(window, "personaDesktop", { value: { setBadge, deviceName: "Deck", notificationPermission: () => "granted" }, configurable: true });

    const { AttentionSignals, setFaviconBase } = await import("./attention-signals");
    setFaviconBase({ light: "data:image/png;base64,LIGHT", dark: "data:image/png;base64,DARK" });
    expect(link.getAttribute("href")).toBe("data:image/png;base64,LIGHT");

    badgeCount = 2;
    await act(async () => {
      root.render(<AttentionSignals label="Agents" pane={undefined} />);
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(host.innerHTML).toBe("");
    expect(document.title).toBe("(2) Agents · Persona");
    expect(setBadge).toHaveBeenLastCalledWith(2);
    expect(link.getAttribute("href")).toBe("data:image/png;base64,BADGED");

    // Resetting badge to 0 restores the custom look's base icon immediately.
    badgeCount = 0;
    await act(async () => {
      root.render(<AttentionSignals label="Agents" pane={undefined} />);
    });
    expect(document.title).toBe("Agents · Persona");
    expect(link.getAttribute("href")).toBe("data:image/png;base64,LIGHT");
    expect(setBadge).toHaveBeenLastCalledWith(0);

    // Finish loading runtime so no dangling promise remains.
    await act(async () => {
      releaseRuntime(runtimeMod);
      await Promise.resolve();
    });
  });

  it("replays buffered transitions and early hello events that arrive while the lazy runtime is still loading", async () => {
    localStorage.setItem("persona.systemNotify", "1");
    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    Object.defineProperty(window, "personaDesktop", {
      value: {
        deviceName: "Deck",
        setBadge: vi.fn(),
        notify: () => false,
        closeNotification: vi.fn(),
        clearNotifications: vi.fn(),
        notificationPermission: () => "granted",
        desktopPresence: () => ({ active: true, notifies: true }),
      },
      configurable: true,
    });

    const { AttentionSignals, preloadAttentionSignals } = await import("./attention-signals");
    sessions = [session("a", "working")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    // Both hello and a working -> needs_you transition happen before the lazy chunk resolves.
    helloListeners.forEach((fn) => fn());
    sessions = [session("a", "needs_you")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });
    expect(posted).toEqual([]);
    expect(sendMock).not.toHaveBeenCalled();

    // Once the runtime chunk resolves, both desktop registration and the buffered notification fire.
    await act(async () => {
      releaseRuntime(runtimeMod);
      await preloadAttentionSignals();
    });
    expect(sendMock).toHaveBeenCalledWith({ type: "register_desktop", device_name: "Deck" });
    expect(posted).toEqual([expect.objectContaining({ tag: "m/a#needs" })]);
  });

  it("declares ws_notify synchronously on initial hello and replays post-silence cold hub notify events without dropping the ring", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-26T00:00:00.000Z"));
    localStorage.setItem("persona.systemNotify", "1");
    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const { AttentionSignals, preloadAttentionSignals } = await import("./attention-signals");
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    // The socket sends its initial client_hello while the lazy runtime is still loading: ws_notify MUST be declared.
    expect(helloExtrasFn()).toEqual(["ws_notify"]);
    sayHelloMock.mockClear();

    // A hub replay burst inside the 3s startup silence window stays suppressed.
    vi.advanceTimersByTime(500);
    emitHubEvent("notify", { kind: "alert", title: "Old replay", body: "Suppressed burst", path: "/persona", tag: "hub#old" });

    // A fresh hub alert arriving after the 3s startup silence window while the chunk is still cold must be preserved.
    vi.advanceTimersByTime(3_000);
    emitHubEvent("notify", { kind: "alert", title: "Live alert", body: "Arrived during cold load", path: "/persona", tag: "hub#live" });
    expect(posted).toEqual([]);

    // Runtime handoff keeps ws_notify state (no redundant sayHello / reset silence window) and announces only the live alert.
    await act(async () => {
      releaseRuntime(runtimeMod);
      await preloadAttentionSignals();
    });
    expect(sayHelloMock).not.toHaveBeenCalled();
    expect(posted).toEqual([expect.objectContaining({ title: "Live alert", body: "Arrived during cold load" })]);
    expect(posted[0]?.tag).toMatch(/^alert:/);
  });

  it("preserves a newly added session's working baseline across >32 cold snapshot updates so its needs_you alert is never dropped", async () => {
    localStorage.setItem("persona.systemNotify", "1");
    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const { AttentionSignals, preloadAttentionSignals } = await import("./attention-signals");
    sessions = [session("busy", "working")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    // Churn >32 snapshots on an existing session before a brand-new session appears in working state.
    for (let i = 1; i <= 35; i++) {
      sessions = [session("busy", "working", { last_text: `tick ${i}` })];
      await act(async () => {
        root.render(<AttentionSignals label="Persona" pane={undefined} />);
      });
    }

    // New session appears as working on snapshot 36, then transitions to needs_you with a permission card on snapshot 37.
    sessions = [session("busy", "working"), session("late", "working")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    const lateCard: Interaction = {
      id: "perm-late",
      machine_id: "m",
      session_id: "late",
      kind: "permission",
      tool_name: "Bash",
      tool_input: { command: "rm -rf build" },
    };
    interactions = [lateCard];
    sessions = [session("busy", "working"), session("late", "needs_you")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    // Additional churn updates after the transition must not wipe out the late session's baseline either.
    for (let i = 38; i <= 45; i++) {
      sessions = [session("busy", "working", { last_text: `tick ${i}` }), session("late", "needs_you")];
      await act(async () => {
        root.render(<AttentionSignals label="Persona" pane={undefined} />);
      });
    }
    expect(posted).toEqual([]);

    await act(async () => {
      releaseRuntime(runtimeMod);
      await preloadAttentionSignals();
    });
    expect(posted).toEqual([expect.objectContaining({ tag: "m/late#needs" })]);
    expect(posted[0]?.body).toContain("rm -rf build");
  });

  it("synchronously unlocks AudioContext on a sole user gesture during cold load so runtime plays sound after activation expires", async () => {
    localStorage.setItem("persona.attentionSound", "1");
    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const contexts: { state: string; resume: ReturnType<typeof vi.fn>; oscCount: number }[] = [];
    let gestureActive = false;
    class FakeAudioContext {
      state = "suspended";
      currentTime = 0;
      destination = {};
      oscCount = 0;
      resume = vi.fn(() => {
        if (gestureActive) this.state = "running";
        return Promise.resolve();
      });
      constructor() {
        contexts.push(this);
      }
      createOscillator() {
        this.oscCount++;
        return {
          type: "sine",
          frequency: { value: 0 },
          connect: (g: unknown) => g,
          start: vi.fn(),
          stop: vi.fn(),
        };
      }
      createGain() {
        return {
          gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
          connect: () => ({}),
        };
      }
    }
    vi.stubGlobal("AudioContext", FakeAudioContext);
    Object.defineProperty(navigator, "userActivation", { value: { isActive: false }, configurable: true });

    const { AttentionSignals, preloadAttentionSignals } = await import("./attention-signals");
    sessions = [session("a", "working")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });

    // User performs their sole click while the lazy runtime chunk is still pending; transient activation ends right after.
    gestureActive = true;
    window.dispatchEvent(new Event("pointerdown"));
    gestureActive = false;

    expect(contexts).toHaveLength(1);
    expect(contexts[0].resume).toHaveBeenCalledTimes(1);
    expect(contexts[0].state).toBe("running");

    // Runtime finishes loading after transient activation has expired, then a transition happens: sound plays on the pre-unlocked context.
    await act(async () => {
      releaseRuntime(runtimeMod);
      await preloadAttentionSignals();
    });
    sessions = [session("a", "needs_you")];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });
    expect(contexts).toHaveLength(1);
    expect(contexts[0].oscCount).toBe(2);
  });

  it("cancels runtime mount cleanly when unmounted before the lazy chunk resolves and clears early hello extras", async () => {
    localStorage.setItem("persona.systemNotify", "1");
    const createSpy = vi.fn(() => ({ setPane: vi.fn(), syncLoaded: vi.fn(), update: vi.fn(), replayHubNotify: vi.fn(), dispose: vi.fn() }));
    let releaseRuntime!: (mod: Partial<typeof import("./attention-signals-runtime")>) => void;
    const runtimeDeferred = new Promise<Partial<typeof import("./attention-signals-runtime")>>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const { AttentionSignals } = await import("./attention-signals");
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });
    expect(helloExtrasFn()).toEqual(["ws_notify"]);

    await act(async () => {
      root.unmount();
    });
    expect(helloExtrasFn()).toEqual([]);

    await act(async () => {
      releaseRuntime({ createAttentionRuntime: createSpy, playOnContext: () => {} });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(createSpy).not.toHaveBeenCalled();
  });

  it("retries loading the lazy runtime after a transient load failure when loaded becomes true", async () => {
    const createSpy = vi.fn(() => ({ setPane: vi.fn(), syncLoaded: vi.fn(), update: vi.fn(), replayHubNotify: vi.fn(), dispose: vi.fn() }));
    let attempt = 0;
    vi.doMock("./attention-signals-runtime", () => {
      attempt++;
      if (attempt === 1) return Promise.reject(new Error("offline"));
      return Promise.resolve({ createAttentionRuntime: createSpy, playOnContext: () => {} });
    });

    const { AttentionSignals } = await import("./attention-signals");
    loaded = false;
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
      await Promise.resolve();
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(attempt).toBe(1));
    expect(createSpy).not.toHaveBeenCalled();

    // Coming online (`loaded = true`) retries loading the lazy runtime automatically.
    loaded = true;
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
      await Promise.resolve();
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(createSpy).toHaveBeenCalledTimes(1));
    expect(attempt).toBe(2);
  });

  it("answers Service Worker notification clicks once the lazy runtime loads and forwards cold play calls when audio is unlocked", async () => {
    localStorage.setItem("persona.attentionSound", "1");
    const swListeners = new Set<(e: MessageEvent) => void>();
    const startMessages = vi.fn();
    vi.stubGlobal("navigator", {
      ...navigator,
      userActivation: { isActive: true },
      serviceWorker: {
        addEventListener: (_type: string, fn: (e: MessageEvent) => void) => swListeners.add(fn),
        removeEventListener: (_type: string, fn: (e: MessageEvent) => void) => swListeners.delete(fn),
        startMessages,
      },
    });
    class RunningAudioContext {
      state = "running";
      resume = vi.fn(() => Promise.resolve());
    }
    vi.stubGlobal("AudioContext", RunningAudioContext);

    const playOnContextSpy = vi.fn();
    const runtimeMod = await vi.importActual<typeof import("./attention-signals-runtime")>("./attention-signals-runtime");
    let releaseRuntime!: (mod: typeof import("./attention-signals-runtime")) => void;
    const runtimeDeferred = new Promise<typeof import("./attention-signals-runtime")>((resolve) => { releaseRuntime = resolve; });
    vi.doMock("./attention-signals-runtime", () => runtimeDeferred);

    const { AttentionSignals, armAudio, play } = await import("./attention-signals");
    const stopAudio = armAudio();
    play("needs");

    const perm: Interaction = { id: "p1", machine_id: "m", session_id: "a", kind: "permission", tool_name: "Bash", tool_input: { command: "ls" } };
    interactions = [perm];
    await act(async () => {
      root.render(<AttentionSignals label="Persona" pane={undefined} />);
    });
    await act(async () => {
      releaseRuntime({ ...runtimeMod, playOnContext: playOnContextSpy });
      await Promise.resolve();
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(swListeners.size).toBe(1));

    expect(playOnContextSpy).toHaveBeenCalledWith(expect.any(RunningAudioContext), "needs");
    stopAudio();
    expect(startMessages).toHaveBeenCalledTimes(1);

    const port = { postMessage: vi.fn() };
    swListeners.forEach((fn) =>
      fn({
        data: { type: "persona.notification", action: "allow", ix: "p1", url: "/s/m/a", until: Date.now() + 5_000 },
        ports: [port],
      } as unknown as MessageEvent),
    );
    expect(port.postMessage).toHaveBeenCalledWith(true);
    expect(answerCardMock).toHaveBeenCalledWith(perm, { allow: true });
  });
});
