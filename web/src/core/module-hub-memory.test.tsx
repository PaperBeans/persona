// @vitest-environment jsdom
// Remembering a hub's module flags across full page reloads (#14431, docs/web/web.md "When a module is there",
// docs/apple/ios.md "Modules" #13849): a module the hub turned off (`modules` without `module.<id>`) stays off on a
// reload before the new hello arrives, so it is never drawn and its code chunk is never fetched; first-seen hubs and
// older hubs (without `modules`) keep modules on, each hub is isolated by origin and subpath, and corrupt or blocked
// storage degrades safely.
import { Suspense, act, useLayoutEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { storedKeyAlias } from "../lib/compatibility";
import type { Interaction } from "../lib/types";
import type { ChatPageProps, ChatSlotComponents, ComposerAction, ModuleCode, ModuleDescriptor } from "./module-host";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HUB_A = "https://hub-a.example";
const HUB_B = "https://hub-b.example";
const PROXY_ORIGIN = "https://proxy.example";
const CREDENTIAL_KEYS = new Set(["persona.token", "persona.tokenId"].flatMap((key) => [key, storedKeyAlias(key)!]));

class Socket {
  static last: Socket | undefined;
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readyState = Socket.OPEN;
  onopen?: () => void;
  onclose?: (event: { code: number }) => void;
  onmessage?: (event: { data: string }) => void;
  constructor(public url: string) {
    Socket.last = this;
  }
  send() {}
  close() {
    this.readyState = Socket.CLOSED;
  }
  push(value: unknown) {
    this.onmessage?.({ data: JSON.stringify(value) });
  }
}

const chatPageProps: ChatPageProps = {
  mid: "m",
  sid: "s",
  owner: "o",
  session: undefined,
  assistant: false,
  label: "Claude",
  persona: "Persona",
  canSend: true,
  items: () => [],
  cards: [],
};
const sampleInteraction = { id: "ix-1", machine_id: "m", session_id: "s", kind: "question" } as unknown as Interaction;

interface BootedPage {
  store: typeof import("../lib/store");
  host: typeof import("./module-host");
  router: typeof import("../lib/router");
  modulePage: typeof import("./ModulePage");
  chatSlots: typeof import("./ChatSlots");
  descriptors: {
    health: ModuleDescriptor;
    voice: ModuleDescriptor;
    recording: ModuleDescriptor;
    notes: ModuleDescriptor;
  };
  spies: {
    healthLoad: ReturnType<typeof vi.fn<() => Promise<ModuleCode>>>;
    healthStrings: ReturnType<typeof vi.fn<() => Promise<{ default: Record<string, string>; fallbacks: Record<string, string> }>>>;
    voiceLoad: ReturnType<typeof vi.fn<() => Promise<ModuleCode>>>;
    voiceStrings: ReturnType<typeof vi.fn<() => Promise<{ default: Record<string, string>; fallbacks: Record<string, string> }>>>;
    recordingLoad: ReturnType<typeof vi.fn<() => Promise<ModuleCode>>>;
    notesLoad: ReturnType<typeof vi.fn<() => Promise<ModuleCode>>>;
  };
}

let current: BootedPage | null = null;
let container: HTMLDivElement | null = null;
let root: Root | null = null;

function setHubLocation(origin: string, baseHref?: string) {
  const url = new URL(baseHref ?? "/", origin);
  vi.stubGlobal("location", {
    href: url.href,
    origin: url.origin,
    protocol: url.protocol,
    host: url.host,
    hostname: url.hostname,
    port: url.port,
    pathname: url.pathname,
    search: url.search,
    hash: url.hash,
    assign: vi.fn(),
    replace: vi.fn(),
    reload: vi.fn(),
  });
  for (const el of [...document.head.querySelectorAll("base")]) el.remove();
  if (baseHref) {
    const base = document.createElement("base");
    base.setAttribute("href", baseHref);
    document.head.append(base);
  }
}

function stubNetwork() {
  vi.stubGlobal("WebSocket", Socket);
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    // Keep background /assistant and /agents/prefs refreshes from writing unrelated cache keys to localStorage on hello.
    if (url.includes("/assistant") || url.includes("/agents/prefs")) {
      return new Response(JSON.stringify({ error: "unavailable" }), {
        status: 503,
        headers: { "Content-Type": "application/json" },
      });
    }
    return Response.json({});
  }));
}

