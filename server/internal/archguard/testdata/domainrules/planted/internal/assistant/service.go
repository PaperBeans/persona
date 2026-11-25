// Package assistant is the planted application: it loads through adapters, reads the clock once, calls the cores and
// acts on what they decided.
//
//persona:layer application
package assistant

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/agentref/agentrefadapt"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcountadapt"
	"github.com/nwverse/persona/server/internal/assistant/inbox/item"
	"github.com/nwverse/persona/server/internal/assistant/inbox/item/itemadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/clock"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/failhttp"
)

// Service is the planted assistant: its stores and its clock.
type Service struct {
	mu       sync.Mutex
	clock    clock.Clock
	sessions func(ctx context.Context) ([]assistantwire.Session, []assistantwire.Interaction, error)
	save     func(itemadapt.Record) error
}

// NeedsYou is the badge's Needs you list.
func (s *Service) NeedsYou(ctx context.Context) (assistantwire.NeedsYou, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, cards, err := s.sessions(ctx)
	if err != nil {
		return assistantwire.NeedsYou{}, err
	}
	return fleetcountadapt.NeedsYou(fleetcount.SurfaceBadge(), sessions, cards), nil
}

// MarkDone marks a new item done for the owner: load, read the clock, decide, act.
func (s *Service) MarkDone(w http.ResponseWriter, id string) {
	it, f := outcome.Unpack(itemadapt.Open(id))
	if f != nil {
		failhttp.Write(w, f)
		return
	}
	decided, f := outcome.Unpack(item.Decide(it, item.CommandMarkDone(item.DoerOwner()), s.clock.Now()))
	if f != nil {
		failhttp.Write(w, f)
		return
	}
	if err := itemadapt.Act(decided, s.save); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// settled logs an agent's settled turn: the adapter's Log goes straight into the log call.
func (s *Service) settled(_ context.Context, agent agentref.Ref) {
	slog.Info("turn settled", "agent", agentrefadapt.Log(agent))
}

// turnSettled is a settled turn the wire reports: its reference becomes the domain value once, at the edge, and goes
// on as it, so nothing here unwraps it again.
func (s *Service) turnSettled(ctx context.Context, w http.ResponseWriter, ref assistantwire.SessionRef) {
	agent, f := outcome.Unpack(agentrefadapt.FromWire(ref))
	if f != nil {
		failhttp.Write(w, f)
		return
	}
	s.settled(ctx, agent)
}
