// @vitest-environment jsdom
// A message's send time (#5285): a hover shows it for your own bubbles (the side bar) and for a turn's last reply
// (ReplyBar), and the chat's last message keeps it on screen.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { clock } from "../../lib/format";
import { segments } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { LatestMessageTime } from "./latest-message-time";
import { MessageActions } from "./MessageActions";
import { ReplyBar } from "./ReplyBar";
import { SegmentView } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ts = "2026-11-12T09:59:01Z";
const earlier = "2026-11-12T09:58:01Z";

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await initI18n("en");
  vi.useFakeTimers({ now: new Date("2026-11-12T10:00:00Z") });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

const own = (latest?: string) =>
  renderToStaticMarkup(
    <LatestMessageTime.Provider value={latest}>
      <MessageActions text="my message" isUser ts={ts} side messageId="u1">
        <p>my message</p>
      </MessageActions>
    </LatestMessageTime.Provider>,
  );

const count = (html: string, s: string) => html.split(s).length - 1;

async function replyBar(pinned?: boolean) {
  await act(async () => root.render(<ReplyBar text="All done." ts={ts} more={[]} pinned={pinned} />));
  const bar = host.querySelector<HTMLElement>("[data-reply-bar]")!;
  return { bar, time: bar.querySelector("time")!, buttons: bar.querySelector("button")!.parentElement! };
}

describe("message send time", () => {
  it("your own message shows its time in the side hover bar", () => {
    const html = own(earlier);
    expect(count(html, `>${clock(ts)}<`)).toBe(1);
    expect(html).not.toContain("data-latest-time");
  });

  it("your own last message keeps its time under the bubble, not in the bar", () => {
    expect(count(own(ts), `>${clock(ts)}<`)).toBe(0);
    const item: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "check CI", client_msg_id: "c1", ts };
    const seg = segments([item])[0];
    const view = (latest?: string) =>
      renderToStaticMarkup(
        <LatestMessageTime.Provider value={latest}>
          <SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={async () => {}} />
        </LatestMessageTime.Provider>,
      );
    expect(view(ts)).toContain("data-latest-time");
    expect(view(earlier)).not.toContain("data-latest-time");
    expect(view(undefined)).not.toContain("data-latest-time");
  });

  it("a last reply that is not the chat's last shows its time only with the bar", async () => {
    const { bar, time } = await replyBar();
    expect(time.textContent).toBeTruthy();
    expect(bar.classList.contains("opacity-0")).toBe(true);
  });

  it("the chat's last reply keeps its time on screen; its buttons still wait for the pointer", async () => {
    const { bar, time, buttons } = await replyBar(true);
    expect(bar.classList.contains("opacity-0")).toBe(false);
    expect(time.closest(".opacity-0")).toBeNull();
    expect(buttons.classList.contains("opacity-0")).toBe(true);
  });

  it("the pinned time keeps up without the pointer", async () => {
    const { time } = await replyBar(true);
    const before = time.textContent;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60 * 60_000);
    });
    expect(time.textContent).not.toBe(before);
  });
});
