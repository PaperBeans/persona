package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// The node reads the run-time module switch from its hub's features by the clients' rule: with the modules marker a
// module whose flag is missing is off; without the marker (an older hub, or no manager connected) every module is on.
func TestNodeReadsTheSwitchFromTheHub(t *testing.T) {
	t.Cleanup(func() { hubModules.Store(nil) })
	p := moduleswitch.NewPort[int]("node/switchtest")
	p.Provide("nodetest", 1)
	if _, ok := p.Get(nodeModules()); !ok {
		t.Fatal("before node.configure a module is off")
	}
	for _, tt := range []struct {
		features []string
		on       bool
	}{
		{[]string{moduleswitch.Marker, "module.nodetest"}, true},
		{[]string{moduleswitch.Marker}, false},
		{[]string{}, true}, // an older hub: no marker
		{[]string{"module.other"}, true},
	} {
		setHubModules(tt.features)
		if _, ok := p.Get(nodeModules()); ok != tt.on {
			t.Errorf("hub features %v: module on = %v, want %v", tt.features, ok, tt.on)
		}
	}
}
