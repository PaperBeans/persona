package knowledgeuihost

import (
	"testing"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// Without a module provided there is no layer; with one, New makes it for each host unless the switch has it off;
// providing twice panics.
func TestProvide(t *testing.T) {
	saved := port
	t.Cleanup(func() { port = saved })
	port = moduleswitch.NewPort[func(Host) Layer]("knowledgeuihosttest")
	var on moduleswitch.Set
	if InBuild(on) {
		t.Fatal("in the build without a module")
	}
	if _, ok := New(on, nil); ok {
		t.Fatal("a layer without a module")
	}
	made := 0
	Provide("knowledge-ui", func(Host) Layer { made++; return nil })
	if _, ok := New(on, nil); !ok || made != 1 || !InBuild(on) {
		t.Fatalf("made %d", made)
	}
	if _, ok := New(moduleswitch.Off("knowledge-ui"), nil); ok || made != 1 || InBuild(moduleswitch.Off("knowledge-ui")) {
		t.Fatalf("made %d", made)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("provided twice without a panic")
		}
	}()
	Provide("knowledge-ui", func(Host) Layer { return nil })
}
