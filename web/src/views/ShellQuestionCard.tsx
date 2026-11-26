// The question a New Agent start's terminal tab stopped on, answered from here (#10365, lib/start-continue.ts): the
// question as the computer shows it, one button per answer it offers, until when the tab waits, and its last lines
// folded. An Agents-surface card in plain words, laid out as the design system's notice (ErrorNotice) with the
// terminal's mark instead of the red one. ShellQuestionCard takes how to send an answer; SessionShellQuestion is it
// for a session's own start, with the refused messages sent again after, TaskShellQuestion for a planned task's failed
// start (the hub starts the task again in that tab); the default export places them in the chat, on a New Agent
// launch's page and on a task's failed row, panel and to-do page, which load this file only for such a start.
import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { errorText } from "../lib/api";
import { clock } from "../lib/format";
import { answerTaskStart, continueStart, openQuestion, resendRefused, shellQuestion, SHELL_WAITING, START_CONTINUE_FEATURE, TODO_START_ANSWER_FEATURE, type ShellQuestion } from "../lib/start-continue";
import { hubHas, useStore } from "../lib/store";
import type { PlanTask } from "../lib/feed";
import type { AgentLaunch } from "../lib/new-agent";
import type { Todo } from "../lib/todos";
import type { Session } from "../lib/types";
import { Button, useNow } from "../ui/atoms";
import { ErrorDetailsText, ErrorNotice } from "../ui/ErrorNotice";
import { Icon } from "../ui/icons";

export interface ShellQuestionCardDeps {
  /** Format a submission error for display (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Fixed timestamp or clock reader (ms) for `keptUntil` checks (disables `useNow` polling when supplied). */
  now?: number | (() => number);
}

export interface ShellQuestionDeps extends ShellQuestionCardDeps {
  /** Send the chosen answer for a session start (defaults to `start-continue.continueStart`). */
  continueStart?: (mid: string, sid: string, choice: string) => Promise<unknown>;
  /** Resend refused messages after a session start answer succeeds (defaults to `start-continue.resendRefused`). */
  resendRefused?: (mid: string, sid: string) => unknown;
  /** Send the chosen answer for a planned task's failed start (defaults to `start-continue.answerTaskStart`). */
  answerTaskStart?: (todo: Pick<Todo, "id">, choice: string) => Promise<unknown>;
  /** Hub feature flags (bypasses `useStore(s => s.features)` when supplied). */
  features?: readonly string[];
  /** Controlled capability predicate (bypasses `useStore(s => s.features)` when supplied). */
  hasFeature?: (feature: string) => boolean;
  /** Explicit override for `START_CONTINUE_FEATURE` (bypasses `useStore` when supplied). */
  canContinueStart?: boolean;
  /** Explicit override for `TODO_START_ANSWER_FEATURE` (bypasses `useStore` when supplied). */
  canAnswerTaskStart?: boolean;
  /** Controlled live session row for a launch (`null` for absent; bypasses `useStore` when supplied). */
  sessionRow?: Session | null;
  /** Read the live session row for a launch (bypasses `useStore` when supplied). */
  getSession?: (mid: string, sid: string) => Session | undefined;
  /** Subscribe to external session updates when `getSession` is supplied. */
  subscribe?: (listener: () => void) => () => void;
}

export interface ShellQuestionCardViewProps {
  question: ShellQuestion;
  busyChoice?: string;
  error?: string;
  onPick: (choice: string) => void;
  className?: string;
  /** React key on the inner `ErrorNotice` so folded screen details reset on scope/question switches. */
  noticeKey?: string | number;
}

