// 设置 › 模型供应商's list (docs/integrations/launchers.md「Sync state」): the rc-file launchers grouped by the file they came from,
// the built-in claude as one row per machine, and how each machine's list came in this time. Pure: the page draws it.
import { t } from "../i18n";
import { api } from "./api";
import { ago } from "./format";

/** What an older hub's rc-file launchers came from: it only read ~/.zshrc. */
export const DEFAULT_RC_FILE = "~/.zshrc";
/** GET /launchers waits on every machine; past this the page says so and offers 重试 instead of spinning. */
export const LAUNCHERS_TIMEOUT_MS = 20_000;

export interface LauncherSync {
  machine: string;
  name?: string;
  state: "ok" | "offline" | "timeout" | "error" | string;
  error?: string;
  shell?: string;
  rc_file?: string;
  /** The last good list (RFC 3339), also for a machine that did not answer now. */
  synced_at?: string;
}

/** The fields the list's grouping reads (Settings' LauncherView has the rest). */
export interface ListedLauncher {
  name: string;
  source: "zshrc" | "path" | "persona" | "agy" | "builtin" | string;
  machines?: string[];
  hidden?: boolean;
  rc_files?: string[];
  stale?: string[];
  versions?: Record<string, string>;
}

export interface LaunchersResponse<L> {
  launchers: L[];
  /** Absent from hubs before the per-machine sync state. */
  sync?: LauncherSync[];
}

export class LaunchersTimeout extends Error {
  constructor() {
    super(t("lib-launcher-list.timeout", { seconds: LAUNCHERS_TIMEOUT_MS / 1000 }));
    this.name = "LaunchersTimeout";
  }
}

/** GET /launchers with a deadline: a machine that hangs must not leave the page loading forever. */
export async function loadLaunchers<L>(timeoutMs = LAUNCHERS_TIMEOUT_MS): Promise<LaunchersResponse<L>> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const r = await api<LaunchersResponse<L>>("/launchers", { signal: controller.signal });
    return { launchers: r.launchers ?? [], sync: r.sync };
  } catch (e) {
    if (controller.signal.aborted) throw new LaunchersTimeout();
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

/** The rc files a launcher came from; an older hub's are all ~/.zshrc. */
export function rcFilesOf(l: Pick<ListedLauncher, "rc_files">): string[] {
  return l.rc_files?.length ? l.rc_files : [DEFAULT_RC_FILE];
}

export interface RCGroup<L> {
  /** The files joined with " / " (the group's title). */
  files: string;
  launchers: L[];
}

/** The shown (not hidden) rc-file launchers, one group per file — or per set of files, for one found in several. */
export function rcGroups<L extends ListedLauncher>(list: L[]): RCGroup<L>[] {
  const out: RCGroup<L>[] = [];
  for (const l of list) {
    if (l.source !== "zshrc" || l.hidden) continue;
    const files = [...rcFilesOf(l)].sort().join(" / ");
    const g = out.find((x) => x.files === files);
    if (g) g.launchers.push(l);
    else out.push({ files, launchers: [l] });
  }
  // single files first (alphabetical), then the combinations
  return out.sort((a, b) => a.files.split(" / ").length - b.files.split(" / ").length || a.files.localeCompare(b.files));
}

export interface BuiltinRow {
  name: string;
  machine: string;
  version?: string;
  /** The machine did not answer now: what it shows is from before. */
  stale: boolean;
}

/** The built-in claude, one row per machine that has it; once hidden it is listed with the hidden ones instead (#4384). */
export function builtinRows(list: ListedLauncher[]): BuiltinRow[] {
  const out: BuiltinRow[] = [];
  for (const l of list) {
    if (l.source !== "builtin" || l.hidden) continue;
    for (const id of l.machines ?? []) out.push({ name: l.name, machine: id, version: l.versions?.[id], stale: (l.stale ?? []).includes(id) });
  }
  return out;
}

export type SyncTone = "green" | "orange" | "red" | undefined;

/** One machine's sync state: a short label (the pill) and what it means for the list. */
export function syncStateText(s: LauncherSync, now = Date.now()): { label: string; detail: string; tone: SyncTone } {
  const when = ago(s.synced_at, now);
  switch (s.state) {
    case "ok":
      return { label: t("lib-launcher-list.ok"), detail: s.rc_file ?? "", tone: "green" };
    case "offline":
      return { label: t("lib-launcher-list.offline"), detail: when ? t("lib-launcher-list.showing_last", { when }) : t("lib-launcher-list.never_synced"), tone: undefined };
    case "timeout":
      return { label: t("lib-launcher-list.timed_out"), detail: when ? t("lib-launcher-list.showing_last", { when }) : t("lib-launcher-list.never_synced"), tone: "orange" };
    default:
      return { label: t("lib-launcher-list.failed"), detail: s.error ?? "", tone: "red" };
  }
}
