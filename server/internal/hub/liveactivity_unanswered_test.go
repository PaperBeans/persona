package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

var (
	fleetWorking = FleetState{Phase: "working", Working: 1, Items: []FleetItem{{ID: "m/a", Title: "upgrade", Kind: "working"}}}
	fleetAsking  = FleetState{Phase: "needsYou", NeedsYou: 1, Items: []FleetItem{{ID: "m/b", Title: "todo", Kind: "permission", Interaction: "ix1"}}}
)

// fleetRun is localFleetLoop's hub with a loop that can be restarted, stopped when the test ends (a failed one too).
type fleetRun struct {
	h    *Hub
	sink *fleetPushSink
	stop func()
}

func newFleetRun(t *testing.T) *fleetRun {
	t.Helper()
	r := &fleetRun{}
	r.h, r.sink, r.stop = localFleetLoop(t)
	t.Cleanup(func() { r.stop() })
	return r
}

// restart replaces the hub's Live Activity manager with a new one over the same store, as a hub restart or a
// blue-green switch does: what each device shows, the round and who was started in it are gone.
func (r *fleetRun) restart() {
	r.stop()
	r.h.live = newLiveActivities(r.h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.h.live.run(ctx) }()
	synctest.Wait()
	r.stop = func() { cancel(); <-done }
}

// elapse moves the bubble's clock on by d and lets the loop settle.
func elapse(d time.Duration) {
	time.Sleep(d) // sleep-ok: the fake clock of a synctest bubble, no wall time
	synctest.Wait()
}

func fleetStarts(sink *fleetPushSink) int { return sink.count("phone", "liveactivity", "start") }

// A push-started activity whose update token the phone reported follows the fleet as before: the agent it lists
// ending and the card it shows being answered end it, across a hub restart too, and start nothing more.
func TestFleetPushStartedActivityFollowsFleet(t *testing.T) {
	for _, tc := range []struct {
		name    string
		shown   FleetState
		restart bool
	}{
		{"agent ends", fleetWorking, false},
		{"card answered", fleetAsking, false},
		{"agent ends across a restart", fleetWorking, true},
		{"card answered across a restart", fleetAsking, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newFleetRun(t)
				h, sink := r.h, r.sink
				h.live.update("phone", func(v *db.LiveActivity) { v.ActivityID, v.PushToken = "", "" })
				reportLive(t, h, tc.shown, FleetStats{})
				elapse(fleetCoalesce)
				if got := fleetStarts(sink); got != 1 {
					t.Fatalf("starts: %d, want 1", got)
				}
				// The app, woken by the start, reports the new activity's token.
				h.live.update("phone", func(v *db.LiveActivity) {
					v.ActivityID, v.PushToken, v.StartedAt = "started", "phone-started", time.Now()
				})
				synctest.Wait()
				if tc.restart {
					r.restart()
				}
				reportLive(t, h, FleetState{}, FleetStats{})
				elapse(fleetEndAfter + 30*time.Second)
				if got := sink.count("phone", "liveactivity", "end"); got != 1 {
					t.Fatalf("the activity was not ended: %d end pushes", got)
				}
				if got := fleetStarts(sink); got != 1 {
					t.Fatalf("starts after the end: %d, want 1", got)
				}
			})
		})
	}
}

