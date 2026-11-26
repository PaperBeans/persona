package recovery

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/operations/backup"
	"github.com/nwverse/persona/server/internal/secretkey"
)

func TestOverviewUsesOnlyTheDefaultDirectoryAndHonestCapabilities(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "backups")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticBundle(t, filepath.Join(directory, "alpha.tar"), false)
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticBundle(t, filepath.Join(directory, "nested", "ignored.tar"), false)
	outside := filepath.Join(home, "outside-private.tar")
	writeSyntheticBundle(t, outside, false)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(directory, "escape.tar")); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(home)
	s.support = func(backup.Options) backup.Support {
		return backup.Support{State: "unsupported", Reason: "compose"}
	}
	s.createSupport = func(backup.Options) backup.Support { return backup.Support{State: "supported"} }
	overview, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Bundles) != 1 || overview.Bundles[0].Name != "alpha.tar" {
		t.Fatalf("unsafe or nested discovery: %+v", overview.Bundles)
	}
	bundle := overview.Bundles[0]
	if len(bundle.ID) != 64 || strings.Contains(bundle.ID, "alpha") || bundle.Revision == "" || bundle.ArchiveBytes == 0 {
		t.Fatalf("bundle identity is not opaque and revision-bound: %+v", bundle)
	}
	if overview.Capabilities.Inspect.State != "supported" || overview.Capabilities.Create.State != "guided" ||
		overview.Capabilities.Restore.State != "unsupported" || overview.Capabilities.Download.State != "unsupported" ||
		overview.Capabilities.Verify.Reason != "compose" || overview.History.State != "unavailable" {
		t.Fatalf("capabilities overstate recovery support: %+v", overview.Capabilities)
	}
	if _, err := s.Inspect(context.Background(), "../outside-private.tar"); !errors.Is(err, ErrInvalidBundleID) {
		t.Fatalf("path traversal reached a bundle: %v", err)
	}
}

func TestInspectSyntheticBundleIsRedactedAndRevisionBound(t *testing.T) {
	home := t.TempDir()
	name := filepath.Join(home, "backups", "private-name.tar")
	writeSyntheticBundle(t, name, false)
	s := NewService(home)
	overview, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := overview.Bundles[0].ID
	check, err := s.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if check.State != "passed" || check.Kind != "offline_integrity" || check.Report == nil ||
		check.Report.DatabaseRestoreVerified || check.Bundle.Revision != overview.Bundles[0].Revision {
		t.Fatalf("inspection did not describe only offline integrity: %+v", check)
	}
	raw, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{home, "synthetic_hub_database", "synthetic_manager_database", "synthetic-token", "fixture-private-note"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("inspection disclosed %q: %s", private, raw)
		}
	}

	// A result must never be attached to a file that replaced the selected one.
	s.inspect = func(ctx context.Context, path string) (backup.InspectionReport, error) {
		writeSyntheticBundle(t, filepath.Join(home, "backups", "private-name.tar"), true)
		return backup.InspectBundle(ctx, path)
	}
	changed, err := s.Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if changed.State != "failed" || changed.Reason != "bundle_changed" || changed.Report != nil {
		t.Fatalf("stale inspection was presented as current: %+v", changed)
	}
}

