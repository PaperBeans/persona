// @vitest-environment jsdom
// Controlled closeout cards accept explicit data, disclosure state, link callbacks, and evidence rendering without touching default link or evidence-chunk singletons; default callers keep existing fallbacks.
import { act, type MouseEvent as ReactMouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Segment } from "../../lib/turns";
import type { Closeout, Session } from "../../lib/types";
import type { CloseoutCardProps, CloseoutEvidenceRenderProps } from "./CloseoutCard";

const mocks = vi.hoisted(() => ({
  shownCloseout: vi.fn(),
  linkHref: vi.fn(),
  clickLink: vi.fn(),
  onCloseoutEvidenceChunk: vi.fn(),
  closeoutEvidenceChunk: vi.fn(),
  loadCloseoutEvidence: vi.fn(),
  CloseoutEvidence: vi.fn(),
}));

vi.mock("../../lib/closeout", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/closeout")>();
  return {
    ...actual,
    shownCloseout: (...args: Parameters<typeof actual.shownCloseout>) => mocks.shownCloseout(...args),
  };
});

vi.mock("../../lib/links", () => ({
  linkHref: mocks.linkHref,
  clickLink: mocks.clickLink,
}));

vi.mock("./closeout-evidence-chunk", () => ({
  onCloseoutEvidenceChunk: mocks.onCloseoutEvidenceChunk,
  closeoutEvidenceChunk: mocks.closeoutEvidenceChunk,
  loadCloseoutEvidence: mocks.loadCloseoutEvidence,
  CloseoutEvidence: (props: { session: Session; verified: string; open: boolean; segment?: Segment }) => {
    mocks.CloseoutEvidence(props);
    return (
      <>
        <dt>default-evidence</dt>
        <dd data-default-evidence data-verified={props.verified} data-open={String(props.open)} />
      </>
    );
  },
}));

const { CloseoutCard } = await import("./CloseoutCard");
const { shownCloseout: actualShownCloseout } = await vi.importActual<typeof import("../../lib/closeout")>("../../lib/closeout");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const controlledCloseout: Closeout = {
  status: "done",
  did: ["Added controlled props"],
  merged: "yes",
  deployed: "no",
  verified: "`npm test`",
  checked: { merged: "yes", deployed: "no" },
};

const defaultSession = {
  machine_id: "example-machine",
  session_id: "example-session",
  cwd: "/workspace",
  title: "Example",
  status: "done",
  state: "idle",
  work: {
    phase: "done",
    artifacts: [{ kind: "pr", ref: "https://github.com/example/repo/pull/42" }],
    closeout: {
      status: "done",
      did: ["Default work"],
      verified: "`npm test`",
    },
  },
} as Session;

const segment: Segment = { id: "seg-1", steps: [], tools: 0, thoughts: 0, replies: 1, folds: [] };

let host: HTMLDivElement;
let root: Root;
let resolveLinkHref: Mock<(url: string) => string>;
let onLinkClick: Mock<(event: ReactMouseEvent<HTMLAnchorElement>, url: string, title?: string) => void>;

