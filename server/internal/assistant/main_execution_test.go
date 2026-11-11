package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestMainExecutionKeepsRouteAndLastObservation(t *testing.T) {
	const main = "main-example"
	f := newFakeHub().withMachine()
	f.hello.Assistant.Machines = []string{"server", "other"}
	f.settings = &Settings{MachineID: "server", Main: main}
	f.sessions["server"] = []proto.Session{{SessionID: main, MachineID: "server", Assistant: true}}
	f.sessions["other"] = []proto.Session{{SessionID: "side", MachineID: "other", Assistant: true}}
	store := &memStore{}
	s := newServiceOn(t, f, store)
	f.nodes = map[string]nodeAnswer{
		assistantwire.DeviceAssistantExecution: func(machine string, params json.RawMessage) (any, error) {
			var p map[string]string
			_ = json.Unmarshal(params, &p)
			if machine != "server" || p["session_id"] != main {
				t.Errorf("wrong execution lookup: %s %s", machine, params)
			}
			return proto.AssistantExecution{State: proto.ExecutionRunning, Source: "native", Harness: "codex",
				Location: &proto.ExecutionLocation{Host: "execution-host"}}, nil
		},
	}
	runtime := func(s *Service) mainExecutionView {
		t.Helper()
		w := serve(s, http.MethodGet, "/api/v1/assistant?view=main-runtime", "")
		var got mainExecutionView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("runtime response: %d %s", w.Code, w.Body)
		}
		return got
	}
	// Change the default before the first runtime fetch. The exact old assistant row
	// already proves which routing node owns the main, even on a legacy settings record.
	w := serve(s, http.MethodPut, "/api/v1/assistant", `{"machine_id":"other"}`)
	var view assistantView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatalf("settings response: %d %s", w.Code, w.Body)
	}
	if view.MachineID != "other" || view.MainMachineID != "server" || view.Main != main {
		t.Fatalf("default change retargeted main: %+v", view.Settings)
	}
	if got := runtime(s); got.State != proto.ExecutionRunning || got.Proxy != "" ||
		got.Harness != "codex" || got.Location == nil || got.Location.Host != "execution-host" {
		t.Fatalf("running report: %+v", got)
	}

	// Simulate a manager restart while the node is disconnected.
	f.settings = nil
	f.devices = []Device{{ID: "server", Name: "routing-label"}, {ID: "other", Online: true}}
	f.nodes[assistantwire.DeviceAssistantExecution] = func(string, json.RawMessage) (any, error) {
		t.Error("offline node must not be queried")
		return nil, errors.New("offline")
	}
	s = newServiceOn(t, f, store)
	if got := runtime(s); got.State != proto.ExecutionOffline || got.MachineID != "server" ||
		got.Harness != "codex" || got.Location == nil || got.Location.Host != "execution-host" {
		t.Fatalf("offline report: %+v", got)
	}

	// An older node's unknown-method error is not exposed and cannot make an old
	// observation look live.
	f.devices[0].Online = true
	f.nodes[assistantwire.DeviceAssistantExecution] = func(string, json.RawMessage) (any, error) {
		return nil, errors.New("unknown method assistant.execution")
	}
	if got := runtime(s); got.State != proto.ExecutionUnknown || got.Location != nil || got.Harness != "" {
		t.Fatalf("legacy node report: %+v", got)
	}
}

func TestMainExecutionDoesNotInventLocationOrUseAnotherDevice(t *testing.T) {
	f := newFakeHub().withMachine()
	f.settings = &Settings{MachineID: "server", Main: "missing"}
	f.sessions["server"] = []proto.Session{{SessionID: "missing", MachineID: "server"}} // ordinary, not assistant
	f.devices = []Device{{ID: "server", Name: "relay-node", Online: true}}
	s := newTestService(t, f)
	ctx := context.Background()
	f.nodes = map[string]nodeAnswer{
		assistantwire.DeviceAssistantExecution: func(string, json.RawMessage) (any, error) {
			return proto.AssistantExecution{State: proto.ExecutionRunning, Source: "runtime", Harness: "example"}, nil
		},
	}
	if got := s.mainExecution(ctx); got.SessionID != "missing" || got.MachineID != "" || got.State != proto.ExecutionUnknown || got.Location != nil {
		t.Fatalf("missing assistant: %+v", got)
	}
	setSettings(t, s, func(v *Settings) { v.Main, v.MainMachineID = "main", "server" })
	if got := s.mainExecution(ctx); got.Proxy != "relay-node" || got.Location != nil || got.Harness != "example" {
		t.Fatalf("proxy must not become a location: %+v", got)
	}

	// Device ownership changed; do not look at or expose the former device.
	f.devices = []Device{}
	f.nodes[assistantwire.DeviceAssistantExecution] = func(string, json.RawMessage) (any, error) {
		t.Error("queried device outside this manager's fleet")
		return nil, nil
	}
	if got := s.mainExecution(ctx); got.MachineID != "" || got.Proxy != "" || got.Location != nil || got.State != proto.ExecutionUnknown {
		t.Fatalf("out-of-scope device: %+v", got)
	}
}

func TestMainExecutionKeepsUnobservedLegacyRouteOnDefaultChange(t *testing.T) {
	f := newFakeHub().withMachine()
	f.hello.Assistant.Machines = []string{"server", "other"}
	f.settings = &Settings{MachineID: "server", Main: "missing"}
	f.sessions["other"] = nil
	s := newTestService(t, f)
	w := serve(s, http.MethodPut, "/api/v1/assistant", `{"machine_id":"other"}`)
	var view assistantView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatalf("settings response: %d %s", w.Code, w.Body)
	}
	if view.MachineID != "other" || view.MainMachineID != "server" || view.Main != "missing" {
		t.Fatalf("legacy route was moved: %+v", view.Settings)
	}
	got := s.mainExecution(context.Background())
	if got.State != proto.ExecutionUnknown || got.Location != nil || got.Harness != "" {
		t.Fatalf("legacy route is not execution evidence: %+v", got)
	}
}
