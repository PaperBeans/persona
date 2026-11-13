// @vitest-environment jsdom
// A model error is cindy's error notice. The one the chat stopped on is the banner over the composer (ErrorBanner, #5313)
// with what can be done about it now: "Retry" when it can help, the text-derived "Fix it" in Persona's own
// conversations only, "Edit last message" for the safeguards error, and ✕. In the chat it is a record without them.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { parseAPIError } from "../../lib/api-errors";
import type { ChatLine } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { ChatLineView, ErrorBanner, errorKey } from "./Lines";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type ErrorLine = Extract<ChatLine, { kind: "apiError" }>;
function line(text: string): ErrorLine {
  const item = { id: "e1", seq: 1, role: "assistant", kind: "text", text } as Item;
  return { kind: "apiError", id: "err-e1", item, info: parseAPIError(text)!, count: 1 };
}
const banner = (text: string, assistant: boolean, extra: Partial<Parameters<typeof ErrorBanner>[0]> = {}) =>
  renderToStaticMarkup(<ErrorBanner line={line(text)} onRetry={() => {}} onDismiss={() => {}} assistant={assistant} {...extra} />);
const inline = (text: string, assistant = false) => renderToStaticMarkup(<ChatLineView line={line(text)} mid="m" sid="s" expandFold={async () => {}} assistant={assistant} />);

describe("a model error's fix", () => {
  it("offers 去修 in a Persona conversation", () => {
    expect(banner("Invalid API key · Fix external API key", true)).toContain("去修");
    // a record in the chat keeps the fix its own text names
    expect(inline("Invalid API key · Fix external API key", true)).toContain("去修");
  });

  it("does not point an agent session at Persona's settings", () => {
    expect(banner("Invalid API key · Fix external API key", false)).not.toContain("去修");
  });

  it("keeps 重试 on an agent session's passing error", () => {
    const html = banner("API Error: 529 overloaded", false);
    expect(html).toContain("重试");
    expect(html).not.toContain("去修");
  });

  it("recognizes safeguards as requiring action and offers editing only on a supported agent chat", () => {
    const text = "API Error: Opus 5.5's safeguards flagged this message. Details: [frontier_llm]";
    const info = parseAPIError(text)!;
    expect(info.code).toBe("safeguards");
    expect(info.transient).toBe(false);
    const html = banner(text, false, { onEditLastMessage: () => {} });
    expect(html).toContain("编辑上一条消息");
    expect(html).toContain("模型的安全检查拦截了这条消息");
    expect(html).not.toContain(">重试</button>");
    expect(banner(text, false)).not.toContain("编辑上一条消息");
    expect(banner(text, true, { onEditLastMessage: () => {} })).not.toContain("编辑上一条消息");
  });
});

