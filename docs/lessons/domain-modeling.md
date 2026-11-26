# Lessons: strict domain code

Read before writing or changing a domain package: a server core, shared kernel, adapter or the domain kit, and the client
domain modules as they arrive. The rules are `docs/server/architecture.md` ("Domain model"); `server/internal/CLAUDE.md`
is the short form, and `python3 scripts/gen/new_domain_slice.py go …` starts a package that follows them. These are the
traps the compiler does not stop, each with what to write instead.

- **A typed nil is not a nil failure.** A `Fail` that holds a nil pointer (`var f *notFound; out.Err(f)`) is not `nil`:
  `f != nil` holds, and the kit's `outcome` visits what `Err` is given, so it panics with "value method … called using
  nil *notFound pointer" where the failure is made. A core's failures are struct values over a kit base, made where the
  rule breaks (`invalid{fail.NewInvalids(…)}`, as the scaffold writes them), never pointer types or a variable that may
  still be nil.
- **`json.Marshal` of a domain value gives `{}` and no error**, because every field is unexported, and `%v` prints the
  fields anyway (`{{mac} {s1}}` for an `agentref.Ref`). Both look like a way out of the domain and are wrong in opposite
  directions: nothing marshals or prints a domain value. An adapter fills the contract (`agentrefadapt.Wire`) or writes
  the log text (`agentrefadapt.Log`, only as an argument of a log call).
- **The zero value exists anyway.** Go cannot stop `var r agentref.Ref` outside the package, so the rules allow it (it
  names the documented zero) and refuse only the literal `agentref.Ref{}` outside the core (`out-literal`). Every exported
  type says what its zero is ("The zero Ref is no session"); constructors refuse a zero part (`Build` lists each one),
  and a function handed a zero that is no value raises a defect: `agentref.Ref`'s `Accept`, `IsSame` and `OnMachine`
  do, and so does every method the scaffold writes, through an unexported `known` check. `Wire` stays the bare field
  (`core-wire-method`), so the check sits where the value is read, and `agentrefadapt.Wire` of a zero `Ref` raises
  rather than send empty ids. Absence is `opt.Option[Ref]`, never the zero standing for "none".
- **Comparing two failures with `==` panics.** A failure cannot be compared (`fail.Code` holds a `[0]func()`, and
  `fail.Invalids` a slice), so `==` on two `fail.Fail` values compiles, being an interface comparison, and panics at run
  time with "comparing uncomparable type agentref.invalid". Read a failure by visiting it (`fail.FailVisitor`); a test compares
  `domaintest.Fails(t, o)` or `domaintest.Describe(f)`, which give its kind, code and part as text.
- **`String()` is a getter.** `fmt` and every logger call it, and anyone can read the text and decide on it, so a core
  declares no `String`, `Error`, `GoString`, `Format`, marshal or `LogValue` method (`core-codec-method`). A decision is a
  predicate or a visitor; text for a log line comes from the adapter's `Log`.
- **Kotlin: a `data class` has `copy()`.** It builds a value from any property values without the checks of the
  factory, so where `copy()` is visible, `window.copy(until = earlier)` makes a value `Window.parse` would refuse,
  whatever visibility this repository's Kotlin version (`grep '^kotlin' android/gradle/libs.versions.toml`) gives it.
  A domain class is a plain class with a private constructor and a companion `parse`.
- **TypeScript: a cast or a coercion compiles.** With `#private` fields, `{} as Title` still compiles, and the forged
  object fails only when used (`TypeError: forged.isEmpty is not a function`); `String(t)` and `` `${t}` `` give
  `[object Object]` and `JSON.stringify(t)` gives `{}` (tsc 5.9.3, the version `web/package.json` pins, and node 24).
  Build domain values only through their static `parse`, and convert them only in the feature's adapter.
- **Swift: `Mirror` reads private stored properties.** `Mirror(reflecting: t).children` lists `title=a title` for a
  `private let title`, and `String(describing: t)` prints `Title(title: "a title")`. Nothing in a domain target reflects
  or describes a value; its adapter target converts it.
