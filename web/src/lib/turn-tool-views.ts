// Out-of-band declarative views attached by tool calls in a turn (#14730): read from Fold.tool_views on
// folded history and ToolResult.tool_views on expanded/live steps, deduplicated in turn order and capped per
// turn. Model runtimes never receive these views (docs/SUPER_RULES.md, rule 2).
import { readView, type UIView } from "./extension-ui";
import { toolDisplayName, type Step } from "./turns";
import type { FoldToolView } from "./types";

export const MAX_TURN_TOOL_VIEWS = 8;

export interface TurnToolView {
  id: string;
  tool?: string;
  owner: string;
  view: UIView;
}

function hasDrawableContent(view: UIView): boolean {
  return view.sections.some((s) => s.items.length > 0 || Boolean(s.footer) || Boolean(s.more));
}

function parseToolView(raw: unknown): UIView | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  try {
    const view = readView(raw, { html: false });
    if (hasDrawableContent(view)) return view;
  } catch {
    return null;
  }
  return null;
}

function foldKeys(tv: FoldToolView, fallback: string, idx: number): string[] {
  const keys: string[] = [];
  if (tv.item_id) keys.push(`i:${tv.item_id}#${idx}`);
  if (tv.tool_use_id) keys.push(`t:${tv.tool_use_id}#${idx}`);
  if (!keys.length) keys.push(`f:${fallback}#${idx}`);
  return keys;
}

function toolKeys(itemId: string | undefined, callId: string | undefined, fallback: string, idx: number): string[] {
  const keys: string[] = [];
  if (itemId) keys.push(`i:${itemId}#${idx}`);
  if (callId) keys.push(`t:${callId}#${idx}`);
  if (!keys.length) keys.push(`s:${fallback}#${idx}`);
  return keys;
}

export function turnToolViews(steps: readonly Step[], mid = "", sid = ""): TurnToolView[] {
  const out: TurnToolView[] = [];
  const seen = new Set<string>();

  const pushEntry = (keys: string[], raw: unknown, rawTool?: string) => {
    if (out.length >= MAX_TURN_TOOL_VIEWS) return;
    if (keys.some((k) => seen.has(k))) return;
    const view = parseToolView(raw);
    if (!view) return;
    for (const k of keys) seen.add(k);
    const id = keys[0];
    const tool = rawTool ? toolDisplayName(rawTool) : undefined;
    out.push({
      id,
      ...(tool ? { tool } : {}),
      owner: `chat-tool:${mid}/${sid}/${id}`,
      view,
    });
  };

  for (const step of steps) {
    if (out.length >= MAX_TURN_TOOL_VIEWS) break;
    if (step.kind === "fold") {
      const list = step.item.fold?.tool_views ?? [];
      for (let i = 0; i < list.length && out.length < MAX_TURN_TOOL_VIEWS; i++) {
        const entry = list[i];
        if (!entry || typeof entry !== "object") continue;
        pushEntry(foldKeys(entry, step.id, i), entry.view, entry.tool);
      }
      continue;
    }
    if (step.kind === "tool") {
      const res = step.result;
      if (!res || !Array.isArray(res.tool_views) || !res.tool_views.length) continue;
      const rawTool = step.use.tool_name || res.tool_name;
      for (let i = 0; i < res.tool_views.length && out.length < MAX_TURN_TOOL_VIEWS; i++) {
        pushEntry(toolKeys(res.id, res.tool_use_id ?? step.use.tool_use_id, step.id, i), res.tool_views[i], rawTool);
      }
    }
  }
  return out;
}
