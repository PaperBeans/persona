// @vitest-environment jsdom
// Controlled panels supply every resource URL: no default-panel session lookup or API URL builder is needed.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { AttachedPhotosProps } from "./AttachedPhotos";

const mocks = vi.hoisted(() => ({
  getSession: vi.fn(),
  fileThumbURL: vi.fn(),
  fileDownloadURL: vi.fn(),
}));
vi.mock("../../lib/store", () => ({ getSession: mocks.getSession }));
vi.mock("../../lib/api", () => ({ fileThumbURL: mocks.fileThumbURL, fileDownloadURL: mocks.fileDownloadURL }));
vi.mock("../../ui/icons", () => ({ Icon: ({ name }: { name: string }) => <span data-icon={name} /> }));
vi.mock("./ImageViewer", () => ({
  ImageViewer: ({ images, start, onClose }: { images: { id: string; src: string }[]; start: number; onClose: () => void }) => (
    <div data-image-viewer data-start={start} data-images={JSON.stringify(images)}>
      <button data-close-viewer onClick={onClose}>close</button>
    </div>
  ),
  ChatImageViewer: () => null,
}));

const { AttachedPhotos } = await import("./AttachedPhotos");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const paths = ["/photos/one.png", "/photos/two.png"];
let host: HTMLDivElement;
let root: Root;
let resolveThumbURL: Mock<(path: string, size: number) => string>;
let resolveDownloadURL: Mock<(path: string, options: { inline?: boolean }) => string>;

const mount = async (props: Partial<AttachedPhotosProps> = {}) => {
  await act(async () => root.render(
    <AttachedPhotos paths={paths} mid="example-machine" sid="example-session" {...props} />,
  ));
};
const images = () => [...host.querySelectorAll<HTMLImageElement>("img")].map((img) => img.getAttribute("src"));
const controlled = () => ({ resolveThumbURL, resolveDownloadURL });

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  resolveThumbURL = vi.fn((path, size) => `thumb:${path}:${size}`);
  resolveDownloadURL = vi.fn((path, options) => `download:${path}:${options.inline ? "inline" : "attachment"}`);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("AttachedPhotos with controlled resources", () => {
  it("renders thumbnails without reading the session or default URL builders", async () => {
    await mount(controlled());
    expect(images()).toEqual(paths.map((path) => `thumb:${path}:256`));
    expect(resolveThumbURL).toHaveBeenCalledTimes(2);
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
  });

  it("falls back from thumbnail to original, then to the image placeholder", async () => {
    await mount({ paths: [paths[0]], ...controlled() });
    const fail = () => act(() => host.querySelector("img")!.dispatchEvent(new Event("error")));
    fail();
    expect(images()).toEqual([`download:${paths[0]}:inline`]);
    expect(resolveDownloadURL).toHaveBeenCalledWith(paths[0], { inline: true });
    fail();
    expect(images()).toEqual([]);
    expect(host.querySelector('[data-icon="image"]')).not.toBeNull();
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
  });

  it("opens the lazy ImageViewer at the selected photo with controlled originals", async () => {
    await mount(controlled());
    const photo = [...host.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.getAttribute("aria-label") === t("chat-attachedphotos.photo", { n: 2 }));
    await act(async () => {
      photo!.click();
      await import("./ImageViewer");
      await vi.dynamicImportSettled();
    });
    const viewer = host.querySelector("[data-image-viewer]");
    expect(viewer?.getAttribute("data-start")).toBe("1");
    expect(JSON.parse(viewer?.getAttribute("data-images") ?? "null")).toEqual(paths.map((path) => ({
      id: path,
      src: `download:${path}:inline`,
    })));
    await act(async () => host.querySelector<HTMLButtonElement>("[data-close-viewer]")!.click());
    expect(host.querySelector("[data-image-viewer]")).toBeNull();
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.fileThumbURL).not.toHaveBeenCalled();
    expect(mocks.fileDownloadURL).not.toHaveBeenCalled();
  });

  it("uses an explicit cwd with the default URL builders without reading the session", async () => {
    mocks.fileThumbURL.mockImplementation((_mid, path, cwd, size) => `default-thumb:${path}:${cwd}:${size}`);
    await mount({ paths: [paths[0]], cwd: "/panel/cwd" });
    expect(images()).toEqual([`default-thumb:${paths[0]}:/panel/cwd:256`]);
    expect(mocks.getSession).not.toHaveBeenCalled();
    expect(mocks.fileThumbURL).toHaveBeenCalledWith("example-machine", paths[0], "/panel/cwd", 256);
  });
});

describe("AttachedPhotos default compatibility", () => {
  it("keeps the session cwd and default thumbnail URL behavior", async () => {
    mocks.getSession.mockReturnValue({ cwd: "/session/cwd" });
    mocks.fileThumbURL.mockImplementation((_mid, path, cwd, size) => `default-thumb:${path}:${cwd}:${size}`);
    await mount({ paths: [paths[0]] });
    expect(images()).toEqual([`default-thumb:${paths[0]}:/session/cwd:256`]);
    expect(mocks.getSession).toHaveBeenCalledWith("example-machine", "example-session");
    expect(mocks.fileThumbURL).toHaveBeenCalledWith("example-machine", paths[0], "/session/cwd", 256);
  });
});
