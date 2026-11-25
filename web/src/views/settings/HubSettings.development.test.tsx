// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { HubUpdateRecord, HubUpdateStatus } from "../../lib/hub-update";
import type { ManagerList } from "../../lib/managers";
import type { Machine } from "../../lib/types";

let status: HubUpdateStatus;
let history: HubUpdateRecord[] = [];
let machines: Machine[] = [];
let roster: ManagerList | null = null;
const saved: unknown[] = [];
let backupReads = 0;
let updateReads = 0;
let readUpdate: (() => Promise<HubUpdateStatus>) | undefined;
const events = new Map<string, (message: { update?: HubUpdateStatus }) => void>();
const installedAt = "2026-11-13T00:23:47Z";
const latestAt = "2026-11-14T10:24:58+08:00";
const backupCommitAt = "2026-11-12T10:25:11Z";
const backupCreatedAt = "2026-11-13T10:26:35.123Z";
const localTime = new Intl.DateTimeFormat("sv-SE", { dateStyle: "short", timeStyle: "medium", hourCycle: "h23" });
vi.mock("../../lib/hub-update", async (original) => ({
  ...await original<typeof import("../../lib/hub-update")>(),
  hubUpdate: async () => { updateReads++; return readUpdate ? readUpdate() : status; },
  startHubUpdate: async (now: boolean) => {
    saved.push({ now });
    status = { ...status, state: "restarting", busy: [] };
    return status;
  },
  setHubUpdateSettings: async (input: { source?: "release" | "beta" | "web-dev"; development_backup_directory?: string }) => {
    saved.push(input);
    status = { ...status, ...input, auto: false };
    if (input.development_backup_directory !== undefined || (input as { development_backup_retain?: number }).development_backup_retain !== undefined) {
      const nextDir = input.development_backup_directory === undefined ? status.development!.backup_directory
        : input.development_backup_directory || status.development!.backup_default_directory;
      const isDefault = input.development_backup_directory === ""
        ? true
        : input.development_backup_directory !== undefined
          ? nextDir === status.development!.backup_default_directory
          : status.development!.backup_directory_is_default;
      status = { ...status, development: { ...status.development!,
        backup_directory: nextDir,
        backup_directory_is_default: isDefault,
        backup_retain: (input as { development_backup_retain?: number }).development_backup_retain ?? status.development!.backup_retain } };
    }
    return status;
  },
}));
vi.mock("../../lib/hub-update-settings", async (original) => ({
  ...await original<typeof import("../../lib/hub-update-settings")>(),
  hubUpdateHistory: async () => history,
  hubUpdateBackups: async () => { backupReads++; return [{ id: "snapshot-fixture", version: "web-dev-backup", build: 202611120001,
    commit: "c".repeat(40), updated_at: backupCommitAt, created_at: backupCreatedAt }]; },
  applyHubUpdateBackup: async (id: string) => {
    saved.push({ backup: id });
    status = { ...status, state: "applying_backup", auto: false };
    return status;
  },
}));
vi.mock("../../lib/store", async (original) => ({
  ...await original<typeof import("../../lib/store")>(),
  onHubEvent: (type: string, callback: (message: { update?: HubUpdateStatus }) => void) => {
    events.set(type, callback); return () => { events.delete(type); };
  },
  useStore: () => machines,
}));
vi.mock("../../lib/managers", async (original) => ({
  ...await original<typeof import("../../lib/managers")>(), useRoster: () => roster,
}));
vi.mock("./ExtensionDownloadMirrors", () => ({ ExtensionDownloadMirrors: () => null }));
await initI18n("en");
const { HubSettings } = await import("./HubSettings");
let root: ReturnType<typeof createRoot> | undefined;
let host: HTMLDivElement;
afterEach(() => {
  act(() => root?.unmount()); host?.remove();
  saved.length = 0; history = []; machines = []; roster = null; backupReads = 0; events.clear();
  updateReads = 0; readUpdate = undefined; vi.useRealTimers();
});

