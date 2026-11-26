package extmgr

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/cliport"
	"github.com/nwverse/persona/server/internal/lark"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/proto"
)

// A node's built-in programs of a switched-off module (iphone of phone-extra, imessage of imessage) are not used: the
// manager neither offers nor calls them, as for a node built without them. cua and ego are the core's.
func TestManagerDropsSwitchedOffNodePrograms(t *testing.T) {
	info := proto.NodeInfo{BuiltinPrograms: []string{"cua", "ego", "imessage", "iphone"}}
	for _, tt := range []struct {
		off  []string
		want []string
	}{
		{nil, []string{"cua", "ego", "imessage", "iphone"}},
		{[]string{"phone-extra"}, []string{"cua", "ego", "imessage"}},
		{[]string{"imessage", "phone-extra"}, []string{"cua", "ego"}},
		{[]string{"github", "voice"}, []string{"cua", "ego", "imessage", "iphone"}},
	} {
		h := &Manager{opts: Options{Modules: moduleswitch.Off(tt.off...)}}
		if got := h.switchedInfo(info).BuiltinPrograms; !slices.Equal(got, tt.want) {
			t.Errorf("off %v: builtin programs %v, want %v", tt.off, got, tt.want)
		}
	}
	if !slices.Equal(info.BuiltinPrograms, []string{"cua", "ego", "imessage", "iphone"}) {
		t.Fatalf("the node's own info changed: %v", info.BuiltinPrograms)
	}
}

// With the lark module switched off, manager.json's lark.cli is not read, as in a build without the module, and the
// bot's client is not made.
func TestManagerWithLarkSwitchedOff(t *testing.T) {
	if !cliport.InBuild() {
		t.Skip("the lark module is not in this build (TestManagerWithoutLark… cover it)")
	}
	cfg := &Config{Lark: lark.Config{CLI: &lark.CLIConfig{Bin: "lark-cli"}}, dir: t.TempDir()}
	h := New(Options{Dir: cfg.dir, Config: cfg, Modules: moduleswitch.Off("lark")})
	if h.cfg.Lark.CLI != nil {
		t.Fatal("lark.cli is read with the lark module switched off")
	}
	if cfg.Lark.CLI == nil {
		t.Fatal("the caller's configuration changed")
	}
	if !cliport.Available(moduleswitch.Set{}) || cliport.Available(h.modules()) {
		t.Fatal("cliport does not follow the switch")
	}
	on := New(Options{Dir: cfg.dir, Config: cfg})
	if on.cfg.Lark.CLI == nil {
		t.Fatal("lark.cli is not read with the lark module on")
	}
}
