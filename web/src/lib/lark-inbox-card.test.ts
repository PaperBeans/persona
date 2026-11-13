import { describe, expect, it } from "vitest";
import { larkInboxCard, larkMessagePreview } from "./lark-inbox-card";

const knownCard = `<card title="Example update">
**Merge Request**
[Example change](https://example.com/merge-requests/1)
**Author**
Example Author
**Repository**
[Example repository](https://example.com/example/repository)
**Description**
Example description.
</card>`;

describe("larkInboxCard", () => {
  it("extracts the four known Markdown fields from one complete card", () => {
    expect(larkInboxCard(knownCard)).toEqual({
      title: "Example update",
      body: "**Merge Request**\n[Example change](https://example.com/merge-requests/1)\n**Author**\nExample Author\n**Repository**\n[Example repository](https://example.com/example/repository)\n**Description**\nExample description.",
      request: { title: "Example change", url: "https://example.com/merge-requests/1" },
      repository: { title: "Example repository", url: "https://example.com/example/repository" },
      author: "Example Author",
      description: "Example description.",
    });
  });

  it("preserves the original body for an untitled or unknown card", () => {
    const body = "An unfamiliar card body.\nKeep this layout as supplied.";
    expect(larkInboxCard(`<card>${body}</card>`)).toEqual({ title: "", body });
    expect(larkInboxCard(`<card title="Example notice">\n${body}\n</card>`)).toEqual({
      title: "Example notice",
      body,
    });
  });

  it("does not parse incomplete envelopes or adjacent cards as one message", () => {
    expect(larkInboxCard('<card title="Example">body')).toBeUndefined();
    expect(larkInboxCard('<card title="Example">body</card> trailing text')).toBeUndefined();
    expect(larkInboxCard('<card title="Example">one</card><card title="Example">two</card>')).toBeUndefined();
    expect(larkInboxCard('<card title="Example"><card>nested</card></card>')).toBeUndefined();
  });

  it("falls back to the unchanged body when known fields are malformed or repeated", () => {
    const cases = [
      `[Example change](javascript:alert(1))`,
      `[Example change](https://user:pass@example.com/change)`,
      `[Example change](ftp://example.com/change)`,
    ];
    for (const request of cases) {
      const body = `**Merge Request**\n${request}\n**Author**\nExample Author\n**Repository**\n[Example repository](https://example.com/repository)\n**Description**\nExample description.`;
      expect(larkInboxCard(`<card title="Example">${body}</card>`)).toEqual({ title: "Example", body });
    }

    const repeated = "**Merge Request**\n[Example change](https://example.com/change)\n**Author**\nExample Author\n**Author**\nExample Author\n**Repository**\n[Example repository](https://example.com/repository)\n**Description**\nExample description.";
    expect(larkInboxCard(`<card title="Example">${repeated}</card>`)).toEqual({ title: "Example", body: repeated });
  });

  it("preserves multiline descriptions and previews the request title", () => {
    const body = "**Merge Request**\n[Example change](https://example.com/change)\n**Author**\nExample Author\n**Repository**\n[Example repository](https://example.com/repository)\n**Description**\nFirst description line.\nSecond description line.";
    const input = `<card title="Example update">\n${body}\n</card>`;
    expect(larkInboxCard(input)?.description).toBe("First description line.\nSecond description line.");
    expect(larkMessagePreview(input)).toBe("Example change");
  });

  it("previews the card title when there is no extracted request", () => {
    expect(larkMessagePreview('<card title="Example notice">A body</card>')).toBe("Example notice");
  });
});
