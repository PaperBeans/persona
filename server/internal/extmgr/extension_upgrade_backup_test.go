package extmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUIData writes name in example-ui's data directory.
func writeUIData(t *testing.T, h *Manager, name, body string) {
	t.Helper()
	dir := h.extensions.reg.DataDir("example-ui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readUIData(h *Manager, name string) string {
	b, err := os.ReadFile(filepath.Join(h.extensions.reg.DataDir("example-ui"), name))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// autoUpgradeTo publishes dir as version v, lets the hub upgrade itself to it and waits until v runs.
func autoUpgradeTo(t *testing.T, h *Manager, mk *upgradeMarket, dir, mid, v string) {
	t.Helper()
	ctx := context.Background()
	setUIVersion(t, dir, v)
	mk.publish(dir)
	if _, err := h.marketRead(ctx, mid); err != nil {
		t.Fatal(err)
	}
	h.extensionUpgrades().upgrade(ctx)
	waitLong(t, "running "+v, func() bool {
		p := h.extensions.reg.Get("example-ui")
		return p.Record.Version == v && h.extensions.view(ctx, p).Status == "running"
	})
}

// Before each automatic upgrade the extension's data is backed up; at most three backups are kept; the upgrade that
// stays keeps its data as it was.
func TestExtensionAutoUpgradeBacksUpData(t *testing.T) {
	h, _, _, mk, dir, mid := upgradeHub(t)
	reg := h.extensions.reg
	writeUIData(t, h, "memory.json", `{"rows":1}`)

	autoUpgradeTo(t, h, mk, dir, mid, "1.1.0")
	backups := reg.Backups("example-ui")
	if len(backups) != 1 || !strings.HasSuffix(backups[0], "-1.0.0.tar.gz") {
		t.Fatalf("after one upgrade: %v", backups)
	}
	if got := readUIData(h, "memory.json"); got != `{"rows":1}` {
		t.Fatalf("data after the upgrade: %s", got)
	}

	for _, v := range []string{"1.2.0", "1.3.0", "1.4.0"} {
		autoUpgradeTo(t, h, mk, dir, mid, v)
	}
	backups = reg.Backups("example-ui")
	if len(backups) != 3 {
		t.Fatalf("kept: %v", backups)
	}
	for i, from := range []string{"1.3.0", "1.2.0", "1.1.0"} { // newest first; the one of 1.0.0 is gone
		if !strings.HasSuffix(backups[i], "-"+from+".tar.gz") {
			t.Fatalf("kept: %v", backups)
		}
	}
}

// A new version that does not come up goes back to the old one and to the data from before it: what it wrote on its
// way down is gone.
func TestExtensionAutoUpgradeRollbackRestoresData(t *testing.T) {
	h, mux, phone, mk, dir, mid := upgradeHub(t)
	ctx := context.Background()
	writeUIData(t, h, "memory.json", `{"schema":1}`)

	setVersion(t, dir, "1.3.0")
	_ = os.WriteFile(filepath.Join(dir, "bin", "ui"), []byte("#!/bin/sh\n"+
		"echo '{\"schema\":2}' > \"$PERSONA_PLUGIN_DATA/memory.json\"\n"+
		"echo half > \"$PERSONA_PLUGIN_DATA/migration.lock\"\nexit 3\n"), 0o755)
	mk.publish(dir)
	if _, err := h.marketRead(ctx, mid); err != nil {
		t.Fatal(err)
	}
	h.extensionUpgrades().upgrade(ctx)
	waitLong(t, "the rollback", func() bool {
		_, d := call(t, mux, "GET", "/api/v1/plugins/example-ui/-/", phone, "")
		n, _ := d["upgrade_note"].(map[string]any)
		return d["version"] == "1.0.0" && n["kind"] == "rolled_back" && n["to"] == "1.3.0"
	})
	waitLong(t, "1.0.0 running", func() bool {
		return h.extensions.view(ctx, h.extensions.reg.Get("example-ui")).Status == "running"
	})
	if got := readUIData(h, "memory.json"); got != `{"schema":1}` {
		t.Fatalf("data not restored: %s", got)
	}
	if _, err := os.Stat(filepath.Join(h.extensions.reg.DataDir("example-ui"), "migration.lock")); !os.IsNotExist(err) {
		t.Fatalf("what the new version wrote stayed: %v", err)
	}
	if n := len(h.extensions.reg.Backups("example-ui")); n != 1 {
		t.Fatalf("backups: %d", n)
	}
}

// A new version that declares upgrade.health_check and fails it is rolled back even though it is running; an old
// version that declares upgrade.prepare is asked before its data is kept.
func TestExtensionAutoUpgradeHealthCheckAndPrepare(t *testing.T) {
	h, mux, phone, mk, dir, mid := upgradeHub(t)
	ctx := context.Background()
	editManifest(t, dir, func(m map[string]any) { m["upgrade"] = map[string]any{"prepare": true, "health_check": true} })
	autoUpgradeTo(t, h, mk, dir, mid, "1.1.0") // healthy: it stays
	writeUIData(t, h, "memory.json", `{"schema":1}`)

	setUIVersion(t, dir, "1.2.0")
	self, _ := os.Executable()
	_ = os.WriteFile(filepath.Join(dir, "bin", "ui"), []byte("#!/bin/sh\n"+
		"echo '{\"schema\":2}' > \"$PERSONA_PLUGIN_DATA/memory.json\"\n"+
		"EXAMPLE_UI_VERSION=1.2.0 EXAMPLE_UI_UNHEALTHY='index missing' PERSONA_TEST_PLUGIN=ui exec "+self+" \"$@\"\n"), 0o755)
	mk.publish(dir)
	if _, err := h.marketRead(ctx, mid); err != nil {
		t.Fatal(err)
	}
	h.extensionUpgrades().upgrade(ctx)
	var note map[string]any
	waitLong(t, "the rollback", func() bool {
		_, d := call(t, mux, "GET", "/api/v1/plugins/example-ui/-/", phone, "")
		note, _ = d["upgrade_note"].(map[string]any)
		return d["version"] == "1.1.0" && note["kind"] == "rolled_back" && note["to"] == "1.2.0"
	})
	if !strings.Contains(note["detail"].(string), "index missing") {
		t.Fatalf("why: %v", note)
	}
	if got := readUIData(h, "memory.json"); got != `{"schema":1}` {
		t.Fatalf("data not restored: %s", got)
	}
	if got := readUIData(h, "prepared-1.2.0"); got != "ok" { // 1.1.0 was asked, and its answer is in the backup
		t.Fatalf("upgrade.prepare: %s", got)
	}
}

// An extension that requires a backup is not upgraded when the backup fails, and its installed version runs again.
func TestExtensionAutoUpgradeRequiredBackupFails(t *testing.T) {
	h, mux, phone, mk, dir, mid := upgradeHub(t)
	ctx := context.Background()
	reg := h.extensions.reg
	writeUIData(t, h, "memory.json", `{"schema":1}`)
	// Its backup directory cannot be made: a file is in its way.
	if err := os.MkdirAll(filepath.Dir(reg.BackupDir("example-ui")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg.BackupDir("example-ui"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	editManifest(t, dir, func(m map[string]any) { m["upgrade"] = map[string]any{"backup": "required"} })
	setUIVersion(t, dir, "1.1.0")
	mk.publish(dir)
	if _, err := h.marketRead(ctx, mid); err != nil {
		t.Fatal(err)
	}
	h.extensionUpgrades().upgrade(ctx)
	waitLong(t, "the failure", func() bool {
		_, d := call(t, mux, "GET", "/api/v1/plugins/example-ui/-/", phone, "")
		n, _ := d["upgrade_note"].(map[string]any)
		return n["kind"] == upgradeFailed && n["to"] == "1.1.0"
	})
	if v := installedVersion(h); v != "1.0.0" {
		t.Fatalf("upgraded without its backup: %s", v)
	}
	waitLong(t, "1.0.0 running again", func() bool {
		return h.extensions.view(ctx, reg.Get("example-ui")).Status == "running"
	})

	// Without "required" the same failure does not hold the upgrade back.
	editManifest(t, dir, func(m map[string]any) { delete(m, "upgrade") })
	autoUpgradeTo(t, h, mk, dir, mid, "1.2.0")
	if got := readUIData(h, "memory.json"); got != `{"schema":1}` {
		t.Fatalf("data: %s", got)
	}
}
