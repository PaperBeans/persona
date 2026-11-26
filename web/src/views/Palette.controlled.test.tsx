// @vitest-environment jsdom
// Controlled Palette accepts explicit query, data, commands, files, search loaders, and navigation/toast callbacks without touching default singletons; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxItem } from "../lib/inbox";
import type { PaletteCommand } from "../lib/palette";
import { NO_PINS } from "../lib/pins";
import type { Route } from "../lib/router";
import type { PanesResponse } from "../lib/terminals";
import type { AssistantInfo, Machine, Session } from "../lib/types";
import type { AssistantSearchAnswer } from "../lib/assistant-search";
import type { PaletteNavPage, PaletteProps, PaletteRuntimeSearch, PaletteSavedSearches } from "./Palette";
import type { FileRow } from "./palette/FilesSection";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn((..._args: unknown[]): unknown => undefined),
  api: vi.fn((..._args: unknown[]): unknown => undefined),
  credentialOwner: vi.fn(() => "default-owner"),
  subscribeCredentialOwner: vi.fn((..._args: unknown[]) => () => {}),
  useShownManager: vi.fn(() => "default-manager"),
  shownManager: vi.fn(() => "default-manager"),
  useHerdrTabName: vi.fn(() => false),
  usePalette: vi.fn(() => ({ open: true, query: "" })),
  setPaletteQuery: vi.fn((..._args: unknown[]) => {}),
  openPaletteWith: vi.fn((..._args: unknown[]) => {}),
  contextCommands: vi.fn((..._args: unknown[]): PaletteCommand[] => []),
  useInbox: vi.fn((..._args: unknown[]): unknown => []),
  itemRoute: vi.fn((it: { id: string }) => ({ name: "inbox", id: it.id }) as Route),
  useNavPages: vi.fn((): { all: PaletteNavPage[] } => ({ all: [] })),
  useScope: vi.fn(() => ({ several: false })),
  filterAgentList: vi.fn((..._args: unknown[]) => {}),
  conversationPage: vi.fn((sid: string) => ({ name: "persona", sid }) as Route),
  href: vi.fn((route: Route) => `/${route.name}`),
  navigate: vi.fn((..._args: unknown[]) => {}),
  useAgentSessions: vi.fn((): Session[] => []),
  useRecentChats: vi.fn(() => ({ entries: [] as string[], current: "" })),
  useBindings: vi.fn(() => ({})),
  handlerOf: vi.fn((..._args: unknown[]): unknown => undefined),
  runShortcut: vi.fn((..._args: unknown[]) => false),
  toast: vi.fn((..._args: unknown[]) => {}),
  usePaletteFiles: vi.fn((..._args: unknown[]) => ({ ctx: null, status: "off" as const, rows: [] as FileRow[] })),
  openFileHit: vi.fn((..._args: unknown[]) => {}),
  copyFileMention: vi.fn((..._args: unknown[]) => {}),
  useSavedSearches: vi.fn((..._args: unknown[]) => ({
    owner: "default-owner",
    scope: '["default-owner","default-manager"]',
    model: null,
    state: { document: null, loading: false, busy: false, error: "", readonly: false, unsupported: false, canUndo: false, conflict: false },
    module: null,
    failed: false,
    retry: vi.fn(),
  })),
  useRuntimeSearch: vi.fn((..._args: unknown[]) => ({
    query: "",
    hits: [],
    loaded: false,
    loading: false,
    loadingMore: false,
    error: "",
    moreError: "",
    hasMore: false,
    total: 0,
    expired: false,
    supported: true,
    retryable: true,
    enabled: false,
    fallback: false,
    Notice: undefined,
    mergeHits: undefined,
    retry: vi.fn(),
    more: vi.fn(),
  })),
  conversationDeleted: vi.fn((..._args: unknown[]) => false),
  conversationDeletionScope: vi.fn((..._args: unknown[]) => 0),
  conversationDeletionRevision: vi.fn((..._args: unknown[]) => 0),
  nodePaletteLoads: 0,
  assistantSearchModuleLoads: 0,
}));

vi.mock("../lib/store", () => ({
  useStore: (...args: unknown[]) => mocks.useStore(...args),
}));

vi.mock("../lib/api", () => ({
  api: (...args: unknown[]) => mocks.api(...args),
  enc: encodeURIComponent,
  credentialOwner: () => mocks.credentialOwner(),
  subscribeCredentialOwner: (...args: unknown[]) => mocks.subscribeCredentialOwner(...args),
}));

