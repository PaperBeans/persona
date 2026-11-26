package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// approvalResultSends counts the messages f was given that carry approval id's terminal result.
func approvalResultSends(f *fakeHub, id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.sends {
		if text, _ := p["text"].(string); strings.Contains(text, "Approval "+id+" ") {
			n++
		}
	}
	return n
}

// approvalRetryManager is a manager on f, d and st with a run of its own, a 20 ms retry and its log lines kept, as
// the manager process builds one for each run.
func approvalRetryManager(t *testing.T, f *fakeHub, d *fleetDevs, st *fleetMem) (*fleetTest, context.CancelFunc, func(string) bool) {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	logf := testLogf(t)
	s := New(Options{Hub: f, Store: func() Store { return st }, Devices: d, Logf: func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
		logf(format, args...)
	}})
	s.approvals.retryDelay = 20 * time.Millisecond
	run, end := context.WithCancel(context.Background())
	s.running.Store(&run)
	t.Cleanup(end)
	logged := func(part string) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, part) })
	}
	return &fleetTest{t: t, s: s, d: d, f: f, st: st}, end, logged
}

// A retry belongs to the run that armed it (#16045). Within the run it delivers once the conversation is back; after
// the run ended it does nothing, and the next manager delivers the result from the durable journal, once.
func TestApprovalResultRetryEndsWithItsRun(t *testing.T) {
	t.Run("within the run the retry delivers", func(t *testing.T) {
		d := newFleetDevs()
		d.add("mac", "Mac")
		a, _, _ := approvalRetryManager(t, newFakeHub(), d, newFleetMem())
		id := "ix_retry_live_run"
		putApprovalJournal(t, a, approvalJournalEntry{ID: id, MachineID: "mac", SessionID: "s1",
			Result: "Approval " + id + " is denied and is no longer pending.", UpdatedAt: time.Now().UTC()})
		a.s.deliverApprovalResults() // s1 is not listed: a transient failure arms the retry
		preparedTestConversation(a)
		waitApprovalResult(t, a, id)
		waitFor(t, "the delivered result to leave the journal", func() bool { return len(storedApprovalJournal(t, a)) == 0 })
	})

	t.Run("after the run the next manager delivers it once", func(t *testing.T) {
		st := newFleetMem()
		dA := newFleetDevs()
		dA.add("mac", "Mac")
		a, endA, loggedA := approvalRetryManager(t, newFakeHub(), dA, st)
		id := "ix_retry_run_ended"
		putApprovalJournal(t, a, approvalJournalEntry{ID: id, MachineID: "mac", SessionID: "s1",
			Result: "Approval " + id + " is denied and is no longer pending.", UpdatedAt: time.Now().UTC()})
		a.s.deliverApprovalResults() // s1 is not listed: a transient failure arms the retry
		endA()
		// The conversation is listed again, on the stopped manager's hub too: a retry that outlived its run would
		// deliver through it, beside the next manager.
		preparedTestConversation(a)
		waitFor(t, "the stopped manager's retry to end with its run", func() bool { return loggedA("retry skipped, the manager's run ended") })
		if got := approvalResultSends(a.f, id); got != 0 {
			t.Fatalf("the stopped manager delivered %d results after its run ended", got)
		}
		a.s.approvals.mu.Lock()
		rearmed := a.s.approvals.retry
		a.s.approvals.mu.Unlock()
		if rearmed {
			t.Fatal("the stopped manager armed another retry")
		}
		if got := storedApprovalJournal(t, a); len(got) != 1 || got[0].ID != id || got[0].Result == "" {
			t.Fatalf("the undelivered result left the durable journal: %+v", got)
		}

		dB := newFleetDevs()
		dB.add("mac", "Mac")
		b, _, _ := approvalRetryManager(t, newFakeHub(), dB, st)
		preparedTestConversation(b)
		b.s.recoverApprovalJournals(context.Background()) // what the next manager's Run does first
		waitApprovalResult(t, b, id)                      // fails on a second delivery
		waitFor(t, "the delivered result to leave the journal", func() bool { return len(storedApprovalJournal(t, b)) == 0 })
		if got := approvalResultSends(b.f, id); got != 1 {
			t.Fatalf("the next manager delivered %d results, want 1", got)
		}
		if got := approvalResultSends(a.f, id); got != 0 {
			t.Fatalf("the stopped manager delivered %d results", got)
		}
	})
}
