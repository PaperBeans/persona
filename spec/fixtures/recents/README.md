# Recent setups fixtures

The rules every client follows for New Agent's recent setups (#2134): iOS and Mac (`apple/Shared/RecentSetups.swift`), the web (`web/src/lib/recent-setups.ts`) and Android (`android/feature/agents/…/RecentSetups.kt`) read `recent_setups.json` in their tests. Machines, folders, launchers and models are made up.

## A setup

`{machine, cwd, launcher, model, agent_type, mode, effort, at}`, all strings:

- `machine`: the client's machine key (Apple `MachineRef.path`, the web's machine id, Android's machine id with its group suffix).
- `launcher`: `""` follows the model configuration ("跟随配置").
- `model`: `""` is the launcher's default.
- `agent_type`: `claude`, `codex` or another runtime; `mode` and `effort` are `""` unless it is `claude`.
- `at`: RFC 3339, when it started.

Only these ids are kept, on the device, per hub; never a message.

## Rules

| Rule | Cases |
| --- | --- |
| **Record** a setup that started an Agent: drop any entry with the same machine, cwd, launcher, model, agent type, mode and effort (`at` is not compared), put the new one first, keep the first 20. | `record_into_empty`, `record_moves_the_same_setup_first`, `record_keeps_twenty`, `record_differs_by_model` |
| **Cards**: the entries whose machine the hub still lists, in order, at most 3; each says whether its machine is online. Entries of machines the hub no longer lists are skipped (and age out). | `cards_skip_unknown_machines`, `cards_at_most_three`, `cards_offline_shown` |
| **A machine's model**: when New Agent picks a machine (and its remembered launcher), the model is the one of that machine's latest entry with the same launcher; none: `""` (the default). | `model_of_the_latest_same_launcher`, `model_other_launcher_is_default`, `model_unknown_machine` |

`record` cases give `before` and `setup` and expect `after`; `cards` cases give `entries` and `machines` (`[{id, online}]`) and expect `cards` (`[{index, online}]`, indexes into `entries`); `model` cases give `entries`, `machine` and `launcher` and expect `model`.

# Recently opened conversations

The conversations opened on a device, in the order they were opened, not by their last activity (#2191): the web's ⌘K
shows them first (`web/src/lib/recent-chats.ts`) and the iPhone's home screen menu has the last one
(`apple/Persona/App/RecentChats.swift`). `recent_conversations.json` holds the rules every client follows.

## An entry

A string, the conversation's key as the client keys it; the rules compare keys only. The web: `s:<machine>/<session>`
for an Agent's conversation, `j:<session>` for one of Persona's (⌘K's row keys). Apple: `SessionKey.path`
(`[<hub group>|]<machine>/<session>`, a Persona conversation as well). Only keys are kept, never a title or a message;
on the device, per hub; never sent to the hub or synced.

## Rules

| Rule | Cases |
| --- | --- |
| **Open**: the conversation shown goes first; one already there moves (no second entry); the 10 newest stay; an empty key changes nothing. | `open_*`, `keeps_ten`, `ignores_empty` |
| **Shown**: the entries the client still has (`existing`), without the one on the page (`current`, `""` for none), in order, at most `limit` (`palette`: 5, `home_screen`: 1). The first is the one to go back to. A missing entry stays in the list until newer ones push it out. | `shown_*` |

`open` cases give `before` and `opened` and expect `after`; `shown` cases give `entries`, `current`, `existing` and `limit`
and expect `shown`.
