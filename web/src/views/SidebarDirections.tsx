// A 常驻 row's disclosure in the sidebar (#4352): each leader row under Persona chats (#4610: its conversation's row with
// "Doing N · Needs you N") gets a chevron that folds open to the tasks the hub's direction tree lists under it (the one
// tree, lib/direction-tree.ts, the counts come from too), each task in its state word and opening its to-do, each task
// folding open to its agents, each agent to its own. Folded (the default, kept per leader on this device) the row is
// #4610's own. A leader the tree does not list (an older hub, a leader made a moment ago) keeps the plain row.
import { memo, useMemo } from "react";
import { t } from "../i18n";
import { availableConversation } from "../lib/conversation-deleted";
import { useDirectionTree, type Direction, type DirectionTree, type ExternalDirection } from "../lib/direction-tree";
import { directionTaskNode } from "../lib/direction-agent-tree";
import { sameManager } from "../lib/manager-choice";
import { useAgentSessions } from "../lib/sessions";
import type { Session } from "../lib/types";
import { useFolded } from "../lib/use-folded";
import { Collapse, cx, sidebarFoldMotion } from "../ui/atoms";
import { Icon } from "../ui/icons";
import type { GoFn, RegFn } from "./SidebarParts";
import { SubHeading } from "./SidebarRows";
import { TreeTodoRows } from "./SidebarTree";

export type SidebarDirectionEntry = {
  id: string;
  conversation?: Session;
  external?: ExternalDirection;
};

/**
 * Unarchived Persona conversations visible to the sidebar: combines the assistant snapshot with live
 * assistant rows on the same machine and manager while preserving object references from `listed` so
 * memoized sidebar rows do not re-render on unrelated pushes.
 */
export function sidebarConversations(
  listed: readonly Session[],
  live: readonly Session[],
  manager?: string,
): Session[] {
  const liveById = new Map<string, Session>();
  for (const s of live) {
    if (!s.assistant || s.space || !sameManager(s.manager, manager)) continue;
    liveById.set(s.session_id, s);
  }
  const out: Session[] = [];
  const seen = new Set<string>();
  for (const c of listed) {
    seen.add(c.session_id);
    if (!availableConversation(c)) continue;
    const pushed = liveById.get(c.session_id);
    if (pushed) {
      if (!availableConversation(pushed) || pushed.archived === true) continue;
      out.push(c.archived ? { ...c, ...pushed, archived: false } : c);
    } else if (!c.archived) {
      out.push(c);
    }
  }
  for (const s of liveById.values()) {
    if (seen.has(s.session_id) || !availableConversation(s) || s.archived === true) continue;
    out.push(s);
  }
  return out;
}

/**
 * Resolves every unarchived pinned direction in hub leader order without truncation: excludes the main
 * chat and built-in background supervisor, prefers a real unarchived conversation, and falls back only
 * to explicitly external directions (never fabricating rows for archived or removed leaders).
 */
export function resolveSidebarDirections({
  leaders,
  main,
  backgroundSupervisor,
  conversations,
  externalOf,
}: {
  leaders: readonly string[];
  main?: string;
  backgroundSupervisor?: string;
  conversations: readonly Session[];
  externalOf: (id: string) => ExternalDirection | undefined;
}): SidebarDirectionEntry[] {
  const byId = new Map<string, Session>();
  for (const c of conversations) {
    if (!c.archived && availableConversation(c) && !byId.has(c.session_id)) byId.set(c.session_id, c);
  }
  const seen = new Set<string>();
  const out: SidebarDirectionEntry[] = [];
  for (const id of leaders) {
    if (!id || seen.has(id) || id === main || (backgroundSupervisor && id === backgroundSupervisor)) continue;
    seen.add(id);
    const conversation = byId.get(id);
    if (conversation) {
      out.push({ id, conversation });
      continue;
    }
    const external = externalOf(id);
    if (external) out.push({ id, external });
  }
  return out;
}

type Props = {
  /** The direction's opaque id (its leader conversation's when it has one, never the external Agent's session id). */
  id: string;
  /** Its name as the row shows it, for the disclosure's label. */
  name: string;
  /** The row #4610 draws for it. */
  row: React.ReactNode;
  reg: RegFn;
  hover: (k: string | null) => void;
  go: GoFn;
  onNavigate?: () => void;
  /** Optional controlled direction tree for shell hosts; when omitted, reads the shared tree store. */
  directionTree?: DirectionTree | null;
  /** Optional controlled agent sessions for shell hosts; when omitted, reads the shared sessions store. */
  agentSessions?: readonly Session[];
};

/** A 常驻 row and, with the tree, its disclosure and its tasks. */
export function SidebarDirectionRow({ directionTree, ...p }: Props) {
  if (directionTree !== undefined) return <SidebarDirectionRowInner directionTree={directionTree} {...p} />;
  return <StoreSidebarDirectionRow {...p} />;
}

function StoreSidebarDirectionRow(p: Omit<Props, "directionTree">) {
  const directionTree = useDirectionTree();
  return <SidebarDirectionRowInner directionTree={directionTree ?? null} {...p} />;
}

