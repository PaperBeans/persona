import { describe, expect, it } from "vitest";
import { chatLines } from "./turns";
import type { Item } from "./types";
import { containsInput, indexedNavTicks, userInputRevision, validUserInputIndex, windowUserInputs, type UserInputIndex } from "./user-input-index";
import { navRailMark } from "./nav-rail";

const index: UserInputIndex = { total: 36, history: "fixture", inputs: Array.from({ length: 36 }, (_, i) => ({ id: `input-${i}#0`, seq: i * 2 + 1 })) };
const items: Item[] = index.inputs.slice(-2).flatMap((input) => [
  { ...input, role: "user" as const, kind: "text" as const, text: "A synthetic input\n<persona-context kind=\"context\" title=\"Fixture\">Context data</persona-context>" },
  { id: `answer-${input.seq}#0`, seq: input.seq + 1, role: "assistant" as const, kind: "text" as const, text: "Synthetic answer" },
]);

describe("progressive user-input positions", () => {
  it("starts a 678-input history at its latest 50 positions with global ordinals", () => {
    const long: UserInputIndex = { total: 678, history: "fixture", inputs: Array.from({ length: 678 }, (_, i) => ({ id: `long-${i}#0`, seq: i * 4 + 1 })) };
    const window = windowUserInputs(long);
    const ticks = indexedNavTicks(window, [], []);
    expect(ticks).toHaveLength(50);
    expect(ticks[0]).toMatchObject({ id: "seg:long-628#0", ordinal: 629 });
    expect(ticks.at(-1)?.ordinal).toBe(678);
    expect(window.total).toBe(678);
    expect(long.inputs).toHaveLength(678);
    const earlier = windowUserInputs(long, long.inputs[578].seq);
    expect(indexedNavTicks(earlier, [], [])).toHaveLength(100);
    expect(indexedNavTicks(earlier, [], [])[0].ordinal).toBe(579);
    expect(windowUserInputs(long, 1).inputs).toHaveLength(678);
  });

  it("keeps all inputs in short histories and does not grow for non-input pages within the newest 50", () => {
    for (const total of [0, 1, 2, 36, 49, 50]) {
      const short = { ...index, total, inputs: Array.from({ length: total }, (_, i) => ({ id: `short-${i}`, seq: i + 1 })) };
      expect(windowUserInputs(short).inputs).toHaveLength(total);
    }
    const long = { ...index, total: 300, inputs: Array.from({ length: 300 }, (_, i) => ({ id: `long-${i}`, seq: i * 10 + 1 })) };
    expect(windowUserInputs(long, 2999).inputs).toHaveLength(50);
    expect(windowUserInputs(long, 2800).inputs).toHaveLength(50);
    expect(windowUserInputs(long, 2500).inputs).toHaveLength(50);
    expect(windowUserInputs(long, 2499).inputs).toHaveLength(50);
    expect(windowUserInputs(long, 2491).inputs).toHaveLength(51);
  });

  it("draws the server total with only the newest two input bodies loaded", () => {
    const ticks = indexedNavTicks(index, chatLines(items), items);
    expect(ticks).toHaveLength(index.total);
    expect(ticks[0]).toMatchObject({ id: "seg:input-0#0", seq: 1, ordinal: 1, preview: "" });
    expect(ticks[0].lineId).toBeUndefined();
    expect(ticks[35].lineId).toBeDefined();
    expect(ticks[35].preview).toBe("A synthetic input");
    expect(ticks.every((_, i) => navRailMark(i, ticks.length, 35, null).width === 6)).toBe(true);
  });
  it("refreshes from raw queued entries without rescanning for tool frames", () => {
    const queued: Item = { id: "queued#0", seq: 80, role: "user", kind: "text", queued: true, text: "Another synthetic input" };
    expect(userInputRevision(items)).toBe("input-35#0");
    expect(userInputRevision([...items, queued])).toBe(queued.id);
    expect(userInputRevision([...items, queued, { id: "tool-image", seq: 81, role: "user", kind: "image", tool_use_id: "tool" }])).toBe(queued.id);
  });

  it("preserves the loaded attachment-only preview while the server sends positions only", () => {
    const image: Item = { id: "picture#0", seq: 1, role: "user", kind: "image", image: { mime: "image/png", lazy: true } };
    const imageIndex: UserInputIndex = { total: 1, history: "fixture", inputs: [{ id: image.id, seq: 1 }] };
    expect(indexedNavTicks(imageIndex, chatLines([image]), [image])[0]).toMatchObject({ preview: "", attachmentsOnly: 1 });
  });

  it("uses stable identities rather than text and recognizes unloaded targets", () => {
    expect(containsInput(items, index.inputs[0])).toBe(false);
    expect(containsInput(items, index.inputs[35])).toBe(true);
    expect(containsInput([{ ...items[0], id: "input-34#1" }], index.inputs[34])).toBe(true);
  });
  it("never accepts a partial count, duplicate identities or unordered sequences", () => {
    expect(validUserInputIndex(index)).toBe(true);
    expect(validUserInputIndex({ ...index, inputs: index.inputs.slice(-2) })).toBe(false);
    expect(validUserInputIndex({ ...index, total: -1 })).toBe(false);
    expect(validUserInputIndex({ ...index, inputs: index.inputs.map(() => index.inputs[0]) })).toBe(false);
    expect(validUserInputIndex({ ...index, inputs: [...index.inputs].reverse() })).toBe(false);
    expect(validUserInputIndex({ total: 1, inputs: [null], history: "bad" })).toBe(false);
    expect(validUserInputIndex({ total: 0, inputs: [], history: "empty" })).toBe(true);
  });
});
