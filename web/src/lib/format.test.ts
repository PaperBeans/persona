import { describe, expect, it } from "vitest";
import { ago, attentionReason, baseName, externalLink, failureReason, failureText, sessionPreview } from "./format";
import { formatRelative } from "../i18n";
import type { Session } from "./types";

const session = (o: Partial<Session>): Session => ({ machine_id: "m", session_id: "s", cwd: "/x", status: "idle", ...o }) as Session;

// A botmux session's reason for waiting and its Feishu topic (attention_reason, external_url; docs/integrations/engines.md "botmux").
describe("sessionPreview", () => {
  it("shows why it waits while it needs you", () => {
    expect(sessionPreview(session({ status: "blocked", state: "needs_you", last_text: "正在跑全量", attention_reason: "本轮执行失败，再发一条消息继续" }))).toBe("本轮执行失败，再发一条消息继续");
  });
  it("keeps the last words otherwise", () => {
    expect(sessionPreview(session({ status: "working", state: "working", last_text: "在补测试", attention_reason: "旧的原因" }))).toBe("在补测试");
    expect(sessionPreview(session({ status: "blocked", state: "needs_you", last_text: "在等你", attention_reason: "  " }))).toBe("在等你");
    expect(sessionPreview(session({}))).toBe("");
  });
});

// The host stopped its turn on an error (failure_reason, iOS listPreview / FailureNote).
describe("failureReason", () => {
  it("is the notice without its warning sign while the session is not working", () => {
    expect(failureReason(session({ state: "idle", failure_reason: "⚠️ 模型接口出错，已停下" }))).toBe("模型接口出错，已停下");
    expect(failureReason(session({ state: "offline", status: "offline", failure_reason: "出错了" }))).toBe("出错了");
  });
  it("is gone while it works again, or when there is none", () => {
    expect(failureReason(session({ state: "working", status: "working", failure_reason: "出错了" }))).toBeUndefined();
    expect(failureReason(session({ failure_reason: " ⚠ " }))).toBeUndefined();
    expect(failureReason(session({}))).toBeUndefined();
    expect(failureReason(undefined)).toBeUndefined();
  });
  it("strips only the leading sign", () => {
    expect(failureText("⚠ 第 ⚠ 步失败")).toBe("第 ⚠ 步失败");
  });
  it("shows in the list in place of the last words", () => {
    expect(sessionPreview(session({ state: "idle", last_text: "改好了", failure_reason: "⚠️ 本轮出错" }))).toBe("本轮出错");
    expect(sessionPreview(session({ state: "needs_you", status: "blocked", attention_reason: "等你确认", failure_reason: "本轮出错" }))).toBe("等你确认");
  });
});

describe("attentionReason", () => {
  it("is there only while it needs you", () => {
    expect(attentionReason(session({ status: "blocked", state: "needs_you", attention_reason: " 等你确认 " }))).toBe("等你确认");
    expect(attentionReason(session({ status: "offline", state: "offline", attention_reason: "等你确认" }))).toBeUndefined();
    expect(attentionReason(session({ status: "blocked", state: "needs_you" }))).toBeUndefined();
  });
});

describe("externalLink", () => {
  it("takes an https link", () => {
    expect(externalLink(session({ external_url: "https://applink.example.com/thread?x=1" }))).toBe("https://applink.example.com/thread?x=1");
  });
  it("drops anything else", () => {
    for (const url of ["http://applink.example.com/t", "javascript:alert(1)", "lark://open", "not a url", ""]) {
      expect(externalLink(session({ external_url: url })), url).toBeUndefined();
    }
    expect(externalLink(undefined)).toBeUndefined();
  });
});

describe("ago", () => {
  const at = (d: number, h: number) => new Date(2026, 10, d, h, 0).getTime();
  const iso = (ms: number) => new Date(ms).toISOString();

  it("counts days by the calendar, not by 24 hour spans (#4281)", () => {
    // yesterday morning seen late tonight: 40 h, still yesterday
    expect(ago(iso(at(4, 7)), at(5, 23))).toBe(formatRelative(-1, "day"));
    // the evening before yesterday seen just past midnight: 30 h, two days ago
    expect(ago(iso(at(3, 19)), at(5, 1))).toBe(formatRelative(-2, "day"));
    // six calendar days back still reads as days; seven is a date
    expect(ago(iso(at(1, 23)), at(7, 1))).toBe(formatRelative(-6, "day"));
    expect(ago(iso(at(1, 1)), at(8, 23))).not.toBe(formatRelative(-7, "day"));
  });

  it("keeps minutes and hours as they were", () => {
    expect(ago(iso(at(5, 1)), at(5, 3))).toBe(formatRelative(-2, "hour"));
    expect(ago(iso(at(4, 23)), at(5, 1))).toBe(formatRelative(-2, "hour"));
  });
});

describe("baseName", () => {
  it("names the last folder of a path, without a trailing separator", () => {
    expect(baseName("/home/me/code/app")).toBe("app");
    expect(baseName("/home/me/code/app/")).toBe("app");
    expect(baseName("app")).toBe("app");
    expect(baseName("")).toBe("");
    expect(baseName(undefined)).toBe("");
  });

  it("reads a Windows computer's path by its own separator", () => {
    expect(baseName("C:\\Users\\me\\code\\app")).toBe("app");
    expect(baseName("C:\\Users\\me\\code\\app\\")).toBe("app");
    expect(baseName("d:/code/app")).toBe("app");
    expect(baseName("C:\\code/mixed\\app")).toBe("app");
    expect(baseName("\\\\server\\share\\app")).toBe("app");
  });

  it("keeps a backslash that is part of a name anywhere else", () => {
    expect(baseName("/home/me/odd\\name")).toBe("odd\\name");
  });
});