/** The words on an answer's button: what it does where the question says, with the key it presses. */
export function choiceLabel(q: Pick<ShellQuestion, "kind" | "default">, id: string): string {
  switch (id) {
    case "enter":
      return q.default === "y" ? t("shellquestioncard.enter_yes") : q.default === "n" ? t("shellquestioncard.enter_no") : t("shellquestioncard.enter");
    case "cancel":
      return t("shellquestioncard.cancel");
    case "yes":
      return t("shellquestioncard.yes");
    case "no":
      return t("shellquestioncard.no");
  }
  const key = { key: id };
  switch (`${q.kind}:${id}`) {
    case "update:y":
      return t("shellquestioncard.update_yes", key);
    case "update:n":
      return t("shellquestioncard.update_no", key);
    case "compinit:y":
      return t("shellquestioncard.compinit_yes", key);
    case "compinit:n":
      return t("shellquestioncard.compinit_no", key);
    case "zsh_newuser:q":
      return t("shellquestioncard.newuser_quit", key);
    case "zsh_newuser:0":
      return t("shellquestioncard.newuser_empty", key);
    case "p10k_wizard:q":
      return t("shellquestioncard.p10k_quit", key);
    case "rvm_trust:n":
      return t("shellquestioncard.rvm_no", key);
    case "rvm_trust:c":
      return t("shellquestioncard.rvm_cancel", key);
  }
  if (id === "y") return t("shellquestioncard.key_yes", key);
  if (id === "n") return t("shellquestioncard.key_no", key);
  return t("shellquestioncard.key", key);
}

/** What helps, for the kind of question. */
function hint(q: ShellQuestion): string {
  if (q.secret) return t("shellquestioncard.hint_secret");
  if (q.kind === "ssh_host_key") return t("shellquestioncard.hint_ssh");
  return t("shellquestioncard.hint");
}

function questionScopeKey(q: ShellQuestion, scopeKey?: string): string {
  return JSON.stringify([scopeKey ?? null, q.line, q.kind, q.choices, q.lineMode, q.secret, q.default ?? null, q.keptUntil ?? null, q.screen ?? null]);
}

function controlledFeature(deps: ShellQuestionDeps | undefined, feature: string, explicit?: boolean): boolean | undefined {
  if (explicit !== undefined) return explicit;
  if (deps?.hasFeature) return deps.hasFeature(feature);
  if (deps?.features !== undefined) return deps.features.includes(feature);
  return undefined;
}

/** Pure presentational view for a shell startup question card. */
export function ShellQuestionCardView({ question, busyChoice, error, onPick, className, noticeKey }: ShellQuestionCardViewProps) {
  const until = clock(question.keptUntil);
  return (
    <ErrorNotice
      key={noticeKey}
      data-testid="shell-question"
      aria-label={t("shellquestioncard.label")}
      className={className}
      icon={<span className="text-ink-2"><Icon name="terminal" size={16} /></span>}
      message={t("shellquestioncard.asking", { line: question.line })}
      description={
        <>
          {hint(question)}
          {until && <> {t("shellquestioncard.kept_until", { time: until })}</>}
          {error && <span role="alert" className="mt-1 block text-red">{error}</span>}
        </>
      }
      actions={question.choices.map((c) => (
        <Button key={c} size="sm" className="touch-hit" busy={busyChoice === c} disabled={busyChoice !== undefined && busyChoice !== c} testId={`shell-choice-${c}`} onClick={() => onPick(c)}>
          {choiceLabel(question, c)}
        </Button>
      ))}
      details={question.screen ? <ErrorDetailsText text={question.screen} /> : undefined}
    />
  );
}

