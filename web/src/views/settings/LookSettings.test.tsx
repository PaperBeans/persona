// @vitest-environment jsdom
// Theme color "Match avatar" (ui/look.ts): the persona store keeps the chosen persona's accent (views/assistant/Persona.tsx setPS),
// and the picker offers "Match avatar" only while that accent is there or it is already the choice.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const hub = vi.hoisted(() => ({ result: null as unknown, fail: false }));
vi.mock("../../lib/api", async (orig) => {
  const real = await orig<typeof import("../../lib/api")>();
  return {
    ...real,
    getToken: () => "",
    // An older hub (no /looks): the packages from /personas, the choice this browser's (Persona.test.tsx has /looks).
    api: vi.fn((path: string) =>
      hub.fail ? Promise.reject(new Error("offline")) : path === "/looks" ? Promise.reject(new real.ApiError(404, "not found", {})) : Promise.resolve(hub.result)),
  };
});
import { BUILTIN_PERSONA, type PersonaInfo } from "../../lib/persona";
import { savedPersonaAccent, setAccentChoice, setPersonaAccent } from "../../ui/look";
import { choosePersona, refreshPersonas } from "../assistant/Persona";
import { ThemeColorPicker } from "./LookSettings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// preload() waits on <img> loads, which jsdom never fires
vi.stubGlobal("Image", class { onload: (() => void) | null = null; onerror: (() => void) | null = null; set src(_: string) { queueMicrotask(() => this.onload?.()); } });

const pack = (rev: string, accent?: string): PersonaInfo => ({
  id: "frog",
  rev,
  manifest: {
    format: 1, id: "frog", name: "奶蛙",
    avatar: { sizes: { "128": "avatar.png" } },
    dot: { states: { idle: { file: "idle.png" } } },
    ...(accent ? { colors: { light: { accent } } } : {}),
  },
});
const list = (...p: PersonaInfo[]) => ({ personas: p });

describe("the persona store keeps 跟随形象's accent", () => {
  beforeEach(() => {
    hub.fail = false;
  });

  it("saves the chosen pack's accent, keeps it through a failed load, and clears it for a built-in or a pack gone", async () => {
    hub.result = list(pack("1", "#94600A"));
    choosePersona("frog");
    await refreshPersonas();
    expect(savedPersonaAccent()).toEqual({ light: "#94600a", dark: "#94600a" });

    // new colors in the list: the accent follows
    hub.result = list(pack("2", "#27747F"));
    await refreshPersonas();
    expect(savedPersonaAccent()).toEqual({ light: "#27747f", dark: "#27747f" });

    // the hub out of reach: the last accent stays
    hub.fail = true;
    await refreshPersonas();
    expect(savedPersonaAccent()).toEqual({ light: "#27747f", dark: "#27747f" });
    hub.fail = false;

    // a built-in has no accent
    choosePersona(BUILTIN_PERSONA);
    expect(savedPersonaAccent()).toBeNull();
    choosePersona("frog");
    expect(savedPersonaAccent()).toEqual({ light: "#27747f", dark: "#27747f" });

    // the pack is gone from the hub's list
    hub.result = list();
    await refreshPersonas();
    expect(savedPersonaAccent()).toBeNull();
  });
});

describe("主题色's options", () => {
  let host: HTMLDivElement;
  let root: Root;
  const draw = async () => {
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => root.render(<ThemeColorPicker />));
    return [...host.querySelectorAll('[role="radio"]')];
  };
  beforeEach(() => {
    localStorage.clear();
    setPersonaAccent(null);
    setAccentChoice("default");
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it("has no 跟随形象 while the persona brings no colors", async () => {
    const radios = await draw();
    expect(radios).toHaveLength(6);
    expect(radios.some((r) => r.getAttribute("aria-label")?.startsWith("跟随形象"))).toBe(false);
  });

  it("offers 跟随形象 when the persona brings an accent", async () => {
    setPersonaAccent({ light: "#94600a", dark: "#b47a0c" });
    const radios = await draw();
    expect(radios).toHaveLength(7);
    expect(radios[1]!.getAttribute("aria-label")).toMatch(/^跟随形象/);
  });

  it("keeps 跟随形象 in sight once chosen, saying this persona has none", async () => {
    setAccentChoice("persona");
    const radios = await draw();
    expect(radios).toHaveLength(7);
    expect(radios[1]!.getAttribute("aria-checked")).toBe("true");
    expect(radios[1]!.getAttribute("aria-label")).toBe("跟随形象，这个形象没带");
  });

  it("offers and applies 跟随形象 for an uploaded avatar with colors", async () => {
    const uploaded: PersonaInfo = {
      id: "u-demo0002",
      rev: "r1",
      manifest: {
        format: 1,
        id: "u-demo0002",
        name: "Mikan",
        avatar: { sizes: { "128": "avatar/128.png" } },
        dot: { states: { idle: { file: "dot/idle.png" } } },
        source: { kind: "upload", color: { seed: "#3A7BD5", auto: false } },
        colors: { light: { primary: "#3A7BD5", accent: "#2255A0" }, dark: { primary: "#3A7BD5", accent: "#6BA0E8" } },
      },
    };
    hub.result = list(uploaded);
    choosePersona("u-demo0002");
    await refreshPersonas();
    expect(savedPersonaAccent()).toEqual({ light: "#2255a0", dark: "#6ba0e8" });
    const radios = await draw();
    expect(radios).toHaveLength(7);
    const matchAvatar = radios[1] as HTMLButtonElement;
    expect(matchAvatar.getAttribute("aria-label")).toBe("跟随形象，Mikan");
    await act(async () => matchAvatar.click());
    expect(document.documentElement.dataset.accent).toBe("persona");
    expect(document.getElementById("persona-accent")?.textContent).toContain("--accent: #2255a0;");
  });
});
