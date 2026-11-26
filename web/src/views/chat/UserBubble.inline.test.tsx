// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { contextXML, withContexts } from "../../lib/context-xml";
import { inlineBody } from "../../lib/inline-message";
import type { ContextRef } from "../../lib/links";
import { segments } from "../../lib/turns";
import { ChatActionsContext, type ChatActions } from "./chat-actions";
import { UserBubble } from "./SegmentView";

const skill: ContextRef = { id: "skill", kind: "skill", title: "Review", url: "", text: "Project · ./skills\nCheck the change for edge cases.", attrs: { ref: "project-review" } };
const file: ContextRef = { id: "file", kind: "file", title: "notes.md", url: "", text: "docs/notes.md", attrs: { path: "docs/notes.md", machine: "fixture", cwd: "/work" } };
const folder: ContextRef = { ...file, id: "folder", title: "Guides", text: "docs/guides/", attrs: { ...file.attrs, path: "docs/guides/" } };
const workspace: ContextRef = { id: "space", kind: "workspace", title: "Design", url: "", text: "Folder: /work/design\nBranch: main", attrs: { machine: "fixture", cwd: "/work/design" } };
const repository: ContextRef = { id: "repo", kind: "repository", title: "Handbook", url: "https://example.com/handbook", text: "Repository: Example Handbook", attrs: { repo_id: "handbook" } };
const session: ContextRef = { id: "chat", kind: "context", title: "Release planning", url: "persona://session/fixture/chat", text: "Review the release checklist." };
const selection: ContextRef = { id: "selection", kind: "context", title: "Selected passage", url: "https://example.com/guide", text: "First selected line\nSecond selected line" };
const chat: ChatActions = { draftKey: "fixture/history", canCompose: true, speaker: "Agent", title: "Review plan", images: () => [] };

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.body.appendChild(document.createElement("div"));
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

const tokens = () => [...host.querySelectorAll<HTMLButtonElement>("[data-bubble-text] button[aria-expanded]")];
const topGroup = () => host.querySelector<HTMLButtonElement>("[data-user-bubble] > div > button[aria-expanded]");
const click = (button: HTMLButtonElement) => act(async () => { button.click(); });
async function mount(raw: string, count = 0) {
  const [{ user }] = segments([{ id: "u1#0", seq: 1, role: "user", kind: "text", text: raw }]);
  await act(async () => root.render(
    <ChatActionsContext.Provider value={chat}>
      <UserBubble msg={user!} mid="fixture" sid="history" />
    </ChatActionsContext.Provider>,
  ));
  await act(async () => { await vi.dynamicImportSettled(); });
  await vi.waitFor(() => expect(tokens()).toHaveLength(count));
  expect(host.textContent).not.toContain("persona-inline-v1");
}

describe("inline references in sent bubbles", () => {
  it("puts native disclosure buttons in the prose and reuses the details and links for every reference kind", async () => {
    const refs = [skill, file, folder, workspace, repository, session];
    await mount(`${withContexts([skill, workspace, repository, session], inlineBody(["Compare ", ...refs.flatMap((ref) => [ref, " "]), "please."]))}\n\n${[file, folder].map(contextXML).join("\n")}`, refs.length);
    expect(host.querySelector("[data-bubble-text]")?.textContent).toBe("Compare Review notes.md Guides Design Handbook Release planning please.");
    expect(topGroup()).toBeNull();
    const buttons = tokens();
    for (const [i, ref] of refs.entries()) {
      const button = buttons[i];
      expect(button.type).toBe("button");
      expect(button.tabIndex).toBe(0);
      expect(button.querySelector("svg")?.getAttribute("width")).toBe("14");
      expect(button.getAttribute("aria-expanded")).toBe("false");
      const id = button.getAttribute("aria-controls");
      expect(id).toBeTruthy();
      await click(button);
      const detail = document.getElementById(id!);
      expect(detail?.textContent).toContain(ref.text);
      if (ref.url) expect(detail?.querySelector("a")?.getAttribute("href")).toBeTruthy();
      expect(button.getAttribute("aria-expanded")).toBe("true");
      if (i > 0) {
        expect(buttons[i - 1].getAttribute("aria-expanded")).toBe("false");
        expect(host.textContent).not.toContain(refs[i - 1].text);
      }
    }
    await click(buttons.at(-1)!);
    expect(buttons.at(-1)?.getAttribute("aria-expanded")).toBe("false");
    expect(host.textContent).not.toContain(session.text);
  });

  it("keeps a leading quote, external selection, and unmatched context in their existing places", async () => {
    const otherSkill = { ...skill, title: "Plan", attrs: { ref: "second-skill" } };
    const body = inlineBody(["Review ", skill, " after ", { ...otherSkill, title: "Plan old" }, "."]);
    await mount(withContexts([skill, otherSkill, selection], `> Quoted words\n\n${body}`), 1);
    expect(host.querySelector("[data-bubble-text]")?.textContent).toBe("Review Review after Plan old.");
    const group = topGroup()!;
    expect(group.textContent).toBe(t("chat-contextgroup.summary_many", { count: 2 }));
    await click(group);
    const row = () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === selection.title);
    await vi.waitFor(() => expect(row()).toBeDefined());
    await click(row()!);
    expect(host.textContent).toContain(selection.text);
    expect(host.textContent).not.toContain(skill.text);
    await click(tokens()[0]);
    expect(group.getAttribute("aria-expanded")).toBe("true");
    expect(host.textContent).toContain(skill.text);
  });

  it("keeps legacy and entirely unmatched references behind the top group", async () => {
    await mount(withContexts([selection], "Check the selected lines."));
    await click(topGroup()!);
    await vi.waitFor(() => expect(host.textContent).toContain(selection.text));
    await mount(withContexts([selection], inlineBody(["Check ", skill, "."])));
    expect(host.querySelector("[data-bubble-text]")?.textContent).toBe("Check Review.");
    expect(topGroup()?.textContent).toContain(selection.title);
  });

  it("copies the sent prose with its native path spelling", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    try {
      await mount(withContexts([file], inlineBody(["Check ", file, "."])), 1);
      await click(host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-messageactions.copy_message")}"]`)!);
      expect(writeText).toHaveBeenCalledExactlyOnceWith("Check @docs/notes.md.");
    } finally {
      Reflect.deleteProperty(navigator, "clipboard");
    }
  });
});
