# Architecture

The phone connects to the hub directly: HTTPS / WSS over the public internet (TLS + public key pinning), with HTTP / WS kept for the LAN / VPN. Public access prefers IPv6; the VPN is set up by the user. See the [network doc](../operations/network.md).

## Goals

- On the phone (and watch), talk to Claude like in a chat app, but Claude itself is always an **interactive Claude Code** inside herdr on some machine:
  open `herdr` at the computer and it is the same session, so you can take over seamlessly; the phone is just another "terminal".
- Pick a launcher at start (`cc_ds` / `cc_max` / `ccc` … from `~/.zshrc`; in essence this picks the model and gateway).
- See the chat as it happens on the phone: text, thinking, tool calls and results, the current screen.
- Every step that needs a person (AskUserQuestion, plan approval, tool permissions, trust-folder and other TUI dialogs) can be handled on the phone / watch.
- Claude can use the iPhone's data (contacts, calendar, reminders, photos, health, location, clipboard…) and mail.

## Components

```
 phone / watch ─┐  HTTPS/WSS (public internet, clients that support pinning)
                ┤  HTTP/WS (LAN / VPN, compatible with older apps)
                ┤ ─────────────────────────►  personad hub (HTTP + optional TLS)
                │                                   │  ├─ session index, event fan-out, push delivery
                │  WS: tool.call ◄──────────────────┤  └─ manager (`personad assistant`, the hub's child by default):
                │      (phone data tools)            │       interactions (questions / plans / permissions), mail (IMAP/SMTP)
                                                    │
                     stdio JSON-RPC over `ssh <host> personad node --stdio`
                     (on the hub machine itself, started directly as a child process)
                                                    ▼
                              personad node (one per machine, lifetime follows the hub's connection)
                               ├─ herdr socket API (~/.config/herdr/herdr.sock, CLI fallback): create tab, start claude, prompt,
                               │   send-keys, read, agent status; events.subscribe pushes changes
                               ├─ tail ~/.claude/projects/*/<session>.jsonl → structured events
                               ├─ unix socket ~/.persona/node.sock ◄── personad hook (Claude Code hooks)
                               │                                 ◄── personad mcp (MCP tools for claude)
                               ├─ unix socket ~/.persona/runtime.sock ◄── runtime: Agent runtimes without a terminal (DSH's web plugin, docs/integrations/runtimes.md)
                               └─ launcher list: zsh -ic (`_claude_launchers` + aliases that call claude)
```

The hub keeps `listen` (HTTP `:18660` by default); TLS is enabled separately with `public.tls_listen`, on a port the user picks. Both listeners share the API and the source-based auth rules. The master token is only accepted from loopback / LAN sources; the public internet uses temporary pairing tokens or device tokens. The public response of `health` contains no version, capabilities or candidate addresses.

Candidate addresses come from config and the local network interfaces, ordered IPv6, domain names, LAN, public IPv4. After auth they are sent through `/api/v1/endpoints` and the WS `hello`; address change events go only to connections that declare the `endpoints` capability. An address on an interface does not mean the modem or firewall lets traffic in; it still has to be verified from outside. Existing iOS / Watch apps, without code changes, keep using the original LAN / VPN address.

A single binary `personad`, four modes:

| Mode | Where it runs | What it does |
| --- | --- | --- |
| `hub` | An always-on machine, resident under systemd --user | The app's API, event fan-out, push delivery, forwarding to the managers (the built-in manager keeps the interactions and mail and connects to the nodes) |
| `node --stdio` | Every machine, started by the manager over SSH | Relays local herdr / claude / transcript / hooks |
| `hook <event>` | Called by Claude Code hooks | Hands events to the node; for things a person must decide (questions, plans, permissions) it blocks waiting for the phone's answer |
| `mcp` | Claude Code MCP (stdio) | Exposes phone data, mail and other tools; a phone tool's call goes node → manager → hub → phone |

## Why it is designed this way

- **No `claude -p --output-format stream-json`**: that is a one-shot headless mode; you cannot take over at the computer, and there is no TUI interaction. What the user wants is
  "a claude inside herdr on the server / Mac".
- **Structured content comes from the transcript JSONL**: Claude Code appends a line as each content block (thinking / text / tool_use / tool_result) completes,
  so tailing it gives a reliable structured stream. **Character-by-character in-progress output** comes from `herdr pane read` (screen sampled every 400 ms, only while someone is subscribed and the agent is working),
  shown in the app as the "live screen" strip. The two combined = real-time streaming.
- **Interaction goes through hooks, not simulated keystrokes**: a hook gets the question / plan as structured data, and the answer is structured too (tested, see `docs/integrations/claude-integration.md`).
  Simulated keystrokes (`herdr agent send-keys`) are only a fallback: for any TUI dialog hooks cannot cover (trust folder, model picker…), the app shows a screen snapshot +
  a key panel.
- **Hooks are injected only into sessions Persona starts** (a plugin loaded only for that session, `--plugin-dir ~/.persona/claude-hooks`; older Claude Code uses
  `--settings ~/.persona/claude-settings.json`). Global settings are not changed, and claude sessions the user opens themselves are not affected. A plugin is used because `--plugin-dir` stacks,
  while `--settings` only honors the last one and would override the launcher's own (gateway, key, model; `docs/integrations/claude-integration.md` ("Hooks")).
  herdr claude sessions not started by Persona can still be viewed in the app (the transcript is found by the session id herdr reports), sent messages and answered with keys.
- **node over SSH stdio**: reuses existing aliases such as `ssh <machine>` from `~/.ssh/config`; no new ports, no new auth surface; the hub reconnects automatically when the connection drops.
  Computers SSH cannot reach (behind NAT, on another LAN) fall back to tailcat hole punching (`docs/server/server.md` ("tailcat hole punching")): SSH first, tailcat if that fails.
- **An Agent without a terminal is a runtime, not a host**: the host is "where the terminal is open" (herdr / Herdr App / Orca, one engine per machine, `docs/integrations/engines.md`), and the node recognizes sessions from panes and
  transcripts. Agent runtimes like DSH, which manage their own sessions and have no terminal, plug in through the runtime protocol on `runtime.sock`: they report their own sessions, push content, ask the phone,
  and coexist with the engine this machine picked. The hub and app need no changes, and new runtimes need no changes to Persona either (`docs/integrations/runtimes.md`: protocol + SDK + acceptance checklist).
  Paseo is the exception: it has no terminal and connects through the runtime protocol (the node runs its bridge itself), yet it is an engine a machine can pick, and both new and resumed sessions are handed to it (`docs/integrations/paseo.md`).
- **The hub is on an always-on machine**: it is always online and can reach Gmail (verified: imap.gmail.com:993 / smtp.gmail.com:465 reachable directly), and every Mac is relayed through it.

## The hub is only a communication center

