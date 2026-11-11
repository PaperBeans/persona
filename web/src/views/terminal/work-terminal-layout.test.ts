import { describe, expect, it } from "vitest";
import { buildTerminalTree, type PaneHostDTO } from "../../lib/terminals";
import { interactiveSessionPane, standaloneShells } from "./work-terminal-layout";

const host: PaneHostDTO = {
  kind: "herdr", stream: true, control: true, new_tab: true,
  workspaces: [{ id: "mixed", label: "Mixed" }, { id: "empty", label: "Empty" }],
  tabs: [{ id: "tab", workspace_id: "mixed", label: "Two panes" }],
  panes: [
    { pane: "w1:p1", terminal: "pane:w1:p1", tab_id: "tab", workspace_id: "mixed", title: "Agent", session_id: "session", agent: "codex" },
    { pane: "w1:p2", terminal: "pane:w1:p2", tab_id: "tab", workspace_id: "mixed", title: "Shell" },
  ],
};

describe("work terminal layout", () => {
  it("requires an interactive host and a real session ID, never treating a pane ID as a session ID", () => {
    const data = { hosts: [host] };
    expect(interactiveSessionPane({ data }, "session")).toBe(true);
    expect(interactiveSessionPane({ data, error: "403 Forbidden" }, "session")).toBe(false);
    expect(interactiveSessionPane({ data }, "pane:w1:p1")).toBe(false);
    expect(interactiveSessionPane({ data: { hosts: [{ ...host, control: false }] } }, "session")).toBe(false);
    expect(interactiveSessionPane(undefined, "session")).toBe(false);
  });

  it("keeps only plain shell rows and preserves their original tab and empty launch targets", () => {
    const result = standaloneShells(buildTerminalTree({ hosts: [host] }, []));
    const mixed = result.find((space) => space.id === "mixed")!;
    expect(mixed.tabs[0].panes.map((p) => p.id)).toEqual(["pane:w1:p2"]);
    expect(mixed.tabs[0].source.panes).toHaveLength(2);
    expect(result.find((space) => space.id === "empty")).toMatchObject({ canOpen: true, tabs: [] });
  });
});