const mount = async (props: CloseoutCardProps) => {
  await act(async () => root.render(<CloseoutCard {...props} />));
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.shownCloseout.mockImplementation(actualShownCloseout);
  mocks.linkHref.mockImplementation((url: string) => `default-href:${url}`);
  mocks.clickLink.mockImplementation((event: ReactMouseEvent) => { event.preventDefault(); });
  mocks.onCloseoutEvidenceChunk.mockImplementation(() => () => {});
  mocks.closeoutEvidenceChunk.mockReturnValue("in");

  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  resolveLinkHref = vi.fn((url) => `/panel/link?to=${encodeURIComponent(url)}`);
  onLinkClick = vi.fn((event) => { event.preventDefault(); });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("CloseoutCard with controlled props", () => {
  it("renders explicit closeout, artifacts, and custom evidence without calling default link or evidence-chunk singletons", async () => {
    const renderEvidence = vi.fn(({ verified, open }: CloseoutEvidenceRenderProps) => (
      <>
        <dt>custom-evidence</dt>
        <dd data-custom-evidence data-verified={verified} data-open={String(open)} />
      </>
    ));

    await mount({
      closeout: controlledCloseout,
      artifacts: [
        { kind: "pr", ref: "https://github.com/example/repo/pull/99" },
        { kind: "url", ref: "https://example.com/report" },
        { kind: "commit", ref: "abc1234" },
      ],
      resolveLinkHref,
      onLinkClick,
      renderEvidence,
    });

    expect(host.querySelector("[data-closeout='done']")).not.toBeNull();
    expect(host.textContent).toContain("Added controlled props");
    expect(host.textContent).toContain("`npm test`");
    expect(host.textContent).toContain("abc1234");

    const customEvidence = host.querySelector("[data-custom-evidence]");
    expect(customEvidence?.getAttribute("data-verified")).toBe("`npm test`");
    expect(customEvidence?.getAttribute("data-open")).toBe("true");
    expect(renderEvidence).toHaveBeenCalledWith({
      session: undefined,
      verified: "`npm test`",
      open: true,
      segment: undefined,
      expandFold: undefined,
    });

    const links = [...host.querySelectorAll<HTMLAnchorElement>("a")];
    expect(links).toHaveLength(2);
    expect(links[0].getAttribute("href")).toBe(`/panel/link?to=${encodeURIComponent("https://github.com/example/repo/pull/99")}`);
    expect(links[0].textContent).toContain(t("todos.stage_pr_n", { n: "99" }));
    expect(links[1].getAttribute("href")).toBe(`/panel/link?to=${encodeURIComponent("https://example.com/report")}`);
    expect(links[1].textContent).toContain("example.com/report");

    act(() => links[0].click());
    expect(onLinkClick).toHaveBeenCalledWith(
      expect.any(Object),
      "https://github.com/example/repo/pull/99",
      t("todos.stage_pr_n", { n: "99" }),
    );
    act(() => links[1].click());
    expect(onLinkClick).toHaveBeenCalledWith(expect.any(Object), "https://example.com/report", "example.com/report");

    expect(mocks.shownCloseout).not.toHaveBeenCalled();
    expect(mocks.linkHref).not.toHaveBeenCalled();
    expect(mocks.clickLink).not.toHaveBeenCalled();
    expect(mocks.onCloseoutEvidenceChunk).not.toHaveBeenCalled();
    expect(mocks.closeoutEvidenceChunk).not.toHaveBeenCalled();
    expect(mocks.loadCloseoutEvidence).not.toHaveBeenCalled();
    expect(mocks.CloseoutEvidence).not.toHaveBeenCalled();
  });

  it("supports controlled open and onOpenChange disclosure without mutating internal state", async () => {
    const onOpenChange = vi.fn();
    const renderEvidence = vi.fn(({ open }: CloseoutEvidenceRenderProps) => <dd data-custom-open={String(open)} />);

    await mount({ closeout: controlledCloseout, open: false, onOpenChange, renderEvidence });
    const header = host.querySelector<HTMLButtonElement>("section > button")!;
    expect(header.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-custom-open]")?.getAttribute("data-custom-open")).toBe("false");

    act(() => header.click());
    expect(onOpenChange).toHaveBeenCalledWith(true);
    expect(header.getAttribute("aria-expanded")).toBe("false");

    await mount({ closeout: controlledCloseout, open: true, onOpenChange, renderEvidence });
    expect(header.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector("[data-custom-open]")?.getAttribute("data-custom-open")).toBe("true");

    act(() => header.click());
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(mocks.onCloseoutEvidenceChunk).not.toHaveBeenCalled();
  });

  it("hides on explicit null or unknown status and skips default evidence when session is omitted", async () => {
    await mount({ session: defaultSession, closeout: null });
    expect(host.querySelector("[data-closeout]")).toBeNull();
    expect(mocks.shownCloseout).not.toHaveBeenCalled();
    expect(mocks.loadCloseoutEvidence).not.toHaveBeenCalled();

    await mount({ session: defaultSession, closeout: { status: "unknown", verified: "`npm test`" } });
    expect(host.querySelector("[data-closeout]")).toBeNull();

    await mount({ closeout: controlledCloseout });
    expect(host.querySelector("[data-closeout='done']")).not.toBeNull();
    expect(host.textContent).toContain("Added controlled props");
    expect(host.textContent).toContain("`npm test`");
    expect(host.querySelector("[data-default-evidence]")).toBeNull();
    expect(mocks.onCloseoutEvidenceChunk).not.toHaveBeenCalled();
    expect(mocks.closeoutEvidenceChunk).not.toHaveBeenCalled();
    expect(mocks.loadCloseoutEvidence).not.toHaveBeenCalled();
    expect(mocks.CloseoutEvidence).not.toHaveBeenCalled();
  });
});

describe("CloseoutCard default compatibility", () => {
  it("keeps shownCloseout, default links, default evidence chunk loading, and uncontrolled toggle when controlled props are absent", async () => {
    await mount({ session: defaultSession, segment });

    expect(mocks.shownCloseout).toHaveBeenCalledWith(defaultSession);
    expect(mocks.onCloseoutEvidenceChunk).toHaveBeenCalledTimes(1);
    expect(mocks.closeoutEvidenceChunk).toHaveBeenCalled();
    expect(mocks.loadCloseoutEvidence).toHaveBeenCalledTimes(1);
    expect(mocks.CloseoutEvidence).toHaveBeenCalledWith(expect.objectContaining({
      session: defaultSession,
      verified: "`npm test`",
      open: true,
      segment,
    }));
    expect(host.querySelector("[data-default-evidence]")?.getAttribute("data-open")).toBe("true");

    const prAnchor = host.querySelector<HTMLAnchorElement>("a")!;
    expect(mocks.linkHref).toHaveBeenCalledWith("https://github.com/example/repo/pull/42");
    expect(prAnchor.getAttribute("href")).toBe("default-href:https://github.com/example/repo/pull/42");
    act(() => prAnchor.click());
    expect(mocks.clickLink).toHaveBeenCalledWith(
      expect.any(Object),
      "https://github.com/example/repo/pull/42",
      t("todos.stage_pr_n", { n: "42" }),
    );

    const header = host.querySelector<HTMLButtonElement>("section > button")!;
    expect(header.getAttribute("aria-expanded")).toBe("true");
    act(() => header.click());
    expect(header.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-default-evidence]")?.getAttribute("data-open")).toBe("false");
  });
});
