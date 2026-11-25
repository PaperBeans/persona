// Model families in the model panel (docs/web/web.md "Model pill and model panel", "Families and earlier versions"):
// a provider's list keeps one family's models together (Claude Fable / Opus / Sonnet / Haiku, a GPT line, a Gemini
// tier), newest version first, and folds the older versions under "Earlier versions" unless the user is using them.
// A family is read only from what the list already says: the model's concrete ID (`variants.base` for a folded
// multi-level variant row, else `resolved` for an alias, else `id`, a gateway's vendor path dropped). A model none of
// the patterns below names stays as it was, in its list order.
import type { PanelRow } from "./model-panel";

export interface ModelFamily {
  /** Unique per family: "claude:opus", "gpt:sol", "gemini:flash". */
  key: string;
  /** What the family's header says: "Claude Opus", "GPT Sol", "Gemini Flash". Vendor names, never translated. */
  label: string;
  vendor: "claude" | "gpt" | "gemini";
  /** Claude's tiers by capability (Fable, Opus, Sonnet, Haiku); other vendors keep their list order. */
  rank?: number;
  /** The version's numbers, major first: Opus 4.8 → [4, 8]. */
  version: number[];
}

export interface FamilyModelRef {
  id: string;
  resolved?: string;
  defaultEffort?: string;
  custom?: boolean;
  providerDefault?: boolean;
  variants?: { base: string };
}

const CLAUDE_TIERS = ["fable", "opus", "sonnet", "haiku"];
// Effort words that never name a GPT or Gemini model tier of their own.
const PURE_EFFORTS = new Set(["minimal", "low", "medium", "high", "xhigh"]);
// Gemini's release-channel and thinking words name a variant of a tier, not a tier of its own.
const GEMINI_NON_EFFORT_QUALIFIERS = new Set(["thinking", "preview", "exp", "experimental", "latest"]);
// Gemini effort words that can also be standalone tier names (e.g. gemini-1.0-ultra): strip only after another tier word.
const GEMINI_TRAILING_EFFORTS = new Set(["max", "ultra"]);
const EFFORT_WORDS = "minimal|low|medium|high|xhigh|max|ultra";
// Trailing colon effort/thinking qualifier used by gateway/omp IDs (e.g. google-antigravity/gemini-4-argon:high).
const COLON_QUALIFIER = new RegExp(`:(?:thinking(?:-(?:${EFFORT_WORDS}))?|(?:${EFFORT_WORDS})(?:-thinking)?)$`);
const DATE_STAMP = /(?:-\d{8}|-\d{4}-\d{2}-\d{2})$/;
const CLAUDE_QUALIFIER = new RegExp(`(?:(?:-thinking)?-(?:${EFFORT_WORDS})|-thinking|-(?:${EFFORT_WORDS})-thinking)$`);
const CLAUDE_TIER_FIRST = /^claude-(fable|opus|sonnet|haiku)-(\d{1,2}(?:[.-]\d{1,2})?)$/;
const CLAUDE_VERSION_FIRST = /^claude-(\d{1,2}(?:[.-]\d{1,2})?)-(opus|sonnet|haiku)$/;
const GPT_PURE_EFFORT = /-(?:minimal|low|medium|high|xhigh)$/;

const title = (word: string) => word.charAt(0).toUpperCase() + word.slice(1);
const numbers = (v: string) => v.split(/[.-]/).filter(Boolean).map(Number);

