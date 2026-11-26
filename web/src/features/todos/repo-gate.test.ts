import { beforeAll, describe, expect, it } from "vitest";
import { initI18n } from "../../i18n";
import {
  formatRowRepoGate,
  repoGatePRLabel,
  repoGateReasonLabel,
  repoGateTone,
  shouldShowRowRepoGate,
} from "./repo-gate";

beforeAll(async () => {
  await initI18n("en");
});

describe("repo-gate presentation helpers (#15757)", () => {
  it("hides row badge for undefined, reference_only, or zero target repos", () => {
    expect(shouldShowRowRepoGate(undefined)).toBe(false);
    expect(
      shouldShowRowRepoGate({
        state: "reference_only",
        target_count: 0,
        ready_count: 0,
        reference_count: 2,
        reason: "reference_only",
      }),
    ).toBe(false);
    expect(
      shouldShowRowRepoGate({
        state: "ready",
        target_count: 2,
        ready_count: 2,
        reference_count: 1,
        reason: "all_targets_ready",
      }),
    ).toBe(true);
  });

  it("maps server gate states to semantic tones and concise row text", () => {
    expect(repoGateTone("ready")).toBe("green");
    expect(repoGateTone("blocked_ci")).toBe("red");
    expect(repoGateTone("blocked_conflict")).toBe("red");
    expect(repoGateTone("blocked_changes")).toBe("red");
    expect(repoGateTone("awaiting_review")).toBe("orange");
    expect(repoGateTone("checks_pending")).toBe("orange");
    expect(repoGateTone("missing_pr")).toBe("orange");
    expect(repoGateTone("reference_only")).toBe("muted");

    expect(
      formatRowRepoGate({
        state: "ready",
        target_count: 2,
        ready_count: 2,
        reference_count: 1,
      }),
    ).toBe("Targets 2/2 ready");

    expect(
      formatRowRepoGate({
        state: "blocked_ci",
        target_count: 3,
        ready_count: 2,
        reference_count: 1,
        blocking_repo: "acme/api",
        reason: "ci_failed",
      }),
    ).toBe("Targets 2/3 · Blocked on acme/api");
  });

  it("formats reason labels and GitHub/GitLab PR labels", () => {
    expect(
      repoGateReasonLabel({
        state: "blocked_ci",
        target_count: 2,
        ready_count: 1,
        reference_count: 0,
        reason: "ci_failed",
      }),
    ).toBe("CI checks failed");
    expect(
      repoGateReasonLabel({
        state: "blocked_conflict",
        target_count: 2,
        ready_count: 1,
        reference_count: 0,
        reason: "merge_conflicts",
      }),
    ).toBe("Merge conflicts");

    expect(repoGatePRLabel("https://github.com/acme/api/pull/42")).toBe("PR #42");
    expect(repoGatePRLabel("https://gitlab.example.com/acme/api/-/merge_requests/9")).toBe("MR !9");
    expect(repoGatePRLabel("https://example.com/change/abc")).toBe("Open blocking PR");
  });
});
