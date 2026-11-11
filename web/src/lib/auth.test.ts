import { beforeEach, describe, expect, it, vi } from "vitest";

const calls = vi.hoisted(() => ({ list: [] as { path: string; token?: string; json?: Record<string, string> }[], good: "" }));
vi.mock("./api", () => ({
  api: vi.fn(async (path: string, init?: { token?: string; json?: Record<string, string> }) => {
    calls.list.push({ path, token: init?.token, json: init?.json });
    if (init?.token !== calls.good) throw new Error("invalid token");
    return path === "/auth/tokens" ? { token: "jd_browser", id: "dev1" } : { machines: [] };
  }),
  setPairedToken: vi.fn(),
}));

import { setPairedToken } from "./api";
import { storeCredentials } from "./compatibility";
import { deviceName, pair, pairToken, takeToken } from "./auth";

describe("pairToken", () => {
  it("keeps bare tokens as they are", () => {
    expect(pairToken("  master-secret \n")).toBe("master-secret");
    expect(pairToken("jp_cafe00a1")).toBe("jp_cafe00a1");
    expect(pairToken("jd_cafe00a1")).toBe("jd_cafe00a1");
  });
  it("takes the token from the app's pairing link, percent-decoded", () => {
    expect(pairToken("persona://pair?token=jp_example%2B%26&url=http%3A%2F%2Fhub.example%3A18660")).toBe("jp_example+&");
    expect(pairToken("persona://pair?url=http%3A%2F%2Fhub.example%3A18660&token=a%26b%3D%2B+%3F")).toBe("a&b=+ ?");
  });
  it("takes it from ?token= or #token= in an ordinary link", () => {
    expect(pairToken("http://hub.example:18660/?token=t0k&tab=x")).toBe("t0k");
    expect(pairToken("https://hub.example/persona#a=1&token=t0k")).toBe("t0k");
  });
  it("passes anything else through, so the hub turns it down", () => {
    expect(pairToken("persona://pair?url=http%3A%2F%2Fhub.example")).toBe("persona://pair?url=http%3A%2F%2Fhub.example");
    expect(pairToken("not a token")).toBe("not a token");
    expect(pairToken("http://[bad")).toBe("http://[bad");
  });
});

describe("pair", () => {
  let stored: Record<string, string>;
  beforeEach(() => {
    calls.list = [];
    stored = {};
    vi.mocked(setPairedToken).mockClear();
    vi.mocked(setPairedToken).mockImplementation((token, id) => storeCredentials(token, id));
    vi.stubGlobal("localStorage", { getItem: (k: string) => stored[k] ?? null, setItem: (k: string, v: string) => void (stored[k] = v), removeItem: (k: string) => void delete stored[k] });
    vi.stubGlobal("navigator", { userAgent: "" });
  });
  it("asks for a web device and keeps its id", async () => {
    calls.good = "jp_example";
    await pair("jp_example");
    const trade = calls.list.find((c) => c.path === "/auth/tokens");
    expect(trade?.json?.kind).toBe("web");
    expect(trade?.json).not.toHaveProperty("replaces");
    expect(stored["persona.tokenId"]).toBe("dev1");
  });
  it("has the hub replace the device this browser had before", async () => {
    calls.good = "jp_example";
    stored["persona.token"] = "jd_previous";
    stored["persona.tokenId"] = "dev_old";
    await pair("jp_example");
    expect(calls.list.find((c) => c.path === "/auth/tokens")?.json?.replaces).toBe("dev_old");
    expect(stored["persona.tokenId"]).toBe("dev1");
  });
  it("checks and trades the token from a pasted pairing link", async () => {
    calls.good = "jp_example";
    await pair("persona://pair?url=http%3A%2F%2Fhub.example%3A18660&token=jp_example");
    expect(calls.list.map((c) => [c.path, c.token])).toEqual([
      ["/machines", "jp_example"],
      ["/auth/tokens", "jp_example"],
    ]);
    expect(setPairedToken).toHaveBeenCalledWith("jd_browser", "dev1");
  });
  it("keeps a device token without trading it", async () => {
    calls.good = "jd_device";
    await pair("jd_device");
    expect(calls.list.map((c) => c.path)).toEqual(["/machines"]);
    expect(setPairedToken).toHaveBeenCalledWith("jd_device");
  });
  it("throws what the hub said for a link without a token", async () => {
    calls.good = "jp_example";
    await expect(pair("persona://pair?url=http%3A%2F%2Fhub.example")).rejects.toThrow("invalid token");
    expect(setPairedToken).not.toHaveBeenCalled();
  });
});

describe("deviceName", () => {
  it("names the browser and the day it signed in", () => {
    vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/130.0 Safari/537.36" });
    const name = deviceName(new Date(2026, 10, 6));
    expect(name).toContain("Chrome · Mac");
    expect(name).toMatch(/6/);
  });
});

describe("takeToken", () => {
  it("takes the token from the query and keeps the rest of the link", () => {
    expect(takeToken("http://hub.example:18660/s/m1/abc?token=t0k&tab=x#top")).toEqual({ token: "t0k", rest: "/s/m1/abc?tab=x#top" });
    expect(takeToken("http://hub.example:18660/?token=t0k")).toEqual({ token: "t0k", rest: "/" });
  });
  it("takes it from the fragment too", () => {
    expect(takeToken("http://hub.example:18660/persona#token=t0k")).toEqual({ token: "t0k", rest: "/persona" });
    expect(takeToken("http://hub.example:18660/persona#a=1&token=t0k")).toEqual({ token: "t0k", rest: "/persona#a=1" });
  });
  it("leaves links without one alone, and strips an empty one", () => {
    expect(takeToken("http://hub.example:18660/settings?tab=models")).toBeNull();
    expect(takeToken("http://hub.example:18660/?token=")).toEqual({ token: "", rest: "/" });
  });
});
