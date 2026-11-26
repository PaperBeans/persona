package devices

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

const testSecretRunEnvCapability = "secret_run_env"

func secretRunEnvGateMachine(t *testing.T, capabilities ...string) (*Machine, *engineNode) {
	t.Helper()
	_, _, m, _ := engineHub(t)
	f, _ := connectEngineNode(t, m)
	m.mu.Lock()
	m.info.Version = "new"
	m.info.Protocol = 1
	m.info.Capabilities = capabilities
	m.mu.Unlock()
	return m, f
}

func requireSecretRunNodeOutdated(t *testing.T, what string, err error) {
	t.Helper()
	var rerr *rpc.Error
	if !errors.As(err, &rerr) || rerr.Code != proto.CodeNodeOutdated || rerr.Message == "" {
		t.Errorf("%s: %v, want node_outdated before anything is sent", what, err)
	}
}

// Neither a check nor a run may send env delivery to a node that only has the original secret_run capability. The
// recurring-provider secret_env capability is deliberately not a substitute for this new one.
func TestSecretRunEnvNeedsItsOwnNodeCapability(t *testing.T) {
	m, f := secretRunEnvGateMachine(t, nodeproto.Sessions, nodeproto.SecretRun, nodeproto.SecretEnv)
	requests := []string{
		`{"check":true,"delivery":"env"}`,
		`{"commands":["true"],"password":"aHVudGVyMg==","timeout_ms":1000,"delivery":"env"}`,
	}
	for _, raw := range requests {
		requireSecretRunNodeOutdated(t, raw, m.call(context.Background(), proto.MethodSecretRun, json.RawMessage(raw), nil))
	}
	if calls := f.seen(); len(calls) != 0 {
		t.Errorf("env requests reached a node without %s: %v", testSecretRunEnvCapability, calls)
	}
}

// A node announcing the new capability receives both the preflight and the approved run with delivery intact.
func TestSecretRunEnvCapabilityAdmitsPreflightAndRun(t *testing.T) {
	m, f := secretRunEnvGateMachine(t, nodeproto.Sessions, nodeproto.SecretRun, testSecretRunEnvCapability)
	requests := []string{
		`{"check":true,"delivery":"env"}`,
		`{"commands":["true"],"password":"aHVudGVyMg==","timeout_ms":1000,"delivery":"env"}`,
	}
	for _, raw := range requests {
		if err := m.call(context.Background(), proto.MethodSecretRun, json.RawMessage(raw), nil); err != nil {
			t.Errorf("capable node refused %s: %v", raw, err)
		}
	}
	calls := f.seen()
	if len(calls) != len(requests) {
		t.Fatalf("capable node calls = %v, want %d", calls, len(requests))
	}
	for i, call := range calls {
		if !strings.Contains(call, `"delivery":"env"`) {
			t.Errorf("call %d lost env delivery: %s", i+1, call)
		}
	}
}

// Omitted and explicit stdin do not require the new capability: an older secret_run node keeps its original behavior.
func TestSecretRunStdinDoesNotNeedEnvCapability(t *testing.T) {
	m, f := secretRunEnvGateMachine(t, nodeproto.Sessions, nodeproto.SecretRun)
	for _, raw := range []string{
		`{"check":true}`,
		`{"commands":["true"],"password":"aHVudGVyMg==","timeout_ms":1000}`,
		`{"check":true,"delivery":"stdin"}`,
		`{"commands":["true"],"password":"aHVudGVyMg==","timeout_ms":1000,"delivery":"stdin"}`,
	} {
		if err := m.call(context.Background(), proto.MethodSecretRun, json.RawMessage(raw), nil); err != nil {
			t.Errorf("old secret_run node refused stdin-compatible request %s: %v", raw, err)
		}
	}
	if calls := f.seen(); len(calls) != 4 {
		t.Errorf("stdin-compatible calls = %v, want all four sent", calls)
	}
}
