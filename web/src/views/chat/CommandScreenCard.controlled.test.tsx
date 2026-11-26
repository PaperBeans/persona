// @vitest-environment jsdom
// Controlled command screen cards supply their own resolve, dismiss, tap-guard and close-blocked-agent handlers; default fallbacks stay pinned too.
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Interaction } from "../../lib/types";
import type { CommandScreenCardProps } from "./CommandScreenCard";

vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const mocks = vi.hoisted(() => ({
  resolve: vi.fn(),
  useChatCardTapGuard: vi.fn(),
  CloseBlockedAgent: vi.fn(({ disabled }: { ix: Interaction; disabled: boolean }) => (
    <span data-default-close-blocked={String(disabled)} />
  )),
}));

vi.mock("./Interactions", async (importOriginal) => ({
  ...await importOriginal<typeof import("./Interactions")>(),
  resolve: mocks.resolve,
  CloseBlockedAgent: mocks.CloseBlockedAgent,
}));
vi.mock("./card-tap-guard", () => ({
  useChatCardTapGuard: mocks.useChatCardTapGuard,
}));

const { CommandScreenCard } = await import("./CommandScreenCard");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const pairInteraction = (): Interaction => ({
  id: "scr_example_pair",
  machine_id: "example-machine",
  session_id: "example-session",
  kind: "screen",
  agent: "claude",
  dialog: {
    question: "Do you want to proceed?",
    rev: "rev-1",
    command: "rm -rf ./tmp-output",
    description: "Clean temporary build output",
    warning: "Dangerous rm operation not ruled out",
    risk: "delete",
    deadline: "Automatically blocked after timeout",
    options: [
      { key: "1", label: "Yes" },
      { key: "2", label: "No" },
    ],
  },
});

