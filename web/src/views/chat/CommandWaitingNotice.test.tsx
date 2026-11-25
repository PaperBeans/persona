// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { Interaction } from "../../lib/types";
import { CommandWaitingNotice, focusPendingCommand, pendingCommandInput } from "./CommandWaitingNotice";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const command: Interaction = { id: "command", machine_id: "host", session_id: "chat", kind: "permission", tool_name: "command_input" };
let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("command waiting in the composer", () => {
  it("selects the unexpired command on this machine and conversation", () => {
    const expired = { ...command, expires_at: "2026-01-01T12:00:00Z" };
    expect(pendingCommandInput([
      { ...command, machine_id: "other" },
      { ...command, session_id: "other" },
      { ...command, tool_name: "Bash" },
      expired,
    ], "host", "chat", Date.parse("2026-01-01T12:00:00Z"))).toBeUndefined();
    expect(pendingCommandInput([expired], "host", "chat", Date.parse("2026-01-01T11:59:59Z"))).toBe(expired);
    expect(pendingCommandInput([command], "host", "chat", 0)).toBe(command);
  });

  it("routes Answer to the existing card and only shows waiting messages after a reported hold", () => {
    const onAnswer = vi.fn();
    const dock = { current: host };
    const show = (interactions: Interaction[], queueHeld = false) => act(() => root.render(
      <CommandWaitingNotice interactions={interactions} mid="host" sid="chat" queueHeld={queueHeld}
        onAnswer={onAnswer} dock={dock} keyboard={false} onFocused={vi.fn()} />,
    ));
    show([command]);
    expect(host.textContent).toContain(t("chat-command-waiting.prompt"));
    expect(host.textContent).not.toContain(t("chat-command-waiting.queue"));
    expect(host.querySelector("input")).toBeNull();
    act(() => host.querySelector("button")!.click());
    expect(onAnswer).toHaveBeenCalledExactlyOnceWith("command");
    show([], true);
    expect(host.textContent).toContain(t("chat-command-waiting.queue"));
    expect(host.querySelector("button")).toBeNull();
    show([]);
    expect(host.textContent).toBe("");
  });

  it("scrolls and focuses only the matching pending request", () => {
    const dock = document.createElement("div");
    dock.innerHTML = '<div data-card="other"><section data-pending-card tabindex="-1"></section></div>' +
      '<div data-card="command"><section data-pending-card tabindex="-1"></section></div>';
    document.body.append(dock);
    try {
      const card = dock.querySelector<HTMLElement>('[data-card="command"] [data-pending-card]')!;
      card.scrollIntoView = vi.fn();
      expect(focusPendingCommand(dock, "absent")).toBe(false);
      expect(focusPendingCommand(dock, "command")).toBe(true);
      expect(card.scrollIntoView).toHaveBeenCalledExactlyOnceWith({ block: "nearest" });
      expect(document.activeElement).toBe(card);
      card.hidden = true;
      expect(focusPendingCommand(dock, "command")).toBe(false);
    } finally {
      dock.remove();
    }
  });
});
