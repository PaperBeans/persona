// Remembered permission grants belong to this conversation; revoke only after the hub accepts the deletion.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { api, enc, errorText } from "../../lib/api";
import { ago } from "../../lib/format";
import { t } from "../../i18n";
import { Button, Loading } from "../../ui/atoms";

export interface AlwaysPermissionGrant {
  key_hash: string;
  tool_name: string;
  created_at: string;
}

export interface AlwaysPermissionRevokeResult {
  restart_required?: boolean;
}

export interface AlwaysPermissionsProps {
  session: string;
  /** Controlled grants list (`null` while loading, `[]` when empty); skips fetching when supplied. */
  grants?: AlwaysPermissionGrant[] | null;
  /** Controlled error message; overrides internal load/revoke error state when supplied. */
  error?: string;
  /** Controlled `key_hash` currently being revoked; overrides internal pending state when supplied. */
  pendingKey?: string;
  /** Controlled restart-warning flag; skips reading and writing `sessionStorage` when supplied. */
  restartRequired?: boolean;
  /** Controlled loader replacing `GET /assistant/conversations/:session/always` when `grants` is omitted. */
  loadGrants?: (session: string) => Promise<AlwaysPermissionGrant[]>;
  /** Controlled revoke callback replacing `DELETE /assistant/conversations/:session/always/:key`. */
  onRevoke?: (keyHash: string, session: string) => Promise<AlwaysPermissionRevokeResult | void>;
  /** Controlled retry callback invoked when clicking Retry after a load failure. */
  onRetry?: () => void;
  /** Controlled handler invoked when a revoke reports `restart_required: true`; skips `sessionStorage` access when supplied. */
  onRestartRequired?: (session: string) => void;
}

const restartKey = (session: string) => `persona.always-restart:${session}`;
function needsRestart(session: string) {
  try { return sessionStorage.getItem(restartKey(session)) === "1"; } catch { return false; }
}

