// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});
vi.mock("../../lib/api", async original => ({
  ...await original<typeof import("../../lib/api")>(),
  api: vi.fn(async () => ({ ok: true, close: { closed: true, outcome: "closed" } })),
}));

import { api } from "../../lib/api";
import { appArchiveDeps } from "../../lib/agent-archives";
import { SESSIONS_LOOKUP_FEATURE } from "../../lib/runtime-list";
import { getState, rememberSessionRow, reset } from "../../lib/store";
import { t } from "../../i18n";
import type { Interaction, Session } from "../../lib/types";
import { InteractionCard } from "./Interactions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ix: Interaction = {
  id: "terminal-card", machine_id: "m", session_id: "s", kind: "screen",
  screen: "Waiting for permission confirmation",
  dialog: { options: [{ key: "1", label: "Allow once" }, { key: "2", label: "Deny" }] },
};
let host: HTMLDivElement;
let root: Root;
const button = (key: Parameters<typeof t>[0]) => [...host.querySelectorAll("button")].find(b => b.textContent?.trim() === t(key));
const click = async (key: Parameters<typeof t>[0]) => {
  const target = button(key);
  expect(target, `Missing button: ${key}`).toBeDefined();
  await act(async () => target!.click());
};

beforeEach(() => {
  reset();
  vi.mocked(api).mockClear();
  Object.assign(getState(), { features: [SESSIONS_LOOKUP_FEATURE], interactions: [ix] });
  vi.spyOn(appArchiveDeps, "refresh").mockResolvedValue(undefined);
  vi.spyOn(appArchiveDeps, "notify").mockImplementation(() => {});
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.restoreAllMocks();
});

describe.each(["sessionSummaries", "rowHints"] as const)("terminal close from %s only", source => {
  function render(overrides: Partial<Session> = {}) {
    const session = {
      machine_id: "m", session_id: "s", agent: "claude", status: "blocked", state: "needs_you", waiting: true,
      ...overrides,
    } as Session;
    if (source === "sessionSummaries") getState().sessionSummaries = [session];
    else rememberSessionRow(session);
    expect(getState().sessions).toEqual({});
    expect(getState().sessionSummaries).toEqual(source === "sessionSummaries" ? [session] : []);
    expect(getState().assistant).toBeNull();
    act(() => root.render(<InteractionCard ix={ix} />));
  }

  it("shows Close Agent and sends exactly one force archive after confirmation", async () => {
    render();
    await click("chat-interactions.close_agent");
    expect(api).not.toHaveBeenCalled();
    await click("chat-interactions.close_agent_confirm");
    expect(vi.mocked(api).mock.calls).toEqual([["/sessions/m/s/archive?force=1", { method: "POST", json: {} }]]);
    expect(getState().sessions).toEqual({});
  });

  it("sends nothing when confirmation is cancelled", async () => {
    render();
    await click("chat-interactions.close_agent");
    await click("chat-interactions.close_agent_cancel");
    expect(button("chat-interactions.close_agent")).toBeDefined();
    expect(api).not.toHaveBeenCalled();
  });

  it("does not offer close for a working Agent", () => {
    render({ status: "working", state: "working" });
    expect(button("chat-interactions.close_agent")).toBeUndefined();
    expect(api).not.toHaveBeenCalled();
  });

  it("does not offer close for an assistant conversation", () => {
    render({ assistant: true });
    expect(button("chat-interactions.close_agent")).toBeUndefined();
    expect(api).not.toHaveBeenCalled();
  });

  it("prevents a read-only source from sending a close", async () => {
    render({ source_kind: "runtime", capabilities: { read: true, approve: false, close: false } });
    const close = button("chat-interactions.close_agent");
    expect(!close || close.disabled || !!close.closest("fieldset[disabled]")).toBe(true);
    if (close) await act(async () => close.click());
    expect(api).not.toHaveBeenCalled();
  });
});
