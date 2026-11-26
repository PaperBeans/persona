// @vitest-environment jsdom
// Controlled disclosure, details rendering, and link forwarding bypass default singletons while keeping default fallbacks.
import { act, type MouseEvent as ReactMouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Context } from "../../lib/turns";
import type { ContextDetailsProps, ContextLinkClick } from "./ContextDetails";
import type { ContextGroupProps } from "./ContextGroup";

const mocks = vi.hoisted(() => ({
  linkHref: vi.fn((url: string) => `default-href:${url}`),
  clickLink: vi.fn((event: ReactMouseEvent) => { event.preventDefault(); }),
  useLinkManager: vi.fn<() => string | undefined>(() => "default-manager"),
  renderDefaultDetails: vi.fn<(props: ContextDetailsProps) => void>(),
}));

vi.mock("../../lib/links", () => ({
  linkHref: mocks.linkHref,
  clickLink: mocks.clickLink,
}));
vi.mock("../../lib/manager-choice", () => ({
  useLinkManager: mocks.useLinkManager,
}));
vi.mock("../../ui/icons", () => ({
  Icon: ({ name }: { name: string }) => <span data-icon={name} />,
}));
vi.mock("./ContextDetails", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./ContextDetails")>();
  return {
    ...actual,
    ContextDetails: (props: ContextDetailsProps) => {
      mocks.renderDefaultDetails(props);
      return <actual.ContextDetails {...props} />;
    },
  };
});

const { ContextGroup } = await import("./ContextGroup");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const docContext: Context = {
  kind: "doc",
  title: "Architecture guide",
  url: "https://example.com/docs/architecture",
  text: "Keep core free of feature modules.",
};

const noteContext: Context = {
  kind: "context",
  title: "Prior excerpt",
  text: "Summary from earlier discussion.",
};

let host: HTMLDivElement;
let root: Root;
let resolveLinkHref: Mock<(url: string) => string>;
let onLinkClick: Mock<ContextLinkClick>;

const mount = async (props: ContextGroupProps) => {
  await act(async () => root.render(<ContextGroup {...props} />));
};

const summaryButton = () => host.querySelector<HTMLButtonElement>("button")!;
const allButtons = () => [...host.querySelectorAll<HTMLButtonElement>("button")];
const anchor = () => host.querySelector<HTMLAnchorElement>("a");

async function openLazyDetails() {
  await act(async () => {
    summaryButton().click();
    await import("./ContextDetails");
    await vi.dynamicImportSettled();
  });
  await vi.waitFor(async () => {
    await act(async () => {});
    expect(mocks.renderDefaultDetails).toHaveBeenCalled();
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.linkHref.mockImplementation((url: string) => `default-href:${url}`);
  mocks.clickLink.mockImplementation((event: ReactMouseEvent) => { event.preventDefault(); });
  mocks.useLinkManager.mockReturnValue("default-manager");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  resolveLinkHref = vi.fn((url: string) => `panel-href:${url}`);
  onLinkClick = vi.fn<ContextLinkClick>((event) => { event.preventDefault(); });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("ContextGroup with controlled disclosure and details", () => {
  it("honors controlled open, onOpenChange, and renderDetails without loading default details or singletons", async () => {
    const onOpenChange = vi.fn();
    const renderDetails = vi.fn((items: Context[]) => (
      <div data-custom-details>{items.map((item) => item.title).join(", ")}</div>
    ));

    await mount({ contexts: [docContext], open: false, onOpenChange, renderDetails });

    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
    expect(renderDetails).not.toHaveBeenCalled();
    expect(host.querySelector("[data-custom-details]")).toBeNull();

    await act(async () => summaryButton().click());
    expect(onOpenChange).toHaveBeenCalledWith(true);
    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
    expect(renderDetails).not.toHaveBeenCalled();

    await mount({ contexts: [docContext], open: true, onOpenChange, renderDetails });
    expect(summaryButton().getAttribute("aria-expanded")).toBe("true");
    expect(renderDetails).toHaveBeenCalledWith([docContext]);
    expect(host.querySelector("[data-custom-details]")?.textContent).toBe(docContext.title);

    await act(async () => summaryButton().click());
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(summaryButton().getAttribute("aria-expanded")).toBe("true");

    expect(mocks.renderDefaultDetails).not.toHaveBeenCalled();
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });

  it("toggles uncontrolled open state while notifying onOpenChange and rendering custom details", async () => {
    const onOpenChange = vi.fn();
    const renderDetails = vi.fn((items: Context[]) => (
      <div data-custom-details>{items.length}</div>
    ));

    await mount({ contexts: [docContext, noteContext], onOpenChange, renderDetails });

    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
    await act(async () => summaryButton().click());
    expect(summaryButton().getAttribute("aria-expanded")).toBe("true");
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    expect(host.querySelector("[data-custom-details]")?.textContent).toBe("2");

    await act(async () => summaryButton().click());
    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
    expect(mocks.renderDefaultDetails).not.toHaveBeenCalled();
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });

  it("forwards controlled link props to lazy ContextDetails without invoking default singletons", async () => {
    await mount({
      contexts: [docContext],
      linkManager: "panel-manager",
      resolveLinkHref,
      onLinkClick,
    });

    await openLazyDetails();

    const link = anchor();
    expect(link).not.toBeNull();
    expect(link?.getAttribute("href")).toBe(`panel-href:${docContext.url}`);
    act(() => link!.click());

    expect(resolveLinkHref).toHaveBeenCalledWith(docContext.url);
    expect(onLinkClick).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "panel-manager");
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });

  it("forwards controlled link props through multi-context rows", async () => {
    await mount({
      contexts: [noteContext, docContext],
      resolveLinkHref,
      onLinkClick,
    });

    await openLazyDetails();

    const rowButtons = allButtons().slice(1);
    expect(rowButtons).toHaveLength(2);
    await act(async () => rowButtons[1].click());

    const link = anchor();
    expect(link?.getAttribute("href")).toBe(`panel-href:${docContext.url}`);
    act(() => link!.click());

    expect(onLinkClick).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, undefined);
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });
});

describe("ContextGroup default compatibility", () => {
  it("starts folded, loads ContextDetails on open, uses default link singletons, and folds back", async () => {
    await mount({ contexts: [docContext] });

    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
    expect(mocks.renderDefaultDetails).not.toHaveBeenCalled();
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();

    await openLazyDetails();

    expect(summaryButton().getAttribute("aria-expanded")).toBe("true");
    const link = anchor();
    expect(link?.getAttribute("href")).toBe(`default-href:${docContext.url}`);

    act(() => link!.click());

    expect(mocks.useLinkManager).toHaveBeenCalledTimes(1);
    expect(mocks.linkHref).toHaveBeenCalledWith(docContext.url);
    expect(mocks.clickLink).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "default-manager");

    await act(async () => summaryButton().click());
    expect(summaryButton().getAttribute("aria-expanded")).toBe("false");
  });
});
