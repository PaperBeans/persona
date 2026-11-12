// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
import { initI18n } from "../../i18n";
import { credentialOwner, handleUnauthorized, setToken } from "../../lib/api";
import { Toasts } from "../../ui/atoms";
import { Repositories } from "./Repositories";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Call = { path: string; init: RequestInit; answer: (body: unknown, status?: number) => void };
let root: Root, host: HTMLDivElement, calls: Call[];
const identity = { provider: "gitlab", instance_url: "https://gitlab.example.com", path: "team/sub/sample", canonical_url: "https://gitlab.example.com/team/sub/sample" };
const binding = { ...identity, id: "repo_sample", name: "Sample", aliases: ["demo"], purpose: "Public-safe sample", verified_at: "2026-11-12T00:00:00Z" };
const text = () => document.body.textContent ?? "";
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) =>
  (b.textContent?.trim() === label || (["Edit", "Unbind"].includes(label) && b.textContent?.startsWith(`${label} `))) && !b.closest("[data-closing]"))!;
const click = async (label: string) => act(async () => { expect(button(label), label).toBeTruthy(); button(label).click(); });
const answer = async (call: Call, body: unknown, status = 200) => act(async () => call.answer(body, status));
const input = async (label: string, value: string) => act(async () => {
  const field = [...document.querySelectorAll<HTMLInputElement>("input")].find((node) => node.labels?.[0]?.textContent === label)!;
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, value);
  field.dispatchEvent(new Event("input", { bubbles: true }));
});
const render = async (manager = "assistant") => act(async () => root.render(<><Repositories key={manager} manager={manager} owner={credentialOwner()} /><Toasts /></>));
const open = async (rows: unknown[] = []) => { await render(); await click("Manage repositories"); await answer(calls[0], { manager: "assistant", repositories: rows }); };

beforeEach(async () => {
  await initI18n("en"); setToken("synthetic-repository-session"); calls = [];
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init: RequestInit = {}) => new Promise<Response>((resolve) => calls.push({
    path: String(url).split("/api/v1")[1], init,
    answer: (body, status = 200) => resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })),
  }))));
});
afterEach(async () => {
  act(() => { document.querySelectorAll<HTMLButtonElement>('[data-toast] button[aria-label]').forEach((b) => b.click()); });
  act(() => root.unmount()); host.remove(); document.body.innerHTML = ""; handleUnauthorized(() => {}); vi.unstubAllGlobals(); setToken(""); await initI18n("zh-Hans");
});

it("loads only on demand and requires parsed identity before existence-check/save", async () => {
  await render(); expect(calls).toHaveLength(0);
  await click("Manage repositories"); expect(text()).toContain("Loading");
  await answer(calls[0], { manager: "assistant", repositories: [] });
  expect(text()).toContain("No repositories bound");
  await click("Bind repository");
  await input("Repository URL", "git@gitlab.example.com:team/sub/sample.git");
  expect(button("Check existence and save").disabled).toBe(true);
  await click("Preview repository");
  expect(calls[1].path).toBe("/assistant/repositories/preview");
  await answer(calls[1], { manager: "assistant", identity });
  expect(text()).toContain(identity.path); expect(text()).toContain("Not yet checked");
  await click("Check existence and save");
  expect(text()).toContain("Checking repository existence");
  expect(new Headers(calls[2].init.headers).get("X-Persona-Manager")).toBe("assistant");
  await answer(calls[2], { manager: "assistant", repository: binding });
  expect(text()).toContain("Repository saved"); expect(text()).toContain(binding.purpose);
});

it("invalidates a preview after a URL change and leaves an inconclusive check unsaved", async () => {
  await open(); await click("Bind repository"); await input("Repository URL", identity.canonical_url); await click("Preview repository");
  await answer(calls[1], { manager: "assistant", identity });
  await input("Repository URL", `${identity.canonical_url}-other`);
  expect(button("Check existence and save").disabled).toBe(true);
  await click("Preview repository"); await answer(calls[2], { manager: "assistant", identity });
  await click("Check existence and save");
  await answer(calls[3], { code: "repository_unavailable", error: "Existence could not be confirmed." }, 422);
  expect(text()).toContain("Existence could not be confirmed.");
  expect(document.querySelector('[role="dialog"][aria-modal="true"]')).toBeTruthy();
});

it("edits descriptive metadata and restores an unbound record with its single-use receipt", async () => {
  await open([binding]); await click("Edit"); await input("Name (optional)", "Renamed");
  await click("Save changes");
  expect(calls[1].init.method).toBe("PUT");
  expect(JSON.parse(calls[1].init.body as string).url).toBe(identity.canonical_url);
  await answer(calls[1], { manager: "assistant", repository: { ...binding, name: "Renamed" } });
  await click("Unbind"); expect(text()).toContain("does not delete or change the remote repository");
  await click("Unbind repository");
  expect(calls[2].init.method).toBe("DELETE");
  await answer(calls[2], { manager: "assistant", receipt: "synthetic-undo" });
  expect(text()).toContain("No repositories bound");
  await click("Undo");
  expect(JSON.parse(calls[3].init.body as string)).toEqual({ receipt: "synthetic-undo" });
  await answer(calls[3], { manager: "assistant", repository: binding });
  expect(text()).toContain(binding.purpose);
});

it("fences late reads and 401s when the manager changes", async () => {
  let unauthorized = 0; handleUnauthorized(() => { unauthorized++; });
  await render(); await click("Manage repositories");
  await render("work"); await click("Manage repositories");
  await answer(calls[1], { manager: "work", repositories: [] });
  await answer(calls[0], {}, 401);
  expect(unauthorized).toBe(0); expect(text()).not.toContain(binding.purpose);
  expect(new Headers(calls[1].init.headers).get("X-Persona-Manager")).toBe("work");
});

it("rejects an out-of-scope or malformed success instead of showing an empty state", async () => {
  await render(); await click("Manage repositories");
  await answer(calls[0], { manager: "work", repositories: [binding] });
  expect(text()).toContain("Could not load repositories"); expect(text()).not.toContain("No repositories bound");
});
