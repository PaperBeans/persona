// @vitest-environment jsdom
// Controlled and custom-storage folded reports skip localStorage; default browser persistence stays pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FoldedReport, useReportOpen, type FoldedReportProps, type ReportOpenStorage } from "./FoldedReport";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let getItem: ReturnType<typeof vi.spyOn>;
let setItem: ReturnType<typeof vi.spyOn>;
let removeItem: ReturnType<typeof vi.spyOn>;

const mount = (props: Partial<FoldedReportProps> = {}) => act(async () => {
  root.render(
    <FoldedReport id="r1" text={"## Summary\nFull report"} {...props}>
      <p data-report-body>Full report</p>
    </FoldedReport>,
  );
});
const expectNoStorage = () => {
  expect(getItem).not.toHaveBeenCalled();
  expect(setItem).not.toHaveBeenCalled();
  expect(removeItem).not.toHaveBeenCalled();
};

beforeEach(() => {
  localStorage.clear();
  getItem = vi.spyOn(Storage.prototype, "getItem");
  setItem = vi.spyOn(Storage.prototype, "setItem");
  removeItem = vi.spyOn(Storage.prototype, "removeItem");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.restoreAllMocks(); localStorage.clear(); });

describe("FoldedReport controlled mode", () => {
  it("renders open/folded from props and reports onOpenChange without touching localStorage", async () => {
    const onOpenChange = vi.fn();
    await mount({ open: false, onOpenChange });
    expect(host.querySelector('[data-report="folded"]')).not.toBeNull();
    expect(host.querySelector("[data-report-line]")?.textContent).toContain(t("chat-report.expand"));
    act(() => host.querySelector<HTMLButtonElement>("[data-report-line]")!.click());
    expect(onOpenChange).toHaveBeenCalledWith(true);
    expect(host.querySelector('[data-report="folded"]')).not.toBeNull();

    await mount({ id: "r2", open: true, onOpenChange });
    expect(host.querySelector('[data-report="open"]')).not.toBeNull();
    expect(host.querySelector("[data-report-body]")?.textContent).toBe("Full report");
    act(() => host.querySelector<HTMLButtonElement>("[data-report-collapse]")!.click());
    expect(onOpenChange).toHaveBeenCalledWith(false);

    await mount({ id: "r3", text: " \n ", open: true });
    expect(host.innerHTML).toBe("");
    expectNoStorage();
  });
});

describe("FoldedReport and useReportOpen custom storage", () => {
  it("uses the supplied storage adapter across toggles and id switches without localStorage", async () => {
    const map = new Map<string, boolean>([["b", true]]);
    const storage: ReportOpenStorage = {
      read: vi.fn((id) => map.get(id) === true),
      write: vi.fn((id, open) => { if (open) map.set(id, true); else map.delete(id); }),
    };
    const onOpenChange = vi.fn();
    await mount({ id: "a", storage, onOpenChange });
    act(() => host.querySelector<HTMLButtonElement>("[data-report-line]")!.click());
    expect(storage.write).toHaveBeenCalledWith("a", true);
    expect(onOpenChange).toHaveBeenCalledWith(true);
    expect(host.querySelector('[data-report="open"]')).not.toBeNull();

    await mount({ id: "b", storage, onOpenChange });
    expect(host.querySelector('[data-report="open"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>("[data-report-collapse]")!.click());
    expect(storage.write).toHaveBeenCalledWith("b", false);

    function Probe() {
      const [open, toggle] = useReportOpen("a", storage);
      return <button type="button" data-open={String(open)} onClick={toggle} />;
    }
    await act(async () => root.render(<Probe />));
    expect(host.querySelector("button")?.getAttribute("data-open")).toBe("true");
    act(() => host.querySelector("button")!.click());
    expect(map.has("a")).toBe(false);
    expectNoStorage();
  });
});

describe("FoldedReport default localStorage compatibility", () => {
  it("persists open state per card in localStorage and notifies optional onOpenChange", async () => {
    const onOpenChange = vi.fn();
    await mount({ id: "d1", onOpenChange });
    expect(getItem).toHaveBeenCalledWith("report:d1");
    act(() => host.querySelector<HTMLButtonElement>("[data-report-line]")!.click());
    expect(setItem).toHaveBeenCalledWith("report:d1", "1");
    expect(onOpenChange).toHaveBeenCalledWith(true);

    await mount({ id: "d2", onOpenChange });
    expect(host.querySelector('[data-report="folded"]')).not.toBeNull();

    await mount({ id: "d1", onOpenChange });
    expect(host.querySelector('[data-report="open"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>("[data-report-collapse]")!.click());
    expect(removeItem).toHaveBeenCalledWith("report:d1");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
