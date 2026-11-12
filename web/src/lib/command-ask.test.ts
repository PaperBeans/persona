// The screen card's plain words for Claude Code's command prompt (#5266), from the dialog the hub reads (#5265).
import { describe, expect, it } from "vitest";
import { t } from "../i18n";
import { allowDeny, commandAsk } from "./command-ask";
import { countdownLine, originalWords, riskLine } from "./command-ask-lines";
import type { ScreenDialog } from "./types";

const hub: ScreenDialog = {
  question: "Do you want to proceed?", rev: "44597c89094b",
  detail: "Bash command\nCount the example names in first-screen chunks\ncd ./dist && ls",
  warning: "Dangerous rm operation not ruled out — the shell may read this command differently than it was parsed",
  deadline: "Claude Code will automatically block this request in 1:44, to avoid holding up an unattended session",
  command: "cd ./dist && ls", description: "Count the example names in first-screen chunks", risk: "delete",
  deadline_action: "deny", deadline_at: "2026-11-12T03:00:00Z",
  options: [{ key: "1", label: "Yes" }, { key: "2", label: "No" }],
};

describe("commandAsk", () => {
  it("reads the hub's command prompt", () => {
    const ask = commandAsk(hub)!;
    expect(ask.command).toBe("cd ./dist && ls");
    expect(ask.description).toBe("Count the example names in first-screen chunks");
    expect(riskLine(ask)).toBe(t("chat-commandask.risk_delete"));
    expect(originalWords(ask)).toMatch(/^Dangerous rm operation not ruled out.*\nClaude Code will automatically block this request in 1:44/s);
    expect(ask.deadlineAt).toBe(Date.parse("2026-11-12T03:00:00Z"));
    expect(ask.deadlineAllows).toBe(false);
    expect(allowDeny(hub)).toEqual({ allow: hub.options![0], deny: hub.options![1] });
  });

  it("leaves an older hub's dialog as a question", () => {
    const old: ScreenDialog = { question: "Do you want to proceed?", detail: "Bash command\nrm -rf build", options: hub.options };
    expect(commandAsk(old)).toBeNull();
    expect(allowDeny(old)).toBeNull();
  });

  it("keeps the labels of a prompt with more choices", () => {
    const three = { ...hub, options: [{ key: "1", label: "Yes" }, { key: "2", label: "Yes, and don't ask again for ls commands" }, { key: "3", label: "No" }] };
    expect(commandAsk(three)).not.toBeNull();
    expect(allowDeny(three)).toBeNull();
  });

  it("says a warning it has no words for only as a warning", () => {
    expect(riskLine(commandAsk({ ...hub, risk: undefined })!)).toBe(t("chat-commandask.risk_other"));
    expect(riskLine(commandAsk({ ...hub, warning: undefined, deadline: undefined })!)).toBeUndefined();
    expect(originalWords(commandAsk({ ...hub, warning: undefined, deadline: undefined })!)).toBeUndefined();
  });
});

describe("countdownLine", () => {
  const end = Date.parse("2026-11-12T03:00:00Z");
  it("counts down to the end, then says what happens", () => {
    const ask = commandAsk(hub)!;
    expect(countdownLine(ask, end - 104_000)).toBe(t("chat-commandask.countdown_deny", { time: "1:44" }));
    expect(countdownLine(ask, end - 500)).toBe(t("chat-commandask.countdown_deny", { time: "0:01" }));
    expect(countdownLine(ask, end)).toBe(t("chat-commandask.countdown_over_deny"));
    const allow = commandAsk({ ...hub, deadline_action: "allow" })!;
    expect(countdownLine(allow, end - 61_000)).toBe(t("chat-commandask.countdown_allow", { time: "1:01" }));
    expect(countdownLine(commandAsk({ ...hub, deadline_at: undefined })!, end)).toBeNull();
  });
});