function storageKeys(): string[] {
  const keys: string[] = [];
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (key !== null) keys.push(key);
  }
  return keys;
}

function unmountShell() {
  if (root) {
    act(() => root!.unmount());
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
}

async function flush() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(200);
    for (let i = 0; i < 6; i++) await Promise.resolve();
  });
}

/** Simulates a full page load / reload while keeping localStorage intact (no sign-out reset). */
async function reload(): Promise<BootedPage> {
  unmountShell();
  if (current) {
    current.store.disconnect();
    current = null;
  }
  Socket.last = undefined;
  vi.clearAllTimers();
  vi.resetModules();

  const [store, host, router, modulePage, chatSlots] = await Promise.all([
    import("../lib/store"),
    import("./module-host"),
    import("../lib/router"),
    import("./ModulePage"),
    import("./ChatSlots"),
  ]);

  host.connectModuleHub(store.getState, store.subscribe);

  const healthStrings = vi.fn(async () => ({ default: { "health.title": "Health" }, fallbacks: {} }));
  const healthLoad = vi.fn<() => Promise<ModuleCode>>(async () => ({
    Page: () => <div data-health-page>Health page</div>,
  }));

  const voiceChat: ChatSlotComponents = {
    "composer.trailingAction": ({ offer, label }) => {
      useLayoutEffect(() => {
        offer({ icon: "heart", name: "Voice", label: `Voice ${label}`, primary: true, onClick: () => {} });
        return () => offer(undefined);
      }, [offer, label]);
      return <span data-voice-action />;
    },
    "chat.tail": ({ sid }) => <span data-voice-tail>{sid}</span>,
    "chat.bar": ({ place, take }) => {
      useLayoutEffect(() => {
        if (place === "composer") take(true);
        return () => {
          if (place === "composer") take(false);
        };
      }, [place, take]);
      return place === "composer" ? <div data-voice-bar /> : null;
    },
    "interaction.asked": ({ interaction }) => <span data-voice-mark>{interaction.id}</span>,
  };
  const voiceStrings = vi.fn(async () => ({ default: { "voice.title": "Voice" }, fallbacks: {} }));
  const voiceLoad = vi.fn<() => Promise<ModuleCode>>(async () => ({
    Page: () => <div data-voice-page>Voice page</div>,
    chat: voiceChat,
  }));

  const recordingLoad = vi.fn<() => Promise<ModuleCode>>(async () => ({
    Page: () => <div data-recording-page>Recording page</div>,
  }));
  const notesLoad = vi.fn<() => Promise<ModuleCode>>(async () => ({
    Page: () => <div data-notes-page>Notes page</div>,
  }));

  const health: ModuleDescriptor = {
    id: "health",
    feature: host.moduleFeature("health"),
    title: () => "Health",
    description: () => "Health tracking",
    routes: [{ base: "health", aliases: [["tracks", "health"]], title: () => "Health" }],
    navItems: [{ key: "health", path: "/health", icon: "heart", label: () => "Health", after: "inbox" }],
    i18nNamespaces: ["health"],
    locales: { en: healthStrings, "zh-Hans": healthStrings },
    load: healthLoad,
  };

  const voice: ModuleDescriptor = {
    id: "voice",
    feature: host.moduleFeature("voice"),
    title: () => "Voice",
    description: () => "Voice calls",
    routes: [{ base: "voice", title: () => "Voice" }],
    navItems: [{ key: "voice", path: "/voice", icon: "heart", label: () => "Voice", after: "inbox" }],
    chatSlots: ["composer.trailingAction", "chat.tail", "chat.bar", "interaction.asked"],
    i18nNamespaces: ["voice"],
    locales: { en: voiceStrings, "zh-Hans": voiceStrings },
    load: voiceLoad,
  };

  const recording: ModuleDescriptor = {
    id: "recording",
    feature: host.moduleFeature("recording"),
    title: () => "Recording",
    description: () => "Meeting recordings",
    routes: [{ base: "recording", title: () => "Recording" }],
    navItems: [{ key: "recording", path: "/recording", icon: "heart", label: () => "Recording", after: "inbox" }],
    load: recordingLoad,
  };

  const notes: ModuleDescriptor = {
    id: "notes",
    feature: undefined,
    title: () => "Notes",
    description: () => "Local ungated module",
    routes: [{ base: "notes", title: () => "Notes" }],
    navItems: [{ key: "notes", path: "/notes", icon: "heart", label: () => "Notes", after: "inbox" }],
    load: notesLoad,
  };

  host.registerModule(health);
  host.registerModule(voice);
  host.registerModule(recording);
  host.registerModule(notes);

  current = {
    store,
    host,
    router,
    modulePage,
    chatSlots,
    descriptors: { health, voice, recording, notes },
    spies: { healthLoad, healthStrings, voiceLoad, voiceStrings, recordingLoad, notesLoad },
  };
  return current;
}

