import { useEffect, useRef, useState } from "react";
import { api, enc, errorText, isAbort } from "../../lib/api";
import { bytes, clock } from "../../lib/format";
import { isDone, itemKey, withToken, type InboxItem } from "../../lib/inbox";
import { defaultManager, itemHeaders, useItemManager, withManager } from "../../lib/manager-choice";
import { openLink } from "../../lib/links";
import { useMailAccounts } from "../../lib/mail";
import { addressText } from "../../lib/mail-stored";
import { markMailSeen, replyAllDraft, replyDraft, type ComposeDraft, type MailMessage } from "./mail";
import { hubTranslation, isForeignCached, languageName, strippedHTML, translateMail, translatesMail, useTranslatePrefs, type MailTranslation } from "../../lib/translate";
import { t } from "../../i18n";
import { Button, cx, Loading, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ComposeDialog } from "./ComposeDialog";
import { Glyph } from "../../views/inbox/InboxParts";

export function FullMailBody({ item, onLatest }: { item: InboxItem; onLatest?: () => void }) {
  const m = item.mail!;
  const uid = m.uids[m.uids.length - 1];
  const mailbox = m.mailbox || "INBOX";
  const [msg, setMsg] = useState<MailMessage | null>(null);
  const [error, setError] = useState("");
  // "Reply" open: its template as it opened, so the dialog stays over a new mail of the thread read under it (#4563)
  const [replying, setReplying] = useState<{ item: string; mode: "reply" | "reply-all"; initial: ComposeDraft } | null>(null);
  const accounts = useMailAccounts();
  const prefs = useTranslatePrefs();
  const [translation, setTranslation] = useState<MailTranslation | null>(null);
  const [translating, setTranslating] = useState(false);
  const [translateError, setTranslateError] = useState("");
  const [showOriginal, setShowOriginal] = useState(false);
  const path = `/mail/messages/${enc(m.account_id)}/${uid}`;
  const unread = !isDone(item) && item.read !== true;
  const manager = useItemManager();

  useEffect(() => {
    let live = true;
    const abort = new AbortController();
    setMsg(null);
    setError("");
    api<{ message: MailMessage }>(`${path}?mailbox=${enc(mailbox)}`, { headers: itemHeaders(manager), signal: abort.signal })
      .then((r) => {
        if (!live) return;
        setMsg(r.message);
        if (unread && r.message?.seen !== true) void markMailSeen(m.account_id, uid, mailbox, manager);
      })
      .catch((e) => { if (live && !isAbort(e)) setError(errorText(e)); });
    return () => {
      live = false;
      abort.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, mailbox, manager]);

  // Settings › Translation: a mail in another language than the target is asked of the hub once it is here.
  const wants = translatesMail(prefs);
  useEffect(() => {
    setTranslation(null);
    setTranslateError("");
    setShowOriginal(false);
    if (!msg || !wants) return;
    const body = msg.text?.trim() ? msg.text : strippedHTML(msg.html ?? "");
    if (!isForeignCached(`${msg.subject ?? ""}\n${body}`, prefs.target)) return;
    let live = true;
    setTranslating(true);
    translateMail(m.account_id, uid, mailbox, prefs.target, manager)
      .then((tr) => live && setTranslation(tr))
      .catch((e) => live && setTranslateError(errorText(e)))
      .finally(() => live && setTranslating(false));
    return () => {
      live = false;
      setTranslating(false);
    };
  }, [msg, wants, prefs.target, m.account_id, uid, mailbox, manager]);

  // kept by the item (the thread): a new mail of it moves `uid` on and reads the body again under the dialog
  // another item opened in this body: that reply's dialog goes (and keeps what was typed for its own item)
  const replyItem = itemKey({ id: item.id, manager: item.manager || defaultManager() });
  const replyKey = replying && `${replying.mode}:${replying.item}`;
  const dialog = replying?.item === replyItem && <ComposeDialog key={replyKey} draftKey={replyKey!} initial={replying.initial} accounts={accounts} onClose={() => setReplying((current) => current === replying ? null : current)} />;
  if (!msg) return <div className="p-5">{error ? <p className="text-[12.5px] text-red">{error}</p> : <Loading label={t("inbox-inboxbodies.reading_mail")} />}{dialog}</div>;
  const shown = showOriginal ? null : translation;
  const lang = languageName(prefs.target);
  const allDraft = replyAllDraft(m.account_id, msg, accounts.find((a) => a.id === m.account_id)?.email);
  return (
    <div className="mx-auto max-w-[760px] px-4 py-4 sm:px-6">
      {onLatest && (
        <div className="mb-3 flex justify-center">
          <Button tone="quiet" icon="chevronDown" onClick={onLatest}>{t("inbox-inboxbodies.latest")}</Button>
        </div>
      )}
      <div className="flex flex-col items-start gap-3 sm:flex-row">
        <dl className="grid min-w-0 flex-1 grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]">
          <dt className="text-ink-3">{t("inbox-inboxbodies.from")}</dt>
          <dd className="min-w-0 truncate text-ink">{addressText(msg.from)}</dd>
          {!!msg.to?.length && (<><dt className="text-ink-3">{t("inbox-inboxbodies.to")}</dt><dd className="min-w-0 truncate text-ink-2">{msg.to.map(addressText).join(t("inbox-inboxbodies.address_sep"))}</dd></>)}
          {!!msg.cc?.length && (<><dt className="text-ink-3">{t("inbox-inboxbodies.cc")}</dt><dd className="min-w-0 truncate text-ink-2">{msg.cc.map(addressText).join(t("inbox-inboxbodies.address_sep"))}</dd></>)}
          {msg.date && (<><dt className="text-ink-3">{t("inbox-inboxbodies.date")}</dt><dd className="text-ink-2">{clock(msg.date)}</dd></>)}
        </dl>
        <div className="flex shrink-0 gap-2">
          <Button tone="secondary" onClick={() => setReplying({ item: replyItem, mode: "reply", initial: replyDraft(m.account_id, msg) })} title={t("inbox-inboxbodies.reply_title")}>
            <Glyph name="reply" size={14} stroke={2.1} />
            {t("inbox-inboxbodies.reply")}
          </Button>
          <Button tone="secondary" disabled={!allDraft} onClick={() => allDraft && setReplying({ item: replyItem, mode: "reply-all", initial: allDraft })} title={t(allDraft ? "inbox-inboxbodies.reply_all_title" : "inbox-inboxbodies.reply_all_unavailable")}>
            {t("inbox-inboxbodies.reply_all")}
          </Button>
        </div>
      </div>
      {(translating || translation || translateError) && (
        <div className="mt-3 flex min-h-8 items-center gap-2 rounded-control bg-inset px-3 py-1.5 text-[12px] text-ink-3 shadow-hairline">
          {translating ? (
            <>
              <Spinner />
              <span className="truncate">{t("inbox-inboxbodies.translating", { language: lang })}</span>
            </>
          ) : translation ? (
            <>
              <Icon name="translate" size={13} className="shrink-0" />
              <span className="min-w-0 flex-1 truncate">{showOriginal ? t("inbox-inboxbodies.original") : t("inbox-inboxbodies.translated_to", { language: lang })}</span>
              <button type="button" onClick={() => setShowOriginal((v) => !v)} className="shrink-0 rounded-[6px] px-1.5 py-0.5 font-medium whitespace-nowrap text-accent-ink hover:bg-hover">
                {showOriginal ? t("inbox-inboxbodies.show_translation") : t("inbox-inboxbodies.show_original")}
              </button>
            </>
          ) : (
            <>
              <Icon name="info" size={13} className="shrink-0" />
              <span className="min-w-0 flex-1 truncate" title={translateError}>{t("inbox-inboxbodies.translate_failed", { error: translateError })}</span>
            </>
          )}
        </div>
      )}
      <div className="mt-4">
        {shown ? (
          <div className="rounded-card bg-surface px-[18px] py-4 shadow-card">
            {shown.subject && shown.subject !== (hubTranslation(item, prefs)?.subject || item.title) && <p className="mb-2 text-[14px] font-semibold text-ink">{shown.subject}</p>}
            <div className="text-[13.5px] leading-relaxed break-words whitespace-pre-wrap text-ink">{shown.text}</div>
            {shown.clipped && <p className="mt-3 text-[12px] text-ink-3">{t("inbox-inboxbodies.clipped")}</p>}
          </div>
        ) : msg.html ? <MailFrame html={msg.html} /> : <div className="text-[13.5px] leading-relaxed break-words whitespace-pre-wrap text-ink">{msg.text || t("inbox-inboxbodies.no_body")}</div>}
      </div>
      {!!msg.attachments?.length && (
        <div className="mt-5">
          <p className="mb-1.5 text-[12px] font-medium text-ink-3">{t("inbox-inboxbodies.attachments", { count: msg.attachments.length })}</p>
          <div className="flex flex-wrap gap-2">
            {msg.attachments.map((a, i) => {
              const url = a.id ? withToken(withManager(`/api/v1${path}/attachments/${enc(a.id)}?mailbox=${enc(mailbox)}`, manager)) : undefined;
              const body = (
                <>
                  <Icon name="clip" size={13} className="shrink-0 text-ink-3" />
                  <span className="truncate">{a.filename || t("inbox-inboxbodies.attachment")}</span>
                  {a.size !== undefined && <span className="shrink-0 text-ink-3">{bytes(a.size)}</span>}
                </>
              );
              const cls = "flex max-w-full items-center gap-2 rounded-[8px] bg-surface px-2.5 py-1.5 text-[12.5px] text-ink shadow-card";
              return url ? <a key={i} href={url} download={a.filename} className={cx(cls, "hover:bg-hover")}>{body}</a> : <span key={i} className={cls}>{body}</span>;
            })}
          </div>
        </div>
      )}
      {dialog}
    </div>
  );
}

// Mail HTML is somebody else's page: no scripts, no forms (sandbox without allow-scripts / allow-forms). Same origin
// only so the page can size the frame to its content and open its links like every other link (openLink).
const FRAME_HEAD =
  '<!doctype html><html><head><meta charset="utf-8"><base target="_blank"><style>' +
  ":root{color-scheme:light}html{background:Canvas;color:CanvasText}" +
  'body{margin:0;padding:16px 18px;font:14px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif;overflow-wrap:anywhere}' +
  "img{max-width:100%;height:auto}table{max-width:100%}pre{white-space:pre-wrap}</style></head><body>";

function MailFrame({ html }: { html: string }) {
  const ref = useRef<HTMLIFrameElement>(null);
  const manager = useItemManager();
  const [height, setHeight] = useState(240);
  // undoes what the last load hooked onto the frame's document. The observer lives in this window: left connected, it
  // keeps that document and this frame's state alive, one more on each load and on each frame that goes (a new mail of
  // the thread, Show translation, another item).
  const release = useRef<(() => void) | null>(null);
  useEffect(() => () => release.current?.(), []);

  const onLoad = () => {
    release.current?.();
    release.current = null;
    const doc = ref.current?.contentDocument;
    if (!doc) return;
    const fit = () => setHeight(Math.max(80, Math.ceil(doc.body.getBoundingClientRect().height)));
    fit();
    const sizes = new ResizeObserver(fit);
    sizes.observe(doc.body);
    const click = (e: MouseEvent) => {
      const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!a) return;
      e.preventDefault();
      if (/^(https?:|mailto:)/i.test(a.href)) openLink(a.href, undefined, manager);
    };
    doc.addEventListener("click", click);
    release.current = () => {
      sizes.disconnect();
      doc.removeEventListener("click", click);
    };
  };

  return (
    <iframe
      ref={ref}
      title={t("inbox-inboxbodies.mail_frame")}
      sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
      srcDoc={FRAME_HEAD + html + "</body></html>"}
      onLoad={onLoad}
      className="block w-full overflow-hidden rounded-card shadow-card"
      style={{ height, colorScheme: "light" }}
    />
  );
}
