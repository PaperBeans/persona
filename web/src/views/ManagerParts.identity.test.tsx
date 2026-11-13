// @vitest-environment jsdom
// Conversation chrome keeps its multi-assistant gate even though the sidebar shows identity whenever a roster exists.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { chooseManager, resetManagerChoice } from "../lib/manager-choice";
import { resetRoster, setRoster, type ManagerList } from "../lib/managers";
import { useAssistantTitle, useManagerOffline, useShownOffline } from "./ManagerParts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const DEFAULT = { id: "assistant", name: "Only assistant", online: true, default: true, builtin: true };
const OTHER = { id: "m-other", name: "Second assistant", online: false };
let root: Root;
let host: HTMLDivElement;
function Chrome() {
  const title = useAssistantTitle();
  const offline = useShownOffline();
  const composer = useManagerOffline("assistant");
  return <><h1>{title}</h1><span>{offline?.title}</span><span>{composer}</span></>;
}
function render(roster: ManagerList) {
  act(() => { setRoster(roster); root.render(<Chrome />); });
}
beforeEach(() => {
  resetManagerChoice();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetRoster();
  resetManagerChoice();
});
describe("assistant conversation identity", () => {
  it("uses the earlier product title with one assistant, including offline, with no personal identity", () => {
    render({ default: "assistant", managers: [DEFAULT] });
    expect(host.textContent).toBe("Persona");
    render({ default: "assistant", managers: [{ ...DEFAULT, online: false }] });
    expect(host.textContent).toBe("Persona");
  });
  it("names the current assistant with multiple assistants and updates on a switch", () => {
    render({ default: "assistant", managers: [DEFAULT, OTHER] });
    expect(host.querySelector("h1")?.textContent).toBe(DEFAULT.name);
    act(() => chooseManager(OTHER.id));
    expect(host.querySelector("h1")?.textContent).toBe(OTHER.name);
    expect(host.textContent).toContain(OTHER.name);
    act(() => setRoster({ default: "assistant", managers: [DEFAULT] }));
    expect(host.textContent).toBe("Persona");
  });
  it("keeps the product title while the only additional assistant is still installing", () => {
    render({ default: "assistant", managers: [DEFAULT, { ...OTHER, installing: new Date(Date.now() + 60_000).toISOString() }] });
    expect(host.textContent).toBe("Persona");
  });
});
