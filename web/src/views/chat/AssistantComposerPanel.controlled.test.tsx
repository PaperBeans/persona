// @vitest-environment jsdom
// Controlled shells supply machines, workspaces, repositories, or custom loaders without touching default store/API/auth singletons; default fallbacks stay pinned too.
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MentionRepository } from "../../lib/mentions-repositories";
import type { Machine } from "../../lib/types";
import type { Workspace } from "../../lib/workspace-board";
import type { AssistantComposerPanelProps } from "./AssistantComposerPanel";
import type { AssistantMentions, ComposerPanel, MentionSource } from "./ComposerPanel";

type CapturedPanelProps = ComponentProps<typeof ComposerPanel> & Record<string, unknown>;

const mocks = vi.hoisted(() => {
  let owner = "owner-1";
  const listeners = new Set<() => void>();
  return {
    api: vi.fn(),
    loadMentionRepositories: vi.fn(),
    useStore: vi.fn(),
    credentialOwner: vi.fn(() => owner),
    subscribeCredentialOwner: vi.fn((listener: () => void) => {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    }),
    setOwner: (next: string) => {
      owner = next;
      listeners.forEach((listener) => listener());
    },
    resetOwner: () => {
      owner = "owner-1";
      listeners.clear();
    },
    captured: [] as CapturedPanelProps[],
  };
});

vi.mock("../../lib/api", () => ({
  api: (...args: unknown[]) => mocks.api(...args),
  credentialOwner: () => mocks.credentialOwner(),
  subscribeCredentialOwner: (listener: () => void) => mocks.subscribeCredentialOwner(listener),
}));

vi.mock("../../lib/mentions-repositories", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/mentions-repositories")>()),
  loadMentionRepositories: (...args: unknown[]) => mocks.loadMentionRepositories(...args),
}));

vi.mock("../../lib/store", () => ({
  useStore: (selector: (state: { machines: Machine[] }) => unknown) => mocks.useStore(selector),
}));

vi.mock("./ComposerPanel", () => ({
  ComposerPanel: (props: CapturedPanelProps) => {
    mocks.captured.push(props);
    return <div data-mock-composer-panel data-loading={String(Boolean(props.assistant?.loading))} />;
  },
}));

const { AssistantComposerPanel } = await import("./AssistantComposerPanel");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MACHINES: Machine[] = [
  { id: "m1", name: "Laptop", hub_id: "h1", hub_name: "Home" } as Machine,
  { id: "m2", name: "Server", hub_id: "h2", hub_name: "Office" } as Machine,
];

const WORKSPACES: Workspace[] = [
  { id: "w1", machine_id: "m1", source: "herdr", source_id: "1", title: "weather", cwd: "/home/me/code/weather", branch: "main", online: true, available: true },
  { id: "w2", machine_id: "m2", source: "session", source_id: "2", title: "notes", cwd: "/home/me/notes", online: true, available: true },
];

const REPOSITORIES: MentionRepository[] = [
  {
    id: "r1",
    provider: "github",
    path: "example/persona",
    canonical_url: "https://github.com/example/persona",
    name: "Persona",
    aliases: ["hub"],
    purpose: "Main monorepo",
  },
  {
    id: "r2",
    provider: "gitlab",
    path: "team/billing",
    canonical_url: "https://gitlab.example.com/team/billing",
    purpose: "Billing service",
  },
];

let host: HTMLDivElement;
let root: Root;

const latestProps = (): CapturedPanelProps => mocks.captured[mocks.captured.length - 1]!;
const latestAssistant = (): AssistantMentions => latestProps().assistant!;

const mount = async (props: Partial<AssistantComposerPanelProps> = {}) => {
  const source: MentionSource = props.source ?? { files: () => [], workspaces: true, repositories: "assistant" };
  await act(async () => {
    root.render(
      <AssistantComposerPanel
        from="at"
        query=""
        source={source}
        onPick={vi.fn()}
        onClose={vi.fn()}
        {...props}
      />,
    );
  });
};

