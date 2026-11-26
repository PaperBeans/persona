// @vitest-environment jsdom
// Controlled SessionInfoDialog bypasses default API, clipboard, external-link, and child-section singletons while keeping default fallbacks intact.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../../lib/types";
import type { SessionInfo, SessionInfoDialogProps, SessionInfoLoader } from "./SessionInfoDialog";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  copyText: vi.fn(),
  openLink: vi.fn(),
  AlwaysPermissions: vi.fn(),
  DelegatedHistory: vi.fn(),
}));

vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (...args: unknown[]) => mocks.api(...args),
}));
vi.mock("../../lib/links", async (orig) => ({
  ...(await orig<typeof import("../../lib/links")>()),
  openLink: (...args: unknown[]) => mocks.openLink(...args),
}));
vi.mock("../../ui/atoms", async (orig) => ({
  ...(await orig<typeof import("../../ui/atoms")>()),
  copyText: (...args: unknown[]) => mocks.copyText(...args),
}));
vi.mock("./AlwaysPermissions", () => ({
  AlwaysPermissions: (props: { session: string }) => {
    mocks.AlwaysPermissions(props);
    return <div data-default-always={props.session} />;
  },
}));
vi.mock("./Delegated", () => ({
  DelegatedHistory: (props: { session: Session }) => {
    mocks.DelegatedHistory(props);
    return <div data-default-delegated={props.session.session_id} />;
  },
}));

const { SessionInfoDialog } = await import("./SessionInfoDialog");
const { initI18n, t } = await import("../../i18n");
const { resumeCommand } = await import("../../lib/format");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const agentSession = {
  machine_id: "box-1",
  session_id: "sess-a",
  title: "Fix tests",
  agent: "claude",
  launcher: "claude",
  status: "idle",
  state: "idle",
  cwd: "/workspace/app",
} as Session;

const secondAgentSession = {
  ...agentSession,
  session_id: "sess-b",
  title: "Other session",
} as Session;

const assistantSession = {
  machine_id: "box-1",
  session_id: "assist-1",
  title: "Daily planning",
  assistant: true,
  agent: "claude",
  status: "idle",
  state: "idle",
  external_url: "https://example.com/lark/thread/1",
} as Session;

const sampleInfo: SessionInfo = {
  session_id: "sess-a",
  model: "claude-sonnet-4-5",
  launcher: "claude",
  permission_mode: "plan",
  effort: "high",
  version: "1.2.3",
  context_tokens: 2400,
  output_tokens: 512,
  turns: 4,
  compactions: 1,
  cwd: "/workspace/app",
  git_branch: "main",
  transcript_path: "/workspace/app/.transcript.jsonl",
};

let host: HTMLDivElement;
let root: Root;

const mount = async (props: SessionInfoDialogProps) => {
  await act(async () => root.render(<SessionInfoDialog {...props} />));
};

const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const dialog = () => document.querySelector<HTMLElement>("[role=dialog]");
const button = (label: string) =>
  [...(dialog()?.querySelectorAll<HTMLButtonElement>("button") ?? [])].find((b) => b.textContent?.includes(label));
const hasSpinner = () => Boolean(dialog()?.querySelector('[role="status"]'));
const rowsOf = () => Object.fromEntries(
  [...(dialog()?.querySelectorAll<HTMLElement>("section div > div") ?? [])].map((r) => {
    const [k, v] = r.querySelectorAll("span");
    return [k?.textContent ?? "", v?.textContent ?? ""];
  }),
);
const expectNoDefaultSingletons = () => {
  expect(mocks.api).not.toHaveBeenCalled();
  expect(mocks.copyText).not.toHaveBeenCalled();
  expect(mocks.openLink).not.toHaveBeenCalled();
  expect(mocks.AlwaysPermissions).not.toHaveBeenCalled();
  expect(mocks.DelegatedHistory).not.toHaveBeenCalled();
};

