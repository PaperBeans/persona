package domainrules

import "strings"

// domainDoc ends every finding: the section that states the rules.
const domainDoc = `see docs/server/architecture.md "Domain model"`

// planted is where the compliant twins live (testdata/domainrules/planted), which GOOD names until the exemplars
// they mirror land in the server; agentref and fleetcount have, so their rules name server/internal/agentref and
// fleetcount.
const planted = "server/internal/archguard/testdata/domainrules/planted/"

// fleetcount is the first core in the server (the agents' and cards' Needs you), the exemplar most rules name.
const fleetcount = "server/internal/assistant/fleetcount"

// A ruleText is what a rule's finding says besides what was found: why the rule holds, the production change
// that fixes it, and code that does it right.
type ruleText struct{ why, fix, good string }

// rules is every rule by its id, which is also its baseline key (docs/server/architecture.md "Domain model",
// "The rules and their checks").
var rules = map[string]ruleText{
	"layer-declared": {
		"every package states whether it is domain code, so new code is checked from its first line",
		"add `//persona:layer <kit|core|core shared|adapter|application|application seam|library|contract|testkit>` to the package comment; start a domain package with python3 scripts/gen/new_domain_slice.py",
		"server/internal/agentref/doc.go",
	},
	"layer-one": {
		"a package has one set of rules",
		"keep one //persona:layer line, in doc.go",
		"server/internal/agentref/doc.go",
	},
	"layer-kit": {
		"the kit is the one shared vocabulary",
		"declare core, adapter or library instead",
		"server/internal/domainkit/opt",
	},
	"layer-contract": {
		"contracts are checked as pure data by TestContractsArePure",
		"add it to contracts.txt with its reason, or declare library",
		"server/internal/assistantwire",
	},
	"layer-shared": {
		"a shared kernel is importable by every core, so it may hold only values",
		"declare core and keep it in its context, or split the values into a shared kernel",
		"server/internal/agentref",
	},
	"layer-application": {
		"domain logic declared application escapes every core rule",
		"declare core (python3 scripts/gen/new_domain_slice.py starts one), or library if it holds no domain concept",
		fleetcount,
	},
	"layer-library": {
		"libraries are shared by the hub and the managers and stay free of domain code",
		"move the code that needs the core into an adapter",
		planted + "internal/failhttp",
	},
	"layer-testkit": {
		"test support must not become production behaviour",
		"import it only from _test.go files, and declare a program (package main) application",
		"server/internal/domainkit/domaintest",
	},
	"core-importers": {
		"libraries and contracts are shared by the hub and must not carry domain code",
		"convert through an adapter in the application",
		fleetcount + "/fleetcountadapt",
	},
	"adapter-no-application": {
		"adapters convert; application orchestrates; the arrow points one way",
		"pass what the adapter needs as arguments",
		fleetcount + "/fleetcountadapt",
	},
	"adapter-import": {
		"contexts meet in the application, not in adapters",
		"call both adapters from the application",
		fleetcount + "/fleetcountadapt",
	},
	"path-no-core": {
		"the hub relays contracts and db and prompts are shared libraries",
		"move the logic to a manager and pass contracts",
		fleetcount + "/fleetcountadapt",
	},
	"core-import": {
		"a core computes only from its arguments",
		"take the value as a parameter from the application, or move the code to an adapter",
		fleetcount + "/tally.go",
	},
	"core-effect": {
		"effects belong to the application",
		"return a decision and let the application act; raise defect.Raise for programmer errors",
		fleetcount + "/tally.go",
	},
	"core-clock-call": {
		"a hidden clock makes a core untestable and drifts per caller",
		"take now as instant.Now from the application",
		fleetcount + "/tally.go",
	},
	"core-package-var": {
		"a core keeps no state between calls",
		"make it a function or a const inside the function",
		fleetcount + "/closed.go",
	},
	"core-exported-const": {
		"primitives leak through constants",
		"use a closed-set constructor or a value object",
		fleetcount + "/closed.go",
	},
	"core-type-param": {
		"a type parameter carries foreign data or ports into the core",
		"carry the caller's place as slot.Slot; generics live in the kit",
		"server/internal/domainkit/slot",
	},
	"core-any": {
		"an untyped value is a hole in the model",
		"name the concept as a value object",
		"server/internal/agentref/ref.go",
	},
	"core-exported-field": {
		"a caller that reads a field decides outside the domain",
		"make it unexported and add the behaviour the caller needs",
		fleetcount + "/agent.go (Counts)",
	},
	"core-tag": {
		"codecs belong to adapters",
		"move the shape to the adapter's Record",
		fleetcount + "/fleetcountadapt",
	},
	"core-bare-type": {
		"any package converts into a named primitive and string(x) unwraps it",
		"make it a struct with one field named for the type",
		"server/internal/agentref/ref.go (MachineID)",
	},
	"core-alias": {
		"an alias is the other type under a new name",
		"declare a value object",
		"server/internal/agentref/ref.go",
	},
	"core-field-name": {
		"sibling types with the same field convert into each other from any package",
		"rename the field to <want>",
		fleetcount + "/closed.go (State)",
	},
	"core-distinct-shape": {
		"identical shapes convert into each other from any package: B(a) compiles",
		"give each its own field names (named for the type)",
		fleetcount + "/agent.go (stages)",
	},
	"core-primitive-field": {
		"a primitive field is a concept without its rules",
		"make it a value object or a closed type",
		fleetcount + "/agent.go",
	},
	"core-primitive-signature": {
		"raw input enters once, through Parse, from an adapter",
		"take a value object; parse in the adapter",
		"server/internal/agentref/ref.go (ParseRef)",
	},
	"core-collection": {
		"a slice handed out is a view into the core's state",
		"return a domain collection and read it through Accept",
		fleetcount + "/agent.go (Agents)",
	},
	"core-interface-param": {
		"a callback or port brings IO and foreign rules into the core",
		"make the variation a closed value; let the application do the IO",
		fleetcount + "/tally.go (AgentsNeedsYou)",
	},
	"core-accessor": {
		"a caller that reads state decides outside the domain",
		"answer the question with a predicate or a transition, or compute the answer in a domain service (a function that returns a field as it is hands state out too); adapters read through Accept",
		fleetcount + "/agent.go (Counts)",
	},
	"core-pointer-receiver": {
		"a value changes only by becoming the next value",
		"use a value receiver and return the next value",
		fleetcount + "/agent.go",
	},
	"core-receiver-write": {
		"value receivers share arrays, so the earlier value changes too",
		"clone the slice (slices.Clone) into the next value, then write",
		fleetcount + "/agent.go",
	},
	"core-codec-method": {
		"a codec reads state out without an adapter",
		"write the conversion in the adapter (Wire, Log)",
		"server/internal/agentref/agentrefadapt",
	},
	"core-wire-method": {
		"Wire is the one way out of a single value",
		"return the field; composites leave through Accept",
		"server/internal/agentref/ref.go (SessionID.Wire)",
	},
	"core-constructor": {
		"one way in per meaning keeps every object valid",
		"rename to Parse/New/Restore by meaning, or name the closed value <Type><Value>",
		fleetcount + "/closed.go",
	},
	"core-params": {
		"long parameter lists hide which inputs belong together",
		"use a staged builder (python3 scripts/gen/new_domain_slice.py --builder writes one)",
		fleetcount + "/agent.go (NewAgentBuilder)",
	},
	"core-outcome-return": {
		"a forgotten Ok or Err must be a defect, never a success",
		"return out.Ok(v) or out.Err(f); handle the failure",
		fleetcount + "/agent.go (Build)",
	},
	"core-fail-family": {
		"callers must know every failure a core can return",
		"declare `type Fail interface{ fail.Fail; <pkg>Fail() }`",
		fleetcount + "/agent.go",
	},
	"core-part-name": {
		"wire field names belong to the adapter that fed the input",
		"name the input in the core's own words; map it in the adapter",
		"server/internal/agentref/ref.go",
	},
	"core-visitor-name": {
		"bare names collide with unrelated methods across the server",
		"rename to Visit<Thing>",
		fleetcount + "/tally.go (TallyVisitor)",
	},
	"core-public-evolve": {
		"an event applied to the wrong state bypasses Decide's rules",
		"keep evolve unexported; return Decided from Decide",
		planted + "internal/assistant/inbox/item/decide.go",
	},
	"core-service-name": {
		"services with dependencies are application code",
		"make it a package-level pure function, or move it to the application",
		fleetcount + "/tally.go (AgentsNeedsYou)",
	},
	"core-map-range": {
		"map order changes run to run, so the result does",
		"range over slices.Sorted(maps.Keys(m))",
		fleetcount + "/tally.go",
	},
	"core-zero-doc": {
		"Go cannot forbid the zero value outside the package",
		"document it, and refuse it where it is not a valid value",
		fleetcount + "/closed.go",
	},
	"core-doc": {
		"the first sentence names the bounded context for readers and Agents",
		"add doc.go with 'Package <name> ...' and the directive",
		fleetcount + "/doc.go",
	},
	"core-test-package": {
		"black-box tests prove tell, don't ask holds",
		"declare package <name>_test and build through constructors",
		fleetcount + "/fleetcount_test.go",
	},
	"core-hook": {
		"the rules run in the package's own go test",
		"add rules_test.go: func TestDomainRules(t *testing.T) { domainrules.Check(t) }",
		fleetcount + "/rules_test.go",
	},
	"seam-exported-field": {
		"a seam is built once and sealed",
		"make it unexported; register through a method",
		planted + "internal/assistant/kernel",
	},
	"seam-any": {
		"a wrong port becomes a run-time nil branch",
		"take a typed port interface through the module's constructor",
		planted + "internal/assistant/kernel",
	},
	"out-dot-import": {
		"dot imports hide literals and conversions from the guard",
		"import it by name",
		fleetcount + "/fleetcountadapt",
	},
	"out-alias": {
		"an alias hides literals and conversions from the guard",
		"use the type by name",
		fleetcount + "/fleetcountadapt",
	},
	"out-literal": {
		"a literal skips every check the constructor makes",
		"call Parse, New or Restore, or a builder; `var x pkg.T` names the documented zero",
		fleetcount + "/fleetcountadapt/fleet.go",
	},
	"out-conversion": {
		"a conversion forges a value without its constructor",
		"build it through its constructor",
		fleetcount + "/fleetcountadapt/fleet.go",
	},
	"out-wire-ref": {
		"only adapters convert domain values to wire values",
		"move the conversion into the adapter and return a contract",
		"server/internal/agentref/agentrefadapt",
	},
	"out-parse-call": {
		"raw input enters the domain only through adapters",
		"call the adapter (agentrefadapt.FromWire)",
		"server/internal/agentref/agentrefadapt",
	},
	"out-visitor-impl": {
		"application code that reads a core decides outside it",
		"call behaviour, or use the adapter's Act",
		fleetcount + "/fleetcountadapt",
	},
	"out-clock-read": {
		"the application's clock is the one place now comes from",
		"take a clock.Clock and call Now()",
		"server/internal/domainkit/clock",
	},
	"out-slot-mint": {
		"a Slot indexes the adapter's own list",
		"let the adapter mint it",
		"server/internal/domainkit/slot",
	},
	"out-code-mint": {
		"failure codes belong to the core that fails",
		"return the core's own failure",
		"server/internal/agentref/ref.go",
	},
	"out-unsafe": {
		"unsafe and reflect read and forge private state",
		"compare through IsSame or a visitor",
		fleetcount + "/fleetcount_test.go",
	},
	"out-rules-import": {
		"the rules are a test",
		"import it from rules_test.go",
		"server/internal/archguard/domainrules",
	},
	"out-log-use": {
		"Log text is for log lines, not for decisions",
		"pass it straight to the log call",
		"server/internal/agentref/agentrefadapt",
	},
	"adapter-name": {
		"two types with one name blur the boundary",
		"name it Record or after the contract it fills",
		fleetcount + "/fleetcountadapt",
	},
	"adapter-exported-result": {
		"an adapter that returns raw values is a getter one layer out",
		"return the contract the caller sends, or offer Act(decision, effects)",
		fleetcount + "/fleetcountadapt",
	},
	"app-exported-record": {
		"new concepts in application and library packages keep the habit the domain model replaces",
		"put the concept in a core (python3 scripts/gen/new_domain_slice.py starts one) or a wire shape in a contract",
		fleetcount,
	},
	"app-named-primitive": {
		"a named primitive is only a name",
		"declare a value object in a core (python3 scripts/gen/new_domain_slice.py --value)",
		"server/internal/agentref/ref.go",
	},
	"app-bridge-call": {
		"legacy callers shrink as their code moves behind adapters",
		"pass the domain value on, or move the conversion into the adapter",
		"server/internal/agentref/agentrefadapt",
	},
	"contract-bare-id": {
		"a bare string id lets any id stand for any other, and a contract's types are what every client generates from",
		"use a named ID type: the contract ID types from schema/wire/ids.schema.json once it lands (#15397), or a named `type XID string` in the contract package; a rename: add the new name to the baseline's exceptions with a PR reference (scripts/ci/wire_types_baseline.json)",
		planted + "internal/assistantwire",
	},
	"app-lifecycle-write": {
		"a done state written field by field takes its own meaning at every writer",
		"call the item's own transition (its done or reopen behaviour), or move the lifecycle into a core",
		planted + "internal/assistant/inbox/item/decide.go",
	},
}

// RuleIDs is every rule's id, sorted.
func RuleIDs() []string {
	var ids []string
	for id := range rules {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

// GoodPath is the code a rule's message names as done right, as a path from the repository's root (without the
// symbol some messages add in parentheses).
func GoodPath(id string) string {
	good := rules[id].good
	if i := strings.Index(good, " ("); i >= 0 {
		good = good[:i]
	}
	return good
}

// message is a finding's text: RULE <id> <what>. WHY ... FIX ... GOOD ... and the doc section.
func message(id, what string) string {
	r, ok := rules[id]
	if !ok {
		panic("domainrules: no rule " + id)
	}
	return "RULE " + id + " " + what + ". WHY " + r.why + ". FIX " + r.fix + ". GOOD " + r.good + ". " + domainDoc
}
