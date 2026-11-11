// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.mock("../../lib/use-fs-live", () => ({ useFsLive: () => {} }));
import { setToken } from "../../lib/api";
import { getState } from "../../lib/store";
import { t } from "../../i18n";
import { FilesPanel } from "./FilesPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia = ((media: string) => ({ matches: media.includes("max-width"), media, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
HTMLElement.prototype.scrollTo ??= () => {};
let host: HTMLDivElement, root: Root, sent: { path: string; body: unknown; auth: string | null }[], exists: boolean;
const originalFeatures = getState().features;
const flush = async () => { for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); }); };
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === label || b.getAttribute("aria-label") === label);
async function click(label: string) { const found = button(label); expect(found, label).toBeTruthy(); await act(async () => found!.click()); await flush(); }
async function trash() {
  await click(t("session-filespanel.more")); await click(t("files-ops.select"));
  const input = document.querySelector<HTMLInputElement>(`input[aria-label="${t("files-ops.select_row", { name: "sample.txt" })}"]`)!;
  expect(input).toBeTruthy(); await act(async () => input.click()); await flush();
  const toolbar = document.querySelector('[role="toolbar"]')!;
  const trashButton = toolbar.querySelector<HTMLButtonElement>(`button[aria-label="${t("files-ops.trash")}"]`)!;
  expect(trashButton).toBeTruthy(); await act(async () => trashButton.click()); await flush();
  const confirmation = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === t("files-ops.trash"))!;
  expect(confirmation).toBeTruthy(); await act(async () => confirmation.click()); await flush();
  expect(sent.filter((call) => call.path.endsWith("/files/trash"))).toHaveLength(1);
  expect(button(t("files-ops.undo"))).toBeTruthy();
}

beforeEach(async () => {
  localStorage.clear(); setToken("synthetic-A"); Object.assign(getState(), { features: ["file_edit"] }); sent = []; exists = true;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const u = new URL(String(input), "http://local.invalid");
    if (u.pathname.endsWith("/git/status")) return Response.json({ is_repo: false, files: [], commits: [] });
    if (u.pathname.endsWith("/fs")) {
      const path = u.searchParams.get("path");
      const entries = path === "/work" ? [...(exists ? [{ name: "sample.txt", dir: false, size: 1 }] : []), { name: "next", dir: true, size: 0 }] : [];
      return Response.json({ path, entries, truncated: false });
    }
    sent.push({ path: u.pathname, body: init?.body ? JSON.parse(String(init.body)) : undefined, auth: new Headers(init?.headers).get("Authorization") });
    if (u.pathname.endsWith("/files/trash")) { exists = false; return Response.json({ trashed: [{ path: "/work/sample.txt", trash_id: "synthetic-trash" }] }); }
    if (u.pathname.endsWith("/files/restore")) { exists = true; return Response.json({ restored: [{ path: "/work/sample.txt" }] }); }
    return Response.json({});
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => root.render(<FilesPanel mid="synthetic-machine" sid="synthetic-session" cwd="/work" onClose={() => {}} />)); await flush();
});
afterEach(() => { act(() => root.unmount()); host.remove(); document.body.innerHTML = ""; Object.assign(getState(), { features: originalFeatures }); setToken(""); vi.unstubAllGlobals(); });

describe("FilesPanel narrow-screen trash Undo", () => {
  it("keeps same-owner Undo available when entering another folder", async () => {
    await trash();
    const next = host.querySelector<HTMLButtonElement>('button[title="/work/next"]')!; expect(next).toBeTruthy();
    await act(async () => next.click()); await flush();
    expect(button(t("files-ops.undo"))).toBeTruthy();
    await click(t("files-ops.undo"));
    expect(sent.find((call) => call.path.endsWith("/files/restore"))).toEqual(expect.objectContaining({ auth: "Bearer synthetic-A", body: { trash_ids: ["synthetic-trash"] } }));
  });
  it("keeps same-owner Undo working in the original folder", async () => {
    await trash(); await click(t("files-ops.undo"));
    expect(sent.find((call) => call.path.endsWith("/files/restore"))).toEqual(expect.objectContaining({ auth: "Bearer synthetic-A", body: { trash_ids: ["synthetic-trash"] } }));
  });
});
