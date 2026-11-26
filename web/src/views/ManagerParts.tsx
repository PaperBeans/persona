// The assistant title shares the sidebar head's identity. Offline restrictions still apply only with multiple managers
// (docs/assistant/managers.md "Clients"). The switcher itself is views/AssistantSwitcher.tsx.
import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore, type Ref } from "react";
import { t } from "../i18n";
import { currentRoster, managerOrder, multiManager, subscribeRoster, type Manager, type ManagerList } from "../lib/managers";
import { onManagerChoice, shownManager, type ShownIdentity } from "../lib/manager-choice";
import { ago } from "../lib/format";
import { api, enc, errorText } from "../lib/api";
import { sameValue } from "../lib/stable-lines";
import { Button, cx, Menu, toast, useMenu } from "../ui/atoms";
import { AssistantAvatar } from "../ui/AssistantAvatar";
import { useAssistantIdentity } from "../lib/assistant-identity";
import { usePersona } from "./assistant/Persona";

const noopSubscribe = () => () => {};

function resolveNow(now?: number | (() => number)): number | undefined {
  return now === undefined ? undefined : typeof now === "function" ? now() : now;
}

function readRosterValue(rosterDep: ManagerList | null | (() => ManagerList | null) | undefined): ManagerList | null {
  return rosterDep !== undefined ? (typeof rosterDep === "function" ? rosterDep() : rosterDep) : currentRoster();
}

function offlineEntryFrom(roster: ManagerList | null, id: string | undefined): Manager | null {
  if (!id || !multiManager(roster)) return null;
  const manager = roster.managers.find((m) => m.id === id);
  return manager && !manager.online && !manager.installing ? manager : null;
}

export interface AssistantTitleDeps {
  /** Controlled assistant identity or reader (bypasses `useAssistantIdentity` when supplied). */
  identity?: Pick<ShownIdentity, "name"> | null | (() => Pick<ShownIdentity, "name"> | null);
  /** Subscribe to external identity updates when `identity` is a reader. */
  subscribeIdentity?: (listener: () => void) => () => void;
  /** Controlled fallback persona name or reader (bypasses `usePersona` when supplied). */
  personaName?: string | (() => string);
  /** Subscribe to external persona name updates when `personaName` is a reader. */
  subscribePersona?: (listener: () => void) => () => void;
}

export interface ManagerOfflineDeps {
  /** Controlled roster snapshot or reader (bypasses `subscribeRoster` / `currentRoster` when supplied). */
  roster?: ManagerList | null | (() => ManagerList | null);
  /** Subscribe to external roster updates (bypasses `subscribeRoster` when supplied). */
  subscribeRoster?: (listener: () => void) => () => void;
  /** Fixed timestamp or clock reader (ms) for relative last-synced formatting. */
  now?: number | (() => number);
}

export interface ShownOfflineDeps extends ManagerOfflineDeps {
  /** Controlled shown manager id or reader (bypasses `onManagerChoice` / `shownManager` when supplied). */
  shownManager?: string | (() => string);
  /** Subscribe to external shown-manager updates (bypasses `onManagerChoice` when supplied). */
  subscribeShownManager?: (listener: () => void) => () => void;
}

export interface OfflineStateProps {
  offline: ShownOffline;
  /** The surface it sits on, which the mark is cut out of. */
  ring?: string;
  className?: string;
}

export interface MoveToManagerDeps {
  /** Controlled roster snapshot or reader (bypasses `useRoster` when supplied). */
  roster?: ManagerList | null | (() => ManagerList | null);
  /** Subscribe to external roster updates (bypasses `subscribeRoster` when supplied). */
  subscribeRoster?: (listener: () => void) => () => void;
  /** Move `machineId` to `to` (defaults to `POST /managers/{id}/machines`). */
  moveMachine?: (machineId: string, to: Manager, signal: AbortSignal) => Promise<unknown>;
  /** Notify on move success or failure (defaults to `toast`). */
  notify?: (message: string, tone: "ok" | "error") => void;
  /** Format thrown move errors (defaults to `api.errorText`). */
  formatError?: (error: unknown) => string;
  /** Optional scope key that aborts in-flight moves and resets menu/busy state on change. */
  scopeKey?: string;
}

