package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// Owner-only timed cards (secret_run, secret_handoff, and any card carrying secret fields) stay
// pending in "Needs you" until the user opens/views them in an app; only then does the short
// countdown begin, and repeated seen receipts are idempotent.
func TestOwnerOnlyCardStaysPendingUntilSeenThenExpires(t *testing.T) {
	oldRun := secretRunCardWait
	oldHandoff := handoffCardWait
	oldDefault := ownerCardDefaultSeenWait
	oldMax := ownerCardMaxWait
	secretRunCardWait = 180 * time.Millisecond
	handoffCardWait = 180 * time.Millisecond
	ownerCardDefaultSeenWait = 180 * time.Millisecond
	ownerCardMaxWait = 2 * time.Second
	t.Cleanup(func() {
		secretRunCardWait = oldRun
		handoffCardWait = oldHandoff
		ownerCardDefaultSeenWait = oldDefault
		ownerCardMaxWait = oldMax
	})

	cases := []struct {
		name string
		ix   proto.Interaction
		how  string // "route" | "event" | "session"
	}{
		{
			name: "secret_run via HTTP seen route",
			ix: proto.Interaction{
				ID: "ix_sr_seen", MachineID: "mac", SessionID: "s1",
				Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
				Fields: []proto.Field{{Key: "password", Label: "Password", Secret: true}},
			},
			how: "route",
		},
		{
			name: "secret_handoff via fleet.interaction_seen event",
			ix: proto.Interaction{
				ID: "ix_sh_seen", MachineID: "mac", SessionID: "s2",
				Kind: proto.IxPermission, ToolName: proto.SecretHandoffTool,
			},
			how: "event",
		},
		{
			name: "secret field card via session seen",
			ix: proto.Interaction{
				ID: "ix_sf_seen", MachineID: "mac", SessionID: "s3",
				Kind: proto.IxPermission, ToolName: "custom_secret_form",
				Fields: []proto.Field{{Key: "token", Label: "Token", Secret: true}},
			},
			how: "session",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFleetTestNoStore(t)
			done := make(chan proto.Resolution, 1)
			go func() {
				r, err := ft.s.waitInteraction(context.Background(), tc.ix)
				if err != nil {
					t.Errorf("waitInteraction: %v", err)
				}
				done <- r
			}()
			pending := ft.waitPending(1)[0]
			if pending.SeenAt != nil {
				t.Fatalf("unseen card already has SeenAt = %v", pending.SeenAt)
			}
			if got := pending.ExpiresAt.Sub(pending.CreatedAt); got != ownerCardMaxWait {
				t.Fatalf("unseen card ExpiresAt-CreatedAt = %v, want max cap %v", got, ownerCardMaxWait)
			}

			// Waiting past the short post-view TTL must NOT expire an unseen card.
			select {
			case r := <-done:
				t.Fatalf("unseen card expired early: %+v", r)
			case <-time.After(260 * time.Millisecond):
			}
			if _, ok := ft.s.pendingIx(tc.ix.ID); !ok {
				t.Fatal("unseen card vanished from pending interactions")
			}

			// Trigger the seen receipt.
			switch tc.how {
			case "route":
				req := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+tc.ix.ID+"/seen", nil)
				rec := httptest.NewRecorder()
				ft.s.fleetHandler(ft.s).ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("POST /seen = %d %s", rec.Code, rec.Body)
				}
			case "event":
				raw, _ := json.Marshal(assistantwire.FleetInteractionSeen{InteractionID: tc.ix.ID})
				if !ft.s.FleetEvent(context.Background(), assistantwire.EventInteractionSeen, raw) {
					t.Fatal("FleetEvent(EventInteractionSeen) not handled")
				}
			case "session":
				ft.s.markSeen(context.Background(), tc.ix.MachineID, tc.ix.SessionID)
			}

			afterSeen, ok := ft.s.pendingIx(tc.ix.ID)
			if !ok || afterSeen.SeenAt == nil {
				t.Fatalf("card after seen = %+v (ok=%v), want SeenAt set", afterSeen, ok)
			}
			firstSeenAt := *afterSeen.SeenAt
			firstExpires := afterSeen.ExpiresAt

			// Repeated seen receipts must be idempotent and never extend the countdown.
			<-time.After(30 * time.Millisecond)
			ft.s.markInteractionSeen(context.Background(), tc.ix.ID)
			afterSecond, ok := ft.s.pendingIx(tc.ix.ID)
			if ok && (!afterSecond.SeenAt.Equal(firstSeenAt) || !afterSecond.ExpiresAt.Equal(firstExpires)) {
				t.Fatalf("second seen moved deadline: %v/%v -> %v/%v", firstSeenAt, firstExpires, afterSecond.SeenAt, afterSecond.ExpiresAt)
			}

			// Now it must expire shortly after the post-view TTL.
			select {
			case r := <-done:
				if !r.Dismiss || r.Outcome() != proto.OutcomeExpired {
					t.Fatalf("resolution = %+v, want expired dismiss", r)
				}
			case <-time.After(time.Second):
				t.Fatal("seen card did not expire after post-view countdown")
			}
		})
	}
}

// Even if never viewed, an owner-only timed card expires once it hits ownerCardMaxWait so it
// cannot linger forever.
func TestOwnerOnlyCardExpiresAtMaxWaitWhenNeverSeen(t *testing.T) {
	oldRun := secretRunCardWait
	oldMax := ownerCardMaxWait
	secretRunCardWait = 10 * time.Minute
	ownerCardMaxWait = 150 * time.Millisecond
	t.Cleanup(func() {
		secretRunCardWait = oldRun
		ownerCardMaxWait = oldMax
	})

	ft := newFleetTestNoStore(t)
	ix := proto.Interaction{
		ID: "ix_cap", MachineID: "mac", SessionID: "s1",
		Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
		Fields: []proto.Field{{Key: "password", Label: "Password", Secret: true}},
	}
	done := make(chan proto.Resolution, 1)
	go func() {
		r, err := ft.s.waitInteraction(context.Background(), ix)
		if err != nil {
			t.Errorf("waitInteraction: %v", err)
		}
		done <- r
	}()
	select {
	case r := <-done:
		if !r.Dismiss || r.Outcome() != proto.OutcomeExpired {
			t.Fatalf("resolution = %+v, want expired dismiss at max cap", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unseen card did not expire at ownerCardMaxWait cap")
	}
}
