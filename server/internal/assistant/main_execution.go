package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const mainExecutionKey = "assistant.main_execution"

type mainExecutionView struct {
	SessionID string `json:"session_id,omitempty"`
	MachineID string `json:"machine_id,omitempty"` // routing only, never a location
	Proxy     string `json:"proxy,omitempty"`
	proto.AssistantExecution
}

type mainExecutionBook struct {
	mu     sync.Mutex
	last   mainExecutionView
	loaded bool
}

// mainRouteMachine is also used by old integrations when no confirmed binding
// exists yet. The execution summary never uses this default as evidence.
func (v Settings) mainRouteMachine() string {
	if v.MainMachineID != "" {
		return v.MainMachineID
	}
	return v.MachineID
}

// confirmedMainMachine upgrades an old binding using an exact assistant row.
// Never take the newly selected default device as proof that it owns Main.
func (s *Service) confirmedMainMachine(ctx context.Context, v Settings) string {
	if v.Main == "" {
		return ""
	}
	if v.MainMachineID != "" {
		if s.fleetOn() {
			if m, ok := s.fleetMachine(v.MainMachineID); ok {
				return m.ID
			}
		}
		return v.MainMachineID
	}
	if c, ok := s.session(ctx, v.MachineID, v.Main); ok && c.Assistant && spaceOf(c) == "" {
		return v.MachineID
	}
	var found string
	for _, d := range s.devices(ctx) {
		if d.ID == v.MachineID {
			continue
		}
		if c, ok := s.session(ctx, d.ID, v.Main); ok && c.Assistant && spaceOf(c) == "" {
			if found != "" {
				return "" // multiple legacy copies; neither is a confirmed binding
			}
			found = d.ID
		}
	}
	return found
}

func (s *Service) mainExecution(ctx context.Context) mainExecutionView {
	v := s.settings(ctx)
	out := mainExecutionView{SessionID: v.Main, AssistantExecution: proto.AssistantExecution{State: proto.ExecutionUnknown}}
	out.MachineID = s.confirmedMainMachine(ctx, v)
	if out.MachineID == "" {
		return out
	}
	if v.MainMachineID == "" {
		// Remember a legacy main that was confirmed while its row was still available.
		_, _ = s.updateSettings(ctx, func(current *Settings) error {
			if current.Main == v.Main && current.MainMachineID == "" {
				current.MainMachineID = out.MachineID
			}
			return nil
		})
	}
	d, exists := s.device(ctx, out.MachineID)
	if !exists {
		out.MachineID = "" // the manager may no longer own this device
		return out
	}
	if !d.Online && !d.Reachable {
		out.State = proto.ExecutionOffline
	} else {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.call(cctx, out.MachineID, assistantwire.DeviceAssistantExecution, map[string]string{"session_id": v.Main}, &out.AssistantExecution)
		cancel()
		if err != nil || out.State == "" {
			out.AssistantExecution = proto.AssistantExecution{State: proto.ExecutionUnknown}
		}
	}
	if out.Source == "runtime" {
		out.Proxy = d.Name
	}
	return s.withLastMainExecution(ctx, out)
}

// Keep one confirmed observation per manager across link and manager restarts.
// A newer observation of a runtime without a location replaces any old one.
func (s *Service) withLastMainExecution(ctx context.Context, out mainExecutionView) mainExecutionView {
	b := &s.main.execution
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.settings(ctx).Main != out.SessionID {
		return out
	}
	st := s.store()
	if !b.loaded {
		if st == nil {
			b.loaded = true
		} else if _, err := st.Setting(ctx, mainExecutionKey, &b.last); err == nil {
			b.loaded = true
		}
	}
	last := b.last
	same := last.SessionID == out.SessionID && last.MachineID == out.MachineID
	if out.State == proto.ExecutionOffline || out.State == proto.ExecutionStopped {
		if same && (out.Source == "" || out.Source == last.Source) &&
			(out.Harness == "" || out.Harness == last.Harness) {
			if out.Location == nil {
				out.Location = last.Location
			}
			if out.Harness == "" {
				out.Harness, out.HarnessLabel = last.Harness, last.HarnessLabel
			}
			if out.Source == "" {
				out.Source, out.Proxy = last.Source, last.Proxy
			}
		}
		return out
	}
	if out.State != proto.ExecutionRunning || out.Source == "" {
		return out
	}
	if same && last.State == out.State && last.Source == out.Source && last.Proxy == out.Proxy &&
		last.Harness == out.Harness && last.HarnessLabel == out.HarnessLabel && sameLocation(last.Location, out.Location) {
		return out
	}
	if st == nil || st.PutSetting(ctx, mainExecutionKey, out) == nil {
		b.last = out
	}
	return out
}

func sameLocation(a, b *proto.ExecutionLocation) bool {
	return a == b || a != nil && b != nil && *a == *b
}