const multiOptionInteraction = (): Interaction => ({
  id: "scr_example_multi",
  machine_id: "example-machine",
  session_id: "example-session",
  kind: "screen",
  agent: "claude",
  dialog: {
    question: "Do you want to proceed?",
    rev: "rev-2",
    command: "npm test",
    description: "Run the unit test suite",
    options: [
      { key: "1", label: "Yes" },
      { key: "2", label: "Yes, and don't ask again" },
      { key: "3", label: "No", detail: "Tell the agent what to do instead" },
    ],
  },
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: CommandScreenCardProps) => {
  await act(async () => root.render(<CommandScreenCard {...props} />));
};

const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const buttonByText = (label: string) =>
  [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.trim() === label);

beforeEach(() => {
  vi.clearAllMocks();
  mocks.resolve.mockResolvedValue(true);
  mocks.useChatCardTapGuard.mockImplementation(() => ({ ref: createRef<HTMLDivElement>() }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("CommandScreenCard with controlled props", () => {
  it("routes Allow, Deny and Hand back through onResolve with busy locking and skips default singletons", async () => {
    let finishAllow!: () => void;
    const onResolve = vi
      .fn<(body: Record<string, unknown>) => Promise<void>>()
      .mockImplementationOnce(() => new Promise<void>((resolve) => { finishAllow = resolve; }))
      .mockResolvedValue(undefined);

    await mount({
      ix: pairInteraction(),
      onResolve,
      tapGuard: null,
      onCloseBlockedAgent: null,
    });

    expect(host.querySelector("[data-screen-command]")?.textContent).toBe("rm -rf ./tmp-output");
    expect(host.querySelector("[data-screen-risk]")?.textContent).toBe(t("chat-commandask.risk_delete"));
    expect(host.querySelector("[data-default-close-blocked]")).toBeNull();

    act(() => buttonByText(t("chat-commandask.show_original"))!.click());
    expect(host.textContent).toContain("Dangerous rm operation not ruled out");

    const allow = buttonByText(t("chat-interactions.allow"))!;
    const deny = buttonByText(t("chat-interactions.deny"))!;
    const handBack = buttonByText(t("chat-interactions.hand_back"))!;

    act(() => allow.click());
    expect(onResolve).toHaveBeenCalledWith({ option: "1" });
    expect(allow.disabled).toBe(true);
    expect(deny.disabled).toBe(true);
    expect(handBack.disabled).toBe(true);

    act(() => allow.click());
    expect(onResolve).toHaveBeenCalledTimes(1);

    await act(async () => { finishAllow(); await Promise.resolve(); });
    await flush();
    expect(allow.disabled).toBe(false);
    expect(deny.disabled).toBe(false);

    act(() => deny.click());
    await flush();
    expect(onResolve).toHaveBeenLastCalledWith({ option: "2" });

    act(() => handBack.click());
    await flush();
    expect(onResolve).toHaveBeenLastCalledWith({ dismiss: true });

    expect(mocks.resolve).not.toHaveBeenCalled();
    expect(mocks.useChatCardTapGuard).not.toHaveBeenCalled();
    expect(mocks.CloseBlockedAgent).not.toHaveBeenCalled();
  });

  it("handles multi-option clicks, digit keys, custom tapGuard, custom onDismiss and hidden dismiss", async () => {
    const onResolve = vi.fn().mockResolvedValue(undefined);
    const onDismiss = vi.fn().mockResolvedValue(undefined);
    const guardRef = createRef<HTMLDivElement>();
    const ix = multiOptionInteraction();

    await mount({
      ix,
      onResolve,
      onDismiss,
      tapGuard: { ref: guardRef },
      onCloseBlockedAgent: null,
    });

    const card = host.querySelector<HTMLDivElement>("[data-pending-card]")!;
    expect(guardRef.current).toBe(card);

    const optionButtons = [...host.querySelectorAll<HTMLButtonElement>("[data-tap-answer]")];
    expect(optionButtons).toHaveLength(3);
    act(() => optionButtons[2].click());
    await flush();
    expect(onResolve).toHaveBeenCalledWith({ option: "3" });

    act(() => {
      card.focus();
      card.dispatchEvent(new KeyboardEvent("keydown", { key: "2", bubbles: true }));
    });
    await flush();
    expect(onResolve).toHaveBeenLastCalledWith({ option: "2" });

    act(() => buttonByText(t("chat-interactions.hand_back"))!.click());
    await flush();
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(onResolve).toHaveBeenCalledTimes(2);

    await mount({
      ix,
      onResolve,
      onDismiss: null,
      tapGuard: { ref: guardRef },
      onCloseBlockedAgent: null,
    });
    expect(buttonByText(t("chat-interactions.hand_back"))).toBeUndefined();

    expect(mocks.resolve).not.toHaveBeenCalled();
    expect(mocks.useChatCardTapGuard).not.toHaveBeenCalled();
    expect(mocks.CloseBlockedAgent).not.toHaveBeenCalled();
  });

  it("runs the controlled two-step blocked-agent close flow without mounting default CloseBlockedAgent", async () => {
    let finishClose!: () => void;
    const onCloseBlockedAgent = vi.fn(() => new Promise<void>((resolve) => { finishClose = resolve; }));

    await mount({
      ix: pairInteraction(),
      onResolve: vi.fn(),
      tapGuard: null,
      onCloseBlockedAgent,
    });

    expect(host.querySelector("[data-default-close-blocked]")).toBeNull();
    act(() => buttonByText(t("chat-interactions.close_agent"))!.click());
    expect(host.textContent).toContain(t("chat-interactions.close_agent_hint"));

    act(() => buttonByText(t("chat-interactions.close_agent_cancel"))!.click());
    expect(onCloseBlockedAgent).not.toHaveBeenCalled();
    expect(buttonByText(t("chat-interactions.close_agent"))).toBeDefined();

    act(() => buttonByText(t("chat-interactions.close_agent"))!.click());
    const cancel = buttonByText(t("chat-interactions.close_agent_cancel"))!;
    const confirm = buttonByText(t("chat-interactions.close_agent_confirm"))!;
    act(() => confirm.click());
    expect(onCloseBlockedAgent).toHaveBeenCalledTimes(1);
    expect(cancel.disabled).toBe(true);
    expect(confirm.disabled).toBe(true);

    act(() => confirm.click());
    expect(onCloseBlockedAgent).toHaveBeenCalledTimes(1);

    await act(async () => { finishClose(); await Promise.resolve(); });
    await flush();
    expect(buttonByText(t("chat-interactions.close_agent"))).toBeDefined();
    expect(mocks.CloseBlockedAgent).not.toHaveBeenCalled();
    expect(mocks.useChatCardTapGuard).not.toHaveBeenCalled();
    expect(mocks.resolve).not.toHaveBeenCalled();
  });
});

describe("CommandScreenCard default compatibility", () => {
  it("uses default resolve, useChatCardTapGuard and CloseBlockedAgent when controlled props are absent", async () => {
    const defaultGuardRef = createRef<HTMLDivElement>();
    mocks.useChatCardTapGuard.mockReturnValue({ ref: defaultGuardRef });
    let finishResolve!: (ok: boolean) => void;
    mocks.resolve
      .mockImplementationOnce(() => new Promise<boolean>((resolve) => { finishResolve = resolve; }))
      .mockResolvedValue(true);

    const ix = pairInteraction();
    await mount({ ix });

    expect(mocks.useChatCardTapGuard).toHaveBeenCalledWith(ix);
    expect(defaultGuardRef.current).toBe(host.querySelector("[data-pending-card]"));
    expect(mocks.CloseBlockedAgent).toHaveBeenCalled();
    expect(host.querySelector("[data-default-close-blocked]")?.getAttribute("data-default-close-blocked")).toBe("false");

    act(() => buttonByText(t("chat-interactions.allow"))!.click());
    expect(mocks.resolve).toHaveBeenCalledWith(ix, { option: "1" });
    expect(host.querySelector("[data-default-close-blocked]")?.getAttribute("data-default-close-blocked")).toBe("true");

    await act(async () => { finishResolve(true); await Promise.resolve(); });
    await flush();
    expect(host.querySelector("[data-default-close-blocked]")?.getAttribute("data-default-close-blocked")).toBe("false");

    act(() => buttonByText(t("chat-interactions.deny"))!.click());
    await flush();
    expect(mocks.resolve).toHaveBeenLastCalledWith(ix, { option: "2" });

    act(() => buttonByText(t("chat-interactions.hand_back"))!.click());
    await flush();
    expect(mocks.resolve).toHaveBeenLastCalledWith(ix, { dismiss: true });
  });
});
