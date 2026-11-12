// @vitest-environment jsdom
// Settings › Agent preferences › "How Agents open" (#5424): the side panel by default, Full page on request, kept by
// this browser.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { t } from "../../i18n";
import { agentOpenMode, setAgentOpenMode } from "../../lib/agent-open";
import { AgentOpenRow } from "./AgentOpenSetting";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<AgentOpenRow />));
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setAgentOpenMode("panel");
});

const radio = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === label)!;

describe("How Agents open", () => {
  it("shows the side panel chosen on a new browser, with its plain label and hint", () => {
    expect(host.textContent).toContain(t("settings-agentopen.label"));
    expect(host.textContent).toContain(t("settings-agentopen.hint"));
    expect(host.querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe(t("settings-agentopen.label"));
    expect(radio(t("settings-agentopen.panel")).getAttribute("aria-checked")).toBe("true");
    expect(radio(t("settings-agentopen.page")).getAttribute("aria-checked")).toBe("false");
    expect(agentOpenMode()).toBe("panel");
  });

  it("switches to Full page and back, kept by this browser", () => {
    act(() => radio(t("settings-agentopen.page")).click());
    expect(agentOpenMode()).toBe("page");
    expect(localStorage.getItem("agent.open")).toBe("page");
    expect(radio(t("settings-agentopen.page")).getAttribute("aria-checked")).toBe("true");
    act(() => radio(t("settings-agentopen.panel")).click());
    expect(agentOpenMode()).toBe("panel");
    expect(localStorage.getItem("agent.open")).toBeNull();
  });

  it("follows a change made in another tab", () => {
    act(() => {
      localStorage.setItem("agent.open", "page");
      window.dispatchEvent(new StorageEvent("storage", { key: "agent.open" }));
    });
    expect(radio(t("settings-agentopen.page")).getAttribute("aria-checked")).toBe("true");
  });
});
