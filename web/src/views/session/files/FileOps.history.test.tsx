// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setToken } from "../../../lib/api";
import { useCloseOnBack } from "../../../lib/back-close";
import { getState } from "../../../lib/store";
import { t } from "../../../i18n";
import { useFileOps, type FileOps } from "./FileOps";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia = ((media: string) => ({ matches: media.includes("max-width"), media, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
let root: Root, host: HTMLDivElement, ops: FileOps;
const panelClosed = vi.fn(), changed = vi.fn();
const oldFeatures = getState().features;
const target = { path: "/work/from/example.txt", name: "example.txt", dir: false };
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === label || b.getAttribute("aria-label") === label);
async function click(label: string) { const b = button(label); expect(b, label).toBeTruthy(); await act(async () => b!.click()); await flush(); }
function Panel() {
  useCloseOnBack(true, panelClosed, true);
  ops = useFileOps({ mid: "synthetic-machine", cwd: "/work", onChanged: changed });
  return <div data-panel>{ops.ui}</div>;
}
beforeEach(async () => {
  setToken("synthetic-credential"); Object.assign(getState(), { features: ["file_edit"] });
  panelClosed.mockClear(); changed.mockClear();
  let moves = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://local.invalid").pathname;
    if (path.endsWith("/fs")) return Response.json({ entries: [] });
    if (path.endsWith("/move") && moves++ === 0) return Response.json({ error: "exists", code: "exists", name: target.name }, { status: 409 });
    return Response.json({ moved: [{ from: target.path, to: "/work/example.txt" }] });
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => root.render(<Panel />)); await flush();
});
afterEach(() => {
  act(() => root.unmount()); host.remove(); document.body.innerHTML = "";
  Object.assign(getState(), { features: oldFeatures }); setToken(""); vi.unstubAllGlobals();
});

it("keeps the narrow parent panel open after a fast conflict replacement", async () => {
  const parentEntry = history.state?.personaOverlay;
  expect(parentEntry).toBeTruthy();
  await act(async () => ops.move([target])); await flush();
  await click(t("files-ops.up")); await click(t("files-ops.move_here"));
  expect(button(t("files-ops.replace"))).toBeTruthy();
  expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
  await click(t("files-ops.replace"));
  await act(async () => { await vi.waitFor(() => expect(history.state?.personaOverlay).toBe(parentEntry)); });
  expect(panelClosed).not.toHaveBeenCalled();
  expect(changed).toHaveBeenCalledExactlyOnceWith(["/work/from", "/work"]);
  expect(host.querySelector("[data-panel]")).toBeTruthy();
});
