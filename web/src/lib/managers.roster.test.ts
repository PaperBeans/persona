// @vitest-environment jsdom
// Reading the roster again (a `managers` push, the socket's hello): asked while a read is out, it reads once more when
// that one is done, so a push that tells about a change the hub made after answering the read is not lost.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import { DISPLAY_IDENTITY_KEY, resetManagerChoice, shownIdentity } from "./manager-choice";
import { currentRoster, loadRoster, resetRoster, setRoster } from "./managers";

let count: number;
let reads: number;
// the answers held back until released, in order; an argument rejects that read instead
let held: ((error?: unknown) => void)[];

const roster = (n: number) => ({ default: "assistant", managers: [{ id: "assistant", name: "助手", online: true }, { id: "m-1", name: "工作", online: true, inbox_needs_you: n }] });
const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
const settle = async () => {
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setTimeout(resolve, 0));
};
const work = () => currentRoster()?.managers.find((m) => m.id === "m-1")?.inbox_needs_you;

beforeEach(() => {
  count = 2;
  reads = 0;
  held = [];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    if (new URL(String(input), "http://hub.example.com").pathname !== "/api/v1/managers") return Promise.resolve(json({}));
    reads++;
    const body = roster(count); // what the hub knows when the read reaches it
    return new Promise<Response>((resolve, reject) => held.push((error) => {
      if (error) reject(error);
      else resolve(json(body));
    }));
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  resetRoster();
  resetManagerChoice();
});

describe("reading the roster again", () => {
  it("reads once more after a read that was out when asked, for any number of asks meanwhile", async () => {
    const first = loadRoster();
    await settle();
    expect(reads).toBe(1); // read out: the hub answered it with 2
    count = 5; // the count moves and the hub pushes `managers`, twice, before that answer is back
    const again = loadRoster();
    const once = loadRoster();
    expect(once).toBe(again);
    held.shift()!();
    await first;
    expect(work()).toBe(2);
    await settle();
    expect(reads).toBe(2); // one more read, not one per ask
    held.shift()!();
    await again;
    expect(work()).toBe(5);
    await settle();
    expect(reads).toBe(2);
  });

  it("reads once with nothing out", async () => {
    const done = loadRoster();
    await settle();
    held.shift()!();
    await done;
    await settle();
    expect(reads).toBe(1);
    expect(work()).toBe(2);
  });

  it("keeps the remembered head through read failures except a genuinely unsupported roster", async () => {
    setRoster({ default: "assistant", managers: [{ id: "assistant", name: "助手", online: true }] });
    expect(shownIdentity()).toEqual({ id: "assistant", name: "助手" });
    resetRoster(); // a reload: no fresh roster, but the head identity remains

    const fail = async (error: ApiError) => {
      const done = loadRoster();
      await settle();
      expect(held).toHaveLength(1);
      held.shift()!(error);
      await done;
    };
    await fail(new ApiError(503, "unavailable", { error: "unavailable" }));
    expect(shownIdentity()).toEqual({ id: "assistant", name: "助手" });
    expect(localStorage.getItem(DISPLAY_IDENTITY_KEY)).not.toBeNull();

    resetRoster(); // cancel the temporary failure's retry without clearing the identity
    await fail(new ApiError(404, "gone", { error: "gone", code: "no_manager" }));
    expect(shownIdentity()).toEqual({ id: "assistant", name: "助手" });

    resetRoster();
    await fail(new ApiError(404, "not found", { error: "not found" }));
    expect(shownIdentity()).toBeNull();
    expect(localStorage.getItem(DISPLAY_IDENTITY_KEY)).toBeNull();
  });

  it("makes no read after a reset for an ask from before it", async () => {
    void loadRoster();
    await settle();
    void loadRoster(); // asked while out
    resetRoster();
    held.shift()!();
    await settle();
    expect(reads).toBe(1);
  });
});