beforeEach(async () => {
  await initI18n("en");
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", (q: string) => ({
    matches: false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("SessionInfoDialog with controlled props", () => {
  it("renders controlled info, custom permissions, and routes copy/external callbacks without default singletons", async () => {
    const onCopy = vi.fn();
    const onOpenExternal = vi.fn();
    const renderPermissions = vi.fn((s: Session) => <div data-custom-permissions={s.session_id} />);
    const loadInfo = vi.fn();

    await mount({
      session: assistantSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
      info: sampleInfo,
      loadInfo,
      onCopy,
      onOpenExternal,
      renderPermissions,
    });

    expect(rowsOf().Model).toBe("claude-sonnet-4-5");
    expect(rowsOf().Machine).toBe("Workstation");
    expect(rowsOf().Context).toBe("2.4K tokens");
    expect(dialog()?.querySelector('[data-custom-permissions="assist-1"]')).not.toBeNull();
    expect(hasSpinner()).toBe(false);
    expect(loadInfo).not.toHaveBeenCalled();
    expect(renderPermissions).toHaveBeenCalledWith(assistantSession);

    act(() => button(t("chat-sessioninfo.copy_session_id"))!.click());
    expect(onCopy).toHaveBeenCalledWith("assist-1", "session_id");

    act(() => button(t("chat-sessioninfo.open_in_lark"))!.click());
    expect(onOpenExternal).toHaveBeenCalledWith("https://example.com/lark/thread/1");

    // Assistant sessions do not show a resume command button.
    expect(button(t("chat-sessioninfo.copy_resume_command"))).toBeUndefined();

    expectNoDefaultSingletons();
  });

  it("renders controlled delegated section, copy resume command, controlled loading, and controlled error states", async () => {
    const onCopy = vi.fn();
    const renderDelegated = vi.fn((s: Session) => <div data-custom-delegated={s.session_id} />);
    const loadInfo = vi.fn();

    await mount({
      session: agentSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
      info: sampleInfo,
      loadInfo,
      onCopy,
      renderDelegated,
    });

    expect(dialog()?.querySelector('[data-custom-delegated="sess-a"]')).not.toBeNull();
    act(() => button(t("chat-sessioninfo.copy_resume_command"))!.click());
    expect(onCopy).toHaveBeenCalledWith(resumeCommand(agentSession), "resume_command");

    // Explicit null info shows loading by default; loading=false hides the spinner.
    await mount({
      session: agentSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
      info: null,
      loadInfo,
      onCopy,
      renderDelegated: () => null,
    });
    expect(hasSpinner()).toBe(true);
    expect(dialog()?.querySelector("[data-custom-delegated]")).toBeNull();

    await mount({
      session: agentSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
      info: null,
      loading: false,
      loadInfo,
      onCopy,
      renderDelegated: () => null,
    });
    expect(hasSpinner()).toBe(false);

    // Controlled error renders error message without loading or fetching.
    await mount({
      session: agentSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
      error: "panel offline",
      loadInfo,
      onCopy,
      renderDelegated: () => null,
    });
    expect(dialog()?.textContent).toContain("panel offline");
    expect(hasSpinner()).toBe(false);
    expect(loadInfo).not.toHaveBeenCalled();

    expectNoDefaultSingletons();
  });

  it("loads via custom loadInfo, keeps inline callbacks stable, aborts and rejects stale ABA promises, and handles sync throws", async () => {
    const requests: Array<{
      session: Session;
      signal: AbortSignal;
      resolve: (value: SessionInfo) => void;
      reject: (reason: unknown) => void;
    }> = [];
    const loadInfo = vi.fn((session: Session, signal: AbortSignal) =>
      new Promise<SessionInfo>((resolve, reject) => {
        requests.push({ session, signal, resolve, reject });
      }),
    );

    const renderDialog = (session: Session, open: boolean, loader: SessionInfoLoader = loadInfo) =>
      mount({
        session,
        machineName: "Workstation",
        open,
        onClose: vi.fn(),
        loadInfo: loader,
        onCopy: vi.fn(),
        onOpenExternal: vi.fn(),
        renderDelegated: () => null,
      });

    // Closed dialog does not fetch.
    await renderDialog(agentSession, false);
    expect(loadInfo).not.toHaveBeenCalled();

    // Open session A -> request #1.
    await renderDialog(agentSession, true);
    expect(requests).toHaveLength(1);
    expect(requests[0].session.session_id).toBe("sess-a");
    expect(hasSpinner()).toBe(true);

    // Re-rendering with a fresh inline loadInfo arrow does not abort or refetch.
    await renderDialog(agentSession, true, (s, sig) => loadInfo(s, sig));
    expect(requests).toHaveLength(1);
    expect(requests[0].signal.aborted).toBe(false);

    // Switch A -> B: aborts request #1 and starts request #2.
    await renderDialog(secondAgentSession, true);
    expect(requests[0].signal.aborted).toBe(true);
    expect(requests).toHaveLength(2);
    expect(requests[1].session.session_id).toBe("sess-b");

    // Switch B -> A (ABA): aborts request #2 and starts request #3.
    await renderDialog(agentSession, true);
    expect(requests[1].signal.aborted).toBe(true);
    expect(requests).toHaveLength(3);
    expect(requests[2].session.session_id).toBe("sess-a");
    expect(hasSpinner()).toBe(true);

    // Stale promise #1 (for session A, ignoring AbortSignal) resolves late: must not overwrite or stop spinner.
    await act(async () => {
      requests[0].resolve({ ...sampleInfo, model: "stale-model-A1" });
      await Promise.resolve();
    });
    await flush();
    expect(dialog()?.textContent).not.toContain("stale-model-A1");
    expect(hasSpinner()).toBe(true);

    // Stale promise #2 (for session B) rejects late: must not render error or stop spinner.
    await act(async () => {
      requests[1].reject(new Error("stale-B-error"));
      await Promise.resolve();
    });
    await flush();
    expect(dialog()?.textContent).not.toContain("stale-B-error");
    expect(hasSpinner()).toBe(true);

    // Fresh promise #3 resolves: renders fresh info and stops spinner.
    await act(async () => {
      requests[2].resolve({ ...sampleInfo, model: "fresh-model-A3" });
      await Promise.resolve();
    });
    await flush();
    expect(rowsOf().Model).toBe("fresh-model-A3");
    expect(hasSpinner()).toBe(false);

    // Close and reopen ABA race: stale in-flight request before close cannot overwrite reopened session.
    await renderDialog(secondAgentSession, true);
    expect(requests).toHaveLength(4);
    await renderDialog(secondAgentSession, false);
    expect(requests[3].signal.aborted).toBe(true);
    await renderDialog(secondAgentSession, true);
    expect(requests).toHaveLength(5);
    await act(async () => {
      requests[3].resolve({ ...sampleInfo, model: "stale-before-close" });
      await Promise.resolve();
    });
    await flush();
    expect(dialog()?.textContent).not.toContain("stale-before-close");
    expect(hasSpinner()).toBe(true);
    await act(async () => {
      requests[4].resolve({ ...sampleInfo, model: "reopened-model" });
      await Promise.resolve();
    });
    await flush();
    expect(rowsOf().Model).toBe("reopened-model");

    // Sync throw from loadInfo renders error cleanly.
    await renderDialog(agentSession, true, () => {
      throw new Error("sync load failure");
    });
    await flush();
    expect(dialog()?.textContent).toContain("sync load failure");
    expect(hasSpinner()).toBe(false);

    expectNoDefaultSingletons();
  });
});

describe("SessionInfoDialog default compatibility", () => {
  it("fetches via api, renders default AlwaysPermissions, and uses global copyText and openLink for assistant sessions", async () => {
    mocks.api.mockResolvedValueOnce(sampleInfo);

    await mount({
      session: assistantSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
    });
    await flush();

    expect(mocks.api).toHaveBeenCalledWith(
      "/sessions/box-1/assist-1/info",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(rowsOf().Model).toBe("claude-sonnet-4-5");
    expect(mocks.AlwaysPermissions).toHaveBeenCalledWith({ session: "assist-1" });
    expect(mocks.DelegatedHistory).not.toHaveBeenCalled();

    act(() => button(t("chat-sessioninfo.copy_session_id"))!.click());
    expect(mocks.copyText).toHaveBeenCalledWith("assist-1");

    act(() => button(t("chat-sessioninfo.open_in_lark"))!.click());
    expect(mocks.openLink).toHaveBeenCalledWith("https://example.com/lark/thread/1");
  });

  it("mounts default DelegatedHistory, copies resume command via copyText, and surfaces API errors for agent sessions", async () => {
    mocks.api.mockRejectedValueOnce(new Error("hub unreachable"));

    await mount({
      session: agentSession,
      machineName: "Workstation",
      open: true,
      onClose: vi.fn(),
    });
    await flush();

    expect(mocks.api).toHaveBeenCalledWith(
      "/sessions/box-1/sess-a/info",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(dialog()?.textContent).toContain("hub unreachable");
    expect(mocks.DelegatedHistory).toHaveBeenCalledWith({ session: agentSession });
    expect(mocks.AlwaysPermissions).not.toHaveBeenCalled();

    act(() => button(t("chat-sessioninfo.copy_resume_command"))!.click());
    expect(mocks.copyText).toHaveBeenCalledWith(resumeCommand(agentSession));
  });
});
