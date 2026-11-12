import { describe, expect, it } from "vitest";
import { modeConfirmation, modeMenuItems, pickerIcon } from "./header-mode";
import type { Session } from "./types";

// The Agent header's permission mode (#3956), the rules of iOS SessionModes (#3719, HeaderModeTests) and Android #3957.
const claude = (permission_mode?: string, extra: Partial<Session> = {}): Session =>
  ({ machine_id: "m", session_id: "s", agent: "claude", status: "idle", permission_mode, ...extra }) as Session;

describe("header mode", () => {
  it("draws each mode with the composer picker's symbol (#5324)", () => {
    expect(["default", "acceptEdits", "plan", "auto", "bypassPermissions"].map(pickerIcon)).toEqual(["palm", "code", "list", "sparkles", "warning"]);
    // the picker reads a mode not reported yet as Default permissions, as its label does
    expect(pickerIcon(undefined)).toBe("palm");
  });

  it("asks first into Allow all, and out of it where the agent can't come back", () => {
    expect(modeConfirmation(claude("default", { bypass_available: true }), "bypassPermissions")).toBe("allow");
    expect(modeConfirmation(claude("bypassPermissions"), "plan")).toBe("leave");
    // it can come back: no question
    expect(modeConfirmation(claude("bypassPermissions", { bypass_available: true }), "plan")).toBeNull();
    expect(modeConfirmation(claude("default"), "plan")).toBeNull();
    expect(modeConfirmation(claude("plan"), "plan")).toBeNull();
    // an assistant conversation, and Antigravity, switch without asking
    expect(modeConfirmation(claude("default", { assistant: true }), "bypassPermissions")).toBeNull();
    expect(modeConfirmation(claude("bypassPermissions", { agent: "agy" }), "plan")).toBeNull();
  });

  it("offers Allow all only where the agent can take it, and says why not elsewhere", () => {
    const picked: string[] = [];
    const rows = (s: Session, locked?: boolean) => modeMenuItems(s, (m) => picked.push(m), { locked });
    const without = rows(claude("default"));
    const bypass = without.find((r) => r.key === "bypassPermissions")!;
    expect(bypass.disabled).toBe(true);
    expect(bypass.detail).toBeTruthy();
    expect(without.find((r) => r.key === "default")?.checked).toBe(true);
    const offered = rows(claude("default", { bypass_available: true })).find((r) => r.key === "bypassPermissions")!;
    expect(offered.disabled).toBeFalsy();
    expect(offered.danger && offered.confirm).toBeTruthy();
    // in it with no way back: listed, checked, and every other row asks first
    const inIt = rows(claude("bypassPermissions"));
    expect(inIt.find((r) => r.key === "bypassPermissions")?.checked).toBe(true);
    expect(inIt.filter((r) => r.key !== "bypassPermissions").every((r) => r.confirm)).toBe(true);
    // not running: nothing can be picked, and no reason row for Allow all
    const locked = rows(claude("plan", { status: "offline" }), true);
    expect(locked.every((r) => r.disabled)).toBe(true);
    expect(locked.some((r) => r.key === "bypassPermissions")).toBe(false);
    without.find((r) => r.key === "plan")!.onSelect();
    expect(picked).toEqual(["plan"]);
  });
});
