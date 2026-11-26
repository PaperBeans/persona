# Agent view fixtures

Each listed session carries its five-phase view, `Session.view` (`schema/wire/agent-view.schema.json`, #15397): its
phase, the reason for it, whether its row counts in the Agents list's Needs you, and its rank. The manager computes it
(`server/internal/assistant/agentview`, through `fleetcountadapt.Views`); clients draw it and sort by it, never
deriving their own. These cases pin the view, so every client and the hub agree (`docs/design/PRINCIPLES.md`,
"Consistency first"). The phases, reasons and ranks are `docs/protocol/agent-states.md` ("Five phases").

| Rule | Cases |
| --- | --- |
| Every needs-you case (`spec/fixtures/needs-you`) with its input unchanged: a session whose row counts in Needs you has `counted`, exactly as fleetcount's Agents list decides; muted, quiet, held and folded ones are `needs_you` without it. | the cases named as in `spec/fixtures/needs-you` |
| A session whose task's supervisor holds every card it asked is in no row: `working`, `turn` (D11). | `a_routed_terminal_question_is_held` |
| A child or side folded under its parent: the parent's row stands for its cards (`needs_you`, `counted`); the child is `needs_you` with its own cards, not counted. | `a_childs_card_counts_under_its_parent`, `a_side_counts_under_its_parent` |
| An interrupted session (`status: interrupted`) is `error`, `restart_interrupted`, `resumable` unless its capabilities take neither messages nor a terminal (D-C). | `interrupted_is_an_error` |
| A session whose host does not say (`state: unknown`, or a state this hub does not know) is `idle`, `host_unknown` (D-B). | `unknown_is_idle` |
| Awaiting your reply (`awaits_reply`) is `done`, `asks`, with the question, never counted (D-A). | `awaiting_reply_is_done_and_not_counted` |
| Working, background work and work that only waits; a muted one sorts last among the working. | `working_and_background` |
| Unseen, just finished, seen and not running. | `finished_seen_and_offline` |
| A model error, a usage limit (with when it resets; after the others when it resumes by itself), a refused start; Persona's own Stop is no error. | `errors` |

- Structure: `{"version": 1, "cases": [...]}`. `input` is the needs-you fixture's shape: the hub's `sessions` as the
  manager has decorated them (`state`, `status`, `background_working`, `awaits_reply`, `turn_error*`,
  `start_error*`, `work.just_finished`…), the pending cards as `interactions`, `mutes` and `quiet`.
- `expected.views` lists each session's view, in the order of `input.sessions`, as `{"id": "<machine>/<session>",
  "view": AgentView}`. A sample writes an optional boolean only when it is true, as Go's `omitempty` does.
- All data is made up.
- Run: `cd server && go test ./internal/assistant/fleetcount/fleetcountadapt/ -run AgentView` (the manager's
  computation, and `counted` against fleetcount's Agents list) and `go test ./internal/agentviewwire/` (every view
  reads and writes back through the generated type and holds to the schema); `python3 scripts/test/test_wire_schema.py`
  checks every sample names exactly the schema's fields. The clients' replays come with each client's change (P5a–d of
  `docs/plans/plan-typed-core.md`).
