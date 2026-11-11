// @vitest-environment jsdom
// The mounted hook, file operations and REST client share a synthetic transport; no file is touched.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleUnauthorized, setToken } from "../../../lib/api";
import { pair } from "../../../lib/auth";
import { chooseManager, resetManagerChoice, setRosterDefault } from "../../../lib/manager-choice";
import { getState } from "../../../lib/store";
import { t } from "../../../i18n";
import { useFileOps, type FileOps, type OpTarget } from "./FileOps";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;

type Call = { path: string; token: string | null; body: Record<string, unknown>; answer: (body: unknown, status?: number) => void };
let host: HTMLDivElement, root: Root, ops: FileOps, calls: Call[];
let props: { mid: string; cwd: string; path: string };
const changed = vi.fn();
const unauthorized = vi.fn();
const target: OpTarget = { path: "/work/from/sample.txt", name: "sample.txt", dir: false };
const originalFeatures = getState().features;
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const byRoute = (route: string) => calls.filter((call) => call.path.endsWith(`/files/${route}`));
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === label || b.getAttribute("aria-label") === label);
async function click(label: string) {
  const found = button(label);
  expect(found, label).toBeTruthy();
  await act(async () => found!.click());
  await flush();
}
async function answer(call: Call, body: unknown, status = 200) { await act(async () => call.answer(body, status)); await flush(); }
async function switchTo(token: string) { await act(async () => setToken(token)); await flush(); }
async function render() { await act(async () => root.render(<StrictMode><Harness {...props} /></StrictMode>)); await flush(); }
async function startMove() {
  await act(async () => ops.move([target])); await flush();
  await click(t("files-ops.up"));
  await click(t("files-ops.move_here"));
}
function Harness(p: typeof props) {
  ops = useFileOps({ ...p, onChanged: changed });
  return <div data-same-panel>{ops.ui}</div>;
}

beforeEach(async () => {
  calls = [];
  changed.mockReset(); unauthorized.mockReset();
  resetManagerChoice(); setRosterDefault("manager-a");
  setToken("jd_synthetic-a");
  handleUnauthorized(unauthorized);
  Object.assign(getState(), { features: ["file_edit"] });
  props = { mid: "machine-a", cwd: "/work", path: "/work/from" };
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://local.invalid");
    if (url.pathname.endsWith("/fs")) return Promise.resolve(Response.json({ entries: [] }));
    if (url.pathname === "/api/v1/machines") return Promise.resolve(Response.json({ machines: [] }));
    return new Promise<Response>((resolve) => calls.push({
      path: url.pathname,
      token: new Headers(init?.headers).get("Authorization"),
      body: init?.body ? JSON.parse(String(init.body)) : {},
      answer: (value, status = 200) => resolve(Response.json(value, { status })),
    }));
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await render();
});
afterEach(() => {
  act(() => root.unmount()); host.remove(); document.body.innerHTML = "";
  Object.assign(getState(), { features: originalFeatures });
  handleUnauthorized(() => {}); setToken(""); resetManagerChoice(); vi.unstubAllGlobals();
});

