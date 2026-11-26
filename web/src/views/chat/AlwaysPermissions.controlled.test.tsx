// @vitest-environment jsdom
// Controlled shells supply grants or async handlers without touching the default hub API or sessionStorage; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AlwaysPermissionGrant, AlwaysPermissionsProps } from "./AlwaysPermissions";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api: (...args: unknown[]) => mocks.api(...args),
  enc: encodeURIComponent,
  errorText: (error: unknown) => (error instanceof Error ? error.message : String(error)),
}));

const { AlwaysPermissions } = await import("./AlwaysPermissions");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SAMPLE_GRANTS: AlwaysPermissionGrant[] = [
  { key_hash: "a", tool_name: "Bash", created_at: "2026-11-06T01:00:00Z" },
  { key_hash: "b", tool_name: "", created_at: "2026-11-06T02:00:00Z" },
];

let host: HTMLDivElement;
let root: Root;
let getItemSpy: ReturnType<typeof vi.spyOn>;
let setItemSpy: ReturnType<typeof vi.spyOn>;

const mount = async (props: Partial<AlwaysPermissionsProps> = {}) => {
  await act(async () => {
    root.render(<AlwaysPermissions session="chat/one" {...props} />);
  });
};

const buttons = () => [...host.querySelectorAll<HTMLButtonElement>("button")];
const flush = async () => {
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

beforeEach(() => {
  sessionStorage.clear();
  vi.clearAllMocks();
  getItemSpy = vi.spyOn(Storage.prototype, "getItem");
  setItemSpy = vi.spyOn(Storage.prototype, "setItem");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  getItemSpy.mockRestore();
  setItemSpy.mockRestore();
});

describe("AlwaysPermissions with controlled props", () => {
  it("renders controlled loading, error/retry, empty, and grant rows without touching api or sessionStorage", async () => {
    const onRetry = vi.fn();
    const onRevoke = vi.fn().mockResolvedValue({ restart_required: true });
    const onRestartRequired = vi.fn();

    await mount({ grants: null, error: "", restartRequired: false, onRevoke, onRetry, onRestartRequired });
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttons()).toHaveLength(0);

    await mount({ grants: null, error: "Panel offline", restartRequired: false, onRevoke, onRestartRequired });
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Panel offline");
    expect(buttons()).toHaveLength(0);

    await mount({ grants: null, error: "Panel offline", restartRequired: false, onRevoke, onRetry, onRestartRequired });
    expect(buttons()).toHaveLength(1);
    expect(buttons()[0].textContent?.trim()).toBe(t("chat-sessioninfo.always_retry"));
    act(() => buttons()[0].click());
    expect(onRetry).toHaveBeenCalledTimes(1);

    await mount({ grants: [], error: "", restartRequired: false, onRevoke, onRestartRequired });
    expect(host.textContent).toContain(t("chat-sessioninfo.always_empty"));

    await mount({
      grants: SAMPLE_GRANTS,
      error: "",
      pendingKey: "a",
      restartRequired: true,
      onRevoke,
      onRestartRequired,
    });
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t("chat-sessioninfo.always_restart"));
    expect(host.textContent).toContain("Bash");
    expect(host.textContent).toContain(t("chat-sessioninfo.always_unknown"));
    expect(buttons().every((button) => button.disabled)).toBe(true);

    await mount({
      grants: SAMPLE_GRANTS,
      error: "",
      pendingKey: "",
      restartRequired: false,
      onRevoke,
      onRestartRequired,
    });
    await act(async () => buttons()[0].click());
    await flush();
    expect(onRevoke).toHaveBeenCalledWith("a", "chat/one");
    expect(onRestartRequired).toHaveBeenCalledWith("chat/one");
    // Controlled grants remain owned by the parent.
    expect(host.textContent).toContain("Bash");
    expect(host.querySelector('[role="status"]')).toBeNull();
    expect(mocks.api).not.toHaveBeenCalled();
    expect(getItemSpy).not.toHaveBeenCalled();
    expect(setItemSpy).not.toHaveBeenCalled();
  });

  it("runs controlled async loadGrants, onRevoke, retry, and onRestartRequired without touching api or sessionStorage", async () => {
    let finishRevoke!: (value: { restart_required?: boolean }) => void;
    const loadGrants = vi.fn().mockResolvedValue(SAMPLE_GRANTS);
    const onRevoke = vi.fn(() => new Promise<{ restart_required?: boolean }>((resolve) => { finishRevoke = resolve; }));
    const onRestartRequired = vi.fn();

    await mount({
      loadGrants: (sid) => loadGrants(sid),
      onRevoke,
      onRestartRequired,
    });
    await flush();
    expect(loadGrants).toHaveBeenCalledTimes(1);
    expect(loadGrants).toHaveBeenCalledWith("chat/one");
    expect(host.textContent).toContain("Bash");

    // Re-rendering with a fresh inline loader closure does not trigger a duplicate fetch.
    await mount({
      loadGrants: (sid) => loadGrants(sid),
      onRevoke,
      onRestartRequired,
    });
    await flush();
    expect(loadGrants).toHaveBeenCalledTimes(1);

    await act(async () => buttons()[0].click());
    expect(onRevoke).toHaveBeenCalledWith("a", "chat/one");
    expect(buttons().every((button) => button.disabled)).toBe(true);
    act(() => buttons()[1].click());
    expect(onRevoke).toHaveBeenCalledTimes(1);

    await act(async () => { finishRevoke({ restart_required: true }); });
    await flush();
    expect(host.textContent).not.toContain("Bash");
    expect(host.textContent).toContain(t("chat-sessioninfo.always_unknown"));
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t("chat-sessioninfo.always_restart"));
    expect(onRestartRequired).toHaveBeenCalledWith("chat/one");

    onRevoke.mockRejectedValueOnce(new Error("Revoke denied"));
    await act(async () => buttons()[0].click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Revoke denied");
    expect(host.textContent).toContain(t("chat-sessioninfo.always_unknown"));
    expect(buttons()[0].disabled).toBe(false);

    expect(mocks.api).not.toHaveBeenCalled();
    expect(getItemSpy).not.toHaveBeenCalled();
    expect(setItemSpy).not.toHaveBeenCalled();
  });

  it("handles loadGrants failures (async and sync) with retry and drops stale loads on session switch", async () => {
    let resolveFirst!: (rows: AlwaysPermissionGrant[]) => void;
    const loadGrants = vi.fn((sid: string) => {
      if (sid === "chat/first") {
        return new Promise<AlwaysPermissionGrant[]>((resolve) => { resolveFirst = resolve; });
      }
      if (sid === "chat/sync-fail") {
        throw new Error("Sync load failed");
      }
      return Promise.resolve([{ key_hash: "s2", tool_name: "Write", created_at: "2026-11-06T03:00:00Z" }]);
    });

    await mount({ session: "chat/first", loadGrants, onRestartRequired: vi.fn() });
    await mount({ session: "chat/second", loadGrants, onRestartRequired: vi.fn() });
    await flush();
    await act(async () => { resolveFirst(SAMPLE_GRANTS); });
    await flush();
    expect(host.textContent).toContain("Write");
    expect(host.textContent).not.toContain("Bash");

    await mount({ session: "chat/sync-fail", loadGrants, onRestartRequired: vi.fn() });
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Sync load failed");
    loadGrants.mockResolvedValueOnce([]);
    await act(async () => buttons()[0].click());
    await flush();
    expect(host.textContent).toContain(t("chat-sessioninfo.always_empty"));
    expect(mocks.api).not.toHaveBeenCalled();
  });

  it("isolates in-flight revoke pending, grants, restart banner, and errors across session switches with matching key_hash", async () => {
    const sharedGrant = (tool: string): AlwaysPermissionGrant[] => [
      { key_hash: "shared-key", tool_name: tool, created_at: "2026-11-06T01:00:00Z" },
    ];
    const pendingRevokes = new Map<string, {
      resolve: (value: { restart_required?: boolean }) => void;
      reject: (reason: Error) => void;
    }>();
    const loadGrants = vi.fn(async (sid: string) => {
      if (sid === "chat/s1") return sharedGrant("Bash");
      if (sid === "chat/s2") return sharedGrant("Bash");
      return sharedGrant("Bash");
    });
    const onRevoke = vi.fn((_key: string, sid: string) => new Promise<{ restart_required?: boolean }>((resolve, reject) => {
      pendingRevokes.set(sid, { resolve, reject });
    }));
    const onRestartRequired = vi.fn();

    await mount({ session: "chat/s1", loadGrants, onRevoke, onRestartRequired });
    await flush();
    await act(async () => buttons()[0].click());
    expect(onRevoke).toHaveBeenCalledWith("shared-key", "chat/s1");
    expect(buttons()[0].disabled).toBe(true);

    // Switching to s2 while s1's revoke is still in flight unlocks s2's buttons.
    await mount({ session: "chat/s2", loadGrants, onRevoke, onRestartRequired });
    await flush();
    expect(host.textContent).toContain("Bash");
    expect(buttons()[0].disabled).toBe(false);

    // Resolving s1's revoke after switching to s2 must not delete s2's matching key_hash
    // or show s1's restart banner on s2, while still notifying onRestartRequired for s1.
    await act(async () => { pendingRevokes.get("chat/s1")!.resolve({ restart_required: true }); });
    await flush();
    expect(host.textContent).toContain("Bash");
    expect(host.querySelector('[role="status"]')).toBeNull();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(onRestartRequired).toHaveBeenCalledWith("chat/s1");

    // Start revoking on s2, switch to s3 and start revoking on s3; rejecting s2 must neither
    // show an error on s3 nor clear s3's active pending lock.
    await act(async () => buttons()[0].click());
    expect(onRevoke).toHaveBeenLastCalledWith("shared-key", "chat/s2");
    await mount({ session: "chat/s3", loadGrants, onRevoke, onRestartRequired });
    await flush();
    expect(buttons()[0].disabled).toBe(false);
    await act(async () => buttons()[0].click());
    expect(onRevoke).toHaveBeenLastCalledWith("shared-key", "chat/s3");
    expect(buttons()[0].disabled).toBe(true);

    await act(async () => { pendingRevokes.get("chat/s2")!.reject(new Error("s2 revoke failed")); });
    await flush();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttons()[0].disabled).toBe(true);

    await act(async () => { pendingRevokes.get("chat/s3")!.resolve({ restart_required: false }); });
    await flush();
    expect(host.textContent).toContain(t("chat-sessioninfo.always_empty"));
    expect(mocks.api).not.toHaveBeenCalled();
    expect(getItemSpy).not.toHaveBeenCalled();
    expect(setItemSpy).not.toHaveBeenCalled();
  });
});