export function ShellQuestionCard({ question, answer, onAnswered, className, scopeKey, deps }: {
  question: ShellQuestion;
  /** Sends one of question.choices; throws with the refusal when it was not taken. */
  answer: (choice: string) => Promise<unknown>;
  /** The answer was taken: the start goes on in that tab. */
  onAnswered?: (choice: string) => void;
  className?: string;
  /** Optional identity scope (e.g. session or task id) that resets local state and fences stale answers on change. */
  scopeKey?: string;
  deps?: ShellQuestionCardDeps;
}) {
  const fixedNow = deps?.now;
  const tick = useNow(30_000, fixedNow === undefined);
  const now = fixedNow === undefined ? tick : typeof fixedNow === "function" ? fixedNow() : fixedNow;
  const fullScope = questionScopeKey(question, scopeKey);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: fullScope, epoch: 1 }));
  const [busyState, setBusyState] = useState<{ epoch: number; choice: string } | null>(null);
  const [errorState, setErrorState] = useState<{ epoch: number; message: string } | null>(null);
  if (scope.key !== fullScope) {
    setScope({ key: fullScope, epoch: scope.epoch + 1 });
    setBusyState(null);
    setErrorState(null);
  }
  const activeEpoch = useRef(0);
  const requestSeq = useRef(0);
  const pending = useRef<{ epoch: number; seq: number } | null>(null);
  const onAnsweredRef = useRef(onAnswered);
  useLayoutEffect(() => { onAnsweredRef.current = onAnswered; }, [onAnswered]);
  useLayoutEffect(() => {
    activeEpoch.current = scope.epoch;
    return () => {
      activeEpoch.current = 0;
      if (pending.current?.epoch === scope.epoch) pending.current = null;
    };
  }, [scope.epoch]);

  const q = openQuestion(question, now);
  if (!q) return null;

  const busyChoice = busyState?.epoch === scope.epoch ? busyState.choice : undefined;
  const error = errorState?.epoch === scope.epoch ? errorState.message : undefined;

  const pick = async (choice: string) => {
    const runEpoch = scope.epoch;
    if (pending.current?.epoch === runEpoch || activeEpoch.current !== runEpoch) return;
    const runSeq = ++requestSeq.current;
    pending.current = { epoch: runEpoch, seq: runSeq };
    const isCurrent = () => activeEpoch.current === runEpoch && pending.current?.seq === runSeq;
    setBusyState({ epoch: runEpoch, choice });
    setErrorState(null);
    const formatError = deps?.formatError ?? errorText;
    let succeeded = false;
    try {
      await answer(choice);
      if (!isCurrent()) return;
      succeeded = true;
    } catch (e) {
      if (!isCurrent()) return;
      setErrorState({ epoch: runEpoch, message: formatError(e) });
    } finally {
      if (pending.current?.seq === runSeq) pending.current = null;
      if (activeEpoch.current === runEpoch) setBusyState((cur) => (cur?.epoch === runEpoch ? null : cur));
    }
    if (succeeded) onAnsweredRef.current?.(choice);
  };

  return <ShellQuestionCardView question={q} busyChoice={busyChoice} error={error} onPick={(c) => void pick(c)} className={className} noticeKey={scope.epoch} />;
}

export interface SessionShellQuestionProps {
  mid: string;
  sid: string;
  args: Record<string, unknown> | undefined;
  onAnswered?: () => void;
  className?: string;
  deps?: ShellQuestionDeps;
}

/** The card for session sid's own start on computer mid, from its start_error_args (or a refused message's details):
 *  an answer starts it again in that tab, and the messages that start refused are sent again. Nothing on a hub that
 *  cannot take the answer. */
export function SessionShellQuestion(props: SessionShellQuestionProps) {
  const controlled = controlledFeature(props.deps, START_CONTINUE_FEATURE, props.deps?.canContinueStart);
  if (controlled !== undefined) return <SessionShellQuestionBody {...props} can={controlled} />;
  return <DefaultSessionShellQuestion {...props} />;
}

function DefaultSessionShellQuestion(props: SessionShellQuestionProps) {
  const can = useStore((s) => hubHas(s.features, START_CONTINUE_FEATURE));
  return <SessionShellQuestionBody {...props} can={can} />;
}