async function mount(state = "available", overrides: Partial<HubUpdateStatus> = {}) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  status = {
    version: "web-dev-old", build: 202611130000, channel: "release", source: "web-dev", can_update: true,
    state, busy: [], auto: false, auto_notice: false, confirmed: true, auto_update_nodes: false,
    development: { configured: true, branch: "main", current_commit: "a".repeat(40), current_updated_at: installedAt,
      backup_directory: "/var/snapshots", backup_default_directory: "/home/persona/.persona/update/development-backups",
      backup_directory_is_default: false, backup_retain: 3, builtin_quiesce: true },
    latest: { version: "web-dev-new", build: 202611130001, newer: true, bad: false, commit: "b".repeat(40), branch: "main",
      updated_at: latestAt,
      backup_id: state === "applying_backup" ? "snapshot-fixture" : undefined },
    backup: { id: "snapshot-fixture", retained: 3, pruned: ["snapshot-old"] },
    ...overrides,
  };
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => root!.render(<HubSettings />));
}

function pipelineNodes(list: "Update stages" | "Phase steps") {
  return [...host.querySelectorAll<HTMLLIElement>(`ol[aria-label="${list}"] > li`)]
    .map(el => [el.dataset.stage ?? el.dataset.step, el.dataset.state]);
}

async function pushUpdate(next: Partial<HubUpdateStatus>) {
  status = { ...status, ...next };
  await act(async () => events.get("hub_update")!({ update: status }));
}