describe("a model error is cindy's error notice (#5157)", () => {
  const gpu = "API Error: 502 upstream error: Req 202610302115A1B2C3D4E5F6 Session 1ebd0000:7e470000 no available GPU pod";

  it("is a neutral card: the red mark only, what happened, what helps, then Retry and Error details", () => {
    const html = banner(gpu, false);
    expect(html).toContain("data-error-notice");
    expect(html).toContain("border border-line bg-surface");
    expect(html).not.toMatch(/bg-red-tint|var\(--red\)_22%/);
    expect(html).toContain('class="mt-0.5 inline-flex shrink-0 text-red"');
    expect(html).toContain("推理服务暂时没有可用的 GPU");
    expect(html).toContain("临时故障，过一会儿多半会好");
    expect(html).toContain("重试");
    // a 28 px action keeps a finger-sized target on a touch screen
    expect(html).toMatch(/<button[^>]*class="[^"]*touch-hit[^"]*"[^>]*>(?:(?!<\/button>).)*重试<\/button>/);
    expect(html).toContain('title="继续当前任务"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain("错误详情");
    // the head stays quiet: the status code, the ids and the original wait under Error details
    expect(html).not.toContain(">502<");
    expect(html).not.toContain("1ebd0000:7e470000");
  });

  it("holds the ids, the status code, how many times and the original under Error details", () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const l = { ...line(gpu), count: 3 };
    act(() => root.render(<ChatLineView line={l} mid="m" sid="s" expandFold={async () => {}} />));
    const toggle = [...host.querySelectorAll("button")].find((b) => b.textContent === "错误详情")!;
    act(() => toggle.click());
    const details = host.querySelector("[data-error-details]")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(toggle.textContent).toBe("收起详情");
    expect(details.textContent).toContain("202610302115A1B2C3D4E5F6");
    expect(details.textContent).toContain("1ebd0000:7e470000");
    expect(details.textContent).toContain("502");
    expect(details.textContent).toContain("连续 3 次");
    expect(details.querySelector("pre")!.textContent).toBe(gpu);
    expect(details.textContent).toContain("复制原文");
    act(() => root.unmount());
    host.remove();
  });

  it("draws a usage limit as the same notice, saying when it resets", () => {
    const html = renderToStaticMarkup(<ChatLineView line={line(gpu)} mid="m" sid="s" expandFold={async () => {}} limit="用量上限 · 15:00 重置" />);
    expect(html).toContain("data-error-notice");
    expect(html).toContain('role="status"');
    expect(html).toContain("用量上限 · 15:00 重置");
    expect(html).not.toContain("错误详情");
    expect(html).not.toContain("重试");
  });
});

describe("the error the chat stopped on, over the composer (#5313)", () => {
  const gpu = "API Error: 502 upstream error: no available GPU pod";

  it("is the same card with ✕ in its top corner: cindy's 28 px round button, labelled", () => {
    const html = banner(gpu, false);
    expect(html).toContain("data-error-banner");
    // out of the transcript, it is announced as a status of its own; the record in the chat is not
    expect(html).toMatch(/<div[^>]*data-error-banner=""[^>]*role="status"/);
    expect(inline(gpu)).not.toContain('role="status"');
    expect(html).toMatch(/<button[^>]*aria-label="关闭错误提示"[^>]*title="关闭错误提示"[^>]*class="touch-hit -mt-1 -mr-1 flex size-7 shrink-0 items-center justify-center rounded-full text-ink-2/);
  });

  it("✕ is the caller's to decide; Retry calls the retry", () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const onRetry = vi.fn();
    const onDismiss = vi.fn();
    act(() => root.render(<ErrorBanner line={line(gpu)} onRetry={onRetry} onDismiss={onDismiss} />));
    act(() => host.querySelector<HTMLButtonElement>('[aria-label="关闭错误提示"]')!.click());
    expect(onDismiss).toHaveBeenCalledTimes(1);
    act(() => [...host.querySelectorAll("button")].find((b) => b.textContent === "重试")!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
    act(() => root.unmount());
    host.remove();
  });

  it("in the chat it is a record: no Retry, no Edit last message, no ✕", () => {
    const html = inline(gpu);
    expect(html).toContain("data-error-notice");
    expect(html).not.toContain("重试");
    expect(html).not.toContain("关闭错误提示");
    expect(html).not.toContain("data-error-banner");
    expect(inline("API Error: Opus 5.5's safeguards flagged this message. Details: [frontier_llm]")).not.toContain("编辑上一条消息");
  });

  it("says a usage limit as the banner too, with ✕ and no Retry", () => {
    const html = banner(gpu, false, { limit: "用量上限 · 15:00 重置" });
    expect(html).toContain("用量上限 · 15:00 重置");
    expect(html).toContain('role="status"');
    expect(html).toContain("关闭错误提示");
    expect(html).not.toContain("重试");
  });

  it("is a new error once the same one comes again after a retry (the line folds with a higher count)", () => {
    expect(errorKey(line(gpu))).not.toBe(errorKey({ ...line(gpu), count: 2 }));
  });
});
