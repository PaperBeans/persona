// The bar above the composer while an Agent works, as cindy's RunningStatusBar (#1963; the owner asked on 2026-11-12
// that it follow cindy, #5312). Left, a short state: in an Agent session cindy's words for what the turn is doing
// ("Thinking…", "Editing files…", "Bash running…", "Spawning a subagent…", "Compacting context…"), worked out from its
// status line and its newest steps (lib/run-step.ts); in a Persona conversation cindy's plain phases ("Thinking…",
// "Replying…", "Reading files…"), for everyday people (super rules 0 and 4). Right, how long the turn has run, in the
// work summary's format, then in an Agent session either the latest speed ("42 tok/s", lib/token-rate.ts), a button
// whose card shows the speed history, or the tokens written so far ("↓ 1.2k tok"); never both. It breathes once each
// time its words or count move (output, not just time passing); when the turn finishes it says ✓ Done for a second (a
// turn waiting on you, stopped or cut off by an error keeps its last words), then fades out. Without a status line (another agent, an older hub) it counts from when the turn started.
// Once the turn is over and a workflow keeps the agent working (#3434, `background_working`), it says what runs:
// "Background workflow · Verify · 169/185+", timed from the workflow's start (the turn's own time read as if the agent
// were still at it). The count stays whole on a narrow screen: the words before it give way first. An agent whose
// background commands only wait (#6045, `background_waiting`) gets a still line instead: what it waits on (the header
// says the state, #3956), timed from when it started waiting, with no breath and no tokens. A subagent still running in
// the background after the turn (#5030, cindy's background subtasks) gets cindy's Activity mark and "Background subtasks
// running, still consuming model usage…" with no time and no tokens (the turn's would be left over); cindy's "Stop all"
// waits for a node that can stop a subagent (#5044). With the hub out of reach the bar says so in front of the last
// state it knew, its clock stands still (nothing new can have arrived) and it shows no speed.
import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import { useSessionLive } from "../../lib/live";
import type { RunStep } from "../../lib/run-step";
import { useStore } from "../../lib/store";
import { compactTokens } from "../../lib/tokens";
import { toolDisplayName } from "../../lib/turns";
import type { SessionLive } from "../../lib/types";
import { TICKING_DURATION, workDuration } from "../../lib/work-groups";
import { workflowCounts, type BackgroundLine, type WaitLine } from "../../lib/workflows";
import { stateName } from "../../lib/state-labels";
import { useNow } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { FADE_OUT_MS, LINGER_MS, useBreath, useCountUp } from "../../ui/motion";
import { Rolling } from "../../ui/Rolling";
import "../../ui/stream/stream.css";
import { t } from "../../i18n";

// The speed (its history, the button and its card) loads once an Agent's turn runs, off the chat's first screen: a
// speed shows only once one is measured, seconds in, and until then the time and the tokens stand as they are.
type SpeedType = typeof import("./RunningSpeedCard").RunningSpeed;
let Speed: SpeedType | undefined;
const loadSpeed = () => import("./RunningSpeedCard").then((m) => (Speed = m.RunningSpeed));

function useSpeed(want: boolean): SpeedType | undefined {
  const [, loaded] = useState(0);
  useEffect(() => {
    // a chunk that fails to load leaves the time and the tokens; the next turn tries again
    if (want && !Speed) loadSpeed().then(() => loaded((n) => n + 1), () => {});
  }, [want]);
  return Speed;
}

/** The tick in the bar's time ("1m 5s"): the work summary's format with cindy's status bar options. */
const elapsedText = (seconds: number) => workDuration(seconds * 1000, TICKING_DURATION);

const EDIT_TOOLS = new Set(["Edit", "Write", "MultiEdit", "NotebookEdit", "apply_patch"]);
const READ_TOOLS = new Set(["Read", "NotebookRead"]);
const SEARCH_TOOLS = new Set(["Grep", "Glob", "LS"]);
const START: RunStep = { kind: "start" };

/** The left side's words, and whether they say the context is being compacted (cindy's Layers mark). `plain`: the
 *  Persona conversation's everyday words, which never name a tool. */