function SessionShellQuestionBody({ mid, sid, args, onAnswered, className, deps, can }: SessionShellQuestionProps & { can: boolean }) {
  const sessionId = JSON.stringify([mid, sid]);
  const question = shellQuestion(args);
  const targetKey = question ? questionScopeKey(question, sessionId) : null;
  const [target, setTarget] = useState<{ sessionId: string; key: string | null; epoch: number }>(() => ({ sessionId, key: targetKey, epoch: 1 }));
  if (target.sessionId !== sessionId || target.key !== targetKey) {
    const sameSessionHide = target.sessionId === sessionId && targetKey === null;
    setTarget({ sessionId, key: targetKey, epoch: sameSessionHide ? target.epoch : target.epoch + 1 });
  }
  const targetEpochRef = useRef(target.epoch);
  const runResendRef = useRef(deps?.resendRefused ?? resendRefused);
  useLayoutEffect(() => { targetEpochRef.current = target.epoch; }, [target.epoch]);
  useLayoutEffect(() => { runResendRef.current = deps?.resendRefused ?? resendRefused; }, [deps?.resendRefused]);
  if (!can || !question) return null;
  const runContinue = deps?.continueStart ?? continueStart;
  const runEpoch = target.epoch;
  return (
    <ShellQuestionCard
      scopeKey={JSON.stringify(["session", mid, sid])}
      question={question}
      className={className}
      deps={deps}
      answer={async (choice) => {
        const result = await runContinue(mid, sid, choice);
        if (targetEpochRef.current === runEpoch) {
          try { void Promise.resolve(runResendRef.current(mid, sid)).catch(() => {}); } catch { /* resend failure stays non-blocking */ }
        }
        return result;
      }}
      onAnswered={onAnswered ? () => onAnswered() : undefined}
    />
  );
}

export interface TaskShellQuestionProps {
  todo: Todo;
  task: PlanTask;
  className?: string;
  onAnswered?: (choice: string) => void;
  deps?: ShellQuestionDeps;
}

/** The card for planned task `todo`'s failed start (`task`, its plan task: error_code shell_waiting, the question in
 *  error_args): an answer starts the task again in that tab, the to-do reopened. Nothing on a hub that cannot take it,
 *  where the task's Retry stays its way on. */
export function TaskShellQuestion(props: TaskShellQuestionProps) {
  const controlled = controlledFeature(props.deps, TODO_START_ANSWER_FEATURE, props.deps?.canAnswerTaskStart);
  if (controlled !== undefined) return <TaskShellQuestionBody {...props} can={controlled} />;
  return <DefaultTaskShellQuestion {...props} />;
}

function DefaultTaskShellQuestion(props: TaskShellQuestionProps) {
  const can = useStore((s) => hubHas(s.features, TODO_START_ANSWER_FEATURE));
  return <TaskShellQuestionBody {...props} can={can} />;
}

function TaskShellQuestionBody({ todo, task, className, onAnswered, deps, can }: TaskShellQuestionProps & { can: boolean }) {
  const question = task.error_code === SHELL_WAITING ? shellQuestion(task.error_args) : undefined;
  if (!can || !question) return null;
  const runAnswer = deps?.answerTaskStart ?? answerTaskStart;
  return (
    <ShellQuestionCard
      scopeKey={JSON.stringify(["task", todo.id, todo.manager ?? "", task.todo_id ?? "", task.machine ?? "", task.cwd ?? "", task.title ?? ""])}
      question={question}
      className={className}
      deps={deps}
      answer={(choice) => runAnswer(todo, choice)}
      onAnswered={onAnswered}
    />
  );
}

/** The card where a start stopped on its question (lazily loaded, the default export): a planned task's (its failed
 *  row, panel and page give `todo` and its plan task), a session's own (the chat's start error), or a New Agent
 *  launch's whose first message was refused with shell_waiting. */
