// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError, HubTimeoutError, isAbort, READ_TIMEOUT_MS, setToken } from "./api";
import { connect, getState, refreshAssistant, refreshTodos, reset, todosPushed } from "./store";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function abortableFetch() {
  const calls: { url: string; init?: RequestInit; signal?: AbortSignal | null; resolve: (r: Response) => void; reject: (e: unknown) => void }[] = [];
  const fn = vi.fn((url: string, init?: RequestInit) => {
    const d = deferred<Response>();
    init?.signal?.addEventListener("abort", () => d.reject(new DOMException("aborted", "AbortError")), { once: true });
    calls.push({ url, init, signal: init?.signal, resolve: d.resolve, reject: d.reject });
    return d.promise;
  });
  vi.stubGlobal("fetch", fn);
  return { fn, calls };
}

function makeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  };
}

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  constructor() { FakeSocket.all.push(this); }
  send() {}
  close() { this.readyState = 3; }
  open() { this.readyState = 1; this.onopen?.(); }
  push(msg: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(msg) }); }
}

describe("api in-flight GET deduplication", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", makeStorage());
    setToken("token-a");
    reset();
  });

  afterEach(() => {
    reset();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it.each([0, READ_TIMEOUT_MS])("uses the global fetch receiver while coalescing and cancelling a waiter (timeout %s)", async (timeout) => {
    const response = deferred<Response>();
    let signal: AbortSignal | null | undefined;
    const nativeFetch = vi.fn(function (this: unknown, _url: RequestInfo | URL, init?: RequestInit) {
      if (this !== globalThis) throw new TypeError("Illegal invocation");
      signal = init?.signal;
      return response.promise;
    });
    // The old entry.fetcher(...) call must fail with this stub, as it does with native fetch in Chrome.
    expect(() => ({ fetcher: nativeFetch }).fetcher("/api/v1/managers")).toThrow("Illegal invocation");
    nativeFetch.mockClear();
    vi.stubGlobal("fetch", nativeFetch);

    const controller = new AbortController();
    const cancelled = api("/managers", { timeout, signal: controller.signal }).catch((e: unknown) => e);
    const remaining = api("/managers", { timeout });
    expect(nativeFetch).toHaveBeenCalledTimes(1);
    expect(nativeFetch.mock.contexts).toEqual([globalThis]);
    controller.abort();
    expect(isAbort(await cancelled)).toBe(true);
    expect(signal?.aborted).toBe(false);

    response.resolve(new Response(JSON.stringify({ managers: [{ id: "assistant", name: "Example assistant" }] })));
    await expect(remaining).resolves.toEqual({ managers: [{ id: "assistant", name: "Example assistant" }] });
  });

  it("coalesces concurrent identical GET requests into a single fetch and isolates response copies", async () => {
    const { fn, calls } = abortableFetch();
    const seenStatuses: number[] = [];
    const p1 = api<{ todos: { id: string }[] }>("/todos", { onResponse: (_h, s) => seenStatuses.push(s) });
    const p2 = api<{ todos: { id: string }[] }>("/todos", { onResponse: (_h, s) => seenStatuses.push(s) });
    const p3 = api<{ todos: { id: string }[] }>("/todos", { onResponse: (_h, s) => seenStatuses.push(s) });

    expect(fn).toHaveBeenCalledTimes(1);
    calls[0]!.resolve(new Response(JSON.stringify({ todos: [{ id: "t1" }] }), { status: 200, headers: { ETag: 'W/"e1"' } }));

    const [r1, r2, r3] = await Promise.all([p1, p2, p3]);
    expect(r1).toEqual({ todos: [{ id: "t1" }] });
    expect(r2).toEqual({ todos: [{ id: "t1" }] });
    expect(r3).toEqual({ todos: [{ id: "t1" }] });
    expect(seenStatuses).toEqual([200, 200, 200]);

    r1.todos.push({ id: "mutated" });
    expect(r2.todos).toEqual([{ id: "t1" }]);
    expect(r3.todos).toEqual([{ id: "t1" }]);
  });

  it("does not coalesce when headers differ or dedupe is explicitly disabled", async () => {
    const { fn, calls } = abortableFetch();
    const a = api("/todos", { headers: { "If-None-Match": 'W/"v1"' } });
    const b = api("/todos", { headers: { "If-None-Match": 'W/"v2"' } });
    const c = api("/todos", { headers: { "If-None-Match": 'W/"v1"' }, dedupe: false });

    expect(fn).toHaveBeenCalledTimes(3);
    for (const call of calls) call.resolve(new Response(JSON.stringify({ todos: [] }), { status: 200 }));
    await Promise.all([a, b, c]);
  });

  it("never caches failed GET promises so subsequent retries issue a fresh fetch", async () => {
    const { fn, calls } = abortableFetch();
    const first = api("/todos").catch((e: unknown) => e);
    const second = api("/todos").catch((e: unknown) => e);
    expect(fn).toHaveBeenCalledTimes(1);

    calls[0]!.resolve(new Response(JSON.stringify({ error: "temporary outage" }), { status: 503 }));
    const [e1, e2] = await Promise.all([first, second]);
    expect(e1).toBeInstanceOf(ApiError);
    expect(e2).toBeInstanceOf(ApiError);

    const retry = api<{ todos: string[] }>("/todos");
    expect(fn).toHaveBeenCalledTimes(2);
    calls[1]!.resolve(new Response(JSON.stringify({ todos: ["ok"] }), { status: 200 }));
    await expect(retry).resolves.toEqual({ todos: ["ok"] });

    // Network failure is also never cached.
    const netFail = api("/assistant").catch((e: unknown) => e);
    expect(fn).toHaveBeenCalledTimes(3);
    calls[2]!.reject(new TypeError("Failed to fetch"));
    expect(await netFail).toBeInstanceOf(TypeError);

    const recovered = api<{ main: string }>("/assistant");
    expect(fn).toHaveBeenCalledTimes(4);
    calls[3]!.resolve(new Response(JSON.stringify({ main: "m1" }), { status: 200 }));
    await expect(recovered).resolves.toEqual({ main: "m1" });
  });

  it("aborts only the cancelling waiter while another waiter remains, and aborts fetch once the last waiter leaves", async () => {
    const { fn, calls } = abortableFetch();
    const c1 = new AbortController();
    const c2 = new AbortController();

    const p1 = api<{ ok: boolean }>("/sessions?agents_only=1", { signal: c1.signal }).catch((e: unknown) => e);
    const p2 = api<{ ok: boolean }>("/sessions?agents_only=1", { signal: c2.signal });
    expect(fn).toHaveBeenCalledTimes(1);

    c1.abort();
    expect(isAbort(await p1)).toBe(true);
    expect(calls[0]!.signal?.aborted).toBe(false);

    calls[0]!.resolve(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    await expect(p2).resolves.toEqual({ ok: true });

    // When all waiters abort, the underlying fetch is aborted and evicted.
    const c3 = new AbortController();
    const c4 = new AbortController();
    const p3 = api("/sessions?agents_only=1", { signal: c3.signal }).catch((e: unknown) => e);
    const p4 = api("/sessions?agents_only=1", { signal: c4.signal }).catch((e: unknown) => e);
    expect(fn).toHaveBeenCalledTimes(2);

    c3.abort();
    expect(isAbort(await p3)).toBe(true);
    expect(calls[1]!.signal?.aborted).toBe(false);

    c4.abort();
    expect(isAbort(await p4)).toBe(true);
    expect(calls[1]!.signal?.aborted).toBe(true);

    const p5 = api<{ fresh: boolean }>("/sessions?agents_only=1");
    expect(fn).toHaveBeenCalledTimes(3);
    calls[2]!.resolve(new Response(JSON.stringify({ fresh: true }), { status: 200 }));
    await expect(p5).resolves.toEqual({ fresh: true });
  });

  it("keeps per-waiter timeout semantics intact while sharing one fetch", async () => {
    vi.useFakeTimers();
    const { fn, calls } = abortableFetch();
    const fast = api("/machines", { timeout: 500 }).catch((e: unknown) => e);
    const slow = api<{ machines: string[] }>("/machines", { timeout: READ_TIMEOUT_MS });
    expect(fn).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(500);
    expect(await fast).toBeInstanceOf(HubTimeoutError);
    expect(calls[0]!.signal?.aborted).toBe(false);

    calls[0]!.resolve(new Response(JSON.stringify({ machines: ["m1"] }), { status: 200 }));
    await expect(slow).resolves.toEqual({ machines: ["m1"] });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("detaches in-flight GETs on non-GET writes and credential changes", async () => {
    const { fn, calls } = abortableFetch();
    const beforeWrite = api<{ v: number }>("/todos");
    expect(fn).toHaveBeenCalledTimes(1);

    const write = api("/todos", { method: "POST", json: { title: "New" } });
    expect(fn).toHaveBeenCalledTimes(2);
    const duringWrite = api<{ v: number }>("/todos");
    expect(fn).toHaveBeenCalledTimes(3);
    calls[1]!.resolve(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    await write;

    const afterWrite = api<{ v: number }>("/todos");
    expect(fn).toHaveBeenCalledTimes(4);
    calls[0]!.resolve(new Response(JSON.stringify({ v: 1 }), { status: 200 }));
    calls[2]!.resolve(new Response(JSON.stringify({ v: 2 }), { status: 200 }));
    calls[3]!.resolve(new Response(JSON.stringify({ v: 3 }), { status: 200 }));
    expect(await beforeWrite).toEqual({ v: 1 });
    expect(await duringWrite).toEqual({ v: 2 });
    expect(await afterWrite).toEqual({ v: 3 });

    const beforeAuth = api<{ v: number }>("/assistant");
    expect(fn).toHaveBeenCalledTimes(5);
    setToken("token-b");
    const afterAuth = api<{ v: number }>("/assistant");
    expect(fn).toHaveBeenCalledTimes(6);
    calls[4]!.resolve(new Response(JSON.stringify({ v: 1 }), { status: 200 }));
    calls[5]!.resolve(new Response(JSON.stringify({ v: 2 }), { status: 200 }));
    expect(await beforeAuth).toEqual({ v: 1 });
    expect(await afterAuth).toEqual({ v: 2 });
  });

  it("does not coalesce onto a dangling entry after global fetch is replaced", async () => {
    const first = abortableFetch();
    const stale = api<{ v: number }>("/machines/m1/browser/cookies");
    expect(first.fn).toHaveBeenCalledTimes(1);

    const second = abortableFetch();
    const fresh = api<{ v: number }>("/machines/m1/browser/cookies");
    expect(second.fn).toHaveBeenCalledTimes(1);

    second.calls[0]!.resolve(new Response(JSON.stringify({ v: 2 }), { status: 200 }));
    await expect(fresh).resolves.toEqual({ v: 2 });

    first.calls[0]!.resolve(new Response(JSON.stringify({ v: 1 }), { status: 200 }));
    await expect(stale).resolves.toEqual({ v: 1 });
  });

  it("coalesces same-boundary passive cold-start reads while non-resumed hello and todosPushed issue fresh boundary reads", async () => {
    vi.useFakeTimers();
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    vi.stubGlobal("location", { protocol: "http:", host: "hub.example.com" });
    const held: Record<string, { resolve: (r: Response) => void }[]> = { "/api/v1/todos": [], "/api/v1/assistant": [] };
    const counts: Record<string, number> = { "/api/v1/todos": 0, "/api/v1/assistant": 0 };
    vi.stubGlobal("fetch", vi.fn((url: string) => {
      const path = new URL(url, "http://hub.example.com").pathname;
      if (path === "/api/v1/todos" || path === "/api/v1/assistant") {
        counts[path]++;
        const d = deferred<Response>();
        held[path]!.push(d);
        return d.promise;
      }
      return Promise.resolve(new Response(JSON.stringify({}), { status: 200 }));
    }));

    connect();
    // 250ms fallback fires before hello arrives; overlapping passive view-mount reads coalesce with it.
    await vi.advanceTimersByTimeAsync(250);
    void refreshTodos({ coalesce: true });
    void refreshAssistant({ coalesce: true }).catch(() => {});
    expect(counts["/api/v1/todos"]).toBe(1);
    expect(counts["/api/v1/assistant"]).toBe(1);

    // Non-resumed WebSocket hello crosses the freshness boundary and issues fresh post-hello GETs.
    const sock = FakeSocket.all.at(-1)!;
    sock.open();
    sock.push({ type: "hello", features: ["todo_revision"] });
    expect(counts["/api/v1/todos"]).toBe(2);
    expect(counts["/api/v1/assistant"]).toBe(2);

    // Feed-triggered todos push settles while /todos still has no revision: issues a fresh post-push GET.
    todosPushed();
    await vi.dynamicImportSettled();
    await vi.advanceTimersByTimeAsync(250);
    expect(counts["/api/v1/todos"]).toBe(3);
    expect(counts["/api/v1/assistant"]).toBe(2);

    // Newest answers resolve first; stale pre-hello/pre-push snapshots resolving later cannot overwrite them.
    held["/api/v1/todos"]![2]!.resolve(new Response(JSON.stringify({ todos: [{ id: "t2", title: "After Push", created_at: "", updated_at: "" }] }), { status: 200 }));
    held["/api/v1/assistant"]![1]!.resolve(new Response(JSON.stringify({ machine_id: "hub", main: "main-after-hello", conversations: [] }), { status: 200 }));
    await vi.advanceTimersByTimeAsync(0);
    held["/api/v1/todos"]![1]!.resolve(new Response(JSON.stringify({ todos: [{ id: "t1", title: "At Hello", created_at: "", updated_at: "" }] }), { status: 200 }));
    held["/api/v1/todos"]![0]!.resolve(new Response(JSON.stringify({ todos: [{ id: "t0", title: "Pre Hello", created_at: "", updated_at: "" }] }), { status: 200 }));
    held["/api/v1/assistant"]![0]!.resolve(new Response(JSON.stringify({ machine_id: "hub", main: "main-pre-hello", conversations: [] }), { status: 200 }));
    await vi.advanceTimersByTimeAsync(0);

    expect(getState().todos.map((t) => t.id)).toEqual(["t2"]);
    expect(getState().assistant?.main).toBe("main-after-hello");
  });
});