vi.mock("../lib/manager-choice", () => ({
  useShownManager: () => mocks.useShownManager(),
  shownManager: () => mocks.shownManager(),
}));

vi.mock("../lib/conversation-deleted", () => ({
  conversationDeleted: (...args: unknown[]) => mocks.conversationDeleted(...args),
  conversationDeletionScope: (...args: unknown[]) => mocks.conversationDeletionScope(...args),
  conversationDeletionRevision: (...args: unknown[]) => mocks.conversationDeletionRevision(...args),
}));

vi.mock("../lib/agents-display", () => ({
  useHerdrTabName: () => mocks.useHerdrTabName(),
}));

vi.mock("../lib/palette", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/palette")>()),
  usePalette: () => mocks.usePalette(),
  setPaletteQuery: (...args: unknown[]) => mocks.setPaletteQuery(...args),
  openPaletteWith: (...args: unknown[]) => mocks.openPaletteWith(...args),
  contextCommands: (...args: unknown[]) => mocks.contextCommands(...args),
}));

vi.mock("../lib/inbox", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/inbox")>()),
  useInbox: (...args: unknown[]) => mocks.useInbox(...args),
  itemRoute: (...args: unknown[]) => mocks.itemRoute(...(args as [{ id: string }])),
}));

vi.mock("./Sidebar", () => ({
  useNavPages: () => mocks.useNavPages(),
}));

vi.mock("../lib/current-assistant", () => ({
  useScope: () => mocks.useScope(),
}));

vi.mock("../lib/agent-list-filters", () => ({
  filterAgentList: (...args: unknown[]) => mocks.filterAgentList(...args),
}));

vi.mock("../lib/conversation-page", () => ({
  conversationPage: (...args: unknown[]) => mocks.conversationPage(...(args as [string])),
}));

vi.mock("../lib/router", () => ({
  href: (...args: unknown[]) => mocks.href(...(args as [Route])),
  navigate: (...args: unknown[]) => mocks.navigate(...args),
}));

vi.mock("../lib/sessions", () => ({
  useAgentSessions: () => mocks.useAgentSessions(),
}));

vi.mock("../lib/recent-chats", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/recent-chats")>()),
  useRecentChats: () => mocks.useRecentChats(),
}));

vi.mock("../lib/shortcuts", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/shortcuts")>()),
  useBindings: () => mocks.useBindings(),
  handlerOf: (...args: unknown[]) => mocks.handlerOf(...args),
  runShortcut: (...args: unknown[]) => mocks.runShortcut(...args),
}));

vi.mock("../ui/atoms", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../ui/atoms")>()),
  toast: (...args: unknown[]) => mocks.toast(...args),
}));

vi.mock("./palette/FilesSection", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./palette/FilesSection")>()),
  usePaletteFiles: (...args: unknown[]) => mocks.usePaletteFiles(...args),
  openFileHit: (...args: unknown[]) => mocks.openFileHit(...args),
  copyFileMention: (...args: unknown[]) => mocks.copyFileMention(...args),
}));

vi.mock("./palette/SavedSearches", () => ({
  useSavedSearches: (...args: unknown[]) => mocks.useSavedSearches(...args),
}));

vi.mock("./palette/RuntimeSearch", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./palette/RuntimeSearch")>()),
  useRuntimeSearch: (...args: unknown[]) => mocks.useRuntimeSearch(...args),
}));

vi.mock("../lib/node-palette", () => {
  mocks.nodePaletteLoads += 1;
  return {};
});

vi.mock("../lib/assistant-search", async (importOriginal) => {
  mocks.assistantSearchModuleLoads += 1;
  return await importOriginal<typeof import("../lib/assistant-search")>();
});

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  (g.Element as { prototype: { scrollIntoView?: () => void } }).prototype.scrollIntoView = () => {};
});

const { Palette } = await import("./Palette");
const { t } = await import("../i18n");
const agentSearchReal = await import("../lib/agent-search");
await import("./palette/AgentConditionsMenu");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MACHINES: Machine[] = [
  { id: "m1", name: "Workstation", online: true },
  { id: "m2", name: "Build Node", online: true },
];

const AGENT_SESSION: Session = {
  machine_id: "m1",
  session_id: "s1",
  title: "Controlled Agent Work",
  cwd: "/work/persona",
  status: "idle",
  state: "idle",
  updated_at: "2026-11-20T10:00:00Z",
};

