// @vitest-environment jsdom
// /lark-oauth: the platform's code goes to the hub once, with the manager the authorization began on, the address
// loses it at once, and the page says how it went and leads back to where the owner started.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const finish = vi.hoisted(() => vi.fn());
vi.mock("../../lib/lark-sync", async (original) => ({
  ...(await original<typeof import("../../lib/lark-sync")>()),
  finishSyncOAuth: (...a: unknown[]) => finish(...a),
}));
import { t } from "../../i18n";
import { readReturn, rememberReturn } from "../../lib/lark-sync";
import { LarkOAuthReturn } from "./LarkOAuthReturn";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const text = () => host.textContent ?? "";
const at = (address: string) => history.replaceState(null, "", address);
const draw = () => act(async () => root.render(<LarkOAuthReturn onToggleSidebar={() => {}} />));

beforeEach(() => {
  finish.mockReset();
  sessionStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  at("/");
});

describe("Lark authorization return page", () => {
  it("hands the code and state to the hub once, then says it is connected", async () => {
    rememberReturn("/settings?tab=integrations&integration=builtin:feishu-bot", "assistant-2");
    at("/lark-oauth?code=c-1&state=s-1");
    finish.mockResolvedValue(null);
    await draw();
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith("s-1", "c-1", "assistant-2");
    // the single-use code is off the address bar
    expect(location.search).toBe("");
    expect(location.pathname).toBe("/lark-oauth");
    expect(text()).toContain(t("settings-larksync.return_done_title"));
    // through: the kept return is dropped
    expect(readReturn().manager).toBeUndefined();

    // drawn again (a remount): the same state is not handed over a second time
    act(() => root.unmount());
    root = createRoot(host);
    at("/lark-oauth?code=c-1&state=s-1");
    await draw();
    expect(finish).toHaveBeenCalledTimes(1);

    await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === t("settings-larksync.return_back"))!.click());
    expect(location.pathname + location.search).toBe("/settings?tab=connectors&connector=feishu-bot");
  });

  it("says what went wrong when the hub refuses the code", async () => {
    at("/lark-oauth?code=c-2&state=s-2");
    finish.mockRejectedValue(new Error("the authorization expired"));
    await draw();
    expect(text()).toContain(t("settings-larksync.return_failed_title"));
    expect(text()).toContain("the authorization expired");
  });

  it("hands nothing over when the owner declined, or when there is nothing to finish", async () => {
    at("/lark-oauth?error=access_denied&state=s-3");
    await draw();
    expect(finish).not.toHaveBeenCalled();
    expect(text()).toContain(t("settings-larksync.return_declined_title"));

    act(() => root.unmount());
    root = createRoot(host);
    at("/lark-oauth");
    await draw();
    expect(finish).not.toHaveBeenCalled();
    expect(text()).toContain(t("settings-larksync.return_missing_title"));
  });
});
