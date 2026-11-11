// @vitest-environment jsdom
// General › Translation shows the default assistant's translation model. Its row opens Models, which edits the current
// assistant's: with another assistant current the row goes nowhere and says whose model it is.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { t } from "../../i18n";
import { chooseManager, resetManagerChoice } from "../../lib/manager-choice";
import { setRoster } from "../../lib/managers";
import { TranslationSettings } from "./TranslationSettings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ROSTER = {
  default: "assistant",
  managers: [
    { id: "assistant", name: "助手", online: true, builtin: true },
    { id: "m-1", name: "工作", online: true },
  ],
};
let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const modelRow = () => [...host.querySelectorAll<HTMLElement>("button, [role=button]")].find((b) => b.textContent?.includes(t("settings-translationsettings.model")));

beforeEach(() => {
  resetManagerChoice();
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, text: async () => JSON.stringify({ purpose: "translate", providers: [] }), headers: new Headers() })));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  setRoster(null);
  resetManagerChoice();
});

describe("Translation's model row", () => {
  it("opens Models while the default assistant is the current one, with no note", async () => {
    setRoster(ROSTER);
    const onConfigure = vi.fn();
    act(() => root.render(<TranslationSettings onConfigure={onConfigure} />));
    await flush();
    act(() => modelRow()!.click());
    expect(onConfigure).toHaveBeenCalledTimes(1);
    expect(host.textContent).not.toContain(t("settings-translationsettings.model_default_note", { name: "助手" }));
  });

  it("goes nowhere and names the default assistant when another one is current", async () => {
    setRoster(ROSTER);
    act(() => chooseManager("m-1"));
    const onConfigure = vi.fn();
    act(() => root.render(<TranslationSettings onConfigure={onConfigure} />));
    await flush();
    expect(modelRow()).toBeUndefined();
    expect(host.textContent).toContain(t("settings-translationsettings.model_default_note", { name: "助手" }));
    expect(onConfigure).not.toHaveBeenCalled();
  });

  it("opens Models with one assistant", async () => {
    setRoster({ default: "assistant", managers: [ROSTER.managers[0]] });
    const onConfigure = vi.fn();
    act(() => root.render(<TranslationSettings onConfigure={onConfigure} />));
    await flush();
    act(() => modelRow()!.click());
    expect(onConfigure).toHaveBeenCalledTimes(1);
  });
});
