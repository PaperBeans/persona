package assistantskills

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestPersonaViewSkillExamplesAreValid(t *testing.T) {
	raw, err := fs.ReadFile(builtin, "persona-views/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	blocks := wire.AssistantViewBlocks(text)
	if len(blocks) != 1 { // one card that chooses, opens a detail page and submits a form
		t.Fatalf("skill has %d examples, want 1", len(blocks))
	}
	for _, block := range blocks {
		if _, err := block.Parse(); err != nil {
			t.Fatalf("skill teaches an invalid view: %v", err)
		}
	}
	for _, want := range []string{"view_show", "view_update", "view_get", "submit=true", "ask_user", "fallback", "自动运行", "getBoundingClientRect"} {
		if !strings.Contains(text, want) {
			t.Errorf("skill lost rich view guidance: %s", want)
		}
	}
}
