# Dashboards

Dashboards are pages the assistant keeps up to date for the owner and the owner comes back to: a morning-run streak, a
reading list, a weekly watch on prices. A conversation drafts one; the owner keeps it with one tap in the chat; it then
lives in the sidebar under **Dashboards**, and its buttons talk back to the conversation that keeps it. Issue #15017;
phase 1 is described here.

## Who it serves

Everyday people first (the Assistant side): nothing to configure beyond one switch, no Agent, runtime or repository to
pick. The owner asks in plain words ("make me a page to track my runs"), sees a preview, taps **Keep in Dashboards**. The
page reads as one of Persona's own, drawn by the apps' design systems, with a fixed line under its header saying it is
kept up to date by Persona, which the model cannot change.

What a dashboard is not: a one-off answer or this conversation's progress (a live card, `assistant-views.md`), a to-do
or a request waiting for the owner (To-dos and Needs you: a page links to them and never copies their numbers), a
program (no HTML, scenes or images, no code, no network access).

## The switch

Settings › Assistant › **Let Persona make dashboards** (`dashboards` in `GET` / `PUT /api/v1/assistant`; absent or
false is off, and phase 1 ships off). Only a device with full access changes it, and only the owner: the assistant can
read it (`persona_settings` key `assistant.dashboards`, action `get` only) and never change it, so no phone card is ever
asked for it. The apps show the switch only while the hub lists the feature `assistant_pages`, which the built-in
manager announces in its hello whenever it keeps pages, the switch on or off (`docs/protocol/protocol.md`, "Health
check and feature flags").

Off: the `page` tool is listed to no conversation and refuses every call, the places list has no Dashboards row, and
`POST /api/v1/extensions/-pages/-/ui/…` answers 404. Pages already kept stay stored and come back when it is turned on.
On with no kept page, nothing on any screen changes.

## The flow

1. **Draft.** A conversation calls `page create` with a title, an icon, a one-line `why` and the page as a View
   (`server/internal/assistant/pages_tool.go`). The hub checks it against the page floor (`wire.ParseAgentPage`: what a
   live card may draw, at most UI version 10, no html, scene, image, badge or danger tone, no action id starting with
   `persona.`), the title rules and the icon list, stores it as a draft (`db.CreateAssistantPage`), and puts a preview
   card in the calling conversation through `view_show`'s path: the page read-only (buttons, forms and anything else to
   tap left out, its first three sections at most), the `why`, and three choices: **Keep in Dashboards**, **Keep and pin
   to the sidebar**, **Not now**. The tool answers `{id, version: 1, status: "draft"}`, or `{error: "exists", id}` when
   a kept page already has the title, so the Agent updates that one. The model is told to end its turn after create.