// A push-start the phone never answered (#7641: no update token came back, so the hub can neither update nor end
// that activity) holds the next one: none in the same round after a restart, none in the next round. A token report,
// the app connecting, or fleetMaxAge lifts it, and the store keeps both the hold and the lift across restarts.
func TestFleetUnansweredStartHoldsTheNext(t *testing.T) {
	// unanswered leaves a start nobody answered and, a restart and a round later, a request waiting.
	unanswered := func(t *testing.T) (r *fleetRun, started time.Time) {
		r = newFleetRun(t)
		h, sink := r.h, r.sink
		h.live.update("phone", func(v *db.LiveActivity) { v.ActivityID, v.PushToken = "", "" })
		reportLive(t, h, fleetWorking, FleetStats{})
		elapse(fleetCoalesce)
		started = time.Now()
		if got := fleetStarts(sink); got != 1 {
			t.Fatalf("starts: %d, want 1", got)
		}
		// Same round, past the rolling cooldown, on a hub that restarted (or the other slot).
		elapse(fleetRefresh + time.Minute)
		r.restart()
		elapse(fleetCoalesce)
		if got := fleetStarts(sink); got != 1 {
			t.Fatalf("restart in the same round: %d starts, want 1", got)
		}
		// The agent ends; later a request arrives: a new round.
		reportLive(t, h, FleetState{}, FleetStats{})
		elapse(fleetRefresh)
		reportLive(t, h, fleetAsking, FleetStats{})
		elapse(fleetCoalesce)
		if got := fleetStarts(sink); got != 1 {
			t.Fatalf("next round: %d starts, want 1", got)
		}
		if n := sink.count("phone", "liveactivity", "update") + sink.count("phone", "liveactivity", "end"); n != 0 {
			t.Fatalf("pushes to an activity without a token: %d", n)
		}
		return r, started
	}

	t.Run("token report", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r, _ := unanswered(t)
			h, sink := r.h, r.sink
			h.live.update("phone", func(v *db.LiveActivity) {
				v.ActivityID, v.PushToken, v.StartedAt = "started", "phone-started", time.Now()
			})
			elapse(20 * time.Second) // the next tick refreshes an activity the hub has pushed nothing to
			if got := sink.count("phone", "liveactivity", "update"); got != 1 {
				t.Fatalf("the reported activity got %d updates, want 1", got)
			}
			reportLive(t, h, FleetState{}, FleetStats{}) // answered: the activity ends
			elapse(fleetEndAfter + 30*time.Second)
			if got := sink.count("phone", "liveactivity", "end"); got != 1 {
				t.Fatalf("ends: %d, want 1", got)
			}
			r.restart()
			reportLive(t, h, fleetWorking, FleetStats{})
			elapse(fleetCoalesce)
			if got := fleetStarts(sink); got != 2 {
				t.Fatalf("after the phone reported a token: %d starts, want 2", got)
			}
		})
	})

	t.Run("app connects", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r, _ := unanswered(t)
			h, sink := r.h, r.sink
			reportLive(t, h, FleetState{}, FleetStats{})
			elapse(fleetCoalesce)
			app := &client{hub: h, device: "credential-ios", role: roleFull, out: make(chan []byte, 64), clean: true}
			addTestClient(h, app)
			h.onClientMessage(context.Background(), app, []byte(`{"type":"register_phone","device_id":"phone","tools":[]}`))
			synctest.Wait()
			h.dropClient(app)
			synctest.Wait()
			r.restart()
			reportLive(t, h, fleetWorking, FleetStats{})
			elapse(fleetCoalesce)
			if got := fleetStarts(sink); got != 2 {
				t.Fatalf("after the app connected: %d starts, want 2", got)
			}
		})
	})

	t.Run("fleetMaxAge", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r, started := unanswered(t)
			sink := r.sink
			elapse(time.Until(started.Add(fleetMaxAge - time.Minute)))
			if got := fleetStarts(sink); got != 1 {
				t.Fatalf("before fleetMaxAge: %d starts, want 1", got)
			}
			r.restart()
			elapse(2 * time.Minute)
			if got := fleetStarts(sink); got != 2 {
				t.Fatalf("after fleetMaxAge: %d starts, want 2", got)
			}
			sink.mu.Lock()
			last := sink.got[len(sink.got)-1]
			sink.mu.Unlock()
			if last.event != "start" || last.state.NeedsYou != 1 {
				t.Fatalf("the start after the hold: %+v, want the request still waiting", last)
			}
		})
	})
}

