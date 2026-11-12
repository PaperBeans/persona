// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { apiBlob, ApiError } from "./api";
import { boundedBlobReader } from "./bounded-blob";

const maxBytes = 2 * 1024 * 1024;
const readBlob = boundedBlobReader({ maxBytes, mimeTypes: ["image/png"] });
afterEach(() => vi.unstubAllGlobals());

it("uses the ordinary API authentication, client headers and binary response seam", async () => {
  const response = new Response(new Uint8Array([1, 2, 3]), { headers: { "Content-Type": "image/png; charset=utf-8", "Content-Length": "3" } });
  const ordinaryBlob = vi.spyOn(response, "blob");
  const fetch = vi.fn().mockResolvedValue(response);
  vi.stubGlobal("fetch", fetch);
  const blob = await apiBlob("/extensions/notes/-/icon?v=1", {
    token: "synthetic-binary-token", headers: { "X-Persona-Manager": "owner-b" },
    credentials: "same-origin", redirect: "manual", readBlob,
  });
  expect(blob.size).toBe(3); expect(blob.type).toBe("image/png"); expect(ordinaryBlob).not.toHaveBeenCalled();
  const init = fetch.mock.calls[0][1] as RequestInit;
  const headers = new Headers(init.headers);
  expect(headers.get("Authorization")).toBe("Bearer synthetic-binary-token");
  expect(headers.get("X-Persona-Client")).toBe("web/1");
  expect(headers.get("X-Persona-Manager")).toBe("owner-b");
  expect(init.credentials).toBe("same-origin"); expect(init.redirect).toBe("manual");
  expect(init).not.toHaveProperty("readBlob");
});

it.each<Record<string, string>>([
  { "Content-Type": "image/jpeg" },
  { "Content-Type": "image/png", "Content-Length": String(maxBytes + 1) },
  { "Content-Type": "image/png", "Content-Length": "invalid" },
])("rejects MIME or declared length before reading bytes and cancels the body: %j", async headers => {
  const pull = vi.fn();
  const cancel = vi.fn();
  const stream = new ReadableStream<Uint8Array>({ pull, cancel }, { highWaterMark: 0 });
  const reader = vi.spyOn(stream, "getReader");
  const response = new Response(stream, { headers });
  vi.stubGlobal("fetch", vi.fn(async () => response));
  await expect(apiBlob("/extensions/notes/-/icon?v=1", { token: "synthetic", readBlob })).rejects.toThrow("Invalid bounded blob response");
  expect(reader).not.toHaveBeenCalled(); expect(pull).not.toHaveBeenCalled(); expect(cancel).toHaveBeenCalledTimes(1);
});

it("stops a chunked body immediately beyond 2 MiB rather than buffering the unread tail", async () => {
  let pulls = 0;
  const cancel = vi.fn();
  const stream = new ReadableStream<Uint8Array>({
    pull(controller) {
      pulls++;
      controller.enqueue(new Uint8Array(pulls < 3 ? maxBytes / 2 : 1));
      // No EOF: an unbounded reader would continue consuming the producer indefinitely.
    }, cancel,
  }, { highWaterMark: 0 });
  const response = new Response(stream, { headers: { "Content-Type": "image/png" } });
  const ordinaryBlob = vi.spyOn(response, "blob");
  vi.stubGlobal("fetch", vi.fn(async () => response));
  await expect(apiBlob("/extensions/notes/-/icon?v=1", { token: "synthetic", readBlob })).rejects.toThrow("exceeds blob limit");
  expect(pulls).toBe(3); expect(cancel).toHaveBeenCalledTimes(1); expect(ordinaryBlob).not.toHaveBeenCalled();
});

it("cancels and unlocks a blocked stream when the caller aborts", async () => {
  let started!: () => void;
  const reading = new Promise<void>(resolve => { started = resolve; });
  const cancel = vi.fn();
  const stream = new ReadableStream<Uint8Array>({ pull() { started(); }, cancel }, { highWaterMark: 0 });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(stream, { headers: { "Content-Type": "image/png" } })));
  const controller = new AbortController();
  const request = apiBlob("/extensions/notes/-/icon?v=1", { token: "synthetic", readBlob, signal: controller.signal });
  await reading;
  controller.abort();
  await expect(request).rejects.toMatchObject({ name: "AbortError" });
  expect(cancel).toHaveBeenCalledTimes(1); expect(stream.locked).toBe(false);
});

it("keeps the normal API's error handling and does not invoke the binary reader for a failed response", async () => {
  const reader = vi.fn(readBlob);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "Missing icon", code: "icon_missing" }), { status: 404 })));
  const error = await apiBlob("/extensions/notes/-/icon?v=1", { token: "synthetic", readBlob: reader }).catch(error => error);
  expect(error).toBeInstanceOf(ApiError); expect(error.code).toBe("icon_missing"); expect(reader).not.toHaveBeenCalled();
});
