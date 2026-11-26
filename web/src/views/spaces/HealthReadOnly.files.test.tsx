// @vitest-environment jsdom
// End-to-end read-only file boundary for Health archive views across browser-off, hub-off, and core-only modes,
// plus default non-readOnly Core editing compatibility and Markdown cache isolation.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { setToken } from "../../lib/api";
import { healthReadOnly } from "../../lib/health-archive";
import { clearPanelHistory, closePanel, getPanel, type Panel } from "../../lib/panel";
import { getState, reset } from "../../lib/store";
import { registerModule, resetModulesForTest } from "../../core/module-host";
import { setModuleSwitchedOff } from "../../core/module-switch";
import { Markdown } from "../../ui/Markdown";
import { forgetMarkdown } from "../../ui/markdown-cache";
import { SidePanel } from "../SidePanel";
import { ReadOnlyChatView } from "../chat/ReadOnlyChatView";
import { resetStalePullsForTests } from "../chat/useTranscript";
import { FileView } from "../session/files/FileView";
import { HealthReadOnlyData } from "./HealthReadOnlyData";

const typed = vi.hoisted(() => ({ value: null as string | null }));
// CodeMirror requires layout APIs absent in jsdom; a plain textarea exposes the same edit/save callback path.
vi.mock("../session/files/CodeEditor", () => ({
  default: ({ text, onChange, bind }: { text: string; onChange: () => void; bind: (r: () => string) => void }) => {
    bind(() => typed.value ?? text);
    return (
      <textarea
        data-testid="mock-code-editor"
        defaultValue={text}
        onChange={(e) => {
          typed.value = e.target.value;
          onChange();
        }}
      />
    );
  },
}));

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof IntersectionObserver;
  window.matchMedia ??= ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// An ordinary writable document inside the user's home documents directory, referenced by a saved report.
const memoryPath = "/home/me/Documents/Health/sleep-diary.md";
const followUpPath = "/home/me/Documents/Health/follow-up.md";
const summaryPath = "/home/me/.persona/health/reports/annual.md";
const reportLinkMarkdown = `See [Sleep memory](${memoryPath}) for details.`;

type RecordedCall = { path: string; method: string; body?: Record<string, unknown> };

let host: HTMLDivElement;
let root: Root;
let calls: RecordedCall[];

const settle = async (check: () => unknown) =>
  vi.waitFor(
    async () => {
      await act(async () => {
        await vi.dynamicImportSettled();
        await Promise.resolve();
      });
      expect(check()).toBeTruthy();
    },
    { timeout: 5000 },
  );

function installFetchStub() {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const method = init?.method ?? "GET";
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
      calls.push({ path: url.pathname, method, body });

      if (url.pathname.endsWith("/health/reports")) {
        return Response.json({
          reports: [
            {
              name: "annual",
              title: "Annual checkup",
              path: "/home/me/.persona/health/reports/annual.pdf",
              record_path: summaryPath,
              size: 1200,
              modified_at: "2026-11-20T08:00:00Z",
            },
          ],
        });
      }
      if (url.pathname.endsWith("/assistant/memory")) {
        return Response.json({ files: [] });
      }
      if (url.pathname.endsWith("/items")) {
        return Response.json({
          items: [
            {
              id: "u1",
              seq: 1,
              role: "user",
              kind: "text",
              text: `<persona-file path="${followUpPath}" name="follow-up.md" kind="file" size="24"/>Review my sleep notes`,
            },
            {
              id: "a1",
              seq: 2,
              role: "assistant",
              kind: "text",
              text: reportLinkMarkdown,
            },
          ],
          has_more: false,
        });
      }
      if (url.pathname.endsWith("/file")) {
        const path = url.searchParams.get("path");
        if (path === summaryPath) return Response.json({ text: reportLinkMarkdown });
        if (path === followUpPath) {
          return Response.json({
            path: followUpPath,
            name: "follow-up.md",
            size: 16,
            mime: "text/markdown",
            text: "Follow-up notes.",
            truncated: false,
            is_dir: false,
            revision: "f1",
          });
        }
        return Response.json({
          path: memoryPath,
          name: "sleep-diary.md",
          size: 46,
          mime: "text/markdown",
          text: "Old memory.\n\n[Follow-up](./follow-up.md)",
          truncated: false,
          is_dir: false,
          revision: "r1",
        });
      }
      if (url.pathname.endsWith("/files/write")) {
        return Response.json({ path: body?.path ?? memoryPath, size: 18, revision: "r2" });
      }
      if (url.pathname.endsWith("/fs/stat")) {
        return Response.json({ stats: [] });
      }
      return Response.json({});
    }),
  );
}

