package fleetcount_test

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// rows is the test's own visitor: it keeps each row's reason and the total.
type rows struct {
	whys  []fleetcount.Why
	total fleetcount.RowCount
}

func (r *rows) VisitAgent(_ slot.Slot, why fleetcount.Why, _ instant.Instant) {
	r.whys = append(r.whys, why)
}
func (r *rows) VisitCard(_ slot.Slot, why fleetcount.Why)           { r.whys = append(r.whys, why) }
func (r *rows) VisitAgentByCard(_, _ slot.Slot, why fleetcount.Why) { r.whys = append(r.whys, why) }
func (r *rows) VisitTotal(total fleetcount.RowCount)                { r.total = total }

var updated = instant.At(time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC))

func waiting(t *testing.T, muting fleetcount.Muting) fleetcount.Agent {
	t.Helper()
	var first slot.Slot // the zero Slot is the caller's first entry
	ref := domaintest.Ok(t, agentref.ParseRef("mac", "s1"))
	return domaintest.Ok(t, fleetcount.NewAgentBuilder(ref, first).
		State(fleetcount.StateNeedsYou(), opt.None[instant.Instant](), updated).
		Role(fleetcount.RoleWorker(), fleetcount.LineageRoot(), muting).
		Build())
}

func TestAMutedAgentDoesNotCount(t *testing.T) {
	for _, c := range []struct {
		name   string
		muting fleetcount.Muting
		want   fleetcount.RowCount
	}{
		{"heard", fleetcount.MutingHeard(), fleetcount.ParseRowCount(1)},
		{"muted", fleetcount.MutingMuted(), fleetcount.ParseRowCount(0)},
	} {
		var got rows
		agents := fleetcount.NewAgents([]fleetcount.Agent{waiting(t, c.muting)})
		fleetcount.AgentsNeedsYou(fleetcount.SurfaceBadge(), agents, fleetcount.NewCards(nil)).Accept(&got)
		if got.total != c.want {
			t.Errorf("a %s agent: the total is not the one it should be", c.name)
		}
	}
}

func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {
	var skipped fleetcount.AgentRoleStep // a skipped stage still compiles; Build refuses it
	got := domaintest.Fails(t, skipped.Role(fleetcount.RoleWorker(), fleetcount.LineageRoot(), fleetcount.MutingHeard()).Build())
	const want = "invalid fleetcount.agent.no_session agent; invalid fleetcount.agent.no_state state; " +
		"invalid fleetcount.agent.no_update updated"
	if got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}
