// @vitest-environment jsdom
// Controlled failure notes accept explicit reason, external link, and open callback without reading default Session formatters; default callers keep existing fallbacks.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Session } from "../../lib/types";
import type { FailureNoteProps } from "./FailureNote";

const mocks = vi.hoisted(() => ({
  failureReason: vi.fn(),
  externalLink: vi.fn(),
}));

vi.mock("../../lib/format", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/format")>();
  return {
    ...actual,
    failureReason: (...args: Parameters<typeof actual.failureReason>) => mocks.failureReason(...args),
    externalLink: (...args: Parameters<typeof actual.externalLink>) => mocks.externalLink(...args),
  };
});

const { FailureNote } = await import("./FailureNote");
const { externalLink: actualExternalLink, failureReason: actualFailureReason } = await vi.importActual<typeof import("../../lib/format")>("../../lib/format");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const session: Session = {
  machine_id: "example-machine",
  session_id: "example-session",
  title: "Example",
  cwd: "/workspace",
  status: "idle",
  failure_reason: "⚠️ API Error: 529 overloaded",
  external_url: "https://example.com/default-chat",
};

let host: HTMLDivElement;
let root: Root;
let onRetry: Mock<() => void>;

const mount = async (props: Partial<FailureNoteProps> = {}) => {
  await act(async () => root.render(<FailureNote onRetry={onRetry} {...props} />));
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.failureReason.mockImplementation(actualFailureReason);
  mocks.externalLink.mockImplementation(actualExternalLink);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  onRetry = vi.fn();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("FailureNote with controlled props", () => {
  it("renders explicit reason and routes external open through onOpenExternal without calling default helpers", async () => {
    const onOpenExternal = vi.fn();
    await mount({
      reason: "Panel turn failed: rate limited",
      externalUrl: "https://example.com/panel-chat",
      onOpenExternal,
    });

    const note = host.querySelector<HTMLElement>('[data-testid="chat-failure"]');
    expect(note).not.toBeNull();
    expect(note?.textContent).toContain("Panel turn failed: rate limited");
    expect(note?.textContent).toContain(t("chat-chatview.retry"));
    expect(note?.textContent).toContain(t("chat-chatview.open_in_lark"));

    act(() => host.querySelector<HTMLButtonElement>('[data-testid="chat-failure-retry"]')!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);

    const externalAction = host.querySelector<HTMLElement>('[data-testid="chat-failure-lark"]')!;
    expect(externalAction.tagName).toBe("BUTTON");
    expect(externalAction.getAttribute("href")).toBeNull();
    act(() => externalAction.click());
    expect(onOpenExternal).toHaveBeenCalledWith("https://example.com/panel-chat");

    expect(mocks.failureReason).not.toHaveBeenCalled();
    expect(mocks.externalLink).not.toHaveBeenCalledWith(session);
  });

  it("hides when explicit reason is null or blank, and rejects non-https or cleared externalUrl", async () => {
    await mount({ session, reason: null, externalUrl: "https://example.com/panel-chat" });
    expect(host.querySelector('[data-testid="chat-failure"]')).toBeNull();
    expect(mocks.failureReason).not.toHaveBeenCalled();
    expect(mocks.externalLink).not.toHaveBeenCalled();

    await mount({ session, reason: "   ", externalUrl: "https://example.com/panel-chat" });
    expect(host.querySelector('[data-testid="chat-failure"]')).toBeNull();

    await mount({ session, reason: "Controlled failure", externalUrl: "javascript:alert(1)" });
    expect(host.querySelector('[data-testid="chat-failure"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="chat-failure-lark"]')).toBeNull();

    await mount({ session, reason: "Controlled failure", externalUrl: "http://example.com/insecure" });
    expect(host.querySelector('[data-testid="chat-failure-lark"]')).toBeNull();

    await mount({ session, reason: "Controlled failure", externalUrl: null });
    expect(host.querySelector('[data-testid="chat-failure-lark"]')).toBeNull();

    await mount({ reason: "Controlled failure", externalUrl: "https://example.com/direct-link" });
    const anchor = host.querySelector<HTMLAnchorElement>('[data-testid="chat-failure-lark"]')!;
    expect(anchor.tagName).toBe("A");
    expect(anchor.href).toBe("https://example.com/direct-link");
    expect(anchor.target).toBe("_blank");

    expect(mocks.failureReason).not.toHaveBeenCalled();
    expect(mocks.externalLink).not.toHaveBeenCalledWith(session);
  });
});

describe("FailureNote default compatibility", () => {
  it("uses failureReason, externalLink, and the default new-tab anchor when controlled props are absent", async () => {
    mocks.failureReason.mockReturnValue("API Error: 529 overloaded");
    mocks.externalLink.mockReturnValue("https://example.com/default-chat");

    await mount({ session });

    expect(mocks.failureReason).toHaveBeenCalledWith(session);
    expect(mocks.externalLink).toHaveBeenCalledWith(session);
    const note = host.querySelector<HTMLElement>('[data-testid="chat-failure"]')!;
    expect(note.textContent).toContain("API Error: 529 overloaded");
    const anchor = host.querySelector<HTMLAnchorElement>('[data-testid="chat-failure-lark"]')!;
    expect(anchor.tagName).toBe("A");
    expect(anchor.href).toBe("https://example.com/default-chat");
    expect(anchor.target).toBe("_blank");

    act(() => host.querySelector<HTMLButtonElement>('[data-testid="chat-failure-retry"]')!.click());
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("returns nothing when default failureReason yields undefined", async () => {
    mocks.failureReason.mockReturnValue(undefined);
    await mount({ session });
    expect(mocks.failureReason).toHaveBeenCalledWith(session);
    expect(mocks.externalLink).not.toHaveBeenCalled();
    expect(host.querySelector('[data-testid="chat-failure"]')).toBeNull();
  });
});
