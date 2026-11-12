// @vitest-environment jsdom
// A model write whose response never arrives cannot leave Apply busy forever. Its result is uncertain, so the optimistic
// pick stays visible for the session push instead of being rolled back as though the hub had refused it.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { pastHub, setToken } from "../../lib/api";
import { reset, getSession, upsertSession } from "../../lib/store";
import type { Session } from "../../lib/types";
import { applyModes } from "./SessionDialogs";

const toast = vi.hoisted(() => vi.fn());
vi.mock("../../ui/atoms", async (load) => ({ ...await load<typeof import("../../ui/atoms")>(), toast }));

beforeEach(async () => {
  await initI18n("en");
  setToken("synthetic-mode-owner");
  reset();
  toast.mockClear();
  vi.useFakeTimers();
  vi.stubGlobal("fetch", vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
  })));
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("rolls back an optimistic pick when the hub explicitly refuses it", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ code: "timeout", error: "Request timed out" }), { status: 502, headers: { "Content-Type": "application/json" } })));
  const session = { machine_id: "m", session_id: "s", title: "Probe", status: "idle", model: "old-model" } as Session;
  upsertSession(session);
  await expect(applyModes(() => session, { model: "new-model" })).resolves.toBe(false);
  expect(getSession("m", "s")?.model).toBe("old-model");
  expect(toast).toHaveBeenCalledWith("Couldn't change: The request timed out. The computer may be busy or the network unstable. Check whether it went through, then try again later.", "error");
  expect(toast).not.toHaveBeenCalledWith(expect.stringContaining("may have been applied"), "error");
});

it("ends an unconfirmed model write with a reason without rolling back a change that may have run", async () => {
  const session = { machine_id: "m", session_id: "s", title: "Probe", status: "idle", model: "old-model" } as Session;
  upsertSession(session);
  let result: boolean | "pending" = "pending";
  void applyModes(() => session, { model: "new-model" }).then((ok) => { result = ok; });
  await vi.advanceTimersByTimeAsync(pastHub(90_000) - 1);
  expect(result).toBe("pending");
  await vi.advanceTimersByTimeAsync(1);
  expect(result).toBe(false);
  expect(getSession("m", "s")?.model).toBe("new-model");
  expect(toast).toHaveBeenCalledWith("The hub didn't confirm the change within 95 seconds. It may have been applied; check the current settings before trying again.", "error");
});