export function AlwaysPermissions({
  session,
  grants: controlledGrants,
  error: controlledError,
  pendingKey: controlledPendingKey,
  restartRequired: controlledRestart,
  loadGrants,
  onRevoke,
  onRetry,
  onRestartRequired,
}: AlwaysPermissionsProps) {
  const isControlledGrants = controlledGrants !== undefined;
  const isControlledError = controlledError !== undefined;
  const isControlledPending = controlledPendingKey !== undefined;
  const isControlledRestart = controlledRestart !== undefined;
  const managedRestart = isControlledRestart || Boolean(onRestartRequired);

  const [localGrants, setLocalGrants] = useState<AlwaysPermissionGrant[] | null>(null);
  const [localError, setLocalError] = useState<{ session: string; message: string }>({ session, message: "" });
  const [localPending, setLocalPending] = useState<{ session: string; key: string } | null>(null);
  const [revision, setRevision] = useState(0);
  const [localRestart, setLocalRestart] = useState<{ session: string; value: boolean }>(() => ({
    session,
    value: managedRestart ? false : needsRestart(session),
  }));

  const sessionRef = useRef(session);
  useLayoutEffect(() => {
    sessionRef.current = session;
  }, [session]);

  const loadGrantsRef = useRef(loadGrants);
  useEffect(() => {
    loadGrantsRef.current = loadGrants;
  }, [loadGrants]);
  const hasLoadGrants = Boolean(loadGrants);
  const path = `/assistant/conversations/${enc(session)}/always`;

  useEffect(() => {
    if (isControlledGrants) return;
    let live = true;
    setLocalGrants(null);
    setLocalError({ session, message: "" });
    let req: Promise<AlwaysPermissionGrant[]>;
    try {
      req = loadGrantsRef.current
        ? Promise.resolve(loadGrantsRef.current(session))
        : api<{ grants: AlwaysPermissionGrant[] }>(path).then((r) => r.grants);
    } catch (e) {
      setLocalError({ session, message: errorText(e) });
      return;
    }
    req
      .then((rows) => { if (live) setLocalGrants(rows); })
      .catch((e) => { if (live) setLocalError({ session, message: errorText(e) }); });
    return () => { live = false; };
  }, [isControlledGrants, hasLoadGrants, path, session, revision]);

  const grants = isControlledGrants ? controlledGrants : localGrants;
  const error = isControlledError
    ? controlledError
    : localError.session === session
      ? localError.message
      : "";
  const pending = isControlledPending
    ? controlledPendingKey
    : localPending?.session === session
      ? localPending.key
      : "";
  const restart = isControlledRestart
    ? controlledRestart
    : localRestart.session === session
      ? localRestart.value
      : managedRestart
        ? false
        : needsRestart(session);

  const revoke = async (key: string) => {
    if (pending) return;
    const targetSession = session;
    const targetPath = path;
    if (!isControlledPending) setLocalPending({ session: targetSession, key });
    if (!isControlledError) setLocalError({ session: targetSession, message: "" });
    try {
      const result = onRevoke
        ? await onRevoke(key, targetSession)
        : await api<{ restart_required: boolean }>(`${targetPath}/${enc(key)}`, { method: "DELETE" });
      const stillActive = sessionRef.current === targetSession;
      if (stillActive && !isControlledGrants) {
        setLocalGrants((rows) => rows?.filter((row) => row.key_hash !== key) ?? null);
      }
      if (result?.restart_required) {
        if (stillActive && !isControlledRestart) {
          setLocalRestart({ session: targetSession, value: true });
        }
        if (onRestartRequired) {
          onRestartRequired(targetSession);
        } else if (!isControlledRestart) {
          try { sessionStorage.setItem(restartKey(targetSession), "1"); } catch { /* The visible warning still remains. */ }
        }
      }
    } catch (e) {
      if (sessionRef.current === targetSession && !isControlledError) {
        setLocalError({ session: targetSession, message: errorText(e) });
      }
    } finally {
      if (!isControlledPending) {
        setLocalPending((cur) => (cur?.session === targetSession && cur.key === key ? null : cur));
      }
    }
  };

  const retry = () => {
    if (onRetry) {
      onRetry();
      return;
    }
    if (!isControlledGrants) setRevision((n) => n + 1);
  };

  return (
    <section aria-label={t("chat-sessioninfo.always_title")}>
      <h3 className="mb-1 text-[11.5px] font-medium text-ink-3">{t("chat-sessioninfo.always_title")}</h3>
      <p className="mb-2 text-[12px] leading-relaxed text-ink-3">{t("chat-sessioninfo.always_hint")}</p>
      {restart && <p role="status" className="mb-2 text-[12px] text-attention">{t("chat-sessioninfo.always_restart")}</p>}
      {error && <p role="alert" className="mb-2 text-[12px] text-red">{error}</p>}
      {!grants && !error && <Loading />}
      {!grants && error && (!isControlledGrants || onRetry) && <Button tone="secondary" onClick={retry}>{t("chat-sessioninfo.always_retry")}</Button>}
      {grants?.length === 0 && <p className="text-[12.5px] text-ink-2">{t("chat-sessioninfo.always_empty")}</p>}
      {!!grants?.length && (
        <>
          <p className="mb-2 text-[12px] leading-relaxed text-ink-3">{t("chat-sessioninfo.always_revoke_hint")}</p>
          <div className="divide-y divide-line overflow-hidden rounded-[10px] bg-inset shadow-hairline">
            {grants.map((grant) => (
              <div key={grant.key_hash} className="flex items-center gap-3 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[12.5px] text-ink" title={grant.tool_name}>{grant.tool_name || t("chat-sessioninfo.always_unknown")}</p>
                  <p className="text-[11.5px] text-ink-3">{ago(grant.created_at)}</p>
                </div>
                <Button tone="quiet" busy={pending === grant.key_hash} disabled={!!pending} onClick={() => void revoke(grant.key_hash)}>
                  {t("chat-sessioninfo.always_revoke")}
                </Button>
              </div>
            ))}
          </div>
        </>
      )}
    </section>
  );
}