const flush = async () => {
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.loadMentionRepositories).not.toHaveBeenCalled();
  expect(mocks.subscribeCredentialOwner).not.toHaveBeenCalled();
  expect(mocks.credentialOwner).not.toHaveBeenCalled();
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.resetOwner();
  mocks.captured.length = 0;
  mocks.useStore.mockImplementation((selector: (state: { machines: Machine[] }) => unknown) => selector({ machines: MACHINES }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("AssistantComposerPanel with controlled props", () => {
  it("renders controlled machines, workspaces, and repositories without calling default singletons or leaking wrapper props", async () => {
    await mount({ machines: MACHINES, workspaces: null, repositories: REPOSITORIES });
    expect(latestAssistant().loading).toBe(true);
    expectNoDefaultSingletons();

    await mount({ machines: MACHINES, workspaces: WORKSPACES, repositories: null });
    expect(latestAssistant().loading).toBe(true);
    expectNoDefaultSingletons();

    await mount({ machines: MACHINES, workspaces: WORKSPACES, repositories: REPOSITORIES });
    const assistant = latestAssistant();
    expect(assistant.loading).toBe(false);
    expect(assistant.rows("")).toEqual([
      expect.objectContaining({ kind: "workspace", id: "workspace:w1", name: "weather", detail: "Home · Laptop · ~/code/weather · main" }),
      expect.objectContaining({ kind: "workspace", id: "workspace:w2", name: "notes", detail: "Office · Server · ~/notes" }),
      expect.objectContaining({ kind: "repository", id: "repository:r1", name: "Persona" }),
      expect.objectContaining({ kind: "repository", id: "repository:r2", name: "team/billing" }),
    ]);
    expect(assistant.rows("office")).toEqual([
      expect.objectContaining({ kind: "workspace", id: "workspace:w2", name: "notes" }),
    ]);
    expect(assistant.browsingRows?.("example/persona")).toEqual([
      expect.objectContaining({ kind: "repository", id: "repository:r1", name: "Persona" }),
    ]);

    const forwarded = latestProps();
    for (const key of ["machines", "machineName", "workspaces", "loadWorkspaces", "repositories", "loadRepositories", "credentialOwner"]) {
      expect(Object.prototype.hasOwnProperty.call(forwarded, key)).toBe(false);
    }
    expectNoDefaultSingletons();
  });

  it("supports a controlled machineName resolver and single-source controlled panels without touching default singletons", async () => {
    await mount({
      source: { files: () => [], workspaces: true },
      machineName: (id) => `custom:${id}`,
      workspaces: WORKSPACES,
    });
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().browsingRows).toBeUndefined();
    expect(latestAssistant().rows("custom:m1")).toEqual([
      expect.objectContaining({ kind: "workspace", id: "workspace:w1", detail: "custom:m1 · ~/code/weather · main" }),
    ]);
    expectNoDefaultSingletons();

    await mount({
      source: { files: () => [], repositories: "assistant" },
      repositories: REPOSITORIES,
    });
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().rows("hub")).toEqual([
      expect.objectContaining({ kind: "repository", id: "repository:r1", name: "Persona" }),
    ]);
    expectNoDefaultSingletons();
  });

  it("loads via controlled loaders, handles credentialOwner/manager changes, degrades on errors, and aborts on unmount", async () => {
    let resolveWs!: (rows: Workspace[]) => void;
    const wsSignals: AbortSignal[] = [];
    const repoSignals: AbortSignal[] = [];
    const loadWorkspaces = vi.fn((signal: AbortSignal) => {
      wsSignals.push(signal);
      return new Promise<Workspace[]>((resolve) => { resolveWs = resolve; });
    });
    const loadRepositories = vi.fn((manager: string | undefined, signal: AbortSignal) => {
      repoSignals.push(signal);
      return Promise.resolve(manager === "alt" ? [REPOSITORIES[1]] : REPOSITORIES);
    });

    await mount({
      machineName: (id) => `box-${id}`,
      loadWorkspaces: (signal) => loadWorkspaces(signal),
      loadRepositories: (manager, signal) => loadRepositories(manager, signal),
      credentialOwner: "scope-a",
    });
    expect(latestAssistant().loading).toBe(true);
    expect(loadWorkspaces).toHaveBeenCalledTimes(1);
    expect(loadRepositories).toHaveBeenCalledWith("assistant", expect.any(AbortSignal));

    // Fresh inline loader closures do not re-trigger fetches.
    await mount({
      machineName: (id) => `box-${id}`,
      loadWorkspaces: (signal) => loadWorkspaces(signal),
      loadRepositories: (manager, signal) => loadRepositories(manager, signal),
      credentialOwner: "scope-a",
    });
    expect(loadWorkspaces).toHaveBeenCalledTimes(1);
    expect(loadRepositories).toHaveBeenCalledTimes(1);

    await act(async () => { resolveWs(WORKSPACES); });
    await flush();
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().rows("weather")).toEqual([
      expect.objectContaining({ kind: "workspace", id: "workspace:w1", detail: "box-m1 · ~/code/weather · main" }),
    ]);

    // Switching credentialOwner or manager triggers a fresh controlled repository load.
    await mount({
      source: { files: () => [], workspaces: true, repositories: "alt" },
      machineName: (id) => `box-${id}`,
      loadWorkspaces,
      loadRepositories,
      credentialOwner: "scope-b",
    });
    await flush();
    expect(loadRepositories).toHaveBeenLastCalledWith("alt", expect.any(AbortSignal));
    expect(latestAssistant().rows("billing")).toEqual([
      expect.objectContaining({ kind: "repository", id: "repository:r2" }),
    ]);
    expect(latestAssistant().rows("Persona")).toEqual([]);
    expectNoDefaultSingletons();

    // Unmount aborts an in-flight loader and skips late resolution.
    let lateResolve!: (rows: Workspace[]) => void;
    const pendingWs = vi.fn((signal: AbortSignal) => {
      wsSignals.push(signal);
      return new Promise<Workspace[]>((resolve) => { lateResolve = resolve; });
    });
    act(() => root.unmount());
    root = createRoot(host);
    await mount({
      source: { files: () => [], workspaces: true, repositories: "assistant" },
      machines: MACHINES,
      loadWorkspaces: pendingWs,
      loadRepositories: () => { throw new Error("sync repo failure"); },
    });
    await flush();
    expect(latestAssistant().loading).toBe(true);
    expect(latestAssistant().rows("Persona")).toEqual([]);
    const activeSignal = wsSignals[wsSignals.length - 1]!;
    expect(activeSignal.aborted).toBe(false);
    act(() => root.unmount());
    expect(activeSignal.aborted).toBe(true);
    lateResolve(WORKSPACES);
    expectNoDefaultSingletons();
  });

  it("skips loaders and rows when neither workspaces nor repositories are enabled", async () => {
    const loadWorkspaces = vi.fn();
    const loadRepositories = vi.fn();
    await mount({
      source: { files: () => [] },
      loadWorkspaces,
      loadRepositories,
    });
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().browsingRows).toBeUndefined();
    expect(latestAssistant().rows("")).toEqual([]);
    expect(loadWorkspaces).not.toHaveBeenCalled();
    expect(loadRepositories).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });
});

