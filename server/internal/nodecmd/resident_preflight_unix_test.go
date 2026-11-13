//go:build unix

package nodecmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/node"
	"github.com/nwverse/persona/server/internal/nodeupdate"
)

func residentCheckFixture(t *testing.T, handover bool) string {
	t.Helper()
	check := nodeupdate.Check{Version: "test", OS: runtime.GOOS, Arch: runtime.GOARCH, Program: "personad", ResidentHandover: handover}
	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+string(payload)+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shortResidentHome(t *testing.T) {
	t.Helper()
	home, err := os.MkdirTemp("", "nr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("PERSONA_HOME", filepath.Join(home, ".persona"))
}

func TestResidentPreflightRejectsLegacyBeforeClosingAttach(t *testing.T) {
	shortResidentHome(t)
	transport, err := node.OpenResidentTransport()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	before, err := os.Stat(node.AttachSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	legacy := residentCheckFixture(t, false)
	if err := checkResidentReplacement(context.Background(), legacy, "personad"); err == nil {
		t.Fatal("legacy image accepted descriptor handover")
	}
	conn, err := net.Dial("unix", node.AttachSocketPath())
	if err != nil {
		t.Fatalf("preflight refusal closed attach: %v", err)
	}
	_ = conn.Close()
	after, err := os.Stat(node.AttachSocketPath())
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("preflight refusal replaced the socket")
	}
	if err := checkResidentReplacement(context.Background(), residentCheckFixture(t, true), "personad"); err != nil {
		t.Fatalf("handover-capable image refused: %v", err)
	}
}

func TestLegacyStartupRecoveryReleasesListenerAndRunLock(t *testing.T) {
	shortResidentHome(t)
	transport, err := node.OpenResidentTransport()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	check, err := residentReplacementInfo(context.Background(), residentCheckFixture(t, false), "personad")
	if err != nil {
		t.Fatal(err)
	}
	files, err := prepareResidentRecovery(transport, check.ResidentHandover)
	if err != nil || files != nil {
		t.Fatalf("legacy recovery inherited descriptors: files=%v error=%v", files, err)
	}
	if conn, err := net.Dial("unix", node.AttachSocketPath()); err == nil {
		_ = conn.Close()
		t.Fatal("legacy recovery retained a listener")
	}
	release, err := link.LockFile(node.AttachSocketPath()+".run", false)
	if err != nil {
		t.Fatalf("legacy recovery retained the run lock: %v", err)
	}
	release()
}