const ASSISTANT_INFO: AssistantInfo = {
  machine_id: "m1",
  main: "main-chat",
  conversations: [
    {
      machine_id: "m1",
      session_id: "main-chat",
      title: "Main",
      cwd: "/assistant",
      status: "idle",
      assistant: true,
      last_text: "Ready to help",
      updated_at: "2026-11-20T11:00:00Z",
    },
    {
      machine_id: "m1",
      session_id: "trip",
      title: "Trip Planning",
      cwd: "/assistant",
      status: "idle",
      assistant: true,
      last_text: "Hotel options",
      updated_at: "2026-11-20T09:00:00Z",
    },
  ],
};

const INBOX_ITEMS: InboxItem[] = [
  {
    id: "in-1",
    source: "mail",
    kind: "mail",
    account: "work",
    title: "Quarterly roadmap review",
    sender: "Alex",
    preview: "Please check slides",
    count: 1,
    updated_at: "2026-11-20T08:00:00Z",
  } as InboxItem,
];

const PAGES: PaletteNavPage[] = [
  { k: "settings", label: "Shell Settings", icon: "settings", to: { name: "settings" } },
];

const FILE_ROW: FileRow = {
  kind: "file",
  key: "f:src/app.ts",
  hit: { path: "src/app.ts", score: 90, matched: [4, 5, 6] },
  ctx: { mid: "m1", cwd: "/work/persona" },
};

const EMPTY_SAVED: PaletteSavedSearches = {
  owner: "shell-owner",
  scope: '["shell-owner","shell-manager"]',
  model: null,
  state: { document: null, loading: false, busy: false, error: "", readonly: false, unsupported: true, canUndo: false, conflict: false },
  module: null,
  failed: false,
  retry: () => {},
};

const EMPTY_RUNTIME: PaletteRuntimeSearch = {
  query: "",
  hits: [],
  loaded: false,
  loading: false,
  loadingMore: false,
  error: "",
  moreError: "",
  hasMore: false,
  total: 0,
  expired: false,
  supported: true,
  retryable: true,
  enabled: false,
  fallback: false,
  Notice: undefined,
  mergeHits: undefined,
  retry: () => {},
  more: () => {},
};

let host: HTMLDivElement;
let root: Root;

const mount = async (props: PaletteProps) => {
  await act(async () => {
    root.render(<Palette {...props} />);
  });
};