describe("AssistantComposerPanel default compatibility", () => {
  it("reads store machines, fetches /workspaces and bound repositories, and reloads when credentialOwner changes", async () => {
    mocks.api.mockResolvedValueOnce({ workspaces: WORKSPACES });
    mocks.loadMentionRepositories.mockResolvedValueOnce(REPOSITORIES);

    await mount();
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.subscribeCredentialOwner).toHaveBeenCalled();
    expect(mocks.api).toHaveBeenCalledWith("/workspaces", { signal: expect.any(AbortSignal) });
    expect(mocks.loadMentionRepositories).toHaveBeenCalledWith("assistant", expect.any(AbortSignal));

    await flush();
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().rows("weather")).toEqual([
      expect.objectContaining({ kind: "workspace", id: "workspace:w1", detail: "Home · Laptop · ~/code/weather · main" }),
    ]);
    expect(latestAssistant().rows("Persona")).toEqual([
      expect.objectContaining({ kind: "repository", id: "repository:r1" }),
    ]);

    mocks.loadMentionRepositories.mockResolvedValueOnce([REPOSITORIES[1]]);
    act(() => mocks.setOwner("owner-2"));
    await flush();
    expect(mocks.loadMentionRepositories).toHaveBeenCalledTimes(2);
    expect(latestAssistant().rows("Persona")).toEqual([]);
    expect(latestAssistant().rows("billing")).toEqual([
      expect.objectContaining({ kind: "repository", id: "repository:r2" }),
    ]);
  });

  it("degrades to empty lists when default fetches fail", async () => {
    mocks.api.mockRejectedValueOnce(new Error("503"));
    mocks.loadMentionRepositories.mockRejectedValueOnce(new Error("503"));
    await mount();
    await flush();
    expect(latestAssistant().loading).toBe(false);
    expect(latestAssistant().rows("")).toEqual([]);
  });
});