/** The family a model belongs to, from its concrete ID; undefined for anything not plainly Claude, GPT or Gemini. */
export function modelFamily(model: Pick<FamilyModelRef, "id" | "resolved" | "variants">): ModelFamily | undefined {
  const raw = (model.variants?.base?.trim() || model.resolved?.trim() || model.id.trim()).toLowerCase();
  // a gateway's vendor path, a context-size mark, a colon effort/thinking qualifier, a date stamp: none makes a family
  const withoutContext = (raw.split("/").pop() ?? raw).replace(/\[[^\]]*\]$/, "");
  const colonMatch = COLON_QUALIFIER.exec(withoutContext);
  const hadColon = Boolean(colonMatch);
  const colonHasThinking = Boolean(colonMatch?.[0].includes("thinking"));
  const withoutColon = hadColon ? withoutContext.replace(COLON_QUALIFIER, "") : withoutContext;
  const id = withoutColon.replace(/(?:-\d{8}|-\d{4}-\d{2}-\d{2}|@[\w.-]+)$/, "");

  let claudeId = id;
  if (!hadColon && CLAUDE_QUALIFIER.test(claudeId)) {
    claudeId = claudeId.replace(CLAUDE_QUALIFIER, "").replace(DATE_STAMP, "");
  } else if (hadColon && !colonHasThinking && claudeId.endsWith("-thinking")) {
    claudeId = claudeId.slice(0, -"-thinking".length).replace(DATE_STAMP, "");
  }
  let m = CLAUDE_TIER_FIRST.exec(claudeId);
  if (m) return claude(m[1], numbers(m[2]));
  // the older order: claude-3-5-sonnet
  m = CLAUDE_VERSION_FIRST.exec(claudeId);
  if (m) return claude(m[2], numbers(m[1]));

  let gptId = id;
  if (!hadColon && GPT_PURE_EFFORT.test(gptId)) {
    gptId = gptId.replace(GPT_PURE_EFFORT, "").replace(DATE_STAMP, "");
  }
  m = /^gpt-(\d+(?:\.\d+)*)(?:-([a-z][a-z0-9]*(?:-[a-z][a-z0-9]*)*))?$/.exec(gptId);
  if (m) {
    const words = m[2] ? m[2].split("-") : [];
    // Reject double-effort IDs (e.g. gpt-5.5-high-low or gpt-5.4-high:low) instead of turning an effort word into a tier.
    if (words.length && PURE_EFFORTS.has(words[words.length - 1])) return undefined;
    return { key: `gpt:${words.join("-")}`, label: ["GPT", ...words.map(title)].join(" "), vendor: "gpt", version: numbers(m[1]) };
  }

  m = /^gemini-(\d+(?:\.\d+)*)-([a-z][a-z0-9]*(?:-[a-z0-9]+)*)$/.exec(id);
  if (m) {
    const tokens = m[2].split("-").filter((w) => !GEMINI_NON_EFFORT_QUALIFIERS.has(w) && !/^\d+$/.test(w));
    if (hadColon) {
      if (tokens.some((w) => PURE_EFFORTS.has(w))) return undefined;
      if (tokens.length > 1 && GEMINI_TRAILING_EFFORTS.has(tokens[tokens.length - 1])) return undefined;
    }
    const words = tokens.filter((w) => !PURE_EFFORTS.has(w));
    if (words.length > 1 && GEMINI_TRAILING_EFFORTS.has(words[words.length - 1])) words.pop();
    if (!words.length) return undefined;
    return { key: `gemini:${words.join("-")}`, label: ["Gemini", ...words.map(title)].join(" "), vendor: "gemini", version: numbers(m[1]) };
  }
  return undefined;
}

function claude(tier: string, version: number[]): ModelFamily {
  return { key: `claude:${tier}`, label: `Claude ${title(tier)}`, vendor: "claude", rank: CLAUDE_TIERS.indexOf(tier), version };
}

/** Newer first: [5, 5] before [5] before [4, 8]; a missing minor counts as 0. */
export function compareVersions(a: number[], b: number[]): number {
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const d = (b[i] ?? 0) - (a[i] ?? 0);
    if (d) return d;
  }
  return 0;
}

/** One stretch of a section's list: a family (a header, its rows, a fold) or rows outside any family (no header). */
export interface PanelBlock {
  /** Unique in the section; a family's is what `open` holds (with the section's key). */
  key: string;
  family?: Pick<ModelFamily, "key" | "label">;
  /** The rows drawn, newest first; a folded family's are its newest version and those the user is using. */
  rows: PanelRow[];
  /** How many older rows the fold holds (folded) or would hold (unfolded); 0: no fold. */
  older: number;
  /** The family is unfolded. */
  open?: boolean;
}

export interface FamilyOptions {
  /** Not searching: older versions fold. A search shows every row that answers it, families kept together. */
  fold: boolean;
  /** Rows that never fold: the one that runs now, a favorite's or a recent setup's model, one customized. */
  keep: (row: PanelRow) => boolean;
  /** Families the user unfolded, by block key. */
  open: ReadonlySet<string>;
  /** A prefix for the block keys (the section's key). */
  scope: string;
  /** A provider's whole list, when `rows` may be a part of it (a search): which version is a family's newest is read
   *  from it, so a search for "opus 4" does not call Opus 4.8 the latest. */
  listed?: (provider: string) => readonly FamilyModelRef[] | null | undefined;
}

/** A provider's own "no model given" entry (Claude Code's `default`, New Agent's Default): first, outside every family. */
const listDefault = (row: PanelRow) => row.model.providerDefault || row.model.id === "default";

/** Fold multi-level effort variants in a provider's full catalog so `listed` and single-level search matches classify
 *  under the exact same variant base as folded rows (e.g. `gpt-5.5-codex-low` + `gpt-5.5-codex-max` -> `gpt-5.5-codex`). */
function catalogVariantInfo(models: readonly FamilyModelRef[] | null | undefined): {
  listed: Pick<FamilyModelRef, "id" | "resolved" | "variants">[];
  baseOfId: Map<string, string>;
} {
  const baseOfId = new Map<string, string>();
  if (!models?.length) return { listed: [], baseOfId };
  const groups = new Map<string, FamilyModelRef[]>();
  const ordered: (FamilyModelRef | { base: string; levels: FamilyModelRef[] })[] = [];
  for (const m of models) {
    const effort = !m.custom && !m.providerDefault && !m.variants ? m.defaultEffort : undefined;
    const base = effort && m.id.endsWith(`-${effort}`) ? m.id.slice(0, -effort.length - 1) || undefined : undefined;
    if (!base) {
      ordered.push(m);
      continue;
    }
    const existing = groups.get(base);
    if (existing) {
      existing.push(m);
    } else {
      const levels = [m];
      groups.set(base, levels);
      ordered.push({ base, levels });
    }
  }
  const listed: Pick<FamilyModelRef, "id" | "resolved" | "variants">[] = [];
  for (const entry of ordered) {
    if ("levels" in entry) {
      if (entry.levels.length > 1) {
        for (const m of entry.levels) baseOfId.set(m.id, entry.base);
        listed.push({ id: entry.levels[0].id, resolved: entry.levels[0].resolved, variants: { base: entry.base } });
      } else {
        listed.push(entry.levels[0]);
      }
    } else {
      listed.push(entry);
    }
  }
  return { listed, baseOfId };
}

