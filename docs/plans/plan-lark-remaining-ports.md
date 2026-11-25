# Plan: the Feishu files still in the core, and the ports that let them move (#13404)

Status: design for review, no code moved. Measured on `origin/main` at 1059104ae64a8616eb7b7011aaa431aa8ab62c85 (2026-11-25).
Audience: maintainers of the server core and of the `lark` module.

## Goal

The Feishu / Lark module (`server/internal/modules/lark`, #13404, tracking #13430) already owns the bot's client, the lark-cli
adapter and connector, the turn rendering, the workbench, the to-do topic lines and the chat's state and inbound path. A batch
of `feishu_*.go` files and the Host adapter that serves the module still sit in `server/internal/assistant`. They cannot move
by copying: they read the `Service`'s own state (`s.chans`, `s.todos`, `s.standing`, `s.workbench`, `s.companions`) and
about thirty core call sites call into them. This plan names the narrow ports that cut those two directions, in a few small
PRs, so the files can move. It follows the shape of `docs/plans/plan-kb-recording-port.md`.

By the owner's definition of Core (UI-free capabilities and interfaces; `docs/architecture/core-and-modules.md`), the port
interfaces and the types they carry are Core: they count against the reduction (section 6).

The state the earlier PRs left, as the issue recorded it after #15000: cards, push and board each wait for "the interaction
port", "the push port" and "the board-rows port"
(https://github.com/nwverse/persona/issues/13404). This plan gives those three ports their shape and adds what the smaller files need.

## 1. Inventory

Measured on the commit above with:

```
git ls-tree -r --name-only origin/main server | grep -i -E 'feishu|lark' | grep '\.go$' | grep -v '^server/internal/modules/lark/'
git show origin/main:<file> | wc -l            # one file's lines; test files end in _test.go
```

133 Go files outside `internal/modules/lark` match: 41 non-test (12,582 lines, the sum of the tables below) and 92 test
files (21,540 lines; 15,706 in `internal/assistant`). Prompt goldens under `assistant/testdata/prompts` (channel_locale.feishu.*,
inbox_triage.larkcli_*, assistant_claude_md.lark_cli) are not counted. The module itself is 13,698 non-test lines
(`find server/internal/modules/lark -name '*.go' ! -name '*_test.go' | xargs cat | wc -l`).

### 1a. In `internal/assistant` (16 files, 5,170 lines): the subject of this plan

"Uses" are the `s.<name>` members of the `Service` the file touches, counted by `grep -o -E '\bs\.[a-zA-Z_]+'` on the file;
only the main ones are listed. "Core callers" are the non-test files outside the Feishu files that call a function the file
defines, found with an identifier index over every non-test `.go` file under `server/internal` (the script is described in
"Re-measuring"). Common identifiers (`later`, `clock`, `notice`, `step`, `target`) match other definitions and are ignored
unless they are the file's own.

| File | Lines | What it does | Uses of the core (`s.…`) | Core callers (file:line) |
| --- | ---: | --- | --- | --- |
| `feishu_cards.go` | 1,159 | An agent's question, permission and plan cards and their answers; the open-in-app buttons and links; wording of an ended card | `botLang` 10, `larkModule` 8, `ixCard` 5, `runExts` 4, `botCardDone` 4, `openLink` 3, `interactionEnded` 3, `channelRuns`, `appCall`, `exitPolicy`, `isMainOn`, `taskOf`, `pendingIx` | `channels_handler.go:66,109,261` (announce, end, done), `reports.go:691`, `fleet_ix.go:1497,1520`, `supervise_leader.go:632`, `fleet_ix_codex.go:124`, `fleet_ix_screen.go:176`, `todo_thread_topics.go:92`; generic helpers also used by `channels_plugin.go:582` (`interactionText`), `asks.go:522` (`answerText`), `ask_route.go:786,854` (`resolutionText`), `acceptance_ask.go:201-205`, `briefing_waits.go:47`, `supervise_digest.go:814` (`todoLink`, `sessionLink`, `webBase`) |
| `feishu_push.go` | 552 | Pushes to the chat (`notify_user`, the finished agent's one card, to-do reminders) and the turn-end sync mirror with its retry | `logf` 6, `botLang` 5, `chat` 4, `openButton` 3, `workbenchOn` 2, `pushedLately`, `markPushed`, `trackPolicy`, `channelLastTurnOf`, `divertAnswer`, `briefMirror`, `taskOf`, `session` | `channels_handler.go:118`, `channels_copies.go:133,135`, `recording_host.go:199`, `todo_plan.go:499,503`, `fleet_push.go:374`, `channels_answer.go:443`, `briefing.go:406,410`, `briefing_fallback.go:112,118` |
| `feishu_board.go` | 467 | The standing "waiting for you" card (state, refresh, put) and the rows and wait wording the briefing shares | `standing` 7, `larkModule` 4, `logf` 3, `boardRef`, `workbenchPin`, `botPostKeyed`, `botUpdateCard`, `loadAcceptLocked`, `visiblePendingInteractions`, `fleetOn` | `boardChanged`: ten sites (`acceptance_loop.go:101,210`, `channels_handler.go:69,109`, ...); `boardRows` `briefing.go:113`, `briefing_card.go:124`; `boardWait` `briefing_card.go:477`, `briefing_waits.go:325,374`; `boardWaited`/`boardFrom` `later.go:248-249`; `awaitingAcceptance` `todo_ask.go:114`, `todo_asks_routes.go:252`, `organization_snapshot_sources.go:484`; `acceptanceRounds` `decision.go:763`; `boardFleetUp` `fleet_events.go:285`; `standingBoard` `service.go:355` |
| `feishu_companions.go` | 295 | Companion bots (a notices bot and a workbench bot): the notice list and long poll, four hub routes | `handle` 4, `larkModule` 3, `companions` 3, `workbenchOn`, `summonWorkbench`, `noticesTaken` | `divertNotices` `reports.go:672`; `divertAnswer` `feishu_push.go:179,351`; `companionRoutes` `service.go:498`; `companionState` `service.go:357` |
| `feishu_workbench_pin.go` | 172 | Pins the home card and keeps it fresh | `workbench` 5, `logf` 5, `boardRef` 3, `appCall` 3, `standing` 2, `postInteractionCard`, `boardPut`, `boardCard` | `workbenchState` `service.go:356`; `workbenchStamp` `lark.go:176`, `lark_chat.go:55` |
| `feishu_overview.go` | 170 | The `/tasks` overview cards (pending decisions, open to-dos) | `todos` 3, `webBase`, `visiblePendingInteractions`, `todoReport`, `overviewTodos`, `fleetMachine` | `tasksOverview` `lark.go:389` only |
| `feishu_progress.go` | 250 | The progress card's content and the folded process panel, read from the channels' live state | `chans` 2 | `progressCard`, `processPanel`, `missingPiecesNote` `lark.go:188-199`; `liveView` `meeting_live.go:90,114`, `livereply.go:209` (a same-named identifier, not checked) |
| `feishu_steps.go` | 202 | Step wording (verb, detail) for a run's steps | none (pure) | `botStep` `channels_runs.go:117`, `channels_watch.go:209`; `botStepParts` `lark.go:188` |
| `feishu_answer.go` | 62 | Answer pagination into at most five cards | none (pure) | `answerCards` `feishu_push.go:155,288`, `lark.go:203`; `botAnswerCard` `briefing_card.go:201,206,272` |
| `feishu_route.go` | 258 | Which conversation a chat message goes to, the run it starts, the labels | `botLang` 5, `todos` 3, `settings` 3, `session` 3, `startChannelRun`, `taskAgents`, `device` | `resolveBotTarget` `channels_plugin.go:270,284,336`; `resolvedTarget` 13 sites (`channels_plugin.go:255,274,278`, ...); `botAllowed` `acceptance_reply.go:69`; `feishuTopic` `todo_source.go:604`; `startBotRun`, `targetLabel`, `todoOfThread`, `threadRoutes`, `openTarget` `lark_chat.go:86-548` |
| `feishu_stop.go` | 119 | The Stop button and the stop command | `botLang` 3, `State` 2, `resolveBotTarget` 2, `interruptBotConversation`, `chans`, `channelRuns` | `stopConversation` `lark_chat.go:262`, `stopProgressCard` `lark_chat.go:267` |
| `feishu_task_titles.go` | 100 | Session and direction names for cards | `fleetMachine` 2, `nodeSession`, `directionSessionNames`, `conversationTitles` | `feishuDirection(s)` `acceptance_ask.go:240`, `conclusion.go:140`; `feishuAgentPlace(Parts)` `feishu_cards.go:548,892,962`; `feishuSessionNames` `lark.go:351`, `lark_chat.go:290` |
| `feishu_transport.go` | 153 | The hub's `feishu.*` calls: post, note, reply, edit, react, read | `appCall` 9, `logf` 3, `workbenchStamp` 2 | `botPostTo` `lark.go:111`; `botNoteTo` `lark.go:115`; `botPost` `bots.go:767`, `bots_chat.go:572`; `botPostContent`/`botReact`/`botUnreact`/`botMessages` `lark.go:160-172` |
| `lark.go` | 415 | The module's layer, made on first use; the sink for the channels; 59 of the `larkHost` methods | `lark` 18, `larkModule` 8, `chans` 5, `setRunExt` 3, `tposts` 2 | `larkModule()` is called from 17 non-test files (`grep -l 'larkModule()' server/internal/assistant/*.go`, tests excluded); `chat()` is the outbound port |
| `lark_chat.go` | 610 | The inbound events (`FeishuMessage`, `FeishuAction`, ...) forwarded to the module; 76 of the `larkHost` methods | `larkModule` 9, `logf` 7, `todoNow` 4, `lark` 3, `targetLabel` 2, `store` 2, `removeTodo` 2, `hello` 2 | `FeishuOwner`/`FeishuRecalled`/`FeishuView` `lark_chat.go:62-68` called from the hub |
| `models_lark_agent_existing.go` | 186 | A Lark `/agent` conversation follows the current model; moves an existing one across runtimes | `store` 3, `saveConversationSwitches` 3, `resolveModel`, `runConversationSwitchesLocked`, `applyModelBinding` | `larkAgentFollowsCurrentModel` `start.go:1480`; `larkAgentInheritedModelKey` `start.go:1501` |

Tests attached to these files (by file name and by what the test drives; coverage was not measured, so the split is an
estimate): cards 1,355 lines (`feishu_cards_test.go`, `_cards_budget`, `_card_back`, `_card_meanwhile`, `_supervision`),
push 1,246 (`_agent_done`, `_push`, `_mirror_retry`, `_todo_reminder`, `_todo_retry`, `_todo_privacy`), board and
overview and workbench 1,758 (`_board`, `_overview`, `_workbench`), answer and progress 875 (`_answer*`, `_pieces`, `_end`,
`_end_locale`), stop and route and titles 910 (`_stop`, `_stale_stop`, `_target`, `_thread_title`), the Lark agent model 294
(`models_lark_agent*_test.go`). The other 8,837 lines in 26 files (`feishu_elsewhere_test.go` 1,887, `feishu_nudge_test.go` 845,
`feishu_thread*_test.go`, `feishu_cot_test.go`, `feishu_stream_test.go`, `lark_chat_test.go`, ...) drive code that already
moved to the module through the assistant's `Service`; they stay until a later pass decides each file's home (section 6).

### 1b. Anywhere else outside `internal/modules/lark` (not moved by this plan, listed for the record)

| Where | Files, lines | What it is | Verdict |
| --- | --- | --- | --- |
| `internal/larkhost` | `larkhost.go` 456 (+64 test) | The module seam: `Outbound`, `Turns`, `Workbench`, `Chat`, `Layer`, `Host` (135 methods, `larkhost.go:162`), `None`, the provider port | Core by classification (`schema/module-classification.txt:154`); this plan edits it |
| `internal/assistantwire` | `larkchat.go` 186; `channels.go` (Feishu types, about 95 mentions) | Contract types for the chat | Core contract; gets the new view types |
| `internal/connectorwire`, `internal/extwire`, `internal/proto` | `lark_reply.go` 39; `lark_avatars.go` 92; `larksent.go` 70 | Wire types of the extension and node protocols | Contracts; unchanged |
| `internal/lark` | `config.go` 75 (+15 test) | `hub.json`'s `lark` block (classified `lark`, `schema/module-classification.txt:261`) | Unchanged |
| `internal/modules/all` | `lark.go` 48 (+126 test) | The composition root's provider wiring | Unchanged, one line per new port |
| `internal/extmgr` | `lark_avatars.go`, `lark_avatar_pages.go`, `lark_image.go`, `lark_reply.go`, `larkcli_profile.go`, `inbox_lark_default.go`: 1,097 src, 2,505 test | The extension manager's side of the Lark extension: avatar and image reads, the reply observation, the lark-cli profile recovery, the Inbox default account | **Not examined in depth.** They serve an extension through generic extension points, not the assistant; a separate plan, outside this series |
| `internal/node` | `larkgate*.go`, `larkcligate.go`: 12 files, 5,349 src, 2,995 test | The node's write gate for gated commands and lark-cli in the assistant's Bash (`node/hooks.go:570`) | **Not examined in depth.** It is the assistant's command gate, with Lark rules; its home is a node-side question (`node_programs` slot, not served yet), a separate plan |
| `hub` | `hub` test files 2 (69 lines) | | No source |

## 2. Seams that exist today

The design builds only on these.

- **The outbound and chat ports**: `internal/larkhost` (`larkhost.go:53` `Outbound`, `:89` `Turns`, `:103` `Workbench`, `:112`
  `Layer`, `:122` `Chat`, `:162` `Host`). `larkhost.None` (`:71`) is the module absent: nothing posts, every post answers
  `ErrNoChannel`, notes are dropped, `Book` runs its function on empty records (`noBook`, `:406`). The core reaches it as
  `s.larkModule()` (`assistant/lark.go:44`) and `s.chat()` (`:55`). Provided by `internal/modules/all/lark.go` under
  `!persona_without_lark && !persona_core_only`; a switched-off module is absent the same way (#14442, `moduleswitch.Port`,
  `larkhost.go:440`).
- **The Host the module calls**: `larkhost.Host` has 135 methods; the core implements it as `larkHost` in `assistant/lark.go`
  (59 methods) and `assistant/lark_chat.go` (76). The module declares the same methods (`modules/lark/layer.go:20`); a mismatch
  fails to build in `modules/all`. Each earlier PR dropped the methods it no longer needed.
- **The chat's records**: `Chat.Book(ctx, f)` hands the core's cards, board and companions the module's state under its lock
  (`larkhost.go:154` region); the earlier issue comment says it shrinks to nothing as these files move.
- **The channels' sinks**: `channelSink`, `channelProgress`, `channelRecall`, `channelTyping` (`assistant/channels_runs.go:25-50`)
  are the generic way a run's answer and progress reach a channel; Feishu is `feishuSink` (`assistant/lark.go:78`), special-cased
  by id at `channels_runs.go:192` and `:369`. Plugin channels use the same interfaces.
- **The generic channel fan-out**, which Feishu sits beside: `channelInteraction` / `channelInteractionEnded`
  (`channels_plugin.go:575,660`), `channelPush` / `channelPushTold` (`:857,872`), `extensionMirror` (`:912`). The core handlers
  call the Feishu function and the channel function side by side (`channels_handler.go:66-69`, `:105-109`, `:118-119`;
  `channels_copies.go:133-139`).
- **The module slots the server serves** (`docs/architecture/core-and-modules.md`, "Slots"): `hub_routes`
  (`modapi.Host.AddHubRoute`, `server/internal/modapi/modapi.go:91`), `background_jobs`, `mcp_tools`, `settings`, `feature`. A module loaded through the
  kernel adds routes only while it is on. `internal/modules/lark` is registered with feature `module.lark`.
- **The guards**: `TestCoreImportsNoModule`, `TestModulesUseOnlyTheSPI`, `TestContractsArePure`
  (`server/internal/archguard`, lists under `archguard/testdata/`). `lark` has no line in `module_services.txt` today
  (`grep -n '^lark' server/internal/archguard/testdata/module_services.txt` prints nothing); none of the ports below adds one.
- **Test helpers**: `needsLarkLayer` and `needsLarkModule` (`assistant/modules_lark_test.go`) skip a test that needs the module.
- **Classification and the e2e gate**: `scripts/ci/module-e2e.py --modules lark` (its `lark` entry is `scripts/ci/module-e2e.py:97`
  and `:165`), `scripts/ci/core-only-smoke.py`.

## 3. The ports

Principle: the core hands the module **data** (contract types), and the module renders and sends. Today the module reaches
back into the core through `larkhost.Host` for the facts a card needs (135 methods); each port below replaces a group of
those reads by one value the core computes, so the Host gets smaller as the files move. A port is an interface in
`internal/larkhost` (a Core package, `module-classification.txt:154`), with a `None` implementation, and its types in
`internal/assistantwire` (a contract, `contracts.txt:7`) or in a new Feishu contract if `assistantwire` would grow past
reason (open question 6). Nothing here changes a wire shape: the `feishu.*` hub calls, the card JSON the bot sends and the
HTTP routes stay byte-identical.

The sketches are shape, not final signatures. Everything the module is "absent" for follows one rule: **the core's value is
the zero value, never a panic**, because each call goes through `s.larkModule()` which is `larkhost.None` in that case.

### 3a. Interactions (cards and answers)

Moves: `feishu_cards.go` (about 800 of 1,159 lines, estimate), `feishu_task_titles.go` (the card-only part, about 40 of 100).
Stays in the core: the parts other core code uses (see "What stays" below).

```go
// larkhost.Asks: what the core tells the chat about an agent's question, permission or plan.
type Asks interface {
	// AskAnnounced: a new interaction (channels_handler.go:66). The core computes the AskView (who asks, which
	// conversation, the task, the web link base, the place names, whether the owner may see its text); the module
	// renders the card and posts it where the owner's switches say.
	AskAnnounced(ix proto.Interaction, v assistantwire.AskView)
	AskAlways(ix proto.Interaction, v assistantwire.AskView)         // reports.go:691: a report's decision card
	AskEnded(ix proto.Interaction, how string, inChat bool)          // channels_handler.go:109
	AskDone(ix proto.Interaction, how string)                        // channels_handler.go:261
	AskDetached(ids []string)                                        // fleet_ix.go:1497,1520; supervise_leader.go:632
	AskCardBack(ix proto.Interaction, v assistantwire.AskView)       // fleet_ix_codex.go:124; fleet_ix_screen.go:176
	AskWanted(ctx context.Context, ix proto.Interaction) bool        // todo_thread_topics.go:92: does a card exist anywhere
	CardOver(ctx context.Context, id string) CardState               // channels_handler.go:255
}
```

- **Implemented by**: the module (it already owns the card state in `chat_state.go` and the presses in `card_actions.go`).
  The core builds `AskView` where it already has the facts (`ixCard`, `ixWho`, `ixTask`, `interactionPlace`, `exitPolicy`).
- **Module absent**: `AskAnnounced`, `AskAlways`, `AskEnded`, `AskDone`, `AskDetached`, `AskCardBack` do nothing;
  `AskWanted` is `false`; `CardOver` answers `cardUnsure` (not "over"), so the core keeps its own record. What the user sees:
  nothing changes, because with the module off no Feishu chat exists to show a card in. The generic text channels still get
  their copy through `channelInteraction`.
- **Reuse instead of invention**: the generic fan-out already calls `channelInteraction` next to `feishuInteraction`. The port
  keeps that shape (a second listener, not a replacement). The ask-resolution paths (`ResolveInteraction`, `ClarifyInteraction`,
  `AcceptAction`) are already `Host` methods the module calls; they stay.
- **What stays in the core** (generic helpers that other code uses): `interactionText` (`channels_plugin.go:582`), `answerText`
  (`asks.go:522`), `resolutionText` (`ask_route.go:786,854`), `endedText`, `finalStatus`; and the link helpers `webBase`,
  `sessionLink`, `sessionPath`, `todoLink`, `openButton`, `openLink`, `openLinkButton`, `safeLinkButton`, `linkNamer`
  (`feishu_cards.go:1003-1122`, about 150 lines; callers include `acceptance_ask.go`, `briefing_waits.go`, `supervise_digest.go`,
  `reports.go`). They are renamed and moved to a generic file in PR 0: they are not Feishu code.

### 3b. Push and notification

Moves: `feishu_push.go` (about 480 of 552 lines, estimate; the `mirrorSend` and `todoReminderSend` data types stay or go to the
contract).

```go
// larkhost.Push: what the core tells the chat to say on its own (not a reply to a message).
type Push interface {
	Push(machine, session, title, body string)                                            // channels_handler.go:118
	PushTold(ctx context.Context, machine, session, title, body, ref string, r *CopyReport) error // channels_copies.go:135
	PushKeyed(ctx context.Context, machine, session, title, body, key string) error       // recording_host.go:199
	TodoReminder(machine, main, title, body string, todo *prompts.BotTodo, send ReminderSend)     // todo_plan.go:503
	AgentDone(machine string, sess proto.Session, head, text, place string, redact bool)  // fleet_push.go:374
	Mirror(ctx context.Context, machine, session string)                                  // channels_answer.go:443
	MirrorPost(ctx context.Context, m MirrorSend, retry int)                              // briefing.go:410, briefing_fallback.go:118
}
```

- **Implemented by**: the module. Needs from the core, as values passed in: the pushes switch, whether the conversation is
  main, scheduled or health, the redaction decision (`s.redactedNotice`, `s.exitPolicy`), the last turn of the conversation
  (`channelLastTurnOf`), the "pushed lately" ledger (`pushedLately`, `markPushed`). The ledgers are the core's and are shared
  with the generic channels; the module asks `Host.PushedLately` / `MarkPushed` (two methods) rather than owning them.
- **Module absent**: all return at once; `PushKeyed` and `PushTold` return `larkhost.ErrNoChannel`, the same value
  `Outbound.Post` returns today, which the callers already treat as "did not go" (callers' handling of the error is **not
  checked** here: PR 2 reads `recording_host.go:199` and `kb` day code first and, if an error would be logged as a failure,
  returns `nil` with a "not applicable" report instead; open question 2).
- **Reuse**: the generic copy report (`copyReport`, `channels_copies.go`) already counts per-channel results. The port
  fills one entry of it; no new report type.

### 3c. Board, workbench pin, overview and companions

Moves: `feishu_board.go` (about 300 of 467), `feishu_workbench_pin.go` (about 170), `feishu_overview.go` (about 150),
`feishu_companions.go` (about 270).

```go
// larkhost.Board: the standing "waiting for you" card, the pinned workbench and the companion bots.
type Board interface {
	BoardChanged()                     // acceptance_loop.go:101,210; channels_handler.go:69,109; ten sites in all
	BoardFleetUp()                     // fleet_events.go:285
	DivertNotices(ctx context.Context, kind string, cards []*botcard.Card, fallbacks []string) bool // reports.go:672
	DivertAnswer(ctx context.Context, kind string, cards []AnswerCard) bool                         // feishu_push.go:179,351
}
// The module serves the four companion routes (feishu/companions, feishu/notices, feishu/notices/action,
// feishu/workbench) through modapi.Host.AddHubRoute instead of Service.companionRoutes (service.go:498).
```

- **Board rows are not Lark code.** `boardRow`, `boardWait`, `boardWaited`, `boardSummary`, `boardFrom`, `boardRowMarkdown`,
  `boardLinkOK` (`feishu_board.go:243-346,448-467`, about 90 lines) feed the briefing (`briefing.go:51-113`,
  `briefing_card.go:85-477`, `briefing_waits.go:319-374`) and `later.go:248-249`. `awaitingAcceptance` and `acceptanceRounds`
  (`:349-402`, about 55 lines) are read by `decision.go:763`, `todo_ask.go:114`, `todo_asks_routes.go:252` and
  `organization_snapshot_sources.go:484`. All of it stays in the core, renamed to a generic file (PR 0); the earlier issue's
  "BoardRows / BoardWait port" is therefore **not a port at all**: the briefing keeps calling plain core functions, and the
  module receives the rows as data (`assistantwire.WorkbenchBoardRow`, already a contract type used by `Host.WorkbenchBoard`).
- **State**: `s.standing` (`standingBoard`, `service.go:355`), `s.workbench` (`service.go:356`) and `s.companions` (`service.go:357`)
  move into the module's own state; `Service` keeps no field for them.
- **Module absent**: `BoardChanged` and `BoardFleetUp` do nothing; `DivertNotices` and `DivertAnswer` return `false` (the
  cards then go to the chat, which is `None`, which drops them: today's behaviour with the bot off). The four routes are not
  registered (the kernel serves a module's route only while it is on, `docs/architecture/core-and-modules.md` "Slots").
  **User-visible**: those four routes answer 404 instead of today's answer from an off bot. Only the Feishu companion apps
  and the web Lark settings call them; open question 1.
- **Reuse**: the board's put/refresh already runs through `Outbound.Post/UpdateCard` and `Host.Setting/PutSetting`; no new
  transport is needed.

### 3d. Progress, steps and answer rendering

Moves: `feishu_progress.go` (about 230 of 250), `feishu_answer.go` (the five-card pagination, about 40 of 62), the Feishu wording
of `feishu_steps.go` (about 100 of 202).

The module's `Host` already declares `StepParts`, `ProgressCard`, `ProcessPanel`, `MissingPiecesNote`, `AnswerCards`, `RunItems`,
`RunSteps` (`larkhost.go` Host, "What the turn rendering needs of the core"): the core renders these **for** the module from its
own run state. They are the cheapest to move: the module can render from the `proto.Item` and `[]string` it is already
handed, so these five Host methods go away with no new port.

- The briefing (`briefing_card.go:201,206,272`) and `feishu_push.go:155,288` split an answer into Feishu cards with
  `botAnswerCard`. After the move the briefing asks the layer, `Layer.AnswerCards(l, name, answer, quiet, steps, foot)`
  (already a `Host` method in the other direction), and gets `nil, nil` with the module absent: nothing to split because nothing
  posts. Pure functions; no state.
- `feishu_steps.go`'s `botStep` is also called by the generic channels' watch (`channels_runs.go:117`, `channels_watch.go:209`),
  so the wording that other channels use stays in the core (about 100 lines, estimate; PR 1 reads it before deciding).
- **Module absent**: nothing renders. User-visible change: none.

### 3e. Routing and stop

Moves little. `feishu_route.go` is the target resolution that **the generic text channels share** (`resolveBotTarget`
`channels_plugin.go:270,284,336`, `resolvedTarget` 13 sites, `botAllowed` `acceptance_reply.go:69`, `feishuTopic`
`todo_source.go:604`). It is the channel framework's, not Feishu's. The proposal is a rename (`botTarget` stays), moving only the
Feishu-specific tail (`readRun`, the chat/thread extensions of `runTo`, `feishuTopic`; about 60 of 258 lines, estimate) behind
`Host.RunTo`-style readers the module already has (`Host.RunExt`, `RunExts`, `RunTo`).

`feishu_stop.go` splits at the card: `stopProgressCard` checks that the callback's message still belongs to a watched Feishu
run (`feishu_stop.go:14`); that check moves to the module, which then calls one core method,
`Host.InterruptConversation(target)`, wrapping `interruptBotConversation`. About 60 of 119 lines leave.

`feishu_task_titles.go` is session naming used by acceptance and conclusions (`acceptance_ask.go:240`, `conclusion.go:140`); it
is not Feishu code and stays (renamed). `feishu_transport.go` (153) is the module calling the hub's `feishu.*` RPC: the module
can call `Host.Call`/`CallApp` directly (already in the Host); the core keeps `botName` and `botPost` for the extension
manager's callers (`bots.go:767`, `bots_chat.go:572`).

### 3f. What does not move

- `models_lark_agent_existing.go` (186 lines): it ties the model profile (`purposeLarkAgent`), the conversation-switch ledger
  and `OriginLarkAgent` (`origins.go:259-289`) together; `start.go:1480` calls it on every send. It is a model-profile feature
  keyed to an origin, not the chat. Recommendation: stays; open question 4.
- `lark.go` and `lark_chat.go` (1,025 lines): they are the **adapter**. They shrink as Host methods are dropped but the
  adapter's skeleton (`larkLayer`, `larkModule`, `chat`, `feishuSink`, the inbound forwarding) is the core's side of the port
  and stays.

## 4. PR plan

Each PR is a move behind a port or a pure rename; default build and wire stay identical. "Core lines out" is non-test lines
leaving `server/internal/assistant`, **before** the lines the PR adds to the port and contract packages (section 6).
Estimates, not measured.

| # | PR | Moves | Core lines out | Behaviour | Depends on | Parallel with |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | Split the generic from the Feishu: move the shared helpers out of `feishu_*.go` into generically named files (link and button helpers, board rows and wait wording, `awaitingAcceptance`, session titles, target resolution, step wording); no signature change | about 550 lines move **within** the package | 0 | none (same package, same names where possible) | none | nothing: it touches the same files as every other PR; land first |
| 1 | Rendering: progress, answer pages, step wording and the transport move; five `Host` methods go (`StepParts`, `ProgressCard`, `ProcessPanel`, `MissingPiecesNote`, `AnswerCards` become module code, `Layer.AnswerCards` for the briefing) | `feishu_progress.go`, `feishu_answer.go`, the Feishu part of `feishu_steps.go`, `feishu_transport.go` | about 450 | none | 0 | 4 (stop/route) |
| 2 | Push port (3b) | `feishu_push.go`, then `Host` drops the pushed-lately readers it no longer needs | about 480 | none when on; with the module off, `PushKeyed` result per open question 2 | 1 (push uses `answerCards`) | 3 |
| 3 | Interaction port (3a), in two steps: 3a announce, end, done, wanted; 3b renderers, detach, card back | `feishu_cards.go` Feishu part, the card-only titles | about 840 | none | 0 | 2 |
| 4 | Board, pin, overview, companions (3c); the four routes through `AddHubRoute`; `Book` is removed | `feishu_board.go` (standing part), `feishu_workbench_pin.go`, `feishu_overview.go`, `feishu_companions.go`; `Service` loses `standing`, `workbench`, `companions` fields | about 890 | companion routes 404 when the module is off (open question 1) | 3 (the board posts through the card renderers) | none |
| 5 | Stop and route tail (3e) | the Feishu part of `feishu_stop.go`, `feishu_route.go`; `Host.InterruptConversation` | about 120 | none | 0 | 1, 2, 3 |
| 6 | Shrink the adapter: delete the Host methods no module code calls any more, the `Book` seam, then `lark.go` and `lark_chat.go` trim | the unused `larkHost` methods | about 600 (estimate; depends on how many of the 135 remain) | none | 1-5 | none |

Order: 0 first; then 1 and 5 together, 2 after 1, 3 beside 2, 4 after 3, 6 last. Two workers can run {1, 5} and {2, 3}
side by side; 4 and 6 are sequential.

**How each PR is verified** (all of them):

- `go build ./...`, `go build -tags persona_without_lark ./...`, `go build -tags persona_core_only ./...`;
- `go test` on `internal/assistant` (with a test database), `internal/modules/lark/...`, `internal/larkhost`, `internal/archguard`,
  `internal/modules/all`, with each of the three tag sets (the `needsLarkLayer` tests skip without the module);
- `python3 scripts/ci/core-only-smoke.py` (the core alone starts and answers) and
  `python3 scripts/ci/module-e2e.py --modules lark` (close and reopen the module);
- core size before and after with `scripts/diag/core_size.py --no-build <before> <after>` (as #15000 did), counting the new
  contract and port lines; the PR body gives the lines both ways and the test-function counts moved and added;
- the pure-move commits are separate from the adaptation commits, so a reviewer can diff the move.

**Tests**: the 6,144 estimated test lines attached to cards, push, board, answer and stop either move with the code into the
module on the fake host (`modules/lark/chat_fake_test.go`) or stay as black-box tests in `assistant` that skip without the
module. Each PR says which for each file; the earlier PRs kept the count of test functions and said so.

**Rollback**: every PR is a revert. The wire, the stored state (`bots.feishu.state`) and the on-disk data do not change, so a
revert needs no migration and a binary built before or after the PR reads the same state. The only PR with a visible effect
(4) is reverted by the same step.

## 5. Risks and open questions

Risks:

1. **The Host is wide now (135 methods).** A PR that adds a port must delete the Host methods it makes redundant, or the
   interface grows instead of shrinking. Each PR states "Host methods: before, after".
2. **Cards read the core in many places.** `feishu_cards.go` touches about 45 `s.` members. The `AskView` must carry the facts, or
   the module calls back into the Host one by one and nothing is cut. PR 3 starts by listing the fields from the code and has
   them reviewed before moving a line.
3. **Locks.** `s.standing`, `s.workbench` and `s.companions` have their own mutexes; `loadAcceptLocked` and the channels' lock
   are taken around some board reads (`feishu_board.go:360-402`). PR 4 keeps each lock with its state in the module, and
   passes the acceptance rounds as data. Lock order is **not** checked here; PR 4 documents it.
4. **Shared names.** `progress`, `notice`, `later`, `clock`, `step` and `target` are common identifiers: the earlier issue comment
   noted it and the caller scan here confirms it (hundreds of matches). A rename in PR 0 must be mechanical (compiler-led,
   one definition at a time).
5. **Briefing and recording call the push functions** (`briefing.go:410`, `recording_host.go:199`): modules calling the core's
   port that calls the Feishu module; the call goes core to port to module, never module to module. Checked only by reading
   the call sites.
6. **The `node` and `extmgr` Lark files** are not covered (section 1b). If the maintainers count them in the module's size,
   this plan's total is smaller than the real distance.

Open questions that need a maintainer or owner decision before code, each with my recommendation:

1. **Routes with the module off.** `PUT /api/v1/assistant/feishu/companions`, `GET .../notices`, `POST .../notices/action` and
   `POST .../workbench` (`feishu_companions.go:200-274`) are registered by the core today and answer from an off bot. Serving
   them through `AddHubRoute` makes them 404 when the module is off or built out, as the contract says ("a disabled module is
   absent"). I did not check what each answers today with the bot off. *Recommendation*: 404 (the contract); the only callers
   are the companion bots and the Lark settings, both gone with the module.
2. **`PushKeyed` and `PushTold` with the module off.** Return `ErrNoChannel` (what `Outbound.Post` returns now) or `nil` and a
   "not applicable" entry in the copy report. Callers' handling is not read yet. *Recommendation*: `nil` plus a report entry
   `skipped`, so a day recording's summary does not log "push failed" on a hub that has no Feishu; PR 2 confirms by reading
   `recording_host.go:199` and its caller first.
3. **Are the board rows and the link helpers "Lark code"?** I classify them as the core's (briefing and acceptance use them).
   If the owner wants a `board` module instead, they are not Lark's to move. *Recommendation*: core, generic names.
4. **`models_lark_agent_existing.go`.** Keep in the core as "an origin follows its purpose's model", or move with a new
   hook (`Layer.AgentConversation`). *Recommendation*: keep; it is a model-profile feature and `start.go:1480` is on the hot
   send path.
5. **Do the `node` gate and the `extmgr` Lark files belong in this series?** *Recommendation*: no; they serve extensions and
   the assistant's command gate, and need their own plan with the maintainers of those packages.
6. **Where the new view types live.** `assistantwire` (`contracts.txt:7`) or a new Feishu contract. `assistantwire` already has
   the chat types (`larkchat.go` 186 lines, about 95 Feishu mentions in `channels.go`). *Recommendation*: `assistantwire`
   while the additions stay under about 400 lines; a new contract package beyond that. Either counts as Core.
7. **Do contracts count in the reduction?** (the same question as #13407). *Recommendation*: yes, as the owner's definition says;
   the PR bodies give both numbers.
8. **The 8,837 test lines that drive already-moved code through `Service`.** Move to the module with a fake host, or keep as
   black-box tests. *Recommendation*: decide per file in PR 6, after the fake host proves itself on PRs 1-5; none of this
   changes behaviour, only where the test lives.

Behaviour, prompts and wire format: nothing in this plan changes a prompt (they stay in `server/internal/prompts/`), the wire
types or the card JSON. The one visible change is question 1; question 2 is internal.

## 6. Expected core reduction

Estimates from section 1 and 3 (not measured); `scripts/diag/core_size.py` needs a Go toolchain and was not run for this
document.

| Item | Non-test lines |
| --- | ---: |
| Feishu code leaving `internal/assistant` (PRs 1-5: about 450 + 480 + 840 + 890 + 120) | about 2,780 |
| Adapter shrink (PR 6) | about 600 |
| **Leaving the assistant package** | **about 3,400** |
| Added to Core: the port interfaces in `internal/larkhost` (`Asks`, `Push`, `Board`, their `None` methods) | about 180 |
| Added to Core: the view and result types in `assistantwire` | about 250 |
| **Net, contracts and ports counted as Core** | **about 2,970** |
| Test lines that move with the code (cards, push, board, answer, stop; section 1a) | about 6,100 |

What stays in the core after the series, about 1,770 lines of the 5,170 (estimate): the adapter skeleton, the link and button
helpers, board rows, target resolution and session titles (renamed, generic), the model feature (186). It is what the generic
channel framework, the briefing and acceptance still need. The module grows by about the moved amount.

## Re-measuring

```
# files and lines (section 1)
git ls-tree -r --name-only origin/main server | grep -i -E 'feishu|lark' | grep '\.go$' | grep -v '^server/internal/modules/lark/' \
  | while read f; do echo "$(git show origin/main:$f | wc -l) $f"; done
# a file's uses of the Service
grep -o -E '\bs\.[a-zA-Z_]+' server/internal/assistant/feishu_cards.go | sort | uniq -c | sort -rn
# the Host's width
awk '/^type Host interface/{f=1;next} f&&/^}/{f=0} f&&/^\t[A-Z][A-Za-z]*\(/{n++} END{print n}' server/internal/larkhost/larkhost.go
# core callers of a file's functions: index every identifier of every non-test .go file under server/internal
# (outside internal/modules/lark), then print, per function the file defines, the files and lines that name it
# (comments skipped, the function's own definition skipped). Read the hits: common names match unrelated code.
```

Not checked: what each companion route answers with the bot off; the callers' handling of `ErrNoChannel` from push; the lock
order around the board; the `node` and `extmgr` files beyond their headers; coverage-based test attribution.
