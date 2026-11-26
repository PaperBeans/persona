package assistant

import (
	"testing"

	"github.com/nwverse/persona/server/internal/extmgr/imessageport"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// needsIMessageModule skips a test that runs the iMessage connector through the extension manager when this build
// has no iMessage module (persona_without_imessage or persona_core_only).
func needsIMessageModule(t testing.TB) {
	t.Helper()
	if imessageport.Factory(moduleswitch.Set{}) == nil {
		t.Skip("the iMessage module is not in this build")
	}
}
