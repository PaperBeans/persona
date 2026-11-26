package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func sentTextsTo(f *fakeHub, sessionID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sends {
		if sid, _ := m["session_id"].(string); sid == sessionID {
			if txt, _ := m["text"].(string); txt != "" {
				out = append(out, txt)
			}
		}
	}
	return out
}

// When a supervisor's owner-only card expires while the user is away, the hub wakes that
// supervisor once as soon as the user's app comes online, and never repeats for the same matter
// across reconnects or subsequent chat messages.
func TestOwnerPresentWakesSupervisorOnAppOnlineAndDedupes(t *testing.T) {
	oldMax := ownerCardMaxWait
	ownerCardMaxWait = 80 * time.Millisecond
	t.Cleanup(func() { ownerCardMaxWait = oldMax })

	s, f := newQuietServiceOn(t, nil)
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	f.mu.Lock()
	f.known["sup1"] = true
	f.mu.Unlock()
	f.setSessions("server",
		proto.Session{MachineID: "server", SessionID: "conv1", Assistant: true, Status: proto.StatusIdle},
		proto.Session{MachineID: "server", SessionID: "sup1", Assistant: true, Status: proto.StatusIdle},
	)

	// Simulate two expired secret_run cards in the same supervisor conversation (same matter).
	for _, id := range []string{"ix_exp_1", "ix_exp_2"} {
		ix := proto.Interaction{
			ID: id, MachineID: "server", SessionID: "sup1",
			Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
			Summary: "Run sudo backup on Mac mini",
			Fields:  []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		}
		r, err := s.waitInteraction(context.Background(), ix)
		if err != nil || !r.Dismiss || r.Outcome() != proto.OutcomeExpired {
			t.Fatalf("card %s waitInteraction = %+v, %v; want expired", id, r, err)
		}
	}

	// User opens the app -> hub emits fleet.clients with AppOnline=true.
	onlineRaw, _ := json.Marshal(assistantwire.Clients{AppOnline: true})
	if !s.FleetEvent(context.Background(), assistantwire.EventClients, onlineRaw) {
		t.Fatal("FleetEvent(EventClients) not handled")
	}

	waitFor(t, "supervisor woken on app online", func() bool {
		return len(sentTextsTo(f, "sup1")) == 1
	})
	first := sentTextsTo(f, "sup1")[0]
	if !strings.Contains(first, "<persona-supervision") || !strings.Contains(first, "重新发卡") {
		t.Fatalf("unexpected wake message: %q", first)
	}

	// App reconnects (offline -> online) and user speaks in another conversation: must NOT nudge twice.
	offlineRaw, _ := json.Marshal(assistantwire.Clients{AppOnline: false})
	s.FleetEvent(context.Background(), assistantwire.EventClients, offlineRaw)
	s.FleetEvent(context.Background(), assistantwire.EventClients, onlineRaw)
	s.ownerAppeared(context.Background(), "user_spoke", "other_chat")
	<-time.After(100 * time.Millisecond)

	if got := len(sentTextsTo(f, "sup1")); got != 1 {
		t.Fatalf("supervisor nudged %d times for the same matter, want 1", got)
	}
}

// When a supervised task is waiting on an owner-only action (only_owner_knows or judge Waits)
// and the user speaks in any conversation, the hub sends one supervision digest entry to the
// task's supervisor and does not duplicate it on repeated presence signals.
func TestOwnerPresentWakesSupervisedTaskOnUserSpokeAndDedupes(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	ctx := context.Background()

	now := time.Now().UTC()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.State = reviewNeedsUser
		v.Reviews = append(v.Reviews, TaskReview{
			At:       now,
			Verdict:  reviewNeedsUser,
			WhyOwner: whyOnlyOwnerKnow,
			Reason:   "Need owner sudo password to finish install",
		})
		return true
	}); err != nil {
		t.Fatal(err)
	}

	before := len(sentTextsTo(f, "conv1"))

	// User speaks in an unrelated conversation.
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "other_chat"})

	waitFor(t, "supervisor receives owner_present supervision message", func() bool {
		return len(sentTextsTo(f, "conv1")) == before+1
	})
	sent := sentTextsTo(f, "conv1")
	msg := sent[len(sent)-1]
	if !strings.Contains(msg, "signal=owner_present") || !strings.Contains(msg, todo.ID) {
		t.Fatalf("supervision message = %q, want owner_present for %s", msg, todo.ID)
	}

	// Speaking again or app coming online must not repeat the nudge for the same waiting round.
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "other_chat"})
	s.ownerAppeared(ctx, "app_online", "")
	<-time.After(150 * time.Millisecond)

	if got := len(sentTextsTo(f, "conv1")); got != before+1 {
		t.Fatalf("supervisor got %d messages for same task wait, want %d", got, before+1)
	}
}

// If a live owner-only card is already pending in "Needs you" for the task, user presence must not
// redundantly nudge the supervisor to issue another card.
func TestOwnerPresentSkipsWhenLiveOwnerCardAlreadyPending(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	ctx := context.Background()

	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Waits = &OwnerWait{Work: time.Now().UTC(), At: time.Now().UTC(), Reason: "Waiting on password"}
		return true
	}); err != nil {
		t.Fatal(err)
	}

	before := len(sentTextsTo(f, "conv1"))

	// Put a live secret_run card on the supervisor conversation.
	cardCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = s.waitInteraction(cardCtx, proto.Interaction{
			ID: "ix_live", MachineID: "server", SessionID: "conv1",
			Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
			Fields: []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		})
	}()
	waitFor(t, "live secret_run card pending", func() bool {
		_, ok := s.pendingIx("ix_live")
		return ok
	})

	s.ownerAppeared(ctx, "app_online", "")
	<-time.After(150 * time.Millisecond)
	if got := len(sentTextsTo(f, "conv1")); got != before {
		t.Fatalf("expected no extra nudges while live owner card is pending, got %d (before=%d)", got, before)
	}
}
