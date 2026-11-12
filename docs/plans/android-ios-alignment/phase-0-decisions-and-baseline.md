# Phase 0: decisions and baseline (#4208)

Part of the [Android alignment plan](README.md) (#4194). Status: plan, 2026-11-11.

## Requirements

### Goal

Before phases 1 to 3 change code, everyone works from the same decisions and the same picture of where Android stands against iOS and the spec.

### R1. Each decision is recorded where its rule lives

- Each owner decision of 2026-11-11 (the table in the [README](README.md)) gets an entry in `docs/design/DECISIONS.md`.
- The entry lands in the PR that writes its rule into `docs/design/DESIGN_SYSTEM.md`: DECISIONS.md holds no rules, and every entry names the section its rule landed in. Until then, the README's table is the record.

### R2. The open questions get answers

- Question 4, where pending requests sit in a chat. Decided 2026-11-12: as the iOS code does, the request cards stack above the composer, which stays usable, and each folds to a one-line preview. `DESIGN_SYSTEM.md` section 4.8 describes paging instead and is corrected with the change ([phase 2](phase-2-interactions.md), Q4).
- Question 6, an in-app text size on Android. Proposal: Small, Standard and Large, as on iOS, one step below or above the system's size.
- The owner answers in #4208. The answers join the README's table and are recorded as in R1. Nothing that depends on a question is built before its answer.

### R3. A living parity list

- It replaces `docs/archive/redesign/parity.md`, archived since 10-30: 295 rows in three table shapes, with no status per row.
- It lives at a maintained path outside this plan, because it outlives the plan. Proposal: a new parity.md under `docs/android/`, listed in the Docs index in CLAUDE.md.
- One row per user-visible feature or behavior, grouped by area: shell and navigation; assistant chat; Agents list; Agent chat and composer; pending requests; Inbox, mail and Lark; to-dos; computers; knowledge; settings; notifications and system entry points; voice; extensions.
- Columns: Feature | iOS (main file) | Android status | Issue | Notes.
- The Android status is one of: Same; Partial (needs an issue); Missing (needs an issue); Different on purpose (needs a reason, which also goes into the intentional-differences list); Android only.
- Each area says the date and the commit it was last checked against.
- It covers every primary screen, every item in #4194 and in the phase documents, and every row of the old list that still applies.

### R4. An Android audit against the spec

- An audit of the Android app against `docs/design/DESIGN_SYSTEM.md`, in the shape of `docs/design/HIG_AUDIT.md`: the snapshot commit; a verdict; counts by severity and by area; a ranked table of the P1 findings; the same severity definitions (P0, P1, P2); what was checked and the limitations; one entry per finding with its severity, location (file and line), the rule it breaks, the problem, the evidence and the fix; and the findings considered and rejected.
- Scope: the Glass style in light and dark, at the default and the largest font scale.
- Proposal for its home: a new ANDROID_AUDIT.md next to HIG_AUDIT.md under `docs/design/`, listed in the Docs index.
- Every P1 finding gets an issue or joins a phase issue.

### Acceptance criteria

- Every owner decision has an entry in `docs/design/DECISIONS.md` with its date and what it replaced.
- Questions 4 and 6 are answered and recorded.
- The parity list covers every primary screen and every item in #4194; each row has a status and an issue or a reason (principle 4).
- The audit lists findings by severity with file and line, and every P1 finding has an issue (principle 6).

### Out of scope

- Fixing what the audit finds. That is phases 1 to 3, or new issues.

## Technical design

### Decision entries

DECISIONS.md fixes the format: `- **MM-DD Title** (#issue): decision. Rejected: …. Where: section.`, under `## 2026-11`. Drafts:

```text
- **11-11 Needs You is blue on every client** (#4194): the color iOS shows today. Rejected: red, recorded for the chat top bar on 11-06 but never used in code; the web's amber. Where: 2.1.
- **11-11 Android takes the iOS contrast floors** (#4194): links 3:1, white on accent 3.5:1, bubble text 4.5:1, with the iOS color values. Rejected: 4.5:1 for all Android text. Where: 6.
- **11-11 No page swipe or sidebar drag on Android** (#4194): the avatar opens the sidebar and the switcher changes pages. Rejected: the iOS pager and drawer drag. Where: 4.2, 7.
- **11-11 The Inbox swipes as on iOS** (#4194): swipe left for Done, and Move Back in the done list. Rejected: Android's swipe right for Done and left to hand off; Android has no platform convention to keep. Where: 4.2.
- **11-11 One feedback layer on Android** (#4194): one toast, modeled on iOS, for all in-app feedback. Rejected: a Snackbar, per-page hosts and system toasts side by side. Where: 4.3.
- **11-11 Android aligns Glass only** (#4194): Brief and Board get no new alignment work, and the iOS-only styles are not ported. Rejected: porting every style. Where: 2.9, 7.
- **11-11 One blank page on the Android assistant page** (#4208): New Chat is the only blank page, and Back from a conversation goes to the main conversation. Rejected: Android's home page, whose messages went to the main conversation. Where: 4.1.
- **11-11 The Agent chat header as on iOS** (#4208): Back, the title and ⋯; the terminal opens from ⋯ and the work line, the permission mode from the composer's model menu. Rejected: Android's header buttons for the terminal and the permission mode. Where: 3.1, 3.5.
```

Which PR lands each entry:

| Decision | Lands with |
| --- | --- |
| 1, needs-you blue | Phase 1, the token pipeline PR (it also closes DS-01) |
| 2, contrast | Phase 1, the colors and contrast PR |
| 3, no page swipe | Phase 4, the intentional-differences PR |
| 5, Inbox swipe | Phase 2, the Inbox PR |
| 7, one feedback layer | Phase 1, the feedback PR |
| 8, Glass only | Phase 4, the intentional-differences PR |
| 10, one blank page | The chats PR for C11 |
| 11, the chat header | The chats PR for C3 and C7 |

### Parity list

- Seed it from three sources:
  - `docs/archive/redesign/parity.md`. Sections 1 to 23 use the columns `# | Feature | Summary | Key files / source | iOS-specific capability | Android approach | Complexity | Priority`; the rest are the N1 to N73 rows and section 26.
  - `docs/archive/android/android-p0-status.md`, which marks done items with links to their files.
  - The gap lists in [phase 2](phase-2-interactions.md) and [phase 3](phase-3-feature-gaps.md).
- Then check every row against the apps: the iOS views under `apple/Persona/Views/`, and the Android modules listed in `docs/android/android.md` ("Modules and code").
- `scripts/test/test_doc_paths.py` checks every backticked path in docs, so a moved iOS file fails the doc tests instead of leaving a wrong row.
- Optional: a small test that each status is one of the five values and that Partial and Missing rows cite an issue.
- Keeping it current is phase 4's self-check line.

### Android audit

- Method, as for the Apple audit: a static review of `android/` against each section of `DESIGN_SYSTEM.md`, then a look at the screens:
  - screenshots from `scripts/app/android.sh shots` with `SHOTS_STYLE=glass`, in light and dark;
  - the same at the largest font scale (`adb shell settings put system font_scale 2.0`);
  - TalkBack through the primary flows: pair, send in the assistant chat, answer a pending request, open an Agent chat, mark an Inbox item done;
  - animations off (`adb shell settings put global animator_duration_scale 0`), the Android counterpart of Reduce Motion;
  - predictive back and edge-to-edge insets with gesture navigation.
- Checks by spec section: tokens only (2), hit areas and labels (6), confirmation or Undo for destructive actions (4.4), copy (5), the structure rules (3.4, 4.8, 5.6), dark mode and font scale (6).
- Findings are numbered AND-001 upward. DS items in `DESIGN_SYSTEM.md` section 8 that concern Android are referenced, not repeated.
- Size: the Apple audit has 85 findings; expect the same order. Drafting takes one Agent session, then one review by the owner.

### Order of work

1. This document and the README (this branch).
2. The owner answers questions 4 and 6 in #4208.
3. The parity list PR.
4. The audit PR; its P1 findings become issues.
5. The decision entries, each with the PR named above.

### Risks

- The parity list goes stale. Phase 4's self-check line and each area's "last checked" line limit that, and a stale area shows its date.
- The audit overlaps `DESIGN_SYSTEM.md` section 8. It references the DS items instead of repeating them.