const flush = async () => {
  for (let i = 0; i < 6; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const options = () => [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')];
const optionByText = (text: string) => options().find((el) => el.textContent?.includes(text));
const field = () => document.querySelector<HTMLInputElement>('[role="combobox"]')!;

const assertNoDefaultSingletonsTouched = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
  expect(mocks.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(mocks.useShownManager).not.toHaveBeenCalled();
  expect(mocks.shownManager).not.toHaveBeenCalled();
  expect(mocks.useHerdrTabName).not.toHaveBeenCalled();
  expect(mocks.usePalette).not.toHaveBeenCalled();
  expect(mocks.setPaletteQuery).not.toHaveBeenCalled();
  expect(mocks.openPaletteWith).not.toHaveBeenCalled();
  expect(mocks.contextCommands).not.toHaveBeenCalled();
  expect(mocks.useInbox).not.toHaveBeenCalled();
  expect(mocks.itemRoute).not.toHaveBeenCalled();
  expect(mocks.useNavPages).not.toHaveBeenCalled();
  expect(mocks.useScope).not.toHaveBeenCalled();
  expect(mocks.filterAgentList).not.toHaveBeenCalled();
  expect(mocks.conversationPage).not.toHaveBeenCalled();
  expect(mocks.href).not.toHaveBeenCalled();
  expect(mocks.navigate).not.toHaveBeenCalled();
  expect(mocks.useAgentSessions).not.toHaveBeenCalled();
  expect(mocks.useRecentChats).not.toHaveBeenCalled();
  expect(mocks.useBindings).not.toHaveBeenCalled();
  expect(mocks.handlerOf).not.toHaveBeenCalled();
  expect(mocks.runShortcut).not.toHaveBeenCalled();
  expect(mocks.toast).not.toHaveBeenCalled();
  expect(mocks.usePaletteFiles).not.toHaveBeenCalled();
  expect(mocks.openFileHit).not.toHaveBeenCalled();
  expect(mocks.copyFileMention).not.toHaveBeenCalled();
  expect(mocks.useSavedSearches).not.toHaveBeenCalled();
  expect(mocks.useRuntimeSearch).not.toHaveBeenCalled();
  expect(mocks.conversationDeleted).not.toHaveBeenCalled();
  expect(mocks.nodePaletteLoads).toBe(0);
  expect(mocks.assistantSearchModuleLoads).toBe(0);
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.nodePaletteLoads = 0;
  mocks.assistantSearchModuleLoads = 0;
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("Palette with controlled props", () => {
  const controlledBase = (overrides: Partial<PaletteProps> = {}): PaletteProps => ({
    open: true,
    onClose: vi.fn(),
    onNewAgent: vi.fn(),
    query: "",
    onQueryChange: vi.fn(),
    credentialOwner: "shell-owner",
    manager: "shell-manager",
    deletionScope: 0,
    deletionRevision: 0,
    preferTab: false,
    sessions: [AGENT_SESSION],
    recentChats: { entries: ["s:m1/s1"], current: "" },
    assistant: ASSISTANT_INFO,
    machines: MACHINES,
    pins: NO_PINS,
    interactions: [],
    features: [],
    completeList: true,
    bindings: {},
    inboxItems: INBOX_ITEMS,
    pages: PAGES,
    panesByMachine: {},
    files: { ctx: { mid: "m1", cwd: "/work/persona" }, status: "ready", rows: [FILE_ROW] },
    savedSearches: EMPTY_SAVED,
    runtimeSearch: EMPTY_RUNTIME,
    loadNodeCommands: false,
    onNavigate: vi.fn(),
    resolveConversationRoute: (s, at) => ({ name: "persona", sid: s.session_id, ...(at ? { at } : {}) }),
    resolveInboxRoute: (it) => ({ name: "inbox", id: it.id }),
    onOpenFile: vi.fn(),
    onCopyFileMention: vi.fn(),
    onFilterAgentList: vi.fn(),
    showAgentsFilter: true,
    onNotify: vi.fn(),
    closeOnBack: false,
    ...overrides,
  });

  it("renders controlled jump results and routes session, inbox, place, file, filter, and condition-menu actions with zero default singleton calls", async () => {
    const controlledSaved: PaletteSavedSearches = {
      ...EMPTY_SAVED,
      module: {
        ...agentSearchReal,
        placeWords: () => "",
        runtimeOrder: () => 0,
        SavedSearchQuickRow: () => null,
        SavedSearchesDialog: () => null,
        SavedSearchFooter: () => null,
        EmptySearch: () => null,
        FilterAgentsList: ({ onFilter }: { onFilter: () => void }) => (
          <button type="button" data-testid="palette-filter-list" onClick={onFilter}>Filter controlled</button>
        ),
      } as unknown as PaletteSavedSearches["module"],
    };
    const props = controlledBase({ savedSearches: controlledSaved });
    await mount(props);

    expect(optionByText("Controlled Agent Work")).toBeDefined();
    expect(optionByText("Trip Planning")).toBeDefined();
    expect(optionByText("Quarterly roadmap review")).toBeDefined();
    expect(optionByText("Shell Settings")).toBeDefined();
    expect(optionByText("app.ts")).toBeDefined();

    // Picking a place navigates via controlled callback
    act(() => optionByText("Shell Settings")!.click());
    expect(props.onClose).toHaveBeenCalledTimes(1);
    expect(props.onNavigate).toHaveBeenCalledWith({ name: "settings" });

    // Picking an inbox item uses resolveInboxRoute + onNavigate
    act(() => optionByText("Quarterly roadmap review")!.click());
    expect(props.onNavigate).toHaveBeenLastCalledWith({ name: "inbox", id: "in-1" });

    // Picking an assistant chat uses resolveConversationRoute + onNavigate
    act(() => optionByText("Trip Planning")!.click());
    expect(props.onNavigate).toHaveBeenLastCalledWith({ name: "persona", sid: "trip" });

    // Picking a file calls onOpenFile
    act(() => optionByText("app.ts")!.click());
    expect(props.onOpenFile).toHaveBeenCalledWith(FILE_ROW);

    // Mod+Enter on active file calls onCopyFileMention
    act(() => optionByText("app.ts")!.dispatchEvent(new MouseEvent("mousemove", { bubbles: true })));
    act(() => {
      field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true, cancelable: true }));
    });
    expect(props.onCopyFileMention).toHaveBeenCalledWith(FILE_ROW);

    // Query matching an Agent enables controlled Filter Agents list (Shift+Enter and button click)
    await mount({ ...props, query: "Controlled", listShown: false });
    const filterBtn = document.querySelector<HTMLButtonElement>('[data-testid="palette-filter-list"]')!;
    expect(filterBtn).not.toBeNull();
    act(() => filterBtn.click());
    expect(props.onFilterAgentList).toHaveBeenCalledWith("Controlled");
    expect(props.onNavigate).toHaveBeenLastCalledWith({ name: "agent-list" });

    // Opening AgentConditionsMenu uses controlled machines and pins without touching useStore
    const condBtn = document.querySelector<HTMLButtonElement>('[data-testid="palette-agent-filter"]')!;
    act(() => condBtn.click());
    await act(async () => {
      await vi.dynamicImportSettled();
    });
    await flush();
    expect(document.body.textContent).toContain(t("palette.agent_conditions"));

    assertNoDefaultSingletonsTouched();
  });

  it("renders controlled command mode and routes explicit commands and unavailable-command notifications without touching singletons", async () => {
    const runExport = vi.fn();
    const commands: PaletteCommand[] = [
      { id: "shell:export", title: "Export Shell Snapshot", section: "Shell", icon: "file", run: runExport },
      { id: "shell:reset", title: "Reset Layout", section: "Shell", icon: "undo", run: vi.fn() },
    ];
    const props = controlledBase({ query: ">Export", commands, bindings: undefined });
    await mount(props);

    expect(options()).toHaveLength(1);
    expect(optionByText("Export Shell Snapshot")).toBeDefined();
    act(() => optionByText("Export Shell Snapshot")!.click());
    expect(props.onClose).toHaveBeenCalledTimes(1);
    expect(runExport).toHaveBeenCalledTimes(1);

    // Controlled contextCommands + isCommandAvailable + onRunCommand returning false routes notice to onNotify
    const onRunCommand = vi.fn(() => false);
    const onNotify = vi.fn();
    await mount(controlledBase({
      query: ">settings",
      commands: undefined,
      contextCommands: [{ id: "ctx:custom", section: "Shell", title: "Custom context action", run: vi.fn() }],
      isCommandAvailable: (id) => id === "settings",
      onRunCommand,
      onNotify,
    }));
    const settingsCmd = options().find((el) => el.textContent?.includes(t("lib-shortcuts.cmd_settings")));
    expect(settingsCmd).toBeDefined();
    act(() => settingsCmd!.click());
    expect(onRunCommand).toHaveBeenCalledWith("settings", undefined);
    expect(onNotify).toHaveBeenCalledWith(t("palette.command_unavailable"));

    assertNoDefaultSingletonsTouched();
  });

  it("isolates controlled searchAssistant and loadMachinePanes across ABA query and scope switches", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const searchCalls: { query: string; signal: AbortSignal; resolve: (ans: AssistantSearchAnswer) => void }[] = [];
    const searchAssistant = vi.fn((query: string, signal: AbortSignal) => new Promise<AssistantSearchAnswer>((resolve) => {
      searchCalls.push({ query, signal, resolve });
    }));
    const paneCalls: { mid: string; signal: AbortSignal; resolve: (res: PanesResponse) => void }[] = [];
    const loadMachinePanes = vi.fn((mid: string, signal: AbortSignal) => new Promise<PanesResponse>((resolve) => {
      paneCalls.push({ mid, signal, resolve });
    }));

    const base = controlledBase({
      query: "needle",
      panesByMachine: undefined,
      loadMachinePanes,
      searchAssistant,
      files: { ctx: null, status: "off", rows: [] },
      pages: [],
      inboxItems: [],
    });

    // Query A ("needle") triggers assistant search #1 + pane loads
    await mount(base);
    await act(async () => { vi.advanceTimersByTime(210); });
    await flush();
    expect(searchCalls).toHaveLength(1);
    expect(searchCalls[0].query).toBe("needle");
    expect(paneCalls.map((c) => c.mid)).toEqual(["m1", "m2"]);

    // Switch to query B ("other")
    await mount({ ...base, query: "other" });
    expect(searchCalls[0].signal.aborted).toBe(true);
    await act(async () => { vi.advanceTimersByTime(210); });
    await flush();
    expect(searchCalls).toHaveLength(2);

    // Switch back to query A ("needle") — ABA!
    await mount({ ...base, query: "needle" });
    await act(async () => { vi.advanceTimersByTime(210); });
    await flush();
    expect(searchCalls).toHaveLength(3);

    // Stale search #1 resolves late (ignoring AbortSignal): must not populate results
    await act(async () => {
      searchCalls[0].resolve({
        results: [{ session_id: "stale", machine_id: "m1", title: "Stale Match", hits: [{ item_id: "i1", seq: 1, role: "user", snippet: "stale needle", ranges: [[6, 12]] }] }],
        partial: false,
      });
    });
    await flush();
    expect(document.body.textContent).not.toContain("Stale Match");

    // Current search #3 resolves: renders highlighted match
    await act(async () => {
      searchCalls[2].resolve({
        results: [{ session_id: "fresh", machine_id: "m1", title: "Fresh Match", hits: [{ item_id: "i2", seq: 2, role: "user", snippet: "found needle here", ranges: [[6, 12]] }] }],
        partial: false,
      });
    });
    await flush();
    expect(document.body.textContent).toContain("Fresh Match");
    expect(document.querySelector("[data-said] mark")?.textContent).toBe("needle");

    // Scope switch A -> B -> A closes palette and drops stale pane loads from prior scope A
    await mount({ ...base, credentialOwner: "shell-owner-b", query: "" });
    expect(base.onClose).toHaveBeenCalledTimes(1);
    expect(paneCalls[0].signal.aborted).toBe(true);
    await mount({ ...base, credentialOwner: "shell-owner", query: "" });
    await act(async () => {
      paneCalls[0].resolve({
        spaces: [{ name: "OldSpace", tabs: [{ name: "OldTab", panes: [{ id: "p1", title: "Stale Pane" }] }] }],
      } as unknown as PanesResponse);
    });
    await flush();
    expect(document.body.textContent).not.toContain("Stale Pane");

    assertNoDefaultSingletonsTouched();
  });

  it("re-initializes custom loadNodeCommands across close/reopen, manager switches, and failed loads without reusing stale callbacks", async () => {
    const calls: string[] = [];
    let registered: PaletteCommand[] = [];
    const context = () => registered;
    const makeLoader = (manager: string) =>
      vi.fn(async () => {
        registered = [
          {
            id: "node:scoped",
            title: "Scoped Node Action",
            section: "Node",
            run: () => {
              calls.push(manager);
            },
          },
        ];
      });
    const loaderA = makeLoader("manager-a");
    const loaderB = makeLoader("manager-b");
    const base = controlledBase({
      query: ">Scoped",
      manager: "manager-a",
      commands: undefined,
      contextCommands: context,
      isCommandAvailable: () => false,
      onRunCommand: vi.fn(),
      loadNodeCommands: loaderA,
    });

    await mount(base);
    await flush();
    expect(loaderA).toHaveBeenCalledTimes(1);
    act(() => optionByText("Scoped Node Action")!.click());
    expect(calls).toEqual(["manager-a"]);

    // Close, switch manager + loader while closed, then reopen under manager B
    await mount({ ...base, open: false });
    await mount({ ...base, manager: "manager-b", open: false, loadNodeCommands: loaderB });
    await mount({ ...base, manager: "manager-b", open: true, loadNodeCommands: loaderB });
    await flush();
    expect(loaderA).toHaveBeenCalledTimes(1);
    expect(loaderB).toHaveBeenCalledTimes(1);
    act(() => optionByText("Scoped Node Action")!.click());
    expect(calls).toEqual(["manager-a", "manager-b"]);

    // Close and reopen under the same manager resets custom readiness and runs its loader again
    await mount({ ...base, manager: "manager-b", open: false, loadNodeCommands: loaderB });
    await mount({ ...base, manager: "manager-b", open: true, loadNodeCommands: loaderB });
    await flush();
    expect(loaderB).toHaveBeenCalledTimes(2);

    // Same loader function reference across manager switch + retry after failure
    let activeManager = "manager-c";
    let failOnce = true;
    const sharedLoader = vi.fn(async () => {
      if (failOnce) {
        failOnce = false;
        throw new Error("temporary failure");
      }
      registered = [
        {
          id: "node:scoped",
          title: "Scoped Node Action",
          section: "Node",
          run: () => {
            calls.push(activeManager);
          },
        },
      ];
    });
    await mount({ ...base, manager: "manager-c", open: false, loadNodeCommands: sharedLoader });
    await mount({ ...base, manager: "manager-c", open: true, loadNodeCommands: sharedLoader });
    await flush();
    expect(sharedLoader).toHaveBeenCalledTimes(1);

    // Failed custom load retries on next open
    await mount({ ...base, manager: "manager-c", open: false, loadNodeCommands: sharedLoader });
    await mount({ ...base, manager: "manager-c", open: true, loadNodeCommands: sharedLoader });
    await flush();
    expect(sharedLoader).toHaveBeenCalledTimes(2);
    act(() => optionByText("Scoped Node Action")!.click());
    expect(calls).toEqual(["manager-a", "manager-b", "manager-c"]);

    // Switching manager while open with the same loader function identity also re-initializes
    activeManager = "manager-d";
    await mount({ ...base, manager: "manager-d", open: true, loadNodeCommands: sharedLoader });
    await flush();
    expect(sharedLoader).toHaveBeenCalledTimes(3);
    act(() => optionByText("Scoped Node Action")!.click());
    expect(calls).toEqual(["manager-a", "manager-b", "manager-c", "manager-d"]);

    assertNoDefaultSingletonsTouched();
  });

  it("rejects stale in-flight custom loadNodeCommands completions across open manager switches and ABA transitions", async () => {
    let resolveA!: () => void;
    let registered = false;
    const loaderA = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveA = () => {
            registered = true;
            resolve();
          };
        }),
    );
    const loaderB = vi.fn().mockResolvedValue(undefined);
    const available = (id: string) => registered && id === "settings";
    const base = controlledBase({
      query: ">settings",
      manager: "manager-a",
      commands: undefined,
      contextCommands: [],
      isCommandAvailable: available,
      onRunCommand: vi.fn(),
      loadNodeCommands: loaderA,
    });

    await mount(base);
    await flush();
    expect(loaderA).toHaveBeenCalledTimes(1);
    expect(options().some((el) => el.textContent?.includes(t("lib-shortcuts.cmd_settings")))).toBe(false);

    // Switch to manager B while still open, then settle stale A: A's late resolution must not surface settings in B
    await mount({ ...base, manager: "manager-b", loadNodeCommands: loaderB });
    await act(async () => resolveA());
    await flush();
    expect(loaderB).toHaveBeenCalledTimes(1);
    expect(options().some((el) => el.textContent?.includes(t("lib-shortcuts.cmd_settings")))).toBe(false);

    // ABA with deferred loaders: A1 -> B -> A2; late A1 and late B must not publish readiness, only current A2 does
    const dispatched: string[] = [];
    let scopedCmds: PaletteCommand[] = [];
    const context = () => scopedCmds;
    const resolvesA: (() => void)[] = [];
    const deferredA = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          const attempt = resolvesA.length + 1;
          resolvesA.push(() => {
            scopedCmds = [
              {
                id: `node:a-${attempt}`,
                title: `ABA Action A${attempt}`,
                section: "Node",
                run: () => {
                  dispatched.push(`a-${attempt}`);
                },
              },
            ];
            resolve();
          });
        }),
    );
    let resolveB!: () => void;
    const deferredB = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveB = () => {
            scopedCmds = [
              {
                id: "node:b-1",
                title: "ABA Action B1",
                section: "Node",
                run: () => {
                  dispatched.push("b-1");
                },
              },
            ];
            resolve();
          };
        }),
    );

    const abaBase = controlledBase({
      query: ">ABA Action",
      manager: "manager-a",
      commands: undefined,
      contextCommands: context,
      isCommandAvailable: () => false,
      onRunCommand: vi.fn(),
      loadNodeCommands: deferredA,
    });

    await mount(abaBase);
    await flush();
    expect(deferredA).toHaveBeenCalledTimes(1);
    expect(options()).toHaveLength(0);

    // Switch to B (deferred), then stale A1 settles while B is pending
    await mount({ ...abaBase, manager: "manager-b", loadNodeCommands: deferredB });
    await flush();
    expect(deferredB).toHaveBeenCalledTimes(1);
    await act(async () => resolvesA[0]());
    await flush();
    expect(optionByText("ABA Action A1")).toBeUndefined();

    // Switch back to A (ABA: starts A2), then stale B settles while A2 is pending
    scopedCmds = [];
    await mount({ ...abaBase, manager: "manager-a", loadNodeCommands: deferredA });
    await flush();
    expect(deferredA).toHaveBeenCalledTimes(2);
    await act(async () => resolveB());
    await flush();
    expect(optionByText("ABA Action B1")).toBeUndefined();

    // Current A2 resolves and dispatches A2
    await act(async () => resolvesA[1]());
    await flush();
    expect(optionByText("ABA Action A2")).toBeDefined();
    act(() => optionByText("ABA Action A2")!.click());
    expect(dispatched).toEqual(["a-2"]);

    assertNoDefaultSingletonsTouched();
  });
});

