// @vitest-environment jsdom
// Controlled file, document and reply-file cards supply their own opener, URL resolvers and path stats;
// default callers still fall back to openPanel, fileThumbURL, fileDownloadURL and usePathLinks.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { PathStat } from "../../lib/path-presence";
import type { AttachedFile } from "../../lib/turns";
import type { DocumentCardsProps, FileCardsProps, ReplyFilesProps } from "./FileCards";

const mocks = vi.hoisted(() => ({
  fileThumbURL: vi.fn(),
  fileDownloadURL: vi.fn(),
  openPanel: vi.fn(),
  usePathLinks: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  fileThumbURL: mocks.fileThumbURL,
  fileDownloadURL: mocks.fileDownloadURL,
}));
vi.mock("../../lib/panel", () => ({ openPanel: mocks.openPanel, useReadOnlyFile: () => false }));
vi.mock("../../lib/path-presence", () => ({ usePathLinks: mocks.usePathLinks }));
vi.mock("../../ui/icons", () => ({ Icon: ({ name }: { name: string }) => <span data-icon={name} /> }));

const { DocumentCards, FileCards, ReplyFiles } = await import("./FileCards");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

let host: HTMLDivElement;
let root: Root;
let onOpenFile: Mock<(file: AttachedFile) => void>;
let resolveThumbURL: Mock<(path: string, size: number) => string>;
let resolveDownloadURL: Mock<(path: string, options: { inline?: boolean }) => string>;

const mountFileCards = async (props: FileCardsProps) => {
  await act(async () => root.render(<FileCards {...props} />));
};
const mountDocumentCards = async (props: DocumentCardsProps) => {
  await act(async () => root.render(<DocumentCards {...props} />));
};
const mountReplyFiles = async (props: ReplyFilesProps) => {
  await act(async () => root.render(<ReplyFiles {...props} />));
};

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  onOpenFile = vi.fn();
  resolveThumbURL = vi.fn((path, size) => `thumb:${path}:${size}`);
  resolveDownloadURL = vi.fn((path, options) => `download:${path}:${options.inline ? "inline" : "attachment"}`);
  mocks.fileThumbURL.mockImplementation((mid, path, cwd, size) => `default-thumb:${mid}:${path}:${cwd ?? ""}:${size}`);
  mocks.fileDownloadURL.mockImplementation((mid, path, cwd, opts?: { inline?: boolean }) =>
    `default-download:${mid}:${path}:${cwd ?? ""}:${opts?.inline ? "inline" : "attachment"}`);
  mocks.usePathLinks.mockReturnValue({ stat: () => undefined, key: "" });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("FileCards with controlled opener and download URLs", () => {
  it("routes plain file clicks to onOpenFile and toggles folded rows without calling openPanel", async () => {
    const files: AttachedFile[] = Array.from({ length: 5 }, (_, i) => ({
      kind: "file",
      path: `/workspace/file-${i + 1}.txt`,
      name: `file-${i + 1}.txt`,
      size: 128,
    }));
    await mountFileCards({ files, mid: "example-machine", onOpenFile });

    expect(host.querySelectorAll("button[title]")).toHaveLength(3);
    const toggle = host.querySelector<HTMLButtonElement>('[data-testid="file-cards-more"]')!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(toggle.textContent).toContain(t("chat-attachedphotos.files_more", { count: 2 }));

    act(() => toggle.click());
    expect(host.querySelectorAll("button[title]")).toHaveLength(5);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    const fifth = host.querySelector<HTMLButtonElement>('button[title="/workspace/file-5.txt"]')!;
    act(() => fifth.click());
    expect(onOpenFile).toHaveBeenCalledWith(files[4]);
    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
  });

  it("resolves inline and fallback download URLs for media cards without calling fileDownloadURL", async () => {
    const media: AttachedFile[] = [
      { kind: "video", path: "/workspace/demo.mp4", name: "demo.mp4", size: 2048, duration: 12 },
      { kind: "audio", path: "/workspace/clip.mp3", name: "clip.mp3", size: 512 },
    ];
    await mountFileCards({ files: media, mid: "example-machine", resolveDownloadURL, onOpenFile });

    const video = host.querySelector("video")!;
    const audio = host.querySelector("audio")!;
    expect(video.getAttribute("src")).toBe("download:/workspace/demo.mp4:inline");
    expect(audio.getAttribute("src")).toBe("download:/workspace/clip.mp3:inline");
    expect(resolveDownloadURL).toHaveBeenCalledWith("/workspace/demo.mp4", { inline: true });
    expect(resolveDownloadURL).toHaveBeenCalledWith("/workspace/clip.mp3", { inline: true });

    act(() => video.dispatchEvent(new Event("error")));
    const link = host.querySelector<HTMLAnchorElement>("a[download]")!;
    expect(link.getAttribute("href")).toBe("download:/workspace/demo.mp4:attachment");
    expect(resolveDownloadURL).toHaveBeenCalledWith("/workspace/demo.mp4", { inline: false });
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
    expect(mocks.openPanel).not.toHaveBeenCalled();
  });
});

