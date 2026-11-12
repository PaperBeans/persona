// @vitest-environment jsdom
// @ a workspace in the Persona assistant's composer (#5594): the menu lists the hub's workspaces (GET /workspaces),
// filters them as you type, Enter picks one into a chip, and the message goes out with a
// <persona-context kind="workspace" machine cwd> in front. Agents' composers don't offer workspaces.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { loadPages } from "../../lib/lazy-page";
import type { Workspace } from "../../lib/workspace-board";
import { Composer, type Outgoing } from "./Composer";
import type { MentionSource } from "./ComposerPanel";

const WORKSPACES: Workspace[] = [
  { id: "w1", machine_id: "laptop", source: "herdr", source_id: "1", title: "weather", cwd: "/home/me/code/weather", branch: "main", online: true, available: true },
  { id: "w2", machine_id: "laptop", source: "session", source_id: "2", title: "notes", cwd: "/home/me/notes", online: true, available: true },
  // the same folder seen again through a session: listed once
  { id: "w3", machine_id: "laptop", source: "session", source_id: "3", title: "weather", cwd: "/home/me/code/weather/", online: true, available: true },
  // a host space without a folder can't be worked in
  { id: "w4", machine_id: "desk", source: "herdr", source_id: "4", title: "scratch", cwd: "", online: true, available: true },
];

const api = vi.fn(async (path: string, _init?: unknown): Promise<unknown> => {
  if (path === "/workspaces") return { workspaces: WORKSPACES };
  throw new Error(`unexpected ${path}`);
});
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

// the + / @ panel is a lazy chunk: in before the first @
beforeAll(() => loadPages());

beforeEach(() => {
  Element.prototype.scrollIntoView ??= () => {}; // jsdom has none
  api.mockClear();
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const render = (mention: MentionSource, onSend: (m: Outgoing) => Promise<boolean> = async () => true, draftKey = "persona-new") =>
  act(() => root.render(<Composer draftKey={draftKey} placeholder="" mention={mention} onSend={onSend} />));

const box = () => host.querySelector("textarea")!;

async function type(text: string) {
  const el = box();
  await act(async () => {
    el.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(el, text);
    el.setSelectionRange(text.length, text.length);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => {}); // the workspaces' read settles
}

const key = (k: string) => act(() => void box().dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true })));
// the panel's resource rows (its own "Add images…" row left out)
const options = () => [...host.querySelectorAll('[role="option"]:not([data-panel-action])')].map((o) => o.textContent ?? "");

describe("@ workspaces", () => {
  it("lists each workspace with a folder once, with its computer and folder", async () => {
    render({ self: "persona-new", files: () => [], workspaces: true });
    await type("@");
    expect(api).toHaveBeenCalledWith("/workspaces", expect.anything());
    const rows = options();
    expect(rows).toHaveLength(2);
    expect(rows[0]).toContain("weather");
    expect(rows[0]).toContain("laptop · ~/code/weather · main");
    expect(rows[1]).toContain("notes");
  });

  it("filters by what is typed after @", async () => {
    render({ self: "persona-new", files: () => [], workspaces: true });
    await type("@note");
    expect(options()).toHaveLength(1);
    expect(options()[0]).toContain("notes");
  });

  it("picks with the keyboard into a chip and sends the workspace in front of the message", async () => {
    const onSend = vi.fn(async (_m: Outgoing) => true);
    render({ self: "persona-new", files: () => [], workspaces: true }, onSend);
    await type("@wea");
    key("Enter");
    expect(options()).toHaveLength(0); // the menu closed
    expect(box().value).toBe("");
    expect(host.textContent).toContain("工作区");
    await type("Add a dark mode");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    expect(onSend).toHaveBeenCalledOnce();
    const text = onSend.mock.calls[0][0].text;
    expect(text).toMatch(/^<persona-context kind="workspace" title="weather" url="" machine="laptop" cwd="\/home\/me\/code\/weather" branch="main">\n/);
    expect(text).toContain("工作区：weather");
    expect(text.endsWith("</persona-context>\n\nAdd a dark mode")).toBe(true);
  });

  it("is not offered where the source doesn't ask for it (an agent's composer)", async () => {
    render({ self: "m/s", files: () => [] }, async () => true, "m/s");
    await type("@wea");
    expect(api).not.toHaveBeenCalledWith("/workspaces", expect.anything());
    expect(options().some((o) => o.includes("工作区"))).toBe(false);
  });

  it("leaves the group out when the hub can't list workspaces", async () => {
    api.mockImplementationOnce(async () => {
      throw new Error("404");
    });
    render({ self: "persona-new", files: () => [], workspaces: true });
    await type("@");
    expect(options()).toHaveLength(0);
  });
});
