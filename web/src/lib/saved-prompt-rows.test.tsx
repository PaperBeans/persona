// @vitest-environment jsdom
// The composer's lazy saved-prompt catalog must not start after its last interested consumer has gone (#15422).
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("the saved-prompt lazy lifecycle", () => {
  beforeEach(() => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("does not start after the last interested composer unmounts", async () => {
    const module = await import("./saved-prompts");
    const starts = vi.spyOn(module, "startSavedPrompts");
    const { useSavedPromptRows } = await import("./saved-prompt-rows");
    function Rows() {
      useSavedPromptRows(true);
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(<Rows />));
    await act(async () => root.unmount());
    await act(async () => { await import("./saved-prompts"); });
    expect(starts).not.toHaveBeenCalled();
  });

  it("starts when the chunk arrives with a composer still interested", async () => {
    const module = await import("./saved-prompts");
    const starts = vi.spyOn(module, "startSavedPrompts");
    const { useSavedPromptRows } = await import("./saved-prompt-rows");
    function Rows() {
      useSavedPromptRows(true);
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(<Rows />));
    await act(async () => { await import("./saved-prompts"); });
    expect(starts).toHaveBeenCalledTimes(1);
    await act(async () => root.unmount());
  });
});
