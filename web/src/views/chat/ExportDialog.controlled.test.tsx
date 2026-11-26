// @vitest-environment jsdom
// Controlled panels supply their own assistant name, conversation reader, save/copy actions and notices; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { ExportStopped } from "../../lib/chat-export";
import type { Item, Session } from "../../lib/types";
import type { ExportDialogProps, ExportReadRequest, ExportTarget } from "./ExportDialog";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  toast: vi.fn(),
  copyText: vi.fn(),
  usePersona: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  api: mocks.api,
}));
vi.mock("../../ui/atoms", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../ui/atoms")>(),
  toast: mocks.toast,
  copyText: mocks.copyText,
}));
vi.mock("../assistant/Persona", () => ({
  usePersona: mocks.usePersona,
}));

const { ExportDialog } = await import("./ExportDialog");
const { initI18n, t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const session: Session = {
  machine_id: "example-machine",
  session_id: "example-session",
  agent: "claude",
  cwd: "/repo/example",
  title: "Controlled chat",
} as Session;

const target: ExportTarget = {
  session,
  title: "Controlled chat",
  machineName: "Example computer",
  assistant: false,
};

const sampleItems: Item[] = [
  { id: "u1", seq: 1, role: "user", kind: "text", text: "Hello from user", ts: "2026-11-25T10:00:00Z" },
  { id: "u2", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t1", tool_input: { command: "echo hi" }, ts: "2026-11-25T10:01:00Z" },
  { id: "u3", seq: 3, role: "assistant", kind: "text", text: "Hello from assistant", ts: "2026-11-25T10:02:00Z" },
];

let host: HTMLDivElement;
let root: Root;
let onClose: Mock<() => void>;
let createObjectURL: Mock<(blob: Blob) => string>;
let revokeObjectURL: Mock<(url: string) => void>;

const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

const mount = async (props: Partial<ExportDialogProps> = {}) => {
  await act(async () => root.render(<ExportDialog open target={target} onClose={onClose} {...props} />));
  await flush();
};

const byTestId = (id: string) => document.querySelector<HTMLElement>(`[data-testid="${id}"]`)!;

beforeEach(async () => {
  await initI18n("en");
  vi.clearAllMocks();
  mocks.usePersona.mockReturnValue({ info: null, builtin: "mochi", mochi: true, name: "Default Persona", chosen: "mochi" });
  mocks.copyText.mockResolvedValue(true);
  createObjectURL = vi.fn(() => "blob:export-controlled");
  revokeObjectURL = vi.fn();
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = revokeObjectURL;
  onClose = vi.fn();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("ExportDialog with controlled callbacks", () => {
  it("reads, reports progress and downloads without touching default persona, API, blob or toast singletons", async () => {
    let finish!: (result: { items: Item[]; partial: boolean }) => void;
    let reportProgress!: (count: number) => void;
    const onReadConversation = vi.fn((options: ExportReadRequest) => {
      reportProgress = options.progress;
      return new Promise<{ items: Item[]; partial: boolean }>((resolve) => { finish = resolve; });
    });
    const onDownload = vi.fn().mockResolvedValue(undefined);
    const onCopy = vi.fn().mockResolvedValue(true);
    const onNotice = vi.fn();

    await act(async () => root.render(
      <ExportDialog
        open
        target={{ ...target, assistant: true }}
        onClose={onClose}
        personaName="Custom Assistant"
        onReadConversation={onReadConversation}
        onDownload={onDownload}
        onCopy={onCopy}
        onNotice={onNotice}
      />,
    ));

    expect(onReadConversation).toHaveBeenCalledTimes(1);
    expect(onReadConversation.mock.calls[0][0].work).toBe(false);
    expect((byTestId("export-download") as HTMLButtonElement).disabled).toBe(true);
    await act(async () => reportProgress(3));
    expect(byTestId("export-status").textContent).toContain(t("chat-export.reading", { n: 3 }));

    await act(async () => { finish({ items: sampleItems, partial: false }); await Promise.resolve(); });
    await flush();
    expect((byTestId("export-download") as HTMLButtonElement).disabled).toBe(false);

    await act(async () => byTestId("export-download").click());
    await flush();

    expect(onDownload).toHaveBeenCalledTimes(1);
    const file = onDownload.mock.calls[0][0] as { name: string; markdown: string };
    expect(file.name).toMatch(/^Controlled chat \d{4}-\d{2}-\d{2}\.md$/);
    expect(file.markdown).toContain("## Custom Assistant · ");
    expect(file.markdown).toContain("Hello from user");
    expect(file.markdown).toContain("Hello from assistant");
    expect(file.markdown).not.toContain("echo hi");
    expect(onNotice).toHaveBeenCalledWith(t("chat-export.downloaded", { name: file.name }), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.usePersona).not.toHaveBeenCalled();
    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
    expect(mocks.copyText).not.toHaveBeenCalled();
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it("re-reads with work steps and routes controlled copy failures and successes through onCopy and onNotice", async () => {
    const onReadConversation = vi.fn(async (_options: ExportReadRequest) => ({ items: sampleItems, partial: false }));
    const onCopy = vi.fn<(markdown: string) => Promise<boolean>>().mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    const onNotice = vi.fn();

    await mount({ personaName: "Custom Assistant", onReadConversation, onCopy, onNotice });
    await act(async () => byTestId("export-work").click());
    await flush();

    expect(onReadConversation).toHaveBeenCalledTimes(2);
    expect(onReadConversation.mock.calls[1]?.[0].work).toBe(true);

    await act(async () => byTestId("export-copy").click());
    await flush();
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy.mock.calls[0]?.[0]).toContain("<summary>Bash: echo hi</summary>");
    expect(onNotice).toHaveBeenCalledWith(t("ui-atoms.copy_failed"), "error");
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => byTestId("export-copy").click());
    await flush();
    expect(onCopy).toHaveBeenCalledTimes(2);
    expect(onNotice).toHaveBeenLastCalledWith(t("chat-export.copied"), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.usePersona).not.toHaveBeenCalled();
    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
    expect(mocks.copyText).not.toHaveBeenCalled();
  });

  it("recovers from controlled read failure via Retry and reports controlled download failures without closing", async () => {
    const onReadConversation = vi.fn()
      .mockRejectedValueOnce(new ExportStopped("changed"))
      .mockResolvedValueOnce({ items: sampleItems, partial: true });
    const onDownload = vi.fn().mockRejectedValue(new Error("disk full"));
    const onNotice = vi.fn();

    await mount({ personaName: "Custom Assistant", onReadConversation, onDownload, onNotice });
    expect(document.querySelector('[data-testid="export-status"] [role="alert"]')?.textContent).toBe(t("chat-export.changed"));
    expect((byTestId("export-download") as HTMLButtonElement).disabled).toBe(true);

    const retry = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === t("chat-export.retry"))!;
    await act(async () => retry.click());
    await flush();
    expect(onReadConversation).toHaveBeenCalledTimes(2);
    expect((byTestId("export-download") as HTMLButtonElement).disabled).toBe(false);

    await act(async () => byTestId("export-download").click());
    await flush();
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(onNotice).toHaveBeenCalledWith("disk full", "error");
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.api).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("does not restart onReadConversation when re-rendered with a new inline callback identity", async () => {
    const first = vi.fn(async () => ({ items: sampleItems, partial: false }));
    const second = vi.fn(async () => ({ items: sampleItems, partial: false }));

    await mount({ personaName: "Custom Assistant", onReadConversation: first });
    expect(first).toHaveBeenCalledTimes(1);

    await mount({ personaName: "Custom Assistant", onReadConversation: second });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();
  });
});

describe("ExportDialog default compatibility", () => {
  it("uses usePersona, default hub api reader, copyText and blob download with toast when optional props are omitted", async () => {
    mocks.api.mockResolvedValue({ items: sampleItems, has_more: false });

    await mount({ target: { ...target, assistant: true } });
    expect(mocks.usePersona).toHaveBeenCalled();
    expect(mocks.api).toHaveBeenCalledWith("/sessions/example-machine/example-session/items?turns=10&lazy_images=1&fold_work=1");

    await act(async () => byTestId("export-copy").click());
    await flush();
    expect(mocks.copyText).toHaveBeenCalledWith(expect.stringContaining("## Default Persona · "), { done: t("chat-export.copied") });
    expect(onClose).toHaveBeenCalledTimes(1);

    onClose.mockClear();
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    try {
      act(() => byTestId("export-download").click());
    } finally {
      click.mockRestore();
    }
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining("Controlled chat"), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
