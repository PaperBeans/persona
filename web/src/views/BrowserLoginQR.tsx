import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { deviceName, forgetUnrevoked, unrevokedTokenId } from "../lib/auth";
import { errorText, setPairedToken } from "../lib/api";
import { BASE } from "../lib/base";
import { storedCredentials } from "../lib/compatibility";
import { t } from "../i18n";
import { Button, Spinner } from "../ui/atoms";

const QRCode = lazy(() => import("./settings/LoginSteps").then((m) => ({ default: m.QRCode })));

export type BrowserLoginChallenge = { id: string; secret: string; expires_at: string; qr: { rows: string[] } };
export type BrowserLoginCredential = { state: string; token?: string; id?: string };

export class BrowserLoginRequestError extends Error {
  constructor(readonly status: number, readonly code = "", readonly state = "") {
    super(status === 410 ? t("login.qr_expired") : t("login.qr_failed"));
  }
}

export interface BrowserLoginQRDeps {
  start?: (body: { name: string; replaces?: string }, signal: AbortSignal) => Promise<BrowserLoginChallenge>;
  poll?: (body: { id: string; secret: string }, signal: AbortSignal) => Promise<BrowserLoginCredential>;
  confirm?: (body: { id: string; secret: string; number: string }, signal: AbortSignal) => Promise<BrowserLoginCredential>;
  setPairedToken?: (token: string, id: string) => void;
  forgetUnrevoked?: () => void;
  deviceName?: () => string;
  replacesTokenId?: () => string;
  formatError?: (error: unknown) => string;
  isVisible?: () => boolean;
  renderQRCode?: (props: { rows: string[]; label: string }) => ReactNode;
}

export interface BrowserLoginQRProps {
  onDone: () => void;
  scopeKey?: string;
  deps?: BrowserLoginQRDeps;
}

export interface BrowserLoginQRViewProps {
  state: string;
  error: string;
  challenge?: BrowserLoginChallenge;
  number: string;
  sending: boolean;
  onNumberChange: (value: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  onRefresh: () => void;
  renderQRCode?: (props: { rows: string[]; label: string }) => ReactNode;
}

async function defaultRequest<T>(action: string, body: unknown, signal: AbortSignal): Promise<T> {
  const response = await fetch(`${BASE}/api/v1/auth/browser-login/${action}`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body), signal, cache: "no-store",
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({})) as { code?: string; state?: string };
    throw new BrowserLoginRequestError(response.status, data.code, data.state);
  }
  return response.json();
}

function requestStatus(err: unknown): number | undefined {
  if (err instanceof BrowserLoginRequestError) return err.status;
  if (err && typeof err === "object" && "status" in err && typeof err.status === "number") return err.status;
  return undefined;
}

function requestState(err: unknown): string {
  if (err instanceof BrowserLoginRequestError) return err.state;
  if (err && typeof err === "object" && "state" in err && typeof err.state === "string") return err.state;
  return "";
}

/** Pure presentational view for the browser QR sign-in card. */
export function BrowserLoginQRView({
  state, error, challenge, number, sending, onNumberChange, onSubmit, onRefresh, renderQRCode,
}: BrowserLoginQRViewProps) {
  const ended = ["expired", "denied", "voided", "failed"].includes(state);
  const endedText = error || t(state === "denied" ? "login.qr_denied" : state === "voided" ? "login.qr_voided" : "login.qr_expired");
  const qrLabel = t("login.qr_title");
  return <section className="rounded-card bg-surface p-4 text-center shadow-card" aria-labelledby="login-qr-title">
    <h2 id="login-qr-title" className="text-card-title font-semibold text-ink">{qrLabel}</h2>
    <div className="my-4 flex items-center justify-center" style={{ minHeight: 184 }}>
      {state === "loading" ? <Spinner />
        : ended ? <p className="text-meta text-ink-2" role="status">{endedText}</p>
        : state === "confirm" ? <form onSubmit={onSubmit} className="flex flex-col items-center gap-3">
          <label htmlFor="login-qr-number" className="text-meta text-ink-2">{t("login.qr_number_label")}</label>
          <span className="flex h-10 w-24 items-center gap-2 rounded-control bg-inset px-3 shadow-hairline focus-within:shadow-[0_0_0_1px_var(--line-strong)]">
            <input id="login-qr-number" value={number} onChange={(e) => onNumberChange(e.target.value.replace(/\D/g, "").slice(0, 2))}
              inputMode="numeric" autoComplete="one-time-code" autoFocus maxLength={2} aria-describedby="login-qr-hint"
              className="min-w-0 flex-1 bg-transparent text-center font-mono text-card-title text-ink outline-none" />
          </span>
          <Button type="submit" tone="primary" disabled={number.length !== 2 || sending}>{sending ? <Spinner /> : t("login.sign_in")}</Button>
        </form>
        : challenge ? (renderQRCode
          ? renderQRCode({ rows: challenge.qr.rows, label: qrLabel })
          : <Suspense fallback={<Spinner />}><QRCode rows={challenge.qr.rows} label={qrLabel} /></Suspense>) : null}
    </div>
    {!ended && <p id="login-qr-hint" className="text-meta text-ink-2" role="status">
      {t(state === "scanned" ? "login.qr_scanned" : state === "confirm" ? "login.qr_number_hint" : state === "retrying" ? "login.qr_retry" : "login.qr_hint")}
    </p>}
    <Button tone="secondary" type="button" className="mt-3" onClick={onRefresh}>{t("login.qr_refresh")}</Button>
  </section>;
}

