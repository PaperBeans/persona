// #2528: the fallback card's view keeps "the next start will use the configured model" apart from proof that it runs,
// "back to the configured model" posts nothing but the session's path, and the node's refusals read in the page's
// language by their code.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";

const calls: { path: string; init: RequestInit & { json?: unknown } }[] = [];
let failWith: ApiError | null = null;
vi.mock("./api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./api")>()),
  api: async (path: string, init: RequestInit & { json?: unknown } = {}) => {
    calls.push({ path, init });
    if (failWith) throw failWith;
    return { restore: "next_start", restore_to: "configured-model", closed: true }; // node/model_restore.go's answer
  },
}));

const { fallbackView, refusalFacts, restoreErrorText, restoreModel } = await import("./model-fallback");

const fallback = {
  subtype: "model_refusal_fallback",
  original_model: "primary-model",
  fallback_model: "fallback-model",
  category: "synthetic-policy",
  scope: "session",
  explanation: "Upstream refused the request",
  text: "Synthetic refusal words",
};

describe("fallbackView", () => {
  it("none without an active fallback, or for a record that names no fallback model", () => {
    expect(fallbackView({})).toBeNull();
    expect(fallbackView({ model_fallback: { ...fallback, fallback_model: "" } })).toBeNull();
    expect(fallbackView({ model_fallback: { ...fallback, subtype: "model_refusal_blocked" } })).toBeNull();
  });
  it("before a restore names the refused and fallback models and the error since, no target", () => {
    const v = fallbackView({ model_fallback: fallback, turn_error: "API Error: 404 fallback-model not available" });
    expect(v).toEqual({ original: "primary-model", fallback: "fallback-model", error: "API Error: 404 fallback-model not available", pending: false, target: "" });
  });
  it("after the restore the target is the configured model the node read, not the refused one", () => {
    const v = fallbackView({ model_fallback: fallback, model_restore: { state: "next_start", to: "configured-model", at: "2026-11-09T00:00:00Z" } });
    expect(v?.pending).toBe(true);
    expect(v?.target).toBe("configured-model");
    expect(v?.original).toBe("primary-model");
  });
  it("an unknown restore state is not pending", () => {
    expect(fallbackView({ model_fallback: fallback, model_restore: { state: "later", to: "x", at: "" } })?.pending).toBe(false);
  });
});

describe("restoreModel", () => {
  beforeEach(() => {
    calls.length = 0;
    failWith = null;
  });
  it("POSTs the session's restore-model with no body", async () => {
    const res = await restoreModel("m 1", "s/1");
    expect(res).toEqual({ restore: "next_start", restore_to: "configured-model", closed: true });
    expect(calls).toHaveLength(1);
    expect(calls[0].path).toBe("/sessions/m%201/s%2F1/restore-model");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.json).toBeUndefined();
    expect(calls[0].init.body).toBeUndefined();
  });
  it("words the node's refusals by code, and keeps other errors' own words", async () => {
    const cases: [string, string][] = [
      ["restore_busy", "正在工作"],
      ["restore_unsupported", "/model"],
      ["restore_nothing", "已经没有在用回退"],
      ["restore_unknown", "没有关闭任何东西"],
      ["node_outdated", "太旧"],
    ];
    for (const [code, words] of cases) {
      failWith = new ApiError(409, "node words " + code, { code, error: "node words " + code });
      const err = await restoreModel("m", "s").catch((e: unknown) => e);
      expect(restoreErrorText(err)).toContain(words);
    }
    expect(restoreErrorText(new ApiError(409, "其他原因", { code: "restore_gone" }))).toBe("其他原因");
  });
});

describe("refusalFacts", () => {
  it("keeps every public value as recorded and the refusal's words untouched", () => {
    const text = "Upstream refused <this>\n  policy";
    expect(refusalFacts({ ...fallback, text })).toEqual({
      original: "primary-model",
      fallback: "fallback-model",
      category: "synthetic-policy",
      scope: "session",
      explanation: "Upstream refused the request",
      text,
    });
    expect(refusalFacts({ subtype: "model_refusal_future", text: "  " })).toEqual({
      original: undefined, fallback: undefined, category: undefined, scope: undefined, explanation: undefined, text: undefined,
    });
  });
  it("reads nothing of what an older hub sent beyond the public fields", () => {
    const MARKER = "HIDDEN-MARKER-2528-facts";
    const old = { ...fallback, raw: `{"syntheticHidden":{"nested":"${MARKER}"}}`, extra: { deeper: MARKER } } as unknown as typeof fallback;
    expect(JSON.stringify(refusalFacts(old))).not.toContain(MARKER);
    expect(refusalFacts(old).text).toBe("Synthetic refusal words");
  });
});