// What came of a start decides its hold (#7641 review). Accepted, or no answer at all (APNs may have taken it): the
// next start waits for the phone or fleetMaxAge. Refused by APNs or the relay (any status but 200, 410 included): there
// is no card to hold for, so the next round starts again once the rolling cooldown is over, as before #7641.
func TestFleetStartOutcomeDecidesTheHold(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		err      error
		wantHold time.Duration
	}{
		{"accepted", http.StatusOK, `{"ok":true}`, nil, fleetMaxAge},
		{"no answer", 0, "", errors.New("connection reset after the request was written"), fleetMaxAge},
		{"relay refused", http.StatusUnprocessableEntity, `{"error":"apns 400","reason":"BadDeviceToken"}`, nil, fleetRefresh},
		{"APNs unavailable", http.StatusBadGateway, `{"error":"apns 503","reason":"ServiceUnavailable"}`, nil, fleetRefresh},
		{"relay rejects the payload", http.StatusBadRequest, `{"error":"bad aps key"}`, nil, fleetRefresh},
		{"token gone", http.StatusGone, `{"reason":"ExpiredToken"}`, nil, fleetRefresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, sink, stop := localFleetLoopWith(t, func(h *Hub) { h.push.retryWaits = nil })
				defer stop()
				store := h.store.(*fleetStartStore)
				sink.mu.Lock()
				sink.answer = func(call fleetPushCall) (int, string, error) {
					if call.event != "start" {
						return http.StatusOK, `{"ok":true}`, nil
					}
					return tc.status, tc.body, tc.err
				}
				sink.mu.Unlock()
				h.live.update("phone", func(v *db.LiveActivity) { v.ActivityID, v.PushToken = "", "" })
				reportLive(t, h, fleetWorking, FleetStats{})
				elapse(fleetCoalesce)
				if got := fleetStarts(sink); got != 1 {
					t.Fatalf("starts: %d, want 1", got)
				}
				store.mu.Lock()
				hold := store.due["phone"].Sub(store.claimed["phone"])
				store.mu.Unlock()
				if hold != tc.wantHold {
					t.Fatalf("next start held for %v after the start, want %v", hold, tc.wantHold)
				}
				// The round is the same past the cooldown: one start per round either way.
				elapse(fleetRefresh + time.Minute)
				if got := fleetStarts(sink); got != 1 {
					t.Fatalf("same round past the cooldown: %d starts, want 1", got)
				}
				// The agent ends; a request arrives: the next round, 10 minutes after the start's cooldown ran out.
				if tc.status == http.StatusGone { // the phone hands the hub its new push-to-start token
					h.live.update("phone", func(v *db.LiveActivity) { v.PushToStartToken = "phone-start-2" })
				}
				reportLive(t, h, FleetState{}, FleetStats{})
				elapse(fleetEndAfter + 30*time.Second)
				reportLive(t, h, fleetAsking, FleetStats{})
				elapse(fleetCoalesce)
				want := 1
				if tc.wantHold == fleetRefresh {
					want = 2
				}
				if got := fleetStarts(sink); got != want {
					t.Fatalf("next round: %d starts, want %d", got, want)
				}
			})
		})
	}
}

// sendFailure tells a start that never reached APNs (never sent, or refused with a status) from one nobody answered.
func TestSendFailureOutcome(t *testing.T) {
	refused := &relayRefusal{status: http.StatusUnprocessableEntity, msg: "apns 400", reason: "BadDeviceToken"}
	for _, tc := range []struct {
		name string
		err  error
		want pushOutcome
	}{
		{"registration changed", errRegistrationChanged, pushRefused},
		{"request not made", fmt.Errorf("%w: %v", errNotSent, errors.New("bad URL")), pushRefused},
		{"relay status", refused, pushRefused},
		{"relay status after retries", fmt.Errorf("%w (%d tries)", refused, 3), pushRefused},
		{"grant renewal failed", fmt.Errorf("%w; renewing the grant: %v", &relayRefusal{status: http.StatusUnauthorized}, errors.New("register: 500")), pushRefused},
		{"no answer", fmt.Errorf("%w (%d tries)", errors.New("i/o timeout"), 3), pushUnknown},
	} {
		if got := sendFailure(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