export function statusWords(live: SessionLive | null, step: RunStep = START, plain = false): { text: string; compacting: boolean } {
  if (live?.verb && /^compact/i.test(live.verb)) return { text: plain ? t("chat-runningstatusbar.organizing") : t("chat-runningstatusbar.compacting"), compacting: true };
  return { text: live?.thinking ? t("chat-runningstatusbar.thinking") : (plain ? plainWord : agentWord)(step), compacting: false };
}

function agentWord(step: RunStep): string {
  switch (step.kind) {
    case "streaming":
    case "text":
      return t("chat-runningstatusbar.generating");
    case "after":
      return t("chat-runningstatusbar.working");
    case "tool": {
      const tool = step.tool ?? "";
      if (EDIT_TOOLS.has(tool)) return t("chat-runningstatusbar.editing_files");
      if (tool === "WebSearch") return t("chat-runningstatusbar.searching_web");
      if (step.spawning) return t("chat-runningstatusbar.spawning_agent");
      return t("chat-runningstatusbar.running_tool", { tool: toolDisplayName(tool) });
    }
    default:
      return t("chat-runningstatusbar.thinking");
  }
}

/** cindy's plain phases (publicToolPhase): only what everyone can follow, nothing of the tool or its target. */
function plainWord(step: RunStep): string {
  switch (step.kind) {
    case "streaming":
      return t("chat-runningstatusbar.replying");
    case "text":
    case "after":
      return t("chat-runningstatusbar.processing");
    case "tool": {
      const tool = step.tool ?? "";
      if (READ_TOOLS.has(tool)) return t("chat-runningstatusbar.reading_files");
      if (EDIT_TOOLS.has(tool)) return t("chat-runningstatusbar.updating_files");
      if (SEARCH_TOOLS.has(tool)) return t("chat-runningstatusbar.searching_files");
      if (tool === "WebSearch") return t("chat-runningstatusbar.searching_web");
      if (tool === "WebFetch") return t("chat-runningstatusbar.reading_web");
      return t("chat-runningstatusbar.processing");
    }
    default:
      return t("chat-runningstatusbar.thinking");
  }
}

export interface RunningStatusProps {
  working: boolean;
  /** Once it is not working: the turn finished (ChatView turnDone), so the bar says ✓ Done; otherwise its last words. */
  done?: boolean;
  startedAt?: string;
  /** No live work line ticks in the chat: only an older hub's guess at a background workflow reads it (inBackground). */
  clock?: boolean;
  /** The Persona conversation: plain words and the time, no tokens and no speed. */
  plain?: boolean;
  /** What the turn is doing, from its newest steps (ChatView, lib/run-step.ts). */
  step?: RunStep;
  background?: BackgroundLine | null;
  subtasks?: boolean;
}

export function RunningStatusBar({ mid, sid, wait, ...props }: RunningStatusProps & { mid: string; sid: string; wait?: WaitLine | null }) {
  const live = useSessionLive(mid, sid);
  const offline = useStore((s) => s.conn !== "online");
  if (wait && props.working) return <WaitingView wait={wait} offline={offline} />;
  return <RunningStatusView {...props} live={live} offline={offline} speedKey={`${mid}/${sid}`} />;
}

/** In front of the last state the bar knows, while the hub is out of reach. */
function Disconnected() {
  return <span className="shrink-0 text-ink-3" data-running-offline>{t("chat-runningstatusbar.disconnected")} ·</span>;
}

/** The bar of an agent that only waits in the background (#6045): still, in the waiting color, timed from when it began. */
export function WaitingView({ wait, offline }: { wait: WaitLine; offline?: boolean }) {
  const now = useNow(1000, !offline);
  const since = wait.since ? Date.parse(wait.since) : NaN;
  // what it waits on: the header's title line says the state ("Waiting in the background"), once (#3956); the state's name
  // only stands in when the wait has no words of its own
  const words = [wait.what || stateName("bg_waiting"), wait.more > 0 ? t("chat-runningstatusbar.more_waits", { count: wait.more }) : ""].filter(Boolean).join(" · ");
  return (
    <div data-running-status="on" data-running-background="waiting" className="flex select-none items-center justify-between gap-3 px-2 py-1.5 text-[13px] font-medium">
      <div className="flex min-w-0 items-center gap-1.5 text-status-waiting">
        <Icon name="clock" size={14} className="shrink-0 -translate-y-px" />
        {offline && <Disconnected />}
        <span className="truncate" data-running-verb>{words}</span>
      </div>
      {Number.isFinite(since) && <span className="shrink-0 whitespace-nowrap text-ink-3 tabular-nums" data-running-elapsed>{elapsedText(Math.max(0, Math.floor((now - since) / 1000)))}</span>}
    </div>
  );
}

