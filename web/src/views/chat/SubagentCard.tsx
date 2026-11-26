// A subagent the agent launched, as one card in the chat where it launched it (#5030, after cindy's AgentTaskCard): never a
// conversation of its own. Two lines: "Subagent", its title and its status word; then what ran it (the chat's agent:
// Claude Code, Codex, a Paseo provider such as Grok), the model
// it actually ran on (the node reads it from the subagent's own transcript; never the one its call asked for), its tokens,
// its tool calls and how long it took, or "Running for …" each second while it runs. The head opens the card in place,
// remembered per card: what it was asked, what it reported (a long report folds), its last tool and its output file.
// A background subagent's call returns at once with a launch receipt, which is not its result: the card runs until its
// report comes back as a notice.
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { bytes } from "../../lib/format";
import { agentSummary, isLaunchReceipt, modelChipLabel, subagentCardKey, subagentEnded, subagentFor, subagentTitle, type TaskIndex } from "../../lib/tasks";
import { openPanel, type Panel } from "../../lib/panel";
import { compactTokens } from "../../lib/tokens";
import { withoutCutMark } from "../../lib/tool-detail";
import type { Item, Subagent, TaskNote } from "../../lib/types";
import { tsMs, workDuration, type AgentStatus, type ToolStep, type WorkLineStyle } from "../../lib/work-groups";
import { agentName } from "../../lib/agent-kind";
import { Collapse, cx, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { useClamped } from "../../ui/use-clamped";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { isHandbackText } from "./FoldedReport";
import { useFullText } from "./FullText";
import { StepTelemetry } from "./StepTelemetry";
import { ModelChip, RunningFor, TaskCard } from "./TaskCard";

export type SubagentCardPanel = Extract<Panel, { kind: "subagent" }>;

export interface SubagentCardProps {
  step: ToolStep;
  status: AgentStatus;
  mid: string;
  sid?: string;
  scope?: string;
  /** Explicit subagent metadata (`null` for none) skips `subagents` and `ChatContext.subagents` lookup. */
  subagent?: Subagent | null;
  /** Explicit session subagent list skips `ChatContext.subagents` lookup when `subagent` is omitted. */
  subagents?: Subagent[];
  /** Explicit task index skips `ChatContext.tasks` lookup for notes and reports. */
  tasks?: TaskIndex;
  /** Explicit task notes for this call skip `tasks.notes` and `ChatContext.tasks` lookup. */
  notes?: TaskNote[];
  /** Explicit report items for this call skip `tasks.reports` and `ChatContext.tasks` lookup. */
  reports?: Item[];
  /** Explicit work-line style skips `ChatContext.lineStyle` lookup (`"assistant"` omits the harness label). */
  lineStyle?: WorkLineStyle;
  /** Explicit harness label skips `ChatContext.harness` lookup; `null` selects the default harness (`Claude Code`). */
  harness?: string | null;
  /** Controlled expanded state skips default-panel expansion memory (`expanded.ts`). */
  expanded?: boolean;
  /** Called with the next expanded state when the card header is toggled. */
  onExpandedChange?: (expanded: boolean) => void;
  /** Controlled callback for "View full", replacing default `openPanel` (`null` hides the button). */
  onOpenFull?: ((panel: SubagentCardPanel) => void) | null;
  /** Controlled full-text loader for clipped results (`item.full_size`), replacing default `useFullText` / `fullItem`. */
  loadFullText?: (item: Item) => Promise<string>;
}

export interface SubagentResultProps {
  item: Item | undefined;
  fallback: string;
  mid: string;
  sid?: string;
  /** Controlled full-text loader for clipped results (`item.full_size`), replacing default `useFullText` / `fullItem`. */
  loadFullText?: (item: Item) => Promise<string>;
}

/** A one-line piece of a call's input: whitespace runs as one space, at most `max` characters (never half of one). */
export function compactText(text: string | undefined, max: number): string {
  const chars = Array.from((text ?? "").replace(/\s+/g, " ").trim());
  return chars.length <= max ? chars.join("") : `${chars.slice(0, max - 1).join("")}…`;
}

/** What a background subagent reported: its notice's words (the call's own result is only the launch receipt). */
export function reportOf(result: Item | undefined, notes: TaskNote[] | undefined): string {
  if (!isLaunchReceipt(result)) return "";
  const done = notes?.findLast((n) => n.kind === "agent" && n.detail?.trim());
  return done?.detail?.trim() ?? "";
}

/** The item its result is read from: its latest report (a background one's notice, or the call's result once it is one),
 *  else the call's result when that is not the launch receipt. */
function resultItem(result: Item | undefined, reports: Item[]): Item | undefined {
  return reports.at(-1) ?? (result && !isLaunchReceipt(result) ? result : undefined);
}

/** The card's second line: the harness (what runs the chat's subagents, its own agent; none in a Persona chat), the
 *  model chip, tokens, tool uses, and the time it took (none while it runs: "Running for …" ticks there instead). */
export function subagentMeta(meta: Subagent | undefined, took: string, harness: string | undefined): { harness?: string; model: string; tokens?: string; uses?: string; took?: string } {
  return {
    ...(harness ? { harness } : {}),
    model: modelChipLabel(meta?.model),
    ...(meta?.tokens ? { tokens: t("chat-subagentcard.tokens", { value: compactTokens(meta.tokens) }) } : {}),
    ...(meta?.steps ? { uses: t("chat-subagentcard.tool_uses", { count: meta.steps }) } : {}),
    ...(took ? { took } : {}),
  };
}

export const SubagentCard = memo(function SubagentCard(props: SubagentCardProps) {
  const needsSubagent = props.subagent === undefined && props.subagents === undefined;
  const needsTasks = props.tasks === undefined && (props.notes === undefined || props.reports === undefined);
  const needsLineStyle = props.lineStyle === undefined;
  const needsHarness = props.lineStyle !== "assistant" && props.harness === undefined;
  if (needsSubagent || needsTasks || needsLineStyle || needsHarness) {
    return <ContextSubagentCard {...props} />;
  }
  return (
    <SubagentCardExpansion
      {...props}
      resolvedSubagents={props.subagents}
      resolvedTasks={props.tasks}
      resolvedLineStyle={props.lineStyle}
      resolvedHarness={props.harness ?? undefined}
    />
  );
});

interface SubagentCardResolvedContextProps extends SubagentCardProps {
  resolvedSubagents?: Subagent[];
  resolvedTasks?: TaskIndex;
  resolvedLineStyle?: WorkLineStyle;
  resolvedHarness?: string;
}

function ContextSubagentCard(props: SubagentCardProps) {
  const ctx = useChatContext();
  return (
    <SubagentCardExpansion
      {...props}
      resolvedSubagents={props.subagents ?? ctx.subagents}
      resolvedTasks={props.tasks ?? ctx.tasks}
      resolvedLineStyle={props.lineStyle ?? ctx.lineStyle}
      resolvedHarness={props.harness !== undefined ? (props.harness ?? undefined) : ctx.harness}
    />
  );
}

function SubagentCardExpansion(props: SubagentCardResolvedContextProps) {
  if (props.expanded !== undefined) {
    return (
      <SubagentCardView
        {...props}
        open={props.expanded}
        onToggle={() => props.onExpandedChange?.(!props.expanded)}
      />
    );
  }
  return <DefaultExpandedSubagentCard {...props} />;
}

function DefaultExpandedSubagentCard(props: SubagentCardResolvedContextProps) {
  const { step, scope = "", onExpandedChange } = props;
  const call = step.use.tool_use_id ?? step.use.id;
  const [open, setOpen] = useExpanded(subagentCardKey(scope, call));
  const onToggle = () => {
    if (onExpandedChange) {
      let next = !open;
      setOpen((o) => {
        next = !o;
        return next;
      });
      onExpandedChange(next);
    } else {
      setOpen((o) => !o);
    }
  };
  return <SubagentCardView {...props} open={open} onToggle={onToggle} />;
}

interface SubagentCardViewProps extends SubagentCardResolvedContextProps {
  open: boolean;
  onToggle: () => void;
}

function SubagentCardView({
  step,
  status,
  mid,
  sid,
  subagent,
  notes: controlledNotes,
  reports: controlledReports,
  resolvedSubagents,
  resolvedTasks,
  resolvedLineStyle,
  resolvedHarness,
  open,
  onToggle,
  onOpenFull,
  loadFullText,
}: SubagentCardViewProps) {
  const { use, result } = step;
  const call = use.tool_use_id ?? use.id;
  const inp = (use.tool_input ?? {}) as Record<string, unknown>;
  const meta = subagent !== undefined ? (subagent ?? undefined) : subagentFor(resolvedSubagents ?? [], use);
  const running = status === "running";
  const description = typeof inp.description === "string" ? inp.description : "";
  const prompt = typeof inp.prompt === "string" ? inp.prompt : "";
  const title = compactText(description || (meta ? subagentTitle(meta) : "") || prompt, 96) || t("chat-subagentcard.untitled_task");
  const notes = controlledNotes ?? (use.tool_use_id ? resolvedTasks?.notes.get(use.tool_use_id) : undefined);
  const reportsList = controlledReports ?? (use.tool_use_id ? resolvedTasks?.reports.get(use.tool_use_id) : undefined);
  const reports = reportsList ?? [];
  const start = tsMs(meta?.started_at) ?? tsMs(use.ts) ?? undefined;
  const end = running ? undefined : subagentEnded(result, meta);
  const tookMs = start !== undefined && end !== undefined && end > start ? end - start : null;
  const took = start !== undefined && end !== undefined && end >= start ? workDuration(end - start) : "";
  const stepItems = useMemo(
    () => Array.from(new Map([use, result, ...(reportsList ?? [])].filter((it): it is Item => Boolean(it)).map((it) => [it.id, it])).values()),
    [use, result, reportsList],
  );
  // the Persona chat's own calls: what ran it is Persona itself, not a tool its reader works with
  const m = subagentMeta(meta, took, resolvedLineStyle === "assistant" ? undefined : (resolvedHarness ?? agentName("claude")));
  const parts = [
    m.harness && <span key="harness">{m.harness}</span>,
    m.model && <ModelChip key="model">{m.model}</ModelChip>,
    m.tokens && <span key="tokens">{m.tokens}</span>,
    m.uses && <span key="uses">{m.uses}</span>,
    m.took && <span key="took">{m.took}</span>,
    running && start !== undefined && <RunningFor key="running" since={start} />,
  ].filter(Boolean);
  const output = notes?.findLast((n) => n.output_file)?.output_file;
  const fullSid = meta?.session_id || (meta?.id && sid ? `${sid}@${meta.id}` : "");
  const openFull = () => {
    if (!fullSid || onOpenFull === null) return;
    const target: SubagentCardPanel = { kind: "subagent", mid, sid: fullSid, parentMid: mid, parentSid: sid, call, from: "card" };
    if (onOpenFull) onOpenFull(target);
    else openPanel(target);
  };
  return (
    <TaskCard
      icon="agent"
      label={t("chat-subagentcard.subagent")}
      title={title}
      status={status}
      meta={parts}
      onClick={onToggle}
      expanded={open}
      ariaLabel={open ? t("chat-subagentcard.collapse_details") : t("chat-subagentcard.expand_details")}
      data={{ "data-subagent-card": status, "data-tool-use": use.tool_use_id }}
    >
      <Collapse open={open} lazy>
        <div className="mt-2 border-l-2 border-tool-rail pl-3 text-[13px] leading-5 text-ink-2" data-subagent-details>
          <StepTelemetry items={stepItems} durationMs={tookMs} className="mb-1.5" />
          {(prompt || description) && <p className="mb-1" data-subagent-prompt>{compactText(prompt || description, 260)}</p>}
          <SubagentResult
            item={resultItem(result, reports)}
            fallback={running ? "" : reportOf(result, notes) || agentSummary(result, meta)}
            mid={mid}
            sid={sid}
            loadFullText={loadFullText}
          />
          {meta?.last_tool?.name && <p className="text-[12px] leading-4 text-ink-3" data-subagent-last-tool>{t("chat-subagentcard.last_tool", { tool: meta.last_tool.name })}</p>}
          {output && <p className="text-[12px] leading-4 break-all text-ink-3" data-subagent-output>{t("chat-subagentcard.output_file", { path: output })}</p>}
          {fullSid && onOpenFull !== null && (
            <div className="mt-1.5">
              <button
                type="button"
                data-subagent-view-full
                onClick={openFull}
                className="whitespace-nowrap text-xs font-medium text-accent-ink hover:underline"
              >
                {t("chat-subagentcard.view_full")}
              </button>
            </div>
          )}
        </div>
      </Collapse>
    </TaskCard>
  );
}

function useControlledFullText(
  item: Item | undefined,
  mid: string,
  sid: string | undefined,
  loadFullText: (item: Item) => Promise<string>,
): { text: string; more: ReactNode } {
  const key = item?.full_size ? JSON.stringify([mid, sid ?? "", item.id, item.text, item.full_size, item.source_fingerprint]) : "";
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key, epoch: 0 }));
  if (scope.key !== key) {
    setScope({ key, epoch: scope.epoch + 1 });
  }
  const activeEpoch = scope.key === key ? scope.epoch : scope.epoch + 1;
  const activeEpochRef = useRef(activeEpoch);
  activeEpochRef.current = activeEpoch;
  const loadRef = useRef(loadFullText);
  loadRef.current = loadFullText;
  const mounted = useRef(true);
  const nextToken = useRef(0);
  const inFlight = useRef<{ epoch: number; token: number } | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      inFlight.current = null;
    };
  }, []);
  const [whole, setWhole] = useState<{ epoch: number; text: string } | null>(null);
  const [status, setStatus] = useState<{ epoch: number; state: "idle" | "loading" | "failed" } | null>(null);
  const state = status?.epoch === activeEpoch ? status.state : "idle";
  const raw = item?.text ?? "";
  const size = item?.full_size;

  const load = () => {
    if (!item || !size || whole?.epoch === activeEpoch || inFlight.current?.epoch === activeEpoch) return;
    const targetEpoch = activeEpoch;
    const token = ++nextToken.current;
    inFlight.current = { epoch: targetEpoch, token };
    setStatus({ epoch: targetEpoch, state: "loading" });
    let req: Promise<string>;
    try {
      req = Promise.resolve(loadRef.current(item));
    } catch (err) {
      req = Promise.reject(err);
    }
    void req
      .then((text) => {
        if (!mounted.current || activeEpochRef.current !== targetEpoch || inFlight.current?.token !== token) return;
        setWhole({ epoch: targetEpoch, text });
        setStatus({ epoch: targetEpoch, state: "idle" });
      })
      .catch(() => {
        if (!mounted.current || activeEpochRef.current !== targetEpoch || inFlight.current?.token !== token) return;
        setStatus({ epoch: targetEpoch, state: "failed" });
      })
      .finally(() => {
        if (inFlight.current?.token === token) inFlight.current = null;
      });
  };

  if (whole?.epoch === activeEpoch) return { text: whole.text, more: null };
  if (!size) return { text: raw, more: null };
  const loading = state === "loading";
  return {
    text: withoutCutMark(raw),
    more: (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          if (!loading) load();
        }}
        disabled={loading}
        aria-busy={loading || undefined}
        className={cx(
          "mt-1.5 flex h-7 w-fit items-center gap-1.5 rounded-control px-1 font-sans text-meta font-medium whitespace-nowrap transition-colors hover:bg-hover disabled:opacity-60",
          state === "failed" ? "text-red" : "text-accent-ink",
        )}
        data-full-text={state}
      >
        {loading ? <Spinner size={12} /> : <Icon name={state === "failed" ? "retry" : "external"} size={12} />}
        {loading ? t("chat-fulltext.loading") : state === "failed" ? t("chat-fulltext.failed") : t("chat-fulltext.show_all", { size: bytes(size) })}
      </button>
    ),
  };
}

