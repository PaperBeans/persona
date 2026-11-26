package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A scheduled task's own conversation is unattended between rounds. Its
// detachable card must not turn a finished turn into a day-long pending card.
func TestScheduledDetachedApprovalExpiresAtInitialWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shortDetachedTimers(t)
		ft := newFleetTest(t)
		preparedTestConversation(ft)
		ft.s.originSet(context.Background(), "mac", "s1", OriginTask, "task_scheduled", "")
		done := startDetachedTest(t, ft, detachedTestCard("ix_scheduled_initial_expiry"))
		elapse(30 * time.Millisecond)

		first := awaitDetachedTest(t, done)
		if first.err != nil || first.res.Pending || first.res.Allow != nil || !first.res.Dismiss {
			t.Fatalf("scheduled initial result = %+v, %v; want expiry without approval or a retained turn", first.res, first.err)
		}
		ft.waitPending(0)
		status, _, err := ft.st.InteractionOutcome(context.Background(), "ix_scheduled_initial_expiry")
		if err != nil || status != "expired" {
			t.Fatalf("scheduled interaction status = %q, %v; want expired", status, err)
		}
		if got := ft.st.endedAs("ix_scheduled_initial_expiry"); got != "expired" {
			t.Fatalf("scheduled interaction ended_by = %q, want expired", got)
		}
	})
}

// The schedule book is an independent authoritative marker while a task's
// current conversation is adopted; the origin marker covers the earlier window.
func TestScheduleBookDetachedApprovalExpiresAtInitialWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shortDetachedTimers(t)
		ft := newFleetTest(t)
		preparedTestConversation(ft)
		sc := ft.s.loadSchedule(context.Background())
		sc.mu.Lock()
		sc.tasks = append(sc.tasks, Task{ID: "task_scheduled", Title: "rounds", Prompt: "p", Repeat: repeatInterval,
			EveryMinutes: 30, Enabled: true, RunMachineID: "mac", RunSessionID: "s1"})
		sc.mu.Unlock()
		done := startDetachedTest(t, ft, detachedTestCard("ix_schedule_book_initial_expiry"))
		elapse(30 * time.Millisecond)

		first := awaitDetachedTest(t, done)
		if first.err != nil || first.res.Pending || first.res.Allow != nil || !first.res.Dismiss {
			t.Fatalf("schedule-book initial result = %+v, %v; want expiry without approval or a retained turn", first.res, first.err)
		}
		ft.waitPending(0)
	})
}

// Interactive cards keep their waiting semantics, and every durable reader sees
// the same extended deadline as the in-memory card and connected clients.
func TestInteractiveDetachedApprovalPersistsWaitingDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shortDetachedTimers(t)
		ft := newFleetTest(t)
		preparedTestConversation(ft)
		done := startDetachedTest(t, ft, detachedTestCard("ix_interactive_waiting_deadline"))
		elapse(30 * time.Millisecond)
		first := awaitDetachedTest(t, done)
		if first.err != nil || !first.res.Pending || !strings.Contains(first.res.Message, "The command was not executed") {
			t.Fatalf("interactive initial result = %+v, %v; want non-final waiting", first.res, first.err)
		}
		pending := ft.s.pendingInteractions()
		if len(pending) != 1 {
			t.Fatalf("interactive pending cards = %+v, want one", pending)
		}
		records, err := ft.st.Interactions(context.Background(), "mac", "s1", 10)
		if err != nil || len(records) != 1 {
			t.Fatalf("stored interactions = %+v, %v; want the card", records, err)
		}
		if got, want := records[0].Interaction.ExpiresAt, pending[0].ExpiresAt; !got.Equal(want) {
			t.Fatalf("stored expires_at = %v, effective expires_at = %v; want the same waiting deadline", got, want)
		}
		allow := true
		if err := ft.s.resolve("ix_interactive_waiting_deadline", proto.Resolution{Allow: &allow}); err != nil {
			t.Fatal(err)
		}
		waitApprovalResult(t, ft, "ix_interactive_waiting_deadline")
	})
}

// A card cannot advertise a deadline that its durable record rejected. Failure
// remains an unanswered expiry, never an Allow and never a detached grant.
func TestInteractiveDetachedApprovalExpiresWhenWaitingDeadlineCannotPersist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shortDetachedTimers(t)
		ft := newFleetTest(t)
		preparedTestConversation(ft)
		ft.st.deadlineErr = errors.New("synthetic deadline write failure")
		done := startDetachedTest(t, ft, detachedTestCard("ix_waiting_deadline_write_failure"))
		elapse(30 * time.Millisecond)

		first := awaitDetachedTest(t, done)
		if first.err != nil || first.res.Pending || first.res.Allow != nil || !first.res.Dismiss {
			t.Fatalf("deadline-write-failure result = %+v, %v; want fail-closed expiry", first.res, first.err)
		}
		ft.waitPending(0)
		status, _, err := ft.st.InteractionOutcome(context.Background(), "ix_waiting_deadline_write_failure")
		if err != nil || status != "expired" {
			t.Fatalf("deadline-write-failure status = %q, %v; want expired", status, err)
		}
	})
}
