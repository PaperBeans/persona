import { describe, expect, it, vi } from "vitest";
import { connFacts, NO_LINK } from "./connection";
import { probeEdge } from "./edge-auth";

describe("WS rejected-upgrade HTTP error probe", () => {
  it("surfaces 5xx JSON error detail via onServerError", async () => {
    const onServerError = vi.fn();
    const fetcher = vi.fn(async () =>
      new Response(JSON.stringify({ error: "hub maintenance in progress" }), {
        status: 503,
        statusText: "Service Unavailable",
        headers: { "Content-Type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    await probeEdge(fetcher, undefined, onServerError);
    expect(onServerError).toHaveBeenCalledWith("hub maintenance in progress");
  });

  it("falls back to HTTP status text for non-JSON 5xx responses", async () => {
    const onServerError = vi.fn();
    const fetcher = vi.fn(async () =>
      new Response("bad gateway", { status: 502, statusText: "Bad Gateway" }),
    ) as unknown as typeof fetch;

    await probeEdge(fetcher, undefined, onServerError);
    expect(onServerError).toHaveBeenCalledWith("HTTP 502 Bad Gateway");
  });

  it("prepends lastError in connFacts when unreachable/connecting and hides it when online or offline-network", () => {
    const now = 10_000;
    const link = { ...NO_LINK, nextTry: now + 3000, lastError: "HTTP 502 Bad Gateway" };
    expect(connFacts("unreachable", link, now)[0]).toBe("HTTP 502 Bad Gateway");
    expect(connFacts("connecting", link, now)[0]).toBe("HTTP 502 Bad Gateway");
    expect(connFacts("no_network", link, now)).toEqual([]);
    expect(connFacts("online", link, now)).toEqual([]);
  });

  it("drops late 5xx, 200, and intercepted callbacks when aborted or stale and lets a newer probe proceed immediately", async () => {
    let resolve5xx!: (r: Response) => void;
    const pending5xx = new Promise<Response>((r) => {
      resolve5xx = r;
    });
    const answered5xx = vi.fn(async () => {});
    const error5xx = vi.fn();
    const controller = new AbortController();

    const p1 = probeEdge(
      vi.fn(() => pending5xx) as unknown as typeof fetch,
      answered5xx,
      error5xx,
      controller.signal,
    );

    controller.abort();

    // A second probe starts and finishes cleanly even while the aborted first fetch has not settled.
    const secondError = vi.fn();
    await probeEdge(
      vi.fn(async () => new Response(JSON.stringify({ error: "new outage" }), { status: 503, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch,
      undefined,
      secondError,
    );
    expect(secondError).toHaveBeenCalledWith("new outage");

    // Resolving the aborted first fetch later must invoke neither answered nor onServerError.
    resolve5xx(new Response(JSON.stringify({ error: "stale outage" }), { status: 502, headers: { "Content-Type": "application/json" } }));
    await p1;
    expect(answered5xx).not.toHaveBeenCalled();
    expect(error5xx).not.toHaveBeenCalled();

    // A probe whose isCurrent() turns false (e.g. credential changed) must skip answered() on 200 and reloadForSignIn() on 302.
    let current = true;
    let resolve200!: (r: Response) => void;
    const pending200 = new Promise<Response>((r) => {
      resolve200 = r;
    });
    const staleAnswered = vi.fn(async () => {});
    const p2 = probeEdge(
      vi.fn(() => pending200) as unknown as typeof fetch,
      staleAnswered,
      undefined,
      undefined,
      () => current,
    );
    current = false;
    resolve200(new Response("{}", { status: 200 }));
    expect(await p2).toBe(false);
    expect(staleAnswered).not.toHaveBeenCalled();

    let current302 = true;
    let resolve302!: (r: Response) => void;
    const pending302 = new Promise<Response>((r) => {
      resolve302 = r;
    });
    const p3 = probeEdge(
      vi.fn(() => pending302) as unknown as typeof fetch,
      undefined,
      undefined,
      undefined,
      () => current302,
    );
    current302 = false;
    resolve302({ status: 302, type: "basic", redirected: false, headers: new Headers() } as unknown as Response);
    expect(await p3).toBe(false);
  });
});
