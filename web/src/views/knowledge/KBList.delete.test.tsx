// @vitest-environment jsdom
// Deleting a project from the knowledge list (#4412): its row's context menu offers Delete, a dialog asks once more,
// the row goes, and the toast's Undo restores it; a restore over a newer project with the same slug says so.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { Toasts } from "../../ui/atoms";
import { ListPane } from "./KBList";
import { listKey, lists, searches } from "./cache";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let projects: { kind: string; id: string; name: string; relevance: number; extra: string }[];
let restoreStatus: number;
const calls: string[] = [];

const project = (id: string, name: string) => ({ kind: "projects", id, name, relevance: 1, extra: "short · active" });
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
  lists.clear();
  searches.clear();
  calls.length = 0;
  projects = [project("alpha", "Alpha"), project("beta", "Beta")];
  restoreStatus = 200;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const path = new URL(url, "http://hub.example").pathname.replace(/^\/api\/v1/, "");
    const method = init?.method ?? "GET";
    calls.push(`${method} ${path}`);
    if (method === "GET" && path === "/kb/projects") return json({ items: projects, tags: [] });
    if (method === "DELETE" && path === "/kb/projects/alpha") {
      projects = projects.filter((p) => p.id !== "alpha");
      return json({ id: "alpha-1", kind: "projects", slug: "alpha", name: "Alpha", deleted_at: "2026-11-11T08:00:00Z" });
    }
    if (method === "POST" && path === "/kb-trash/alpha-1/restore") {
      if (restoreStatus !== 200) return json({ error: "conflict" }, restoreStatus);
      projects = [project("alpha", "Alpha"), ...projects];
      return json({ id: "alpha-1", slug: "alpha" });
    }
    return json({ error: "unexpected" }, 500);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  lists.clear();
  searches.clear();
});

const row = (name: string) => [...host.querySelectorAll<HTMLAnchorElement>("a")].find((a) => a.textContent?.includes(name));
const button = (label: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const menuItem = (label: string) => [...document.body.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((b) => b.textContent?.includes(label));
const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });

async function deleteAlpha() {
  await act(async () => root.render(
    <>
      <ListPane kind="projects" detailOpen={false} reloadKey={0} onToggleSidebar={() => {}} onOpen={() => {}} />
      <Toasts />
    </>,
  ));
  await flush();
  expect(row("Alpha")).toBeTruthy();
  await act(async () => { row("Alpha")!.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 40, clientY: 40 })); });
  await act(async () => menuItem(t("knowledge-kblist.delete"))!.click());
  expect(document.body.textContent).toContain(t("knowledge-kblist.delete_title"));
  expect(calls.filter((c) => c.startsWith("DELETE"))).toEqual([]);
  const confirm = [...document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === t("knowledge-kblist.delete"))!;
  await act(async () => confirm.click());
  await flush();
}

describe("deleting a project from the knowledge list", () => {
  it("asks once, removes the row, and Undo restores it", async () => {
    await deleteAlpha();
    expect(calls).toContain("DELETE /kb/projects/alpha");
    expect(row("Alpha")).toBeUndefined();
    expect(row("Beta")).toBeTruthy();
    // The cached list a later visit shows first has lost it too.
    expect(lists.get(listKey("projects"))?.items?.map((x) => x.id)).toEqual(["beta"]);
    expect(document.body.textContent).toContain(t("knowledge-kblist.deleted", { name: "Alpha" }));
    await act(async () => button(t("knowledge-kblist.undo"))!.click());
    await flush();
    expect(calls).toContain("POST /kb-trash/alpha-1/restore");
    expect(row("Alpha")).toBeTruthy();
    expect(document.body.textContent).toContain(t("knowledge-kblist.restored", { name: "Alpha" }));
  });

  it("says a newer project took the name when the restore is refused", async () => {
    restoreStatus = 409;
    await deleteAlpha();
    await act(async () => button(t("knowledge-kblist.undo"))!.click());
    await flush();
    expect(document.body.textContent).toContain(t("knowledge-kblist.restore_conflict", { name: "Alpha" }));
    expect(row("Alpha")).toBeUndefined();
  });

  it("offers the menu only on projects", async () => {
    await act(async () => root.render(<ListPane kind="projects" detailOpen={false} reloadKey={0} onToggleSidebar={() => {}} onOpen={() => {}} />));
    await flush();
    expect(row("Alpha")!.getAttribute("aria-haspopup")).toBe("menu");
    await act(async () => { row("Alpha")!.dispatchEvent(new KeyboardEvent("keydown", { key: "F10", shiftKey: true, bubbles: true })); });
    expect(menuItem(t("knowledge-kblist.delete"))).toBeTruthy();
  });
});