2. **Keep.** The owner's tap on a choice is an ordinary card action (`POST /api/v1/assistant/conversations/{id}/views/
   {key}/actions`). The hub applies it inside that request (`applyPageOffer` in `commitViewAction`), before storing the
   card's answer: Keep makes the draft a kept page (`db.KeepAssistantPage`, idempotent by the card), Keep and pin also
   pins it while fewer than three are pinned, then the apps are told (`assistant_pages`) and the sidebar's arrangement
   gets the Dashboards key, and the pinned page's (`navigation`, so the web does not push them under More) before the
   request is answered. Not now declines the draft. Only telling the conversation what the owner chose is asynchronous,
   through the card's outbox as for any card: the place appears even while that conversation's computer is offline and
   the card's answer waits for it. A choice that cannot be made (Dashboards off, 20 pages kept, a kept page with the
   same title) answers 409 with a sentence and leaves the card unanswered. A draft not kept within 7 days expires; its
   card then reads **Expired** and its choices are gone (a stale tap gets the card as it is now).
3. **Use.** The owner opens Dashboards (the index: Pinned, All, Recently removed) or a pinned page from the sidebar.
   A tap on one of the page's own buttons is received in its request (**Received**), then delivered to the conversation
   that keeps the page as the owner's message ending in `<persona-action view="-pages/pg_…" action="…">{"values":…}
   </persona-action>` (the tag the apps already hide), queued after the turn under way: never `ownerInterjects`, never
   `interject_background`. The footer follows it: **Delivered to Persona**, **Persona is working** when that turn
   starts, then nothing once the turn updated the page, or **Persona replied in the chat** with a link.
4. **Update.** The conversation answers with `page update`: the whole View again, with the version it read
   (`version_conflict` otherwise). The apps showing the page draw it again (`extension_ui` invalidate); nothing is pushed.

## Ownership and writers

| Page of | Who writes it | Where taps go |
| --- | --- | --- |
| `main` | Whatever conversation is the main one now: a new main conversation takes over the old one's pages | The main conversation now |
| `conversation` | The ordinary assistant conversation that drafted it, direction leaders included (its machine and public id, `logical_authority.go`) | That conversation; when it is gone, the main one, and the page says so |

Who calls is only the MCP route's machine and conversation, never an argument. The tool is listed only to a
conversation that may write (`guardTools`) and each call checks again. Refused, with a sentence saying why: Inbox
handling chats (`inboxReadOnlyTools`), conversations that write to other people (`outwardOrigins`: Inbox, Lark /agent,
an extension's, and any an extension's token opened), meeting guests, the background supervisor and its rounds
(`roundToolNames` does not list `page`), a scheduled routine's own conversation, conversations on work computers,
spaces (the Health space included), and every conversation of a manager other than the built-in one.

`page list` gives the pages the caller writes; the main conversation also reads the others' kept pages (read only), to
answer what the owner keeps. `page get` gives the page, its version and its last 10 taps. `page remove` needs a reason,
which the owner sees in Recently removed; removing a draft withdraws it (its card says so). Only the owner deletes a
page for good. A deleted conversation's pages go to Recently removed in the deletion's own transaction
(`db.RemoveConversationAssistantPagesTx` in `PutConversationDeletion`) and come back as the main conversation's when
restored; its drafts go unlisted.

Once kept, a page's title and icon are the owner's (the page's **Rename**); an Agent changes the contents only. After
the owner paused a page or restored an older version, writes are refused (`paused_by_owner`) and the model is told to
ask first (phase 2 adds pausing and history; the refusal exists now).

## Titles and icons

1 to 40 characters on one line, without control, direction or invisible characters, and never passing for one of the
app's own places: the reserved names are the three apps' sidebar names in every language
(`server/internal/i18n/locales/*/assistant-pages.json`, kept in line by `scripts/test/test_dashboards_reserved_names.py`),
compared after NFKC and case folding. An Agent's title is refused when it has one in it: anywhere for a name in Chinese
or Japanese ("看收件箱"), as a whole word for a name in Latin letters ("Inbox (2)", "Inbox · work" and "週のToDo" are
refused; "Personal budget" and "Todoist export" are not). The owner's rename is refused only for a title that is a
reserved name. No two kept pages share a title. Icons come from `wire.AgentPageIcons`: the sidebar icons without
settings, security and the app's own places' icons.

## Limits

| Limit | Value |
| --- | --- |
| Kept pages | 20; Recently removed keeps 100, each 30 days |
| Drafts | 3 waiting per conversation, 10 in all, 5 drafted in any 24 hours (declined and expired drafts count) |
| Pinned | 3 |
| A page | 64 KB, 8 detail pages, 300 components a page |
| Writes | 60 an hour a page, 600 an hour for all pages together (both survive restarts); past them `rate_limited` |
| Taps | 10 a minute a page; the same tap within 30 seconds is the first |
| Tap records | 90 days, 200 a page |
| Versions | the latest 20 a page, stored, not shown yet |

## Events

`assistant_pages` (`{}`) when the places change: a page kept, pinned, unpinned, renamed, removed, restored or deleted,
the switch turned on or off. `extension_ui` `{extension: "-pages", id, invalidate: true}` when what a page (or the
index) draws changed: each page's at most once a second, the index's times at most once a minute, every `-pages` event
together within 150 in 15 minutes (`pages_events.go`), well inside what the hub's event log replays. Never a whole page
in an event, never a badge, never a push: neither drafts nor updates count in the card ledger (`assistant.card_ledger`).

## Measurement

Principle 7 (`docs/design/PRINCIPLES.md`): receipt, delivery and execution are three words on the page and three times
in the tap's row (`at`, `delivered_at`, `turn_started_at`, `turn_ended_at`). Once a day the manager logs the day's taps,
queue time (tap to delivered) and turn wait (delivered to the turn that took it up) as p50 / p95 / max, and the turns a
tap preempted, which must stay 0 (`pageTapDayLine`). The tests pin the rest: Keep works with the computer offline, a
tap is received within its request, a thousand writes stay within the event budget.

## Where it lives

| Part | Code |
| --- | --- |
| Storage, limits | `server/internal/db/assistant_pages.go`, `assistant_page_actions.go`, `assistant_page_stats.go` |
| Page floor, titles, icons | `sdk/go/wire/agentpage.go` |
| Places and pages over the extensions' routes | `server/internal/extmgr/pages_hook.go` (reserved id `-pages`, HTTP only) |
| Drawing, owner actions | `server/internal/assistant/pages_ui.go` |
| Taps, delivery, upkeep | `server/internal/assistant/pages_actions.go` |
| Events | `server/internal/assistant/pages_events.go` |
| The tool, Keep, callers | `server/internal/assistant/pages_tool.go`; prompt `server/internal/prompts/mcp/assistant/page.md` |
| Feature flag | `server/internal/hub/assistant_pages.go` |

## What phase 2 adds

Pages a scheduled routine refreshes (`schedule_routine.page_id`, a grant to write the data only), **Refresh now** and
**Pause updates**, the history of versions with restore (which pauses updates), a glance of each page on the index and
the web Overview, alert rules the owner turns on (the only pushes a page ever makes), copying a live card into a page,
and links to a page from a card. Phase 3 attaches pages to spaces, directions and to-dos, and gives task Agents a
data-only `todo_page`. Phase 2 also decides whether the switch turns on by default.
