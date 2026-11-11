import { describe, expect, it } from "vitest";
import { initI18n } from "../i18n";
import { dependencyText, dependentError, partialUninstall, previewBlocked, previewConsent } from "./extension-model";

describe("extension model consent", () => {
  const pv = { preview_id: "main", permissions_digest: "main-digest", expires_at: "2026-11-05T12:00:00Z" };
  const b = { ...pv, preview_id: "member", permissions_digest: "member-digest" };
  it("confirms every bundled preview, in order, without extra manifest fields", () => {
    expect(previewConsent(pv)).toEqual({ preview_id: "main", permissions_digest: "main-digest" });
    expect(previewConsent({ ...pv, bundle: [b] })).toEqual({ preview_id: "main", permissions_digest: "main-digest", bundle: [{ preview_id: "member", permissions_digest: "member-digest" }] });
  });
  it("blocks missing dependencies and any expired or malformed preview", () => {
    const now = Date.parse("2026-11-05T11:00:00Z");
    expect(previewBlocked(pv, now)).toBe(false);
    for (const p of [{ ...pv, unresolved: ["missing"] }, { ...pv, expires_at: "" }, { ...pv, bundle: [{ ...b, expires_at: "2026-11-05T10:00:00Z" }] }]) expect(previewBlocked(p, now)).toBe(true);
  });
  it("does not label an unready or unknown dependency as satisfied", async () => {
    await initI18n("zh-Hans");
    expect(dependencyText({ id: "base", state: "not_ready", installed: "1.0.0" })).toBe("依赖未就绪");
    expect(dependencyText({ id: "base", state: "future_state" })).toBe("状态待确认");
    expect(dependencyText({ id: "base", state: "ok", installed: "1.0.0" })).toBe("已满足 · 1.0.0");
  });
  it("translates the new dependency states in every supported language", async () => {
    for (const [lang, notReady, unknown] of [
      ["en", "Dependency not ready", "Status not confirmed"],
      ["ja", "依存先の設定待ち", "状態の確認待ち"],
    ] as const) {
      await initI18n(lang);
      expect(dependencyText({ id: "base", state: "not_ready" })).toBe(notReady);
      expect(dependencyText({ id: "base", state: "future_state" })).toBe(unknown);
    }
    await initI18n("zh-Hans");
  });
  it("reads the dependent conflict without assuming an API error class", () => {
    expect(dependentError({ body: { code: "has_dependents", dependents: [{ id: "notes", name: "笔记" }, { id: 123 }] } })).toEqual([{ id: "notes", name: "笔记" }]);
    expect(dependentError(new Error("no"))).toBeNull();
    // A pack uninstalled part way (#4072): the three groups, with odd entries left out.
    expect(partialUninstall({ body: { code: "partial_uninstall", removed_members: ["a", 1], failed_members: [{ id: "b", error: "busy" }, { id: "c" }, { error: "x" }], remaining_members: ["d"] } }))
      .toEqual({ removed: ["a"], failed: [{ id: "b", error: "busy" }, { id: "c", error: "" }], remaining: ["d"] });
    expect(partialUninstall({ body: { code: "has_dependents" } })).toBeNull();
    expect(partialUninstall(new Error("no"))).toBeNull();
    expect(dependencyText({ id: "notes", state: "old", version: ">=2.0.0", installed: "1.0.0" })).toContain(">=2.0.0");
  });
});
