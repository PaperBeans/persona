package assistant

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Parked tasks (#3804, docs/assistant/todos.md "Parked tasks"). A supervised task that only waits — for the owner to say
// what comes first, for someone else, for something outside to happen, for a date — is no decision, so needs_user is
// refused for it. Its supervisor parks it instead (task_review parked): one sentence of what it waits for, what kind
// of thing that is, and until when. A parked task stays open and is nobody's to answer: no ask, no push, no conclusion
// card, and none of the signals that say work stopped (stalled, idle, orphaned, overdue, undecidable). At its time its
// supervisor gets one parked_due signal: decide, park again with a new reason, or close it. Its agent starting a turn
// (someone sent it something) ends the parking, and it is supervised as before. Parking again and again is the old
// habit under a new name, so the parks in a row with no agent turn between them are counted, and one past maxReparks
// re-parks is refused. Only waiting_on owner_priority concerns the owner: the daily digest lists it, never Needs you.

const (
	reviewParked = "parked"
	// sigParkedDue: a parked task's time came (parkedDueSignal).
	sigParkedDue = "parked_due"
)

// What a parked task waits on (task_review waiting_on).
const (
	parkOwnerPriority = "owner_priority" // the owner says whether it is done at all, or what comes first
	parkSomeoneElse   = "someone_else"   // another person has to do or answer something
	parkExternalEvent = "external_event" // something outside has to happen (a release, a result, a fix upstream)
	parkDate          = "date"           // nothing but the time
)

var parkWaitingOn = []string{parkOwnerPriority, parkSomeoneElse, parkExternalEvent, parkDate}

var (
	// parkDefault: how long a task is parked when the review gives no time; parkMin and parkMax bound the time it gives.
	parkDefault = 24 * time.Hour
	parkMin     = time.Minute
	parkMax     = 30 * 24 * time.Hour
)

// maxReparks is how many times in a row a task is parked again with no agent turn in between; one more is refused.
const maxReparks = 3

// Parked is what a parked task waits for, as the apps get it on the to-do (supervision.parked): the reason in one
// sentence, until when, what kind of thing it waits on, and how many times in a row it was parked with no agent turn
// in between (1: parked once).
type Parked struct {
	Reason    string    `json:"reason"`
	Until     time.Time `json:"until"`
	WaitingOn string    `json:"waiting_on"`
	Count     int       `json:"count"`
}

// parking is what holds v parked now: its latest review, when the ledger's state is parked and that review parked it.
// nil when it is not parked.
func (v *Supervision) parking() *TaskReview {
	if v == nil || v.State != reviewParked || len(v.Reviews) == 0 {
		return nil
	}
	if r := &v.Reviews[len(v.Reviews)-1]; r.Verdict == reviewParked {
		return r
	}
	return nil
}

// parked is v's parking as the apps get it; nil when it is not parked.
func (v *Supervision) parked() *Parked {
	r := v.parking()
	if r == nil {
		return nil
	}
	return &Parked{Reason: r.Reason, Until: r.Until, WaitingOn: r.WaitingOn, Count: max(1, r.Parks)}
}

// parseParkUntil reads a park's time: a duration ("3d", "36h", "2d12h", "90m") or an RFC 3339 time; "" is parkDefault
// from now. It must be parkMin to parkMax ahead.
func parseParkUntil(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now.Add(parkDefault).UTC(), nil
	}
	var at time.Time
	if d, ok := parkDuration(raw); ok {
		at = now.Add(d)
	} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
		at = t
	} else {
		return time.Time{}, fmt.Errorf("until %q: give a duration such as 3d, 36h or 90m, or an RFC 3339 time", raw)
	}
	if d := at.Sub(now); d < parkMin || d > parkMax {
		return time.Time{}, fmt.Errorf("until %q: must be between 1 minute and 30 days from now", raw)
	}
	return at.UTC(), nil
}

// parkDuration reads a duration that may count days ("3d", "2d12h"), which time.ParseDuration does not.
func parkDuration(raw string) (time.Duration, bool) {
	days, rest, hasDays := strings.Cut(raw, "d")
	if !hasDays {
		d, err := time.ParseDuration(raw)
		return d, err == nil
	}
	n, err := strconv.Atoi(days)
	if err != nil || n < 0 || n > 365 {
		return 0, false
	}
	d := time.Duration(n) * 24 * time.Hour
	if rest != "" {
		more, err := time.ParseDuration(rest)
		if err != nil || more < 0 {
			return 0, false
		}
		d += more
	}
	return d, true
}

// parkElse is what a refused park is told to do instead.
const parkElse = "A task that waits for nothing is not parked: decide it yourself and send the agent on (agent_send), send " +
	"the work back (rework), or close it (accepted or cancelled); a decision only the user can make is needs_user."

// readPark checks a parked review: what it waits for in one sentence, what kind of thing that is, and until when. The
// error names everything missing at once.
func readPark(reason, until, waitingOn string, now time.Time) (Parked, error) {
	p := Parked{Reason: clip(oneLine(reason), 200), WaitingOn: strings.TrimSpace(waitingOn)}
	var missing []string
	if p.Reason == "" {
		missing = append(missing, "reason (one sentence: what the task waits for)")
	}
	if !slices.Contains(parkWaitingOn, p.WaitingOn) {
		missing = append(missing, "waiting_on (what kind of thing it waits on: "+strings.Join(parkWaitingOn, ", ")+")")
	}
	at, err := parseParkUntil(until, now)
	if err != nil {
		missing = append(missing, err.Error())
	}
	if len(missing) > 0 {
		return Parked{}, fmt.Errorf("parked is refused: it does not say what the task waits for. Missing: %s. %s",
			strings.Join(missing, "; "), parkElse)
	}
	p.Until = at
	return p, nil
}

