import { describe, expect, it } from "vitest";
import { chatLines } from "./turns";
import { indexedNavTicks } from "./nav-rail";
import type { Item } from "./types";
import { containsInput, userInputRevision, validUserInputIndex, type UserInputIndex } from "./user-input-index";

const index: UserInputIndex = { total: 36, history: "fixture", inputs: Array.from({ length: 36 }, (_, i) => ({ id: `input-${i}#0`, seq: i * 2 + 1 })) };
const items: Item[] = index.inputs.slice(-2).flatMap((input) => [
  { ...input, role: "user" as const, kind: "text" as const, text: "A synthetic input\n<persona-context kind=\"context\" title=\"Fixture\">Context data</persona-context>" },
  { id: `answer-${input.seq}#0`, seq: input.seq + 1, role: "assistant" as const, kind: "text" as const, text: "Synthetic answer" },
]);

describe("independent user-input positions", () => {
  it("keeps the first and last positions in a 678-input history when no bodies are loaded", () => {
    const long: UserInputIndex = { total: 678, history: "fixture", inputs: Array.from({ length: 678 }, (_, i) => ({ id: `long-${i}#0`, seq: i * 4 + 1 })) };
    const ticks = indexedNavTicks(long, [], []);
    expect(ticks).toHaveLength(678);
    expect(ticks[0]).toMatchObject({ id: "seg:long-0#0", ordinal: 1, loadPreview: true });
    expect(ticks.at(-1)?.ordinal).toBe(678);
  });

  it("draws the server total with only the newest two input bodies loaded", () => {
    const ticks = indexedNavTicks(index, chatLines(items), items);
    expect(ticks).toHaveLength(index.total);
    expect(ticks[0]).toMatchObject({ id: "seg:input-0#0", seq: 1, ordinal: 1, preview: "" });
    expect(ticks[0].lineId).toBeUndefined();
    expect(ticks[35].lineId).toBeDefined();
    expect(ticks[35].preview).toBe("A synthetic input");
  });

  it("uses local query and answer text when available and projects server previews for unloaded turns", () => {
    const rich = {
      ...index,
      inputs: index.inputs.map((position, i) => i === 1 ? { ...position, attachments: 3 } :
        { ...position, preview: "Indexed query\nMore detail", answer: "Indexed answer.", attachments: 2 }),
    };
    expect(validUserInputIndex(rich)).toBe(true);
    const ticks = indexedNavTicks(rich, chatLines(items), items);
    expect(ticks[0]).toMatchObject({ preview: "Indexed query", answerExcerpt: "Indexed answer." });
    expect(ticks[0].attachmentsOnly).toBeUndefined();
    expect(ticks[1]).toMatchObject({ preview: "", attachmentsOnly: 3 });
    expect(ticks[35]).toMatchObject({ preview: "A synthetic input", answerExcerpt: "Synthetic answer" });

    const withoutReply = items.slice(0, -1);
    expect(indexedNavTicks(rich, chatLines(withoutReply), withoutReply)[35])
      .toMatchObject({ preview: "A synthetic input", answerExcerpt: "Indexed answer." });
  });

  it("skips an inline trailer when the preview falls back to a leading quote", () => {
    const raw = '> Review\n\n<!-- persona-inline-v1 [[6,6,"opaque-anchor"';
    const indexed = { total: 1, history: "fixture", inputs: [{ id: "inline#0", seq: 1, preview: raw }] };
    const item: Item = { id: "inline#0", seq: 1, role: "user", kind: "text", text: raw };
    expect(indexedNavTicks(indexed, [], [])[0].preview).toBe("Review");
    expect(indexedNavTicks(indexed, chatLines([item]), [item])[0].preview).toBe("Review");
  });

  it("validates the additive preview fields without requiring them", () => {
    const valid = (fields: Record<string, unknown>) => validUserInputIndex({
      total: 1, history: "fixture", inputs: [{ id: "input", seq: 1, ...fields }],
    });
    expect(valid({})).toBe(true);
    expect(valid({ answer: "Answer only", attachments: 0 })).toBe(true);
    for (const fields of [{ preview: 17 }, { answer: null }, { attachments: -1 }, { attachments: 1.5 }, { attachments: "2" }]) {
      expect(valid(fields)).toBe(false);
    }
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
    imageIndex.inputs[0] = { ...imageIndex.inputs[0], attachments: 3 };
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
