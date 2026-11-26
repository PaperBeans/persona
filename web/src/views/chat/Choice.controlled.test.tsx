// @vitest-environment jsdom
// Controlled panels supply Choice parsing, badges, keyboard hints and change handlers without touching default label
// parsers or recommendation tags; default compatibility remains pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChoiceProps } from "./Choice";

const mocks = vi.hoisted(() => ({
  optionDisplay: vi.fn(),
  recommendedTag: vi.fn(),
}));

vi.mock("../../lib/tool-detail", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/tool-detail")>();
  mocks.optionDisplay.mockImplementation(actual.optionDisplay);
  return { ...actual, optionDisplay: mocks.optionDisplay };
});

vi.mock("../../ui/RecommendedTag", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../ui/RecommendedTag")>();
  return {
    RecommendedTag: (props: { assistant?: boolean }) => {
      mocks.recommendedTag(props);
      return actual.RecommendedTag(props);
    },
  };
});

const { Choice, DigitHint } = await import("./Choice");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

const mount = async (props: ChoiceProps) => {
  await act(async () => root.render(<Choice {...props} />));
};
const button = () => host.querySelector<HTMLButtonElement>("button")!;

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

describe("Choice with controlled props", () => {
  it("uses custom label parser, badge renderer, hint renderer and onChange without touching default helpers", async () => {
    const parseLabel = vi.fn((raw: string) => ({ text: raw.toUpperCase(), recommended: true }));
    const renderRecommendedTag = vi.fn((kind: "option" | "assistant") => (
      <span data-custom-tag={kind}>{kind}</span>
    ));
    const renderHint = vi.fn((n: number) => <span data-custom-hint={n}>#{n}</span>);
    const onChange = vi.fn();

    await mount({
      on: false,
      multi: true,
      label: "Fast path",
      desc: "Minimal work",
      pick: true,
      hint: 2,
      marker: true,
      className: "custom-choice-row",
      parseLabel,
      renderRecommendedTag,
      renderHint,
      onChange,
    });

    expect(parseLabel).toHaveBeenCalledWith("Fast path");
    expect(button().getAttribute("aria-pressed")).toBe("false");
    expect(button().hasAttribute("data-tap-answer")).toBe(true);
    expect(button().className).toContain("custom-choice-row");
    expect(host.textContent).toContain("FAST PATH");
    expect(host.textContent).toContain("Minimal work");
    expect(host.querySelector('[data-custom-tag="option"]')).not.toBeNull();
    expect(host.querySelector('[data-custom-tag="assistant"]')).not.toBeNull();
    expect(host.querySelector('[data-custom-hint="2"]')).not.toBeNull();
    expect(host.querySelector("[data-key-hint]")).toBeNull();

    act(() => button().click());
    expect(onChange).toHaveBeenCalledWith(true);

    await mount({
      on: true,
      label: "Fast path",
      parseLabel,
      renderRecommendedTag,
      onChange,
    });
    expect(button().getAttribute("aria-pressed")).toBe("true");
    act(() => button().click());
    expect(onChange).toHaveBeenLastCalledWith(false);

    expect(mocks.optionDisplay).not.toHaveBeenCalled();
    expect(mocks.recommendedTag).not.toHaveBeenCalled();
  });

  it("respects explicit recommended overrides, invokes both onClick and onChange, and guards disabled clicks", async () => {
    const parseLabel = vi.fn((raw: string) => ({ text: raw, recommended: true }));
    const renderRecommendedTag = vi.fn((kind: "option" | "assistant") => <span data-custom-tag={kind} />);
    const onClick = vi.fn();
    const onChange = vi.fn();

    await mount({
      on: false,
      label: "Suppress option badge",
      recommended: false,
      pick: true,
      parseLabel,
      renderRecommendedTag,
      onClick,
      onChange,
    });
    expect(host.querySelector('[data-custom-tag="option"]')).toBeNull();
    expect(host.querySelector('[data-custom-tag="assistant"]')).not.toBeNull();

    act(() => button().click());
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(true);

    await mount({
      on: true,
      label: "Explicitly recommended",
      recommended: true,
      disabled: true,
      parseLabel: (raw) => ({ text: raw, recommended: false }),
      renderRecommendedTag,
      onClick,
      onChange,
    });
    expect(host.querySelector('[data-custom-tag="option"]')).not.toBeNull();
    expect(button().disabled).toBe(true);
    act(() => button().click());
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(mocks.optionDisplay).not.toHaveBeenCalled();
    expect(mocks.recommendedTag).not.toHaveBeenCalled();
  });

  it("renders DigitHint with optional className", async () => {
    await act(async () => root.render(<DigitHint n={4} className="custom-hint" />));
    const el = host.querySelector<HTMLElement>("[data-key-hint]")!;
    expect(el.textContent).toBe("4");
    expect(el.getAttribute("aria-hidden")).toBe("true");
    expect(el.className).toContain("custom-hint");
  });
});

describe("Choice default compatibility", () => {
  it("uses optionDisplay, RecommendedTag, DigitHint and onClick when controlled props are absent", async () => {
    const onClick = vi.fn();
    await mount({
      on: true,
      label: "Use default (Recommended)",
      desc: "Standard option",
      pick: true,
      hint: 1,
      onClick,
    });

    expect(mocks.optionDisplay).toHaveBeenCalledWith("Use default (Recommended)");
    expect(mocks.recommendedTag).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("Use default");
    expect(host.textContent).not.toContain("(Recommended)");
    expect(host.textContent).toContain(t("chat-interactions.recommended"));
    expect(host.textContent).toContain(t("chat-delegate.recommended"));
    expect(host.querySelector("[data-assistant-pick]")).not.toBeNull();
    expect(host.querySelector("[data-key-hint]")?.textContent).toBe("1");
    expect(button().hasAttribute("data-tap-answer")).toBe(false);
    expect(button().getAttribute("aria-pressed")).toBe("true");

    act(() => button().click());
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