/** Whether the bar speaks of the background workflow: the hub says the turn is over, or (a hub from before
 *  background_working) a workflow runs and no turn of the agent's own shows: no status line, no live work line
 *  ticking in the chat (`clock`) and no call it waits on (a subagent launched in the foreground, say). */
function inBackground(background: BackgroundLine | null | undefined, live: SessionLive | null, clock: boolean, step: RunStep | undefined): background is BackgroundLine {
  return Boolean(background && (!background.guessed || (clock && !(live?.verb || live?.thinking) && step?.kind !== "tool")));
}

/** The words for it: "Background workflow · Verify · 169/185+ · 1 more". Only the words before the count shrink (the
 *  name for it first, then the phase), so a narrow bar keeps the count and the other runs whole. */
function BackgroundWords({ line }: { line: BackgroundLine }) {
  const w = line.run;
  return (
    <span className="flex min-w-0 items-center whitespace-pre tabular-nums" data-running-verb>
      <span className="min-w-0 truncate">{t("chat-runningstatusbar.background_workflow")}</span>
      {/* not `truncate`: its nowrap would drop the space the piece starts with */}
      {w.phase && <span className="max-w-[40%] shrink-0 overflow-hidden text-ellipsis">{` · ${w.phase}`}</span>}
      <span className="shrink-0" data-running-counts>
        {" · "}
        <Rolling value={workflowCounts(w)} />
      </span>
      {line.more > 0 && <span className="shrink-0" data-running-more>{` · ${t("chat-runningstatusbar.more_workflows", { count: line.more })}`}</span>}
    </span>
  );
}

type Phase = "on" | "linger" | "fading" | "off";

/** The bar for a given status line (RunningStatusBar subscribes; tests render this). `speedKey`: the conversation its
 *  speed history is kept for while the page is open. */
