// A child session (a Codex subagent, an Agent a Paseo Agent created) as one card in its parent's chat (#5120, after cindy's
// AgentTaskCard): never a conversation of its own, and no link to one. The same TaskCard as a subagent call's: "Subagent",
// its title and its status word; then what ran it, the model it runs on with its effort, and how long it took, or
// "Running for …" each second while it runs. Its parent's transcript names no call for it: everything comes from the
// child's own row (lib/use-child-sessions.ts), which the card reads itself (a child's update redraws only its card). The head opens the card in
// place, remembered per card: what it was asked, its latest words (folded past four lines) and its last tool, read from
// the child's transcript only while it is open (lib/child-detail.ts).
import { memo, useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { sessionAgent, agentName } from "../../lib/agent-kind";
import { childCall, childCardKey } from "../../lib/child-cards";
import { readChildDetail, type ChildDetail } from "../../lib/child-detail";
import { stateOf } from "../../lib/format";
import { openPanel, type Panel } from "../../lib/panel";
import { effortTitle } from "../../lib/session-actions";
import { getSession, useStore } from "../../lib/store";
import { modelChipLabel } from "../../lib/tasks";
import { useChildRow } from "../../lib/use-child-sessions";
import { mcpServer, toolDisplayName, toolLabel } from "../../lib/turns";
import type { Session } from "../../lib/types";
import { tsMs, workDuration, type AgentStatus, type WorkLineStyle } from "../../lib/work-groups";
import { Collapse } from "../../ui/atoms";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { compactText, SubagentResult } from "./SubagentCard";
import { ModelChip, RunningFor, TaskCard } from "./TaskCard";

/** How a child is going, by its own row: running while it works or waits on the user (or its background work runs),
 *  failed with a turn error, stopped when it was interrupted, completed otherwise. */
export function childStatus(s: Session): AgentStatus {
  const own = stateOf(s);
  if (own === "working" || own === "needs_you" || s.background_working) return "running";
  if (s.turn_error) return "failed";
  if (s.status === "interrupted") return "stopped";
  return "completed";
}

/** The efforts a card's model chip names (cindy's badge levels); any other value is left out. */
const EFFORTS = new Set(["minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);
export const knownEffort = (effort: string | undefined): string | undefined => (effort && EFFORTS.has(effort) ? effort : undefined);

/** How often an open card of a running child reads its transcript again, at most. */
const CHILD_REREAD_MS = 5000;

/** The model chip's words: the model as a subagent card says it, and the effort it runs at ("GPT-5.5 · High"). */
export function childModelChip(s: Pick<Session, "model" | "effort">): string {
  const effort = knownEffort(s.effort);
  return [modelChipLabel(s.model), effort ? effortTitle(effort) : ""].filter(Boolean).join(" · ");
}

/** A tool named the way the chat's tool rows name it: another server's MCP tool as "<Server> · <Tool>". */
export function childToolName(name: string): string {
  return mcpServer(name) !== null ? toolDisplayName(name) : toolLabel(name);
}

/** The card's second line: what ran it (not in a Persona conversation, where it is Persona itself), the model chip, and
 *  how long it took once it is over (while it runs, "Running for …" ticks there instead). */
export function childMeta(s: Session, harness: boolean): { harness?: string; model?: string; took?: string; since?: number } {
  const running = childStatus(s) === "running";
  const start = tsMs(s.started_at) ?? undefined;
  const end = tsMs(s.updated_at) ?? undefined;
  const agent = sessionAgent(s);
  const model = childModelChip(s);
  return {
    ...(harness ? { harness: agentName(agent.agent, agent.label) } : {}),
    ...(model ? { model } : {}),
    ...(!running && start !== undefined && end !== undefined && end >= start ? { took: workDuration(end - start) } : {}),
    ...(running && start !== undefined ? { since: start } : {}),
  };
}

export type ChildSessionPanel = Extract<Panel, { kind: "subagent" }>;

export type ChildDetailLoader = (mid: string, sid: string, signal?: AbortSignal) => Promise<ChildDetail>;

export interface ChildSessionCardProps {
  mid: string;
  sid: string;
  parentMid?: string;
  parentSid?: string;
  scope?: string;
  /** Explicit child session row (`null` when absent) skips default held-row and hub-store lookups. */
  child?: Session | null;
  /** Explicit work-line style skips `ChatContext` lookup (`"assistant"` omits the harness label). */
  lineStyle?: WorkLineStyle;
  /** Controlled expanded state skips default-panel expansion memory (`expanded.ts`). */
  expanded?: boolean;
  /** Called with the next expanded state when the card header is toggled. */
  onExpandedChange?: (expanded: boolean) => void;
  /** Explicit transcript detail (`null` while loading) skips automatic `readChildDetail` loading. */
  detail?: ChildDetail | null;
  /** Custom transcript detail loader replacing `readChildDetail` when `detail` is omitted. */
  loadDetail?: ChildDetailLoader;
  /** Controlled callback for "View full", replacing default `openPanel`. */
  onOpenFull?: (panel: ChildSessionPanel) => void;
}

export type { ChildDetail };

export const ChildSessionCard = memo(function ChildSessionCard(props: ChildSessionCardProps) {
  if (props.child !== undefined) {
    return props.child ? <ChildSessionCardStyle {...props} child={props.child} /> : null;
  }
  return <DefaultChildRowCard {...props} />;
});

function DefaultChildRowCard(props: ChildSessionCardProps) {
  // the row its parent's chat holds (a paging hub's is a list summary), else the store's
  const held = useChildRow(props.mid, props.sid);
  const stored = useStore(() => (held ? undefined : getSession(props.mid, props.sid)));
  const child = held ?? stored;
  if (!child) return null;
  return <ChildSessionCardStyle {...props} child={child} />;
}

interface ChildCardWithRowProps extends Omit<ChildSessionCardProps, "child"> {
  child: Session;
}

function ChildSessionCardStyle(props: ChildCardWithRowProps) {
  if (props.lineStyle !== undefined) {
    return <ChildSessionCardExpansion {...props} lineStyle={props.lineStyle} />;
  }
  return <ContextLineStyleCard {...props} />;
}

function ContextLineStyleCard(props: ChildCardWithRowProps) {
  const { lineStyle } = useChatContext();
  return <ChildSessionCardExpansion {...props} lineStyle={lineStyle} />;
}

interface ChildCardWithStyleProps extends Omit<ChildCardWithRowProps, "lineStyle"> {
  lineStyle: WorkLineStyle | undefined;
}

function ChildSessionCardExpansion(props: ChildCardWithStyleProps) {
  if (props.expanded !== undefined) {
    return (
      <ChildSessionCardView
        {...props}
        open={props.expanded}
        onToggle={() => props.onExpandedChange?.(!props.expanded)}
      />
    );
  }
  return <DefaultExpandedCard {...props} />;
}

function DefaultExpandedCard(props: ChildCardWithStyleProps) {
  const { mid, sid, parentMid, scope = "", onExpandedChange } = props;
  const cardMid = parentMid && parentMid !== mid ? mid : undefined;
  const [open, setOpen] = useExpanded(childCardKey(scope, sid, cardMid));
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
  return <ChildSessionCardView {...props} open={open} onToggle={onToggle} />;
}

interface ChildCardViewProps extends ChildCardWithStyleProps {
  open: boolean;
  onToggle: () => void;
}

function ChildSessionCardView({ mid, sid, parentMid, parentSid, child, lineStyle, open, onToggle, detail, loadDetail, onOpenFull }: ChildCardViewProps) {
  const cardMid = parentMid && parentMid !== mid ? mid : undefined;
  const status = childStatus(child);
  const title = compactText(child.title, 96) || t("chat-subagentcard.untitled_task");
  const m = childMeta(child, lineStyle !== "assistant");
  const parts = [
    m.harness && <span key="harness">{m.harness}</span>,
    m.model && <ModelChip key="model">{m.model}</ModelChip>,
    m.took && <span key="took">{m.took}</span>,
    m.since !== undefined && <RunningFor key="running" since={m.since} />,
  ].filter(Boolean);
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
      data={{ "data-child-card": status, "data-tool-use": childCall(sid, cardMid) }}
    >
      <Collapse open={open} lazy>
        <ChildDetails
          mid={mid}
          child={child}
          parentMid={parentMid}
          parentSid={parentSid}
          running={status === "running"}
          detail={detail}
          loadDetail={loadDetail}
          onOpenFull={onOpenFull}
        />
      </Collapse>
    </TaskCard>
  );
}

interface ChildDetailsProps {
  mid: string;
  child: Session;
  parentMid?: string;
  parentSid?: string;
  running: boolean;
  detail?: ChildDetail | null;
  loadDetail?: ChildDetailLoader;
  onOpenFull?: (panel: ChildSessionPanel) => void;
}

/** The opened part: mounted only while the card is open, and only then is the child's transcript read. Until it is in,
 *  and when it has no words of the child's, the row's last words stand for its result. */
function ChildDetails(props: ChildDetailsProps) {
  if (props.detail !== undefined) {
    return <ChildDetailsView {...props} detail={props.detail} />;
  }
  return <LoadedChildDetails {...props} />;
}

function LoadedChildDetails(props: ChildDetailsProps) {
  const detail = useChildDetail(props.mid, props.child.session_id, props.child.updated_at, props.running, props.loadDetail);
  return <ChildDetailsView {...props} detail={detail} />;
}

function ChildDetailsView({ mid, child, parentMid, parentSid, detail, onOpenFull }: Omit<ChildDetailsProps, "detail"> & { detail: ChildDetail | null }) {
  const sid = child.session_id;
  const prompt = detail?.prompt ?? "";
  const tool = detail?.lastTool;
  const openFull = () => {
    const target: ChildSessionPanel = {
      kind: "subagent",
      mid,
      sid,
      parentMid: parentMid ?? child.folded_under_machine_id ?? child.parent_machine_id ?? mid,
      parentSid: parentSid ?? child.folded_under ?? child.parent_session_id,
      from: "card",
    };
    if (onOpenFull) onOpenFull(target);
    else openPanel(target);
  };
  return (
    <div className="mt-2 border-l-2 border-tool-rail pl-3 text-[13px] leading-5 text-ink-2" data-subagent-details data-child-details={detail ? "read" : "reading"}>
      {prompt && <p className="mb-1" data-subagent-prompt>{compactText(prompt, 260)}</p>}
      <SubagentResult item={detail?.result} fallback={child.last_text ?? ""} mid={mid} sid={sid} />
      {tool && <p className="text-[12px] leading-4 text-ink-3" data-subagent-last-tool>{t("chat-subagentcard.last_tool", { tool: childToolName(tool) })}</p>}
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
    </div>
  );
}

/** The child's transcript as the opened card shows it: read on opening, and again as the child's row moves while it
 *  runs (at most every CHILD_REREAD_MS) and once more when it stops. A failed read keeps what was shown. */
function useChildDetail(
  mid: string,
  sid: string,
  rev: string | undefined,
  running: boolean,
  loadDetail?: ChildDetailLoader,
): ChildDetail | null {
  const [detail, setDetail] = useState<ChildDetail | null>(null);
  const [read, setRead] = useState(0);
  const last = useRef(0);
  const loadRef = useRef(loadDetail);
  loadRef.current = loadDetail;
  // the row moved: read again now, or once the wait since the last read is over (a burst of pushes is one read)
  useEffect(() => {
    if (!last.current) return;
    const wait = running ? last.current + CHILD_REREAD_MS - Date.now() : 0;
    if (wait <= 0) return void setRead((n) => n + 1);
    const timer = setTimeout(() => setRead((n) => n + 1), wait);
    return () => clearTimeout(timer);
  }, [rev, running]);
  useEffect(() => {
    let live = true;
    const ctl = new AbortController();
    last.current = Date.now();
    (loadRef.current ?? readChildDetail)(mid, sid, ctl.signal).then(
      (d) => live && setDetail(d),
      () => {},
    );
    return () => {
      live = false;
      ctl.abort();
    };
  }, [mid, sid, read]);
  return detail;
}