// reparkError refuses parking task id again after it was parked parks times in a row with no agent turn in between.
func reparkError(id string, parks int) error {
	return fmt.Errorf("task %s was parked %d times in a row (re-parked %d times) and its agent did not work in between: a "+
		"task nobody moves is not waiting, it is stuck. It is not parked again. Close it (cancelled, or accepted when what "+
		"was done is enough), or state the real decision for the user: needs_user with why_owner, reason, recommend, options "+
		"and cost_of_waiting", id, parks, parks-1)
}

// parkedAnswer is what task_review answers a park of task id.
func parkedAnswer(id string, p Parked, loc *time.Location) string {
	owner := ""
	if p.WaitingOn == parkOwnerPriority {
		owner = " The user's daily digest lists it as parked, waiting for their priority call, with your reason; it is not in their Needs you."
	}
	left := 1 + maxReparks - p.Count
	again := fmt.Sprintf("It can be parked again %d more time(s) with no agent turn in between.", left)
	if left <= 0 {
		again = "It cannot be parked again unless its agent works in between: next time, decide or close it."
	}
	return fmt.Sprintf("Task %s is parked until %s (waiting_on %s; parked %d time(s) in a row). It stays open; the user is not asked, and "+
		"no stalled, idle, orphaned or overdue signal is raised for it meanwhile.%s At that time a 督办 message (parked_due) brings it "+
		"back to you: decide, park it again with a new reason, or close it. Its agent starting a turn ends the parking (anything sent "+
		"to it does that, so tell it what it needs before you park, not after). %s", id, p.Until.In(loc).Format(toolTimeLayout), p.WaitingOn,
		p.Count, owner, again)
}

// parkedDueSignal: supervised open task t is parked and its time came, and no supervision message carried that yet
// (kept on the review once one did: parkedDueTold): the signal that hands it back. Its key names the park, so the
// watch raising it again every tick tells it once.
func parkedDueSignal(t Todo, now time.Time) (superviseSignal, bool) {
	v := t.Supervision
	if v == nil || t.DoneAt != nil {
		return superviseSignal{}, false
	}
	r := v.parking()
	if r == nil || !r.DueAt.IsZero() || now.Before(r.Until) {
		return superviseSignal{}, false
	}
	return superviseSignal{Todo: t.ID, Kind: sigParkedDue, Key: parkKey(r), Detail: r.Reason,
		Minutes: int(now.Sub(r.At).Minutes())}, true
}

// parkKey names one park: when the review parked it.
func parkKey(r *TaskReview) string { return "parked@" + r.At.UTC().Format(time.RFC3339Nano) }

// parkedDueTold keeps on each park in list that a supervision message carried its parked_due (sent, or queued where a
// restart keeps it), so a manager started again does not tell it a second time; until then the signal is raised again
// from the ledger, so a restart before the message went loses nothing.
func (s *Service) parkedDueTold(ctx context.Context, list []superviseSignal) {
	for _, g := range list {
		if g.Kind != sigParkedDue {
			continue
		}
		if _, _, err := s.updateSupervision(ctx, g.Todo, func(v *Supervision) bool {
			r := v.parking()
			if r == nil || parkKey(r) != g.Key || !r.DueAt.IsZero() {
				return false
			}
			r.DueAt = time.Now().UTC()
			return true
		}); err != nil {
			s.logf("task %s: parked due: %v", g.Todo, err)
		}
	}
}

// parkedSession: the open task this session works for is parked: the manager sends its agent nothing on its own
// (workGuard), since any turn of its agent ends the parking.
func (s *Service) parkedSession(machine, session string) bool {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	t := s.todos.todos[s.todos.activeBySession[[2]string{machine, session}]]
	return t != nil && t.DoneAt == nil && t.Supervision.parking() != nil
}

// parkedEntry is park r as a parked_due signal's entry carries it.
func (s *Service) parkedEntry(ctx context.Context, r *TaskReview) *prompts.SuperviseParked {
	return &prompts.SuperviseParked{Reason: r.Reason, WaitingOn: r.WaitingOn, Count: max(1, r.Parks),
		Until: r.Until.In(s.location(ctx)).Format(toolTimeLayout), Left: max(0, 1+maxReparks-max(1, r.Parks))}
}

// parkedHealth is a parked task's supervision in the snapshot's words: until when and why.
func parkedHealth(p *Parked, loc *time.Location) string {
	on := map[string]string{parkOwnerPriority: "等用户定优先级", parkSomeoneElse: "等别人", parkExternalEvent: "等外部的事", // i18n-ignore: the snapshot is the assistant's prompt
		parkDate: "等到时间"}[p.WaitingOn] // i18n-ignore: the snapshot is the assistant's prompt
	return fmt.Sprintf("已搁置到 %s（%s）：%s", p.Until.In(loc).Format("01-02 15:04"), on, p.Reason) // i18n-ignore: the snapshot is the assistant's prompt
}
