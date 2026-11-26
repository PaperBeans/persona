import { describe, expect, it } from "vitest";
import { LEGACY_SCHEME } from "./compatibility";
import { contextXML, withContexts } from "./context-xml";
import {
  draftText, editorText, readInlineDraft, replaceInlineRange, writeInlineDraft,
} from "./inline-mentions";
import { inlineBody, inlineReferences, readInlineBody } from "./inline-message";
import type { ContextRef } from "./links";
import { skillContext, splitSkillDraft } from "./skill-mentions";
import { parseUserText } from "./turns";

const skill = skillContext({ id: "personal-review", name: "Review", source: "user · ~/.skills", description: "Review & check" });
const projectSkill = skillContext({ id: "project-review", name: "Review", source: "project · ./skills" });
const file: ContextRef = {
  id: "notes", kind: "file", title: "notes & plans.md", url: "", text: "docs/notes & plans.md",
  attrs: { path: "docs/notes & plans.md", machine: "fixture", cwd: "/work", launcher: "codex", runtime: "" },
};
const workspace: ContextRef = {
  id: "design", kind: "workspace", title: "Design", url: "", text: "Design workspace",
  attrs: { machine: "fixture", cwd: "/work/design", branch: "main" },
};
const repository: ContextRef = {
  id: "repo", kind: "repository", title: "Design Repo", url: "https://example.com/design", text: "Repository: Design",
  attrs: { repo_id: "design-repo", path: "example/design", provider: "github" },
};
const session: ContextRef = {
  id: "chat", kind: "context", title: "Review", url: "persona://session/fixture/chat", text: "Session: Review",
};

describe("inline mention documents", () => {
  it("round-trips plain drafts and repeated references without taking whitespace or Unicode out of the draft", () => {
    const doc = ["  介绍 🎨 ", skill, "\n然后 ", file, " ", skill, "\n\n"];
    expect(readInlineDraft(writeInlineDraft(doc))).toEqual(doc);
    expect(writeInlineDraft(["old draft\n"])).toBe("old draft\n");
    expect(readInlineDraft("old draft\n")).toEqual(["old draft\n"]);
    expect(draftText(writeInlineDraft(doc))).toBe("  介绍 🎨 Review\n然后 @docs/notes & plans.md Review\n\n");
  });

  it("uses one caret step per token and replaces text and tokens in the same selection", () => {
    const doc = ["ab", skill, "cdef", file, "gh"];
    expect(editorText(doc)).toBe("ab\uFFFCcdef\uFFFCgh");
    expect(replaceInlineRange(doc, 1, 8, [workspace, " "])).toEqual(["a", workspace, " gh"]);
    expect(replaceInlineRange(doc, 2, 3, [])).toEqual(["abcdef", file, "gh"]);
  });
});

describe("inline mention message bodies", () => {
  it("matches the exact selection tags even with repeated labels, literal text, new prefixes and reordered contexts", () => {
    const doc = ["  Review 介绍 ", skill, "、", projectSkill, " 和 ", session, "，查阅 ", file, "，放在 ", workspace, " 和 ", repository, "。 "];
    const refs = inlineReferences(doc);
    const body = inlineBody(doc);
    const wire = withContexts(refs, body);
    expect(wire.startsWith(refs.map(contextXML).join("\n") + "\n\nReview 介绍 Review、Review 和 Review，查阅 @docs/notes & plans.md，放在 Design 和 Design Repo。")).toBe(true);
    const external = { ...session, url: "https://example.com/review" };
    const restored = readInlineBody(`> Quoted words\n\n${body}`, [external, ...refs.toReversed()]);
    expect(restored.document).toEqual(["> Quoted words\n\nReview 介绍 ", ...doc.slice(1, -1), "。"]);
    expect(restored.text).toBe("> Quoted words\n\nReview 介绍 Review、Review 和 Review，查阅 @docs/notes & plans.md，放在 Design 和 Design Repo。");
    expect(restored.used).toEqual(new Set(refs));
  });

  it("keeps scheduled picker scope and leaves unmatched or legacy prose as text", () => {
    const body = inlineBody(["Check ", file, " with ", skill]);
    const prefix = [file, skill].map(contextXML).join("\n");
    const split = splitSkillDraft(`${prefix}\n\n${body}`, true);
    expect(split.skills.map(contextXML)).toEqual([file, skill].map(contextXML));
    expect(readInlineBody(split.text, [skill]).document).toEqual(["Check @docs/notes & plans.md with ", skill]);
    expect(readInlineBody("Use Review and @docs/notes & plans.md", [skill, file]).used.size).toBe(0);
  });

  it("keeps plain prose when an anchor trailer is malformed or truncated", () => {
    const text = "Use Review.";
    const body = `${text}\n\n<!-- persona-inline-v1 [[invalid]] -->`;
    expect(readInlineBody(body, [skill])).toEqual({ text, document: [text], used: new Set() });
    expect(parseUserText(body.slice(0, -8)).text).toBe(text);
  });

  it("rebinds references after session-link spelling changes and the workspace body is enriched", () => {
    const legacy = { ...session, url: session.url.replace("persona:", `${LEGACY_SCHEME}:`) };
    const enriched = { ...workspace, attrs: { machine: "fixture", cwd: "/work/design" }, text: "Resolved workspace" };
    expect(readInlineBody(inlineBody([workspace, " and ", session]), [legacy, enriched]).document).toEqual([enriched, " and ", legacy]);
  });

  it("keeps live file metadata out of the leading selection envelope and native @path in the body", () => {
    const live = { ...file, attrs: { path: "notes.md", cwd: "/work", machine: "fixture" } };
    const doc = ["Check ", live, " with ", skill];
    const message = `${withContexts([skill], inlineBody(doc))}\n\n${contextXML(live)}`;
    expect(splitSkillDraft(message, true).skills.map(contextXML)).toEqual([contextXML(skill)]);
    expect(message).toContain("Check @notes.md with Review");
    expect(message.endsWith(contextXML(live))).toBe(true);
  });
});
