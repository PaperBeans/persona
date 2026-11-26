package extmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension/host"
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

// Non-OnCall extensions declaring upgrade.health_check (such as connector extensions without on_startup or on_http+RPC
// extensions) start in StateStopped until held or invoked. watchUpgrade must wait for StateRunning instead of calling
// upgrade.health on a stopped non-OnCall process (which returns ErrNotRunning and causes a false rollback), while still
// lazily starting OnCall extensions and rolling back genuinely unhealthy ones.
func TestWatchUpgradeNonOnCallHealthCheckWaitsForRunning(t *testing.T) {
	setupStoppedHealthCheckProc := func(t *testing.T, onCall bool, unhealthy string) (*Manager, *host.Proc) {
		t.Helper()
		h, mux, phone, mk, dir, mid := upgradeHub(t)
		editManifest(t, dir, func(m map[string]any) { m["upgrade"] = map[string]any{"health_check": true} })
		autoUpgradeTo(t, h, mk, dir, mid, "1.1.0")
		// Wait for autoUpgradeTo's background watchUpgrade goroutine to settle before replacing byID["example-ui"].
		waitLong(t, "1.1.0 watch settled", func() bool {
			_, d := call(t, mux, "GET", "/api/v1/plugins/example-ui/-/", phone, "")
			n, _ := d["upgrade_note"].(map[string]any)
			return n["kind"] == "upgraded" && n["to"] == "1.1.0"
		})

		// Replace the running startup proc with a stopped non-Startup proc matching specOf for a connector/on_http
		// extension (OnCall=false) or a lazy on_call extension (OnCall=true).
		h.extensions.mu.Lock()
		old := h.extensions.byID["example-ui"]
		delete(h.extensions.byID, "example-ui")
		h.extensions.mu.Unlock()
		if old != nil && old.proc != nil {
			old.proc.Stop()
		}

		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		env := []string{"PERSONA_TEST_PLUGIN=ui", "EXAMPLE_UI_VERSION=1.1.0"}
		if unhealthy != "" {
			env = append(env, "EXAMPLE_UI_UNHEALTHY="+unhealthy)
		}
		p := host.Start(t.Context(), host.Spec{
			ID:      "example-ui",
			Name:    "example-ui",
			Command: self,
			Dir:     h.extensions.reg.DataDir("example-ui"),
			Env:     env,
			RPC:     true,
			Startup: false,
			OnCall:  onCall,
		})
		t.Cleanup(p.Stop)
		if st, _ := p.Status(); st != host.StateStopped {
			t.Fatalf("initial proc state = %q, want %q", st, host.StateStopped)
		}
		h.extensions.mu.Lock()
		h.extensions.byID["example-ui"] = &hubExtension{id: "example-ui", proc: p}
		h.extensions.mu.Unlock()
		return h, p
	}

	t.Run("unheld non-OnCall stopped proc does not falsely roll back", func(t *testing.T) {
		h, _ := setupStoppedHealthCheckProc(t, false, "")
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		rolledBack, why := h.extensions.watchUpgrade(ctx, "example-ui")
		if rolledBack {
			t.Fatalf("unheld non-OnCall extension was falsely rolled back: %q", why)
		}
		if v := installedVersion(h); v != "1.1.0" {
			t.Fatalf("installed version = %s, want 1.1.0", v)
		}
	})

	t.Run("held non-OnCall connector-style start passes upgrade.health after reaching StateRunning", func(t *testing.T) {
		h, p := setupStoppedHealthCheckProc(t, false, "")
		done := make(chan struct{})
		var (
			rolledBack bool
			why        string
		)
		go func() {
			defer close(done)
			rolledBack, why = h.extensions.watchUpgrade(t.Context(), "example-ui")
		}()

		timer := time.NewTimer(50 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
			t.Fatalf("watchUpgrade returned before Hold() started the process: rolledBack=%v why=%q", rolledBack, why)
		case <-timer.C:
		}
		release := p.Hold()
		defer release()

		<-done
		if rolledBack {
			t.Fatalf("healthy non-OnCall extension was falsely rolled back: %q", why)
		}
		if v := installedVersion(h); v != "1.1.0" {
			t.Fatalf("installed version = %s, want 1.1.0", v)
		}
	})

	t.Run("held non-OnCall unhealthy extension rolls back with real health error after reaching StateRunning", func(t *testing.T) {
		h, p := setupStoppedHealthCheckProc(t, false, "index missing")
		done := make(chan struct{})
		var (
			rolledBack bool
			why        string
		)
		go func() {
			defer close(done)
			rolledBack, why = h.extensions.watchUpgrade(t.Context(), "example-ui")
		}()

		timer := time.NewTimer(50 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
			t.Fatalf("watchUpgrade returned before Hold() started the process: rolledBack=%v why=%q", rolledBack, why)
		case <-timer.C:
		}
		release := p.Hold()
		defer release()

		<-done
		if !rolledBack {
			t.Fatal("unhealthy non-OnCall extension was not rolled back")
		}
		if !strings.Contains(why, "index missing") {
			t.Fatalf("rollback reason = %q, want substring %q", why, "index missing")
		}
		if v := installedVersion(h); v != "1.0.0" {
			t.Fatalf("installed version = %s, want 1.0.0 after rollback", v)
		}
	})

	t.Run("control OnCall extension lazily starts from StateStopped for upgrade.health", func(t *testing.T) {
		h, _ := setupStoppedHealthCheckProc(t, true, "lazy broken")
		rolledBack, why := h.extensions.watchUpgrade(t.Context(), "example-ui")
		if !rolledBack {
			t.Fatal("unhealthy OnCall extension was not rolled back")
		}
		if !strings.Contains(why, "lazy broken") {
			t.Fatalf("rollback reason = %q, want substring %q", why, "lazy broken")
		}
		if v := installedVersion(h); v != "1.0.0" {
			t.Fatalf("installed version = %s, want 1.0.0 after rollback", v)
		}
	})

	t.Run("superseded watchUpgrade preserves newer backup and inflight tracking", func(t *testing.T) {
		h, p := setupStoppedHealthCheckProc(t, false, "")
		key := h.extensions.reg.StorageKeyOf("example-ui")

		// Start watching 1.1.0 while stopped.
		done := make(chan struct{})
		var rolledBack bool
		go func() {
			defer close(done)
			rolledBack, _ = h.extensions.watchUpgrade(t.Context(), "example-ui")
		}()

		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
			t.Fatal("watchUpgrade exited prematurely before superseding")
		case <-timer.C:
		}

		// Simulate a newer 1.2.0 upgrade taking over the pending backup, inflight marker, and process slot.
		h.extensions.upgradeBackups.hold(key, pendingBackup{from: "1.1.0", to: "1.2.0", backup: "backup-1.1.0.tar.gz"})
		u := h.extensionUpgrades()
		u.mu.Lock()
		if u.inflight == nil {
			u.inflight = map[string]string{}
		}
		u.inflight[key] = "1.2.0"
		u.mu.Unlock()

		h.extensions.mu.Lock()
		h.extensions.byID["example-ui"] = &hubExtension{id: "example-ui"}
		h.extensions.mu.Unlock()
		// Trigger a state change on the old proc so the superseded watchUpgrade wakes immediately.
		release := p.Hold()
		defer release()
		<-done

		if rolledBack {
			t.Fatal("superseded watchUpgrade rolled back")
		}
		if got, ok := h.extensions.upgradeBackups.take(key, "1.2.0"); !ok || got.backup != "backup-1.1.0.tar.gz" {
			t.Fatalf("superseded watchUpgrade dropped newer version's pending backup: %+v, ok=%v", got, ok)
		}
		u.watched(t.Context(), "example-ui", "1.0.0", "1.1.0", false, "")
		u.mu.Lock()
		inflight := u.inflight[key]
		u.mu.Unlock()
		if inflight != "1.2.0" {
			t.Fatalf("superseded watched() clobbered newer inflight version: got %q, want %q", inflight, "1.2.0")
		}
	})

	t.Run("canceled context during upgradeHealth does not roll back", func(t *testing.T) {
		h, _ := setupStoppedHealthCheckProc(t, true, "")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		rolledBack, why := h.extensions.watchUpgrade(ctx, "example-ui")
		if rolledBack {
			t.Fatalf("canceled watchUpgrade rolled back: %q", why)
		}
		if v := installedVersion(h); v != "1.1.0" {
			t.Fatalf("installed version = %s, want 1.1.0", v)
		}
	})
}
