// The bar under a finished turn's last reply (#5314), after cindy's MessageActionBar as it sits under an assistant
// message, at the owner's request (2026-11-12): Copy message (a ✓ once copied, the copy icon again on the next hover),
// Start a new session where the session can branch, ⋯ More message actions (Quote, Forward, Use as context), then when
// the reply was written ("5 minutes ago", the exact time as its tooltip). The pointer over the reply (the element the bar
// is in: MessageActions') or keyboard focus in the bar shows it; it stays REPLY_BAR_LINGER_MS after the pointer leaves,
// then fades (at once with reduced motion), and an open ⋯ menu keeps it. Narration inside a work summary and a reply
// still streaming have no bar: their right-click / long-press menu (MessageActions) has every action, and touch screens
// have MessageActions' copy + ⋯ row instead. The chat's last reply (`pinned`, #5285) keeps its time on screen in its
// place at the end of the bar; the buttons still come only with the pointer. After the time, what the exchange cost or its
// tokens, with cindy's usage card (TurnUsage, #5426): it comes with the pointer like the buttons (cindy shows it on hover),
// and an open card keeps the bar. All of it watches the pointer itself, so the reply around it draws nothing again, and
// none of it is on the first screen (MessageActions loads it lazily).
import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { exactTime, relativeTime } from "../../lib/relative-time";
import { copyText, cx, Menu } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { REPLY_BAR_FADE_MS, REPLY_BAR_LINGER_MS } from "../../ui/motion";
import type { Action } from "./MessageActions";
import { TurnUsage } from "./TurnUsage";

export { TurnUsage };

/** How often the time is worked out again while the pointer is over the reply (cindy's 30 s): only then, so a long chat
 *  holds no timer per reply. */
const TIME_TICK_MS = 30_000;

const BUTTON = "flex size-6 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:bg-hover-2";

/** Copies without a toast (the ✓ says it); without the async clipboard (plain http), copyText's way, which says it. */
async function copyQuietly(text: string): Promise<boolean> {
  const done = await navigator.clipboard?.writeText(text).then(() => true, () => false);
  return done || copyText(text);
}

/** `fork`: "Start a new session", when the session can. `more`: the ⋯ menu's actions; no ⋯ without any. `pinned`: the
 *  chat's last message, whose time stays shown without the pointer. `usage`: what the exchange used (lib/turn-usage.ts
 *  usageKey), `usageMs` its work time, `persona` a Persona conversation's plain cost. */
export function ReplyBar({ text, ts, fork, more, pinned, usage, usageMs, persona }: { text: string; ts?: string; fork?: Action; more: Action[]; pinned?: boolean; usage?: string; usageMs?: number; persona?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [hovered, setHovered] = useState(false);
  const [visible, setVisible] = useState(false);
  const [copied, setCopied] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const [card, setCard] = useState(false);

  // the pointer over the reply: the element this bar is in
  useEffect(() => {
    const reply = ref.current?.parentElement;
    if (!reply) return;
    const enter = () => setHovered(true);
    const leave = () => setHovered(false);
    reply.addEventListener("mouseenter", enter);
    reply.addEventListener("mouseleave", leave);
    if (reply.matches(":hover")) enter(); // it came in while the pointer was there
    return () => {
      reply.removeEventListener("mouseenter", enter);
      reply.removeEventListener("mouseleave", leave);
    };
  }, []);

  useEffect(() => {
    if (!hovered) {
      // a quick way back in cancels this, so the bar does not flicker
      const linger = window.setTimeout(() => setVisible(false), REPLY_BAR_LINGER_MS);
      return () => clearTimeout(linger);
    }
    setVisible(true);
    // the ✓ goes on the way back in, not as the bar fades (it would snap back mid-fade)
    setCopied(false);
    setNow(Date.now());
    const tick = window.setInterval(() => setNow(Date.now()), TIME_TICK_MS);
    return () => clearInterval(tick);
  }, [hovered]);

  // a pinned reply's time is on screen without the pointer, so it keeps up by itself (one reply in a chat is pinned)
  useEffect(() => {
    if (!pinned) return;
    setNow(Date.now());
    const tick = window.setInterval(() => setNow(Date.now()), TIME_TICK_MS);
    return () => clearInterval(tick);
  }, [pinned]);

  const time = relativeTime(ts, now);
  // with the pointer, an open ⋯ menu or keyboard focus in it: the whole bar; otherwise nothing, or (`pinned`) the time only
  const fade = visible || menu || card ? "opacity-100" : pinned ? "pointer-events-none opacity-0 group-has-[:focus-visible]/bar:pointer-events-auto group-has-[:focus-visible]/bar:opacity-100" : "pointer-events-none opacity-0 has-[:focus-visible]:pointer-events-auto has-[:focus-visible]:opacity-100";
  const fadeStyle = { transitionDuration: `${REPLY_BAR_FADE_MS}ms` };
  const copyLabel = t("chat-messageactions.copy_message");
  const moreLabel = t("chat-replybar.more_actions");
  return (
    <div
      ref={ref}
      data-reply-bar
      // hidden on touch screens, which have the copy + ⋯ row (MessageActions); the keyboard shows it (:focus-visible,
      // so a click does not keep it after the pointer leaves)
      className={cx(
        "mt-1.5 hidden h-6 items-center gap-0.5 transition-opacity [@media(hover:hover)]:flex",
        pinned ? "group/bar" : fade,
      )}
      style={fadeStyle}
      onFocus={() => setNow(Date.now())}
    >
      <div className={cx("flex items-center gap-0.5", pinned && cx("transition-opacity", fade))} style={pinned ? fadeStyle : undefined}>
        <button
          type="button"
          aria-label={copyLabel}
          title={copyLabel}
          onClick={async () => {
            if (await copyQuietly(text)) setCopied(true);
          }}
          className={cx(BUTTON, !copied && "hover:text-ink")}
        >
          <Icon name={copied ? "check" : "copy"} size={14} />
        </button>
        {fork && (
          <button type="button" aria-label={fork.label} title={fork.label} onClick={() => void fork.onSelect()} className={cx(BUTTON, "hover:text-ink")}>
            <Icon name={fork.icon} size={14} />
          </button>
        )}
        {more.length > 0 && (
          <button
            type="button"
            aria-label={moreLabel}
            title={moreLabel}
            aria-haspopup="menu"
            aria-expanded={Boolean(menu)}
            onClick={(e) => setMenu(menu ? null : e.currentTarget)}
            className={cx(BUTTON, "hover:text-ink", menu && "bg-hover-2 text-ink")}
          >
            <Icon name="more" size={14} />
          </button>
        )}
      </div>
      {time && (
        <time dateTime={ts} title={exactTime(ts)} className="flex h-6 cursor-default items-center text-[12px] whitespace-nowrap text-ink-3">
          {time}
        </time>
      )}
      {usage &&
        (pinned ? (
          // a pinned reply's time stays; what it cost comes with the pointer, as the buttons do
          <span data-usage-fade className={cx("flex items-center transition-opacity", fade)} style={fadeStyle}>
            <TurnUsage usage={usage} ms={usageMs} persona={persona} onOpen={setCard} />
          </span>
        ) : (
          <TurnUsage usage={usage} ms={usageMs} persona={persona} onOpen={setCard} />
        ))}
      {menu && <Menu anchor={menu} onClose={() => setMenu(null)} items={more} width={more.some((a) => a.hint) ? 200 : 184} />}
    </div>
  );
}