describe("DocumentCards with controlled opener and thumbnail URLs", () => {
  it("renders controlled thumbnails, falls back to the glyph on error, and opens via onOpenFile", async () => {
    const doc: AttachedFile = { kind: "file", path: "/workspace/report.pdf", name: "report.pdf", size: 4096 };
    await mountDocumentCards({ files: [doc], mid: "example-machine", cwd: "/workspace", onOpenFile, resolveThumbURL });

    const img = host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')!;
    expect(img.getAttribute("src")).toBe("thumb:/workspace/report.pdf:128");
    expect(resolveThumbURL).toHaveBeenCalledWith("/workspace/report.pdf", 128);

    act(() => img.dispatchEvent(new Event("error")));
    expect(host.querySelector('[data-testid="document-card"] img')).toBeNull();

    const card = host.querySelector<HTMLButtonElement>('[data-testid="document-card"]')!;
    act(() => card.click());
    expect(onOpenFile).toHaveBeenCalledWith(doc);
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.openPanel).not.toHaveBeenCalled();
  });
});

describe("ReplyFiles with controlled path stats and explicit files", () => {
  it("resolves reply paths with resolvePathStat without touching usePathLinks or default singletons", async () => {
    const stats: Record<string, PathStat> = {
      "/workspace/report.docx": { kind: "file", size: 2048 },
      "/workspace/run.csv": { kind: "file", size: 64 },
      "/workspace/demo.mp4": { kind: "file", size: 4096 },
      "/workspace/missing.pdf": { kind: "missing" },
    };
    const resolvePathStat = vi.fn((path: string) => stats[path]);
    await mountReplyFiles({
      text: "Check `/workspace/report.docx`, `/workspace/run.csv`, `/workspace/demo.mp4` and `/workspace/missing.pdf`.",
      mid: "example-machine",
      cwd: "/workspace",
      resolvePathStat,
      onOpenFile,
      resolveThumbURL,
      resolveDownloadURL,
    });

    expect(mocks.usePathLinks).not.toHaveBeenCalled();
    expect(host.querySelectorAll('[data-testid="document-card"]')).toHaveLength(1);
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe(
      "thumb:/workspace/report.docx:128",
    );
    expect(host.querySelector("video")?.getAttribute("src")).toBe("download:/workspace/demo.mp4:inline");
    expect(host.textContent).toContain("run.csv");
    expect(host.textContent).not.toContain("missing.pdf");

    act(() => host.querySelector<HTMLButtonElement>('button[title="/workspace/run.csv"]')!.click());
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="document-card"]')!.click());
    expect(onOpenFile).toHaveBeenNthCalledWith(1, { kind: "file", path: "/workspace/run.csv", name: "run.csv", size: 64 });
    expect(onOpenFile).toHaveBeenNthCalledWith(2, { kind: "file", path: "/workspace/report.docx", name: "report.docx", size: 2048 });
    expect(mocks.openPanel).not.toHaveBeenCalled();
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
  });

  it("renders explicit files directly without calling usePathLinks or parsing reply text", async () => {
    const files: AttachedFile[] = [
      ...Array.from({ length: 7 }, (_, i) => ({ kind: "file" as const, path: `/workspace/doc-${i + 1}.pdf`, name: `doc-${i + 1}.pdf` })),
      { kind: "file", path: "/workspace/notes.txt", name: "notes.txt", size: 42 },
    ];
    await mountReplyFiles({
      text: "no paths in text",
      mid: "example-machine",
      cwd: "/workspace",
      files,
      onOpenFile,
      resolveThumbURL,
    });

    expect(mocks.usePathLinks).not.toHaveBeenCalled();
    expect(host.querySelectorAll('[data-testid="document-card"]')).toHaveLength(6);
    expect(host.querySelector('button[title="/workspace/notes.txt"]')).not.toBeNull();
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.openPanel).not.toHaveBeenCalled();
  });
});

