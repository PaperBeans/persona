import type { JsonObject, JsonValue, TranscriptItem } from './Domain';
import { legacyContextTag, legacyProduct } from './WireCompatibility.ts';

export interface BackgroundRound {
  readonly id: string;
  readonly start: number;
  readonly until: number;
  readonly items: readonly TranscriptItem[];
}

export interface BackgroundProjection {
  readonly timeline: readonly TranscriptItem[];
  readonly rounds: readonly BackgroundRound[];
}

function object(value: JsonValue | undefined): JsonObject | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : undefined;
}

function parsed(text: string): JsonObject | undefined {
  try { return object(JSON.parse(text) as JsonValue); }
  catch { return undefined; }
}

function visibilityTool(raw: JsonValue | undefined): string {
  if (typeof raw !== 'string') return '';
  const single = new RegExp('^mcp_(?:persona|' + legacyProduct + ')(?:_[0-9a-f]{8,16})?_([a-z][a-z0-9_]*)$', 'i').exec(raw);
  if (single) return single[1];
  if (!raw.startsWith('mcp__')) return raw;
  return new RegExp('^mcp__(?:plugin_)?(?:persona|' + legacyProduct + ')__([a-z][a-z0-9_]*)$').exec(raw)?.[1] || '';
}

function timeOf(item: TranscriptItem): number {
  if (typeof item.ts === 'number') return item.ts < 100000000000 ? item.ts * 1000 : item.ts;
  return typeof item.ts === 'string' ? Date.parse(item.ts) : NaN;
}

/** A message-source block can explicitly identify a user's own pasted content. */
function persistedSource(text: string): JsonObject | null | undefined {
  let rest = text;
  for (;;) {
    const context = new RegExp('^\\s*<(persona-context|' + legacyContextTag + ')\\b([^>]*)>([\\s\\S]*?)<\\/\\1>\\s*').exec(rest);
    if (!context) return undefined;
    if (/\bkind="message-source"/.test(context[2])) {
      const source = parsed(context[3]);
      return source && typeof source.kind === 'string' && source.author !== 'user' ? source : null;
    }
    rest = rest.slice(context[0].length);
  }
}

function backgroundInput(item: TranscriptItem): boolean {
  if (item.role !== 'user' || item.kind !== 'text') return false;
  const body = item.text || '';
  const explicit = object(item.source);
  const persisted = persistedSource(body);
  const source = explicit || (!item.client_msg_id && !item.from ? persisted : undefined);
  const injected = object(item.injected);
  if (source?.kind === 'conversation' || source?.kind === 'patrol') return true;
  if (source?.kind === 'followup') {
    return injected?.state !== 'limit_continue' && injected?.state !== 'wake' && typeof source.title === 'string' && !!source.title.trim();
  }
  if (persisted === null) return false;
  if (injected?.state === 'supervise') return true;
  if (injected?.kind === 'followup' && injected.state !== 'limit_continue' && injected.state !== 'wake') {
    return injected.state === 'daily' || injected.state === 'interval' || !!injected.detail || !!injected.line;
  }
  if (item.client_msg_id || item.from) return false;
  // A legacy supervision input has both the exact heading and the Hub's notice marker.
  return /^📋 督办：你派出去的任务有 \d+ 个要你处理(?:\r?\n|$)/.test(body) && body.includes('这是 Persona 的督办消息');
}

function inputBoundary(item: TranscriptItem, previous?: TranscriptItem): boolean {
  if (item.role !== 'user' || item.tool_use_id || item.toolUseId) return false;
  return item.kind === 'text' || item.wake === true || (item.kind === 'image' && previous?.seq !== item.seq);
}

function completedSilent(items: readonly TranscriptItem[], results: ReadonlyMap<string, TranscriptItem>): boolean {
  let silent = false;
  for (const item of items) {
    const input = object(item.injected);
    if (item.supervision_user || item.fold?.supervision_user || (input?.state === 'supervise' && input.level === 'user')) return false;
    if (item.role === 'user' && !item.client_msg_id && !item.from && /^\s*<persona-supervision level="user"\s*\/>\s*$/m.test(item.text || '')) return false;
    if (item.kind !== 'tool_use') continue;
    const id = item.tool_use_id || item.toolUseId;
    const result = id ? results.get(id) : undefined;
    if (!result || result.is_error === true) continue;
    const name = visibilityTool(item.tool_name);
    if (name === 'notify_user' || name === 'ask_user') return false;
    if (name === 'task_review') {
      const verdict = object(item.tool_input)?.verdict;
      if (verdict === 'accepted' || verdict === 'needs_user') return false;
    }
    if (name === 'supervision_finish') {
      const visibility = parsed(result.text || '')?.visibility;
      if (visibility === 'user') return false;
      if (visibility === 'silent') silent = true;
    }
  }
  return silent;
}

/** Only a completed, unpromoted round leaves Main. Results may arrive on a later history page. */
export function splitBackgroundRounds(items: readonly TranscriptItem[], running: boolean): BackgroundProjection {
  const results = new Map<string, TranscriptItem>();
  for (const item of items) {
    const id = item.tool_use_id || item.toolUseId;
    if (item.kind === 'tool_result' && id) results.set(id, item);
  }
  const rounds: BackgroundRound[] = [];
  const timeline: TranscriptItem[] = [];
  let start = 0;
  function append(end: number): void {
    if (end <= start) return;
    const input = items[start];
    const group = items.slice(start, end);
    if ((!running || end < items.length) && backgroundInput(input) && completedSilent(group, results)) {
      rounds.push({ id: input.id, start: timeOf(input), until: end < items.length ? timeOf(items[end]) : NaN, items: group });
    } else {
      for (const item of group) timeline.push(item);
    }
  }
  for (let i = 1; i < items.length; i++) {
    if (inputBoundary(items[i], items[i - 1])) { append(i); start = i; }
  }
  append(items.length);
  return { timeline, rounds };
}

export function activityFeedRound(rounds: readonly BackgroundRound[], kind: string, at: number): string {
  if (!['task', 'routed', 'answered'].includes(kind) || !Number.isFinite(at)) return '';
  return rounds.find(round => Number.isFinite(round.start) && at >= round.start &&
    (!Number.isFinite(round.until) || at < round.until))?.id || '';
}
