// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { HubUpdateStatus } from "../../lib/hub-update";

let current: HubUpdateStatus;
let checked: HubUpdateStatus | Promise<HubUpdateStatus> | undefined;
const updateApi = vi.fn(async (path: string, opts?: { json?: { source?: HubUpdateStatus["source"] } }) => {
  switch (path) {
    case "/hub/update": return current;
    case "/hub/update/check": return checked;
    case "/hub/update/backups": return { backups: [] };
    case "/hub/update/history": return { history: [] };
    case "/hub/update/settings": return { ...current, source: opts?.json?.source, last_check: undefined, latest: undefined };
    default: return {};
  }
});
vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  api: (...args: Parameters<typeof updateApi>) => updateApi(...args),
}));
await initI18n("en");
const { HubSettings } = await import("./HubSettings");

let root: Root;
let host: HTMLDivElement;

const released = "2026-11-20T15:04:05Z";
const latest = { version: "v1.0.0", build: 202611201504, released, newer: false, bad: false };
const status = (o: Partial<HubUpdateStatus>): HubUpdateStatus => ({
  version: "web-dev-aaaaaaaaaaaa-202611251445", build: 202611251445, channel: "release", source: "release",
  can_update: true, state: "idle", busy: [], development: { configured: true, branch: "main" },
  auto: true, auto_notice: false, confirmed: true, update_url: "https://updates.example.com/dl", update_url_from: "build",
  last_check: "2026-11-25T15:00:00Z", latest, ...o,
});

const updateCard = () => host.querySelector('[data-settings-group="hub-update"]')!;
const button = (label: string) => [...host.querySelectorAll("button")].find(b => b.textContent === label);

const mount = async (s: HubUpdateStatus) => {
  current = s;
  await act(async () => root.render(<HubSettings />));
};

beforeEach(() => {
  checked = undefined;
  updateApi.mockClear();
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("Settings › Hub update source", () => {
  it("keeps the cached version visible during the opening preview and starts the latest target", async () => {
    let finish!: (s: HubUpdateStatus) => void;
    checked = new Promise(resolve => { finish = resolve; });
    const offer = status({ supports_preview: true, source: "web-dev", latest: { ...latest, commit: "b".repeat(40), newer: true } });
    await mount(offer);
    expect(host.textContent).toContain("bbbbbbbbbbbb");
    expect(button("Checking…")).toBeDefined();
    expect(updateApi).toHaveBeenCalledWith("/hub/update/check", { method: "POST", json: { preview: true } });
    await act(async () => finish({ ...offer, latest: { ...offer.latest!, commit: "c".repeat(40) } }));
    expect(host.textContent).toContain("cccccccccccc");
    current = { ...offer, state: "resolving" };
    await act(async () => button("Update hub")!.click());
    expect(updateApi).toHaveBeenCalledWith("/hub/update", { method: "POST", json: { target: "latest" } });
    expect(host.textContent).toContain("Checking the latest version before updating");
    expect(host.querySelector('[aria-label="Update pipeline"]')).toBeNull();
    expect(updateApi.mock.calls.filter(([path]) => path === "/hub/update/check")).toHaveLength(1);
  });

  it.each([
    ["web-dev", "available", false], ["web-dev", "building", true], ["web-dev", "resolving", true],
    ["release", "available", true], ["beta", "available", true],
  ] as const)("skips opening preview for %s / %s / support %s", async (source, state, supports_preview) => {
    await mount(status({ source, state, supports_preview }));
    expect(updateApi.mock.calls.some(([path]) => path === "/hub/update/check")).toBe(false);
  });

  it.each(["release", "beta"] as const)("shows the %s version and time after checking from a newer development build", async source => {
    const version = source === "beta" ? "v1.1.0-beta.1" : latest.version;
    await mount(status({ source, last_check: undefined, latest: undefined }));
    checked = status({ source, latest: { ...latest, version } });
    await act(async () => button("Check for updates")!.click());

    expect(updateApi).toHaveBeenCalledWith("/hub/update/check", { method: "POST" });
    expect(host.querySelector('[data-settings-row="hub-channel"]')?.textContent).toContain("Web development");
    expect(updateCard().textContent).toContain(version);
    expect(updateCard().querySelector("time")?.getAttribute("datetime")).toBe(released);
    expect(updateCard().textContent).toContain("running a newer version");
    expect(updateCard().textContent).toContain("You can't update from this source because downgrades aren't supported.");
    expect(updateCard().textContent).not.toContain("Up to date");
    expect(button("Update hub")).toBeUndefined();
  });

  it("keeps Up to date beside the matching release and its publication time", async () => {
    await mount(status({ version: latest.version, build: latest.build, latest: { ...latest, notes: "Release notes" } }));
    const card = updateCard();
    const date = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(released));
    expect(card.textContent).toContain("Up to date");
    expect(card.textContent).toContain(latest.version);
    expect(card.querySelector("time")?.textContent).toBe(`Released ${date}`);
    expect(card.textContent).not.toContain("Release notes");
    expect(card.textContent).not.toContain("update.url");
  });

  it("offers a newer release with its notes and publication time", async () => {
    await mount(status({ build: 202611191504, latest: { ...latest, newer: true, notes: "Release notes" } }));
    const card = updateCard();
    expect(card.textContent).toContain(`New version ${latest.version}`);
    expect(card.querySelector("time")?.getAttribute("datetime")).toBe(released);
    expect(card.textContent).toContain("Release notes");
    expect(button("Update hub")?.disabled).toBe(false);
  });

  it("shows the stopped source in place of Up to date", async () => {
    await mount(status({ version: "v1.1.0", source_behind: true }));
    const text = updateCard().textContent;
    expect(text).toContain("https://updates.example.com/dl");
    expect(text).toContain("update.url");
    expect(text).toContain(latest.version);
    expect(updateCard().querySelector("time")?.getAttribute("datetime")).toBe(released);
    expect(text).not.toContain("downgrades aren't supported");
    expect(text).not.toContain("Up to date");
  });

  it("prompts for a check after switching to a source with no result", async () => {
    await mount(status({ version: latest.version, build: latest.build }));
    expect(updateCard().textContent).toContain("Up to date");
    const picker = host.querySelector<HTMLSelectElement>('select[aria-label="Update source"]')!;
    await act(async () => { picker.value = "beta"; picker.dispatchEvent(new Event("change", { bubbles: true })); });

    expect(updateApi).toHaveBeenCalledWith("/hub/update/settings", { method: "PUT", json: { source: "beta" } });
    expect(updateCard().textContent).toContain("Check for updates to see the latest version from this source.");
    expect(updateCard().textContent).not.toContain("Up to date");
    expect(updateCard().textContent).not.toContain(latest.version);
    expect(updateCard().querySelector("time")).toBeNull();
  });

  it.each([undefined, "0001-01-01T00:00:00Z"])("shows the version without a publication time when released is %s", async value => {
    await mount(status({ latest: { ...latest, released: value } }));
    expect(updateCard().textContent).toContain(latest.version);
    expect(updateCard().querySelector("time")).toBeNull();
  });

  it("keeps the stopped-source warning beside a failed-check error", async () => {
    await mount(status({ version: "v1.1.0", source_behind: true, error: "Fixture check failed" }));
    const text = updateCard().textContent;
    expect(text).toContain("update.url");
    expect(text).toContain("Fixture check failed");
    expect(text).not.toContain("Up to date");
  });
});