describe("FileCards default compatibility", () => {
  it("keeps openPanel and fileDownloadURL defaults in FileCards when cwd is omitted", async () => {
    const files: AttachedFile[] = [
      { kind: "file", path: "/workspace/readme.md", name: "readme.md", size: 100 },
      { kind: "video", path: "/workspace/clip.mp4", name: "clip.mp4", size: 1000 },
    ];
    await mountFileCards({ files, mid: "example-machine" });

    act(() => host.querySelector<HTMLButtonElement>('button[title="/workspace/readme.md"]')!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({ kind: "file", mid: "example-machine", path: "/workspace/readme.md" });
    expect(Object.hasOwn(mocks.openPanel.mock.calls[0][0], "cwd")).toBe(false);

    const video = host.querySelector("video")!;
    expect(video.getAttribute("src")).toBe("default-download:example-machine:/workspace/clip.mp4::inline");
    act(() => video.dispatchEvent(new Event("error")));
    expect(host.querySelector<HTMLAnchorElement>("a[download]")?.getAttribute("href")).toBe(
      "default-download:example-machine:/workspace/clip.mp4::attachment",
    );
  });

  it("forwards cwd to default openPanel and MediaPlayer for relative paths in FileCards", async () => {
    const files: AttachedFile[] = [
      { kind: "file", path: "notes/plan.md", name: "plan.md", size: 64 },
      { kind: "video", path: "clips/demo.mp4", name: "demo.mp4", size: 1024 },
      { kind: "audio", path: "audio/clip.mp3", name: "clip.mp3", size: 256 },
    ];
    await mountFileCards({ files, mid: "example-machine", cwd: "/workspace/project" });

    act(() => host.querySelector<HTMLButtonElement>('button[title="notes/plan.md"]')!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "file",
      mid: "example-machine",
      path: "notes/plan.md",
      cwd: "/workspace/project",
    });

    const video = host.querySelector("video")!;
    const audio = host.querySelector("audio")!;
    expect(video.getAttribute("src")).toBe("default-download:example-machine:clips/demo.mp4:/workspace/project:inline");
    expect(audio.getAttribute("src")).toBe("default-download:example-machine:audio/clip.mp3:/workspace/project:inline");
    act(() => video.dispatchEvent(new Event("error")));
    expect(host.querySelector<HTMLAnchorElement>("a[download]")?.getAttribute("href")).toBe(
      "default-download:example-machine:clips/demo.mp4:/workspace/project:attachment",
    );
  });

  it("forwards cwd for explicit ReplyFiles relative media, plain files, and documents", async () => {
    const files: AttachedFile[] = [
      { kind: "video", path: "clips/demo.mp4", name: "demo.mp4", size: 2048 },
      { kind: "audio", path: "audio/voice.mp3", name: "voice.mp3", size: 512 },
      { kind: "file", path: "notes/run.csv", name: "run.csv", size: 32 },
      { kind: "file", path: "docs/report.pdf", name: "report.pdf", size: 4096 },
    ];
    await mountReplyFiles({
      text: "",
      mid: "example-machine",
      cwd: "/workspace/project",
      files,
    });

    expect(mocks.usePathLinks).not.toHaveBeenCalled();

    const video = host.querySelector("video")!;
    const audio = host.querySelector("audio")!;
    expect(video.getAttribute("src")).toBe("default-download:example-machine:clips/demo.mp4:/workspace/project:inline");
    expect(audio.getAttribute("src")).toBe("default-download:example-machine:audio/voice.mp3:/workspace/project:inline");
    act(() => video.dispatchEvent(new Event("error")));
    expect(host.querySelector<HTMLAnchorElement>("a[download]")?.getAttribute("href")).toBe(
      "default-download:example-machine:clips/demo.mp4:/workspace/project:attachment",
    );

    act(() => host.querySelector<HTMLButtonElement>('button[title="notes/run.csv"]')!.click());
    expect(mocks.openPanel).toHaveBeenNthCalledWith(1, {
      kind: "file",
      mid: "example-machine",
      path: "notes/run.csv",
      cwd: "/workspace/project",
    });

    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe(
      "default-thumb:example-machine:docs/report.pdf:/workspace/project:128",
    );
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="document-card"]')!.click());
    expect(mocks.openPanel).toHaveBeenNthCalledWith(2, {
      kind: "file",
      mid: "example-machine",
      path: "docs/report.pdf",
      cwd: "/workspace/project",
    });
  });

  it("keeps fileThumbURL and openPanel defaults in DocumentCards consistent for absolute and relative paths", async () => {
    const relativeDoc: AttachedFile = { kind: "file", path: "docs/slides.pptx", name: "slides.pptx" };
    await mountDocumentCards({ files: [relativeDoc], mid: "example-machine", cwd: "/workspace/project" });

    expect(mocks.fileThumbURL).toHaveBeenCalledWith("example-machine", "docs/slides.pptx", "/workspace/project", 128);
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe(
      "default-thumb:example-machine:docs/slides.pptx:/workspace/project:128",
    );
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="document-card"]')!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "file",
      mid: "example-machine",
      path: "docs/slides.pptx",
      cwd: "/workspace/project",
    });

    mocks.openPanel.mockClear();
    const absoluteDoc: AttachedFile = { kind: "file", path: "/workspace/slides.pptx", name: "slides.pptx" };
    await mountDocumentCards({ files: [absoluteDoc], mid: "example-machine" });
    act(() => host.querySelector<HTMLButtonElement>('[data-testid="document-card"]')!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "file",
      mid: "example-machine",
      path: "/workspace/slides.pptx",
    });
    expect(Object.hasOwn(mocks.openPanel.mock.calls[0][0], "cwd")).toBe(false);
  });

  it("keeps usePathLinks and default card wiring in ReplyFiles", async () => {
    const stats: Record<string, PathStat> = {
      "/workspace/spec.pdf": { kind: "file", size: 1024 },
      "/workspace/run.csv": { kind: "file", size: 20 },
    };
    mocks.usePathLinks.mockReturnValue({ stat: (p: string) => stats[p], key: "/workspace/spec.pdf\n/workspace/run.csv" });

    await mountReplyFiles({
      text: "See `/workspace/spec.pdf` and `/workspace/run.csv`.",
      mid: "example-machine",
      cwd: "/workspace",
    });

    expect(mocks.usePathLinks).toHaveBeenCalledWith("example-machine", "/workspace", ["/workspace/spec.pdf", "/workspace/run.csv"]);
    expect(host.querySelector<HTMLImageElement>('[data-testid="document-card"] img')?.getAttribute("src")).toBe(
      "default-thumb:example-machine:/workspace/spec.pdf:/workspace:128",
    );
    act(() => host.querySelector<HTMLButtonElement>('button[title="/workspace/run.csv"]')!.click());
    expect(mocks.openPanel).toHaveBeenCalledWith({
      kind: "file",
      mid: "example-machine",
      path: "/workspace/run.csv",
      cwd: "/workspace",
    });
  });
});
