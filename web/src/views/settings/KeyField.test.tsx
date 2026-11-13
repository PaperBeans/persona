// @vitest-environment jsdom
// A model provider's key field (#3962 step 4c): the "Use a reference" switch shows only where the hub takes references;
// on, the field is plain text for a reference, and a stored reference says where its value comes from; the hint the
// caller gives (a computer's own key, its Remove) stays under it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { KeyField } from "./ProviderFields";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});
const render = (node: React.ReactNode) => act(() => root.render(node));
const noop = () => {};
const stored = { provider: "example-vault", provider_name: "Example Vault", reference: "op://Dev/Gateway/key", title: "Gateway › key" };

describe("KeyField", () => {
  it("has no switch where the hub takes no references, and hides a typed key", () => {
    render(<KeyField label="API Key" value="sk-1" onChange={noop} byReference={false} />);
    expect(host.querySelector('[role="switch"]')).toBeNull();
    expect(host.querySelector("input")!.type).toBe("password");
  });

  it("offers the switch on the key's own label row, and turning it on asks for a reference", () => {
    const on = vi.fn();
    render(<KeyField label="API Key" value="" onChange={noop} byReference={false} onByReference={on} />);
    const sw = host.querySelector<HTMLElement>('[role="switch"]')!;
    // in a list of computers' keys, each switch stays with the key it changes
    expect(host.querySelector("label")!.parentElement!.contains(sw)).toBe(true);
    act(() => sw.click());
    expect(on).toHaveBeenCalledWith(true);
  });

  it("takes a reference in plain text and says where a stored one comes from, the caller's hint kept", () => {
    render(<KeyField label="API Key" value="" onChange={noop} byReference onByReference={noop} stored={stored} hint={<button type="button">Remove</button>} />);
    const input = host.querySelector<HTMLInputElement>("[data-key-reference]")!;
    expect(input.type).toBe("text");
    expect(input.placeholder).toBe(stored.reference);
    const text = host.textContent ?? "";
    expect(text).toContain("Example Vault");
    expect(text).toContain("Gateway › key");
    expect([...host.querySelectorAll("button")].some((b) => b.textContent === "Remove")).toBe(true);
  });
});
