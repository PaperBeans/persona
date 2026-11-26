// @vitest-environment jsdom
// Controlled comment navigation and CommentBlock handlers avoid default panel, spot, transcript-jump and toast singletons; default fallbacks stay covered.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DraftQuote } from "../../lib/drafts";
import type { CommentBlockProps, CommentNavigationHandlers } from "./CommentCard";

const mocks = vi.hoisted(() => ({
  jumpToComment: vi.fn(),
  openSpot: vi.fn(),
  openPanel: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../../lib/comment-marks", () => ({
  jumpToComment: mocks.jumpToComment,
}));
vi.mock("../../lib/comments", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/comments")>(),
  openSpot: mocks.openSpot,
}));
vi.mock("../../lib/panel", () => ({
  openPanel: mocks.openPanel,
}));
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: mocks.toast,
}));

const { CommentBlock, goToComment } = await import("./CommentCard");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const elementQuote = (overrides: Partial<NonNullable<NonNullable<DraftQuote["comment"]>["element"]>> = {}): DraftQuote => ({
  id: "q-element",
  text: "<button>Save</button>",
  comment: {
    body: "Align this button",
    element: {
      url: "http://localhost:5173/settings?tab=1",
      selector: "button.save",
      omitted: 0,
      mid: "example-machine",
      sid: "example-session",
      cwd: "/example/repo",
      port: 5173,
      ...overrides,
    },
  },
});

const codeQuote = (): DraftQuote => ({
  id: "q-code",
  text: "const count = 1;\nreturn count;",
  comment: {
    body: "Prefer explicit naming",
    spot: {
      file: "src/app.ts",
      path: "/example/repo/src/app.ts",
      from: 10,
      to: 12,
      omitted: 3,
      mid: "example-machine",
      sid: "example-session",
      cwd: "/example/repo",
    },
  },
});

const textQuote = (id?: string): DraftQuote => ({
  ...(id !== undefined ? { id } : {}),
  text: "Selected transcript words",
  comment: {
    body: "Clarify this sentence",
  },
});

let host: HTMLDivElement;
let root: Root;

