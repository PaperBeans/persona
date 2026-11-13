package nodeupdate

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestResidentUpdateRejectsLegacyBeforeInstallAndWatchdog(t *testing.T) {
	m, data, restarted := testManager(t)
	m.requireResidentHandover = true
	before := hash(t, m.exe)
	watchdogs := 0
	m.startWatch = func(string, string, string) error { watchdogs++; return nil }
	id := receive(t, m, data, spec(data, "next")) // the old-format fixture does not announce resident_handover
	if _, err := m.Commit(context.Background(), id, nil); err == nil || !strings.Contains(err.Error(), "resident listener handover") {
		t.Fatalf("legacy upload was not refused before installation: %v", err)
	}
	if hash(t, m.exe) != before || watchdogs != 0 || restarted.Load() != 0 {
		t.Fatalf("legacy upload changed the live image or armed restart: watchdogs=%d restarts=%d", watchdogs, restarted.Load())
	}
	if _, err := os.Stat(markerPath(m.exe)); !os.IsNotExist(err) {
		t.Fatalf("legacy upload left an update marker: %v", err)
	}
}
