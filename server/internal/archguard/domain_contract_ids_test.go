package archguard

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/archguard/domainrules"
)

// contract-bare-id is a hard rule with no count: a plain string or []string field named *ID, *Id or *IDs of a
// contract package fails unless its "<Type>.<Field>" is in the wire-types baseline's bare-id names ("metrics", "wide"
// or "exceptions"), so a field that moves or is lifted into a contract package keeps passing and one written anew fails.
func TestContractBareID(t *testing.T) {
	baseline := `{"metrics": {"bare-id": {"Row.OwnerID": 1}},
		"wide": {"bare-id": {"Row.OwnerID": 1, "Lifted.CoreID": 1}},
		"exceptions": {"bare-id": {"Kept.KeepID": {"count": 1, "pr": "#15397", "why": "test"}}}}`
	names, err := domainrules.ParseBareIDs(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Row.OwnerID", "Lifted.CoreID", "Kept.KeepID"} {
		if !names[n] {
			t.Fatalf("the baseline reader lacks %s: %v", n, names)
		}
	}
	run := func(body string) []domainrules.Finding {
		t.Helper()
		files := pkgFiles("internal/awire", "contract", body)
		delete(files, "internal/awire/rules_test.go")
		pkgs, err := domainrules.LoadFiles(serverModule, merged(plantedKit(t), files))
		if err != nil {
			t.Fatal(err)
		}
		idx := domainrules.NewIndex(serverModule, pkgs, []string{"internal/awire"}).WithBareIDs(names)
		all, _, err := domainrules.Run(pkgs, idx, "")
		if err != nil {
			t.Fatal(err)
		}
		var out []domainrules.Finding
		for _, f := range all {
			if f.Rule == "contract-bare-id" {
				out = append(out, f)
			}
		}
		return out
	}
	// A name in metrics, in wide only (a lift from a Core package or a module) or in exceptions passes.
	for name, body := range map[string]string{
		"metrics":    "// Row is a row.\ntype Row struct{ OwnerID string }\n",
		"wide only":  "// Lifted was a Core type.\ntype Lifted struct{ CoreID string }\n",
		"exceptions": "// Kept stays.\ntype Kept struct{ KeepID string }\n",
		"named type": "// PeerID names a peer.\ntype PeerID string\n\n// Link is a link.\ntype Link struct {\n\tPeerID  PeerID\n\tPeerIDs []PeerID\n\tName    string\n}\n",
	} {
		if got := run(body); len(got) != 0 {
			t.Errorf("%s passes: %v", name, got)
		}
	}
	// A new name fails at its file and line with the fix, and the rule offers no ignore marker.
	got := run("// Row is a row.\ntype Row struct {\n\tOwnerID string\n\tPeerIDs []string\n\tName    string\n}\n\n// Extra is new.\ntype Extra struct{ MachineID string }\n")
	if len(got) != 2 {
		t.Fatalf("new names: %v", got)
	}
	for i, want := range []string{"Row.PeerIDs", "Extra.MachineID"} {
		f := got[i]
		for _, w := range []string{"RULE contract-bare-id " + want + " is a plain", "FIX use a named ID type: the contract ID types from " +
			"schema/wire/ids.schema.json once it lands (#15397), or a named `type XID string` in the contract package; a rename: " +
			"add the new name to the baseline's exceptions with a PR reference"} {
			if !strings.Contains(f.Msg, w) {
				t.Errorf("the finding for %s lacks %q: %s", want, w, f.Msg)
			}
		}
		if f.Pos.Filename != "internal/awire/awire.go" && !strings.HasPrefix(f.Pos.Filename, "internal/awire/") || f.Pos.Line == 0 {
			t.Errorf("the finding for %s has no file:line: %v", want, f.Pos)
		}
		if strings.Contains(strings.ToLower(f.Msg), "persona:ignore") {
			t.Errorf("the rule offers an ignore marker: %s", f.Msg)
		}
	}
	// A rename is a new name, and a field with the same name in another type is one too.
	if got := run("// Row is a row.\ntype Row struct{ OwnerRefID string }\n"); len(got) != 1 {
		t.Errorf("a renamed field: %v", got)
	}
	// The rule takes no count: a baseline line for it is refused.
	lists := domainrules.ParseLists("", "", "", "contracts contract-bare-id 1\n")
	if v := domainrules.Judge(nil, nil, nil, lists, nil, ""); len(v.Failures) != 1 || !strings.Contains(v.Failures[0], "takes no count") {
		t.Errorf("a count line for a hard rule: %q", v.Failures)
	}
}
