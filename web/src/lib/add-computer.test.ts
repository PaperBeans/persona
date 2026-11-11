import { describe, expect, it } from "vitest";
import {
  addBody, addedUnder, addErrorText, addStage, canAddComputer, canConfirmComputer, confirmStage, linuxCommand, machineErrorText, neverConnected, pairCommand, pendingToConfirm, windowsCommand,
} from "./add-computer";
import { ApiError } from "./api";
import type { Machine } from "./types";

const PAIR = "persona://link?url=https%3A%2F%2Fhub.example.com&token=jp_x&machine=mac";
const m = (over: Partial<Machine> = {}): Machine => ({ id: "mac", name: "我的 MacBook", online: false, ...over });

describe("canAddComputer", () => {
  it("needs the hub to say machines_add; an old hub, or one that said nothing, has no 添加电脑", () => {
    expect(canAddComputer(["machine_trust", "machines_add"])).toBe(true);
    expect(canAddComputer(["machine_trust"])).toBe(false);
    expect(canAddComputer([])).toBe(false);
  });
});

describe("addBody", () => {
  it("sends the name as typed, trimmed, and the system", () => {
    expect(addBody("  小林的 MacBook ", "mac")).toEqual({ add: { name: "小林的 MacBook", os: "mac" } });
  });

  it("names the assistant the computer is for, the default's real id too; none leaves it to the hub", () => {
    expect(addBody("Work Mac", "mac", "m-work")).toEqual({ add: { name: "Work Mac", os: "mac", manager: "m-work" } });
    expect(addBody("Home Mac", "mac", "assistant")).toEqual({ add: { name: "Home Mac", os: "mac", manager: "assistant" } });
    expect(addBody("Mac", "mac", undefined)).toEqual({ add: { name: "Mac", os: "mac" } });
  });
});

describe("addedUnder", () => {
  it("says nothing when the computer went where it was asked, or nothing was asked", () => {
    expect(addedUnder("m-work", { manager: "m-work", added: true })).toBeNull();
    expect(addedUnder(undefined, { manager: "m-work", added: true })).toBeNull();
    expect(addedUnder(undefined, { added: true })).toBeNull();
  });

  it("names the manager the reply says when it is another", () => {
    expect(addedUnder("m-work", { manager: "assistant", added: false })).toBe("assistant");
  });

  it("takes a hub without add.manager's new computer as its own assistant's, a reused one as where the list has it", () => {
    expect(addedUnder("m-work", { added: true })).toBe("assistant");
    expect(addedUnder("assistant", { added: true })).toBeNull();
    expect(addedUnder("m-work", {}, "m-home")).toBe("m-home");
    expect(addedUnder("m-work", {}, "m-work")).toBeNull();
    expect(addedUnder("m-work", {})).toBe("assistant");
  });
});

describe("commands", () => {
  it("uses the hub's one install line on Linux, else only the pairing", () => {
    expect(linuxCommand({ pair_url: PAIR, install: { linux: "curl -fsSL https://example.com/install.sh | sh" } })).toEqual({
      line: "curl -fsSL https://example.com/install.sh | sh",
      installs: true,
    });
    expect(linuxCommand({ pair_url: PAIR, command: `persona-link pair '${PAIR}'` })).toEqual({ line: `persona-link pair '${PAIR}'`, installs: false });
  });

  it("builds the pairing line when the hub gave none, and keeps a quote out of the quotes", () => {
    expect(pairCommand({ pair_url: PAIR })).toBe(`persona-link pair '${PAIR}'`);
    expect(pairCommand({ pair_url: "persona://link?x='y" })).toBe("persona-link pair 'persona://link?x=%27y'");
  });

  it("runs persona-link install with personad beside it on Windows (docs/operations/windows.md)", () => {
    expect(windowsCommand(PAIR)).toBe(`.\\persona-link.exe install --personad .\\personad.exe --pair '${PAIR}'`);
  });
});