describe("file mutation ownership", () => {
  it.each(["B", "B then A", "same-token pair"])("retires A's confirmation after %s", async (transition) => {
    await act(async () => ops.trash([target])); await flush();
    const samePanel = host.querySelector("[data-same-panel]");
    const oldConfirm = button(t("files-ops.trash"))!;
    expect(oldConfirm).toBeTruthy();
    if (transition === "same-token pair") await act(async () => pair("jd_synthetic-a"));
    else {
      await switchTo("jd_synthetic-b");
      if (transition === "B then A") await switchTo("jd_synthetic-a");
    }
    await act(async () => oldConfirm.click()); await flush();
    expect(host.querySelector("[data-same-panel]")).toBe(samePanel);
    expect(button(t("files-ops.trash"))).toBeUndefined();
    expect(byRoute("trash")).toHaveLength(0);
  });

  it("checks ownership synchronously between a credential change and React's next render", async () => {
    await act(async () => ops.trash([target])); await flush();
    const oldConfirm = button(t("files-ops.trash"))!;
    const oldTrash = ops.trash;
    await act(async () => { setToken("jd_synthetic-b"); oldConfirm.click(); oldTrash([target]); });
    await flush();
    expect(byRoute("trash")).toHaveLength(0);
    expect(button(t("files-ops.trash"))).toBeUndefined();
  });

  it("retires A's undo and its saved callback on B login", async () => {
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    expect(byRoute("trash")[0].token).toBe("Bearer jd_synthetic-a");
    await answer(byRoute("trash")[0], { trashed: [{ path: target.path, trash_id: "synthetic-trash-a" }] });
    const undo = button(t("files-ops.undo"))!; expect(undo).toBeTruthy();
    await switchTo("jd_synthetic-b");
    await act(async () => undo.click()); await flush();
    expect(button(t("files-ops.undo"))).toBeUndefined();
    expect(byRoute("restore")).toHaveLength(0);
  });

  it.each(["success", "error"])("ignores A's late trash %s while B has a pending confirmation", async (outcome) => {
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    const old = byRoute("trash")[0];
    await switchTo("jd_synthetic-b"); changed.mockClear();
    await act(async () => ops.trash([{ ...target, name: "new.txt", path: "/work/from/new.txt" }])); await flush();
    await answer(old, outcome === "success" ? { trashed: [{ path: target.path, trash_id: "synthetic-trash-a" }] } : { error: "old failure" }, outcome === "success" ? 200 : 500);
    expect(changed).not.toHaveBeenCalled();
    expect(button(t("files-ops.undo"))).toBeUndefined();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("new.txt");
    await click(t("files-ops.trash"));
    expect(byRoute("trash")[1].token).toBe("Bearer jd_synthetic-b");
  });

  it.each(["manager", "manager round trip", "node", "cwd", "path"])("retires dialogs and old admissions on a %s change", async (change) => {
    await act(async () => ops.trash([target])); await flush();
    const oldConfirm = button(t("files-ops.trash"))!;
    const oldMove = ops.move;
    if (change.startsWith("manager")) {
      await act(async () => { chooseManager("manager-b"); if (change === "manager round trip") chooseManager("manager-a"); });
      await flush();
    } else {
      if (change === "node") props = { ...props, mid: "machine-b" };
      if (change === "cwd") props = { ...props, cwd: "/other" };
      if (change === "path") props = { ...props, path: "/work/other" };
      await render();
    }
    await act(async () => { oldConfirm.click(); oldMove([target]); }); await flush();
    expect(byRoute("trash")).toHaveLength(0);
    expect(button(t("files-ops.trash"))).toBeUndefined();
    expect(button(t("files-ops.move_here"))).toBeUndefined();
  });

  it("retires a conflict replace and does not borrow the next credential", async () => {
    await startMove();
    const first = byRoute("move")[0]; expect(first.token).toBe("Bearer jd_synthetic-a");
    await answer(first, { error: "exists", code: "exists", name: target.name }, 409);
    const replace = button(t("files-ops.replace"))!; expect(replace).toBeTruthy();
    await switchTo("jd_synthetic-b");
    await act(async () => replace.click()); await flush();
    expect(byRoute("move")).toHaveLength(1);
    expect(button(t("files-ops.replace"))).toBeUndefined();
  });

  it("keeps current-owner conflict, failed replace, retry and duplicate protection", async () => {
    await startMove();
    await answer(byRoute("move")[0], { error: "exists", code: "exists", name: target.name }, 409);
    const replace = button(t("files-ops.replace"))!;
    await act(async () => { replace.click(); replace.click(); }); await flush();
    expect(byRoute("move")).toHaveLength(2);
    expect(byRoute("move")[1].body.conflict).toBe("replace");
    await answer(byRoute("move")[1], { error: "synthetic failure" }, 500);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("synthetic failure");
    await click(t("files-ops.move_here"));
    expect(byRoute("move")).toHaveLength(3);
    await answer(byRoute("move")[2], { moved: [{ from: target.path, to: "/work/sample.txt" }] });
    expect(changed).toHaveBeenCalledExactlyOnceWith(["/work/from", "/work"]);
  });

  it("binds rename at menu admission and fences its late success and failure", async () => {
    const old = ops.rowItems(target, () => {}).find((item) => item.key === "rename")!.rename!.commit;
    const pending = Promise.resolve(old("renamed.txt")).catch((error: unknown) => error);
    expect(byRoute("rename")[0].token).toBe("Bearer jd_synthetic-a");
    await switchTo("jd_synthetic-b"); changed.mockClear();
    await answer(byRoute("rename")[0], { path: "/work/from/renamed.txt" });
    expect(await pending).toMatchObject({ name: "AbortError" });
    await expect(old("another.txt")).rejects.toMatchObject({ name: "AbortError" });
    expect(byRoute("rename")).toHaveLength(1);
    expect(changed).not.toHaveBeenCalled();
    const fresh = ops.rowItems(target, () => {}).find((item) => item.key === "rename")!.rename!.commit("fresh.txt");
    expect(byRoute("rename")[1].token).toBe("Bearer jd_synthetic-b");
    await answer(byRoute("rename")[1], { path: "/work/from/fresh.txt" });
    await fresh;
    expect(changed).toHaveBeenCalledExactlyOnceWith(["/work/from"]);
  });

  it("keeps trash retry and undo, admitting each request once", async () => {
    await act(async () => ops.trash([target])); await flush();
    const confirm = button(t("files-ops.trash"))!;
    await act(async () => { confirm.click(); confirm.click(); }); await flush();
    expect(byRoute("trash")).toHaveLength(1);
    await answer(byRoute("trash")[0], { error: "synthetic failure" }, 500);
    await click(t("files-ops.trash"));
    await answer(byRoute("trash")[1], { trashed: [{ path: target.path, trash_id: "synthetic-trash" }] });
    const undo = button(t("files-ops.undo"))!;
    await act(async () => { undo.click(); undo.click(); }); await flush();
    expect(byRoute("restore")).toHaveLength(1);
    expect(byRoute("restore")[0].token).toBe("Bearer jd_synthetic-a");
    await answer(byRoute("restore")[0], { restored: [{ path: target.path }] });
    expect(changed).toHaveBeenCalledTimes(2);
  });

  it.each(["restore success", "restore failure"])("ignores a late %s after replacement", async (result) => {
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    await answer(byRoute("trash")[0], { trashed: [{ path: target.path, trash_id: "synthetic-trash" }] });
    await click(t("files-ops.undo"));
    await switchTo("jd_synthetic-b"); changed.mockClear();
    await answer(byRoute("restore")[0], result === "restore success" ? { restored: [{ path: target.path }] } : { error: "old failure" }, result === "restore success" ? 200 : 500);
    expect(changed).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("old failure");
    expect(button(t("files-ops.undo"))).toBeUndefined();
  });

  it("keeps current-owner 401 recovery but rejects a retired response", async () => {
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    await switchTo("jd_synthetic-b");
    await answer(byRoute("trash")[0], { error: "invalid token" }, 401);
    expect(unauthorized).not.toHaveBeenCalled();
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    await answer(byRoute("trash")[1], { error: "invalid token" }, 401);
    expect(unauthorized).toHaveBeenCalledOnce();
  });

  it.each(["manager", "node", "path"])("does not sign out for a late 401 after the %s context changes", async (change) => {
    await act(async () => ops.trash([target])); await click(t("files-ops.trash"));
    if (change === "manager") await act(async () => chooseManager("manager-b"));
    else { props = change === "node" ? { ...props, mid: "machine-b" } : { ...props, path: "/work/other" }; await render(); }
    await answer(byRoute("trash")[0], { error: "invalid token" }, 401);
    expect(unauthorized).not.toHaveBeenCalled();
    expect(changed).not.toHaveBeenCalled();
  });
});
