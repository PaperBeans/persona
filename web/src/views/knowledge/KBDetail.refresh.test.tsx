// @vitest-environment jsdom
// An open entry read again after its tags were saved (#5048): a failed read keeps the last good detail but says so,
// with Retry; Retry only reads again. Errors go through the real REST decoder.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBDetailResponse } from "../../lib/kb";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { EntryDetail } from "./KBDetail";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const entry = (tags: string[]): KBDetailResponse => ({ detail: { kind: "people", id: "example", path: "", meta: { name: "Example entry", tags }, summary: "# Example entry" } });

// The page's owner bumps reloadKey after a save, as Knowledge does.
function Page() {
  const [reloadKey, setReloadKey] = useState(0);
  return <EntryDetail kind="people" id="example" reloadKey={reloadKey} onToggleSidebar={() => {}} onBack={() => {}} onOpen={() => {}} onSaved={() => setReloadKey((k) => k + 1)} />;
}
const render = () => act(() => root.render(<Page />));
const json = (r: Request, value: unknown, status = 200) => act(async () => r.resolve(new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } })));
const refuse = (r: Request) => json(r, { code: "kb_unavailable", error: "Request failed" }, 503);
const button = (label: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const reads = () => requests.filter((r) => !r.init.method && r.url === "/api/v1/kb/people/example");
const writes = () => requests.filter((r) => r.init.method === "PUT");
const unavailable = () => t("lib-api.knowledge_unavailable");
const retry = () => button(t("knowledge-kblist.retry"));

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetManagerChoice();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  resetManagerChoice();
  vi.unstubAllGlobals();
});

it("shows a failed first read with Retry, as before", async () => {
  render();
  await refuse(reads()[0]);
  expect(host.textContent).toContain(unavailable());
  expect(retry()).toBeDefined();
  act(() => retry()!.click());
  expect(reads()).toHaveLength(2);
  await json(reads()[1], entry(["old"]));
  expect(host.textContent).not.toContain(unavailable());
  expect(host.textContent).toContain("#old");
});

it("keeps the detail but shows a failed read after saving tags, and Retry reads it again without saving again", async () => {
  render();
  await json(reads()[0], entry(["old"]));
  expect(host.textContent).not.toContain(unavailable());
  act(() => button("Tag")!.click());
  await act(async () => button("Save")!.click());
  expect(writes()).toHaveLength(1);
  await json(writes()[0], { id: "example" });
  expect(reads()).toHaveLength(2);
  await refuse(reads()[1]);
  // the last good detail stays, and the page says its read failed
  expect(host.textContent).toContain("Example entry");
  expect(host.textContent).toContain("#old");
  expect(host.textContent).toContain(unavailable());
  expect(retry()).toBeDefined();

  act(() => retry()!.click());
  expect(reads()).toHaveLength(3);
  expect(writes()).toHaveLength(1);
  await json(reads()[2], entry(["new"]));
  expect(host.textContent).not.toContain(unavailable());
  expect(host.textContent).toContain("#new");
  expect(host.textContent).not.toContain("#old");
});

it("shows nothing extra after a successful read following the save", async () => {
  render();
  await json(reads()[0], entry(["old"]));
  act(() => button("Tag")!.click());
  await act(async () => button("Save")!.click());
  await json(writes()[0], { id: "example" });
  await json(reads()[1], entry(["new"]));
  expect(host.textContent).toContain("#new");
  expect(host.textContent).not.toContain(unavailable());
  expect(retry()).toBeUndefined();
});