it("polls active development phases and replaces the step and metrics", async () => {
  vi.useFakeTimers();
  await mount("building", { development_progress: {
    phase: "building", step: "npm_install", step_index: 2, step_total: 6, elapsed_seconds: 10,
  } });
  expect(pipelineNodes("Update stages")).toEqual([
    ["building", "active"], ["backing_up", "pending"], ["waiting_idle", "pending"], ["restarting", "pending"],
  ]);
  expect(pipelineNodes("Phase steps")).toEqual([
    ["fetch", "done"], ["npm_install", "active"], ["web_build", "pending"], ["runtime_packages", "pending"],
    ["go_build", "pending"], ["manifest", "pending"],
  ]);
  const announcement = host.querySelector('section[aria-label="Update pipeline"] [role="status"]');
  expect(announcement?.textContent).toContain("Install Web dependencies");
  expect(host.querySelector('section[aria-label="Update pipeline"] header')?.textContent).toContain("b".repeat(12));
  expect(host.textContent).toContain("Elapsed 10 sec");
  expect(host.querySelector('[role="progressbar"]')).toBeNull();

  status = { ...status, state: "backing_up", development_progress: {
    phase: "backing_up", step: "archive_files", step_index: 3, step_total: 5, elapsed_seconds: 1,
    bytes_done: 182 * 1024 ** 2, bytes_total: 460 * 1024 ** 2, bytes_per_second: 21.5 * 1024 ** 2, percent: 39,
    item_done: 20, item_total: 45,
  } };
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(announcement?.textContent).toContain("Archive history files");
  expect(host.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("39");
  expect(host.textContent).toContain("182 MB / 460 MB");
  expect(host.textContent).toContain("21.5 MB/s");
  expect(host.textContent).toContain("Files 20/45");
  expect(host.textContent).toContain("Elapsed 11 sec");
  expect(pipelineNodes("Update stages")).toEqual([
    ["building", "done"], ["backing_up", "active"], ["waiting_idle", "done"], ["restarting", "pending"],
  ]);
  expect(pipelineNodes("Phase steps")).toEqual([
    ["quiesce", "done"], ["database_dump", "done"], ["archive_files", "active"], ["archive_programs", "pending"], ["verify_snapshot", "pending"],
  ]);

  status = { ...status, state: "confirming", development_progress: undefined };
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(pipelineNodes("Phase steps")).toEqual([]);
  expect(pipelineNodes("Update stages")).toEqual([
    ["building", "done"], ["backing_up", "done"], ["waiting_idle", "done"], ["restarting", "active"],
  ]);
  expect(host.textContent).toContain("Confirming");
  const reads = updateReads;
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(updateReads).toBe(reads);
});

it.each([
  { step: "database_dump", stepIndex: 2, stepTotal: 6, builtinQuiesce: undefined },
  { step: "resume_writers", stepIndex: 5, stepTotal: 5, builtinQuiesce: true },
  { step: "database_dump", stepIndex: 2, stepTotal: 5, builtinQuiesce: false },
])("shows external quiesce's resume step with $step at $stepIndex/$stepTotal ($builtinQuiesce)", async ({ step, stepIndex, stepTotal, builtinQuiesce }) => {
  await mount("backing_up", { development: { configured: true, branch: "main", builtin_quiesce: builtinQuiesce },
    development_progress: { phase: "backing_up", step, step_index: stepIndex, step_total: stepTotal,
      elapsed_seconds: 24, item_done: 1, item_total: 2 } });
  expect(pipelineNodes("Phase steps").map(([name]) => name)).toEqual([
    "quiesce", "database_dump", "archive_files", "archive_programs", "resume_writers", "verify_snapshot",
  ]);
  expect(host.querySelector('ol[aria-label="Phase steps"] [aria-current="step"]')?.getAttribute("data-step")).toBe(step);
  expect(host.textContent).toContain("Phase elapsed 24 sec");
  if (step === "database_dump") expect(host.textContent).toContain("Databases 1/2");
});

it("uses the named node before the index and keeps telemetry for an unknown step", async () => {
  const p = { phase: "building", step: "go_build", step_index: 1, step_total: 6, elapsed_seconds: 5,
    item_label: "personad (linux-amd64)", percent: 75, item_done: 3, item_total: 4 } as const;
  await mount("building", { development_progress: p });
  expect(host.querySelector('ol[aria-label="Phase steps"] [aria-current="step"]')?.getAttribute("data-step")).toBe("go_build");
  expect(host.textContent).toContain("personad (linux-amd64)");
  expect(host.textContent).toContain("Items 3/4");

  await pushUpdate({ development_progress: { ...p, step: "sign_artifacts", step_index: 3, step_total: 7, percent: 0 } });
  expect(pipelineNodes("Phase steps")).toEqual([
    ["fetch", "done"], ["npm_install", "done"], ["sign_artifacts", "active"], ["web_build", "pending"],
    ["runtime_packages", "pending"], ["go_build", "pending"], ["manifest", "pending"],
  ]);
  const bar = host.querySelector('[role="progressbar"]');
  expect(bar?.getAttribute("aria-label")).toMatch(/sign artifacts/i);
  expect(bar?.getAttribute("aria-valuenow")).toBe("0");
});

it("keeps the backup target and stages through preparation, idle wait and reconnect", async () => {
  await mount("applying_backup", { development_progress: { phase: "applying_backup", step: "prepare_artifacts",
    step_index: 2, step_total: 2, elapsed_seconds: 6, item_done: 0, item_total: 2, percent: 0 } });
  expect(pipelineNodes("Update stages")).toEqual([
    ["backing_up", "pending"], ["applying_backup", "active"], ["waiting_idle", "pending"], ["restarting", "pending"],
  ]);
  expect(pipelineNodes("Phase steps")).toEqual([["verify_snapshot", "done"], ["prepare_artifacts", "active"]]);
  expect(host.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("0");

  await pushUpdate({ state: "waiting_idle", busy: [{ title: "Writing a summary", machine: "Desk computer", machine_id: "desk" }] });
  expect(pipelineNodes("Update stages")).toEqual([
    ["backing_up", "pending"], ["applying_backup", "done"], ["waiting_idle", "active"], ["restarting", "pending"],
  ]);
  expect(pipelineNodes("Phase steps")).toEqual([]);
  const pipeline = host.querySelector('section[aria-label="Update pipeline"]')!;
  expect(pipeline.textContent).toContain("Writing a summary");
  expect(pipeline.textContent).toContain("Desk computer");
  const now = [...pipeline.querySelectorAll("button")].find(b => b.textContent === "Update now…")!;
  await act(async () => now.click());
  expect(saved).toEqual([]);
  const dialog = document.querySelector('[role="dialog"]')!;
  await act(async () => [...dialog.querySelectorAll("button")].find(b => b.textContent === "Update now")!.click());
  expect(saved).toEqual([{ now: true }]);
  await pushUpdate({ state: "confirming", latest: undefined });
  expect(pipelineNodes("Update stages")).toEqual([
    ["backing_up", "done"], ["applying_backup", "done"], ["waiting_idle", "done"], ["restarting", "active"],
  ]);
  expect(pipeline.querySelector("header")?.textContent).toContain("b".repeat(12));
});

it.each(["release", "beta"] as const)("shows download progress and the restart stages for %s", async source => {
  await mount("downloading", { source, progress: "hub 75%", latest: { version: "v2.0", build: 2, newer: true, bad: false } });
  expect(pipelineNodes("Update stages")).toEqual([["downloading", "active"], ["waiting_idle", "pending"], ["restarting", "pending"]]);
  expect(host.textContent).toContain("hub 75%");
  expect(host.querySelector('section[aria-label="Update pipeline"] header')?.textContent).toContain("v2.0");
  for (const state of ["installing", "restarting", "confirming"]) {
    await pushUpdate({ state });
    expect(pipelineNodes("Update stages")).toEqual([["downloading", "done"], ["waiting_idle", "done"], ["restarting", "active"]]);
    expect(host.textContent).not.toContain("hub 75%");
  }
  await pushUpdate({ state: "updated", latest: undefined });
  expect(host.querySelector('section[aria-label="Update pipeline"]')).toBeNull();
});

it("ignores a delayed poll after a newer state push", async () => {
  vi.useFakeTimers();
  await mount("backing_up");
  let respond!: (value: HubUpdateStatus) => void;
  readUpdate = () => new Promise(resolve => { respond = resolve; });
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(updateReads).toBe(2); // No overlapping requests while a poll is pending.
  await act(async () => {
    events.get("hub_update")?.({ update: { ...status, state: "failed", error: "The snapshot failed." } });
    respond(status);
  });
  expect(host.textContent).toContain("The snapshot failed.");
});

it("selects Web development with independent computers and shows each assistant’s reported deployment", async () => {
  roster = { default: "studio", managers: [
    { id: "assistant", name: "Home", online: true, builtin: true },
    { id: "studio", name: "Studio", online: true, default: true },
  ] };
  machines = [
    { id: "desk", name: "Desk computer", manager: "assistant", online: true, node_version: "desk-build",
      node_mismatch: true, deploy_phase: "transferring", deploy_source: "hub_package" },
    { id: "lab", name: "Lab computer", manager: "studio", online: true, node_version: "lab-build",
      deploy_phase: "waiting_idle", deploy_source: "local_build" },
    { id: "travel", name: "Travel computer", manager: "studio", online: true, node_version: "travel-build",
      node_mismatch: true, can_deploy: false, deploy_reason: "Install Git and Go on the assistant’s computer.",
      deploy_error: "Could not build the confirmed commit." },
  ];
  await mount("idle", { source: "release" });
  const source = host.querySelector<HTMLSelectElement>('select[aria-label="Update source"]')!;
  expect(source.querySelector<HTMLOptionElement>('[value="web-dev"]')!.disabled).toBe(false);
  await act(async () => { source.value = "web-dev"; source.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(saved).toEqual([{ source: "web-dev" }]);
  expect(source.value).toBe("web-dev");
  expect(host.textContent).toContain("Once the hub confirms that commit, independent assistants update when idle.");
  expect(host.textContent).toContain("it restores its previous program");
  expect(host.textContent).toContain("the assistant needs Git and Go on its own computer to build the confirmed commit");
  expect(host.querySelector('[role="switch"][aria-label="Update computers automatically"]')?.getAttribute("aria-checked")).toBe("false");

  const rows = [...host.querySelectorAll<HTMLButtonElement>('[data-settings-group="hub-nodes"] button')];
  expect(rows).toHaveLength(3);
  expect(rows[0].textContent).toContain("Managed by Home · Package from hub");
  expect(rows[0].textContent).toContain("desk-build");
  expect(rows[0].textContent).toContain("Transferring");
  expect(rows[1].textContent).toContain("Managed by Studio · Built locally by the assistant");
  expect(rows[1].textContent).toContain("lab-build");
  expect(rows[1].textContent).toContain("Waiting for idle");
  expect(rows[2].textContent).toContain("Managed by Studio");
  expect(rows[2].textContent).toContain("Version differs");
  expect(rows[2].textContent).toContain("Last deploy failed: Could not build the confirmed commit.");
  expect(rows[2].textContent).toContain("Install Git and Go on the assistant’s computer.");
  expect(rows[2].textContent).not.toMatch(/Package from hub|Built locally by the assistant/);
  await act(async () => rows[1].click());
  expect(location.pathname).toBe("/machines/lab");
});

it("keeps Web development disabled and displays the hub’s reason when local writers need hooks", async () => {
  const why = "Configure both backup quiesce and resume commands for the second local assistant.";
  await mount("idle", { source: "release", development: { branch: "main", configured: false, why } });
  expect(host.querySelector<HTMLOptionElement>('option[value="web-dev"]')!.disabled).toBe(true);
  expect(host.textContent).toContain(why);
  expect(host.querySelector('select[aria-label="Update version"]')).toBeNull();
});

it("keeps an older or offline computer navigable without guessing a package source or deployment result", async () => {
  machines = [
    { id: "offline", name: "Offline computer", online: false, node_version: "last-reported" },
    { id: "older", name: "Older computer", online: true, deploying: true },
  ];
  await mount();
  const rows = host.querySelectorAll('[data-settings-group="hub-nodes"] button');
  expect(rows[0].textContent).toContain("last-reported");
  expect(rows[0].textContent).toContain("Offline");
  expect(rows[1].textContent).toContain("Unknown");
  expect(rows[1].textContent).toContain("Deploying");
  for (const row of rows) expect(row.textContent).not.toMatch(/Package from hub|Built locally by the assistant|Managed by|Up to date/);
});

it("offers beta as an explicit reversible source without enabling it by default", async () => {
  await mount();
  await act(async () => events.get("hub_update")!({ update: { ...status, source: "release" } }));
  const source = host.querySelector('select[aria-label="Update source"]') as HTMLSelectElement;
  expect(source.value).toBe("release");
  expect(source.textContent).toContain("Beta (prereleases)");
  await act(async () => { source.value = "beta"; source.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(saved).toEqual([{ source: "beta" }]);
  expect(source.value).toBe("beta");
  expect(host.textContent).toContain("Choose Release to return to stable updates");
  await act(async () => { source.value = "release"; source.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(saved).toEqual([{ source: "beta" }, { source: "release" }]);
  expect(host.textContent).not.toContain("Choose Release to return to stable updates");
});

it("shows second-level local times on commits, version options, backup details and history", async () => {
  history = [{ from_version: "previous", to_version: "current", to_build: 123, result: "updated", auto: false,
    source: "web-dev", commit: "d".repeat(40), started: backupCreatedAt, ended: installedAt },
  { from_version: "older", to_version: "previous", to_build: 122, result: "updated", auto: false, started: backupCommitAt }];
  await mount();
  const versionGroup = host.querySelector('[data-settings-group="hub-version"]')!;
  const installed = localTime.format(new Date(installedAt));
  const latest = localTime.format(new Date(latestAt));
  const backup = localTime.format(new Date(backupCommitAt));
  const created = localTime.format(new Date(backupCreatedAt));
  expect(versionGroup.textContent).toContain(`Installed commit${installed} · ${"a".repeat(12)}`);
  expect(versionGroup.textContent).toContain(`Latest commit${latest} · ${"b".repeat(12)}`);
  expect(versionGroup.textContent).not.toContain("Updated at");
  const version = host.querySelector<HTMLSelectElement>('select[aria-label="Update version"]')!;
  expect(version.options[0].textContent).toContain(`${latest} · ${"b".repeat(12)}`);
  expect(version.options[1].textContent).toContain(`${backup} · ${"c".repeat(12)}`);
  expect(version.classList.contains("tabular-nums")).toBe(true);
  await act(async () => { version.value = "snapshot-fixture"; version.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(host.textContent).toContain(`Target commit${backup} · ${"c".repeat(12)}`);
  expect(host.textContent).toContain(`Backed up at${created}`);
  const historyGroup = [...host.querySelectorAll("section")].find(s => s.querySelector("h2")?.textContent === "Update history")!;
  expect([...historyGroup.querySelectorAll(".tabular-nums")].map(el => el.textContent)).toEqual([
    installed, `${installed} · ${"d".repeat(12)}`, backup,
  ]);
});

it.each(["invalid", "2026-02-30T10:00:00Z", "2026-11-14"])("shows Unknown for omitted timestamps and %s", async updatedAt => {
  history = [{ from_version: "previous", to_version: "current", to_build: 123, result: "updated", auto: false }];
  await mount("available", { development: { configured: true, branch: "main", current_commit: "a".repeat(40) },
    latest: { version: "web-dev-new", build: 123, newer: true, bad: false, commit: "b".repeat(40), updated_at: updatedAt } });
  expect(host.textContent).toContain(`Installed commitUnknown · ${"a".repeat(12)}`);
  expect(host.textContent).toContain(`Latest commitUnknown · ${"b".repeat(12)}`);
  const version = host.querySelector<HTMLSelectElement>('select[aria-label="Update version"]')!;
  expect(version.options[0].textContent).toContain(`Unknown · ${"b".repeat(12)}`);
  expect(host.textContent).toContain("Update historyUpdatedUnknown");
});

it("hides backup versions when switching to the release source", async () => {
  await mount();
  const automatic = host.querySelector('[role="switch"]') as HTMLButtonElement;
  expect(automatic.getAttribute("aria-checked")).toBe("false");
  const source = host.querySelector('select[aria-label="Update source"]') as HTMLSelectElement;
  await act(async () => { source.value = "release"; source.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(saved).toEqual([{ source: "release" }]);
  expect(host.textContent).not.toContain("snapshot-fixture");
  expect(host.querySelector('select[aria-label="Update version"]')).toBeNull();
});

it.each([
  { version: "web-dev-running", channel: "release", source: "web-dev", match: true },
  { version: "v1.0", channel: "release", source: undefined, match: true },
  { version: "v1.0-beta.1", channel: "beta", source: "beta", match: true },
  { version: "web-dev-running", channel: "release", source: "release", match: false, running: "Web development" },
  { version: "v1.0-beta.1", channel: "beta", source: "release", match: false, running: "Beta (prereleases)" },
] as const)("keeps the channel search anchor with $version and source $source", async ({ match, running, ...s }) => {
  await mount("idle", s);
  const source = host.querySelector<HTMLSelectElement>('select[aria-label="Update source"]')!;
  expect(source.disabled).toBe(false);
  expect(source.value).toBe(s.source ?? "release");
  const anchors = host.querySelectorAll('[data-settings-row="hub-channel"]');
  expect(anchors).toHaveLength(1);
  if (match) expect(anchors[0].contains(source)).toBe(true);
  else expect(anchors[0].textContent).toBe(`Channel${running}`);
});

it.each(["release", "docker"])("keeps the Channel row on a %s hub without source selection", async channel => {
  await mount("idle", { version: "v1.0", channel, source: undefined, development: undefined });
  expect(host.querySelector('[data-settings-row="hub-channel"]')?.textContent).toBe(`Channel${channel === "docker" ? "Docker" : "Release"}`);
  expect(host.querySelector('select[aria-label="Update source"]')).toBeNull();
});

it.each(["web-dev", "release", "beta"] as const)("shows target notes and warnings with the %s source", async source => {
  await mount("available", { source, latest: { version: "fixture-target", commit: "b".repeat(40), build: 123,
    newer: true, bad: false, held: "Test update required", notes: "A note from the source." }, auto: true });
  const update = host.querySelector('[data-settings-group="hub-update"]')!;
  expect(update.textContent).toContain("A note from the source.");
  expect(update.textContent).toContain("Test update required");
  if (source === "web-dev") {
    expect(update.querySelector('select[aria-label="Update version"]')).not.toBeNull();
    expect(update.textContent).not.toContain("New version fixture-target");
  } else expect(update.textContent).toContain("New version fixture-target");
});

it("confirms program-only restoration and applies the selected backup", async () => {
  await mount();
  const version = host.querySelector('select[aria-label="Update version"]') as HTMLSelectElement;
  expect(version).not.toBeNull();
  await act(async () => { version.value = "snapshot-fixture"; version.dispatchEvent(new Event("change", { bubbles: true })); });
  const apply = [...host.querySelectorAll("button")].find(b => b.textContent === "Apply backup")!;
  await act(async () => apply.click());
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("current database and history");
  expect(saved).toEqual([]);
  const confirm = [...dialog.querySelectorAll("button")].find(b => b.textContent === "Apply backup")!;
  await act(async () => confirm.click());
  expect(saved).toEqual([{ backup: "snapshot-fixture" }]);
  expect((host.querySelector('select[aria-label="Update version"]') as HTMLSelectElement).disabled).toBe(true);
});

it("keeps the current-release message hidden when a historical backup is selected", async () => {
  await mount();
  await act(async () => events.get("hub_update")!({ update: {
    ...status, last_check: "2026-11-14T10:00:00Z", latest: { ...status.latest!, newer: false },
  } }));
  expect(host.textContent).toContain("Up to date");
  const version = host.querySelector('select[aria-label="Update version"]') as HTMLSelectElement;
  await act(async () => { version.value = "snapshot-fixture"; version.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(host.textContent).toContain("Apply backup");
  expect(host.textContent).not.toContain("Up to date");
});

it("refreshes backups when an update finishes without a changed build or receipt", async () => {
  await mount();
  expect(backupReads).toBe(1);
  for (const state of ["building", "backing_up", "waiting_idle", "confirming"]) {
    await act(async () => events.get("hub_update")!({ update: { ...status, state } }));
  }
  expect(backupReads).toBe(1);
  await act(async () => events.get("hub_update")!({ update: { ...status, state: "updated" } }));
  expect(backupReads).toBe(2);
});

it.each(["building", "backing_up", "applying_backup"])("keeps channel and duplicate actions disabled while %s", async state => {
  await mount(state);
  expect((host.querySelector("select") as HTMLSelectElement).disabled).toBe(true);
  expect([...host.querySelectorAll("button")].some(button => button.textContent === "Update hub")).toBe(false);
  expect(host.textContent).toContain(state === "building" ? "Building" : state === "backing_up" ? "Backing up" : "Preparing backup");
  if (state === "applying_backup") {
    const version = host.querySelector('select[aria-label="Update version"]') as HTMLSelectElement;
    expect(version.value).toBe("snapshot-fixture");
    expect(version.options[0].textContent).not.toContain("b".repeat(12));
  }
});

it("saves a declarative backup folder and retention, and resets to the recommended folder", async () => {
  await mount();
  const group = host.querySelector('[data-settings-group="hub-dev-backup"]')!;
  expect(group.textContent).toContain("Development backup");
  expect(group.textContent).toContain("The hub pauses its own writers");
  const folder = group.querySelector('input[aria-label="Backup folder"]') as HTMLInputElement;
  const retain = group.querySelector('select[aria-label="Snapshots to keep"]') as HTMLSelectElement;
  expect(folder.placeholder).toBe("/home/persona/.persona/update/development-backups");
  expect(folder.value).toBe("/var/snapshots");
  const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    setValue.call(folder, "/srv/persona-snapshots");
    folder.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => { retain.value = "10"; retain.dispatchEvent(new Event("change", { bubbles: true })); });
  const save = () => [...group.querySelectorAll("button")].find((b) => b.textContent === "Save")!;
  await act(async () => save().click());
  expect(saved).toContainEqual({ development_backup_directory: "/srv/persona-snapshots", development_backup_retain: 10 });
  await act(async () => [...group.querySelectorAll("button")].find((b) => b.textContent === "Use recommended")!.click());
  expect((group.querySelector('input[aria-label="Backup folder"]') as HTMLInputElement).value).toBe("");
  await act(async () => save().click());
  expect(saved).toContainEqual({ development_backup_directory: "", development_backup_retain: 10 });
});

it("reloads the snapshot list after the folder changes", async () => {
  await mount();
  expect(backupReads).toBe(1);
  const group = host.querySelector('[data-settings-group="hub-dev-backup"]')!;
  const folder = group.querySelector('input[aria-label="Backup folder"]') as HTMLInputElement;
  const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    setValue.call(folder, "/srv/persona-snapshots");
    folder.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const save = [...group.querySelectorAll("button")].find((b) => b.textContent === "Save")!;
  await act(async () => save.click());
  // A snapshot listed from the old folder cannot be found in the new one.
  expect(backupReads).toBe(2);
});

it("omits development_backup_directory when only snapshot retention changed", async () => {
  await mount();
  const group = host.querySelector('[data-settings-group="hub-dev-backup"]')!;
  const retain = group.querySelector('select[aria-label="Snapshots to keep"]') as HTMLSelectElement;
  await act(async () => { retain.value = "10"; retain.dispatchEvent(new Event("change", { bubbles: true })); });
  const save = [...group.querySelectorAll("button")].find((b) => b.textContent === "Save")!;
  await act(async () => save.click());
  expect(saved).toEqual([{ development_backup_retain: 10 }]);
});

it("uses backup_directory_is_default even when symlinked raw paths differ", async () => {
  await mount();
  await act(async () => events.get("hub_update")!({ update: {
    ...status,
    development: {
      ...status.development!,
      backup_directory: "/private/var/persona/update/development-backups",
      backup_default_directory: "/var/persona/update/development-backups",
      backup_directory_is_default: true,
    },
  } }));
  const group = host.querySelector('[data-settings-group="hub-dev-backup"]')!;
  const folder = group.querySelector('input[aria-label="Backup folder"]') as HTMLInputElement;
  const useRecommended = [...group.querySelectorAll("button")].find((b) => b.textContent === "Use recommended") as HTMLButtonElement;
  expect(folder.value).toBe("");
  expect(useRecommended.disabled).toBe(true);
  const retain = group.querySelector('select[aria-label="Snapshots to keep"]') as HTMLSelectElement;
  await act(async () => { retain.value = "5"; retain.dispatchEvent(new Event("change", { bubbles: true })); });
  const save = [...group.querySelectorAll("button")].find((b) => b.textContent === "Save")!;
  await act(async () => save.click());
  expect(saved).toEqual([{ development_backup_retain: 5 }]);
});
