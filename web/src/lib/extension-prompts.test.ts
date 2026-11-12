import { describe, expect, it } from "vitest";
import { promptCommands } from "./extension-prompts";

describe("declarative composer prompts", () => {
  it("keeps provider identity separate from the visible title and preserves prompt text", () => {
    const prompts = [{ extension: "example-kit", extension_name: "入门套件", id: "today", title: "今天做了什么", text: "整理今天的进展。\n不要直接发送。" }];
    // the row names the extension; the description stays empty, the palette's card shows what it types
    expect(promptCommands(prompts)).toEqual([{ key: "extension:example-kit/today", word: "/今天做了什么", desc: "", text: prompts[0].text, source: "入门套件" }]);
    expect(promptCommands([...prompts, { ...prompts[0], extension: "another" }]).map((c) => c.key)).toEqual(["extension:example-kit/today", "extension:another/today"]);
  });
  it("does not alter the command menu without enabled extension prompts", () => {
    expect(promptCommands([])).toEqual([]);
  });
});
