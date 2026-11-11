// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { Connectors } from "./Connectors";

vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let fetcher: ReturnType<typeof vi.fn>;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const type = (input: HTMLInputElement, value: string) => act(() => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
});
const posts = () => fetcher.mock.calls.filter(([url, init]) => url === "/api/v1/mail/accounts" && init?.method === "POST");

beforeEach(() => {
  fetcher = vi.fn(async () => ({ ok: true, status: 200, text: async () => JSON.stringify({ accounts: [] }) }));
  vi.stubGlobal("fetch", fetcher);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const openMail = async (port: string) => {
  act(() => root.render(<Connectors open={null} onOpen={() => {}} />));
  await flush();
  const add = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-connectors.add_mail"))!;
  expect(add).toBeTruthy();
  act(() => add.click());
  await flush();
  const form = document.querySelector<HTMLFormElement>("[role=dialog] form")!;
  expect(form).toBeTruthy();
  type(form.querySelector<HTMLInputElement>('input[type=email]')!, "me@example.com");
  type(form.querySelector<HTMLInputElement>('input[type=password]')!, "example-app-password");
  type(form.querySelector<HTMLInputElement>('input[placeholder="smtp.example.com"]')!, "smtp.example.com");
  type(form.querySelector<HTMLInputElement>('input[placeholder="465"]')!, port);
  return form;
};

describe("mail account SMTP submission (#4879)", () => {
  it.each([["0587", 587, "starttls"], ["587", 587, "starttls"], ["465", 465, "tls"]])("submits %s with its numeric port and connection mode", async (text, port, security) => {
    const form = await openMail(String(text));
    act(() => form.requestSubmit());
    await flush();
    expect(posts()).toHaveLength(1);
    expect(JSON.parse(posts()[0][1].body)).toEqual({ name: "me@example.com", email: "me@example.com", provider: "imap", auth: "password", password: "example-app-password", smtp_host: "smtp.example.com", smtp_port: port, smtp_security: security });
  });

  it("refuses an invalid port without posting, then submits the corrected value", async () => {
    const form = await openMail("abc");
    const input = form.querySelector<HTMLInputElement>('input[placeholder="465"]')!;
    act(() => form.requestSubmit());
    await flush();
    expect(posts()).toHaveLength(0);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    type(input, "465");
    act(() => form.requestSubmit());
    await flush();
    expect(posts()).toHaveLength(1);
    expect(JSON.parse(posts()[0][1].body)).toMatchObject({ smtp_port: 465, smtp_security: "tls" });
  });
});