async function deliverHello(features: string[]) {
  if (!Socket.last) throw new Error("expected an open WebSocket before sending hello");
  Socket.last.push({ type: "hello", features });
  await flush();
}

async function mountShell(page: BootedPage) {
  unmountShell();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);

  const observed: { action: ComposerAction | undefined; taken: boolean } = {
    action: undefined,
    taken: false,
  };

  const { ModulePage } = page.modulePage;
  const { ChatTail, InteractionMarks, useChatBar, useComposerAction } = page.chatSlots;
  const { useModuleStates } = page.host;

  function Shell() {
    useModuleStates();
    const { action, slot } = useComposerAction(chatPageProps);
    const bar = useChatBar(chatPageProps);
    observed.action = action;
    observed.taken = bar.taken;
    return (
      <div>
        <div data-page-slot>
          <Suspense fallback={null}>
            <ModulePage route={{ name: "module", module: "health", base: "health", path: [] }} />
          </Suspense>
        </div>
        <div data-chat-slots>
          {slot}
          <ChatTail {...chatPageProps} />
          {bar.bar("dock")}
          {bar.bar("composer")}
          <InteractionMarks interaction={sampleInteraction} />
        </div>
      </div>
    );
  }

  await act(async () => {
    root!.render(<Shell />);
  });
  return { host: container, observed };
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  localStorage.setItem("persona.token", "demo-token");
  localStorage.setItem("persona.notifyDevice", "web-demo-device");
  setHubLocation(HUB_A);
  stubNetwork();
});

afterEach(() => {
  unmountShell();
  if (current) {
    current.store.disconnect();
    current = null;
  }
  Socket.last = undefined;
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  for (const el of [...document.head.querySelectorAll("base")]) el.remove();
  localStorage.clear();
});

