package assistant

import (
	"context"
	"testing"
)

func TestStartPassesExplicitConfigDir(t *testing.T) {
	for _, dir := range []string{"", "~/.config/claude-assistant", "default", "legacy"} {
		t.Run(dir, func(t *testing.T) {
			f := newFakeHub().withMachine()
			f.settings = &Settings{MachineID: "server"}
			s := newTestService(t, f)
			if _, err := s.Start(context.Background(), StartParams{Text: "hello", ConfigDir: dir, Untitled: true}); err != nil {
				t.Fatal(err)
			}
			got, set := f.sends[0]["config_dir"]
			if dir == "" && set || dir != "" && got != dir {
				t.Fatalf("config_dir = %v (set=%v), want %q (omitted when empty)", got, set, dir)
			}
		})
	}
}
