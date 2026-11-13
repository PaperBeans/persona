# Changed files of a run of work

What a run of an Agent's work changed, by file: the changed-files card at the end of a finished Agent exchange (web,
#5157) and the `changes` a fold carries (`docs/protocol/protocol.md` "Folded work"). The hub's node counts a folded run
(`server/internal/transcript/fold_changes.go` `foldChanges`); the web counts the same calls when they are on the page
(`web/src/views/chat/TurnChanges.tsx` `turnChanges`). Both read these cases, so the card says the same before a reload
(the calls came live) and after it (the page came folded). All data is made up.

## Rules

| Rule | Cases |
| --- | --- |
| An Edit counts the lines Myers' line diff adds and removes between its old and new text. | `edit_one_line` |
| A newline at a text's end ends its last line rather than starting another (as git counts), on a side whose other side is empty or ends in a newline too; otherwise it starts a line, so adding only a final newline is still a change. | `write_trailing_newline`, `edit_adds_a_line_with_newline`, `edit_adds_only_a_final_newline`, `both_sides_end_in_newline` |
| A MultiEdit counts each of its pieces. | `multiedit_sums_its_pieces` |
| Edits of one file sum; files are listed in the order first edited. | `same_file_twice_in_first_order` |
| Paths are cleaned (`./`, `../`, doubled slashes), so a file named two ways is one entry. | `paths_are_normalized` |
| A Codex file change is an Edit with a unified `diff`: lines in its hunks, file headers not counted; without hunks the whole file, added, or removed when `change` is `delete`; an empty diff is the file with nothing counted. | `codex_unified_diff`, `codex_whole_file` |
| Only edits whose result is there and is not an error count; reads and commands never do. | `only_successful_edits` |
| An input the hub clipped for good (a string ending in its "truncated" note) keeps its file with `exact: false`. | `clipped_input_is_not_exact` |
| A run without edits has an empty list. | `no_edits` |

## Format

`{"version": 1, "cases": [...]}`. Each case has `input.items`, the run's items as the hub sends them (`tool_use` and
`tool_result` items, with `tool_name`, `tool_input`, `is_error`), and `expected.changes`, the list in order, each
`{"path", "add", "del", "exact"}`. Paths are compared as the edits name them. The web's card leaves out a run with an
empty list (no card); the hub sends the empty list.

## Checks

- Go: `cd server && go test ./internal/transcript -run TestFoldChangesSharedCases`
- Web: `cd web && npx vitest run src/views/chat/TurnChanges.test.tsx` (each case unfolded, the same case as a fold
  carrying the expected list, and each call's count against the diff its row draws)
- Mock hub: `scripts/mock/mock_hub.py` `fold_changes` counts the same way for its folded pages.

To add a case, write the items by hand with the counts worked out by hand, then run both checks.