describe("remembering hub module flags across reloads (#14431)", () => {
  it("keeps gated and ungated modules on before hello on a first-seen hub", async () => {
    const page = await reload();
    const { host, router, descriptors } = page;

    expect(host.hubServes(host.moduleFeature("health"))).toBe(true);
    expect(host.hubServes(host.moduleFeature("voice"))).toBe(true);
    expect(host.moduleState(descriptors.health)).toBe("on");
    expect(host.moduleState(descriptors.voice)).toBe("on");
    expect(host.moduleState(descriptors.notes)).toBe("on");
    expect(router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
    expect(host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);
    expect(host.chatSlotFillers("composer.trailingAction").map((f) => f.id)).toEqual(["voice"]);

    // Also while the WebSocket is open and awaiting its first hello.
    page.store.connect();
    expect(host.hubServes(host.moduleFeature("health"))).toBe(true);
    expect(router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
    expect(host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);

    // Rendering the shell on a first-seen hub draws and loads the enabled modules before hello.
    const { host: dom, observed } = await mountShell(page);
    await flush();
    expect(dom.querySelector("[data-health-page]")?.textContent).toBe("Health page");
    expect(dom.querySelector("[data-voice-action]")).not.toBeNull();
    expect(dom.querySelector("[data-voice-tail]")?.textContent).toBe("s");
    expect(dom.querySelector("[data-voice-bar]")).not.toBeNull();
    expect(dom.querySelector("[data-voice-mark]")?.textContent).toBe("ix-1");
    expect(observed.action).toMatchObject({ name: "Voice", primary: true });
    expect(observed.taken).toBe(true);
    expect(page.spies.healthLoad).toHaveBeenCalledTimes(1);
    expect(page.spies.voiceLoad).toHaveBeenCalledTimes(1);
  });

  it("keeps modules disabled by a publishing hub off synchronously on reload before hello and never draws or fetches their code", async () => {
    // First load: publishing hub serves only `recording`, leaving `health` and `voice` off.
    const first = await reload();
    first.store.connect();
    await deliverHello([first.host.MODULES_FEATURE, first.host.moduleFeature("recording")]);
    expect(first.host.moduleState(first.descriptors.health)).toBe("off");
    expect(first.host.moduleState(first.descriptors.voice)).toBe("off");
    expect(first.host.moduleState(first.descriptors.recording)).toBe("on");

    // Full page reload: check synchronously BEFORE any WebSocket hello arrives.
    const next = await reload();
    const { host, router, descriptors, spies } = next;

    expect(host.hubServes(host.moduleFeature("health"))).toBe(false);
    expect(host.hubServes(host.moduleFeature("voice"))).toBe(false);
    expect(host.hubServes(host.moduleFeature("recording"))).toBe(true);
    expect(host.hubServes(undefined)).toBe(true);

    expect(host.moduleState(descriptors.health)).toBe("off");
    expect(host.moduleState(descriptors.voice)).toBe("off");
    expect(host.moduleState(descriptors.recording)).toBe("on");
    expect(host.moduleState(descriptors.notes)).toBe("on");

    expect(router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(router.parse("/health/water")).toEqual({ name: "track", slug: "health", sid: "water" });
    expect(router.parse("/tracks/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(router.parse("/voice")).toEqual({ name: "not-found", path: "/voice" });
    expect(router.parse("/recording")).toEqual({ name: "module", module: "recording", base: "recording", path: [] });
    expect(router.parse("/notes")).toEqual({ name: "module", module: "notes", base: "notes", path: [] });

    expect(host.enabledNavItems().map((n) => n.key)).toEqual(["recording", "notes"]);
    for (const slot of ["composer.trailingAction", "chat.tail", "chat.bar", "interaction.asked"] as const) {
      expect(host.chatSlotFillers(slot), slot).toEqual([]);
    }

    // Open socket (still no hello) and render ModulePage + all ChatSlots.
    next.store.connect();
    const { host: dom, observed } = await mountShell(next);
    await flush();

    expect(dom.querySelector("[data-page-slot]")?.innerHTML).toBe("");
    expect(dom.querySelector("[data-chat-slots]")?.innerHTML).toBe("");
    expect(observed).toEqual({ action: undefined, taken: false });
    expect(spies.healthLoad).not.toHaveBeenCalled();
    expect(spies.healthStrings).not.toHaveBeenCalled();
    expect(spies.voiceLoad).not.toHaveBeenCalled();
    expect(spies.voiceStrings).not.toHaveBeenCalled();

    await expect(host.loadModule("health")).rejects.toThrow(/not enabled/);
    await expect(host.loadModule("voice")).rejects.toThrow(/not enabled/);
    expect(spies.healthLoad).not.toHaveBeenCalled();
    expect(spies.voiceLoad).not.toHaveBeenCalled();
  });

  it("disables all gated modules while keeping ungated modules on after reload when the hub published modules alone", async () => {
    const first = await reload();
    first.store.connect();
    await deliverHello([first.host.MODULES_FEATURE]);

    const next = await reload();
    const { host, router, descriptors, spies } = next;

    expect(host.hubServes(host.moduleFeature("health"))).toBe(false);
    expect(host.hubServes(host.moduleFeature("voice"))).toBe(false);
    expect(host.hubServes(host.moduleFeature("recording"))).toBe(false);
    expect(host.hubServes(undefined)).toBe(true);

    expect(host.moduleState(descriptors.health)).toBe("off");
    expect(host.moduleState(descriptors.voice)).toBe("off");
    expect(host.moduleState(descriptors.recording)).toBe("off");
    expect(host.moduleState(descriptors.notes)).toBe("on");

    expect(router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(router.parse("/recording")).toEqual({ name: "not-found", path: "/recording" });
    expect(router.parse("/notes")).toEqual({ name: "module", module: "notes", base: "notes", path: [] });
    expect(host.enabledNavItems().map((n) => n.key)).toEqual(["notes"]);

    await mountShell(next);
    await flush();
    expect(spies.healthLoad).not.toHaveBeenCalled();
    expect(spies.voiceLoad).not.toHaveBeenCalled();
    expect(spies.recordingLoad).not.toHaveBeenCalled();
  });

  it("does not disable modules on reload for an older hub whose hello lacks the modules marker", async () => {
    for (const features of [["terminal_stream"], ["module.health"], []]) {
      localStorage.clear();
      localStorage.setItem("persona.token", "demo-token");
      localStorage.setItem("persona.notifyDevice", "web-demo-device");

      const first = await reload();
      first.store.connect();
      await deliverHello(features);
      expect(first.host.moduleState(first.descriptors.health)).toBe("on");
      expect(first.host.moduleState(first.descriptors.voice)).toBe("on");

      const next = await reload();
      expect(next.host.hubServes(next.host.moduleFeature("health")), JSON.stringify(features)).toBe(true);
      expect(next.host.hubServes(next.host.moduleFeature("voice")), JSON.stringify(features)).toBe(true);
      expect(next.host.moduleState(next.descriptors.health)).toBe("on");
      expect(next.host.moduleState(next.descriptors.voice)).toBe("on");
      expect(next.router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
      expect(next.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);
    }
  });

  it("clears remembered disabled state when a publishing hub downgrades to an older hub without the modules marker", async () => {
    for (const olderFeatures of [["terminal_stream"], ["module.health"], []]) {
      localStorage.clear();
      localStorage.setItem("persona.token", "demo-token");
      localStorage.setItem("persona.notifyDevice", "web-demo-device");

      // Step 1: publishing hub disables all gated modules.
      const publishing = await reload();
      publishing.store.connect();
      await deliverHello([publishing.host.MODULES_FEATURE]);

      // Step 2: reload remembers them as off before hello.
      const downgraded = await reload();
      expect(downgraded.host.moduleState(downgraded.descriptors.health)).toBe("off");
      expect(downgraded.host.moduleState(downgraded.descriptors.voice)).toBe("off");

      // Step 3: live hello from older hub (without `modules`) turns them back on and clears memory.
      downgraded.store.connect();
      await deliverHello(olderFeatures);
      expect(downgraded.host.moduleState(downgraded.descriptors.health)).toBe("on");
      expect(downgraded.host.moduleState(downgraded.descriptors.voice)).toBe("on");

      // Step 4: next reload starts with modules on before hello.
      const afterDowngrade = await reload();
      expect(afterDowngrade.host.hubServes(afterDowngrade.host.moduleFeature("health")), JSON.stringify(olderFeatures)).toBe(true);
      expect(afterDowngrade.host.hubServes(afterDowngrade.host.moduleFeature("voice")), JSON.stringify(olderFeatures)).toBe(true);
      expect(afterDowngrade.host.moduleState(afterDowngrade.descriptors.health)).toBe("on");
      expect(afterDowngrade.host.moduleState(afterDowngrade.descriptors.voice)).toBe("on");
      expect(afterDowngrade.router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
      expect(afterDowngrade.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);
    }
  });

  it("applies live hello transitions in both directions and persists them for the following reload", async () => {
    // Hub starts with health ON, voice OFF.
    const first = await reload();
    first.store.connect();
    await deliverHello([first.host.MODULES_FEATURE, first.host.moduleFeature("health")]);

    // Reload: before hello, health is ON and voice is OFF (voice slots empty and chunk unfetched).
    const second = await reload();
    expect(second.host.moduleState(second.descriptors.health)).toBe("on");
    expect(second.host.moduleState(second.descriptors.voice)).toBe("off");

    second.store.connect();
    const { host: dom, observed } = await mountShell(second);
    await flush();
    expect(dom.querySelector("[data-voice-action]")).toBeNull();
    expect(dom.querySelector("[data-voice-tail]")).toBeNull();
    expect(dom.querySelector("[data-voice-bar]")).toBeNull();
    expect(dom.querySelector("[data-voice-mark]")).toBeNull();
    expect(observed).toEqual({ action: undefined, taken: false });
    expect(second.spies.voiceLoad).not.toHaveBeenCalled();

    // Live hello flips both: voice turns ON and health turns OFF.
    await deliverHello([second.host.MODULES_FEATURE, second.host.moduleFeature("voice")]);

    expect(second.host.hubServes(second.host.moduleFeature("voice"))).toBe(true);
    expect(second.host.moduleState(second.descriptors.voice)).toBe("on");
    expect(second.host.hubServes(second.host.moduleFeature("health"))).toBe(false);
    expect(second.host.moduleState(second.descriptors.health)).toBe("off");
    expect(second.router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(second.host.enabledNavItems().map((n) => n.key)).toEqual(["voice", "notes"]);

    // Voice chat slots reactively load and draw now that the live hello enabled it.
    expect(second.spies.voiceLoad).toHaveBeenCalledTimes(1);
    expect(dom.querySelector("[data-voice-action]")).not.toBeNull();
    expect(dom.querySelector("[data-voice-tail]")?.textContent).toBe("s");
    expect(dom.querySelector("[data-voice-bar]")).not.toBeNull();
    expect(dom.querySelector("[data-voice-mark]")?.textContent).toBe("ix-1");
    expect(observed.action).toMatchObject({ name: "Voice", primary: true });
    expect(observed.taken).toBe(true);

    // Following reload starts with the updated flags BEFORE hello: voice ON, health OFF (never fetched).
    const third = await reload();
    expect(third.host.hubServes(third.host.moduleFeature("voice"))).toBe(true);
    expect(third.host.moduleState(third.descriptors.voice)).toBe("on");
    expect(third.host.hubServes(third.host.moduleFeature("health"))).toBe(false);
    expect(third.host.moduleState(third.descriptors.health)).toBe("off");
    expect(third.router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(third.host.enabledNavItems().map((n) => n.key)).toEqual(["voice", "notes"]);

    await mountShell(third);
    await flush();
    expect(third.spies.healthLoad).not.toHaveBeenCalled();
    expect(third.spies.voiceLoad).toHaveBeenCalledTimes(1);
  });

  it("does not write to localStorage again when repeated hellos carry the same module flags", async () => {
    const first = await reload();
    const beforeKeys = new Set(storageKeys());
    first.store.connect();
    await deliverHello([
      first.host.MODULES_FEATURE,
      first.host.moduleFeature("recording"),
      first.host.moduleFeature("health"),
      "terminal_stream",
    ]);

    const writtenKeys = storageKeys().filter((k) => !beforeKeys.has(k));
    expect(writtenKeys.length).toBeGreaterThan(0);

    const setItem = vi.spyOn(Storage.prototype, "setItem");
    const removeItem = vi.spyOn(Storage.prototype, "removeItem");

    // Same module flags in different order with different unrelated features: no storage write.
    await deliverHello([
      "app_ping",
      first.host.moduleFeature("health"),
      "sessions_delta",
      first.host.moduleFeature("recording"),
      first.host.MODULES_FEATURE,
    ]);
    expect(setItem).not.toHaveBeenCalled();
    expect(removeItem).not.toHaveBeenCalled();

    // After a reload, receiving a hello with the same remembered module flags still does not rewrite storage.
    const reloaded = await reload();
    setItem.mockClear();
    removeItem.mockClear();
    reloaded.store.connect();
    await deliverHello([
      reloaded.host.moduleFeature("health"),
      reloaded.host.MODULES_FEATURE,
      reloaded.host.moduleFeature("recording"),
      "other_feature",
    ]);
    expect(setItem).not.toHaveBeenCalled();
    expect(removeItem).not.toHaveBeenCalled();

    // A hello that actually changes the module flags does persist the update.
    await deliverHello([reloaded.host.MODULES_FEATURE, reloaded.host.moduleFeature("health")]);
    expect(setItem).toHaveBeenCalled();
  });

  it("isolates remembered module flags across different hub origins", async () => {
    // Hub A disables health and voice.
    setHubLocation(HUB_A);
    const a1 = await reload();
    a1.store.connect();
    await deliverHello([a1.host.MODULES_FEATURE]);

    // Switching to Hub B (before any hello from Hub B): modules stay ON.
    setHubLocation(HUB_B);
    const b1 = await reload();
    expect(b1.host.hubServes(b1.host.moduleFeature("health"))).toBe(true);
    expect(b1.host.hubServes(b1.host.moduleFeature("voice"))).toBe(true);
    expect(b1.host.moduleState(b1.descriptors.health)).toBe("on");
    expect(b1.host.moduleState(b1.descriptors.voice)).toBe("on");
    expect(b1.router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
    expect(b1.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);

    // Hub B publishes health ON, voice OFF.
    b1.store.connect();
    await deliverHello([b1.host.MODULES_FEATURE, b1.host.moduleFeature("health")]);

    // Switching back to Hub A before hello still remembers Hub A's disabled modules.
    setHubLocation(HUB_A);
    const a2 = await reload();
    expect(a2.host.hubServes(a2.host.moduleFeature("health"))).toBe(false);
    expect(a2.host.hubServes(a2.host.moduleFeature("voice"))).toBe(false);
    expect(a2.host.moduleState(a2.descriptors.health)).toBe("off");
    expect(a2.host.moduleState(a2.descriptors.voice)).toBe("off");
    expect(a2.router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(a2.host.enabledNavItems().map((n) => n.key)).toEqual(["notes"]);

    // Switching back to Hub B before hello still remembers Hub B's own flags.
    setHubLocation(HUB_B);
    const b2 = await reload();
    expect(b2.host.hubServes(b2.host.moduleFeature("health"))).toBe(true);
    expect(b2.host.hubServes(b2.host.moduleFeature("voice"))).toBe(false);
    expect(b2.host.moduleState(b2.descriptors.health)).toBe("on");
    expect(b2.host.moduleState(b2.descriptors.voice)).toBe("off");
    expect(b2.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "notes"]);
  });

  it("isolates remembered module flags across different reverse-proxy subpaths on the same origin", async () => {
    // Subpath /hub-a/ disables health and voice.
    setHubLocation(PROXY_ORIGIN, "/hub-a/");
    const a1 = await reload();
    a1.store.connect();
    await deliverHello([a1.host.MODULES_FEATURE]);

    // Subpath /hub-b/ on the same origin starts with modules ON before hello.
    setHubLocation(PROXY_ORIGIN, "/hub-b/");
    const b1 = await reload();
    expect(b1.host.hubServes(b1.host.moduleFeature("health"))).toBe(true);
    expect(b1.host.hubServes(b1.host.moduleFeature("voice"))).toBe(true);
    expect(b1.router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
    expect(b1.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);

    // Subpath /hub-b/ publishes voice ON, health OFF.
    b1.store.connect();
    await deliverHello([b1.host.MODULES_FEATURE, b1.host.moduleFeature("voice")]);

    // Returning to /hub-a/ still remembers /hub-a/ had both disabled.
    setHubLocation(PROXY_ORIGIN, "/hub-a/");
    const a2 = await reload();
    expect(a2.host.hubServes(a2.host.moduleFeature("health"))).toBe(false);
    expect(a2.host.hubServes(a2.host.moduleFeature("voice"))).toBe(false);
    expect(a2.router.parse("/health")).toEqual({ name: "track", slug: "health", fresh: true });
    expect(a2.host.enabledNavItems().map((n) => n.key)).toEqual(["notes"]);

    // Returning to /hub-b/ still remembers /hub-b/ had voice ON and health OFF.
    setHubLocation(PROXY_ORIGIN, "/hub-b/");
    const b2 = await reload();
    expect(b2.host.hubServes(b2.host.moduleFeature("voice"))).toBe(true);
    expect(b2.host.hubServes(b2.host.moduleFeature("health"))).toBe(false);
    expect(b2.host.enabledNavItems().map((n) => n.key)).toEqual(["voice", "notes"]);
  });

  it("falls back safely to modules on when any remembered module-flag storage entry is corrupt or invalid", async () => {
    const first = await reload();
    const beforeKeys = new Set(storageKeys());
    first.store.connect();
    await deliverHello([first.host.MODULES_FEATURE]);

    const writtenKeys = storageKeys().filter((k) => !beforeKeys.has(k));
    expect(writtenKeys.length).toBeGreaterThan(0);

    const corruptPayloads = [
      "{not json",
      "",
      "null",
      "42",
      '"broken"',
      "true",
      JSON.stringify({ [HUB_A]: 123 }),
    ];

    for (const payload of corruptPayloads) {
      for (const key of writtenKeys) localStorage.setItem(key, payload);
      const next = await reload();
      expect(next.host.hubServes(next.host.moduleFeature("health")), payload).toBe(true);
      expect(next.host.hubServes(next.host.moduleFeature("voice")), payload).toBe(true);
      expect(next.host.moduleState(next.descriptors.health), payload).toBe("on");
      expect(next.host.moduleState(next.descriptors.voice), payload).toBe("on");
      expect(next.router.parse("/health"), payload).toEqual({ name: "module", module: "health", base: "health", path: [] });
      expect(next.host.enabledNavItems().map((n) => n.key), payload).toEqual(["health", "voice", "recording", "notes"]);
    }
  });

  it("does not crash startup or hello handling when localStorage throws SecurityError or QuotaExceededError", async () => {
    // 1. Completely blocked storage on startup (all getItem/setItem/removeItem throw SecurityError).
    const realGet = Storage.prototype.getItem;
    const getSpy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("Storage access denied", "SecurityError");
    });
    const setSpy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Quota exceeded", "QuotaExceededError");
    });
    const removeSpy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new DOMException("Storage access denied", "SecurityError");
    });

    const blockedStartup = await reload();
    expect(blockedStartup.host.hubServes(blockedStartup.host.moduleFeature("health"))).toBe(true);
    expect(blockedStartup.host.moduleState(blockedStartup.descriptors.health)).toBe("on");
    expect(blockedStartup.router.parse("/health")).toEqual({ name: "module", module: "health", base: "health", path: [] });
    expect(blockedStartup.host.enabledNavItems().map((n) => n.key)).toEqual(["health", "voice", "recording", "notes"]);

    // 2. Module-memory reads throw SecurityError and writes/deletes throw QuotaExceededError/SecurityError during live hellos.
    getSpy.mockImplementation(function (this: Storage, key: string) {
      if (CREDENTIAL_KEYS.has(key)) return realGet.call(this, key);
      throw new DOMException("Storage access denied", "SecurityError");
    });

    const live = await reload();
    expect(() => live.store.connect()).not.toThrow();
    await expect(deliverHello([live.host.MODULES_FEATURE])).resolves.toBeUndefined();
    expect(live.host.moduleState(live.descriptors.health)).toBe("off");
    expect(live.host.moduleState(live.descriptors.voice)).toBe("off");

    await expect(deliverHello(["terminal_stream"])).resolves.toBeUndefined();
    expect(live.host.moduleState(live.descriptors.health)).toBe("on");
    expect(live.host.moduleState(live.descriptors.voice)).toBe("on");

    expect(setSpy).toBeDefined();
    expect(removeSpy).toBeDefined();
  });

  it("clears remembered module flags on sign-out reset() even before a hello arrives on reload", async () => {
    const first = await reload();
    const beforeKeys = new Set(storageKeys());
    first.store.connect();
    await deliverHello([first.host.MODULES_FEATURE]);

    const writtenKeys = storageKeys().filter((k) => !beforeKeys.has(k));
    expect(writtenKeys.length).toBeGreaterThan(0);

    // Reload: modules start OFF from remembered flags, then sign-out (reset()) runs BEFORE any hello arrives.
    const reloaded = await reload();
    expect(reloaded.host.moduleState(reloaded.descriptors.health)).toBe("off");
    expect(reloaded.host.moduleState(reloaded.descriptors.voice)).toBe("off");

    reloaded.store.reset();
    expect(reloaded.host.moduleState(reloaded.descriptors.health)).toBe("on");
    expect(reloaded.host.moduleState(reloaded.descriptors.voice)).toBe("on");
    for (const key of writtenKeys) expect(localStorage.getItem(key)).toBeNull();

    // Next reload after sign-out starts with modules ON before hello.
    const afterSignOut = await reload();
    expect(afterSignOut.host.moduleState(afterSignOut.descriptors.health)).toBe("on");
    expect(afterSignOut.host.moduleState(afterSignOut.descriptors.voice)).toBe("on");

    // Also when reset() races a freshly received hello before store.ts's notifySoon() flush runs.
    afterSignOut.store.connect();
    Socket.last!.push({ type: "hello", features: [afterSignOut.host.MODULES_FEATURE] });
    afterSignOut.store.reset();
    await flush();
    expect(afterSignOut.host.moduleState(afterSignOut.descriptors.health)).toBe("on");
    for (const key of writtenKeys) expect(localStorage.getItem(key)).toBeNull();
  });
});