const editButton = () => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("files-editor.edit")}"]`);
const showInFilesButton = () => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("files-nav.show_in_files")}"]`);
const downloadButton = () => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("session-files-fileview.download")}"]`);
const copyPathButton = () => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("session-files-fileview.copy_path")}"]`);

beforeEach(() => {
  reset();
  resetModulesForTest();
  resetStalePullsForTests();
  forgetMarkdown();
  clearPanelHistory();
  closePanel();
  localStorage.clear();
  typed.value = null;
  setToken("synthetic-test-token");
  installFetchStub();
  host = document.body.appendChild(document.createElement("div"));
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePanel();
  clearPanelHistory();
  forgetMarkdown();
  resetStalePullsForTests();
  setToken("");
  reset();
  resetModulesForTest();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("Health read-only file panel boundary", () => {
  it.each(["browser-off", "hub-off", "core-only"] as const)(
    "%s: report file links stay readable and downloadable without exposing Edit, Show in Files, or files/write",
    async (mode) => {
      Object.assign(getState(), {
        features: ["modules", "file_write", ...(mode === "browser-off" ? ["module.health"] : [])],
        loaded: true,
        conn: "online",
      });
      if (mode !== "core-only") {
        registerModule({
          id: "health",
          title: () => "Health",
          description: () => "Health",
          feature: "module.health",
          load: async () => ({}),
        });
      }
      if (mode === "browser-off") setModuleSwitchedOff("health", true);
      expect(healthReadOnly("health")).toBe(true);

      await act(async () =>
        root.render(
          <>
            <HealthReadOnlyData mid="mac" />
            <SidePanel />
          </>,
        ),
      );

      // Open the saved report and confirm both summary prose and inline report link remain readable.
      await settle(() => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Annual checkup")));
      await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Annual checkup"))!.click());
      await settle(() => [...document.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory"));
      expect([...document.querySelectorAll("a")].some((a) => a.textContent?.includes(t("spaces.report_open")))).toBe(true);

      // Click the machine-file link inside the report, then close the report modal as a real user must.
      await act(async () => [...document.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());
      await act(async () => [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === t("spaces.close"))!.click());

      expect(getPanel()).toMatchObject({ kind: "file", mid: "mac", path: memoryPath, readOnly: true });
      await settle(() => document.querySelector('[data-testid="file-markdown"]')?.textContent?.includes("Old memory."));

      // Linked file is readable and downloadable, with no Edit button and no indirect Show-in-Files write escape.
      expect(editButton()).toBeNull();
      expect(showInFilesButton()).toBeNull();
      expect(downloadButton()).not.toBeNull();
      expect(downloadButton()!.disabled).toBe(false);
      expect(copyPathButton()).not.toBeNull();

      // Switching to Source view still exposes no Edit or Show in Files controls.
      const sourceBtn = [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === t("session-files-fileview.mode_source"));
      expect(sourceBtn).toBeTruthy();
      await act(async () => sourceBtn!.click());
      await settle(() => document.querySelector('[data-testid="file-code"]'));
      expect(editButton()).toBeNull();
      expect(showInFilesButton()).toBeNull();

      // Following a nested relative link inside the read-only file viewer stays read-only too.
      const previewBtn = [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === t("session-files-fileview.mode_preview"));
      await act(async () => previewBtn!.click());
      await settle(() => [...document.querySelectorAll<HTMLAnchorElement>('[data-testid="file-markdown"] a')].find((a) => a.textContent === "Follow-up"));
      await act(async () => [...document.querySelectorAll<HTMLAnchorElement>('[data-testid="file-markdown"] a')].find((a) => a.textContent === "Follow-up")!.click());
      await settle(() => document.querySelector('[data-testid="file-markdown"]')?.textContent?.includes("Follow-up notes."));
      expect(editButton()).toBeNull();
      expect(showInFilesButton()).toBeNull();
      expect(downloadButton()).not.toBeNull();

      expect(calls.filter((c) => c.method !== "GET")).toEqual([]);
      expect(calls.some((c) => c.path.endsWith("/files/write"))).toBe(false);
    },
  );

  it("opens read-only file panels from stored Health transcript links and attached file cards", async () => {
    Object.assign(getState(), { features: ["modules", "file_write"], loaded: true, conn: "online" });
    expect(healthReadOnly("health")).toBe(true);

    await act(async () =>
      root.render(
        <>
          <ReadOnlyChatView mid="mac" sid="c1" onToggleSidebar={() => {}} />
          <SidePanel />
        </>,
      ),
    );

    // Assistant Markdown link opens a read-only file panel.
    await settle(() => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory"));
    await act(async () => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());
    expect(getPanel()).toMatchObject({ kind: "file", mid: "mac", path: memoryPath, readOnly: true });
    await settle(() => document.querySelector('[data-testid="file-markdown"]')?.textContent?.includes("Old memory."));
    expect(editButton()).toBeNull();
    expect(showInFilesButton()).toBeNull();
    expect(downloadButton()).not.toBeNull();

    // User attached file card also opens a read-only file panel.
    const fileCard = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("follow-up.md"));
    expect(fileCard).toBeTruthy();
    await act(async () => fileCard!.click());
    expect(getPanel()).toMatchObject({ kind: "file", mid: "mac", path: followUpPath, readOnly: true });
    await settle(() => document.querySelector('[data-testid="file-markdown"]')?.textContent?.includes("Follow-up notes."));
    expect(editButton()).toBeNull();
    expect(showInFilesButton()).toBeNull();

    expect(calls.filter((c) => c.method !== "GET")).toEqual([]);
  });

  it("preserves default non-readOnly Core editing and isolates Markdown cache entries between writable and read-only views", async () => {
    Object.assign(getState(), { features: ["modules", "file_write", "module.health"], loaded: true, conn: "online" });

    // 1. Default Markdown (writable) renders first and populates markdown-cache.
    await act(async () =>
      root.render(
        <>
          <Markdown text={reportLinkMarkdown} mid="mac" />
          <SidePanel />
        </>,
      ),
    );
    await settle(() => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory"));
    await act(async () => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());

    const writablePanel = getPanel() as Extract<Panel, { kind: "file" }> | null;
    expect(writablePanel?.kind).toBe("file");
    expect(writablePanel?.readOnly).toBeUndefined();
    await settle(() => editButton());
    expect(showInFilesButton()).not.toBeNull();

    // Verify normal Core editing and POST /files/write remain fully functional.
    await act(async () => editButton()!.click());
    await settle(() => document.querySelector('[data-testid="mock-code-editor"]'));
    const area = document.querySelector<HTMLTextAreaElement>('[data-testid="mock-code-editor"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, "Updated memory.");
      area.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const saveBtn = [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === t("files-editor.save"))!;
    await act(async () => saveBtn.click());
    await settle(() => calls.some((c) => c.method === "POST" && c.path.endsWith("/files/write")));
    expect(calls.find((c) => c.method === "POST" && c.path.endsWith("/files/write"))?.body).toMatchObject({
      path: memoryPath,
      text: "Updated memory.",
    });

    // 2. Same text and machine rendered with readOnly must NOT reuse the cached writable tree.
    await act(async () =>
      root.render(
        <>
          <Markdown text={reportLinkMarkdown} mid="mac" readOnly />
          <SidePanel />
        </>,
      ),
    );
    await act(async () => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());
    expect(getPanel()).toMatchObject({ kind: "file", mid: "mac", path: memoryPath, readOnly: true });
    await settle(() => document.querySelector('[data-testid="file-markdown"]'));
    expect(editButton()).toBeNull();
    expect(showInFilesButton()).toBeNull();

    // 3. Reverse order: readOnly cached first must NOT leak into a subsequent default writable Markdown render.
    forgetMarkdown();
    closePanel();
    await act(async () =>
      root.render(
        <>
          <Markdown text={reportLinkMarkdown} mid="mac" readOnly />
          <SidePanel />
        </>,
      ),
    );
    await act(async () => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());
    expect(getPanel()).toMatchObject({ kind: "file", mid: "mac", path: memoryPath, readOnly: true });

    await act(async () =>
      root.render(
        <>
          <Markdown text={reportLinkMarkdown} mid="mac" />
          <SidePanel />
        </>,
      ),
    );
    await act(async () => [...host.querySelectorAll("a")].find((a) => a.textContent === "Sleep memory")!.click());
    const restoredWritable = getPanel() as Extract<Panel, { kind: "file" }> | null;
    expect(restoredWritable?.kind).toBe("file");
    expect(restoredWritable?.readOnly).toBeUndefined();
    await settle(() => editButton());
    expect(showInFilesButton()).not.toBeNull();
  });

  it("suppresses Edit and Show in Files directly in FileView when readOnly is set even if onShowInFolder is passed", async () => {
    Object.assign(getState(), { features: ["file_write"], loaded: true, conn: "online" });
    const showInFolder = vi.fn();

    await act(async () =>
      root.render(
        <FileView mid="mac" path={memoryPath} readOnly onClose={() => {}} onOpenPath={() => {}} onShowInFolder={showInFolder} />,
      ),
    );
    await settle(() => document.querySelector('[data-testid="file-markdown"]')?.textContent?.includes("Old memory."));
    expect(editButton()).toBeNull();
    expect(showInFilesButton()).toBeNull();
    expect(downloadButton()).not.toBeNull();
    expect(showInFolder).not.toHaveBeenCalled();
  });
});
