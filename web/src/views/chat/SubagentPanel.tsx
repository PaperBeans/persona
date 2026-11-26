// Full subagent detail panel (#14323, revising #5030): renders an embedded ChatView for any subagent
// (embedded `<parent>@<agent>`, same-machine folded child, or cross-machine A2A child) in the right side panel
// (full-screen overlay on narrow viewports) while leaving the parent chat column untouched.
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { sessionAgent } from "../../lib/agent-kind";
import { errorText, pastHub, sessionPath } from "../../lib/api";
import { apiOp } from "../../lib/ops";
import { useBackgroundChat, type BackgroundChat } from "../../lib/background-panel";
import { sessionCan, sessionTitle } from "../../lib/format";
import { openFromPage } from "../../lib/handoff";
import { stopMessageFeedback } from "../../lib/message-send";
import { useSidePanelBack } from "../../lib/panel";
import { getSession, useStore } from "../../lib/store";
import { splitSubagentLink } from "../../lib/subagent-link";
import { supervisorHeld } from "../../lib/supervisor-held";
import { buildTaskIndex, subagentCardKey, subagentTitle } from "../../lib/tasks";
import type { Interaction, Item, Machine, Session } from "../../lib/types";
import { useChildRow } from "../../lib/use-child-sessions";
import { mediaMatches, NARROW } from "../../lib/viewport";
import { AgentMark } from "../../ui/AgentMark";
import { Button, IconButton, Menu, toast, useMenu, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { StatusMark } from "../../ui/StatusMark";
import { ChatView } from "./ChatView";
import { openExpanded } from "./expanded";
import {
  embeddedSubagentStatus,
  standaloneSubagentStatus,
  subagentProvenance,
  type OverviewSubagentStatus,
} from "./overview-subagents";
import { useSharedTranscript } from "./transcript-share";

const EMPTY_ITEMS: Item[] = [];

export interface SubagentPanelChatProps {
  mid: string;
  sid: string;
  embedded: true;
}

export type SubagentChildLoader = (mid: string, sid: string, signal?: AbortSignal) => Promise<Session | null | undefined>;

export interface SubagentPanelProps {
  mid: string;
  sid: string;
  parentMid?: string;
  parentSid?: string;
  call?: string;
  from?: "overview" | "card" | "link";
  onClose: () => void;
  /** Explicit child session row (`null` when absent or embedded) skips `useChildRow`, `loadChild`, and `useStore` / `getSession`. */
  child?: Session | null;
  /** Custom async child session loader replacing `useChildRow` and `useStore` when `child` is omitted. */
  loadChild?: SubagentChildLoader;
  /** Explicit parent session row (`null` for none) skips `useStore` / `getSession` for parent lookup. */
  parent?: Session | null;
  /** Explicit machine list skips `useStore((s) => s.machines)` for provenance labels. */
  machines?: Machine[];
  /** Explicit pending interaction list skips `useStore((s) => s.interactions)` for status calculation. */
  interactions?: Interaction[];
  /** Explicit parent transcript items skip `useSharedTranscript` for embedded subagent failure status. */
  parentItems?: Item[];
  /** Explicit back callback (`null` for none) skips `useSidePanelBack`. */
  onBack?: (() => void) | null;
  /** Explicit whether the parent chat contains the launching call; used with `onShowInChat`. */
  canShowInChat?: boolean;
  /** Controlled "Show in chat" handler (`null` hides the menu item); replaces default `openExpanded` and `useBackgroundChat`. */
  onShowInChat?: ((call: string) => void) | null;
  /** Controlled "Open as page" handler (`null` hides the menu item); replaces default `openFromPage`. */
  onOpenAsPage?: ((target: { mid: string; sid: string }) => void) | null;
  /** Explicit Stop availability override (still hidden when `onStop === null`). */
  canStop?: boolean;
  /** Controlled Stop handler (`null` hides Stop); replaces default `apiOp(".../interrupt")` and `stopMessageFeedback`. */
  onStop?: ((target: { mid: string; sid: string }) => Promise<void> | void) | null;
  /** Controlled notification handler replacing default `toast`. */
  onNotify?: (message: string, tone?: "ok" | "error") => void;
  /** Controlled renderer for the subagent conversation body, replacing default `<ChatView embedded />`. */
  renderChat?: (props: SubagentPanelChatProps) => ReactNode;
}

interface SubagentPanelResolvedBase extends SubagentPanelProps {
  parsed: ReturnType<typeof splitSubagentLink>;
  resolvedParentMid: string;
  resolvedParentSid: string | undefined;
}

export function SubagentPanel(props: SubagentPanelProps) {
  const parsed = splitSubagentLink(props.sid);
  const resolvedParentMid = props.parentMid ?? props.mid;
  const resolvedParentSid = props.parentSid ?? parsed?.parent;
  const sessionKey = JSON.stringify([props.mid, props.sid]);
  return (
    <SubagentPanelChildBridge
      key={sessionKey}
      {...props}
      parsed={parsed}
      resolvedParentMid={resolvedParentMid}
      resolvedParentSid={resolvedParentSid}
    />
  );
}

function SubagentPanelChildBridge(props: SubagentPanelResolvedBase) {
  if (props.child !== undefined) {
    return <SubagentPanelParentBridge {...props} resolvedChild={props.child ?? undefined} />;
  }
  if (props.loadChild) {
    return <LoadedSubagentChild {...props} loadChild={props.loadChild} />;
  }
  return <DefaultSubagentChild {...props} />;
}

function LoadedSubagentChild(props: SubagentPanelResolvedBase & { loadChild: SubagentChildLoader }) {
  const { mid, sid, loadChild } = props;
  const [loaded, setLoaded] = useState<Session | undefined>(undefined);
  const loadRef = useRef(loadChild);
  loadRef.current = loadChild;
  useEffect(() => {
    let live = true;
    const ctl = new AbortController();
    let req: Promise<Session | null | undefined>;
    try {
      req = Promise.resolve(loadRef.current(mid, sid, ctl.signal));
    } catch (err) {
      req = Promise.reject(err);
    }
    void req
      .then((row) => {
        if (!live || ctl.signal.aborted) return;
        if (!row) {
          setLoaded(undefined);
          return;
        }
        // Guard against stale or cross-session rows returned by an external loader.
        if (row.machine_id !== mid || row.session_id !== sid) return;
        setLoaded(row);
      })
      .catch(() => {});
    return () => {
      live = false;
      ctl.abort();
    };
  }, [mid, sid]);
  return <SubagentPanelParentBridge {...props} resolvedChild={loaded} />;
}

function DefaultSubagentChild(props: SubagentPanelResolvedBase) {
  const held = useChildRow(props.mid, props.sid);
  const stored = useStore(() => getSession(props.mid, props.sid));
  return <SubagentPanelParentBridge {...props} resolvedChild={held ?? stored} />;
}

interface SubagentPanelWithChild extends SubagentPanelResolvedBase {
  resolvedChild: Session | undefined;
}

function SubagentPanelParentBridge(props: SubagentPanelWithChild) {
  if (props.parent !== undefined) {
    return <SubagentPanelMachinesBridge {...props} resolvedParent={props.parent ?? undefined} />;
  }
  if (!props.resolvedParentSid) {
    return <SubagentPanelMachinesBridge {...props} resolvedParent={undefined} />;
  }
  return <DefaultSubagentParent {...props} parentSid={props.resolvedParentSid} />;
}

function DefaultSubagentParent(props: SubagentPanelWithChild & { parentSid: string }) {
  const parent = useStore(() => getSession(props.resolvedParentMid, props.parentSid));
  return <SubagentPanelMachinesBridge {...props} resolvedParent={parent} />;
}

interface SubagentPanelWithParent extends SubagentPanelWithChild {
  resolvedParent: Session | undefined;
}

function SubagentPanelMachinesBridge(props: SubagentPanelWithParent) {
  if (props.machines !== undefined) {
    return <SubagentPanelInteractionsBridge {...props} resolvedMachines={props.machines} />;
  }
  return <DefaultSubagentMachines {...props} />;
}

function DefaultSubagentMachines(props: SubagentPanelWithParent) {
  const machines = useStore((s) => s.machines);
  return <SubagentPanelInteractionsBridge {...props} resolvedMachines={machines} />;
}

interface SubagentPanelWithMachines extends SubagentPanelWithParent {
  resolvedMachines: Machine[];
}

function SubagentPanelInteractionsBridge(props: SubagentPanelWithMachines) {
  if (props.interactions !== undefined) {
    return <SubagentPanelParentItemsBridge {...props} resolvedInteractions={props.interactions} />;
  }
  return <DefaultSubagentInteractions {...props} />;
}

function DefaultSubagentInteractions(props: SubagentPanelWithMachines) {
  const interactions = useStore((s) => s.interactions);
  return <SubagentPanelParentItemsBridge {...props} resolvedInteractions={interactions} />;
}

interface SubagentPanelWithInteractions extends SubagentPanelWithMachines {
  resolvedInteractions: Interaction[];
}

function SubagentPanelParentItemsBridge(props: SubagentPanelWithInteractions) {
  if (props.parentItems !== undefined) {
    return <SubagentPanelBackBridge {...props} resolvedParentItems={props.parentItems} />;
  }
  return <DefaultSubagentParentItems {...props} />;
}

function DefaultSubagentParentItems(props: SubagentPanelWithInteractions) {
  const parentTr = useSharedTranscript(props.resolvedParentMid, props.resolvedParentSid ?? "");
  return <SubagentPanelBackBridge {...props} resolvedParentItems={parentTr?.items ?? EMPTY_ITEMS} />;
}

interface SubagentPanelWithItems extends SubagentPanelWithInteractions {
  resolvedParentItems: Item[];
}

function SubagentPanelBackBridge(props: SubagentPanelWithItems) {
  if (props.from === "overview" || props.onBack !== undefined) {
    return <SubagentPanelChatBridge {...props} resolvedBack={props.onBack ?? undefined} />;
  }
  return <DefaultSubagentBack {...props} />;
}

function DefaultSubagentBack(props: SubagentPanelWithItems) {
  const contextBack = useSidePanelBack();
  return <SubagentPanelChatBridge {...props} resolvedBack={contextBack} />;
}

interface SubagentPanelWithBack extends SubagentPanelWithItems {
  resolvedBack: (() => void) | undefined;
}

function SubagentPanelChatBridge(props: SubagentPanelWithBack) {
  if (props.onShowInChat !== undefined) {
    return <SubagentPanelView {...props} resolvedParentChat={undefined} />;
  }
  return <DefaultSubagentBackgroundChat {...props} />;
}

function DefaultSubagentBackgroundChat(props: SubagentPanelWithBack) {
  const parentChat = useBackgroundChat(props.resolvedParentMid, props.resolvedParentSid ?? "");
  return <SubagentPanelView {...props} resolvedParentChat={parentChat} />;
}

interface SubagentPanelViewProps extends SubagentPanelWithBack {
  resolvedParentChat: BackgroundChat | undefined;
}

function SubagentPanelView({
  mid,
  sid,
  call,
  from,
  onClose,
  parsed,
  resolvedParentMid,
  resolvedParentSid,
  resolvedChild: child,
  resolvedParent: parent,
  resolvedMachines: machines,
  resolvedInteractions: interactions,
  resolvedParentItems: parentItems,
  resolvedBack: contextBack,
  resolvedParentChat: parentChat,
  canShowInChat,
  onShowInChat,
  onOpenAsPage,
  canStop: controlledCanStop,
  onStop,
  onNotify,
  renderChat,
}: SubagentPanelViewProps) {
  const more = useMenu();

  const embeddedMeta = parsed
    ? parent?.subagents?.find((a) => a.id === parsed.agent || a.session_id === sid)
    : undefined;
  const parentTasks = useMemo(() => buildTaskIndex(parentItems), [parentItems]);

  const title = child
    ? sessionTitle(child)
    : embeddedMeta
      ? subagentTitle(embeddedMeta)
      : parsed?.agent || sid;

  const agent = child ? sessionAgent(child) : parent ? sessionAgent(parent) : { agent: "claude" };
  const provenance = parent ? subagentProvenance(parent, mid, agent, machines) : "";

  const status: OverviewSubagentStatus = child
    ? standaloneSubagentStatus(child, interactions)
    : embeddedMeta && resolvedParentSid
      ? embeddedSubagentStatus(mid, resolvedParentSid, embeddedMeta, interactions, parentItems, parentTasks)
      : interactions.some((ix) => !supervisorHeld(ix) && ix.machine_id === mid && (ix.session_id === sid || (parsed && ix.agent_id === parsed.agent)))
        ? "needs_you"
        : "ended";

  const active = status === "running" || status === "needs_you";
  const canStop = onStop === null
    ? false
    : controlledCanStop !== undefined
      ? controlledCanStop
      : active && Boolean(child && sessionCan(child, "interrupt"));

  const [stopping, setStopping] = useState(false);
  const mounted = useRef(true);
  const nextStopToken = useRef(0);
  const stopInFlight = useRef<number | null>(null);
  const notifyRef = useRef(onNotify);
  notifyRef.current = onNotify;
  const onStopRef = useRef(onStop);
  onStopRef.current = onStop;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      stopInFlight.current = null;
    };
  }, []);

  const stopSubagent = useCallback(async () => {
    if (stopInFlight.current !== null || !canStop) return;
    if (!onStopRef.current && (!child || !sessionCan(child, "interrupt"))) return;
    const token = ++nextStopToken.current;
    const targetMid = mid;
    const targetSid = sid;
    const notify = (message: string, tone?: "ok" | "error") => {
      if (notifyRef.current) notifyRef.current(message, tone);
      else toast(message, tone);
    };
    stopInFlight.current = token;
    setStopping(true);
    try {
      if (onStopRef.current) {
        await onStopRef.current({ mid: targetMid, sid: targetSid });
        if (!mounted.current || stopInFlight.current !== token) return;
        notify(t("chat-chatview.interrupted"));
      } else {
        const r = await apiOp<{ state?: string }>(`${sessionPath(targetMid, targetSid)}/interrupt`, {
          method: "POST",
          timeout: pastHub(90_000),
        });
        if (!mounted.current || stopInFlight.current !== token) return;
        if (r.state === "unknown") throw new Error(t("lib-new-agent.outcome_unknown"));
        notify(t("chat-chatview.interrupted"));
        stopMessageFeedback(targetMid, targetSid);
      }
    } catch (e) {
      if (!mounted.current || stopInFlight.current !== token) return;
      notify(t("chat-chatview.interrupt_failed", { error: errorText(e) }), "error");
    } finally {
      if (stopInFlight.current === token) {
        stopInFlight.current = null;
        if (mounted.current) setStopping(false);
      }
    }
  }, [canStop, child, mid, sid]);

  const resolvedCall = call ?? embeddedMeta?.tool_use_id;
  const canJumpToCall = onShowInChat === null
    ? false
    : onShowInChat
      ? Boolean(resolvedCall && (canShowInChat ?? Boolean(resolvedParentSid)))
      : Boolean(
          resolvedParentSid &&
          resolvedCall &&
          parentChat?.jumpToCall &&
          (parentChat.hasCall ? parentChat.hasCall(resolvedCall) : Boolean(embeddedMeta?.tool_use_id)),
        );

  const menuItems: MenuItem[] = [
    ...(canJumpToCall
      ? [{
          key: "show-in-chat",
          label: t("chat-sessionoverview.show_in_chat"),
          icon: "chat" as const,
          onSelect: () => {
            if (!resolvedCall) return;
            if (onShowInChat) {
              onShowInChat(resolvedCall);
              return;
            }
            if (!resolvedParentSid) return;
            const scope = `${resolvedParentMid}/${resolvedParentSid}:`;
            openExpanded([subagentCardKey(scope, resolvedCall)]);
            parentChat?.jumpToCall?.(resolvedCall);
            if (mediaMatches(NARROW)) onClose();
          },
        }]
      : []),
    ...(onOpenAsPage !== null
      ? [{
          key: "open-as-page",
          label: t("chat-sessionoverview.open_as_page"),
          icon: "external" as const,
          onSelect: () => {
            if (onOpenAsPage) onOpenAsPage({ mid, sid });
            else openFromPage({ name: "session", mid, sid, standalone: true });
            onClose();
          },
        }]
      : []),
  ];

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-subagent-panel={`${mid}/${sid}`}>
      <header className="flex shrink-0 flex-col gap-1.5 border-b border-line px-3 py-2">
        <div className="flex min-w-0 items-center gap-1.5">
          {from === "overview" ? (
            <button
              type="button"
              data-subagent-back
              onClick={onClose}
              className="touch-hit -ml-1.5 inline-flex shrink-0 items-center gap-0.5 rounded-control px-1.5 py-1 text-xs font-medium whitespace-nowrap text-ink-2 hover:bg-hover hover:text-ink"
            >
              <Icon name="chevronLeft" size={14} />
              <span>{t("chat-chatview.session_overview")}</span>
            </button>
          ) : contextBack ? (
            <IconButton icon="arrowLeft" label={t("sidepanel.back")} onClick={contextBack} />
          ) : null}
          <span className="min-w-0 flex-1" />
          {canStop && (
            <Button
              tone="danger"
              size="sm"
              icon="square"
              busy={stopping}
              onClick={() => void stopSubagent()}
              className="whitespace-nowrap"
            >
              {t("chat-sessionoverview.stop")}
            </Button>
          )}
          {menuItems.length > 0 && (
            <IconButton
              icon="more"
              label={t("chat-chatview.more_actions")}
              onClick={more.open}
              active={Boolean(more.anchor)}
            />
          )}
          {more.anchor && menuItems.length > 0 && <Menu anchor={more.anchor} onClose={more.close} align="right" items={menuItems} />}
          <IconButton icon="x" label={t("sidepanel.close")} onClick={onClose} />
        </div>
        <div className="flex min-w-0 items-center gap-2">
          <span className="inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-field text-ink-2">
            <AgentMark agent={agent.agent} label={agent.label} size={13} decorative />
          </span>
          <h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-ink" title={title}>
            {title}
          </h2>
          {provenance && (
            <span
              data-subagent-provenance
              title={provenance}
              className="inline-flex max-w-36 shrink-0 items-center truncate rounded-full bg-field px-1.5 py-0.5 text-submeta whitespace-nowrap text-ink-3"
            >
              {provenance}
            </span>
          )}
          <span
            data-subagent-panel-status={status}
            className="flex shrink-0 items-center gap-1 text-xs whitespace-nowrap"
          >
            {status === "needs_you" ? (
              <>
                <StatusMark state="needs_you" size="sm" label={false} />
                <span className="font-medium text-attention">{t("chat-sessionoverview.status_needs_you")}</span>
              </>
            ) : status === "running" ? (
              <>
                <StatusMark state="working" size="sm" label={false} />
                <span className="text-ink-2">{t("chat-subagentcard.status_running")}</span>
              </>
            ) : status === "failed" ? (
              <span className="font-medium text-red">{t("chat-sessionoverview.status_failed")}</span>
            ) : (
              <span className="text-ink-3">{t("chat-sessionoverview.ended")}</span>
            )}
          </span>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col">
        {renderChat ? (
          renderChat({ mid, sid, embedded: true })
        ) : (
          <ChatView key={`${mid}/${sid}`} mid={mid} sid={sid} embedded />
        )}
      </div>
    </div>
  );
}
