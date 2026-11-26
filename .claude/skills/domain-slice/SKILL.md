---
name: domain-slice
description: Start a new server domain package (a core, a shared kernel, an entity with a staged builder, a decider, an adapter) that follows the domain rules from its first line, then run its rules. Use for "add a domain package", "new bounded context", "model X as values / a closed set / an entity", "start a slice under server/internal", or /domain-slice. Go only; the Swift, Kotlin and web flavours arrive with each platform's first domain module.
---

# Start a domain slice

The rules are `docs/server/architecture.md` ("Domain model"), in short `server/internal/CLAUDE.md`; their pitfalls are
`docs/lessons/domain-modeling.md`. The scaffold writes the compliant shape, so start from it rather than from a package
nearby: packages with lines in `server/internal/archguard/testdata/domain_layers/baseline.txt` predate the rules.

1. **Name the context and its home.** A core lives in its context's tree (`internal/assistant/<name>`,
   `internal/node/<name>`); a shared kernel (`--shared`, values and closed sets only) directly under `internal/`. Write
   the package's one sentence: it follows "Package <name> ".
2. **Map the concepts to flags** (`python3 scripts/gen/new_domain_slice.py --help` says what each writes): every concept a
   value (`--value Name=string|int`, `--zero-is` when its zero is a real value, `--value Name=part:Type,…` for a value
   of parts, `--parse Name=raw:string,…` for one parsed from several raw inputs), every state, role or reason a closed set
   (`--closed Name=a,b,c`, a payload as `b(Type)`), the aggregate an entity (`--entity Name --id NameID`, `--builder`
   past four inputs), its changes a decider (`--decider`), and its conversions an adapter (`--adapter`, `--act` to store
   a decision).
3. **Write it**, from the repository root:

   ```sh
   python3 scripts/gen/new_domain_slice.py go internal/<context>/<name> --doc "<one sentence>" <flags>
   ```

   It refuses a directory that exists, and a combination the rules would refuse (an entity in a shared kernel, more
   than four inputs without `--builder`) with what to do instead.
4. **Run its rules and tests** before editing anything, and again after each edit:

   ```sh
   cd server && GOFLAGS=-trimpath go test -count=1 ./internal/<context>/<name>/...
   ```

   The `rules_test.go` hook names each broken rule with its fix (`RULE <id> … FIX … GOOD <path>`). Fix the code the line
   names; there is no ignore marker, and no rule is satisfied by editing a list.
5. **Edit it into the domain's words**: rename the scaffold's open / closed lifecycle, its sample docs and its codes;
   add behaviour as predicates (`CanX` beside `X`) and transitions, never getters; keep the adapter's `Record` the only
   exported struct with fields. A caller that wants data gets a predicate, a decision through `Act`, or a contract the
   adapter fills. A method you add to a value whose zero is no value starts with its `known` check, as the scaffold's
   do, so the zero is a defect wherever it is handed in; absence is `opt.Option`.
6. **Classify the package and its adapter** in `schema/module-classification.txt` (`go server/internal/<context>/<name>
   <core|module id>`, sorted with its neighbours); `python3 scripts/test/test_module_classification.py` fails on a
   package nobody classified.
7. **Before the PR**, run archguard too (check-fast does for any server change), and fill the "Domain model" line under
   Verification in the PR template:

   ```sh
   cd server && GOFLAGS=-trimpath go test -count=1 ./internal/archguard/ ./internal/<context>/<name>/...
   ```
