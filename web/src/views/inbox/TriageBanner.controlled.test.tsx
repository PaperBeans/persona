// @vitest-environment jsdom
// A controlled configuration callback replaces default router navigation; the fallback path stays covered.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxTriage } from "../../lib/inbox";
import { TRIAGE_SETTINGS } from "../../lib/inbox-page";
import type { TriageBannerProps } from "./TriageBanner";

const mocks = vi.hoisted(() => ({ navigate: vi.fn() }));
vi.mock("../../lib/router", () => ({ navigate: mocks.navigate }));

const { TriageBanner } = await import("./TriageBanner");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const triage = {
  state: "unconfigured",
  waiting: 3,
  sources: [{ source: "example", name: "Example", state: "unconfigured", waiting: 3 }],
} as InboxTriage;

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<TriageBannerProps> = {}) => {
  await act(async () => root.render(<TriageBanner triage={triage} {...props} />));
};
const configureButton = () => [...host.querySelectorAll<HTMLButtonElement>("button")]
  .find((button) => button.textContent?.trim() === t("inbox-triagebanner.configure"))!;

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

describe("TriageBanner with controlled configuration", () => {
  it("calls the configuration callback without invoking default router navigation", async () => {
    const onConfigure = vi.fn();
    await mount({ onConfigure });

    act(() => configureButton().click());
    expect(onConfigure).toHaveBeenCalledTimes(1);
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});

describe("TriageBanner default compatibility", () => {
  it("navigates to the triage settings route when no callback is supplied", async () => {
    await mount();

    act(() => configureButton().click());
    expect(mocks.navigate).toHaveBeenCalledWith(TRIAGE_SETTINGS);
  });
});
