// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const edge = vi.hoisted(() => ({ reload: vi.fn() }));
vi.mock("./edge-auth", async (original) => ({ ...await original<typeof import("./edge-auth")>(), reloadForSignIn: edge.reload }));

import { api, credentialOwner, getToken, handleUnauthorized, setToken } from "./api";
import { chooseManager, resetManagerChoice, setRosterDefault } from "./manager-choice";
import { taskTrustContext } from "./task-trust";

let answer: (response: Response) => void;
let request: RequestInit | undefined;
const unauthorized = vi.fn();
const proxy = (kind: string) => kind === "redirect"
  ? new Response(null, { status: 302, headers: { Location: "/synthetic-login" } })
  : new Response("<html>synthetic proxy login</html>", { status: Number(kind.slice(4)), headers: { "Content-Type": "text/html" } });

beforeEach(() => {
  localStorage.clear(); resetManagerChoice(); setRosterDefault("assistant"); setToken("synthetic-a");
  edge.reload.mockClear(); unauthorized.mockClear(); handleUnauthorized(unauthorized);
  vi.stubGlobal("fetch", vi.fn((_url: RequestInfo | URL, init?: RequestInit) => {
    request = init;
    return new Promise<Response>((resolve) => { answer = resolve; });
  }));
});
afterEach(() => { handleUnauthorized(() => {}); vi.unstubAllGlobals(); setToken(""); resetManagerChoice(); });

function ownedDelete(isCurrent?: () => boolean) {
  const context = taskTrustContext(), owner = credentialOwner(), token = getToken();
  return api("/task-trust?key=synthetic", {
    method: "DELETE", token, tokenOwner: owner, isAuthContextCurrent: isCurrent ?? (() => context === taskTrustContext()),
  }).catch((error: Error) => error);
}

describe("proxy authentication and request ownership", () => {
  it.each(["html401", "redirect", "html200", "html403", "html407", "html511"])("does not reload a replacement manager for an old %s response", async (kind) => {
    const pending = ownedDelete();
    expect(new Headers(request?.headers).get("Authorization")).toBe("Bearer synthetic-a");
    expect(request).not.toHaveProperty("isAuthContextCurrent");
    chooseManager("work"); answer(proxy(kind));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it("retires the previous manager even after a round trip to it", async () => {
    const pending = ownedDelete();
    chooseManager("work"); chooseManager(null); answer(proxy("redirect"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it.each(["replacement", "round-trip"])("does not reload a %s credential even if the context callback stays true", async (mode) => {
    const pending = ownedDelete(() => true);
    setToken("synthetic-b"); if (mode === "round-trip") setToken("synthetic-a");
    answer(proxy("html401"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it.each(["replacement", "round-trip"])("does not reload an ordinary request's retired %s credential", async (mode) => {
    const pending = api("/machines", { timeout: 0 }).catch((error: Error) => error);
    setToken("synthetic-b"); if (mode === "round-trip") setToken("synthetic-a");
    answer(proxy("html401"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it("does not reload when an explicitly owned request uses a different credential", async () => {
    const pending = api("/machines", { method: "DELETE", token: "synthetic-probe", tokenOwner: credentialOwner(), isAuthContextCurrent: () => true }).catch((error: Error) => error);
    answer(proxy("redirect"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it.each(["html401", "redirect", "html200", "html403", "html407", "html511"])("reloads the current owned context for %s", async (kind) => {
    const pending = ownedDelete(); answer(proxy(kind));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).toHaveBeenCalledOnce();
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it.each(["html401", "redirect"])("still reloads an unowned pairing probe for %s", async (kind) => {
    const pending = api("/machines", { token: "synthetic-probe", timeout: 0 }).catch((error: Error) => error);
    answer(proxy(kind));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).toHaveBeenCalledOnce();
  });

  it("does not reload an unowned request when its explicit context was retired", async () => {
    const pending = api("/machines", { token: "synthetic-probe", timeout: 0, isAuthContextCurrent: () => false }).catch((error: Error) => error);
    answer(proxy("html401"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).not.toHaveBeenCalled();
  });

  it("keeps the ordinary current request's proxy login behavior", async () => {
    const pending = api("/machines", { timeout: 0 }).catch((error: Error) => error);
    answer(proxy("redirect"));
    expect(await pending).toBeInstanceOf(Error); expect(edge.reload).toHaveBeenCalledOnce();
  });

  it("keeps JSON 401 recovery separate from proxy reloads", async () => {
    const pending = ownedDelete();
    answer(new Response(JSON.stringify({ error: "synthetic refusal" }), { status: 401, headers: { "Content-Type": "application/json" } }));
    expect(await pending).toMatchObject({ status: 401 });
    expect(unauthorized).toHaveBeenCalledOnce(); expect(edge.reload).not.toHaveBeenCalled();
  });

  it("keeps a validated 304 out of proxy and 401 recovery", async () => {
    const metadata = vi.fn(), owner = credentialOwner();
    const pending = api("/machines", { token: getToken(), tokenOwner: owner, isAuthContextCurrent: () => true,
      headers: { "If-None-Match": 'W/"synthetic"' }, onResponse: metadata, timeout: 0 });
    answer(new Response(null, { status: 304, headers: { ETag: 'W/"synthetic"' } }));
    expect(await pending).toEqual({});
    expect(metadata).toHaveBeenCalledOnce(); expect(metadata.mock.calls[0]![1]).toBe(304);
    expect(edge.reload).not.toHaveBeenCalled(); expect(unauthorized).not.toHaveBeenCalled();
  });
});
