// @vitest-environment jsdom
// Controlled link props bypass default link and manager singletons; default callers keep existing fallbacks.
import { act, type MouseEvent as ReactMouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Context } from "../../lib/turns";
import type { ContextDetailsProps, ContextLinkClick } from "./ContextDetails";

const mocks = vi.hoisted(() => ({
  linkHref: vi.fn((url: string) => `default-href:${url}`),
  clickLink: vi.fn((event: ReactMouseEvent) => { event.preventDefault(); }),
  useLinkManager: vi.fn<() => string | undefined>(() => "default-manager"),
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

const { ContextDetails } = await import("./ContextDetails");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const docContext: Context = {
  kind: "doc",
  title: "Design handbook",
  url: "https://example.com/docs/handbook",
  text: "Follow the verification steps.",
};

const emptyContext: Context = {
  kind: "context",
  title: "Empty note",
  text: "",
};

const textOnlyContext: Context = {
  kind: "instructions",
  title: "Review notes",
  text: "Check edge cases before merging.",
};

let host: HTMLDivElement;
let root: Root;
let resolveLinkHref: Mock<(url: string) => string>;
let onLinkClick: Mock<ContextLinkClick>;

const mount = async (props: ContextDetailsProps) => {
  await act(async () => root.render(<ContextDetails {...props} />));
};

const buttons = () => [...host.querySelectorAll<HTMLButtonElement>("button")];
const anchor = () => host.querySelector<HTMLAnchorElement>("a");

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

describe("ContextDetails with controlled links", () => {
  it("renders a single context and routes links through controlled props without touching singletons", async () => {
    await mount({ contexts: [docContext], resolveLinkHref, onLinkClick });

    expect(host.textContent).toContain(docContext.text);
    const link = anchor();
    expect(link).not.toBeNull();
    expect(link?.getAttribute("href")).toBe(`panel-href:${docContext.url}`);
    expect(link?.textContent).toContain(t("chat-contextgroup.open_url", { url: docContext.url! }));

    act(() => link!.click());

    expect(resolveLinkHref).toHaveBeenCalledWith(docContext.url);
    expect(onLinkClick).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, undefined);
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });

  it("expands multiple rows, handles empty bodies, and forwards an explicit linkManager", async () => {
    await mount({
      contexts: [docContext, emptyContext],
      linkManager: "panel-manager",
      resolveLinkHref,
      onLinkClick,
    });

    const rows = buttons();
    expect(rows).toHaveLength(2);
    expect(rows.map((row) => row.getAttribute("aria-expanded"))).toEqual(["false", "false"]);
    expect(rows[1].textContent).toContain(t("chat-contextgroup.empty"));
    expect(anchor()).toBeNull();

    await act(async () => rows[0].click());
    expect(rows[0].getAttribute("aria-expanded")).toBe("true");
    expect(anchor()?.getAttribute("href")).toBe(`panel-href:${docContext.url}`);

    act(() => anchor()!.click());
    expect(onLinkClick).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "panel-manager");

    await act(async () => rows[1].click());
    expect(rows[1].getAttribute("aria-expanded")).toBe("true");
    expect(host.textContent).toContain(t("chat-contextgroup.no_content"));

    await act(async () => rows[0].click());
    expect(rows[0].getAttribute("aria-expanded")).toBe("false");

    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });

  it("uses an explicit linkManager with default link helpers without reading useLinkManager", async () => {
    await mount({ contexts: [docContext], linkManager: "workspace-manager" });

    const link = anchor();
    expect(link?.getAttribute("href")).toBe(`default-href:${docContext.url}`);
    act(() => link!.click());

    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).toHaveBeenCalledWith(docContext.url);
    expect(mocks.clickLink).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "workspace-manager");
  });

  it("treats null linkManager as explicit default manager without reading useLinkManager", async () => {
    await mount({ contexts: [docContext], linkManager: null });

    act(() => anchor()!.click());

    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.clickLink).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, undefined);
  });

  it("renders a text-only context without touching default singletons when controlled", async () => {
    await mount({ contexts: [textOnlyContext], resolveLinkHref, onLinkClick });

    expect(host.textContent).toContain(textOnlyContext.text);
    expect(anchor()).toBeNull();
    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
  });
});

describe("ContextDetails default compatibility", () => {
  it("uses useLinkManager, linkHref, and clickLink for a single context when optional props are absent", async () => {
    await mount({ contexts: [docContext] });

    const link = anchor();
    expect(link?.getAttribute("href")).toBe(`default-href:${docContext.url}`);
    act(() => link!.click());

    expect(mocks.useLinkManager).toHaveBeenCalledTimes(1);
    expect(mocks.linkHref).toHaveBeenCalledWith(docContext.url);
    expect(mocks.clickLink).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "default-manager");
  });

  it("uses default link helpers when expanding a multi-context row", async () => {
    await mount({ contexts: [textOnlyContext, docContext] });

    expect(mocks.useLinkManager).not.toHaveBeenCalled();
    await act(async () => buttons()[1].click());

    const link = anchor();
    expect(link?.getAttribute("href")).toBe(`default-href:${docContext.url}`);
    act(() => link!.click());

    expect(mocks.useLinkManager).toHaveBeenCalledTimes(1);
    expect(mocks.linkHref).toHaveBeenCalledWith(docContext.url);
    expect(mocks.clickLink).toHaveBeenCalledWith(expect.any(Object), docContext.url, docContext.title, "default-manager");
  });
});
