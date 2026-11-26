// @vitest-environment jsdom
// The upload dialog (#2404): nothing goes before there is an image and a name, a file the hub can't read or one too
// small is refused at once, the body carries the crop and the trimmed texts, a second click sends nothing more, the
// progress shows, and the hub's refusal is shown as it is.
import { StrictMode, act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";
import type { PickedImage, UploadAnswer, UploadBody } from "../../lib/look-upload";
import type { ExtendedUploadBody, PatchAnswer, PatchLookBody, PickedUploadImage } from "../../lib/look-upload-states";
import { PERSONA_STATES, type PersonaInfo } from "../../lib/persona";
import { LookUploadDialog, type LookUploadDeps } from "./LookUpload";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout"] });
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const image = (w: number, h: number): PickedImage => ({ source: {} as CanvasImageSource, size: { w, h }, release: vi.fn() });

interface Sent {
  body: UploadBody;
  progress?: (sent: number, total: number) => void;
  resolve: (a: UploadAnswer) => void;
  reject: (e: unknown) => void;
}

function setup(over: Partial<LookUploadDeps> = {}, size = { w: 800, h: 600 }) {
  const sent: Sent[] = [];
  const picked = image(size.w, size.h);
  const deps: LookUploadDeps = {
    read: vi.fn(async () => picked),
    render: vi.fn(() => ({ image: "Q1JPUA==", mime: "image/png", side: 600 })),
    send: vi.fn((body: UploadBody, opts: { onProgress?: (a: number, b: number) => void } = {}) =>
      new Promise<UploadAnswer>((resolve, reject) => sent.push({ body, progress: opts.onProgress, resolve, reject }))),
    ...over,
  };
  const onClose = vi.fn();
  const onSend = vi.fn();
  const onUploaded = vi.fn();
  act(() => root.render(<LookUploadDialog open onClose={onClose} onSend={onSend} onUploaded={onUploaded} deps={deps} />));
  return { deps, sent, onClose, onSend, onUploaded, picked };
}

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const button = (label: string) => [...dialog().querySelectorAll("button")].find((b) => b.textContent?.includes(label))!;
const uploadButton = () => button(t("assistant-lookupload.upload"));
const field = (label: string) => document.getElementById([...dialog().querySelectorAll("label")].find((l) => l.textContent === label)!.htmlFor) as HTMLInputElement;
const alertText = () => dialog().querySelector('[role="alert"]')?.textContent ?? "";

async function pick(file: File) {
  const input = dialog().querySelector<HTMLInputElement>('[data-testid="look-upload-file"]')!;
  Object.defineProperty(input, "files", { value: [file], configurable: true });
  await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
}

function type(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const png = () => new File(["x"], "me.png", { type: "image/png" });

describe("the upload dialog", () => {
  it("asks for an image first, then crops it with a zoom slider", async () => {
    const { deps } = setup();
    expect(dialog().querySelector('[data-testid="look-upload-drop"]')).not.toBeNull();
    expect(uploadButton().disabled).toBe(true);
    await pick(png());
    expect(deps.read).toHaveBeenCalledTimes(1);
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).not.toBeNull();
    const zoom = dialog().querySelector<HTMLInputElement>('input[type="range"]')!;
    expect(zoom.getAttribute("aria-label")).toBe(t("assistant-lookupload.zoom"));
    expect(Number(zoom.max)).toBe(8);
    // no name yet
    expect(uploadButton().disabled).toBe(true);
  });

  it("refuses a file the hub can't read and an image too small, before anything goes", async () => {
    const { deps } = setup({}, { w: 40, h: 300 });
    await pick(new File(["x"], "notes.txt", { type: "text/plain" }));
    expect(alertText()).toBe(t("assistant-lookupload.not_image"));
    expect(deps.read).not.toHaveBeenCalled();
    await pick(png());
    expect(alertText()).toBe(t("assistant-lookupload.too_small", { px: 64 }));
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).toBeNull();
  });

  it("says so when the browser can't decode the file", async () => {
    setup({ read: vi.fn(async () => Promise.reject(new Error("decode"))) });
    await pick(png());
    expect(alertText()).toBe(t("assistant-lookupload.not_image"));
  });

  it("sends the crop with the trimmed name and description once, shows the progress, and hands over the answer", async () => {
    const { deps, sent, onClose, onSend, onUploaded, picked } = setup();
    await pick(png());
    type(field(t("assistant-lookupload.name")), "  Doudou  ");
    type(field(t("assistant-lookupload.subtitle")), " A round one ");
    expect(uploadButton().disabled).toBe(false);
    await act(async () => uploadButton().click());
    await act(async () => uploadButton().click()); // a second click while it is busy sends nothing
    await act(async () => vi.advanceTimersByTime(50));
    // the picker hears as the request leaves, before the answer
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend.mock.invocationCallOrder[0]).toBeLessThan((deps.send as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0]);
    expect(deps.render).toHaveBeenCalledTimes(1);
    expect((deps.render as ReturnType<typeof vi.fn>).mock.calls[0].slice(1)).toEqual([picked.size, { x: 400, y: 300, zoom: 1 }]);
    expect(sent).toHaveLength(1);
    expect(sent[0].body).toEqual({ name: "Doudou", subtitle: "A round one", image: "Q1JPUA==", choose: true });
    expect(uploadButton().disabled).toBe(true);
    expect(uploadButton().getAttribute("aria-busy")).toBe("true");

    act(() => sent[0].progress!(50, 100));
    expect(document.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("50");
    expect(document.body.textContent).toContain(t("assistant-lookupload.uploading", { percent: 50 }));
    act(() => sent[0].progress!(100, 100));
    expect(document.body.textContent).toContain(t("assistant-lookupload.making"));

    const answer = { look: { id: "u-1", source: "hub", name: "Doudou", app_icon: "persona", uploaded: true }, current: { id: "u-1", effective: "u-1" } } as UploadAnswer;
    await act(async () => sent[0].resolve(answer));
    expect(onUploaded).toHaveBeenCalledWith(answer);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows the hub's refusal as it is and lets the user try again", async () => {
    const { sent } = setup();
    await pick(png());
    type(field(t("assistant-lookupload.name")), "Doudou");
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    await act(async () => sent[0].reject(new ApiError(409, "Already 50 uploaded avatars", { error: "Already 50 uploaded avatars", code: "too_many" })));
    expect(alertText()).toBe("Already 50 uploaded avatars");
    expect(uploadButton().disabled).toBe(false);
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    expect(sent).toHaveLength(2);
    expect(alertText()).toBe("");
  });

  it("says so when the crop can't be drawn, and sends nothing", async () => {
    const { sent } = setup({ render: vi.fn(() => { throw new Error("canvas"); }) });
    await pick(png());
    type(field(t("assistant-lookupload.name")), "Doudou");
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    expect(alertText()).toBe(t("assistant-lookupload.export_failed", { error: "canvas" }));
    expect(sent).toHaveLength(0);
    expect(uploadButton().disabled).toBe(false);
  });

  it("holds a name past 40 characters back, and counts characters the way the hub does", async () => {
    setup();
    await pick(png());
    const name = field(t("assistant-lookupload.name"));
    type(name, "x".repeat(41));
    expect(uploadButton().disabled).toBe(true);
    expect(name.getAttribute("aria-invalid")).toBe("true");
    expect(document.getElementById(name.getAttribute("aria-describedby")!)?.textContent).toBe("41/40");
    type(name, "😀".repeat(40));
    expect(uploadButton().disabled).toBe(false);
  });

  it("stops the request when it is closed", async () => {
    let signal: AbortSignal | undefined;
    const { onClose, onUploaded } = setup({
      send: vi.fn((_: UploadBody, opts: { signal?: AbortSignal } = {}) => {
        signal = opts.signal;
        return new Promise<UploadAnswer>(() => {});
      }),
    });
    await pick(png());
    type(field(t("assistant-lookupload.name")), "Doudou");
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    await act(async () => button(t("common.cancel")).click());
    expect(signal?.aborted).toBe(true);
    expect(onClose).toHaveBeenCalled();
    expect(onUploaded).not.toHaveBeenCalled();
  });

  it("stops the request when it goes without closing (the page left under it)", async () => {
    let signal: AbortSignal | undefined;
    const { onUploaded, picked } = setup({
      send: vi.fn((_: UploadBody, opts: { signal?: AbortSignal } = {}) => {
        signal = opts.signal;
        return new Promise<UploadAnswer>(() => {});
      }),
    });
    await pick(png());
    type(field(t("assistant-lookupload.name")), "Doudou");
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    expect(signal?.aborted).toBe(false);
    act(() => root.unmount());
    root = createRoot(host);
    expect(signal?.aborted).toBe(true);
    expect(picked.release).toHaveBeenCalledTimes(1);
    expect(onUploaded).not.toHaveBeenCalled();
  });

  it("lets go of an image decoded after it went", async () => {
    let decoded: (img: PickedImage) => void = () => {};
    setup({ read: vi.fn(() => new Promise<PickedImage>((done) => (decoded = done))) });
    await pick(png());
    act(() => root.unmount());
    root = createRoot(host);
    const late = image(800, 600);
    await act(async () => decoded(late));
    expect(late.release).toHaveBeenCalledTimes(1);
  });

  it("hides theme color and state rows on an old hub without look_uploads_states", async () => {
    setup();
    expect(dialog().querySelector('[data-testid="look-color-toggle"]')).toBeNull();
    expect(dialog().querySelector('[data-testid="look-states-toggle"]')).toBeNull();
  });

  it("offers collapsed Theme color and Pictures for each state when look_uploads_states is enabled, handles animated state previews and respects Reduce Motion", async () => {
    const sent: ExtendedUploadBody[] = [];
    const mainImg: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 800, h: 600 }, release: vi.fn() };
    const animBytes = new Uint8Array([1, 2, 3, 4]);
    const animBlob = new Blob([animBytes], { type: "image/gif" });
    const animImg: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 400, h: 400 }, release: vi.fn(), animated: true, file: animBlob, bytes: animBytes, url: "blob:anim-working" };
    let reads = 0;
    const deps: LookUploadDeps = {
      read: vi.fn(async () => (++reads === 1 ? mainImg : animImg)),
      render: vi.fn(() => ({ image: "U1RJTEw=", mime: "image/png", side: 600 })),
      extractColor: vi.fn(() => "#D4683F"),
      send: vi.fn(async (body: UploadBody) => {
        sent.push(body as ExtendedUploadBody);
        return { look: { id: "u-2", source: "hub", name: "Nova", app_icon: "persona", uploaded: true }, current: { id: "u-2", effective: "u-2" } } as UploadAnswer;
      }),
    };
    const onClose = vi.fn();
    const onUploaded = vi.fn();
    act(() => root.render(<LookUploadDialog open supportsStates onClose={onClose} onUploaded={onUploaded} deps={deps} />));

    // Secondary entries start collapsed
    const colorToggle = dialog().querySelector<HTMLButtonElement>('[data-testid="look-color-toggle"]')!;
    const statesToggle = dialog().querySelector<HTMLButtonElement>('[data-testid="look-states-toggle"]')!;
    expect(colorToggle.getAttribute("aria-expanded")).toBe("false");
    expect(statesToggle.getAttribute("aria-expanded")).toBe("false");

    await pick(png());
    type(field(t("assistant-lookupload.name")), "Nova");
    // Automatic swatch estimated from the crop
    expect(dialog().querySelector('[data-testid="look-color-summary-swatch"]')?.getAttribute("data-color")).toBe("#D4683F");

    // Expand Theme color and switch to Choose color
    act(() => colorToggle.click());
    expect(colorToggle.getAttribute("aria-expanded")).toBe("true");
    const chooseColorRadio = [...dialog().querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === t("assistant-lookupload.color_custom"))!;
    act(() => chooseColorRadio.click());
    const colorInput = dialog().querySelector<HTMLInputElement>('[data-testid="look-color-input"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(colorInput, "#3a7bd5");
      colorInput.dispatchEvent(new Event("change", { bubbles: true }));
    });

    // Expand Pictures for each state and pick an animated file for Working
    act(() => statesToggle.click());
    expect(dialog().querySelectorAll("[data-state-row]")).toHaveLength(5);
    const workingInput = dialog().querySelector<HTMLInputElement>('[data-testid="look-upload-state-file-working"]')!;
    Object.defineProperty(workingInput, "files", { value: [new File(["gif"], "work.gif", { type: "image/gif" })], configurable: true });
    await act(async () => workingInput.dispatchEvent(new Event("change", { bubbles: true })));

    // State crop sub-view plays animated preview and shows Status mark switch (on by default)
    expect(dialog().querySelector('[data-testid="look-upload-animated"]')).not.toBeNull();
    const markInCrop = dialog().querySelector<HTMLButtonElement>('[role="switch"]')!;
    expect(markInCrop.getAttribute("aria-checked")).toBe("true");
    act(() => markInCrop.click()); // turn mark off
    expect(markInCrop.getAttribute("aria-checked")).toBe("false");
    act(() => button(t("assistant-lookupload.apply_crop")).click());

    // Back on main form: working state shows playing preview and switch off
    const workingRow = dialog().querySelector('[data-state-row="working"]')!;
    expect(workingRow.querySelector('[data-state-preview="working"]')?.getAttribute("data-playing")).toBe("true");
    expect(workingRow.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("false");
    // Idle never has a Status mark switch even if customized
    const idleRow = dialog().querySelector('[data-state-row="idle"]')!;
    expect(idleRow.querySelector('[role="switch"]')).toBeNull();

    // Upload sends original animated bytes + crop + mark: false + chosen color
    await act(async () => uploadButton().click());
    await act(async () => vi.advanceTimersByTime(50));
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({
      name: "Nova",
      image: "U1RJTEw=",
      color: "#3A7BD5",
      choose: true,
      states: {
        working: { crop: { x: 0, y: 0, side: 400 }, mark: false },
      },
    });
    expect(sent[0].states!.working!.image).toBe(btoa(String.fromCharCode(1, 2, 3, 4)));
  });

  it("stops animated preview playback under prefers-reduced-motion", async () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }));
    const animImg: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 200, h: 200 }, release: vi.fn(), animated: true, file: new Blob(["x"]), url: "blob:anim" };
    const deps: LookUploadDeps = { read: vi.fn(async () => animImg), extractColor: vi.fn(() => "#27747F") };
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={deps} />));
    await pick(new File(["x"], "loop.gif", { type: "image/gif" }));
    expect(dialog().querySelector('[data-testid="look-upload-animated"]')).toBeNull();
    expect(dialog().querySelector('[data-testid="look-upload-crop"] canvas')).not.toBeNull();
  });

  it("opens filled from the package in Edit mode and PATCHes only what changed", async () => {
    const patches: { id: string; body: PatchLookBody }[] = [];
    const onEdited = vi.fn();
    const onClose = vi.fn();
    const editLook: PersonaInfo = {
      id: "u-demo0001",
      rev: "r1",
      manifest: {
        format: 1,
        id: "u-demo0001",
        name: "Doudou",
        subtitle: "Warm",
        avatar: { sizes: { "128": "avatar/128.png" } },
        dot: {
          size: 144,
          states: {
            idle: { file: "dot/idle.png", frames: 1 },
            thinking: { file: "dot/thinking.png", frames: 1 },
            working: { file: "dot/working.png", frames: 12 },
            needs_you: { file: "dot/needs_you.png", frames: 1 },
            offline: { file: "dot/offline.png", frames: 1 },
          },
        },
        colors: { light: { primary: "#3A7BD5", accent: "#2357A5" }, dark: { primary: "#3A7BD5", accent: "#6BA0E8" } },
        source: { kind: "upload", color: { auto: false, seed: "#3A7BD5" }, states: { working: { mark: true } } },
      },
    };
    const deps: LookUploadDeps = {
      patch: vi.fn(async (id: string, body: PatchLookBody) => {
        patches.push({ id, body });
        return { look: { id, source: "hub", name: "Doudou", app_icon: "persona", rev: "r2", uploaded: true, manifest: editLook.manifest } } as PatchAnswer;
      }),
    };
    act(() => root.render(<LookUploadDialog open supportsStates edit={editLook} onClose={onClose} onUploaded={vi.fn()} onEdited={onEdited} deps={deps} />));

    const saveBtn = button(t("common.save"));
    expect(saveBtn.disabled).toBe(true); // nothing changed yet
    expect(field(t("assistant-lookupload.name")).value).toBe("Doudou");
    expect(dialog().querySelector('[data-state-preview="working"]')?.getAttribute("data-playing")).toBe("true");

    // Toggle working's status mark off and switch color back to Automatic
    const workingSwitch = dialog().querySelector<HTMLButtonElement>('[data-state-row="working"] [role="switch"]')!;
    expect(workingSwitch.getAttribute("aria-checked")).toBe("true");
    act(() => workingSwitch.click());
    const autoColorRadio = [...dialog().querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === t("assistant-lookupload.auto"))!;
    act(() => autoColorRadio.click());
    expect(saveBtn.disabled).toBe(false);

    await act(async () => saveBtn.click());
    await act(async () => vi.advanceTimersByTime(50));
    expect(patches).toEqual([{ id: "u-demo0001", body: { color: "auto", states: { working: { mark: false } } } }]);
    expect(onEdited).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  function dispatchDrag(el: Element, type: "dragenter" | "dragover" | "dragleave" | "drop", files: File[] = [png()], types: string[] = ["Files"]) {
    const ev = new Event(type, { bubbles: true, cancelable: true });
    const dt = { types, files, dropEffect: "" };
    Object.defineProperty(ev, "dataTransfer", { value: dt, configurable: true });
    act(() => { el.dispatchEvent(ev); });
    return { ev, dt };
  }

  async function dispatchDrop(el: Element, files: File[] = [png()], types: string[] = ["Files"]) {
    const ev = new Event("drop", { bubbles: true, cancelable: true });
    const dt = { types, files, dropEffect: "" };
    Object.defineProperty(ev, "dataTransfer", { value: dt, configurable: true });
    await act(async () => { el.dispatchEvent(ev); });
    return { ev, dt };
  }

  const editFixture = (): PersonaInfo => ({
    id: "u-demo0001",
    rev: "r1",
    manifest: {
      format: 1,
      id: "u-demo0001",
      name: "Doudou",
      subtitle: "Warm",
      avatar: { sizes: { "128": "avatar/128.png" } },
      dot: {
        size: 144,
        states: {
          idle: { file: "dot/idle.png", frames: 1 },
          thinking: { file: "dot/thinking.png", frames: 1 },
          working: { file: "dot/working.png", frames: 1 },
          needs_you: { file: "dot/needs_you.png", frames: 1 },
          offline: { file: "dot/offline.png", frames: 1 },
        },
      },
      source: { kind: "upload", color: { auto: true }, states: {} },
    },
  });

  it("highlights and drops onto the main slot in upload and edit dialogs, and keeps Keep current working", async () => {
    const read = vi.fn(async () => image(600, 600));
    const deps: LookUploadDeps = { read, extractColor: vi.fn(() => "#27747F") };
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={deps} />));

    const dropZone = dialog().querySelector('[data-testid="look-upload-drop"]')!;
    const { dt } = dispatchDrag(dropZone, "dragenter");
    expect(dropZone.getAttribute("data-drop-over")).toBe("true");
    expect(dropZone.className).toContain("border-accent");
    expect(dropZone.className).toContain("bg-accent-tint");
    // Child enter/leave does not drop the highlight while still inside
    dispatchDrag(dropZone.firstElementChild!, "dragenter");
    dispatchDrag(dropZone, "dragleave");
    expect(dropZone.getAttribute("data-drop-over")).toBe("true");
    dispatchDrag(dropZone, "dragover");
    expect(dt.dropEffect === "" || dt.dropEffect === "copy").toBe(true);

    await dispatchDrop(dropZone, [png()]);
    expect(read).toHaveBeenCalledTimes(1);
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).not.toBeNull();

    // Edit dialog: drag-over highlights preview, drop opens crop with Keep current
    act(() => root.render(<LookUploadDialog key="edit" open supportsStates edit={editFixture()} onClose={vi.fn()} onUploaded={vi.fn()} deps={deps} />));
    const preview = dialog().querySelector('[data-testid="look-edit-preview"]')!;
    dispatchDrag(preview, "dragover");
    expect(preview.getAttribute("data-drop-over")).toBe("true");
    expect(preview.className).toContain("border-accent");
    await dispatchDrop(preview, [png()]);
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).not.toBeNull();
    const keepBtn = button(t("assistant-lookupload.keep_current"));
    expect(keepBtn).toBeDefined();
    act(() => keepBtn.click());
    expect(dialog().querySelector('[data-testid="look-edit-preview"]')).not.toBeNull();
  });

  for (const mode of ["upload", "edit"] as const) {
    it(`drops onto each of the 5 state rows independently in ${mode} mode and labels each Choose/Change button`, async () => {
      for (const st of PERSONA_STATES) {
        const deps: LookUploadDeps = {
          read: vi.fn(async () => image(400, 400)),
          extractColor: vi.fn(() => "#3A7BD5"),
        };
        act(() => root.render(
          <LookUploadDialog
            key={`${mode}-${st.id}`}
            open
            supportsStates
            edit={mode === "edit" ? editFixture() : undefined}
            onClose={vi.fn()}
            onUploaded={vi.fn()}
            deps={deps}
          />,
        ));
        if (mode === "upload") {
          act(() => dialog().querySelector<HTMLButtonElement>('[data-testid="look-states-toggle"]')!.click());
        }
        const row = dialog().querySelector(`[data-state-row="${st.id}"]`)!;
        // Choose button has an accessible name naming the state
        const chooseBtn = row.querySelector<HTMLButtonElement>(`button[aria-label="${t("assistant-lookupload.state_choose_label", { state: st.title })}"]`);
        expect(chooseBtn).not.toBeNull();

        // Drag-over highlights ONLY this state row
        dispatchDrag(row, "dragover");
        expect(row.getAttribute("data-drop-over")).toBe("true");
        expect(row.className).toContain("border-accent");
        expect(row.className).toContain("bg-accent-tint");
        for (const other of PERSONA_STATES) {
          if (other.id === st.id) continue;
          expect(dialog().querySelector(`[data-state-row="${other.id}"]`)!.getAttribute("data-drop-over")).toBeNull();
        }
        expect(dialog().querySelector('[data-testid="look-upload-drop"]')?.getAttribute("data-drop-over") ?? null).toBeNull();
        expect(dialog().querySelector('[data-testid="look-edit-preview"]')?.getAttribute("data-drop-over") ?? null).toBeNull();

        // Drop opens that state's crop sub-view with the right title
        await dispatchDrop(row, [new File(["img"], `${st.id}.png`, { type: "image/png" })]);
        expect(dialog().querySelector("h2")?.textContent).toBe(t("assistant-lookupload.crop_state_title", { state: st.title }));

        // Apply sets ONLY that state row to Custom and updates its button label to Change
        act(() => button(t("assistant-lookupload.apply_crop")).click());
        for (const other of PERSONA_STATES) {
          const r = dialog().querySelector(`[data-state-row="${other.id}"]`)!;
          if (other.id === st.id) {
            expect(r.textContent).toContain(t("assistant-lookupload.state_custom"));
            expect(r.querySelector(`button[aria-label="${t("assistant-lookupload.state_change_label", { state: st.title })}"]`)).not.toBeNull();
          } else {
            expect(r.textContent).not.toContain(t("assistant-lookupload.state_custom"));
          }
        }
      }
    });
  }

  it("refuses an unsupported file dropped on a state row and keeps animated GIF frames when dropped on a state", async () => {
    const animBytes = new Uint8Array([71, 73, 70, 56]);
    const animImg: PickedUploadImage = {
      source: {} as CanvasImageSource,
      size: { w: 300, h: 300 },
      release: vi.fn(),
      animated: true,
      file: new Blob([animBytes], { type: "image/gif" }),
      bytes: animBytes,
      url: "blob:anim-drop",
    };
    const deps: LookUploadDeps = { read: vi.fn(async () => animImg), extractColor: vi.fn(() => "#D4683F") };
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={deps} />));
    act(() => dialog().querySelector<HTMLButtonElement>('[data-testid="look-states-toggle"]')!.click());

    const thinkingRow = dialog().querySelector('[data-state-row="thinking"]')!;
    await dispatchDrop(thinkingRow, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
    expect(alertText()).toBe(t("assistant-lookupload.not_image"));
    expect(deps.read).not.toHaveBeenCalled();

    // Drop an animated GIF on Thinking
    await dispatchDrop(thinkingRow, [new File([animBytes], "think.gif", { type: "image/gif" })]);
    expect(dialog().querySelector('[data-testid="look-upload-animated"]')).not.toBeNull();
    act(() => button(t("assistant-lookupload.apply_crop")).click());
    expect(dialog().querySelector('[data-state-preview="thinking"]')?.getAttribute("data-playing")).toBe("true");
  });

  it("replaces a state's picture when dropped onto its crop sub-view and releases the previous unapplied image", async () => {
    const first: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 400, h: 400 }, release: vi.fn() };
    const second: PickedUploadImage = { source: {} as CanvasImageSource, size: { w: 500, h: 500 }, release: vi.fn(), animated: true, url: "blob:second" };
    let n = 0;
    const deps: LookUploadDeps = { read: vi.fn(async () => (++n === 1 ? first : second)), extractColor: vi.fn(() => "#3A7BD5") };
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={deps} />));
    act(() => dialog().querySelector<HTMLButtonElement>('[data-testid="look-states-toggle"]')!.click());

    await dispatchDrop(dialog().querySelector('[data-state-row="working"]')!, [png()]);
    const cropBox = dialog().querySelector('[data-testid="look-upload-crop"]')!;
    expect(dialog().querySelector('[data-testid="look-upload-animated"]')).toBeNull();

    // Drag over shows accent ring on crop sub-view
    dispatchDrag(cropBox, "dragover");
    expect(cropBox.getAttribute("data-drop-over")).toBe("true");
    expect(cropBox.className).toContain("shadow-[0_0_0_2px_var(--accent)]");

    // Near-miss drop inside the crop sub-view does nothing and is defaultPrevented
    const nearMissSub = await dispatchDrop(dialog().querySelector('[data-dialog-body]')!.firstElementChild!, [png()]);
    expect(nearMissSub.ev.defaultPrevented).toBe(true);
    expect(deps.read).toHaveBeenCalledTimes(1);

    // Drop onto CropBox replaces the picture and releases the first
    await dispatchDrop(cropBox, [new File(["gif"], "replace.gif", { type: "image/gif" })]);
    expect(first.release).toHaveBeenCalledTimes(1);
    expect(dialog().querySelector('[data-testid="look-upload-animated"]')).not.toBeNull();

    // Unsupported file dropped onto CropBox shows error while staying in crop sub-view
    await dispatchDrop(dialog().querySelector('[data-testid="look-upload-crop"]')!, [new File(["x"], "bad.txt", { type: "text/plain" })]);
    expect(alertText()).toBe(t("assistant-lookupload.not_image"));
  });

  it("swallows near-miss drags and drops on the main dialog content without opening or picking anything", async () => {
    const read = vi.fn(async () => image(600, 600));
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read }} />));
    const contentRoot = dialog().querySelector('[data-dialog-body]')!.firstElementChild!;
    const over = dispatchDrag(contentRoot, "dragover");
    expect(over.ev.defaultPrevented).toBe(true);
    expect(over.dt.dropEffect).toBe("none");
    const drop = await dispatchDrop(contentRoot, [png()]);
    expect(drop.ev.defaultPrevented).toBe(true);
    expect(read).not.toHaveBeenCalled();
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).toBeNull();
  });

  it("swallows a near-miss drop on the footer or outside the dialog while open, and stops once closed", async () => {
    const read = vi.fn(async () => image(600, 600));
    act(() => root.render(<LookUploadDialog open supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read }} />));
    const footer = dialog().querySelector("[data-dialog-footer]")!;
    const over = dispatchDrag(footer, "dragover");
    expect(over.ev.defaultPrevented).toBe(true);
    expect(over.dt.dropEffect).toBe("none");
    expect((await dispatchDrop(footer)).ev.defaultPrevented).toBe(true);
    expect((await dispatchDrop(document.body)).ev.defaultPrevented).toBe(true);
    expect(read).not.toHaveBeenCalled();
    act(() => root.render(<LookUploadDialog open={false} supportsStates onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read }} />));
    expect((await dispatchDrop(document.body)).ev.defaultPrevented).toBe(false);
  });

  it("picks initialFile once when opened in upload or edit mode", async () => {
    const read = vi.fn(async () => image(600, 600));
    const file = png();
    await act(async () => root.render(<LookUploadDialog open supportsStates initialFile={file} onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read, extractColor: vi.fn(() => "#27747F") }} />));
    expect(read).toHaveBeenCalledTimes(1);
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).not.toBeNull();

    // Rerendering with the same initialFile does not pick again
    await act(async () => root.render(<LookUploadDialog open supportsStates initialFile={file} onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read, extractColor: vi.fn(() => "#27747F") }} />));
    expect(read).toHaveBeenCalledTimes(1);

    // Under React StrictMode (dev double-mount effect), initialFile still completes and shows the crop box
    const strictRead = vi.fn(async () => image(600, 600));
    await act(async () => root.render(
      <StrictMode>
        <LookUploadDialog key="strict" open supportsStates initialFile={png()} onClose={vi.fn()} onUploaded={vi.fn()} deps={{ read: strictRead, extractColor: vi.fn(() => "#27747F") }} />
      </StrictMode>,
    ));
    expect(dialog().querySelector('[data-testid="look-upload-crop"]')).not.toBeNull();
  });
});