/** What it reported, as plain text: four lines until "Show full result" when it is longer (measured, `useClamped`); a
 *  report the hub cut has its "View full text" too. A child session's card (ChildSessionCard) hands it the child's own
 *  latest words (a text item). */
export function SubagentResult(props: SubagentResultProps) {
  if (props.loadFullText) {
    return <ControlledSubagentResult {...props} loadFullText={props.loadFullText} />;
  }
  return <DefaultSubagentResult {...props} />;
}

function DefaultSubagentResult({ item, fallback, mid, sid }: SubagentResultProps) {
  const own = item?.kind === "tool_result" || item?.kind === "text";
  const full = useFullText(own ? item : undefined, mid, sid);
  return <SubagentResultView item={item} fallback={fallback} fullText={full.text} fullMore={full.more} />;
}

function ControlledSubagentResult({ item, fallback, mid, sid, loadFullText }: SubagentResultProps & { loadFullText: (item: Item) => Promise<string> }) {
  const own = item?.kind === "tool_result" || item?.kind === "text";
  const full = useControlledFullText(own ? item : undefined, mid, sid, loadFullText);
  return <SubagentResultView item={item} fallback={fallback} fullText={full.text} fullMore={full.more} />;
}

function SubagentResultView({ item, fallback, fullText, fullMore }: { item: Item | undefined; fallback: string; fullText: string; fullMore: ReactNode }) {
  // a report the call returned ends with Claude Code's "agentId: …" line, and may start with its hand-back frame: both
  // are for the model
  const said = item?.kind === "tool_result" ? agentSummary({ ...item, text: fullText }, undefined) : item?.kind === "text" ? fullText.trim() : (item ? item.task?.detail ?? "" : fallback).trim();
  const text = isHandbackText(said) ? said.split("\n").slice(1).join("\n").trim() : said;
  const [whole, setWhole] = useState(false);
  const clamp = useClamped<HTMLParagraphElement>(whole, "y", text);
  if (!text) return null;
  return (
    <div className="mb-1" data-subagent-summary data-items={item?.id}>
      <p ref={clamp.ref} className={cx("whitespace-pre-wrap [overflow-wrap:anywhere]", !whole && "line-clamp-4")}>{text}</p>
      {(clamp.cut || whole) && (
        <button type="button" aria-expanded={whole} onClick={() => setWhole((w) => !w)} className="rounded-full text-[12px] text-accent-ink hover:underline">
          {whole ? t("chat-subagentcard.hide_full_result") : t("chat-subagentcard.show_full_result")}
        </button>
      )}
      {fullMore}
    </div>
  );
}
