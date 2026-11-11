package larkcli

import (
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func TestKindsAreDescribed(t *testing.T) {
	m := (&Connector{}).Manifest()
	if err := connector.CheckKinds(m.Kinds, func(kind string) string {
		return prompts.RenderInboxItem(prompts.InboxItem{Source: ID, Kind: kind})
	}); err != nil {
		t.Fatal(err)
	}
}
