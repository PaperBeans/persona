// @vitest-environment jsdom
// Controlled ContextMeter owns open state, usage/tokens overrides, and custom popover rendering without loading default SessionInsightsPopover; default callers keep the lazy popover fallback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ContextUsage } from "../../lib/context-usage";
import type { Item, Session } from "../../lib/types";
import type { ContextMeterPopoverProps, ContextMeterProps } from "./ContextMeter";
import type { SessionInsightsPopoverProps } from "./SessionInsightsPopover";

const mocks = vi.hoisted(() => ({
  defaultPopover: vi.fn(),
}));

vi.mock("./SessionInsightsPopover", () => ({
  SessionInsightsPopover: (props: SessionInsightsPopoverProps) => {
    mocks.defaultPopover(props);
    return (
      <div data-testid="default-insights-popover">
        <button type="button" data-testid="default-close" onClick={props.onClose}>Close</button>
      </div>
    );
  },
}));

const { ContextMeter } = await import("./ContextMeter");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const baseSession: Session = {
  machine_id: "example-machine",
  session_id: "example-session",
  title: "Session",
  cwd: "/workspace/example",
  status: "idle",
  agent: "claude",
  context_tokens: 78_000,
  context_window: 200_000,
};

const sampleItems: Item[] = [
  { id: "a1", seq: 1, role: "assistant", kind: "text", usage: [{ in: 1200, out: 800 }] },
];

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<ContextMeterProps> = {}) => {
  await act(async () => {
    root.render(<ContextMeter session={baseSession} {...props} />);
  });
};