func TestVerifyIsExplicitAndCanBeUnsupportedWithoutRunning(t *testing.T) {
	home := t.TempDir()
	writeSyntheticBundle(t, filepath.Join(home, "backups", "alpha.tar"), false)
	s := NewService(home)
	calls := 0
	s.verify = func(_ context.Context, opts backup.Options) (backup.Result, error) {
		calls++
		if opts.WritesQuiesced || opts.Output != "" || opts.Bundle == "" {
			t.Fatalf("verification gained create/restore inputs: %+v", opts)
		}
		return backup.Result{Bundle: opts.Bundle, Verified: true}, nil
	}
	s.support = func(backup.Options) backup.Support { return backup.Support{State: "supported"} }
	overview, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, err := s.Verify(context.Background(), overview.Bundles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || check.State != "passed" || check.Kind != "database_restore" {
		t.Fatalf("explicit verification: calls=%d check=%+v", calls, check)
	}

	s.support = func(backup.Options) backup.Support {
		return backup.Support{State: "unsupported", Reason: "postgresql_tools"}
	}
	unsupported, err := s.Verify(context.Background(), overview.Bundles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || unsupported.State != "unsupported" || unsupported.Reason != "postgresql_tools" {
		t.Fatalf("unsupported verification ran or looked successful: calls=%d check=%+v", calls, unsupported)
	}
}

func TestUnreadableDirectoryAndMalformedBundleNeverLookHealthy(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(home, "backups", "broken.tar")
	if err := os.WriteFile(name, []byte("not a backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewService(home)
	overview, err := s.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check, err := s.Inspect(context.Background(), overview.Bundles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if check.State != "failed" || check.Report != nil {
		t.Fatalf("malformed bundle has a passing report: %+v", check)
	}

	if runtime.GOOS != "windows" {
		linkedHome := t.TempDir()
		if err := os.Symlink(filepath.Join(home, "backups"), filepath.Join(linkedHome, "backups")); err != nil {
			t.Fatal(err)
		}
		overview, err = NewService(linkedHome).Overview(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if overview.Discovery.State != "unsupported" || len(overview.Bundles) != 0 {
			t.Fatalf("symbolic-link discovery was treated as authoritative: %+v", overview)
		}
	}
}

func writeSyntheticBundle(t *testing.T, name string, alternate bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	hubKey := bytes.Repeat([]byte("h"), secretkey.Size)
	managerKey := bytes.Repeat([]byte("m"), secretkey.Size)
	payload := []struct {
		name string
		data []byte
	}{
		{"db/hub.dump", []byte("synthetic hub dump")},
		{"db/manager.dump", []byte("synthetic manager dump")},
		{"runtime/hub.json", []byte(`{"token":"synthetic-token"}`)},
		{"runtime/secret.key", hubKey},
		{"runtime/managers/assistant/secret.key", managerKey},
		{"runtime/managers/assistant/manager.json", []byte(`{}`)},
	}
	if alternate {
		payload = append(payload, struct {
			name string
			data []byte
		}{"kb/fixture-note.md", []byte("fixture-private-note")})
	}
	manifest := backup.Manifest{
		Format: 1, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Hub:         backup.DatabaseIdentity{Schema: []backup.Migration{{Version: 1, Name: "setup", Part: "hub"}}},
		Manager:     backup.DatabaseIdentity{Schema: []backup.Migration{{Version: 1, Name: "setup", Part: "manager"}}},
		HubDatabase: "synthetic_hub_database", ManagerDatabase: "synthetic_manager_database",
		HubKeyFingerprint: secretkey.Fingerprint(hubKey), ManagerKeyFingerprint: secretkey.Fingerprint(managerKey),
		KnowledgeBase: backup.KnowledgeBaseLocator{Relative: "data", Present: alternate},
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	write := func(name string, data []byte) {
		t.Helper()
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range payload {
		digest := sha256.Sum256(item.data)
		manifest.Files = append(manifest.Files, backup.FileEntry{Path: item.name, Size: int64(len(item.data)), Mode: 0o600, SHA256: hex.EncodeToString(digest[:])})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	write("manifest.json", encoded)
	for _, item := range payload {
		write(item.name, item.data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyOneBundleCheckRunsAtATime(t *testing.T) {
	home := t.TempDir()
	writeSyntheticBundle(t, filepath.Join(home, "backups", "alpha.tar"), false)
	// The hub builds a Service per request, so the slot must hold across instances.
	first, second := NewService(home), NewService(home)
	entered, release := make(chan struct{}), make(chan struct{})
	first.support = func(backup.Options) backup.Support { return backup.Support{State: "supported"} }
	first.verify = func(_ context.Context, opts backup.Options) (backup.Result, error) {
		close(entered)
		<-release
		return backup.Result{Bundle: opts.Bundle, Verified: true}, nil
	}
	second.support = first.support
	secondCalls := 0
	second.verify = func(_ context.Context, opts backup.Options) (backup.Result, error) {
		secondCalls++
		return backup.Result{Bundle: opts.Bundle, Verified: true}, nil
	}
	overview, err := first.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := overview.Bundles[0].ID
	done := make(chan error, 1)
	go func() {
		_, err := first.Verify(context.Background(), id)
		done <- err
	}()
	<-entered
	if _, err := second.Verify(context.Background(), id); !errors.Is(err, ErrBusy) || secondCalls != 0 {
		t.Fatalf("a second verification ran beside the first: err=%v calls=%d", err, secondCalls)
	}
	if _, err := second.Inspect(context.Background(), id); !errors.Is(err, ErrBusy) {
		t.Fatalf("an inspection ran beside a verification: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if check, err := second.Verify(context.Background(), id); err != nil || check.State != "passed" || secondCalls != 1 {
		t.Fatalf("the slot was not released: err=%v check=%+v calls=%d", err, check, secondCalls)
	}
}
