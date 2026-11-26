// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { ApiError } from "../../lib/api";
import type { RecoveryCheck, RecoveryOverview } from "../../lib/recovery";
import { settingsAt, settingsHref, itemIsPage } from "./nav";

const api = vi.hoisted(() => ({ overview: vi.fn(), inspect: vi.fn(), verify: vi.fn() }));
vi.mock("../../lib/recovery", async (original) => ({
  ...await original<typeof import("../../lib/recovery")>(),
  recoveryOverview: api.overview,
  inspectRecoveryBundle: api.inspect,
  verifyRecoveryBundle: api.verify,
}));
const { RecoverySettings } = await import("./RecoverySettings");

const bundle = { id: "a".repeat(64), revision: "revision-1", name: "synthetic.tar", archive_bytes: 8192, modified_at: "2026-01-02T03:04:05Z" };
const overview: RecoveryOverview = {
  capabilities: {
    inspect: { state: "supported" },
    verify: { state: "supported" },
    create: { state: "guided", reason: "writes_quiesced" },
    restore: { state: "unsupported", reason: "terminal_only" },
    download: { state: "unsupported", reason: "private_keys" },
  },
  discovery: { state: "supported" },
  history: { state: "unavailable", reason: "no_authoritative_source" },
  bundles: [bundle],
};
const inspection: RecoveryCheck = {
  kind: "offline_integrity", state: "passed", checked_at: "2026-01-03T03:04:05Z", bundle,
  report: {
    check: "offline_integrity", format: 1, created_at: "2026-01-02T03:04:05Z", archive_bytes: 8192, database_restore_verified: false,
    hub_dump: { files: 1, bytes: 100 }, manager_dump: { files: 1, bytes: 100 }, runtime: { files: 4, bytes: 200 },
    knowledge_base: { state: "included", location: "internal", files: 1, bytes: 50 },
  },
};

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  api.overview.mockReset(); api.inspect.mockReset(); api.verify.mockReset();
  api.overview.mockResolvedValue(overview);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove(); document.documentElement.classList.remove("dark");
});
async function mount(language = "en") {
  await initI18n(language);
  await act(async () => root.render(<RecoverySettings onBack={() => {}} />));
}
function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll("button")].find((item) => item.textContent === label);
  expect(found, label).toBeDefined();
  return found!;
}

it("opens a Hub secondary page and never implies create, restore, or download", async () => {
  expect(settingsAt("?tab=hub&page=recovery").sub).toBe("recovery");
  expect(settingsHref("hub", "recovery", false)).toBe("/settings?tab=hub&page=recovery");
  expect(itemIsPage("hub")).toBe(true);
  await mount();
  expect(host.textContent).toContain("Recovery");
  expect(host.textContent).toContain("Hub and built-in assistant databases");
  expect(host.textContent).toContain("Knowledge base");
  expect(host.textContent).toContain("TLS identity");
  expect(host.textContent).toContain("No authoritative restore or drill history");
  expect(host.textContent).toContain("--writes-quiesced");
  expect(host.textContent).toContain("stop the hub and built-in assistant");
  expect(api.inspect).not.toHaveBeenCalled(); expect(api.verify).not.toHaveBeenCalled();
  for (const label of ["Create backup", "Restore", "Download", "Export keys"]) {
    expect([...host.querySelectorAll("button")].some((item) => item.textContent === label)).toBe(false);
  }
});

it("inspects only on request and keeps database verification distinct", async () => {
  api.inspect.mockResolvedValue(inspection);
  await mount();
  await act(async () => button("Check integrity").click());
  expect(api.inspect).toHaveBeenCalledWith(bundle.id);
  expect(api.verify).not.toHaveBeenCalled();
  expect(host.textContent).toContain("Offline integrity passed");
  expect(host.textContent).toContain("Database restore not verified");
  expect(host.textContent).not.toContain("Database verification passed");
});

it("verifies databases only on request without calling it a restore drill", async () => {
  api.verify.mockResolvedValue({ ...inspection, kind: "database_restore", report: undefined });
  await mount();
  expect(api.verify).not.toHaveBeenCalled();
  await act(async () => button("Verify databases").click());
  expect(api.verify).toHaveBeenCalledWith(bundle.id);
  expect(api.inspect).not.toHaveBeenCalled();
  expect(host.textContent).toContain("Database verification passed");
  expect(host.textContent).toContain("This is not a restore drill");
});

it("fails closed for malformed bundles and unsupported verification", async () => {
  api.overview.mockResolvedValue({ ...overview, capabilities: { ...overview.capabilities,
    verify: { state: "unsupported", reason: "compose" } } });
  api.inspect.mockResolvedValue({ ...inspection, state: "failed", reason: "invalid_bundle", report: undefined });
  await mount();
  expect(button("Verify databases").disabled).toBe(true);
  expect(host.textContent).toContain("shipped Docker Compose deployment is not supported");
  await act(async () => button("Check integrity").click());
  expect(host.textContent).toContain("Integrity check failed");
  expect(host.textContent).not.toContain("Offline integrity passed");
  expect(host.textContent).not.toContain("Database verification passed");
});

it("drops a passing result when the bundle revision changes", async () => {
  api.inspect.mockResolvedValue(inspection);
  await mount();
  await act(async () => button("Check integrity").click());
  expect(host.textContent).toContain("Offline integrity passed");
  api.overview.mockResolvedValue({ ...overview, bundles: [{ ...bundle, revision: "revision-2", archive_bytes: 9000 }] });
  await act(async () => button("Refresh").click());
  expect(host.textContent).not.toContain("Offline integrity passed");
  expect(host.textContent).toContain("Not checked in this page");
});

it("keeps an old hub and a failed read honest", async () => {
  api.overview.mockRejectedValue(new ApiError(404, "not found", { error: "not found" }));
  await mount();
  expect(host.textContent).toContain("This hub does not offer Recovery reads yet");
  expect(host.textContent).not.toContain("Offline integrity passed");
  expect(host.textContent).toContain("--writes-quiesced");
});

it.each([
  ["en", "Recovery"], ["zh-Hans", "恢复"], ["ja", "復旧"],
])("uses the complete %s catalog and semantic tokens in light and dark", async (language, title) => {
  for (const dark of [false, true]) {
    document.documentElement.classList.toggle("dark", dark);
    await mount(language);
    expect(host.textContent).toContain(title);
    expect(host.querySelector(".bg-surface")).not.toBeNull();
    expect(host.querySelector(".text-ink")).not.toBeNull();
    expect(host.innerHTML).not.toMatch(/#[0-9a-fA-F]{6}/);
    if (dark) await act(async () => root.unmount());
    if (dark) { host.remove(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); }
  }
});
