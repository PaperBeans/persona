package prompts

import (
	"strings"
	"testing"
)

func TestSupervisionRoleInstructionsPreserveTheFullPolicy(t *testing.T) {
	// Which parts a role gets is the assistant's (supervisionPolicy, from supervisionrole's IsLeader and IsBackground);
	// here each combination a role can get is held to the full policy.
	parts := map[string]SupervisionParts{"common": {}, "leader": {Leader: true}, "background": {Background: true}}
	rendered := make(map[string]string, len(parts))
	for name, p := range parts {
		text := RenderSupervisionInstructionsFor(p)
		if text == "" {
			t.Fatalf("%s: empty policy", name)
		}
		rendered[name] = text
	}
	full := RenderSupervisionInstructions()
	fullLines := strings.Split(full, "\n")
	for role, text := range rendered {
		at := 0
		for _, line := range strings.Split(text, "\n") {
			if line == "" {
				continue
			}
			for at < len(fullLines) && fullLines[at] != line {
				at++
			}
			if at == len(fullLines) {
				t.Fatalf("%s contains new, changed, or reordered policy line: %q", role, line)
			}
			at++
		}
	}
	leaderOnly := []string{
		"- A direction (leader conversation) supervises",
		"- Report upward only results",
		"- For each reached item and each reporting cycle",
	}
	backgroundOnly := []string{
		"- Background supervises for main",
		"- Background's initial role notice",
	}
	private := func(line string) bool {
		for _, prefixes := range [][]string{leaderOnly, backgroundOnly} {
			for _, prefix := range prefixes {
				if strings.HasPrefix(line, prefix) {
					return true
				}
			}
		}
		return false
	}
	for role, text := range rendered {
		for _, prefix := range leaderOnly {
			if strings.Contains(text, prefix) != (role == "leader") {
				t.Errorf("%s: leader-only rule %q leaked or disappeared", role, prefix)
			}
		}
		for _, prefix := range backgroundOnly {
			if strings.Contains(text, prefix) != (role == "background") {
				t.Errorf("%s: background-only rule %q leaked or disappeared", role, prefix)
			}
		}
		for _, line := range fullLines {
			if line != "" && !private(line) && !strings.Contains(text, line) {
				t.Errorf("%s lost common policy line: %q", role, line)
			}
		}
	}
	// Together the role outputs cover every full-union line; no rule exists only in the compatibility renderer.
	for _, line := range fullLines {
		if line == "" {
			continue
		}
		covered := false
		for _, text := range rendered {
			if strings.Contains(text, line) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("full policy is not reconstructible from its roles; missing %q", line)
		}
	}
	if got := RenderSupervisionInstructionsFor(SupervisionParts{Leader: true, Background: true}); got != full {
		t.Fatal("both role-private parts together differ from the full policy union")
	}
}
