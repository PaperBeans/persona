// Package wirecount counts the weak types in the contract packages and the weak call shapes in the hub's handler
// layer, for the wire-types ratchet (scripts/ci/wire_types_ratchet.py, docs/operations/testing.md "Count ratchets").
//
// It parses source with go/parser only (no type information, every non-test .go file whatever its build tags) and
// prints one JSON document: for each metric, a map from a name to how many times that name occurs. Names carry no
// package, so a type that moves from one contract package to another (a module slimming move) keeps its names and
// the ratchet sees nothing new; only a name that is new, or one that occurs more often, is a rise.
//
// Field metrics, over the packages listed in internal/archguard/testdata/contracts.txt, named "<Type>.<Field>"
// (a field of an unnamed struct inside it: "<Type>.<Outer>.<Field>"):
//
//	bare-enum-state   string field whose name ends in State, Status or Phase
//	bare-enum-kind    string field whose name ends in Kind
//	bare-enum-type    string field whose name ends in Type
//	bare-enum-role    string field whose name ends in Role
//	bare-id           string or []string field whose name ends in ID, Id or IDs
//	bare-error        string field whose name ends in Error
//	map-any           field whose type holds map[string]any or map[string]interface{}
//	raw-json          field whose type holds json.RawMessage
//	ptr-time          *time.Time field
//	ptr-bool          *bool field
//	ptr-error-string  *string field whose name ends in Error
//
// Handler-layer metrics, over server/internal/{hub,assistant,devices,extmgr} and their subpackages:
//
//	inline-event      map[string]any{"type": ...} literal, named by the type's string literal ("<dynamic>" if not one)
//	write-json-map    writeJSON(..., map[string]any{...}) call, named by the literal's sorted string keys
//	literal-call      x.Call(ctx, "method", ...) with a string literal method, named by the method
//
// Wide (field metrics only) counts the same names over every non-test package under internal/, modules included, and
// the SDK's wire package. A contract name above its baseline is let through when its wide count has not risen: the
// field already existed outside the contract packages and was lifted or moved into one, which is neutral.
//
// TestEmit (count_test.go) writes the counts as JSON to the file named by WIRE_COUNTS_OUT; the ratchet runs it.
//
//persona:layer library
package wirecount