export function RunningStatusView({ live, working, done: finished, startedAt, clock = true, plain, step, background, subtasks, offline, speedKey = "" }: RunningStatusProps & { live: SessionLive | null; offline?: boolean; speedKey?: string }) {
  const phase = usePhase(working);
  const [pinned, setPinned] = useState(false);
  // after the turn: what it last said, while it lingers and fades. Its status line goes a moment before the turn ends,
  // so the last one of this turn stays for that (not one from a turn before)
  const last = useRef({ live, background, clock, subtasks, step });
  const wasWorking = useRef(working);
  if (working) last.current = { live: live ?? (wasWorking.current ? last.current.live : null), background, clock, subtasks, step };
  wasWorking.current = working;
  const kept = working ? { live, background, clock, subtasks, step } : last.current;
  const shown = kept.live;
  const bg = kept.background;
  const behind = inBackground(bg, shown, kept.clock, kept.step);
  // the turn is over and background subagents run on: neither the turn's time nor its tokens say anything of them
  const usage = Boolean(kept.subtasks);
  // the turn's own work, measured: an Agent's, not the Persona conversation's, not background work's
  const own = !plain && !behind && !usage;
  const now = useNow(1000, phase === "on" && !offline && !usage);
  // from the run's start; a run that does not say when it started, from the turn's (not from when the bar appeared).
  // After the turn it stays as it last was (cindy): the turn's start and its status line are gone by then.
  const counted = useElapsed(behind ? null : shown, behind ? (bg.since ?? startedAt) : startedAt, now);
  const lastElapsed = useRef(counted);
  if (working) lastElapsed.current = counted;
  const elapsed = working ? counted : lastElapsed.current;
  const tokens = useCountUp(shown?.tokens_down ?? 0);
  const done = !working && Boolean(finished) && !behind && !usage;
  const state = statusWords(shown, kept.step, plain);
  const words = behind ? "" : usage ? t("chat-runningstatusbar.background_subtasks") : done ? t("chat-runningstatusbar.done") : state.text;
  const compacting = !behind && !usage && !done && state.compacting;
  const breath = useBreath(useMoves(`${words}\n${shown?.tokens_down ?? ""}`), phase === "on");
  const onPinned = useCallback((p: boolean) => setPinned(p), []);
  const SpeedView = useSpeed(working && own);
  // a card pinned open keeps its button through the end of the turn and after it (cindy)
  if (phase === "off" && !pinned) return null;
  const hidden = phase === "off";
  // each side fades on its own: a pinned card's button stays as it is (cindy)
  const fade: CSSProperties | undefined = hidden ? undefined : { opacity: phase === "fading" ? 0 : 1, transition: `opacity ${FADE_OUT_MS}ms ease-out` };
  const icon: IconName = behind ? "workflow" : usage ? "activity" : done ? "check" : compacting ? "layers" : "sparkles";
  const count = own && shown?.tokens_down ? (
    <>
      <Icon name="arrowDown" size={13} className="shrink-0" />
      <span data-running-tokens>{t("chat-runningstatusbar.token_count", { tokens: compactTokens(tokens) })}</span>
    </>
  ) : null;
  const time = <span data-running-elapsed>{elapsedText(elapsed)}</span>;
  return (
    <div
      data-running-status={phase}
      data-running-background={behind ? "workflow" : usage ? "subtasks" : undefined}
      className="flex select-none items-center justify-between gap-3 px-2 py-1.5 text-[13px] font-medium"
    >
      <div
        key={done ? "done" : breath.cycle}
        onAnimationEnd={done ? undefined : breath.ended}
        aria-hidden={hidden || undefined}
        className={`flex min-w-0 items-center gap-1.5 ${behind ? "text-ai" : "text-status-accent"} ${hidden ? "invisible" : done ? "status-done" : breath.cycle ? "status-breath" : ""}`}
        data-running-done={done || undefined}
        style={fade}
      >
        <Icon name={icon} size={14} stroke={done ? 2.5 : undefined} className={`shrink-0 ${done ? "" : "-translate-y-px"}`} />
        {offline && <Disconnected />}
        {behind ? <BackgroundWords line={bg} /> : <span className="truncate" data-running-verb>{words}</span>}
      </div>
      <div className="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-ink-3 tabular-nums" style={pinned ? undefined : fade}>
        {usage ? null : own && SpeedView ? (
          <SpeedView speedKey={speedKey} turn={startedAt} live={shown} working={working} paused={kept.step?.kind === "tool"} offline={offline} now={now} elapsed={elapsedText(elapsed)} time={time} count={count} onPinned={onPinned} />
        ) : (
          <>
            {time}
            {count && <span aria-hidden>·</span>}
            {count}
          </>
        )}
      </div>
    </div>
  );
}

/** A number that goes up each time `key` changes: what the bar breathes on (cindy: its words or its count moved). */
function useMoves(key: string): number {
  const moves = useRef({ key, n: 0 });
  if (moves.current.key !== key) moves.current = { key, n: moves.current.n + 1 };
  return moves.current.n;
}

/** on while working; after it, a second as it was, then the fade, then gone. */
function usePhase(working: boolean): Phase {
  const [phase, setPhase] = useState<Phase>(working ? "on" : "off");
  useEffect(() => {
    if (working) {
      setPhase("on");
      return;
    }
    if (phase === "off") return;
    const a = window.setTimeout(() => setPhase("fading"), LINGER_MS);
    const b = window.setTimeout(() => setPhase("off"), LINGER_MS + FADE_OUT_MS);
    return () => {
      clearTimeout(a);
      clearTimeout(b);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [working]);
  return working ? "on" : phase === "on" ? "linger" : phase;
}

/**
 * Seconds the turn has run: the status line's count, going on locally between its updates (about one a second);
 * without one, from `startedAt` (or from when the bar appeared).
 */
function useElapsed(live: SessionLive | null, startedAt: string | undefined, now: number): number {
  const base = useRef<{ s: number; at: number } | null>(null);
  const mounted = useRef(Date.now());
  if (live?.elapsed_s === undefined) base.current = null;
  else if (base.current?.s !== live.elapsed_s) base.current = { s: live.elapsed_s, at: Date.now() };
  if (base.current) return base.current.s + Math.max(0, Math.floor((now - base.current.at) / 1000));
  const from = startedAt ? Date.parse(startedAt) : NaN;
  return Math.max(0, Math.floor((now - (Number.isNaN(from) ? mounted.current : from)) / 1000));
}