The hub only handles communication: app and other clients ↔ managers, hub ↔ hub (linked hubs), connections (WebSocket, node / connector links), pairing and auth, the admin UI, push delivery, and various relays.
Business logic lives in the managers (`server/internal/assistant`, `devices`, `extmgr`; see `docs/server/machines.md`, `docs/extensions/extensions.md` ("Who owns what"), `docs/assistant/assistant.md`):
the data for the assistant, Inbox, to-dos, usage limits, board, extensions and devices, plus model calls (triage, titles, summaries, judgments). The hub forwards these endpoints to the managers as-is (`assistantwire.Routes`, `devicewire.Routes`, `extwire.Routes`), except the merged lists, which it joins, adding up the to-dos' `counts` and joining their places over the JSON as the managers send it (`server/internal/hub/ownership_lists.go`, `server/internal/hub/todo_view.go`); it never works out a to-do's phase. A few relayed writes keep an operation receipt at the hub (`client_ops`): a meeting's Just me and follow-up (`server/internal/hub/meeting_receipts.go`), and the answer to a to-do's ask and a message to a to-do on its conversation's route (`server/internal/hub/todo_receipts.go`); the hub keeps the manager's reply for the same op id, while the manager decides the answer. For the two to-do writes the hub also says on the socket that it has the write (`todo_send_state`) before the manager answers, as it does for a session's message (`message_send_state`): transport only, never the to-do's state.
An ordinary route goes as one `http.request` on the bus, its body read whole (a body of 1 MiB or more goes on a relay stream of its own beside the bus, `http.request_streamed`, so the bus is not held up behind it: `docs/protocol/protocol.md` "Forwarding App requests"); a route that upgrades to a WebSocket cannot, so the extensions' routes and the assistant's
WebSocket routes (`assistantwire.SocketRoutes`: a meeting's audio socket, `docs/assistant/meeting-minutes.md` "Audio from the phone") go over a relay stream instead
(`extwire.MethodAccept`): the manager dials the relay and serves the request there, and later ones while the hub keeps the stream open (`docs/extensions/extensions.md` ("Who owns what")), and the manager's extensions layer hands the assistant's to it
(`extmgr.Options.AssistantSocket`). Who asked goes in the hub's headers, the caller's credential stays at the hub, and revoking the device ends the socket.

`server/internal/hub/architecture_guard_test.go` pins down what the hub looks like now and fails if it grows in the wrong direction:

- **Routes the hub answers itself** = `testdata/hub_routes_allowed.txt` (registered in the hub package with a string literal, or a loop over a literal string list, and not in the forwarding tables: the three above, plus `assistantwire.InboxRoutes`, `assistantwire.SocketRoutes`, `devicewire.ShareRoutes`, `devicewire.MetricsRoutes`, `devicewire.TransferRoutes`, `devicewire.UsageRoutes` and `devicewire.AdminUsageRoutes`). A loop over any other name fails the guard. New business endpoints go in a manager,
  and the route is added to the matching forwarding table; only routes that truly belong to the hub (connections, pairing / auth, admin, push delivery, relays) get a line added to the list by hand. Removing a route means removing its line too (extra lines fail as well).
  While a piece of business logic is being moved into a manager, its line can be marked `# moving to the manager` at the end, and the line is removed once the move is done (usage windows `docs/server/usage-windows.md` ("Who owns this") and the workspace board `docs/web/workspace-board.md` ("Who owns the board") have both been moved).
- **Dependencies**: the hub's dependencies must not include manager packages (`assistant`, `assistantskills`, `devices`, `extmgr`, `extension/host`, `agents`, `mail`, `office`, `feishubot`, `wechat`, `node`, `mcpserver`),
  the knowledge base (`kb`), the connectors (`connector` and everything under it) or the model client (`ai`): the types it carries for them come from the contract packages below.
- **No model calls**: the hub imports nothing of `internal/ai` (a model setting it hands on is an `aiwire.Config`), and no model API addresses appear in its code.
- **Store** methods = `testdata/hub_store_methods.txt`: new business tables go in the manager's own storage, not in `hub.Store`.

The lists are edited by hand, with no auto-rewrite switch: the changed line shows up in the PR diff, so a reviewer can see whether it really belongs to the hub.

## Contract packages

The parts (the hub, the managers, the node on each computer, extensions) talk only through contracts, and the Go packages that define them hold only types, constants and trivial pure helpers:

| Package | What it carries |
| --- | --- |
| `assistantwire`, `devicewire`, `extwire` | The bus between the hub and a manager: methods, events, routes the hub relays, extension scopes and manifest types the bus carries; in `assistantwire` also the To-dos view, counts and places, generated from `schema/wire/todo-view.schema.json` (`server/internal/assistantwire/todo_view_gen.go`, never edited by hand) |
| `kbwire` | Knowledge-base entities, identity hints, updates and chat context as they cross the bus and the connector protocol |
| `recordingwire` | A meeting recording's data types (the recording, its upload and timeline, meeting.json, minutes, day recap, photos, live log, meeting room, hub segments), their constants, sentinel errors, pure accessors and lenient JSON readers; `kb` aliases and re-exports them and keeps its own rules as functions over them (`docs/plans/plan-kb-recording-port.md`) |
| `connectorwire` | The connector SPI: `Connector`, its `Manifest` and capability interfaces, statuses, sign-in prompts, threads and recall errors; inbox things and replies, conversation batches, posts and snapshots from connectors (`docs/protocol/connector-protocol.md`, `docs/extensions/connectors.md`) |
| `aiwire` | A model setting (`aiwire.Config`) |
| `dialogwire` | A terminal dialog read off a screen, as the node and the apps see it |
| `repositorywire` | A task's requested refs, repository layouts and the preparation snapshot a manager hands to a node, with the alias and ref rules both apply; `assistant/repository` aliases them |
| `nodeproto` | The node protocol handshake: the protocol version and capability names a node announces (`docs/protocol/protocol.md` "Node handshake") |
| `proto`, `rpc`, `relayproto` | The client API and node protocol types, the JSON-RPC framing, the push relay |
| `githubwire` | What the extension manager and the GitHub connector module pass (manager.json's `github` section, the token source, the CI-check snapshot the assistant reads) and the `Connector` the manager drives (`docs/architecture/core-and-modules.md` "Provider seams") |
| `imessagewire` | What the extension manager, the node and the iMessage connector module pass (manager.json's `imessage` section, the `Caller` to a Mac's node and its errors) and the `Remote` the manager drives and the node's `connector.*` handler (`docs/architecture/core-and-modules.md` "Provider seams") |
| `mailwire` | What the extension manager and the mail module pass (accounts, messages, sends) and the `Service` the manager reaches mail through (`docs/architecture/core-and-modules.md` "Provider seams") |
| `modapi` | The feature-module contract: a module's manifest, its registration and the `Host` it reaches the core through (`docs/architecture/core-and-modules.md`) |
| `sdk/go/wire` (the SDK's module) | The extension protocol's types generated from its schema (an error report the bus carries, say), shared with extensions written against the SDK |

The business packages alias the contract types so their callers keep the old names (`type InboxThing = connectorwire.InboxThing` in `connector`, `type Update = kbwire.Update` in `kb`).
A contract type cannot carry business methods: logic moves into the business package as a function (`kb`'s `tagUpdate`, `screen`'s `hash`).
Where the business type needs fields of its own, it stays a separate type with the same JSON and a converter (`kb.Item` keeps store bookkeeping; connectors hand over a `kbwire.Item`, and `kb.ItemFromWire` turns it into one; `kb`'s `TestItemMatchesTheContract` keeps the two in step).

`server/internal/archguard` (tests only) keeps it that way:

- **Pure contracts**: every package in `testdata/contracts.txt` depends only on the standard library and other listed packages (the SDK's `sdk/go/wire` is listed by its full import path), and every package named `*wire` must be listed.
- **Part boundaries**: the hub (`hub`, `hubedge`) depends on no manager (`assistant`, `devices`, `extmgr`, `manager`) or node package, a manager on no hub package, and the node (`node`) on neither.
  Known violations would go into `testdata/boundary_allowed.txt` (shrink-only, each line naming the step that removes it); it is empty, and a line that no longer applies fails too.
- **Core and modules**: no core package imports a feature module (`internal/modules/<id>`), and a module imports only
  `modapi`, the contract packages and the modules it needs (`TestCoreImportsNoModule`, `TestModulesUseOnlyTheSPI`;
  `docs/architecture/core-and-modules.md` "Guards").
- **Reap first**: every production `osproc.ReapInherited` call (the hub's `claimInherited`, the manager's `runManager`, `managercli.Assistant`, the node's `runNode`) comes before anything that could start a child, and the hub's `Run`, `Serve` and `RunSlot` call `claimInherited` first (`reap_test.go`).
  Only the calls its list names, each with why it starts no process, may run ahead; a new call site has to be listed.

## Domain model

Domain code on the server is strict, and the rules are code: each one below is a check in
`server/internal/archguard/domainrules`, with a failing case and a compliant twin under
`server/internal/archguard/testdata/domainrules/planted`, a tree laid out like `server/`. Every Go example in this
section is copied from a compliant twin there, and archguard's `TestDomainModelExamples` keeps them the same. New
domain code follows the rules from its first line. Code that predates them is held by shrink-only counts and frozen
names in `server/internal/archguard/testdata/domain_layers`, which only fall: copy the domain kit
(`server/internal/domainkit`), the first shared kernel (`server/internal/agentref` and its adapter) and the planted
twins, never the packages those lists hold. A new domain package starts from the scaffold,
`python3 scripts/new_domain_slice.py go <dir> --doc "<one sentence>" [flags]`, which writes the compliant shape with
its black-box tests and its hook; its output with every flag but `--shared` is the golden
`server/internal/archguard/testdata/scaffold`, which archguard's `TestScaffoldGoldenPasses` holds to the rules and
`scripts/test/test_new_domain_slice.py` writes again and compares byte for byte, then compiles, vets and tests in a
temporary module together with the flag combinations its `COMBINATIONS` table lists (the package's own types and the
shared kernel's as parts and payloads among them), each through its own rules hook.

Each sentence below states a rule, and the rule holds whether or not a check sees a breach. The checks read syntax
only (`go/ast`, no type information) and enforce each rule where the source shows it: in names, declarations,
signatures, literals, imports and the calls they can resolve. The routes they do not see yet are listed in #15088, and
the type-aware check #10613 plans with the fleetcount core (its item 5.2) closes more of them. A route found later is
a bug in the check, never permission to take it.

Why the rules exist, in this repository's own code:

- **One type plays three roles.** `InboxItem` (`server/internal/assistant/inbox.go`) is the wire shape, the stored row
  and the domain type at once, with exported, json-tagged fields (`awk '/^type InboxItem struct/,/^}/'
  server/internal/assistant/inbox.go`), so callers decide from its fields:
  `git grep -nE '\.(DoneAt|DoneBy) = ' -- 'server/internal/assistant/*.go' ':!*_test.go'` lists writes of an item's or
  a to-do's done state, each with its own meaning.
- **Validating constructors are skipped.** `sessionhistory` validates input in `Parse`, yet
  `git grep -nE '[^A-Za-z0-9_]sessionhistory\.[A-Z][A-Za-z0-9_]*\{' -- server` lists literals of its types built
  outside it.
- **A named primitive is only a name.** `git grep -nE '^type [A-Z][A-Za-z0-9]* (string|int|int64|bool)( |$)' --
  'server/*.go' ':!*_test.go'` lists the server's named basic types; any package converts into them and `string(x)`
  unwraps them.

### Layers

Every package under `server/` states its layer on a line of its own in its package comment. go/ast leaves such
directive lines out of a comment's text, so godoc does not show it:

```go
// Package fleetcount decides which agents and cards need the owner, and why: the Needs you count every surface
// shows.
//
//persona:layer core
package fleetcount
```

| Layer | What it is | Where | May import |
| --- | --- | --- | --- |
| `kit` | the building blocks every core uses | `server/internal/domainkit` only | the standard library a core may import, the other kit packages, and `time` in `instant` |
| `core` | one bounded context: values, entities, deciders, failures, events, domain services and its visitors | the context's home (`assistant/<domain>`, `node/<domain>`), and the legacy top-level packages | `bytes`, `cmp`, `iter`, `maps`, `math`, `math/bits`, `slices`, `sort`, `strconv`, `strings`, `unicode`, `unicode/utf8`; the kit's `outcome`, `fail`, `instant`, `opt`, `defect` and `slot`; shared kernels |
| `core shared` | a shared kernel: value objects and closed sets only | directly under `server/internal`, outside the managers', the node's and the hub's trees | as a core |
| `adapter` | the only code that converts between a core and contracts, rows, prompt inputs or log text | `<core>/<core>adapt`, and the edge adapters such as the HTTP failure writer | its core, shared kernels and their adapters, the kit, contracts, libraries, the standard library, third-party code |
| `application` | orchestration: the clock, ids, IO, locks, goroutines, routes, retries and model calls | package `assistant`, the hub, the node, `cmd/*`, the managers, `domainkit/clock` | every layer but `testkit` |
| `application seam` | a typed registration seam between application modules | `assistant/kernel` and later seams | as application |
| `library` | technical code with no domain concept; its records and named primitives are counted as application code's | `lazyre`, `osproc`, `db`, `prompts`, `apierr`, `i18n` and the like | the standard library, third-party code, libraries, contracts |
| `contract` | plain wire data | exactly the packages `server/internal/archguard/testdata/contracts.txt` lists | the standard library and contracts |
| `testkit` | test support, never a program (`package main`) | `domainkit/domaintest`, `domainkit/clock/clocktest`, `*test` packages | anything; imported only by tests and other testkit |

A declaration is checked, or it would be a way around the rules: `application` must show an effect use (a call into
`os`, `net`, `net/http`, `database/sql`, `os/exec`, `log`, `log/slog`, `syscall`, `crypto/rand`, `math/rand` or an
application package, a clock call, a goroutine, a channel, a select, or a field or parameter typed from `net/http`,
`database/sql`, `net`, `os`, `os/exec` or `sync`; a `context.Context` parameter or an adapter import alone is none),
`kit` lives only under `server/internal/domainkit`, `contract` if and only if `contracts.txt` lists the package,
`core shared` holds only values, a library imports no core, kit or adapter and is counted like application code (so
declaring `library` holds no domain concept either), an adapter's named basic types are counted as application code's
and it exports no named collection, func or chan type, alias or constant (so declaring `adapter` exports no raw shape;
where an adapter lives is not checked yet, #15088), and testkit is imported only by tests and other testkit and is no
program (`package main`, which nothing imports), so its code reaches no binary. A core is imported only by application
code, adapters, testkit and tests (a shared kernel by cores too). The hub (`internal/hub`, `internal/hubedge`,
`cmd/persona-hub`) relays contracts and imports no core, kit or adapter, and neither do `db` and `prompts`, libraries
the hub links. A manager is application plus adapters, with its cores in its own tree; the node is application plus
engine adapters, with any core under `node/<domain>`.

A package that declares nothing counts as application. The packages that declared nothing when the rules landed are
frozen by name in `server/internal/archguard/testdata/domain_layers/undeclared.txt`; a new package has to declare, and
a frozen one that gains its line prints a notice, after which its line can go. The packages open PRs were adding come
from `python3 scripts/test/name_freezes.py pending --write`.

### Values

One value object per concept: a struct whose state is unexported, made only by its own package. A one-field struct names
its field for its type in lower camel case (an `ID` suffix may go: `MachineID{machine}`), and no two exported structs of
a package share a field list, so no value converts into a sibling (`fleetcount.Role(state)` does not compile). There are
no named primitives, aliases or conversions from outside, and a struct with more than one field holds no primitive, a
kit type's argument included (a field of `opt.Option[string]` holds a `string`). A name of the package's own, exported
or not, is what it names: a field of `type label = string` or `type label string` holds a `string`, and a parameter of
`type rows []row` is a slice. Every exported struct and sealed
interface documents its zero value ("The zero Ref is no session"); where the zero is not a valid value, constructors
refuse it and functions handed it raise a defect. Absence inside a core is `opt.Option`, never a nil pointer or a zero
marker.

```go
// MachineID is one of the owner's computers. The zero MachineID is no machine; ParseRef never makes one.
type MachineID struct{ machine string }

// SessionID is one session on a machine. The zero SessionID is no session; ParseRef never makes one.
type SessionID struct{ session string }

// InteractionID is one pending card. The zero InteractionID is no card; ParseInteractionID never makes one.
type InteractionID struct{ interaction string }

// Ref is one session on one machine. It is comparable, so it keys maps. The zero Ref is no session; ParseRef never
// makes one.
type Ref struct {
	machine MachineID
	session SessionID
}
```

```go
// State is where an agent's turn stands. The zero State is no state; Build refuses it.
type State struct{ state uint8 }

// StateWorking is an agent at work.
func StateWorking() State { return State{state: 1} }

// StateNeedsYou is an agent waiting for the owner.
func StateNeedsYou() State { return State{state: 2} }
```

### Tell, don't ask

Code outside a core never reads or writes its state. A core has no exported fields, package variables or exported
constants, and every exported method returns nothing, a bool, the receiver's own type or an outcome of it, whatever
its parameters. The exceptions are `ID()` (the package's own `<X>ID` type, or a shared kernel's, which other
aggregates and stores refer to it by), `Wire()`, `Build()` on a builder (an outcome of the package's type), a builder
stage's next stage and `Accept`. A computation that yields another type is a domain service, a package-level function
taking core values, never a method. No exported function or method but `ID` and `Wire` hands a field out as it is: a
body that is only `return n.title` is a getter whatever it is named, a `NewX` or a domain service included. Receivers
are values, and a method writes no slice or map it shares with the receiver: it clones first. Ask ahead of an action
with a `CanX` beside `X`. A change that removes a getter adds, in the same change, the behaviour that answers the
caller's question.

```go
// ID is the session the agent is.
func (a Agent) ID() agentref.Ref { return a.ref }

// Counts says whether the agent counts on a surface: a muted agent never does, and the Live Activity leaves subtasks
// out.
func (a Agent) Counts(on Surface) bool {
	if a.muting == MutingMuted() {
		return false
	}
	return !(on == SurfaceLiveActivity() && a.lineage == LineageSubtask())
}
```

### Building always-valid objects

A value exists only in a valid state, from birth and from storage. `ParseX` is the only way in from raw input and the
only exported function that takes primitives, a kit type's argument included (`opt.Option[string]` is raw input as
`string` is); `NewX` builds from domain values; both make a type of their own package (or an outcome of one); `RestoreX`
rebuilds stored state and always returns an outcome; a closed set's values are `<Type><Value>`. Make, Create, Build,
Init, From, To, Get, Set, Reconstruct and Must are refused as prefixes. No exported function takes more than four
parameters: more inputs make a staged builder, whose stages are distinct types named for themselves (so they cannot
convert into each other) and whose last stage, `XBuilder`, has `Build`. `Build` refuses a zero value in every part and
reports every one, in order (`fail.Invalids`): Go cannot stop a stage from being declared zero, so a skipped stage fails
at `Build`, never silently. Nothing fills in a default the caller did not pass.

```go
// ParseRef is the session a machine id and a session id name; either one empty is invalid.
func ParseRef(machine, session string) (out outcome.Of[Ref, Fail]) {
	switch {
	case strings.TrimSpace(machine) == "":
		return out.Err(wrong("agentref.machine.empty", "machine"))
	case strings.TrimSpace(session) == "":
		return out.Err(wrong("agentref.session.empty", "session"))
	}
	return out.Ok(Ref{machine: MachineID{machine: machine}, session: SessionID{session: session}})
}
```

```go
// AgentStateStep is the builder's first stage: the agent's state. The zero AgentStateStep is a skipped stage, and
// Build refuses what it leaves out.
type AgentStateStep struct{ agentStateStep Agent }

// AgentRoleStep is the builder's second stage: the agent's role. The zero AgentRoleStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentRoleStep struct{ agentRoleStep Agent }

// AgentBuilder is the builder's last stage. The zero AgentBuilder is a skipped stage, and Build refuses what it leaves
// out.
type AgentBuilder struct{ agentBuilder Agent }

// NewAgentBuilder starts an agent: the session it is and the caller's place for it.
func NewAgentBuilder(ref agentref.Ref, at slot.Slot) AgentStateStep {
	return AgentStateStep{agentStateStep: Agent{ref: ref, at: at}}
}
```

### Visitors and acting on decisions

Every read of a core's data from outside goes through `Accept(XVisitor)`. Every method of an exported visitor
interface starts with `Visit` and takes domain values, and only adapters, testkit and the core's own tests implement
one: application code that wants an answer calls behaviour. A `Visit` method of a generic type names only its type
parameters, yet an instance of the type implements a core's visitor, so in a package that imports a core or the kit it
counts as implementing one, whichever of the package's files declares it. A core that renders rows for its caller
keeps the caller's place per entry as a kit `slot.Slot`, which only adapters mint, and hands it back through the
visitor; a core declares no type parameters. An adapter keeps nested visitor targets as fields, so nested `Accept`
calls do not allocate, and offers `Act(decision, effects)` when the application must carry out a decision that holds
data.

```go
// TallyVisitor is told every row of a tally, then the total.
type TallyVisitor interface {
	VisitAgent(agent slot.Slot, why Why, since instant.Instant) // an agent that waits with no card
	VisitCard(card slot.Slot, why Why)                          // a card on no session
	VisitAgentByCard(agent, card slot.Slot, why Why)            // an agent its oldest card stands for
	VisitTotal(total RowCount)
}

// Accept tells v every row, then the total.
func (t Tally) Accept(v TallyVisitor) {
	for i := range t.tally {
		r := &t.tally[i]
		switch r.kind {
		case agentRow():
			v.VisitAgent(r.agent, r.why, r.since)
		case cardRow():
			v.VisitCard(r.card, r.why)
		case agentByCardRow():
			v.VisitAgentByCard(r.agent, r.card, r.why)
		}
	}
	v.VisitTotal(RowCount{rowCount: len(t.tally)})
}
```

```go
func (r *renderer) VisitAgent(agent slot.Slot, why fleetcount.Why, since instant.Instant) {
	s := &r.sessions[agent.Wire()]
	r.list.Rows = append(r.list.Rows, assistantwire.NeedsYouRow{Machine: s.MachineID, Session: s.SessionID,
		Why: r.word(why), Since: time.Unix(0, since.Wire()).UTC()})
}
```

```go
// Act stores what a decision decided: the item's record after it, written through save.
func Act(d item.Decided, save func(Record) error) error {
	var r recorder
	d.Accept(&r)
	return save(r.record)
}
```

### Closed sets and lifecycles

Every closed set is a closed type: states, roles, reasons, surfaces, commands, events and failures. Without data it is
a struct over its own field with one constructor per value (`StateWorking()` above) and `Accept` for exhaustive
mapping; a flag is a two-value closed type (`MutingHeard`, `MutingMuted`), never a `bool` field. With data it is an
exported sealed interface (one with an unexported method, so only its package implements it) over unexported variants:

```go
// Package quiethours decides whether a notice is delivered now or held.
//
//persona:layer core
package quiethours

import "github.com/nwverse/persona/server/internal/domainkit/instant"

// Decision is what to do with a notice. The zero Decision is nil; Decide never returns it.
type Decision interface {
	Accept(v DecisionVisitor) // only adapters implement DecisionVisitor
	decision()                // sealed: only this package implements Decision
}

type DecisionVisitor interface {
	VisitDeliver()
	VisitHoldUntil(until instant.Instant)
}

type deliver struct{}
type hold struct{ until instant.Instant }

func DecisionDeliver() Decision                        { return deliver{} }
func DecisionHoldUntil(until instant.Instant) Decision { return hold{until: until} }

func (deliver) Accept(v DecisionVisitor) { v.VisitDeliver() }
func (h hold) Accept(v DecisionVisitor)  { v.VisitHoldUntil(h.until) }
func (deliver) decision()                {}
func (hold) decision()                   {}
```

A lifecycle keeps each state's data only in that state (an Inbox item's done time exists only while it is done). An
aggregate whose changes others react to is a decider: commands are sealed values named for the action, `Decide`
returns the next state with its events, `Decided.Then` decides a further command, and `Decided.Accept` hands both to
an adapter. Evolving a state by an event stays unexported, so no caller applies an event to the wrong state. A wire
value a newer peer sends stops in the adapter as its own unknown case and never enters the set.

```go
// Decide is what a command does to an item at now: the next item with its events, or why the item refuses it.
func Decide(i Item, c Command, now instant.Now) outcome.Of[Decided, Fail] {
	if c == nil {
		defect.Raise("item.Decide", "the zero Command")
	}
	return c.decide(i, now)
}
```

### The functional core

A core computes only from its arguments. It imports only the packages in the layer table, and has no goroutine,
channel, select, `panic`, `recover`, `print` or `init`, no clock call (`time.Now`, `Since`, `Until`, `Sleep`, `After`,
`AfterFunc`, `Tick`, `NewTimer`, `NewTicker`, `LoadLocation`, `math/rand`), no `any`, no func or foreign interface
parameter, no service object and no range over a map (range over `slices.Sorted(maps.Keys(m))`). It receives now as an
`instant.Now`, which only the application's clock (`domainkit/clock`) reads, ids as values an adapter made, and the
owner's wall-clock time as a value an adapter computed in the owner's time zone. The application around it loads
through adapters, reads the clock once, calls the core and acts on what it decided:

```go
// MarkDone marks a new item done for the owner: load, read the clock, decide, act.
func (s *Service) MarkDone(w http.ResponseWriter, id string) {
	it, f := outcome.Unpack(itemadapt.Open(id))
	if f != nil {
		failhttp.Write(w, f)
		return
	}
	decided, f := outcome.Unpack(item.Decide(it, item.CommandMarkDone(item.DoerOwner()), s.clock.Now()))
	if f != nil {
		failhttp.Write(w, f)
		return
	}
	if err := itemadapt.Act(decided, s.save); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

### Failures

An expected failure is a value: a fallible core API returns `outcome.Of[T, F]` through a named result, `return
out.Ok(v)` or `return out.Err(f)` on every path, and an outcome nobody set raises a defect wherever it is read. No
core discards what a call returns, with `_` or by writing the call as a statement: a value changes only by becoming
the next value, so `all.With(f)` alone loses the failure `all = all.With(f)` keeps. The check resolves a call through
the declarations it parses (the package's own, the kit's and those of the cores it imports); a call of the standard
library, of a func value, or of a method name those declarations also give without results is left alone. Each core
seals one failure family on the kit's bases:

```go
// Fail is every failure agentref returns. The zero Fail is nil; no Parse returns it as a failure.
type Fail interface {
	fail.Fail
	agentrefFail()
}

// invalid is input that names no session.
type invalid struct{ fail.Invalid }

func (invalid) agentrefFail() {}
```

A failure is read only by visiting it (`fail.FailVisitor`), so nothing decides on a code's text: codes and parts
cannot be compared, and only cores and the kit make them. A part names the wrong input in the core's own words
(`machine`, never `machine_id`). An adapter that returns failures of two families widens them (`outcome.Widen`,
`outcome.Then`). At the HTTP edge one adapter maps failures to answers:

```go
// Write answers w with f.
func Write(w http.ResponseWriter, f fail.Fail) {
	var a answer
	f.Accept(&a)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": a.code, "part": a.part})
}
```

A programmer error is a defect (`defect.Raise`), never a returned failure, and only listed sites recover one, through
`defect.Catch`. A stored row that fails to restore is skipped and logged with its code, never rebuilt half-valid.

### Data objects and adapters

Stored rows, wire types, prompt inputs and fixture shapes are plain data and live outside cores: in contracts, in `db`
or `prompts`, or in the adapter that owns them, named `Record`. A core has no struct tags and no codec method
(`String`, `Error`, `GoString`, `Format`, `MarshalJSON` and the other marshal and unmarshal methods, `AppendText`,
`Scan`, `Value`, `LogValue`). A single value leaves through `Wire()`, which returns its field and which only adapters
and testkit name, called or not; a composite leaves through `Accept`. Outside the core, its tests, adapters and
testkit, nothing calls its `Parse` or `Restore`; nothing outside it builds its types by literal (the zero literal
included: `var x pkg.T` names the documented zero), converts into them, aliases them, dot-imports the package, or
reads them with `unsafe` or `reflect` (`reflect.DeepEqual` aside). An adapter returns nothing, core values (as
outcomes), contracts, `db` rows, prompt inputs, its `Record`, `error`, or `Log`'s string, which goes straight into a
log call; a pointer, slice, map or channel of them holds only them, a map's keys included. A contract's, `db`'s or
`prompts`' type is plain data only in what it holds: what can be called on it is read from its declaration, and the
methods of an interface (those it embeds included), a func type, and the func- and interface-typed exported fields of
a struct, through the types of those packages it holds, take and return only what an adapter may hand out, or the type
is a getter one layer out (`kbwire.ItemSource`, whose `DocTitle` returns a `string`). It hands out nothing else
through its parameters either: what it writes through a pointer, slice, map or channel, passes to a callback, writes
into what a callback returns, or passes to an interface it declares or to a contract's, `db`'s or `prompts`'
interface, func or func field, or to what those return, is one of those (`Act`'s `save func(Record) error` passes;
`mailwire.Store`, whose `PutSetting` takes `any`, does not, and neither does `connectorwire.Items`, whose `ItemSource`
returns a `kbwire.ItemSource` whose `DocTitle` takes text). It takes no parameter of type `any`, `interface{}` or a
type parameter, which can carry a pointer to anything, and no parameter of another package's type but one it may hand
out: a named map or slice (`http.Header`, `url.Values`), a struct with a map inside (`*http.Request`) or an interface
whose methods take what the adapter passes them (`context.Context`'s `Value`) all reach the caller, and the rule does
not read other packages' declarations. No exported struct of its own but `Record` has an exported field, and it has no
exported variable (a getter any caller can also replace) or constant, and no exported alias or named slice, map,
array, func or chan type; its named basic types are counted as application code's. It exports no type named like one
of its core's.

What the rule does not read is a review's to check, and the adapter's signature still names the domain value it starts
from: `io.Writer` and `http.ResponseWriter`, which an answer is written to (`sinks` in
`server/internal/archguard/domainrules/outside.go`); a contract of another module (`sdk/go/wire`, outside the server's
tree), whose types pass as data; and, inside a contract's, `db`'s or `prompts`' type, a field or element of type `any`
or `interface{}`, or of a type from a package that is none of these, read as data (`time.Time`) unless `knownInterfaces`
in `server/internal/archguard/domainrules/core.go` lists it (`io.Reader`), in which case it is refused. #15088 tracks
reading them.

```go
// FromWire is the session a wire reference names.
func FromWire(r assistantwire.SessionRef) outcome.Of[agentref.Ref, agentref.Fail] {
	return agentref.ParseRef(r.MachineID, r.SessionID)
}

// Wire is r as the wire carries it.
func Wire(r agentref.Ref) assistantwire.SessionRef {
	var w refWire
	r.Accept(&w)
	return assistantwire.SessionRef{MachineID: w.machine, SessionID: w.session}
}

// Log is r for a log line, and only for one: "<machine>/<session>".
func Log(r agentref.Ref) string {
	var w refWire
	r.Accept(&w)
	return w.machine + "/" + w.session
}
```

```go
// Record is the stored item: plain and tagged, owned by this adapter.
type Record struct {
	ID     string     `json:"id"`
	DoneAt *time.Time `json:"done_at,omitempty"`
	DoneBy string     `json:"done_by,omitempty"`
}
```

### Bounded contexts

One core is one bounded context. Inside it, peer types read each other's unexported state; across contexts, never: a
core imports no other context's core, and an adapter imports no other context's adapter (a shared kernel's adapter
aside). A concept another context owns is either this context's own value object or a value of a shared kernel,
which holds only value objects and closed sets (no entity with an `ID()`, no builder, no `Decide`, no domain service).
Contexts meet in the application.

### Typed seams

Where application modules meet, the seam is an `application seam` package: it exports no field or variable and takes
no `any`, `interface{}` or map of them, so a wrong port is a compile error, never a nil branch at run time. Hooks
receive domain values. The registration rules for the central lists are in "Registering a domain's entries" below.

```go
// TurnSettled calls fn whenever an agent's turn settles: hooks receive the session as a domain value.
func (r *Registry) TurnSettled(fn func(ctx context.Context, agent agentref.Ref)) {
	r.settled = append(r.settled, fn)
}
```

### Tests

A core's tests are black-box (`package <name>_test`): they build through Parse, New, Restore and the builders, act
through behaviour, and assert through predicates, whole-value equality, a test visitor, or the kit's `domaintest`
(`domaintest.Ok`, and `domaintest.Fails` for a failure's kind and code). Fixtures replay through adapters.

```go
// rows is the test's own visitor: it keeps each row's reason and the total.
type rows struct {
	whys  []fleetcount.Why
	total fleetcount.RowCount
}

func (r *rows) VisitAgent(_ slot.Slot, why fleetcount.Why, _ instant.Instant) {
	r.whys = append(r.whys, why)
}
func (r *rows) VisitCard(_ slot.Slot, why fleetcount.Why)           { r.whys = append(r.whys, why) }
func (r *rows) VisitAgentByCard(_, _ slot.Slot, why fleetcount.Why) { r.whys = append(r.whys, why) }
func (r *rows) VisitTotal(total fleetcount.RowCount)                { r.total = total }
```

```go
func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {
	var skipped fleetcount.AgentRoleStep // a skipped stage still compiles; Build refuses it
	got := domaintest.Fails(t, skipped.Role(fleetcount.RoleWorker(), fleetcount.LineageRoot(), fleetcount.MutingHeard()).Build())
	const want = "invalid fleetcount.agent.no_session agent; invalid fleetcount.agent.no_state state; " +
		"invalid fleetcount.agent.no_update updated"
	if got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}
```

### The rules and their checks

The rules run in two places. archguard's `TestDomainLayers` (`server/internal/archguard/domain_test.go`) checks the
whole server, and check-fast runs `./internal/archguard` for every server change; it prints how many packages of each
layer it checked and how many files it parsed in full, a count that is its budget, and fails when it inspected
nothing. Every declared core, shared kernel, kit, adapter and seam package also runs them in its own `go test`
through a `rules_test.go`, for its own code and for what its importers do to it:

```go
// The domain rules for this package and the code that imports it (docs/server/architecture.md "Domain model").
func TestDomainRules(t *testing.T) { domainrules.Check(t) }
```

Every finding is one line, `RULE <id> <what>. WHY <why>. FIX <the change>. GOOD <code that does it right>. see
docs/server/architecture.md "Domain model"`. There is no ignore marker. Code that predates the rules is held by three
lists under `server/internal/archguard/testdata/domain_layers`: `undeclared.txt` (the packages frozen without a
layer), `legacy_edges.txt` (imports frozen by name, `<pkg> imports <path>`) and `baseline.txt` (shrink-only counts,
`<key> <rule> <count>`). Core, seam and outside counts are kept only for the five cores that predate the rules
(`legacyCores` in `server/internal/archguard/domainrules/lists.go`): a line for any other package is refused, so a
core declared since starts at zero. A count may not rise above its line. Given a base, `TestDomainLayers` (which
reads it from `CI_BASE_SHA`; check-fast's diff mode sets it to its merge base) also holds every count to its count at
the merge base, a package the merge base's baseline has no line for included. It also fails on a name in
`undeclared.txt` or `legacy_edges.txt` and an allowance that the merge base's lists lack, and on a count whose line
and allowances give more room than the merge base's did, a raised or new line included, so no later change adds
itself to a list or widens a line. The server lane and the `rules_test.go` hooks run without a base, and there only
the lines hold. A count that falls prints a notice, and `python3 scripts/ci/lower_baselines.py --only
domain-rules` lowers the lines ("Count ratchets" in `docs/operations/testing.md`). A rule marked "fails at once" takes
no count.

Code that moves takes its lines along. A change that moves a package's files with `git mv`, whole or split, runs
`python3 scripts/test/name_freezes.py moves`: from git's renames of `.go` files under `server/` since its merge base
with `origin/main`, it puts the packages a frozen one's files went to in `undeclared.txt` in its place, renames the
moved imports in `legacy_edges.txt`, and sets the counts of the packages files moved between to their counts now,
provided their total has not risen; a waiting allowance of a package that is gone goes to the package most of its
files went to. With a base, the packages files moved between are held together: their total no higher than at the
merge base, their lines' room no more than it was, and a name the merge base's lists lack passes only as one a move
carried. A move that also adds a record
therefore still fails. The failures of a moved package whose lines were not carried name the command, and a legacy
core that moves is renamed in `legacyCores` in the same change.

The day the rules landed, the open PRs' additions to the record and named-primitive counts became allowances,
`<key> <rule> <Type> #PR ...`, each naming the type a PR adds. An allowance adds one to its count's limit only while
a type of its name is in the package, so the room is for that type alone (the check reads names, so another change
that adds a type of the same name to the package spends it too; #15088); with a base, the merge base's
count already holds the allowed types it has, and an allowance the merge base's baseline does not list gives no room
and fails. Allowances only leave: once the type is on main, `lower_baselines.py`
takes it into the count and removes the line (`TestDomainLayers` says so in a notice), and once its PRs have all closed
without it, `python3 scripts/test/name_freezes.py pending --write --only domain_layers_baseline`, run on a checkout of
main, drops it. That script also wrote them, the day the baseline landed; it adds none to a baseline main has, and a
run that does not name the baseline with `--only` leaves it out once main has it, as it does `undeclared.txt`.

The table says what each check reads in the source. A breach the source does not show is still a breach of the rule
stated above; #15088 lists the ones known.

| Rule | Checks | Counted against |
| --- | --- | --- |
| `layer-declared` | the package comment declares one known layer, or `undeclared.txt` lists the package | frozen names |
| `layer-one` | the package's files agree on one layer | fails at once |
| `layer-kit` | `kit` only under `server/internal/domainkit` | fails at once |
| `layer-contract` | `contract` if and only if `contracts.txt` lists the package | fails at once |
| `layer-shared` | a shared kernel's place and that it holds only values | fails at once |
| `layer-application` | an application or seam package shows an effect use | fails at once |
| `layer-library` | a library imports no core, kit or adapter | fails at once |
| `layer-testkit` | testkit is imported only by tests and other testkit, and no program (`package main`) declares it | fails at once |
| `core-importers` | a core is imported only by application code, adapters, testkit and tests | fails at once |
| `adapter-no-application` | an adapter imports no application package | fails at once |
| `adapter-import` | an adapter imports another adapter only if it is a shared kernel's | fails at once |
| `path-no-core` | the hub, `hubedge`, `db`, `prompts` and `cmd/persona-hub` import no core, kit or adapter | frozen names |
| `core-import` | a core or the kit imports only what the layer table lists | frozen names |
| `core-effect` | no goroutine, channel, select, `panic`, `recover`, `print`, `println` or `init` in a core or the kit (`defect`'s `Raise` and `Catch` aside) | the package |
| `core-clock-call` | no clock function of `time` and no `math/rand` in a core or the kit, called or not | the package |
| `core-package-var` | no package-level variable in a core or the kit | the package |
| `core-exported-const` | no exported constant in a core or the kit | the package |
| `core-type-param` | no type parameter in a core (the kit's documented generics aside) | the package |
| `core-any` | no `any` or `interface{}` in a core or the kit (the kit's type parameter constraints and `outcome`'s nil check aside) | the package |
| `core-exported-field` | no exported field, and no embedded field but a fail base | the package |
| `core-tag` | no struct tags in a core or the kit | the package |
| `core-bare-type` | no exported type over a basic, slice, map, array, func or chan type | the package |
| `core-alias` | no exported alias | the package |
| `core-field-name` | a one-field struct's field is named for its type | the package |
| `core-distinct-shape` | no two exported structs of a package share a field list | the package |
| `core-primitive-field` | a struct with more than one field holds no primitive, a kit type's argument and a name of the package's own included | the package |
| `core-primitive-signature` | primitives, a kit type's argument and a name of the package's own included, only as `ParseX` parameters, `bool` results and `Wire`'s result | the package |
| `core-collection` | slices and maps in exported signatures, a kit type's argument and a name of the package's own included, only as `NewX` and `RestoreX` parameters | the package |
| `core-interface-param` | no func or interface parameter, a func type of the package's own included, but the package's own visitors and sealed interfaces | the package |
| `core-accessor` | every exported method returns nothing, a bool, its receiver's type or an outcome of it, and no exported function or method but `ID` and `Wire` has a body that is one `return` of its inputs' fields | the package |
| `core-pointer-receiver` | no pointer receivers | the package |
| `core-receiver-write` | no write through a receiver's slice or map | the package |
| `core-codec-method` | no codec method | the package |
| `core-wire-method` | `Wire` only on a one-field struct, returning the field | the package |
| `core-constructor` | exported functions are Parse, New, Restore, builder entries, `<Type><Value>` or domain services; Parse and New make their package's type | the package |
| `core-params` | four parameters at most | the package |
| `core-outcome-return` | every return sets the outcome, no outcome literal, no call result discarded with `_` or dropped by a call statement (one the package, the kit or an imported core declares with results), in a core or the kit | the package |
| `core-fail-family` | one sealed `Fail` family, and every failure type in it | the package |
| `core-part-name` | a `fail.NewPart` name has no underscore | the package |
| `core-visitor-name` | every visitor method starts with `Visit` | the package |
| `core-public-evolve` | no exported `Evolve` or `Apply` | the package |
| `core-service-name` | no exported service, manager, repository, store, port, client, gateway or handler struct | the package |
| `core-map-range` | no range over a map; `maps.Keys`, `Values` and `All` only inside `slices.Sorted` | the package |
| `core-zero-doc` | every exported struct and sealed interface says what its zero value is | the package |
| `core-doc` | a core, kit or adapter package has a package comment starting `Package <name>` | the package |
| `core-test-package` | a core's and the kit's tests are black-box | the package |
| `core-hook` | a declared domain or seam package has the `rules_test.go` hook | fails at once |
| `seam-exported-field` | a seam exports no field or variable | the package |
| `seam-any` | a seam's exported signatures take no untyped value | the package |
| `out-dot-import` | no dot import of a core or the kit | the core |
| `out-alias` | no alias of a core's or the kit's type | the core |
| `out-literal` | no literal of a core's or the kit's type outside it, tests and the zero literal included | the core |
| `out-conversion` | no conversion to a core's or the kit's type outside it | the core |
| `out-wire-ref` | no `.Wire` outside adapters and testkit, called or not | the package it is in |
| `out-parse-call` | a core's `Parse` and `Restore` only from it, its tests, adapters and testkit | the core |
| `out-visitor-impl` | a `Visit` method that takes a core's or the kit's type, or any `Visit` method of a generic type in a package that imports a core or the kit, only in adapters, testkit and the core's own tests | the core |
| `out-clock-read` | `instant.ReadClock` only in `domainkit/clock` and its testkit | `domainkit/instant` |
| `out-slot-mint` | `slot.NewSlot` only in adapters and testkit | `domainkit/slot` |
| `out-code-mint` | `fail.NewCode` and `fail.NewPart` only in cores, the kit and testkit | `domainkit/fail` |
| `out-unsafe` | no `unsafe`, and `reflect` only for `DeepEqual`, in a file that imports a core or the kit | the first core it imports |
| `out-rules-import` | only tests import `domainrules` | fails at once |
| `out-log-use` | an adapter's `Log` result only as an argument of a logging call | the package it is in |
| `adapter-name` | an adapter exports no type named like one of its core's | the adapter |
| `adapter-exported-result` | an adapter hands out only what the section above lists, in results (a map's keys included, and what a contract's, `db`'s or `prompts`' type can be called with), through parameters (none of type `any`, `interface{}`, a type parameter or another package's type but `io.Writer` and `http.ResponseWriter`; a contract's, `db`'s or `prompts`' type only where what it can be called with takes what an adapter may hand out, what is called on what its methods and funcs return included) and in exported fields; it has no exported variable or constant, and no exported alias or named slice, map, array, func or chan type. What it does not read is listed above | the adapter |
| `app-exported-record` | exported structs with an exported field, in undeclared, application and library packages (contracts, `db` and `prompts` aside) | the package |
| `app-named-primitive` | named basic types, in undeclared, application and library packages and adapters | the package |
| `app-lifecycle-write` | assignments to a `DoneAt` or `DoneBy` field, in undeclared, application and library packages | the package |
| `app-bridge-call` | uses of an adapter's `Wire` function, called or passed on, in undeclared, application and library packages' production code | the package |

The `app-*` counts hold the habits this model replaces from spreading in code that has not moved yet, and, given a
base, a new package starts at zero; they run only in `TestDomainLayers`, as does `layer-declared`.

### The domain kit

`server/internal/domainkit` is held to every rule a core is, since every core imports it: it imports only the
standard library a core may import, the other kit packages and, in `instant`, `time` (`At` and `ReadClock` take a
`time.Time`). Its listed API needs a few exceptions, and `kitAPI` in `server/internal/archguard/domainrules/core.go`
names each one by package, function, method or field: type parameters constrained by `any` or `fail.Fail`; the
callbacks of `outcome`'s `Fold`, `Then` and `Map` and `opt`'s `Match` and `Fold`; the functions that are neither a
constructor nor a domain service in a core's sense (`outcome`'s `Unpack`, `Fold`, `Widen`, `Then`, `Map` and `Must`,
`opt`'s `Some`, `None` and `Fold`, `instant`'s `At` and `ReadClock`, `defect`'s `Raise` and `Catch`); the raw input
of `NewCode`, `NewPart`, `NewSlot`, `Raise` and `Must`'s operation name; `Of`'s pointer receivers, so that
`Of{}.Ok(v)` does not compile and a producer sets its named result; `outcome`, which carries every core's failure
family and declares none; `Defect`'s text fields and its `Error`; `Option`'s presence flag; and `Now.Stamp`, which
hands a core the instant the clock was read at. Every other rule runs over the kit, and `TestKitAPIIsNeeded` fails on
an exception nothing needs, so a new kit type or function meets every rule until a reviewed line says why not.
The hub links none of it.

- `outcome`: `Of[T, F]`, a value or a failure of family `F`; producers write `return out.Ok(v)` or `out.Err(f)`, and
  `IsOk`, `Unpack`, `Fold`, `Map`, `Widen`, `Then` and `Must` raise a defect on an outcome nobody set. `Widen` and
  `Then` let an adapter return two families as `fail.Fail`; `Must` is for input the program made itself.
- `fail`: `Fail`, sealed to the kit's bases `Invalid` (a code and the part that was wrong), `Refusal`, `Absence`,
  `Conflict` and `Invalids`; `FailVisitor`; `Code` and `Part`, made by `NewCode` and `NewPart` and not comparable. A
  base's zero value is no failure: visiting it raises a defect, and `outcome`'s `Err` visits what it is given, so a
  family type built empty fails where it is made.
- `instant`: `Instant` (UTC nanoseconds; the zero is unknown), `Span`, and `Now`, the application's reading of the
  clock, which only `domainkit/clock` and its testkit make (`ReadClock`); `At`, `ParseMinutes`, `ParseSeconds`,
  `Elapsed`, and `Wire` for adapters.
- `opt`: `Option` with `Some`, `None`, `IsNone`, `Match` and `Fold`; no `Get` and no `Or`.
- `defect`: `Raise` for programmer errors, `Catch` for the sites that recover one.
- `slot`: `Slot`, the caller's place for an entry; `NewSlot` for adapters.
- `clock` (application): `Clock`, read once per operation, and `System`; `clock/clocktest` (testkit) has `Fixed` and
  `Fake`.
- `domaintest` (testkit): `Ok`, `Fails` and `Describe`.

## Registering a domain's entries

Nearly every feature used to add a line to one of a few central lists: the route calls in the assistant's `New`,
`toolNames` and the cases of `runTool`, the loops `Service.Run` starts, `proto.Features`, `assistantwire.Routes`,
`extwire.RouteScopes`, the switch in `Node.handle` and `server/internal/prompts/assistant.go`. Those lines are where most
of the server's merge conflicts come from (#7891). A domain adds its entries from its own files instead, and every kind
of entry goes through the same design. The central lists keep the entries they have, and take no new name (below).

### One registry per Service

- **The registry**: the package `kernel`, under `server/internal/assistant`, holds one registry per assistant `Service`,
  with a slot for each kind of entry. The slots are routes (installed through `Service.handle`), bus methods, tools
  (each placed after a named anchor tool), the hooks for a settled turn and a new connection, snapshot parts, and loops.
  `Kernel.Bus` is where any domain adds a new bus method, not only the Inbox. Not on `main` yet: the package comes with
  step S1 of `docs/plans/plan-inbox-vertical-slice.md`.
- **Two ways in, one registry**:
  - A module in a subpackage calls `Register(k)` from `New`, where its central line was. `saved` comes first: today
    `New` calls `saved.Register(saved.Host{…})`, and S1 registers it through the kernel instead, keeping `saved.Host`
    as saved's own adapter.
  - A domain inside package `assistant` calls a package-level function from an `init()` in its own file:
    `registerRoutes(name, (*Service).xRoutes)`, `registerTools(after, …)` or `registerLoop(name, (*Service).xLoop)`.
    `New` adds them to the same registry before the modules register.
- **One of each**: there is one route table and one list of loops, and tool placement is computed once, at the end of
  `New`. Route order means nothing (`TestRoutesAreTheWireOnes` sorts, and `http.ServeMux` ignores it). The tools keep
  the order `server/internal/hub/testdata/prompts/mcp_tools/assistant.index.golden` pins.
- **When each part lands**:
  1. the kernel, with `saved` (S1);
  2. the Inbox's routes, bus methods, hooks and snapshot part (S4);
  3. `registerRoutes` and `registerLoop` (#7891);
  4. `registerTools`, with or just before the Inbox's tools (S5).

  Until a slot exists, its entries go where they went before. The 63 route calls already in `New`, and the tools and
  loops already listed, stay where they are.

How each registration is checked, and when the registry closes, is written with the kernel (S1).

### Lists that stay package variables

A wire list that is a plain variable in a contract package takes a domain's entries from an `init()` in that domain's
file. The append comes after the literal, which stays first and unchanged:

- **`assistantwire.Routes`**: `func init() { Routes = append(Routes, …) }` in `assistantwire/<domain>.go`. Seven files do
  this today, among them `agents.go`, `board.go` and `urgent_call.go`. Package `assistant` reads the list after they
  have run (`server/internal/assistant/hub.go`), and `TestRoutesAreTheWireOnes` keeps it equal to what the assistant
  serves.
- **`proto.Features`**: `func init() { Features = append(Features, FeatureX) }` in `proto/features_<domain>.go`, beside
  the flag's constant. Go runs the init()s after the literal is set, file by file in name order, so the hello and
  `/health` list the literal's flags, then each domain file's. `server/internal/proto/features_test.go` holds the
  package to that one form of append, and `scripts/test/test_doc_wire.py` reads the appends too.
- **`extwire.RouteScopes`**: `addRouteScopes(…)` from an `init()` in `extwire/<domain>_scopes.go`, which panics on a key
  that is already there, on a key that is no route pattern and on an empty scope, so a bad entry stops every program
  that imports `extwire` at start-up. `extwire/inbox_scopes.go` does this for the Inbox's routes (step S6 of the Inbox
  plan), and archguard's `TestCentralListsGainNoInboxEntry` keeps them there.
  `server/internal/hub/testdata/route_scopes.txt` stays one sorted golden, the permission list a reviewer reads.

The hub's bus handlers already work this way: `handleBus("notify.request", (*Hub).busNotifyRequest)` runs from an
`init()` beside the handler, and a method registered twice panics (`server/internal/hub/bus.go`). A domain's feature
flags and methods are documented in its domain's protocol doc: `docs/protocol/protocol.md`, or a doc split from it
(`scripts/test/test_doc_wire.py` finds a flag in either).

### The node's method table

The node has a table of the same shape, its own because the node may not import the manager's packages
(`server/internal/node/node_methods.go`).

- **Registering**: a method that is not bound to a session registers from an `init()` in its domain's own file,
  `func init() { nodeRPC(proto.MethodMachinePorts, (*Node).machinePorts) }`. `nodeRPC` decodes each call's params into
  the handler's own type once: fields the type does not have are ignored, empty params leave it zero, and params that
  do not decode are answered `bad <method> params`. A handler that takes `json.RawMessage` is handed the params
  themselves, because `secret.run` and `model.run` clear them after reading.
- **Where it is consulted**: in `Node.handleMethod`, after the delivery aliases and before the shared parameter
  struct, where `sessions.history` was first handed to its domain's handler (`server/internal/node/sessions_history.go`).
- **Validated on entry**: each `nodeRPC` call is checked where it is made, and an invalid one panics when the node
  starts, at the `init()` that made it. The table refuses an empty name, a nil handler, a name registered twice, a
  name under a prefix `handleMethod` hands to a dispatcher of its own (`assistant.segment.`, `git.backend.`,
  `node.update.`, `task.workspace.`, `plugin.node.`), a delivery alias (`session.source_send`,
  `session.owner_interrupt`, `codex.owner_answer`) and a session-bound name. No flag closes the table after start-up:
  nothing registers later, and a call only reads it.
- **What stays in the switch**: methods bound to a session (`session.*`, `terminal.*`, `assistant.send`,
  `assistant.answer_again`, `assistant.permissions.reset`, `codex.answer`). The shared decode, alias resolution, the
  Codex thread mapping and the identity-conflict refusal run before them, and a table entry would skip all of them.
- **No new label**: `handleMethod` takes no new label for a method the table may hold (the `node_handle_method` row
  under "Frozen central lists"). The labels it has stay, and move into the table only while no open PR edits their
  lines.

### Prompts

A new prompt's types and `Render` function go in a file of their own, `prompts/assistant_<topic>.go`, as
`assistant_answer_again.go` has them. Templates and goldens do not change (`docs/assistant/prompts.md`). The topics
already in `prompts/assistant.go` move out one file at a time, and only while no open PR edits them.

### Frozen central lists

A central list with a seam takes no new name. `server/internal/archguard/central_lists_test.go` checks this.

- **The names**: each frozen list has a row in `centralLists`, and its names are in
  `server/internal/archguard/testdata/central_lists/<id>.txt`. The file holds the names the list had when its freeze
  landed, then the names the PRs open that day were adding, each with its PRs.
- **The check**: a new name fails. The message gives the file and line, where the entry goes instead, and this section.
  A listed name that has left the list is a notice, and its line can go. Editing an existing entry is fine, because the
  freeze is on names, not lines. These files hold names, never counts (they are not count ratchets,
  `docs/operations/testing.md` "Count ratchets").
- **Frozen today**:
  - the labels of `Node.handleMethod` for the methods the node's method table may hold, by their source text
    (`"node.info"`, `proto.MethodMachineToolchain`), and the prefixes it routes on as `prefix "plugin.node."`;
  - the `assistantwire.Routes` literal, by each entry's source text (`"GET /api/v1/todos"`, `"GET " + StarredImportPath`);
  - the top-level declarations of `prompts/assistant.go`, a method as `Type.Method`;
  - the `proto.Features` literal, by each entry's source text (`"locale"`, `FeatureTodoSendState`);
  - the keys of the `extwire.RouteScopes` literal in `extwire/scopes.go`, by their source text (`"GET /api/v1/kb/{kind}"`).

  Each later seam's PR adds its own list's row. That PR shows a planted entry failing and a registered one passing.
- **Pending names**: `python3 scripts/test/name_freezes.py pending` lists the names that open PRs add, read from their
  heads. A draft whose head commit is more than 14 days old does not count. The script asks the archguard test for the
  names (`-central-list-names`), so they are in the form the freeze compares.
- **Writing the list**: on the day a row lands, `pending --write --only <the row's id>` rewrites the list's file, after
  `origin/main` is merged in; without `--only` the run also prunes every central list `main` has, and leaves out the
  domain lists (`undeclared.txt` and the baseline, "Domain model") once `main` has them. Once the list is on `main`,
  `--write` only prunes it: it keeps the listed names `main` still has, a pending one whose PR merged among them, and
  drops the names that have left and the pending ones whose PRs closed. It adds no name: one that `main` has and the
  list lacks is reported as an error, and the freeze keeps failing on it. So it is not how a later PR gets a name on a
  list.
- **The mock hub too**: `scripts/mock/mock_hub_switches.py` uses the same module for the mock hub's switch freeze
  (`docs/operations/testing.md` "Mock hub domains").

## Key flows

### Starting a session
1. App: pick a machine → pick a folder (recent projects / browse) → pick a launcher → optional first message.
2. hub → node `session.start`: in the herdr workspace `persona` (created if missing), create a tab (cwd=folder, label=folder name),
   `pane run "<launcher> --session-id <uuid> --mcp-config ~/.persona/mcp.json --plugin-dir ~/.persona/agent-plugin --plugin-dir ~/.persona/claude-hooks --plugin-dir ~/.persona/claude-live-replies"`
   (the last one, the live replies `MessageDisplay` hook, only while the machine's Settings › Claude Code › Live replies is on; older Claude Code: `--settings ~/.persona/claude-settings.json` instead of the plugins).
3. The node waits for the agent to be ready (`agent wait`); if it is blocked (trust folder, etc.), the screen is pushed to the phone as a `screen` interaction. Once ready, the first message is sent.
4. The node starts tailing the transcript, and events are pushed through the hub to subscribed apps.

### Retrying a held Agent message

The hub forwards the owner's Retry to the devices manager, which owns the held queue and claims each send in its
database. If the last attempt may have arrived, the manager asks the node for echo evidence for that session and
message ID. The node can confirm a match from its echo record. It can authorize another attempt when the live Agent
is idle and the transcript covers the original send through a later turn end. The node rechecks with the session's
input lock held before replacing an ambiguous send receipt. With an unknown result or an older node, the manager can
still confirm delivery from a matching ID in recent turns.

### Answering questions / approving plans
1. claude calls AskUserQuestion → PreToolUse lets Claude draw its terminal dialog → the following PermissionRequest connects `personad hook` to node.sock and sends an `interaction` → the node hands it to the manager (`interaction.create`), which records it and has the hub push it to the app / watch (including a local notification).
2. The user picks on the phone → hub → manager → node → PermissionRequest outputs `updatedInput.answers` → claude continues. If the person answers in the terminal first, PostToolUse withdraws the waiting phone card instead.
3. The card waits whether or not an app is online. The hook leaves the question to the terminal (the TUI shows it as usual, and it can be answered at the computer too) on timeout (30 minutes by default),
   when the node has no connection to its manager, or when a resident node's manager has been away longer than the 10-minute hold (`docs/protocol/protocol.md` "When the manager is away (resident nodes)").

### Phone data tools
claude calls the MCP tool `phone_calendar_events` → `personad mcp` → node.sock → node → manager (`app.call` `phone` / `phone.call`) → hub → a connected iPhone that offers the tool (WS) → the app queries with EventKit → returns the same way.
When no iPhone is connected, the hub wakes the newest iOS registration that offers the tool with a silent push (only when push is set up; a device at most once per 30 seconds) and waits up to 20 seconds for it to connect
(`docs/apple/phone-tools.md` "iOS limitations"). If none does, it returns the error "The user's iPhone is not connected and did not wake up in the background: ask them to open the Persona app, then retry."

### Runtime list origin projection

`internal/sessionorigin` classifies the runtime DTO's pane/host metadata using the host registry, matching the clients' desktop-first origin rule (`docs/protocol/protocol.md`, "Pane/host origins"). The hub's existing list adapter adds this scalar to bounded rows and passes it to `internal/sessionlist`; the generic index only stores and matches it. State, preferences and Agents membership remain with their existing owners.

### Runtime list catalog

`catalog=1` on the paged runtime list (`docs/protocol/protocol.md`, "Projects and spaces"; `server/internal/hub/sessions_catalog.go`) names the filtered list's projects and spaces. The index counts the rows by machine and project and by machine and group (`sessionlist.PlaceCounts`, a scan at request time); the hub's adapter turns each directory into a project. What repository a directory holds is not on the row: the adapter asks the directory's computer (`git.status` with `identity_only`), remembers the answer in memory for 30 minutes, and falls back to the folder's name. `place` and `space` are the matching filters, sets of machine and project (or group) pairs.

Why not a field on the row: `proto.Session` has no word left in its size class and `sessionlist.Item` none in a 150-row batch (`docs/lessons/server.md`, "A field that pushes a row past its size class"), and a repository's remote is a fact about a directory, the same for every row in it. Asked per directory when a client opens the view, it costs nothing on a list update, a push or a page, works with the nodes already deployed, and is lost with nothing else when the hub restarts.
