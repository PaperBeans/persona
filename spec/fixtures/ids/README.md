# Wire ID fixtures

One example of each ID in `schema/wire/ids.schema.json` (#15397) and of `AgentRef`, the one shape for "points at an
Agent". Values are made up. `expected` is `{definition, value}`: an ID is a bare string on the wire, an `AgentRef` is
`{machine, session}`, both required. `scripts/test/test_wire_schema.py` holds each to the schema; the generated types
read each back unchanged (Go `server/internal/wireids/ids_gen_test.go`; Swift, Kotlin and TypeScript tests follow the
same fixture).