/**
 * A section's rows as family blocks, or undefined when no family has two rows (nothing to keep together or fold: the
 * list stays as its provider gave it). Order: the provider's default entry first; then the families, vendor by vendor in
 * the order the list first names them, Claude's tiers by capability (Fable, Opus, Sonnet, Haiku) and other families by
 * first mention; then the rows no family names, in list order, typed IDs last as before. Within a family the newest
 * version leads (every row of that version, its 1M-context one too) and is marked `latest` when older ones exist.
 * Rows from several providers (the Recommended group) make families of their own per provider.
 */
export function familyBlocks(rows: PanelRow[], o: FamilyOptions): PanelBlock[] | undefined {
  const lead: PanelRow[] = [];
  const rest: PanelRow[] = [];
  const typed: PanelRow[] = [];
  const families = new Map<string, { family: ModelFamily; first: number; rows: { row: PanelRow; family: ModelFamily; at: number }[] }>();
  const vendorFirst = new Map<string, number>();
  const catalogCache = new Map<string, ReturnType<typeof catalogVariantInfo>>();
  const catalogFor = (providerId: string) => {
    let info = catalogCache.get(providerId);
    if (!info) {
      info = catalogVariantInfo(o.listed?.(providerId));
      catalogCache.set(providerId, info);
    }
    return info;
  };
  for (const [at, row] of rows.entries()) {
    if (row.kind === "typed" || row.model.custom) {
      typed.push(row);
      continue;
    }
    if (listDefault(row)) {
      lead.push(row);
      continue;
    }
    const catalogBase = row.kind === "model" && !row.model.variants ? catalogFor(row.provider.id).baseOfId.get(row.model.id) : undefined;
    const family = row.kind === "model" ? modelFamily(catalogBase ? { ...row.model, variants: { base: catalogBase } } : row.model) : undefined;
    if (!family) {
      rest.push(row);
      continue;
    }
    const key = `${row.provider.id}\n${family.key}`;
    const vendorKey = `${row.provider.id}\n${family.vendor}`;
    if (!vendorFirst.has(vendorKey)) vendorFirst.set(vendorKey, at);
    const f = families.get(key) ?? { family, first: at, rows: [] };
    f.rows.push({ row, family, at });
    families.set(key, f);
  }
  if (![...families.values()].some((f) => f.rows.length > 1)) return undefined;

  const ordered = [...families.entries()].sort(([, a], [, b]) => {
    const va = vendorFirst.get(`${a.rows[0].row.provider.id}\n${a.family.vendor}`) ?? 0;
    const vb = vendorFirst.get(`${b.rows[0].row.provider.id}\n${b.family.vendor}`) ?? 0;
    if (va !== vb) return va - vb;
    if (a.family.rank !== undefined && b.family.rank !== undefined && a.family.rank !== b.family.rank) return a.family.rank - b.family.rank;
    return a.first - b.first;
  });

  const blocks: PanelBlock[] = [];
  if (lead.length) blocks.push({ key: `${o.scope}\nlead`, rows: lead, older: 0 });
  for (const [key, f] of ordered) {
    const sorted = [...f.rows].sort((a, b) => compareVersions(a.family.version, b.family.version) || a.at - b.at);
    // the family's versions in the provider's whole list (the rows given, when it isn't known)
    const versions = catalogFor(sorted[0].row.provider.id).listed.map((m) => modelFamily(m)).filter((x) => x?.key === f.family.key).map((x) => x!.version);
    for (const x of sorted) versions.push(x.family.version);
    versions.sort(compareVersions);
    const newest = versions[0];
    const anyOlder = versions.some((v) => compareVersions(v, newest) !== 0);
    const isNewest = (x: (typeof sorted)[number]) => compareVersions(x.family.version, newest) === 0;
    const older = sorted.filter((x) => !isNewest(x) && !o.keep(x.row)).length;
    const blockKey = `${o.scope}\n${key}`;
    const open = o.open.has(blockKey);
    const shown = !o.fold || open ? sorted : sorted.filter((x) => isNewest(x) || o.keep(x.row));
    const marked = shown.map((x) => (isNewest(x) && anyOlder ? { ...x.row, latest: true } : x.row));
    blocks.push({ key: blockKey, family: { key: f.family.key, label: f.family.label }, rows: marked, older: o.fold ? older : 0, ...(o.fold && open ? { open: true } : {}) });
  }
  if (rest.length || typed.length) blocks.push({ key: `${o.scope}\nrest`, rows: [...rest, ...typed], older: 0 });
  return blocks;
}
