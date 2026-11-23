# Persona Core and feature modules

Who this serves: people who change Persona's code, on any platform. Nothing here is visible to someone using Persona:
with every module on (the default) the product is what it was.

Persona is split into **Persona Core**, the parts everything else stands on, and **feature modules**, optional features
that reach the core only through one declared contract. The core never imports a module, so a module can be left out of
a build, switched off without one, or tested on its own. Decisions and stages: #13430 (tracking #13411); the server
half: #13408.

## What Persona Core is

"Persona Core" is a product-layer term. It holds (#13430 section 1):

- the Assistant main chat, with its memory (the knowledge-base files and search, the `kb_*` tools) and composer dictation;
- Agents: handing work to coding Agents, supervision with its follow-up scheduler, and `claudeplugin`;
- To-dos and the task board;
- Computers: machines, Link, the node with Claude Code, Codex and herdr;
- Inputs: the connector and channel framework, and the Inbox shell;
- hub plumbing (transport, auth, push, pairing), the extension host, and the shell mechanics (navigation, the settings
  shell, the design system);
- the north-star metrics, the yardstick of the core flow.

The membership test: if the owner can say "I never use this" and the assistant, Agents, to-dos, computers and connectors
still work, it is a module. The first set (voice, recording, lark, mail, health, briefing, ...) is in #13430 section 2.

No code is renamed for the term. The names that already say "core" mean something else:

| Name | What it is |
| --- | --- |
| `apple/Packages/PersonaCore` | The Apple binding of the shared Rust client library; it keeps its name. The Apple layer for Persona Core is `PersonaHostApp` |
| `native/app-core` | The shared Rust client logic every Persona Core client depends on; module-specific domains never go into it |
| `android/core/*` | Android's infrastructure Gradle modules |
| `server/internal/herdrcore` | An engine library |

## The module contract

One manifest concept, with the same field names on every platform: `schema/module.schema.json`. A module's manifest says
who it is (`id`, `api`, `origin`: `builtin` or `extension`, `title`), what it depends on (`needs`: must be loaded; `wants`:
used when loaded), and one list per kind of slot it adds to: `feature` (a wire feature flag), `hub_routes`, `mcp_tools`,
`prompt_sections`, `settings`, `shell_places`, `chat_card_kinds`, `composer_actions`, `chat_accessories`,
`background_jobs`, `schedules`, `db_migrations`, `i18n_namespaces`, `node_programs`, `channel_kinds`, `frame_handlers` and
`push_categories`. A module adds nothing it does not declare, and a host refuses a contribution the manifest does not
name. Where an extension point exists or is filed, the slot uses its name (#13400 to #13403), so built-in modules and
extensions share one registry shape and a module can later become an extension.

**A disabled or excluded module is absent.** That is the contract: its routes answer 404, its tools and feature flags
are not announced, it runs no loop, its settings keys stay stored but are hidden, and its tables stay. The core without
modules is a tested product.

What the server enforces today is only the slots it serves (see "Slots" below): a module's settings are hidden but
stay stored, its routine facts are gone (the routine opens with none), and its tables stay, since no module owns one
yet. Routes, tools, feature flags and loops are not served yet, so no module adds any; a module that declares one is
not loaded. Each of those parts of the contract holds from the PR that serves its slot.

## Server

| Piece | Where | What it is |
| --- | --- | --- |
| Module API | `server/internal/modapi` | Pure types: `Info` (the manifest), `Module` (`Info()` + `Register(Host)`), `Host` (everything a module may use of the core) and the contribution types. A contract package (`server/internal/archguard/testdata/contracts.txt`) |
| Modules | `server/internal/modules/<id>` | One package per module, with its `module.json` (embedded: `Info()` reads it). No nested Go modules |
| Composition root | `server/internal/modules/all` | The one list of built-in modules, one file per module with its build constraint. `internal/manager` and `internal/managercli` link it |
| Host | `server/internal/assistant/kernel` | Loads the modules compiled in and switched on, serves `modapi.Host` to each, and answers the core's questions about what they added |

The core asks the kernel (`s.modules` in package assistant, `server/internal/assistant/modules.go`), never a module's
package. A module may later keep its own prompts in its own tree, under the same rules as `server/internal/prompts/`
(#13430, decision 5); none does yet.

### Loading

The manager loads modules once, in `assistant.New`, before anything a module adds to is set up. The kernel leaves out a
module that is switched off, is written for another `api`, is not `builtin`, declares a slot the host does not serve
yet, needs a module that does not load (wherever it stands in the list; a cycle of needs loads none of them), or whose
`Register` fails. A module registers after the modules it needs and after those it wants that load; otherwise list
order holds. `wants` never keeps a module from loading. An id listed again is ignored after its first entry. A module
left out adds nothing, including what it added before failing; why is kept (`Kernel.Skipped`) and logged.

### Slots

`modapi.Info` declares every slot of the contract so manifests can be written against the whole of it. The server
serves only what the pilot needs so far; a module that fills any other slot is not loaded, so nothing it expects goes
missing quietly.

| Slot | Served | How |
| --- | --- | --- |
| `settings` | Yes | `Host.AddSetting(modapi.Setting)`: the module says what a value means (`Value`, `Check`, `Describe`); the core keeps it stored, lists it in `persona_settings` and asks the owner to approve a change |
| `schedules`: `facts` | Yes | `Host.AddScheduleFacts(modapi.ScheduleFacts)`: the wait rules of the facts a scheduled routine with facts on opens with |
| `schedules` (others), `hub_routes`, `mcp_tools`, `prompt_sections`, `feature`, `background_jobs`, `db_migrations`, `i18n_namespaces`, `node_programs`, `channel_kinds`, `frame_handlers`, `push_categories`, `shell_places`, `chat_card_kinds`, `composer_actions`, `chat_accessories` | Not yet | Declared but not served: each gets a `Host` method when its first module moves (#13408 remaining work, #13400 to #13403) |

### The switch

- **At build time**: the build tag `persona_without_<id>` leaves one module out, `persona_core_only` leaves every module
  out. `go build -tags persona_core_only ./...` must pass. `scripts/ci/server.sh build` builds both and runs, with each
  tag, the module and kernel tests and a `-run` subset of `internal/assistant` (Brief, Module, Setting, Routine, Facts,
  ScheduledConfiguration, NorthStar, Accept, Later, Golden): the assistant's tests of the briefing itself skip
  (`needsBriefingModule`), and `TestBuildWithoutBriefingLoadsNone` checks the core without it. The whole package passes
  with both tags too (about 9 minutes each, too long for the lane).
- **At run time**: the settings row `modules` (the `settings` table, as every other setting; no state file) holds
  `{"disabled": ["<id>", …]}`. The manager reads it once where it starts (`server/internal/assistant/main.go`,
  `ReadModuleSwitch`) and hands it to `assistant.New` as `Options.ModulesOff`, so a change takes effect at the next
  start. A module switched off is absent exactly as one left out of the build. There is no screen or tool for the row
  yet: the settings registry of #13395 is its intended home.

### Guards

`server/internal/archguard` keeps the boundary. Both module guards read the import lines of every `.go` file under
`server/` (testdata left out), whatever its build constraint or GOOS, so a file only a `persona_core_only` build or one
platform compiles cannot hide a violation (`TestModuleGuardsSeeEveryBuild` proves it on a fixture).

- **`TestCoreImportsNoModule`**: no package outside `internal/modules` imports `internal/modules/…`, except the composition
  roots listed in `testdata/module_roots.txt`, which import only `internal/modules/all`. A core package's tests may
  import `internal/modules/all` to run as the manager does (`internal/assistant/modules_link_test.go`), and nothing
  else of `internal/modules`. Known violations would go into `testdata/module_imports_baseline.txt`, which only shrinks
  (a stale line fails); it is empty.
- **`TestModulesUseOnlyTheSPI`**: every file of a module, tests included, imports only the standard library, `modapi` and
  the other contract packages (pure, so nothing more comes with them), its own packages, and the modules its
  `module.json` needs. A module it only wants may be left out of the build (`persona_without_<id>`), so it is reached
  through the kernel, never imported. Each module directory has a `module.json` whose id is the directory's name.
- **`TestContractsArePure`** covers `modapi` like every contract package, and the hub's
  `TestHubDoesNotImportTheManager` (`server/internal/hub/architecture_guard_test.go`) forbids `internal/modules`.
- `scripts/test/test_module_schema.py` checks every `module.json` against `schema/module.schema.json`, refuses invalid
  manifests, and keeps `modapi.Info`'s fields and the schema's in step.

### The briefing pilot

The daily briefing (`internal/assistant/briefing*.go`) is a module (#13430, decision 3). This first step moves the
part that is self-contained behind the contract; the facts' gathering, the Lark card and delivery stay in package
assistant and move as the host learns to serve them.

- **The module owns** (`server/internal/modules/briefing`): the `assistant.briefing_waits` setting (its type, defaults,
  validation and card text) and the wait rules of the briefing's per-direction rows (when stopped work counts as waiting,
  how rows are ordered, how many are shown).
- **The core keeps** the stored value (the `briefing_waits` field of the assistant's settings, byte for byte as before),
  the setting's description in `persona_settings`, and the gathering of the rows from the ledgers it owns.
- **With the module absent** a routine with facts on opens with no facts and keeps no briefing run, so its answer goes
  out as a routine with facts off does (no brief item, no briefing rows on the Lark card); a daily report gets the north
  star's lines alone; the acceptance reminders wait for no briefing; `assistant.briefing_waits` is hidden from
  `persona_settings` but stays stored. `server/internal/assistant/modules_test.go` covers each of these.

### How to add a module

1. Pick the id from #13430 section 2 and check its manifest needs only slots the kernel serves; if it needs another,
   add the `Host` method and the kernel's support first, in their own PR, and update "Slots" above.
2. Create `server/internal/modules/<id>/` with `module.json` (validated by `schema/module.schema.json`), a `Module` whose
   `Info()` reads it and whose `Register` adds what it declares. Import only `modapi`, contract packages and the modules
   it needs (never one it only wants).
3. Add `server/internal/modules/all/<id>.go` with `//go:build !persona_without_<id> && !persona_core_only`, and the id to
   `want_default_test.go` there.
4. In the core, replace each direct call with a question to the kernel (`s.modules`), and define what happens when the
   module is absent; test both.
5. Run the archguard tests, the tagged builds and `scripts/test/test_module_schema.py`.

## Clients

Each client documents its side in its own platform doc; these are pointers only.

- **Apple** (#13432): `PersonaHostApp` with `PersonaModuleAPI` and `PersonaModule<Name>` packages. See `docs/apple/` (written with #13432).
- **Android** (#13433): `:core:moduleapi` and `:module:<id>`. See `docs/android/` (written with #13433).
- **Web** (#13434): a module host (planned as web/src/core/module-host.ts) and `web/src/modules/<id>/`. See `docs/web/` (written with #13434).

Every client reads the same manifest fields from `schema/module.schema.json`.