describe("AlwaysPermissions default compatibility", () => {
  it("uses the default hub API and sessionStorage persistence when optional props are omitted", async () => {
    mocks.api.mockResolvedValueOnce({ grants: SAMPLE_GRANTS });
    await mount();
    await flush();
    expect(mocks.api).toHaveBeenCalledWith("/assistant/conversations/chat%2Fone/always");
    expect(getItemSpy).toHaveBeenCalledWith("persona.always-restart:chat/one");

    mocks.api.mockResolvedValueOnce({ restart_required: true });
    await act(async () => buttons()[0].click());
    await flush();
    expect(mocks.api).toHaveBeenLastCalledWith("/assistant/conversations/chat%2Fone/always/a", { method: "DELETE" });
    expect(host.textContent).not.toContain("Bash");
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t("chat-sessioninfo.always_restart"));
    expect(setItemSpy).toHaveBeenCalledWith("persona.always-restart:chat/one", "1");

    act(() => root.unmount());
    root = createRoot(host);
    mocks.api.mockRejectedValueOnce(new Error("Hub offline"));
    await mount();
    await flush();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t("chat-sessioninfo.always_restart"));
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Hub offline");

    mocks.api.mockResolvedValueOnce({ grants: [] });
    await act(async () => buttons()[0].click());
    await flush();
    expect(host.textContent).toContain(t("chat-sessioninfo.always_empty"));
  });

  it("persists sessionStorage restart warning for the original session if default revoke finishes after switching sessions", async () => {
    const shared = [{ key_hash: "shared-key", tool_name: "Bash", created_at: "2026-11-06T01:00:00Z" }];
    let finishDelete!: (value: { restart_required: boolean }) => void;
    mocks.api.mockImplementation((_path: string, init?: { method?: string }) => {
      if (init?.method === "DELETE") {
        return new Promise<{ restart_required: boolean }>((resolve) => { finishDelete = resolve; });
      }
      return Promise.resolve({ grants: shared });
    });

    await mount({ session: "chat/s1" });
    await flush();
    await act(async () => buttons()[0].click());
    await mount({ session: "chat/s2" });
    await flush();
    expect(buttons()[0].disabled).toBe(false);

    await act(async () => { finishDelete({ restart_required: true }); });
    await flush();
    expect(host.textContent).toContain("Bash");
    expect(host.querySelector('[role="status"]')).toBeNull();
    expect(setItemSpy).toHaveBeenCalledWith("persona.always-restart:chat/s1", "1");
    expect(sessionStorage.getItem("persona.always-restart:chat/s1")).toBe("1");
    expect(sessionStorage.getItem("persona.always-restart:chat/s2")).toBeNull();
  });
});
