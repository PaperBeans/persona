// /lark-oauth: where Feishu / Lark sends the owner back after they allowed (or declined) a bot's app to read as them
// (lib/lark-sync.ts, LarkSyncSection.tsx). It hands the code to the hub once, says plainly how it went, and leads back
// to the Lark settings the owner came from. A lazy page: only this address loads it, and its words stay off the first
// screen.
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { BASE } from "../../lib/base";
import { finishSyncOAuth, forgetReturn, OAUTH_RETURN_PATH, oauthReturn, readReturn } from "../../lib/lark-sync";
import { navigate } from "../../lib/router";
import { Button, IconButton, Spinner } from "../../ui/atoms";
import { Empty } from "../session/files/parts";

type Outcome = { state: "working" } | { state: "done" } | { state: "declined" } | { state: "missing" } | { state: "failed"; error: string };

// One hand-over per authorization, however often the page draws (a remount in development runs the effect twice): a
// code can be exchanged only once, and a second try would fail the one that worked.
const handed = new Map<string, Promise<unknown>>();

/** Hands `code` for `state` to the hub, once per state. Exported for the tests. */
export function handOver(state: string, code: string, manager?: string): Promise<unknown> {
  let p = handed.get(state);
  if (!p) {
    p = finishSyncOAuth(state, code, manager);
    handed.set(state, p);
    // a failure may be tried again from the start (a new authorization), not by reusing this one
    p.catch(() => handed.delete(state));
  }
  return p;
}

export function LarkOAuthReturn({ onToggleSidebar }: { onToggleSidebar: () => void }) {
  // Read once: the address loses the code below, and the page must not hand it over again on a reload.
  const [query] = useState(() => oauthReturn(location.search));
  const [back] = useState(() => readReturn());
  const [outcome, setOutcome] = useState<Outcome>(() =>
    query.error ? { state: "declined" } : query.code && query.state ? { state: "working" } : { state: "missing" },
  );

  useEffect(() => {
    // the code is single-use and private: off the address bar and out of history right away
    if (location.search) history.replaceState(history.state, "", `${BASE}${OAUTH_RETURN_PATH}`);
    if (query.error || !query.code || !query.state) return;
    let live = true;
    handOver(query.state, query.code, back.manager).then(
      () => {
        forgetReturn();
        if (live) setOutcome({ state: "done" });
      },
      (e) => {
        if (live) setOutcome({ state: "failed", error: errorText(e) });
      },
    );
    return () => void (live = false);
  }, [query, back.manager]);

  const toSettings = (
    <Button tone={outcome.state === "done" ? "accent" : "secondary"} icon="gear" onClick={() => navigate(back.back, true)}>
      {t("settings-larksync.return_back")}
    </Button>
  );

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("app.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
      </header>
      <div className="flex min-h-0 flex-1 flex-col pb-[10vh]" role="status" aria-live="polite">
        {outcome.state === "working" ? (
          <div className="flex flex-1 items-center justify-center gap-2 text-meta text-ink-3">
            <Spinner />
            {t("settings-larksync.return_working")}
          </div>
        ) : outcome.state === "done" ? (
          <Empty icon="check" title={t("settings-larksync.return_done_title")} detail={t("settings-larksync.return_done_detail")} action={toSettings} />
        ) : outcome.state === "declined" ? (
          <Empty icon="x" title={t("settings-larksync.return_declined_title")} detail={t("settings-larksync.return_declined_detail")} action={toSettings} />
        ) : outcome.state === "missing" ? (
          <Empty icon="search" title={t("settings-larksync.return_missing_title")} detail={t("settings-larksync.return_missing_detail")} action={toSettings} />
        ) : (
          <Empty
            icon="x"
            title={t("settings-larksync.return_failed_title")}
            detail={t("settings-larksync.return_failed_detail", { error: outcome.error })}
            action={toSettings}
          />
        )}
      </div>
    </div>
  );
}