describe("addStage", () => {
  it("waits while the code lasts, then has expired", () => {
    expect(addStage(m({ trust: "pending" }), 30, true)).toBe("waiting");
    expect(addStage(m({ trust: "pending" }), 0, true)).toBe("expired");
  });

  it("asks once the computer is online and still pending; is done once it is confirmed", () => {
    expect(addStage(m({ online: true, trust: "pending" }), 30, true)).toBe("joined");
    expect(addStage(m({ online: true }), 30, true)).toBe("connected");
  });

  it("keeps waiting for one the list has not had yet, and says gone for one it had", () => {
    expect(addStage(undefined, 30, false)).toBe("waiting");
    expect(addStage(undefined, 0, false)).toBe("expired");
    expect(addStage(undefined, 30, true)).toBe("gone");
  });
});

describe("words", () => {
  it("says each machine error_code in words, nothing for one it does not know", () => {
    for (const code of ["node_service_down", "link_offline", "never_connected", "node_outdated"]) {
      expect(machineErrorText(code)).toMatch(/[一-鿿]/);
      expect(machineErrorText(code)).not.toContain("lib-add-computer.");
    }
    expect(machineErrorText("something_new")).toBe("");
    expect(machineErrorText(undefined)).toBe("");
  });

  it("explains the codes of a refused add, and passes the hub's words for the rest", () => {
    const err = (code: string, message = "hub said so") => new ApiError(409, message, { code, error: message });
    expect(addErrorText(err("tls_required"))).toContain("安全连接");
    expect(addErrorText(err("mesh_member"))).toContain("中心 hub");
    expect(addErrorText(err("too_many_pending"))).toContain("3 台");
    expect(addErrorText(err("exists", "这个 id 已经有了"))).toBe("这个 id 已经有了");
  });

  it("shows the hub's own words for add.manager's refusals, not the generic words for the status", () => {
    const refused = (status: number, code: string, error: string) => new ApiError(status, error, { code, error });
    expect(addErrorText(refused(404, "no_manager", "That assistant isn't on this hub"))).toBe("That assistant isn't on this hub");
    expect(addErrorText(refused(409, "installing", "That assistant is still being installed"))).toBe("That assistant is still being installed");
    expect(addErrorText(refused(503, "assistant_offline", "The computer's assistant is offline"))).toBe("The computer's assistant is offline");
    expect(addErrorText(refused(503, "owners_unknown", "Try again in a moment"))).toBe("Try again in a moment");
    // no words from the hub: the generic ones
    expect(addErrorText(new ApiError(503, "HTTP 503", { code: "owners_unknown" }))).not.toBe("");
    expect(addErrorText(new ApiError(503, "HTTP 503", { code: "owners_unknown" }))).not.toBe("HTTP 503");
  });
});

describe("confirming a computer waiting to be confirmed", () => {
  it("needs the hub to say machine_trust", () => {
    expect(canConfirmComputer(["machine_trust"])).toBe(true);
    expect(canConfirmComputer([])).toBe(false);
  });

  it("asks only once it is online, and follows a confirm or a removal made elsewhere", () => {
    expect(confirmStage(m({ trust: "pending", online: true }))).toBe("joined");
    expect(confirmStage(m({ trust: "pending" }))).toBe("offline");
    expect(confirmStage(m({ online: true }))).toBe("connected");
    expect(confirmStage(undefined)).toBe("gone");
  });

  it("opens the step's computer, else the first one waiting that is online, else the first one waiting", () => {
    const a = m({ id: "a", trust: "pending" });
    const b = m({ id: "b", trust: "pending", online: true });
    const c = m({ id: "c", online: true });
    expect(pendingToConfirm([a, b, c], "a")?.id).toBe("a");
    expect(pendingToConfirm([a, b, c], "c")?.id).toBe("b");
    expect(pendingToConfirm([a, b, c])?.id).toBe("b");
    expect(pendingToConfirm([a, c])?.id).toBe("a");
    expect(pendingToConfirm([c])).toBeUndefined();
  });
});

describe("neverConnected", () => {
  it("is a computer that has not come online once: the hub's never_connected, or one waiting with no last_seen", () => {
    expect(neverConnected(m({ error_code: "never_connected" }))).toBe(true);
    expect(neverConnected(m({ trust: "pending" }))).toBe(true);
    expect(neverConnected(m({ trust: "pending", last_seen: "2026-11-06T09:00:00Z" }))).toBe(false);
    expect(neverConnected(m({ error_code: "link_offline", last_seen: "2026-11-06T09:00:00Z" }))).toBe(false);
    expect(neverConnected(m({ online: true, trust: "pending" }))).toBe(false);
  });
});
