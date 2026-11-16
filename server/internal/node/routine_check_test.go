package node

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRoutineCheck(t *testing.T) {
	if loginShellProgram() == "" {
		t.Skip("node shell unavailable")
	}
	for _, tc := range []struct {
		name, command string
		code          int
		reason        string
		timeout       bool
	}{
		{name: "pass", command: "exit 0"},
		{name: "skip", command: "printf 'holiday\\nmore\\n'; exit 1", code: 1, reason: "holiday"},
		{name: "error", command: "printf 'failed\\n' >&2; exit 4", code: 4, reason: "failed"},
		{name: "timeout", command: "sleep 30; exit 0", timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(proto.RoutineCheckParams{Command: tc.command, Timeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			result, err := routineCheck(t.Context(), raw)
			if tc.timeout {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout = %v", err)
				}
				return
			}
			if err != nil || result.ExitCode != tc.code || result.Reason != tc.reason {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestRoutineCheckDeadlineAfterShellExit(t *testing.T) {
	if runtime.GOOS == "windows" || loginShellProgram() == "" {
		t.Skip("requires a Unix shell")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	raw, err := json.Marshal(proto.RoutineCheckParams{Command: "sleep 30 & exit 0", Timeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := routineCheck(ctx, raw); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("background child kept the check waiting for %s", elapsed)
	}
}