const trigger = () => host.querySelector<HTMLButtonElement>("button")!;
const flush = async () => {
  await act(async () => {
    await vi.dynamicImportSettled();
    for (let i = 0; i < 4; i++) await Promise.resolve();
  });
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

describe("ContextMeter with controlled props", () => {
  it("supports controlled open, onOpenChange, and custom renderPopover without touching default SessionInsightsPopover", async () => {
    const onOpenChange = vi.fn();
    const onCompact = vi.fn();
    const seen: ContextMeterPopoverProps[] = [];
    const renderPopover = vi.fn((props: ContextMeterPopoverProps) => {
      seen.push(props);
      return (
        <div data-testid="custom-insights-popover">
          <button type="button" data-testid="custom-close" onClick={props.onClose}>Dismiss</button>
        </div>
      );
    });

    await mount({
      items: sampleItems,
      assistant: true,
      onCompact,
      compactBlocked: true,
      open: false,
      onOpenChange,
      renderPopover,
    });

    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    expect(trigger().getAttribute("title")).toBeTruthy();
    expect(renderPopover).not.toHaveBeenCalled();

    // Clicking requests open=true, while DOM stays closed until parent flips controlled `open`
    act(() => trigger().click());
    expect(onOpenChange).toHaveBeenCalledWith(true);
    expect(renderPopover).not.toHaveBeenCalled();

    await mount({
      items: sampleItems,
      assistant: true,
      onCompact,
      compactBlocked: true,
      open: true,
      onOpenChange,
      renderPopover,
    });

    expect(trigger().getAttribute("aria-expanded")).toBe("true");
    expect(trigger().getAttribute("title")).toBeNull();
    expect(host.querySelector('[data-testid="custom-insights-popover"]')).not.toBeNull();
    expect(seen).toHaveLength(1);
    expect(seen[0].anchor.current).toBe(trigger());
    expect(seen[0].session).toBe(baseSession);
    expect(seen[0].items).toBe(sampleItems);
    expect(seen[0].assistant).toBe(true);
    expect(seen[0].onCompact).toBe(onCompact);
    expect(seen[0].compactBlocked).toBe(true);

    // Popover close and trigger toggle both request open=false
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="custom-close"]')!.click());
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
    onOpenChange.mockClear();
    act(() => trigger().click());
    expect(onOpenChange).toHaveBeenCalledWith(false);

    // Clearing usage while controlled-open notifies onOpenChange(false) and unmounts cleanly
    onOpenChange.mockClear();
    await mount({
      session: { ...baseSession, context_tokens: undefined, context_window: undefined },
      open: true,
      onOpenChange,
      renderPopover,
    });
    expect(host.innerHTML).toBe("");
    expect(onOpenChange).toHaveBeenCalledTimes(1);
    expect(onOpenChange).toHaveBeenCalledWith(false);

    expect(mocks.defaultPopover).not.toHaveBeenCalled();
  });

  it("supports uncontrolled toggle with custom renderPopover and resets on /clear", async () => {
    const onOpenChange = vi.fn();
    const renderPopover = vi.fn(({ onClose }: ContextMeterPopoverProps) => (
      <div data-testid="custom-uncontrolled">
        <button type="button" data-testid="close-btn" onClick={onClose}>Close</button>
      </div>
    ));

    await mount({ onOpenChange, renderPopover });
    expect(onOpenChange).not.toHaveBeenCalled();

    act(() => trigger().click());
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    expect(host.querySelector('[data-testid="custom-uncontrolled"]')).not.toBeNull();

    // Closing via /clear emits false and does not reopen when usage returns
    await mount({
      session: { ...baseSession, context_tokens: undefined, context_window: undefined },
      onOpenChange,
      renderPopover,
    });
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
    expect(host.innerHTML).toBe("");

    await mount({ onOpenChange, renderPopover });
    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector('[data-testid="custom-uncontrolled"]')).toBeNull();
    expect(mocks.defaultPopover).not.toHaveBeenCalled();
  });

  it("honors explicit controlled usage and tokens overrides", async () => {
    const unknownSession: Session = { ...baseSession, context_tokens: undefined, context_window: undefined };
    const explicitUsage: ContextUsage = { used: 85, left: 15, tokens: 170_000, window: 200_000 };

    await mount({ session: unknownSession, usage: explicitUsage, renderPopover: () => null });
    expect(host.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("85");
    expect(host.textContent).toContain(t("context.label", { percent: 85 }));

    await mount({ session: unknownSession, usage: explicitUsage, compact: true, renderPopover: () => null });
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    expect(host.textContent).toContain(t("context.label", { percent: 85 }));

    // Explicit usage=null + tokens forces the token-only fallback even when session has window data
    await mount({ session: baseSession, usage: null, tokens: 4200, renderPopover: () => null });
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    expect(host.querySelector("[data-context-tokens]")?.textContent).toBe(t("chat-runningstatusbar.token_count", { tokens: "4.2k" }));

    // Explicit usage=null + tokens=0 hides the meter
    await mount({ session: baseSession, usage: null, tokens: 0, renderPopover: () => null });
    expect(host.innerHTML).toBe("");
    expect(mocks.defaultPopover).not.toHaveBeenCalled();
  });
});

describe("ContextMeter default fallback compatibility", () => {
  it("opens the default lazy SessionInsightsPopover on click, closes via onClose, and resets when usage becomes unknown", async () => {
    const onCompact = vi.fn();
    await mount({ items: sampleItems, assistant: true, onCompact, compactBlocked: false });

    expect(mocks.defaultPopover).not.toHaveBeenCalled();
    act(() => trigger().click());
    await flush();

    expect(mocks.defaultPopover).toHaveBeenCalledTimes(1);
    const props = mocks.defaultPopover.mock.calls[0][0] as SessionInsightsPopoverProps;
    expect(props.anchor.current).toBe(trigger());
    expect(props.session).toBe(baseSession);
    expect(props.items).toBe(sampleItems);
    expect(props.assistant).toBe(true);
    expect(props.onCompact).toBe(onCompact);
    expect(props.compactBlocked).toBe(false);

    act(() => host.querySelector<HTMLButtonElement>('[data-testid="default-close"]')!.click());
    expect(host.querySelector('[data-testid="default-insights-popover"]')).toBeNull();

    // Open again, then clear usage: popover closes and stays closed once usage returns
    act(() => trigger().click());
    await flush();
    expect(host.querySelector('[data-testid="default-insights-popover"]')).not.toBeNull();

    await mount({ session: { ...baseSession, context_tokens: undefined, context_window: undefined } });
    expect(host.innerHTML).toBe("");

    await mount({ items: sampleItems });
    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector('[data-testid="default-insights-popover"]')).toBeNull();
  });
});