describe("Palette default compatibility", () => {
  it("falls back to default store, palette, inbox, nav pages, router, and toast when optional props are omitted", async () => {
    const storeState = {
      assistant: ASSISTANT_INFO,
      machines: MACHINES,
      pins: NO_PINS,
      interactions: [],
      sessions: { m1: [AGENT_SESSION] },
      features: [],
      todosSupported: true,
    };
    mocks.useStore.mockImplementation((selector: unknown) => (selector as (s: typeof storeState) => unknown)(storeState));
    mocks.useAgentSessions.mockReturnValue([AGENT_SESSION]);
    mocks.useInbox.mockImplementation((selector: unknown) => (selector as (s: { items: InboxItem[] }) => unknown)({ items: INBOX_ITEMS }));
    mocks.useNavPages.mockReturnValue({ all: [{ k: "settings", label: "Settings", icon: "settings", to: { name: "settings" } }] });
    mocks.usePalette.mockReturnValue({ open: false, query: "" });
    const onClose = vi.fn();
    const onNewAgent = vi.fn();

    // Mounted closed does not eagerly import default node-palette
    await mount({ open: false, onClose, onNewAgent });
    await act(async () => {
      await vi.dynamicImportSettled();
    });
    await flush();
    expect(mocks.nodePaletteLoads).toBe(0);

    mocks.usePalette.mockReturnValue({ open: true, query: "" });
    await mount({ open: true, onClose, onNewAgent });
    await act(async () => {
      await vi.dynamicImportSettled();
    });
    await flush();
    expect(mocks.nodePaletteLoads).toBe(1);
    expect(mocks.usePalette).toHaveBeenCalled();
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useInbox).toHaveBeenCalled();
    expect(mocks.useNavPages).toHaveBeenCalled();
    expect(mocks.useSavedSearches).toHaveBeenCalled();
    expect(mocks.useRuntimeSearch).toHaveBeenCalled();
    expect(mocks.usePaletteFiles).toHaveBeenCalled();

    act(() => optionByText("Controlled Agent Work")!.click());
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.navigate).toHaveBeenCalledWith({ name: "session", mid: "m1", sid: "s1" });

    // Default command mode notifies via global toast when a shortcut handler fails
    mocks.usePalette.mockReturnValue({ open: true, query: ">settings" });
    mocks.handlerOf.mockImplementation((id: unknown) => (id === "settings" ? () => {} : undefined));
    mocks.runShortcut.mockReturnValue(false);
    await mount({ open: true, onClose, onNewAgent });
    const settingsCmd = options().find((el) => el.textContent?.includes(t("lib-shortcuts.cmd_settings")));
    expect(settingsCmd).toBeDefined();
    act(() => settingsCmd!.click());
    expect(mocks.runShortcut).toHaveBeenCalledWith("settings", { digit: undefined });
    expect(mocks.toast).toHaveBeenCalledWith(t("palette.command_unavailable"));

    // Closing and reopening in default mode keeps one-time node-palette readiness and lists default contextCommands
    const defaultNodeRun = vi.fn();
    mocks.contextCommands.mockReturnValue([{ id: "node:default-check", section: "Node", title: "Default Node Action", run: defaultNodeRun }]);
    mocks.usePalette.mockReturnValue({ open: false, query: "" });
    await mount({ open: false, onClose, onNewAgent });
    mocks.usePalette.mockReturnValue({ open: true, query: ">Default Node" });
    await mount({ open: true, onClose, onNewAgent });
    await act(async () => {
      await vi.dynamicImportSettled();
    });
    await flush();
    expect(mocks.nodePaletteLoads).toBe(1);
    expect(optionByText("Default Node Action")).toBeDefined();
    act(() => optionByText("Default Node Action")!.click());
    expect(defaultNodeRun).toHaveBeenCalledTimes(1);
  });
});
