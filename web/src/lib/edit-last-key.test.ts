import { describe, expect, it } from "vitest";
import { canStartEdit, escapeCancelsEdit, escapeCountsForEdit, upStartsEdit, type EditKey, type EditLastBox } from "./edit-last-key";

const key = (k: string, extra: Partial<EditKey> = {}): EditKey => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...extra });
// an idle Claude session whose last prompt can be edited, an empty box, nothing open
const ready: EditLastBox = { offered: true, busy: false, holds: false, menu: false };

describe("↑ edits the last message (#4503)", () => {
  it("starts an edit from an empty box where Edit last message is on offer", () => {
    expect(upStartsEdit(key("ArrowUp"), ready)).toBe(true);
    expect(canStartEdit(ready)).toBe(true);
  });

  it.each([
    ["not on offer (Persona conversations, Codex, a prompt with pictures)", { offered: false }],
    ["sending held (working, waiting on a card, disabled, blocked, a rewind running)", { busy: true }],
    ["text, quotes, pictures, contexts or an upload in the box", { holds: true }],
    ["a / or @ menu, the Ctrl+R search, a dialog or menu open", { menu: true }],
  ] as const)("leaves ↑ alone when %s", (_, change) => {
    const box = { ...ready, ...change };
    expect(canStartEdit(box)).toBe(false);
    expect(upStartsEdit(key("ArrowUp"), box)).toBe(false);
    expect(escapeCountsForEdit(key("Escape"), box)).toBe(false);
  });

  it.each([
    ["Shift", { shiftKey: true }],
    ["Ctrl", { ctrlKey: true }],
    ["Alt", { altKey: true }],
    ["⌘", { metaKey: true }],
    ["a held-down repeat", { repeat: true }],
    ["an input method composing", { isComposing: true }],
    ["an input method's keyCode 229", { keyCode: 229 }],
  ] as const)("does not take ↑ with %s", (_, extra) => {
    expect(upStartsEdit(key("ArrowUp", extra), ready)).toBe(false);
    expect(escapeCountsForEdit(key("Escape", extra), ready)).toBe(false);
  });

  it("takes only ↑ for the edit, and only Esc toward Esc Esc", () => {
    for (const k of ["ArrowDown", "ArrowLeft", "Enter", "Escape", "Tab", "k"]) expect(upStartsEdit(key(k), ready)).toBe(false);
    for (const k of ["ArrowUp", "ArrowDown", "Enter", "Backspace"]) expect(escapeCountsForEdit(key(k), ready)).toBe(false);
    expect(escapeCountsForEdit(key("Escape"), ready)).toBe(true);
  });
});

describe("Esc while editing", () => {
  it("cancels the edit: a plain, fresh Esc, only while editing", () => {
    expect(escapeCancelsEdit(key("Escape"), true)).toBe(true);
    expect(escapeCancelsEdit(key("Escape"), false)).toBe(false);
    expect(escapeCancelsEdit(key("ArrowUp"), true)).toBe(false);
    for (const extra of [{ shiftKey: true }, { ctrlKey: true }, { altKey: true }, { metaKey: true }, { repeat: true }, { isComposing: true }, { keyCode: 229 }]) {
      expect(escapeCancelsEdit(key("Escape", extra), true)).toBe(false);
    }
  });
});