function SidebarDirectionRowInner({ id, row, directionTree, agentSessions, ...p }: Omit<Props, "directionTree"> & { directionTree: DirectionTree | null }) {
  const node = useMemo(() => directionTree?.directions.find((d) => d.id === id && !d.secretary), [directionTree, id]);
  if (!node) return <>{row}</>;
  return <DirectionTasks key={id} id={id} row={row} node={node} agentSessions={agentSessions} {...p} />;
}

export type SidebarDirectionsSectionProps = {
  directions: readonly SidebarDirectionEntry[];
  label?: string;
  nameOf: (entry: SidebarDirectionEntry) => string;
  renderRow: (entry: SidebarDirectionEntry) => React.ReactNode;
  reg: RegFn;
  hover: (k: string | null) => void;
  go: GoFn;
  onNavigate?: () => void;
  /** Optional controlled direction tree for reusable shell hosts. */
  directionTree?: DirectionTree | null;
  /** Optional controlled agent sessions for reusable shell hosts. */
  agentSessions?: readonly Session[];
};

/** Controlled pinned-directions section reusable across default sidebar and web_shells hosts. */
export function SidebarDirectionsSection({
  directions,
  label = t("sidebar.directions"),
  nameOf,
  renderRow,
  reg,
  hover,
  go,
  onNavigate,
  directionTree,
  agentSessions,
}: SidebarDirectionsSectionProps) {
  if (directions.length === 0) return null;
  return (
    <>
      <SubHeading label={label} />
      <div className="sidebar-indent">
        {directions.map((d) => (
          <SidebarDirectionRow
            key={d.id}
            id={d.id}
            name={nameOf(d)}
            row={renderRow(d)}
            reg={reg}
            hover={hover}
            go={go}
            onNavigate={onNavigate}
            directionTree={directionTree}
            agentSessions={agentSessions}
          />
        ))}
      </div>
    </>
  );
}

type DirectionTasksProps = Omit<Props, "directionTree" | "agentSessions"> & { node: Direction };

const DirectionTasks = memo(function DirectionTasks({ agentSessions, ...p }: DirectionTasksProps & { agentSessions?: readonly Session[] }) {
  if (agentSessions !== undefined) return <DirectionTasksView agentSessions={agentSessions} {...p} />;
  return <StoreDirectionTasks {...p} />;
});

function StoreDirectionTasks(p: DirectionTasksProps) {
  const agentSessions = useAgentSessions();
  return <DirectionTasksView agentSessions={agentSessions} {...p} />;
}

const DirectionTasksView = memo(function DirectionTasksView({ id, name, row, node, reg, hover, go, onNavigate, agentSessions }: DirectionTasksProps & { agentSessions: readonly Session[] }) {
  // only the saved fold (or folded): an open agent under it does not unfold it. Its rows are copies of the Agents list's,
  // which owns the open row (highlight, scroll to it): these register under their own keys and are never the open one
  const [folded, toggle] = useFolded(`direction:${id}`, true);
  const todos = useMemo(() => {
    const sessions = new Map(agentSessions.map((s) => [`${s.machine_id}/${s.session_id}`, s]));
    const placed = new Set<string>();
    return (node.tasks ?? []).map((td) => directionTaskNode(td, sessions, placed, { keyPrefix: "direction:task:", folded: true, standIns: true }));
  }, [agentSessions, node]);
  const tasks = todos.length;
  // leading, as every fold in the tree under it; small, so the row keeps #4610's width for its name, with a finger's
  // 44 on a touch screen (touch-hit) from a wider box that keeps that area inside the sidebar. The label says the
  // action in words (#6214): a bare glyph alone read as a dead "fork" icon.
  const chevron = "touch-hit relative z-20 -mr-1 flex size-4 shrink-0 items-center justify-center rounded-chip text-ink-3 transition-colors hover:bg-hover hover:text-ink-2 [@media(pointer:coarse)]:w-7";
  const foldLabel = folded ? t("sidebar.direction_tasks_expand", { name }) : t("sidebar.direction_tasks_collapse", { name });
  return (
    <div data-direction={id}>
      <div className="flex items-center">
        {tasks > 0 ? (
          <button type="button" aria-expanded={!folded} aria-label={foldLabel} title={foldLabel} onClick={toggle} className={chevron}>
            <Icon name="chevron" size={10} stroke={2.4} className="transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
          </button>
        ) : (
          // nothing to open: the name stays in the column of the others
          <span aria-hidden className={cx(chevron, "invisible")} />
        )}
        <div className="min-w-0 flex-1">{row}</div>
      </div>
      {tasks > 0 && (
        <Collapse open={!folded} sidebarFold>
          <div className="flex flex-col gap-px sidebar-indent" inert={folded || undefined}>
            {todos.map((td) => (
              <TreeTodoRows key={td.key} td={td} keyPrefix={`d:${id}:`} reg={reg} hover={hover} activeKey="" go={go} onNavigate={onNavigate} />
            ))}
          </div>
        </Collapse>
      )}
    </div>
  );
});
