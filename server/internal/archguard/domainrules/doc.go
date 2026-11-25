// Package domainrules is the server's domain rules as code (docs/server/architecture.md "Domain model"): every
// package declares its layer with a //persona:layer line in its package comment, and the rules for each layer are
// checked from the source alone, with go/parser and no type information. One implementation runs in two places:
// archguard's TestDomainLayers over the whole server, in check-fast's guard step, and the rules_test.go hook in every
// declared package (func TestDomainRules(t *testing.T) { domainrules.Check(t) }), in the package's own go test.
//
// Load reads the package clause, the package comment and the imports of every non-generated .go file; full parses
// are kept to the declared domain packages and to the files a byte prefilter marks (an import of a domain package,
// or a .Wire selector), so the cost is a count of files, which the test prints. Every finding is one line,
// "RULE <id> <what>. WHY ... FIX ... GOOD <path>. see docs/server/architecture.md "Domain model"", and there is no
// ignore marker: code that predates the rules is held by the shrink-only counts and frozen names in
// archguard/testdata/domain_layers.
//
// It imports only go/ast, go/parser, go/token, os, path/filepath, strings and testing, and only _test.go files import
// it (out-rules-import).
//
//persona:layer testkit
package domainrules
