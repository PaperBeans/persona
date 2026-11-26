// @vitest-environment jsdom
// Controlled ComposerPanel bypasses global store, /fs API, and default skill-catalog hooks while keeping 100% default fallbacks intact.
import { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FsListing } from "../../lib/files";
import type { Session } from "../../lib/types";
import type { ComposerPanelHandle, ComposerPanelProps, MentionDirectoryLoader, MentionDirectoryRequest, MentionRow, MentionSource } from "./ComposerPanel";
import type { SkillCatalogState } from "./useSkillCatalog";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  useStore: vi.fn(),
  useSkillCatalog: vi.fn<() => SkillCatalogState>(() => ({})),
  useCloseOnBack: vi.fn(),
}));

vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (...args: unknown[]) => mocks.api(...args),
}));
vi.mock("../../lib/store", () => ({
  useStore: (selector: (s: unknown) => unknown) => mocks.useStore(selector),
}));
vi.mock("./useSkillCatalog", () => ({
  useSkillCatalog: (...args: unknown[]) => mocks.useSkillCatalog(...(args as [])),
}));
vi.mock("../../lib/back-close", () => ({
  useCloseOnBack: (...args: unknown[]) => mocks.useCloseOnBack(...args),
}));

const { ComposerPanel } = await import("./ComposerPanel");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const makeSession = (id: string, extra: Partial<Session> = {}): Session => ({
  machine_id: "box-1",
  session_id: id,
  title: `Session ${id}`,
  cwd: `/workspace/${id}`,
  status: "idle",
  state: "idle",
  agent: "claude",
  updated_at: "2026-11-20T10:00:00Z",
  ...extra,
});