export interface MoveToManagerProps {
  machineId: string;
  manager?: string;
  movable?: boolean;
  deps?: MoveToManagerDeps;
}

export interface MoveToManagerViewProps {
  managers: readonly Manager[];
  owner: string;
  busy: boolean;
  anchor: HTMLElement | null;
  buttonHostRef?: Ref<HTMLSpanElement>;
  onToggleMenu: () => void;
  onCloseMenu: () => void;
  onSelect: (manager: Manager) => void;
}

function useControlledIdentityName(deps: AssistantTitleDeps): string {
  const identityDep = deps.identity;
  const subscribe = deps.subscribeIdentity ?? noopSubscribe;
  const getSnapshot = useCallback(
    () => (typeof identityDep === "function" ? identityDep()?.name ?? "" : identityDep?.name ?? ""),
    [identityDep],
  );
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

function useControlledPersonaName(deps: AssistantTitleDeps): string {
  const personaDep = deps.personaName;
  const subscribe = deps.subscribePersona ?? noopSubscribe;
  const getSnapshot = useCallback(
    () => (typeof personaDep === "function" ? personaDep() : personaDep ?? ""),
    [personaDep],
  );
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

/** The header names the same current assistant as the sidebar, including a single assistant and reloads.
 *  An older hub without a roster keeps the chosen look's name. */
export function useAssistantTitle(deps?: AssistantTitleDeps): string {
  const identityName = deps?.identity !== undefined ? useControlledIdentityName(deps) : (useAssistantIdentity()?.name ?? "");
  const personaName = deps?.personaName !== undefined ? useControlledPersonaName(deps) : usePersona().name;
  return identityName || personaName;
}

/** What a composer says for a conversation of manager id while it is offline (several managers only): "<name> · last
 *  synced …"; undefined when it is online, unknown, or the hub has one manager. Such a conversation can't be sent to,
 *  wherever it was opened from (the inbox, a notification, the Agents list). */
export function useManagerOffline(id: string | undefined, deps?: ManagerOfflineDeps): string | undefined {
  const controlled = deps?.roster !== undefined || deps?.subscribeRoster !== undefined;
  const subscribe = controlled ? (deps?.subscribeRoster ?? noopSubscribe) : subscribeRoster;
  const rosterDep = deps?.roster;
  const nowDep = deps?.now;
  const line = useCallback(() => {
    const m = offlineEntryFrom(readRosterValue(rosterDep), id);
    if (!m) return undefined;
    const when = ago(m.last_seen, resolveNow(nowDep));
    return when ? t("settings-managers.offline_composer", { name: m.name, time: when }) : t("settings-managers.offline_composer_never", { name: m.name });
  }, [id, rosterDep, nowDep]);
  return useSyncExternalStore(subscribe, line, line);
}

/** The current assistant while it is offline (several assistants only), and what the pages say of it. */
export interface ShownOffline {
  manager: Manager;
  /** Whether it is the roster's default (its avatar's color). */
  isDefault: boolean;
  /** The assistant page's composer: "<name> · last synced …". */
  composer: string;
  /** The assistant page's starter: it can read the history and not send. */
  body: string;
  /** A list's page (OfflineState): "<name> is offline", and when it last synced and that the page updates once it's back. */
  title: string;
  note: string;
}

/** The shown manager when it is offline (only with more than one): the assistant page then can't send, and the lists
 *  the hub merges say it is offline (OfflineState). */
export function useShownOffline(deps?: ShownOfflineDeps): ShownOffline | null {
  const controlledShown = deps?.shownManager !== undefined || deps?.subscribeShownManager !== undefined;
  const subShown = controlledShown ? (deps?.subscribeShownManager ?? noopSubscribe) : onManagerChoice;
  const shownDep = deps?.shownManager;
  const getShown = useCallback(
    () => (shownDep !== undefined ? (typeof shownDep === "function" ? shownDep() : shownDep) : shownManager()),
    [shownDep],
  );
  const shown = useSyncExternalStore(subShown, getShown, getShown);

  const controlledRoster = deps?.roster !== undefined || deps?.subscribeRoster !== undefined;
  const subRoster = controlledRoster ? (deps?.subscribeRoster ?? noopSubscribe) : subscribeRoster;
  const rosterDep = deps?.roster;
  const cache = useRef<{ manager: Manager; isDefault: boolean } | null>(null);
  const getEntry = useCallback(() => {
    const roster = readRosterValue(rosterDep);
    const m = offlineEntryFrom(roster, shown);
    if (!m) {
      cache.current = null;
      return null;
    }
    const next = { manager: m, isDefault: m.id === roster?.default };
    if (cache.current && sameValue(cache.current, next)) return cache.current;
    cache.current = next;
    return next;
  }, [rosterDep, shown]);
  const entry = useSyncExternalStore(subRoster, getEntry, getEntry);
  if (!entry) return null;
  const m = entry.manager;
  const when = ago(m.last_seen, resolveNow(deps?.now));
  return {
    manager: m,
    isDefault: entry.isDefault,
    composer: when ? t("settings-managers.offline_composer", { name: m.name, time: when }) : t("settings-managers.offline_composer_never", { name: m.name }),
    body: t("settings-managers.offline_body", { name: m.name }),
    title: t("settings-managers.offline_page_title", { name: m.name }),
    note: when ? t("settings-managers.offline_page_body", { time: when }) : t("settings-managers.offline_page_body_never"),
  };
}

/** Pure presentational view for the offline-assistant empty state. */
export function OfflineStateView({ offline, ring = "ring-page", className }: OfflineStateProps) {
  const { manager } = offline;
  return (
    <div role="status" data-assistant-offline={manager.id} className={cx("flex flex-col items-center gap-2 px-6 text-center", className)}>
      <AssistantAvatar id={manager.id} name={manager.name} isDefault={offline.isDefault} size={40} status="grey" ring={ring} className="mb-1 opacity-60" />
      <p className="text-[13.5px] font-medium text-ink">{offline.title}</p>
      <p className="max-w-80 text-[12.5px] leading-relaxed text-ink-3">{offline.note}</p>
    </div>
  );
}

/** In place of a list's empty state (or its first-run page) while the current assistant is offline (useShownOffline):
 *  the hub's merged lists (the inbox, to-dos) leave out a manager that doesn't answer (docs/protocol/protocol.md
 *  "Per-manager routing"), so its list would read as empty. Its avatar with the offline mark, as in the switcher. */
export function OfflineState(props: OfflineStateProps) {
  return <OfflineStateView {...props} />;
}

/** Pure presentational view for the Move-to-manager button and dropdown menu. */
export function MoveToManagerView({
  managers,
  owner,
  busy,
  anchor,
  buttonHostRef,
  onToggleMenu,
  onCloseMenu,
  onSelect,
}: MoveToManagerViewProps) {
  return (
    <>
      <span ref={buttonHostRef} className="contents">
        <Button tone="secondary" icon="arrowRight" busy={busy} onClick={onToggleMenu}>
          {t("settings-managers.move_to")}
        </Button>
      </span>
      {anchor && (
        <Menu
          anchor={anchor}
          onClose={onCloseMenu}
          align="right"
          header={t("settings-managers.move_title")}
          items={managers.map((m) => ({
            key: m.id,
            label: m.name,
            checked: m.id === owner,
            hint: m.online ? undefined : t("settings-managers.move_offline"),
            disabled: busy || m.id === owner,
            onSelect: () => onSelect(m),
          }))}
        />
      )}
    </>
  );
}

/** Move to… on a computer's page: gives it to another manager (`POST /managers/{id}/machines`, docs/assistant/managers.md "Moving a
 *  machine or a source"); its sessions and cards follow, the machines push shows where it went. Only with more than
 *  one manager, and not for a linked hub's computer (not movable). */
export function MoveToManager({ machineId, manager, movable = true, deps }: MoveToManagerProps) {
  const depsRef = useRef(deps);
  useLayoutEffect(() => {
    depsRef.current = deps;
  });

  const controlledRoster = deps?.roster !== undefined || deps?.subscribeRoster !== undefined;
  const subscribe = controlledRoster ? (deps?.subscribeRoster ?? noopSubscribe) : subscribeRoster;
  const rosterDep = deps?.roster;
  const rosterCache = useRef<{ set: boolean; value: ManagerList | null }>({ set: false, value: null });
  const getRosterSnapshot = useCallback(() => {
    const next = readRosterValue(rosterDep);
    if (rosterCache.current.set && sameValue(rosterCache.current.value, next)) return rosterCache.current.value;
    rosterCache.current = { set: true, value: next };
    return next;
  }, [rosterDep]);
  const roster = useSyncExternalStore(subscribe, getRosterSnapshot, getRosterSnapshot);

  const menu = useMenu();
  const at = useRef<HTMLSpanElement>(null);
  const visible = multiManager(roster) && movable;
  const owner = manager || roster?.default || "";
  const scopeId = JSON.stringify([deps?.scopeKey ?? null, machineId, owner, visible]);
  const [scope, setScope] = useState<{ id: string; epoch: number }>(() => ({ id: scopeId, epoch: 1 }));
  const [busyEpoch, setBusyEpoch] = useState<number | null>(null);
  if (scope.id !== scopeId) {
    setScope({ id: scopeId, epoch: scope.epoch + 1 });
    setBusyEpoch(null);
  }

  const activeEpochRef = useRef(0);
  const moveSeqRef = useRef(0);
  const pendingRef = useRef<{ epoch: number; seq: number; controller: AbortController } | null>(null);
  const closeMenuRef = useRef(menu.close);
  useLayoutEffect(() => {
    closeMenuRef.current = menu.close;
  });

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    return () => {
      activeEpochRef.current = 0;
      pendingRef.current?.controller.abort();
      pendingRef.current = null;
      moveSeqRef.current += 1;
      closeMenuRef.current();
    };
  }, [scope.epoch]);

  if (!visible || !roster) return null;
  const busy = busyEpoch === scope.epoch;

  const notify = (message: string, tone: "ok" | "error") => {
    try {
      if (depsRef.current?.notify) depsRef.current.notify(message, tone);
      else toast(message, tone);
    } catch {
      // Notification errors stay non-blocking so move recovery never leaves busy stuck or rejects unhandled.
    }
  };

  const move = async (to: Manager) => {
    const runEpoch = scope.epoch;
    if (pendingRef.current !== null || activeEpochRef.current !== runEpoch || to.id === owner) return;
    const controller = new AbortController();
    const runSeq = ++moveSeqRef.current;
    pendingRef.current = { epoch: runEpoch, seq: runSeq, controller };
    const isCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === runEpoch
      && pendingRef.current?.seq === runSeq;
    setBusyEpoch(runEpoch);
    try {
      const moveMachine = depsRef.current?.moveMachine;
      if (moveMachine) {
        await moveMachine(machineId, to, controller.signal);
      } else {
        await api(`/managers/${enc(to.id)}/machines`, { method: "POST", json: { machine_id: machineId }, signal: controller.signal });
      }
      if (!isCurrent()) return;
      notify(t("settings-managers.moved", { name: to.name }), "ok");
    } catch (e) {
      if (!isCurrent()) return;
      let errText: string;
      try {
        errText = (depsRef.current?.formatError ?? errorText)(e);
      } catch {
        errText = errorText(e);
      }
      notify(t("settings-managers.move_failed", { error: errText }), "error");
    } finally {
      if (pendingRef.current?.seq === runSeq) pendingRef.current = null;
      if (activeEpochRef.current === runEpoch && moveSeqRef.current === runSeq) {
        setBusyEpoch((cur) => (cur === runEpoch ? null : cur));
      }
    }
  };

  const toggleMenu = () => {
    if (busy || pendingRef.current !== null) return;
    if (menu.anchor) menu.close();
    else menu.openAt(at.current?.firstElementChild as HTMLElement | null);
  };

  return (
    <MoveToManagerView
      managers={managerOrder(roster)}
      owner={owner}
      busy={busy}
      anchor={menu.anchor}
      buttonHostRef={at}
      onToggleMenu={toggleMenu}
      onCloseMenu={menu.close}
      onSelect={(m) => void move(m)}
    />
  );
}
