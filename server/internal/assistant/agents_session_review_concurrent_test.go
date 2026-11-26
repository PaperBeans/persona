package assistant

import (
	"errors"
	"testing"
	"time"
)

func TestSessionReviewTerminalAnswerRequiresMatchingDurableOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, requested, winning, delivery string
		accepted                           bool
	}{
		{"same dismiss", "dismiss", "dismiss", "", true},
		{"same sent report", sessionReviewAnswerSend, sessionReviewAnswerSend, sessionReviewDeliverySent, true},
		{"opposite decision", "dismiss", sessionReviewAnswerSend, sessionReviewDeliverySent, false},
		{"send without delivery receipt", sessionReviewAnswerSend, sessionReviewAnswerSend, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, st := t.Context(), todoTestDB(t)
			first, replay := newSessionReviewFixtureOn(t, st), newSessionReviewFixtureOn(t, st)
			e := sessionReview{ID: sessionReviewPrefix + "competing", MachineID: sessionReviewMachine, SessionID: sessionReviewSource,
				State: sessionReviewPending, StartedAt: time.Now().UTC(), Delivery: tc.delivery}
			if err := first.ft.s.changeSessionReviews(ctx, func(doc *sessionReviewDocument) error {
				doc.Reviews = append(doc.Reviews, e)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// The request and the other manager's replay saw the same pending revision.
			if seen, _, err := replay.ft.s.sessionReviewForAnswer(ctx, e.ID); err != nil || seen.State != sessionReviewPending {
				t.Fatalf("original inventory = %+v, %v", seen, err)
			}
			if err := first.ft.s.finishSessionReviewAnswer(ctx, e, tc.winning); err != nil {
				t.Fatal(err)
			}
			err := replay.ft.s.finishSessionReviewAnswer(ctx, e, tc.requested)
			if tc.accepted && err != nil || !tc.accepted && !errors.Is(err, errSessionReviewConflict) {
				t.Fatalf("concurrent outcome = %v, want accepted=%t", err, tc.accepted)
			}
			var doc sessionReviewDocument
			if _, err := st.Setting(ctx, sessionReviewSetting, &doc); err != nil {
				t.Fatal(err)
			}
			winner, ok := sessionReviewByID(doc.Reviews, e.ID)
			want := sessionReviewDismissed
			if tc.winning == sessionReviewAnswerSend {
				want = sessionReviewSent
			}
			if !ok || doc.Revision != 2 || winner.State != want || winner.ResolvedAt == nil || winner.Delivery != tc.delivery {
				t.Fatalf("winning revision was overwritten: %+v", doc)
			}
			if calls := sessionReviewSendAttempts(replay.ft.f); len(calls) != 0 {
				t.Fatalf("acknowledging the winning revision sent to the node again: %+v", calls)
			}
		})
	}
}