const makeListing = (path: string, entries: Array<{ name: string; dir: boolean }>): FsListing => ({
  path,
  truncated: false,
  entries: entries.map((e) => ({ ...e, size: e.dir ? 0 : 120 })),
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: ComposerPanelProps) => {
  await act(async () => root.render(<ComposerPanel {...props} />));
};

const options = () => [...host.querySelectorAll<HTMLButtonElement>('[data-composer-panel] [role="option"]')];
const option = (text: string) => options().find((o) => o.textContent?.includes(text));
const searchInput = () => host.querySelector<HTMLInputElement>("[data-composer-panel-search]");
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};
const setInputValue = (el: HTMLInputElement, value: string) => act(() => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
});
const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.useSkillCatalog).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", (q: string) => ({
    matches: q.includes("fine"),
    media: q,
    addEventListener() {},
    removeEventListener() {},
  }));
  Element.prototype.scrollIntoView ??= () => {};
  mocks.useSkillCatalog.mockReturnValue({});
  mocks.useStore.mockImplementation((selector: (s: { sessions: Record<string, Session[]>; sessionSummaries: Session[] }) => unknown) =>
    selector({ sessions: {}, sessionSummaries: [] }),
  );
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("ComposerPanel with controlled props", () => {
  it("renders explicit rows, loading, skill status, controlled search, and keyboard actions without touching default singletons", async () => {
    const onPick = vi.fn();
    const onClose = vi.fn();
    const onSearchChange = vi.fn();
    const handle = createRef<ComposerPanelHandle>();
    const rows: MentionRow[] = [
      { kind: "skill", id: "skill:s1", skill: { id: "s1", name: "audit", description: "Check boundaries", source: "project" } },
      { kind: "file", id: "top:src", name: "src/", detail: "Folder", insert: "@src/", dir: true },
      { kind: "file", id: "top:README.md", name: "README.md", detail: "Session folder", insert: "@README.md ", dir: false },
    ];

    await mount({
      from: "plus",
      rows,
      loading: false,
      skillCatalog: { status: "partial" },
      attach: "files",
      plan: { on: true },
      search: "",
      onSearchChange,
      closeOnBack: false,
      onPick,
      onClose,
      handle,
    });

    expect(mocks.useCloseOnBack).toHaveBeenLastCalledWith(false, expect.any(Function), false);
    expect(options().map((o) => o.textContent)).toEqual([
      t("chat-composerpanel.add_files"),
      t("lib-session-actions.mode_plan"),
      expect.stringContaining("audit"),
      expect.stringContaining("src/"),
      expect.stringContaining("README.md"),
    ]);
    expect(host.textContent).toContain(t("chat-composerpanel.skills_partial"));

    // Controlled search forwards typed changes without mutating uncontrolled state.
    setInputValue(searchInput()!, "read");
    expect(onSearchChange).toHaveBeenCalledWith("read");
    expect(searchInput()!.value).toBe("");

    // Clicking folder disclosure routes browse into onSearchChange in plus mode.
    act(() => option("src/")!.querySelector<HTMLElement>("[data-browse-folder]")!.click());
    expect(onSearchChange).toHaveBeenLastCalledWith("src/");

    // Imperative keyboard navigation selects and picks the skill row.
    act(() => {
      expect(handle.current?.key("ArrowDown")).toBe(true);
    });
    act(() => {
      expect(handle.current?.key("ArrowDown")).toBe(true);
    });
    expect(host.querySelector("[data-picker-detail]")?.textContent).toContain("project");
    act(() => {
      expect(handle.current?.key("Enter")).toBe(true);
    });
    expect(onPick).toHaveBeenCalledWith({ kind: "row", row: rows[0] });

    // Escape closes and requests caret refocus in plus mode.
    act(() => {
      searchInput()!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    expect(onClose).toHaveBeenCalledWith(true);

    // ArrowRight on a folder in "at" mode emits a browse pick without calling default singletons.
    await mount({
      from: "at",
      query: "",
      rows: [rows[1]],
      onPick,
      onClose,
      handle,
    });
    act(() => {
      expect(handle.current?.key("ArrowRight")).toBe(true);
    });
    expect(onPick).toHaveBeenLastCalledWith({ kind: "browse", insert: "@src/" });

    // Controlled loading empty state when rows are empty.
    await mount({
      from: "at",
      query: "missing",
      rows: [],
      loading: true,
      onPick,
      onClose,
    });
    expect(options()).toHaveLength(0);
    expect(host.textContent).toContain(t("chat-composerpanel.reading_folder"));

    expectNoDefaultSingletons();
  });

  it("derives rows from explicit sessions, static directoryListings, and skillCatalog without store, API, or skill-hook calls", async () => {
    const onPick = vi.fn();
    const onClose = vi.fn();
    const source: MentionSource = {
      cwd: "/workspace/app",
      self: "box-1/self",
      files: () => ["/workspace/app/src/index.ts"],
      skills: { manager: "mgr-1", environment: "env-1" },
    };
    const sessions: Session[] = [
      makeSession("self", { title: "Current chat" }),
      makeSession("assistant-1", { title: "Persona chat", assistant: true }),
      makeSession("other-1", { title: "Fix caching", cwd: "/workspace/cache", updated_at: "2026-11-21T10:00:00Z" }),
    ];
    const directoryListings: Record<string, FsListing | null> = {
      "/workspace/app": makeListing("/workspace/app", [
        { name: "src", dir: true },
        { name: "package.json", dir: false },
      ]),
      "/workspace/app/src": makeListing("/workspace/app/src", [
        { name: "utils.ts", dir: false },
      ]),
    };
    const skillCatalog: SkillCatalogState = {
      catalog: {
        catalog_version: 1,
        complete: true,
        skills: [{ id: "k1", name: "review", description: "Check caching edge cases", source: "project" }],
      },
    };

    await mount({
      from: "at",
      query: "",
      source,
      machineId: "box-1",
      sessions,
      directoryListings,
      skillCatalog,
      onPick,
      onClose,
    });

    const texts = options().map((o) => o.textContent ?? "");
    expect(texts.join("|")).toContain("review");
    expect(texts.join("|")).toContain("index.ts");
    expect(texts.join("|")).toContain("src/");
    expect(texts.join("|")).toContain("package.json");
    expect(texts.join("|")).toContain("Fix caching");
    expect(texts.join("|")).not.toContain("Current chat");
    expect(texts.join("|")).not.toContain("Persona chat");

    // Folder browsing reads from static directoryListings directly without network calls.
    await mount({
      from: "at",
      query: "src/",
      source,
      machineId: "box-1",
      sessions,
      directoryListings,
      skillCatalog,
      onPick,
      onClose,
    });
    expect(options().map((o) => o.textContent)).toEqual([expect.stringContaining("utils.ts")]);

    // Unlisted folder degrades to empty without falling back to default API.
    await mount({
      from: "at",
      query: "missing/",
      source,
      machineId: "box-1",
      sessions,
      directoryListings,
      skillCatalog,
      onPick,
      onClose,
    });
    expect(options()).toHaveLength(0);
    expect(host.textContent).toContain(t("chat-composerpanel.no_match_in_folder"));

    expectNoDefaultSingletons();
  });

  it("uses custom loadDirectory, keeps inline loaders stable, rejects stale ABA promises, and handles sync throws", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const onPick = vi.fn();
    const onClose = vi.fn();
    const source: MentionSource = {
      cwd: "/workspace/app",
      self: "box-1/self",
      files: () => [],
      referenceFiles: { machine: "box-1", cwd: "/workspace/app", launcher: "claude", runtime: "claude" },
      fileManager: "mgr-files",
    };

    const requests: Array<{
      req: MentionDirectoryRequest;
      signal: AbortSignal;
      resolve: (value: FsListing | null) => void;
      reject: (reason: unknown) => void;
    }> = [];
    const loadDirectory = vi.fn((req: MentionDirectoryRequest, signal: AbortSignal) =>
      new Promise<FsListing | null>((resolve, reject) => {
        requests.push({ req, signal, resolve, reject });
      }),
    );

    const renderAt = (query: string, loader: MentionDirectoryLoader = loadDirectory) =>
      mount({
        from: "at",
        query,
        source,
        machineId: "box-1",
        sessions: [],
        loadDirectory: loader,
        onPick,
        onClose,
      });

    // Request #1 (A: src/)
    await renderAt("src/");
    await act(async () => void vi.advanceTimersByTime(150));
    expect(requests).toHaveLength(1);
    expect(requests[0].req).toEqual({
      machineId: "box-1",
      dir: "/workspace/app/src",
      cwd: "/workspace/app",
      taskFiles: true,
      fileManager: "mgr-files",
    });
    expect(host.textContent).toContain(t("chat-composerpanel.reading_folder"));

    // Re-rendering with a fresh inline loader callback does not abort or refetch request #1.
    await renderAt("src/", (req, sig) => loadDirectory(req, sig));
    await act(async () => void vi.advanceTimersByTime(150));
    expect(requests).toHaveLength(1);
    expect(requests[0].signal.aborted).toBe(false);

    // Switch to B (lib/): aborts request #1 and starts request #2.
    await renderAt("lib/");
    expect(requests[0].signal.aborted).toBe(true);
    await act(async () => void vi.advanceTimersByTime(150));
    expect(requests).toHaveLength(2);
    expect(requests[1].req.dir).toBe("/workspace/app/lib");

    // Switch back to A (src/): aborts request #2 and starts request #3.
    await renderAt("src/");
    expect(requests[1].signal.aborted).toBe(true);
    await act(async () => void vi.advanceTimersByTime(150));
    expect(requests).toHaveLength(3);
    expect(requests[2].req.dir).toBe("/workspace/app/src");
    expect(host.textContent).toContain(t("chat-composerpanel.reading_folder"));

    // Stale promise #1 (same key "src/", ignoring AbortSignal) resolves late: must not populate cache or clear loading.
    await act(async () => {
      requests[0].resolve(makeListing("/workspace/app/src", [{ name: "stale.ts", dir: false }]));
      await Promise.resolve();
    });
    await flush();
    expect(host.textContent).not.toContain("stale.ts");
    expect(host.textContent).toContain(t("chat-composerpanel.reading_folder"));

    // Stale promise #2 (lib/) rejects late: must not corrupt state or clear loading.
    await act(async () => {
      requests[1].reject(new Error("stale failure"));
      await Promise.resolve();
    });
    await flush();
    expect(host.textContent).toContain(t("chat-composerpanel.reading_folder"));

    // Fresh promise #3 resolves: renders only fresh entries and clears loading.
    await act(async () => {
      requests[2].resolve(makeListing("/workspace/app/src", [{ name: "fresh.ts", dir: false }]));
      await Promise.resolve();
    });
    await flush();
    expect(options().map((o) => o.textContent)).toEqual([expect.stringContaining("fresh.ts")]);
    expect(host.textContent).not.toContain("stale.ts");
    expect(host.textContent).not.toContain(t("chat-composerpanel.reading_folder"));

    // Sync throw from custom loader degrades cleanly to empty without leaving loading stuck.
    const throwingLoader = vi.fn(() => {
      throw new Error("sync failure");
    });
    await renderAt("broken/", throwingLoader);
    await act(async () => void vi.advanceTimersByTime(150));
    await flush();
    expect(throwingLoader).toHaveBeenCalledTimes(1);
    expect(options()).toHaveLength(0);
    expect(host.textContent).toContain(t("chat-composerpanel.no_match_in_folder"));

    expectNoDefaultSingletons();
  });
});

describe("ComposerPanel default compatibility", () => {
  it("reads useStore, fetches /machines/:mid/fs via api, and loads skills via useSkillCatalog when controlled props are absent", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const opened = makeSession("s2", { title: "Opened Agent", updated_at: "2026-11-21T12:00:00Z" });
    const summaryDup = makeSession("s2", { title: "Duplicate Summary", updated_at: "2026-11-21T11:00:00Z" });
    const summaryOnly = makeSession("s3", { title: "Summary Agent", updated_at: "2026-11-21T10:00:00Z" });
    mocks.useStore.mockImplementation((selector: (s: { sessions: Record<string, Session[]>; sessionSummaries: Session[] }) => unknown) =>
      selector({ sessions: { "box-1": [opened] }, sessionSummaries: [summaryDup, summaryOnly] }),
    );
    mocks.api.mockResolvedValue(makeListing("/workspace/app", [
      { name: "src", dir: true },
      { name: "notes.md", dir: false },
    ]));
    mocks.useSkillCatalog.mockReturnValue({
      catalog: {
        catalog_version: 1,
        complete: true,
        skills: [{ id: "sk-1", name: "deploy-check", description: "Verify release", source: "user" }],
      },
    });

    const source: MentionSource = {
      cwd: "/workspace/app",
      self: "box-1/self",
      files: () => ["/workspace/app/main.ts"],
      skills: { manager: "mgr-1", environment: "env-1" },
    };

    await mount({
      from: "plus",
      source,
      machineId: "box-1",
      onPick: vi.fn(),
      onClose: vi.fn(),
    });
    await act(async () => void vi.advanceTimersByTime(150));
    await flush();

    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useSkillCatalog).toHaveBeenCalledWith(source.skills, "box-1", "/workspace/app", undefined);
    expect(mocks.api).toHaveBeenCalledWith(
      "/machines/box-1/fs?path=%2Fworkspace%2Fapp&cwd=%2Fworkspace%2Fapp",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    const texts = options().map((o) => o.textContent ?? "").join("|");
    expect(texts).toContain("deploy-check");
    expect(texts).toContain("main.ts");
    expect(texts).toContain("src/");
    expect(texts).toContain("notes.md");
    expect(texts).toContain("Opened Agent");
    expect(texts).toContain("Summary Agent");
    expect(texts).not.toContain("Duplicate Summary");

    // Uncontrolled search filters locally and updates on folder disclosure click.
    setInputValue(searchInput()!, "notes");
    expect(options().map((o) => o.textContent)).toEqual([expect.stringContaining("notes.md")]);
    setInputValue(searchInput()!, "");
    act(() => option("src/")!.querySelector<HTMLElement>("[data-browse-folder]")!.click());
    expect(searchInput()!.value).toBe("src/");
  });
});