export default function ShellQuestion({ session, launch, todo, task, className, onAnswered, deps }: {
  session?: Session;
  launch?: AgentLaunch;
  todo?: Todo;
  task?: PlanTask;
  className?: string;
  onAnswered?: () => void;
  deps?: ShellQuestionDeps;
}) {
  return todo && task
    ? <TaskShellQuestion todo={todo} task={task} className={className} onAnswered={onAnswered ? () => onAnswered() : undefined} deps={deps} />
    : <StartShellQuestion session={session} launch={launch} className={className} onAnswered={onAnswered} deps={deps} />;
}

interface StartShellQuestionProps {
  session?: Session;
  launch?: AgentLaunch;
  className?: string;
  onAnswered?: () => void;
  deps?: ShellQuestionDeps;
}

/** A session's own start or a New Agent launch's, the started session's row saying the question as it stands now (a
 *  renewed tab), the refusal's own body until it does. */
function StartShellQuestion(props: StartShellQuestionProps) {
  const mid = props.session?.machine_id ?? props.launch?.machineId ?? "";
  const sid = props.session?.session_id ?? props.launch?.session?.session_id ?? "";
  if (!sid) return null;
  if (!props.launch) return <StartShellQuestionBody {...props} mid={mid} sid={sid} row={undefined} />;
  if (props.deps?.sessionRow !== undefined) return <StartShellQuestionBody {...props} mid={mid} sid={sid} row={props.deps.sessionRow ?? undefined} />;
  if (props.deps?.getSession) {
    if (props.deps.subscribe) {
      return <SubscribedLaunchShellQuestion {...props} mid={mid} sid={sid} getSession={props.deps.getSession} subscribe={props.deps.subscribe} />;
    }
    return <StartShellQuestionBody {...props} mid={mid} sid={sid} row={props.deps.getSession(mid, sid)} />;
  }
  return <DefaultLaunchShellQuestion {...props} mid={mid} sid={sid} />;
}

function DefaultLaunchShellQuestion(props: StartShellQuestionProps & { mid: string; sid: string }) {
  const row = useStore((s) => s.sessions[props.mid]?.find((x) => x.session_id === props.sid));
  return <StartShellQuestionBody {...props} row={row} />;
}

function sameRecord(a: Record<string, unknown> | undefined, b: Record<string, unknown> | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  const keys = Object.keys(a);
  if (keys.length !== Object.keys(b).length) return false;
  for (const k of keys) {
    if (!Object.prototype.hasOwnProperty.call(b, k) || !Object.is(a[k], b[k])) return false;
  }
  return true;
}

function sameLaunchSessionRow(a: Session | undefined, b: Session | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.machine_id === b.machine_id
    && a.session_id === b.session_id
    && a.start_error_code === b.start_error_code
    && sameRecord(a.start_error_args, b.start_error_args);
}

function SubscribedLaunchShellQuestion({
  getSession, subscribe, ...props
}: StartShellQuestionProps & { mid: string; sid: string; getSession: (mid: string, sid: string) => Session | undefined; subscribe: (listener: () => void) => () => void }) {
  const cache = useRef<{ mid: string; sid: string; row: Session | undefined } | null>(null);
  const getSnapshot = useCallback(() => {
    const next = getSession(props.mid, props.sid);
    const prev = cache.current;
    if (prev && prev.mid === props.mid && prev.sid === props.sid && sameLaunchSessionRow(prev.row, next)) {
      return prev.row;
    }
    cache.current = { mid: props.mid, sid: props.sid, row: next };
    return next;
  }, [getSession, props.mid, props.sid]);
  const row = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <StartShellQuestionBody {...props} row={row} />;
}

function StartShellQuestionBody({ session, launch, mid, sid, row, className, onAnswered, deps }: StartShellQuestionProps & { mid: string; sid: string; row: Session | undefined }) {
  const args = session ? session.start_error_args : row?.start_error_code === SHELL_WAITING ? row.start_error_args : launch?.shellWaiting;
  return <SessionShellQuestion className={className ?? (launch ? "w-full" : undefined)} mid={mid} sid={sid} args={args} onAnswered={onAnswered} deps={deps} />;
}
