// @vitest-environment jsdom
// A pack's install preview names its members (#4122): the ones installed with it by their manifests, the ones already
// installed by the installed list, and one that is neither by its id.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});
vi.mock("../../lib/extensions", async (original) => ({
  ...(await original<typeof import("../../lib/extensions")>()),
  listExtensions: () => Promise.resolve([{ id: "base", name: "基础扩展" }]),
}));
import type { MarketPreview } from "../../lib/extension-market";
import { PreviewDialog } from "./ExtensionMarket";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement, root: Root;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const later = new Date(Date.now() + 600_000).toISOString();
const member = (id: string, name: string): MarketPreview => ({ preview_id: id, manifest: { id, name, version: "1.0.0" }, permissions: [], permissions_digest: "d-" + id, expires_at: later });

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  document.body.innerHTML = "";
});

describe("a pack's install preview", () => {
  it("names its members, keeping the id of one it can't name", async () => {
    const pv: MarketPreview = {
      preview_id: "starter", permissions: [], permissions_digest: "digest", expires_at: later,
      manifest: { id: "starter", name: "入门合集", version: "1.0.0", extension_pack: [{ id: "notes", market: "m1" }, { id: "base" }, { id: "other" }] },
      bundle: [member("notes", "笔记扩展")],
    };
    await act(async () => root.render(<PreviewDialog pv={pv} onClose={() => {}} onAgree={async () => {}} />));
    await flush();
    const text = document.body.textContent ?? "";
    expect(text).toContain("笔记扩展notes · m1");
    expect(text).toContain("基础扩展base");
    expect(text).toContain("other");
    expect(text).not.toContain("otherother");
  });
});
