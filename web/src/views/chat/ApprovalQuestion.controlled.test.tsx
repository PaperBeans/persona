// @vitest-environment jsdom
// The controlled question editor needs no default-panel request or toast singleton; the old fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Interaction } from "../../lib/types";
import type { ApprovalQuestionProps } from "./ApprovalQuestion";

const mocks = vi.hoisted(() => ({ ask: vi.fn(), toast: vi.fn() }));
vi.mock("../../lib/approval-question", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/approval-question")>(),
  askApprovalQuestion: mocks.ask,
}));
vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  errorText: (error: unknown) => error instanceof Error ? error.message : String(error),
}));
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: mocks.toast,
}));

const { ApprovalQuestion } = await import("./ApprovalQuestion");
const { APPROVAL_QUESTION_MAX } = await import("../../lib/approval-question");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ix = { id: "interaction-1", machine_id: "example-machine", session_id: "example-session", kind: "permission" } as Interaction;
let host: HTMLDivElement;
let root: Root;
let onClose: Mock<() => void>;

const mount = async (props: Partial<ApprovalQuestionProps> = {}) => {
  await act(async () => root.render(<ApprovalQuestion ix={ix} busy={false} onClose={onClose} {...props} />));
};
const area = () => host.querySelector<HTMLTextAreaElement>("[data-approval-question] textarea")!;
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.trim() === label)!;
const send = () => button(t("approval-question.send"));
const type = async (text: string) => act(async () => {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area(), text);
  area().dispatchEvent(new Event("input", { bubbles: true }));
});
const flush = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  onClose = vi.fn();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("ApprovalQuestion with controlled callbacks", () => {
  it("rejects blank and over-limit text and respects the external busy state", async () => {
    await mount({ onSubmitQuestion: vi.fn() });
    expect(send().disabled).toBe(true);
    await type("   ");
    expect(send().disabled).toBe(true);
    await type("😀".repeat(APPROVAL_QUESTION_MAX + 1));
    expect(send().disabled).toBe(true);
    expect(host.textContent).toContain(t("approval-question.too_long"));
    await type("😀".repeat(APPROVAL_QUESTION_MAX));
    expect(send().disabled).toBe(false);
    await act(async () => root.render(<ApprovalQuestion ix={ix} busy onClose={onClose} onSubmitQuestion={vi.fn()} />));
    expect(area().disabled).toBe(true);
    expect(send().disabled).toBe(true);
    expect(mocks.ask).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("disables duplicate submission, then clears and closes after controlled success", async () => {
    let finish!: () => void;
    const onSubmitQuestion = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    const onSuccess = vi.fn();
    const onError = vi.fn();
    await mount({ onSubmitQuestion, onSuccess, onError });
    const text = "  keep the question verbatim  ";
    await type(text);
    act(() => send().click());
    expect(onSubmitQuestion).toHaveBeenCalledWith(text);
    expect(area().disabled).toBe(true);
    expect(send().disabled).toBe(true);
    act(() => send().click());
    expect(onSubmitQuestion).toHaveBeenCalledTimes(1);

    await act(async () => { finish(); await Promise.resolve(); });
    await flush();
    expect(area().value).toBe("");
    expect(onSuccess).toHaveBeenCalledWith(t("approval-question.sent_pending"));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
    expect(mocks.ask).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("reports controlled failure, preserves the draft and becomes retryable", async () => {
    const onSubmitQuestion = vi.fn().mockRejectedValue(new Error("panel offline"));
    const onSuccess = vi.fn();
    const onError = vi.fn();
    await mount({ onSubmitQuestion, onSuccess, onError });
    const text = "  preserve every character\n";
    await type(text);
    act(() => send().click());
    await flush();
    expect(onError).toHaveBeenCalledWith(t("chat-interactions.send_failed", { error: "panel offline" }));
    expect(area().value).toBe(text);
    expect(send().disabled).toBe(false);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.ask).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });
});

describe("ApprovalQuestion default compatibility", () => {
  it("uses the default request and success toast when callbacks are absent", async () => {
    mocks.ask.mockResolvedValue(undefined);
    await mount();
    await type("default question");
    act(() => send().click());
    await flush();
    expect(mocks.ask).toHaveBeenCalledWith(ix, "default question", expect.any(String));
    expect(mocks.toast).toHaveBeenCalledWith(t("approval-question.sent_pending"), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("uses the default error toast and preserves the draft", async () => {
    mocks.ask.mockRejectedValue(new Error("default offline"));
    await mount();
    await type("unsent question");
    act(() => send().click());
    await flush();
    expect(mocks.toast).toHaveBeenCalledWith(t("chat-interactions.send_failed", { error: "default offline" }), "error");
    expect(area().value).toBe("unsent question");
    expect(onClose).not.toHaveBeenCalled();
  });
});
