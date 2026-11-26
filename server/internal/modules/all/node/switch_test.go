package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch/switchtest"
)

// binaryOnly are the node root's calls that add a subcommand to the binary (`personad office`, `personad iphone`): a
// command run as its own process has no hub, so only a build without the module removes it (switchtest.Check).
var binaryOnly = []string{"commandport.AddCommand"}

// Every provider seam a node-side module of this build fills follows the run-time module switch as the node reads it
// from its hub's features (moduleswitch.FromFeatures): present with the module on, absent with it switched off.
func TestEverySeamFollowsTheSwitch(t *testing.T) {
	switchtest.Check(t, ".", want, nil, binaryOnly)
}
