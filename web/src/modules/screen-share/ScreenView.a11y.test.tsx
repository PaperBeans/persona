// @vitest-environment jsdom
// The remote screen's sign-in form: its fields have names a screen reader reads, and a refused sign-in is announced.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";

const rfbs = vi.hoisted(() => [] as EventTarget[]);
vi.mock("@novnc/novnc", () => ({
  default: class extends EventTarget {
    viewOnly = true;
    constructor() {
      super();
      rfbs.push(this);
    }
    sendCredentials() {}
    disconnect() {}
  },
}));
vi.mock("../../views/theme", () => ({})); // follows the system theme through matchMedia, which jsdom has none of
vi.mock("./aes", () => ({ ensureSubtleAES: () => {} }));
vi.mock("../../lib/api", async (actual) => ({
  ...(await actual<typeof import("../../lib/api")>()),
  api: async (path: string) =>
    path === "/machines" ? { machines: [{ id: "mac", name: "Mac", online: true }] } : { enabled: true, screen_sharing: true },
}));
import { ScreenView } from "./ScreenView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const fire = (type: string, detail?: unknown) => act(() => void rfbs.at(-1)!.dispatchEvent(new CustomEvent(type, { detail })));

beforeEach(() => {
  rfbs.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("ScreenView sign-in", () => {
  it("names its fields and announces a refused sign-in", async () => {
    act(() => root.render(<ScreenView target={{ mid: "mac", token: "tok", embedded: false, name: "Mac", keep: "", unlock: false }} />));
    await flush();
    expect(rfbs).toHaveLength(1);
    fire("credentialsrequired");

    const account = host.querySelector<HTMLInputElement>('input[autocomplete="username"]')!;
    const password = host.querySelector<HTMLInputElement>('input[type="password"]')!;
    expect(account.getAttribute("aria-label")).toBe(t("screen-share-screenview.account"));
    expect(password.getAttribute("aria-label")).toBe(t("screen-share-screenview.password"));
    expect(host.querySelector('[role="alert"]')).toBeNull();

    act(() => host.querySelector("form")!.requestSubmit());
    fire("securityfailure", { reason: "nope" });
    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe(t("screen-share-screenview.sign_in_failed", { reason: "nope" }));
  });
});