// Sign in by scanning a code with Persona on a phone (docs/protocol/protocol.md "Browser QR sign-in"): the phone allows
// it and shows a two-digit number, which is typed here. One try: a wrong number cancels the request.
export function BrowserLoginQR({ onDone, scopeKey, deps }: BrowserLoginQRProps) {
  const [generation, refresh] = useState(0);
  const [renderedScope, setRenderedScope] = useState(scopeKey);
  const [challenge, setChallenge] = useState<BrowserLoginChallenge>();
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  const [number, setNumber] = useState("");
  const [sending, setSending] = useState(false);

  if (renderedScope !== scopeKey) {
    setRenderedScope(scopeKey);
    setChallenge(undefined);
    setState("loading");
    setError("");
    setNumber("");
    setSending(false);
  }

  const onDoneRef = useRef(onDone);
  const depsRef = useRef(deps);
  useLayoutEffect(() => {
    onDoneRef.current = onDone;
    depsRef.current = deps;
  });

  const runSeqRef = useRef(0);
  const activeRunRef = useRef(0);
  const confirmRef = useRef<{ runId: number; run: (n: string) => Promise<void> } | null>(null);
  const confirmSeqRef = useRef(0);
  const pendingConfirmRef = useRef<{ runId: number; seq: number } | null>(null);

  useLayoutEffect(() => () => {
    activeRunRef.current = 0;
    confirmRef.current = null;
    pendingConfirmRef.current = null;
    confirmSeqRef.current += 1;
  }, [generation, scopeKey]);

  useEffect(() => {
    const controller = new AbortController();
    const runId = ++runSeqRef.current;
    activeRunRef.current = runId;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let done = false;
    let replacing = false;
    const isStale = () => controller.signal.aborted || activeRunRef.current !== runId;
    const stopTimer = () => {
      if (timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
      }
    };

    const callStart = (body: { name: string; replaces?: string }) =>
      depsRef.current?.start ? depsRef.current.start(body, controller.signal) : defaultRequest<BrowserLoginChallenge>("start", body, controller.signal);
    const callPoll = (body: { id: string; secret: string }) =>
      depsRef.current?.poll ? depsRef.current.poll(body, controller.signal) : defaultRequest<BrowserLoginCredential>("poll", body, controller.signal);
    const callConfirm = (body: { id: string; secret: string; number: string }) =>
      depsRef.current?.confirm ? depsRef.current.confirm(body, controller.signal) : defaultRequest<BrowserLoginCredential>("confirm", body, controller.signal);

    function failed(err: unknown) {
      if (isStale() || done) return;
      done = true;
      stopTimer();
      setState("failed");
      setError((depsRef.current?.formatError ?? errorText)(err));
    }

    function finish(result: BrowserLoginCredential): boolean {
      if (isStale() || done) return true;
      if (result.state === "approved" && result.token && result.id) {
        done = true;
        stopTimer();
        (depsRef.current?.setPairedToken ?? setPairedToken)(result.token, result.id);
        if (replacing) (depsRef.current?.forgetUnrevoked ?? forgetUnrevoked)(); // the hub has had its one chance at it
        onDoneRef.current();
        return true;
      }
      if (result.state === "denied" || result.state === "voided") {
        done = true;
        stopTimer();
        setState(result.state);
        return true;
      }
      return false;
    }

    async function poll(c: BrowserLoginChallenge) {
      if (isStale() || done) return;
      const visible = depsRef.current?.isVisible ? depsRef.current.isVisible() : document.visibilityState !== "hidden";
      if (visible) {
        try {
          const result = await callPoll({ id: c.id, secret: c.secret });
          if (isStale() || done) return;
          if (finish(result)) return;
          setState(result.state);
        } catch (err) {
          if (isStale() || done) return;
          const status = requestStatus(err);
          if (err instanceof TypeError || (status !== undefined && (status >= 500 || status === 429))) {
            setState((s) => (s === "confirm" ? s : "retrying"));
            timer = setTimeout(() => void poll(c), 3000);
          } else failed(err);
          return;
        }
      }
      if (!isStale() && !done) timer = setTimeout(() => void poll(c), 1500);
    }

    setChallenge(undefined);
    setState("loading");
    setError("");
    setNumber("");
    setSending(false);

    async function startChallenge(): Promise<BrowserLoginChallenge> {
      // The credential this browser had before, as pair() sends it: the hub revokes it once the new one is issued.
      const replaces = depsRef.current?.replacesTokenId
        ? depsRef.current.replacesTokenId()
        : (storedCredentials().tokenId || unrevokedTokenId());
      const name = (depsRef.current?.deviceName ?? deviceName)();
      if (!replaces) return callStart({ name });
      try {
        const c = await callStart({ name, replaces });
        replacing = true;
        return c;
      } catch (err) {
        if (isStale()) throw err;
        // A hub from before #10291 refuses the unknown field: sign in without replacing, as before.
        if (requestStatus(err) === 400) return callStart({ name });
        throw err;
      }
    }

    startChallenge().then((c) => {
      if (isStale()) return;
      confirmRef.current = {
        runId,
        run: async (n) => {
          if (isStale() || done) return;
          try {
            const result = await callConfirm({ id: c.id, secret: c.secret, number: n });
            if (isStale() || done) return;
            finish(result);
          } catch (err) {
            if (isStale() || done) return;
            const status = requestStatus(err);
            if (status === 409) finish({ state: requestState(err) || "voided" });
            else if (status === 410) { done = true; stopTimer(); setState("expired"); }
            // A lost reply: the poll picks up the credential if the number went through.
            else if (!(err instanceof TypeError || (status !== undefined && status >= 500))) failed(err);
          }
        },
      };
      setChallenge(c);
      setState("pending");
      void poll(c);
    }).catch(failed);

    return () => {
      controller.abort();
      stopTimer();
      confirmRef.current = null;
      if (activeRunRef.current === runId) activeRunRef.current = 0;
      if (pendingConfirmRef.current?.runId === runId) pendingConfirmRef.current = null;
      confirmSeqRef.current += 1;
    };
  }, [generation, scopeKey]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const runId = activeRunRef.current;
    const activeConfirm = confirmRef.current;
    if (number.length !== 2 || sending || !activeConfirm || activeConfirm.runId !== runId || pendingConfirmRef.current !== null) return;
    const seq = ++confirmSeqRef.current;
    pendingConfirmRef.current = { runId, seq };
    setSending(true);
    try {
      await activeConfirm.run(number);
    } finally {
      if (pendingConfirmRef.current?.seq === seq) pendingConfirmRef.current = null;
      if (activeRunRef.current === runId && confirmSeqRef.current === seq) setSending(false);
    }
  };

  const handleRefresh = () => {
    activeRunRef.current = 0;
    confirmRef.current = null;
    pendingConfirmRef.current = null;
    confirmSeqRef.current += 1;
    setChallenge(undefined);
    setState("loading");
    setError("");
    setNumber("");
    setSending(false);
    refresh((n) => n + 1);
  };

  return <BrowserLoginQRView
    state={state}
    error={error}
    challenge={challenge}
    number={number}
    sending={sending}
    onNumberChange={setNumber}
    onSubmit={submit}
    onRefresh={handleRefresh}
    renderQRCode={deps?.renderQRCode}
  />;
}
