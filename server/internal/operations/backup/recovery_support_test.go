package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverySupportIsStableAndRedacted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PERSONA_DATABASE_URL", "")
	t.Setenv("PATH", "")
	for _, support := range []Support{VerificationSupport(Options{Home: home}), CreationSupport(Options{Home: home})} {
		if support.State != "unsupported" || support.Reason != "configuration" {
			fatalf(t, "missing synthetic configuration: %+v", support)
		}
	}
	config := `{"database_url":"postgres://synthetic:secret@postgres:5432/synthetic_hub"}`
	if err := os.WriteFile(filepath.Join(home, "hub.json"), []byte(config), 0o600); err != nil {
		fatal(t, err)
	}
	for _, support := range []Support{VerificationSupport(Options{Home: home}), CreationSupport(Options{Home: home})} {
		if support.State != "unsupported" || support.Reason != "compose" {
			fatalf(t, "Compose must be explicitly unsupported: %+v", support)
		}
		raw, err := json.Marshal(support)
		if err != nil {
			fatal(t, err)
		}
		if strings.Contains(string(raw), home) || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "synthetic_hub") {
			fatalf(t, "support leaked configuration: %s", raw)
		}
	}

	if err := os.WriteFile(filepath.Join(home, "hub.json"), []byte(`{"database_url":"postgres://synthetic:secret@postgres:5432/synthetic_hub","assistant":{"external":true}}`), 0o600); err != nil {
		fatal(t, err)
	}
	if support := VerificationSupport(Options{Home: home}); support.Reason != "external_manager" {
		fatalf(t, "external manager boundary: %+v", support)
	}
}
