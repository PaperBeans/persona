package hub

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// restartFleetLoop replaces the hub's Live Activity manager with a new one that loads the persisted rows, as a hub
// restart or a blue-green switch does: everything else it knew (what each device shows, the round, who was started)
// is gone.
func restartFleetLoop(t *testing.T, h *Hub, stop func()) func() {
	t.Helper()
	stop()
	h.live = newLiveActivities(h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.live.run(ctx) }()
	synctest.Wait()
	return func() { cancel(); <-done }
}

// A push-started activity whose update token the phone reported follows the fleet: the agent it lists ending, the
// card it shows being answered, and a hub restart in between each end or update it.
func TestFleetPushStartedActivityFollowsFleet(t *testing.T) {
	working := FleetState{Phase: "working", Working: 1, Items: []FleetItem{{ID: "m/a", Title: "upgrade", Kind: "working"}}}
	asking := FleetState{Phase: "needsYou", NeedsYou: 1, Items: []FleetItem{{ID: "m/b", Title: "todo", Kind: "permission", Interaction: "ix1"}}}
	for _, tc := range []struct {
		name    string
		shown   FleetState
		restart bool
	}{
		{"agent ends", working, false},
		{"card answered", asking, false},
		{"agent ends across a restart", working, true},
		{"card answered across a restart", asking, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, sink, stop := localFleetLoop(t)
				defer func() { stop() }()
				h.live.update("phone", func(v *db.LiveActivity) { v.ActivityID, v.PushToken = "", "" })
				reportLive(t, h, tc.shown, FleetStats{})
				time.Sleep(fleetCoalesce)
				synctest.Wait()
				if got := sink.count("phone", "liveactivity", "start"); got != 1 {
					t.Fatalf("starts: %d, want 1", got)
				}
				// The app, woken by the start, reports the new activity's token.
				h.live.update("phone", func(v *db.LiveActivity) {
					v.ActivityID, v.PushToken, v.StartedAt = "started", "phone-started", time.Now()
				})
				synctest.Wait()
				if tc.restart {
					stop = restartFleetLoop(t, h, stop)
				}
				reportLive(t, h, FleetState{}, FleetStats{})
				time.Sleep(fleetEndAfter + 30*time.Second)
				synctest.Wait()
				if got := sink.count("phone", "liveactivity", "end"); got != 1 {
					t.Fatalf("the activity was not ended: %d end pushes, %d updates", got, sink.count("phone", "liveactivity", "update"))
				}
				if got := sink.count("phone", "liveactivity", "start"); got != 1 {
					t.Fatalf("starts after the end: %d, want 1", got)
				}
			})
		})
	}
}

// What main does when the phone never reports the started activity's token (the owner's case, 2026-11-14).
func TestFleetPushStartWithoutToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sink, stop := localFleetLoop(t)
		defer func() { stop() }()
		h.live.update("phone", func(v *db.LiveActivity) { v.ActivityID, v.PushToken = "", "" })
		working := FleetState{Phase: "working", Working: 1, Items: []FleetItem{{ID: "m/a", Title: "upgrade", Kind: "working"}}}
		asking := FleetState{Phase: "needsYou", NeedsYou: 1, Items: []FleetItem{{ID: "m/b", Title: "todo", Kind: "permission", Interaction: "ix1"}}}

		reportLive(t, h, working, FleetStats{})
		time.Sleep(fleetCoalesce)
		synctest.Wait()
		// A restart in the same round, more than the rolling cooldown later.
		time.Sleep(fleetRefresh)
		stop = restartFleetLoop(t, h, stop)
		time.Sleep(fleetCoalesce)
		synctest.Wait()
		t.Logf("after the restart: starts=%d", sink.count("phone", "liveactivity", "start"))
		// The agent ends; nothing for a while; a card arrives; it is answered.
		reportLive(t, h, FleetState{}, FleetStats{})
		time.Sleep(fleetRefresh + fleetEndAfter)
		synctest.Wait()
		reportLive(t, h, asking, FleetStats{})
		synctest.Wait()
		reportLive(t, h, FleetState{}, FleetStats{})
		time.Sleep(fleetRefresh + fleetEndAfter)
		synctest.Wait()
		t.Logf("at the end: starts=%d updates=%d ends=%d", sink.count("phone", "liveactivity", "start"),
			sink.count("phone", "liveactivity", "update"), sink.count("phone", "liveactivity", "end"))
	})
}