const mountBlock = async (props: CommentBlockProps) => {
  await act(async () => root.render(<CommentBlock {...props} />));
};

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("CommentCard with controlled handlers", () => {
  it("routes element preview comments through openPreview and onNotice without touching default singletons", () => {
    const openPreview = vi.fn();
    const onNotice = vi.fn();
    const handlers: CommentNavigationHandlers = { openPreview, onNotice };

    const q = elementQuote();
    expect(goToComment(q, handlers)).toBe(true);
    expect(openPreview).toHaveBeenCalledWith({
      mid: "example-machine",
      sid: "example-session",
      cwd: "/example/repo",
      port: 5173,
      path: "/settings?tab=1",
      element: q.comment!.element,
    });
    expect(onNotice).not.toHaveBeenCalled();

    const nonRoot = elementQuote({ url: "settings" });
    expect(goToComment(nonRoot, handlers)).toBe(true);
    expect(openPreview).toHaveBeenLastCalledWith(expect.objectContaining({ path: "/" }));

    openPreview.mockReturnValueOnce(false);
    expect(goToComment(q, handlers)).toBe(false);
    expect(onNotice).toHaveBeenCalledWith(t("chat-comments.jump_code_gone"));

    onNotice.mockClear();
    openPreview.mockClear();
    expect(goToComment(elementQuote({ port: undefined }), handlers)).toBe(false);
    expect(openPreview).not.toHaveBeenCalled();
    expect(onNotice).toHaveBeenCalledWith(t("chat-comments.jump_code_gone"));

    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.openSpot).not.toHaveBeenCalled();
    expect(mocks.jumpToComment).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("routes code spot and text comments through controlled callbacks without touching default singletons", () => {
    const openSpot = vi.fn().mockReturnValue(true);
    const jumpToText = vi.fn().mockReturnValue(true);
    const onNotice = vi.fn();
    const handlers: CommentNavigationHandlers = { openSpot, jumpToText, onNotice };

    const cq = codeQuote();
    expect(goToComment(cq, handlers)).toBe(true);
    expect(openSpot).toHaveBeenCalledWith(cq.comment!.spot);
    expect(onNotice).not.toHaveBeenCalled();

    openSpot.mockReturnValueOnce(false);
    expect(goToComment(cq, handlers)).toBe(false);
    expect(onNotice).toHaveBeenLastCalledWith(t("chat-comments.jump_code_gone"));

    expect(goToComment(textQuote("q-text"), handlers)).toBe(true);
    expect(jumpToText).toHaveBeenCalledWith("q-text");

    jumpToText.mockReturnValueOnce(false);
    expect(goToComment(textQuote("q-missing"), handlers)).toBe(false);
    expect(onNotice).toHaveBeenLastCalledWith(t("chat-comments.jump_text_gone"));

    onNotice.mockClear();
    expect(goToComment(textQuote(undefined), handlers)).toBe(false);
    expect(onNotice).toHaveBeenCalledWith(t("chat-comments.jump_text_gone"));

    onNotice.mockClear();
    expect(goToComment({ text: "plain quote" }, handlers)).toBe(false);
    expect(onNotice).not.toHaveBeenCalled();

    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.openSpot).not.toHaveBeenCalled();
    expect(mocks.jumpToComment).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("renders CommentBlock with controlled navigation and gives explicit open precedence", async () => {
    const openSpot = vi.fn().mockReturnValue(true);
    const onNotice = vi.fn();
    const cq = codeQuote();

    await mountBlock({ quote: cq, n: 2, navigation: { openSpot, onNotice } });

    const block = host.querySelector('[data-comment-block="2"]')!;
    expect(block.textContent).toContain(t("chat-comments.comment_n", { n: 2 }));
    expect(block.textContent).toContain("src/app.ts:10-12");
    expect(block.textContent).toContain(t("chat-comments.omitted", { count: 3 }));
    expect(block.textContent).toContain("Prefer explicit naming");

    const trigger = block.querySelector<HTMLButtonElement>("button")!;
    act(() => trigger.click());
    expect(openSpot).toHaveBeenCalledWith(cq.comment!.spot);
    expect(onNotice).not.toHaveBeenCalled();

    const customOpen = vi.fn();
    openSpot.mockClear();
    await mountBlock({ quote: cq, n: 2, open: customOpen, navigation: { openSpot, onNotice } });
    act(() => host.querySelector<HTMLButtonElement>('[data-comment-block="2"] button')!.click());
    expect(customOpen).toHaveBeenCalledTimes(1);
    expect(openSpot).not.toHaveBeenCalled();

    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.openSpot).not.toHaveBeenCalled();
    expect(mocks.jumpToComment).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });
});

describe("CommentCard default compatibility", () => {
  it("uses default openPanel, openSpot, jumpToComment and toast when handlers are absent", () => {
    expect(goToComment(elementQuote())).toBe(true);
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "preview",
      mid: "example-machine",
      sid: "example-session",
      cwd: "/example/repo",
      port: 5173,
      path: "/settings?tab=1",
    });

    expect(goToComment(elementQuote({ cwd: undefined }))).toBe(false);
    expect(mocks.toast).toHaveBeenLastCalledWith(t("chat-comments.jump_code_gone"));

    mocks.openSpot.mockReturnValueOnce(true);
    const cq = codeQuote();
    expect(goToComment(cq)).toBe(true);
    expect(mocks.openSpot).toHaveBeenCalledWith(cq.comment!.spot);

    mocks.openSpot.mockReturnValueOnce(false);
    expect(goToComment(cq)).toBe(false);
    expect(mocks.toast).toHaveBeenLastCalledWith(t("chat-comments.jump_code_gone"));

    mocks.jumpToComment.mockReturnValueOnce(true);
    expect(goToComment(textQuote("q-default"))).toBe(true);
    expect(mocks.jumpToComment).toHaveBeenCalledWith("q-default");

    mocks.jumpToComment.mockReturnValueOnce(false);
    expect(goToComment(textQuote("q-gone"))).toBe(false);
    expect(mocks.toast).toHaveBeenLastCalledWith(t("chat-comments.jump_text_gone"));
  });

  it("keeps CommentBlock non-interactive when neither open nor navigation is supplied", async () => {
    await mountBlock({ quote: textQuote(), n: 1 });
    const block = host.querySelector('[data-comment-block="1"]')!;
    expect(block.querySelector("button")).toBeNull();
    expect(block.textContent).toContain("Selected transcript words");
    expect(block.textContent).toContain("Clarify this sentence");

    const open = vi.fn();
    await mountBlock({ quote: textQuote(), n: 1, open });
    act(() => host.querySelector<HTMLButtonElement>('[data-comment-block="1"] button')!.click());
    expect(open).toHaveBeenCalledTimes(1);
    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.openSpot).not.toHaveBeenCalled();
    expect(mocks.jumpToComment).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });
});
