package assistant

import (
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestSessionReviewAnswerFromRecoveredInventory(t *testing.T) {
	for _, usePostgres := range []bool{false, true} {
		for _, send := range []bool{true, false} {
			storeName := "memory"
			if usePostgres {
				storeName = "postgres"
			}
			action := "dismiss"
			if send {
				action = "send"
			}
			t.Run(storeName+"/"+action, func(t *testing.T) {
				var st Store = newFleetMem()
				if usePostgres {
					st = todoTestDB(t)
				}
				first := newSessionReviewFixtureOn(t, st)
				_, reviewer := first.start(t)
				finished := first.finish(t, reviewer, sessionReviewRawReport, sessionReviewDisplayReport)
				waitFor(t, "completed review cleanup", func() bool {
					list, err := first.ft.s.sessionReviewRecords(t.Context())
					e, ok := sessionReviewByID(list, finished.ID)
					return err == nil && ok && e.CleanedAt != nil
				})
				first.ft.cancel()

				// A reconnect can announce the durable inventory before its snapshot
				// waiter has run. Answer the card as soon as the new manager lists it.
				back := newSessionReviewFixtureOn(t, st)
				card := sessionReviewOneCard(t, back.ft.s)
				if card.ID != finished.ID || card.ReviewReport != sessionReviewDisplayReport {
					t.Fatalf("recovered a different review: %+v", card)
				}
				answer := proto.Resolution{Dismiss: true}
				wantOutcome, wantAttempts := "dismissed", 0
				if send {
					answer = sessionReviewSendResolution(t, card)
					wantOutcome, wantAttempts = "resolved", 1
				}
				if w := back.resolve(t, card, answer); w.Code < 200 || w.Code >= 300 {
					t.Fatalf("first %s of a listed review: HTTP %d, node sends %d: %s",
						action, w.Code, len(sessionReviewSendAttempts(back.ft.f)), w.Body)
				}
				if cards := sessionReviewCards(back.ft.s); len(cards) != 0 {
					t.Fatalf("answered review still listed: %+v", cards)
				}
				if got, _, err := back.ft.s.fleetStore().InteractionOutcome(t.Context(), card.ID); err != nil || got != wantOutcome {
					t.Fatalf("durable outcome = %q, %v; want %q", got, err, wantOutcome)
				}
				if w := back.resolve(t, card, answer); w.Code >= http.StatusInternalServerError {
					t.Fatalf("repeated %s: HTTP %d: %s", action, w.Code, w.Body)
				}
				calls := sessionReviewSendAttempts(back.ft.f)
				if len(calls) != wantAttempts {
					t.Fatalf("guarded node calls = %+v, want %d", calls, wantAttempts)
				}
				if send && (calls[0]["method"] != DeviceSessionSendOnce || calls[0]["client_msg_id"] != card.ID ||
					calls[0]["session_id"] != sessionReviewSource || calls[0]["text"] != sessionReviewDisplayReport) {
					t.Fatalf("wrong guarded delivery: %+v", calls)
				}
			})
		}
	}
}
