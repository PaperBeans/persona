# Organization snapshot fixtures

These synthetic cases pin the server read contract for issue #11774. They contain no real conversations, machine names, models, paths, or personal data.

- `state-mapping.json` covers the three-state projection and its detailed activity and availability. `expected.state` is deliberately `null` for unknown, offline, and stale runtime state. `expected.availability` describes the runtime source, not whether a durable owner decision exists.
- `tree.json` covers one owner → main → two directions → workers → subagent snapshot, human names, links, goals and review records, organizational-versus-runtime direction conflict, open to-dos, machine and direction concurrency, dated transcript model observations, and complete and incomplete current-P0 paths. `Session.Model` is intentionally different from the transcript observations so the test cannot pass by using the display model as execution evidence.

Each case has `name`, `description`, `source`, `input`, and `expected`. Array order is significant only where the consumer treats it as observation order; ID-based maps are compared by key. Missing values, `null`, `false`, `0`, and empty strings remain distinct.

The Go server replays both files in `server/internal/assistant/organization_snapshot_test.go`. Later iPhone, desktop-web, and other client implementations should reuse the same inputs and expectations for rendering tests; this issue does not implement those clients.

Run:

```sh
go -C server test ./internal/assistant -run 'TestOrganizationSnapshot' -count=1
```

Add cases by hand from the protocol rule. Do not generate expected values from the implementation under test and then treat them as independent truth.
