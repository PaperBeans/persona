package writers

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// recapWaiting: a try ran out of time and the next is not due yet.
func recapWaiting(t *RecapTarget, now time.Time) bool { return now.Before(t.RetryAt) }

// armRecap makes a recap of r due for reason, as r is now; one still due is settled by the same recap, so it gives
// way (the newer reason says what kind of recap it is: a final one's is complete or cutoff).
func armRecap(r *Recording, reason string, now time.Time) {
	r.RecapTargets++
	r.RecapDue = &RecapTarget{N: r.RecapTargets, Reason: reason, Base: r.Base, Revision: r.Revision, To: coverageTo(r),
		At: now.UTC()}
	planRecap(r)
}

// askDayRecap is AskMinutes for day recording r (as read under recMu, which the caller holds): a recap from what the
// hub has now, never a push; ErrRecordingIncomplete while it has no text. Asking again is trying again: the chunks the
// model gave up on, or that wait after running out of time, are noted afresh (retryRecapNotes).
func (e *engine) askDayRecap(r *Recording) error {
	if r.Chars == 0 {
		return ErrRecordingIncomplete
	}
	if err := e.Store.retryRecapNotes(r.ID); err != nil {
		return err
	}
	r.RecapAsks++
	armRecap(r, RecapDueAsked, e.now())
	return e.Store.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// putRecap writes recap.md of recording id, summary.md from its summary (none while it is blank), then recap.json,
// unless it was deleted meanwhile (false then). recap.json goes last, so a write that failed half way leaves the
// recap.json before it, and the next write (a PUT tried again, mendRecapFiles) writes them all again: what an erase
// withdrew never stays in recap.md or summary.md behind a recap.json without it. The caller holds recMu.
func (s *store) putRecap(id string, rc *DayRecap) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return false, err
	}
	wrote, err := s.recapFilesLocked(id, func() error {
		if err := s.writeFile(s.path(KindRecording, id, recapMarkdown), []byte(strings.TrimSpace(rc.Markdown)+"\n")); err != nil {
			return err
		}
		if rc.Summary != "" {
			return nil
		}
		// summary.md is the recap's, unless the user wrote it (putSummary leaves a locked one alone too).
		if locked, err := s.summaryLocked(id); err != nil || locked {
			return err
		}
		if err := os.Remove(s.path(KindRecording, id, "summary.md")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	if err != nil || !wrote {
		return wrote, err
	}
	if rc.Summary != "" {
		if err := s.putSummary(KindRecording, id, rc.Summary, false); err != nil {
			return true, err
		}
	}
	if wrote, err = s.recapFilesLocked(id, func() error {
		return s.writeFile(s.path(KindRecording, id, recapFile), append(raw, '\n'))
	}); err != nil || !wrote {
		return wrote, err
	}
	s.notify(KindRecording, id, "summary")
	return true, nil
}

// recapFilesLocked runs write on recording id's files under recFilesMu and metaMu, unless the recording was deleted
// meanwhile (false then: writeFile makes missing directories, and a recording deleted must stay gone).
func (s *store) recapFilesLocked(id string, write func() error) (bool, error) {
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if err := s.recoverRecordingLocked(id); err != nil {
		return false, err
	}
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	if _, err := os.Stat(s.path(KindRecording, id, "meta.json")); err != nil {
		return false, nil
	}
	return true, write()
}

// summaryLocked: the user wrote recording id's summary (its meta.json locks it). The caller holds metaMu.
func (s *store) summaryLocked(id string) (bool, error) {
	var t Tagging
	if _, err := readJSON(s.entityPath(KindRecording, id), &t); err != nil {
		return false, err
	}
	return taggingFieldLocked(&t, "summary"), nil
}

// mendRecapFiles writes recording r's recap.md and summary.md again from its recap.json when they do not say what it
// does (a write that failed half way, putRecap). A trimmed recording keeps no summary.md, and one the user wrote is
// theirs. The caller holds recMu.
func (s *store) mendRecapFiles(r *Recording) error {
	if s.readOnly {
		return nil
	}
	rc, err := s.readRecap(r.ID)
	if err != nil {
		return nil // none (one that cannot be read shows nothing either)
	}
	md, _ := os.ReadFile(s.path(KindRecording, r.ID, recapMarkdown))
	mdOK := string(md) == strings.TrimSpace(rc.Markdown)+"\n"
	sumOK := r.Trimmed()
	if !sumOK {
		sum, err := os.ReadFile(s.path(KindRecording, r.ID, "summary.md"))
		sumOK = rc.Summary == "" && os.IsNotExist(err) || rc.Summary != "" && err == nil && string(sum) == strings.TrimSpace(rc.Summary)+"\n"
	}
	if !sumOK {
		s.metaMu.Lock()
		sumOK, err = s.summaryLocked(r.ID)
		s.metaMu.Unlock()
		if err != nil {
			return err
		}
	}
	switch {
	case mdOK && sumOK:
		return nil
	case sumOK:
		_, err := s.recapFilesLocked(r.ID, func() error {
			return s.writeFile(s.path(KindRecording, r.ID, recapMarkdown), []byte(strings.TrimSpace(rc.Markdown)+"\n"))
		})
		return err
	}
	_, err = s.putRecap(r.ID, rc)
	return err
}

// repairDayRecap is what a PUT of day recording r (as stored before it, read under recMu, which the caller holds)
// with upload u (cleaned) that sets a new base does to its recap, before the upload is committed (docs/assistant/
// day-recap.md "When a recap is due"): from a recap made before it goes at once every entry overlapping an erased gap
// of u's coverage (its title may quote the erased words), every point and action whose time no longer starts a line
// with text in u, the entries left with no point, and the summary. An action made a to-do goes too: the to-do stays,
// and r.RecapTodos gives it back to the same action in a later version (keepRecapTodos). A recap withdrawn already (a
// PUT tried again after one that failed) has recap.md and summary.md written again from it when they still differ.
// The notes of the chunks whose lines the PUT changed go with them (pruneRecapNotes). recapped: r has a recap
// (readable or not).
func (e *engine) repairDayRecap(r *Recording, u *RecordingUpload) (recapped bool, err error) {
	st := e.Store
	rc, err := st.readRecap(r.ID)
	if err != nil {
		// None, or one that cannot be read (nothing of it is shown either): only a recap written before counts.
		return r.RecapVersion > 0, nil
	}
	if withdrawRepaired(rc, u) {
		// Another version: the actions changed, and a confirmation naming the one before must not land on them.
		rc.Version = max(rc.Version, r.RecapVersion) + 1
		zone := st.Zone()
		if u.TZ != "" {
			if l, err := dayLocation(u.TZ); err == nil {
				zone = l
			}
		}
		rc.Markdown = renderDayRecap(rc, u.StartedAt, zone)
		if _, err := st.putRecap(r.ID, rc); err != nil {
			return true, err
		}
		r.RecapVersion = rc.Version
		return true, nil
	}
	return true, st.mendRecapFiles(r)
}

// rebaseRecap is what a PUT that set a new base does to r's recap targets: one still due moves to the new base with
// its reason and tries (it reads what the hub holds when it runs); else, when r has a recap (recapped) or a target the
// model failed at, a repair is due at once.
func rebaseRecap(r *Recording, recapped bool, now time.Time) {
	switch t := r.RecapDue; {
	case t != nil && !recapFailed(t):
		t.Base = r.Base
	case recapped || t != nil:
		armRecap(r, RecapDueRepair, now)
	}
}

// withdrawRepaired takes out of rc what upload u no longer says (see repairDayRecap) and gives it u's gaps; false when
// nothing changed.
func withdrawRepaired(rc *DayRecap, u *RecordingUpload) bool {
	starts := map[float64]bool{}
	for _, sg := range u.Segments { // cleaned: only those with text
		starts[sg.Start] = true
	}
	var erased, gaps []CoverageGap
	if c := u.Coverage; c != nil {
		gaps = gapsWithin(c.Gaps, rc.Covers.From, rc.Covers.To)
		for _, g := range c.Gaps {
			if g.Reason == GapErased {
				erased = append(erased, g)
			}
		}
	}
	overlaps := func(from, to float64) bool {
		return slices.ContainsFunc(erased, func(g CoverageGap) bool { return g.From <= to && g.To >= from })
	}
	// gone: at no longer starts a line with text, or (the phone's own log disagreeing) lies in an erased gap.
	gone := func(at float64) bool { return !starts[at] || overlaps(at, at) }
	changed := rc.Summary != ""
	rc.Summary = "" // it may quote anything the PUT took out
	timeline := rc.Timeline[:0]
	for _, en := range rc.Timeline {
		if overlaps(en.From, en.To) {
			changed = true
			continue
		}
		n := len(en.Points)
		en.Points = slices.DeleteFunc(en.Points, func(p MinutesPoint) bool { return gone(p.At) })
		changed = changed || len(en.Points) != n
		if len(en.Points) > 0 {
			timeline = append(timeline, en)
		}
	}
	rc.Timeline = timeline
	n := len(rc.Actions)
	rc.Actions = slices.DeleteFunc(rc.Actions, func(a MinutesAction) bool { return gone(a.At) })
	changed = changed || len(rc.Actions) != n
	if !slices.Equal(rc.Gaps, gaps) {
		rc.Gaps, changed = gaps, true
	}
	return changed
}

// gapsWithin is the part of gaps inside from–to, each cut to it.
func gapsWithin(gaps []CoverageGap, from, to float64) []CoverageGap {
	var out []CoverageGap
	for _, g := range gaps {
		if g.To <= from || g.From >= to {
			continue
		}
		g.From, g.To = max(g.From, from), min(g.To, to)
		out = append(out, g)
	}
	return out
}

// recapTodoNear is how far apart in time two actions with the same who and what may be and still be one task: one
// said again later in the day is another.
const recapTodoNear = 15 * 60

// recapTaskKey is what makes a recap's action the same task in any version: who and what, trimmed and case-folded,
// hashed (meta.json keeps it, and is searched).
func recapTaskKey(who, what string) string {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	h := sha256.Sum256([]byte(norm(who) + "\x00" + norm(what)))
	return hex.EncodeToString(h[:12])
}

// keepRecapTodos gives rc's actions the to-dos made of the same task in any version of r's recap: same who and what,
// said within recapTodoNear of it, the nearest first; each to-do goes to one action at most. True when it gave any.
func keepRecapTodos(rc *DayRecap, todos []RecapTodo) bool {
	used, gave := map[string]bool{}, false
	for i := range rc.Actions {
		a := &rc.Actions[i]
		if a.TodoID != "" {
			used[a.TodoID] = true
		}
	}
	for i := range rc.Actions {
		a := &rc.Actions[i]
		if a.TodoID != "" {
			continue
		}
		key, best, gap := recapTaskKey(a.Who, a.What), "", math.Inf(1)
		for _, t := range todos {
			if d := math.Abs(t.At - a.At); t.Task == key && !used[t.Todo] && d <= recapTodoNear && d < gap {
				best, gap = t.Todo, d
			}
		}
		if best != "" {
			a.TodoID, used[best], gave = best, true, true
		}
	}
	return gave
}

// rememberTodo notes that to-do todo was made of action a.
func rememberTodo(r *Recording, a MinutesAction, todo string) bool {
	t := RecapTodo{Task: recapTaskKey(a.Who, a.What), At: a.At, Todo: todo}
	if slices.Contains(r.RecapTodos, t) {
		return false
	}
	r.RecapTodos = append(r.RecapTodos, t)
	return true
}

// SetDayRecapTodos records the to-dos made of actions of version of day recording id's recap (confirmed: each action
// as that version has it, TodoID the to-do made of it), as SetMinutesTodos does for minutes; ErrNotFound without a
// recap. Each is remembered with its action whatever the recap says now (Recording.RecapTodos: its who and what, and
// when it was said), so any version has it on the same task said within 15 minutes, one action each
// (keepRecapTodos): the version confirmed on that action, one written since (a rewrite while the to-dos were made)
// where it has the task near the same time and nowhere else, and one written later even after a version that left it
// out.
func (e *engine) SetDayRecapTodos(rawID string, version int, confirmed []MinutesAction) (*DayRecap, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		return nil, ErrNotFound
	}
	rc, err := st.readRecap(id)
	if err != nil {
		return nil, err
	}
	changed, remembered := false, false
	for _, a := range confirmed {
		if a.TodoID == "" {
			continue
		}
		remembered = rememberTodo(r, a, a.TodoID) || remembered
		if rc.Version != version {
			continue
		}
		i := slices.IndexFunc(rc.Actions, func(b MinutesAction) bool { return b.ID == a.ID })
		if i >= 0 && rc.Actions[i].TodoID != a.TodoID {
			rc.Actions[i].TodoID, changed = a.TodoID, true
		}
	}
	changed = keepRecapTodos(rc, r.RecapTodos) || changed
	if changed {
		if ok, err := st.putRecap(id, rc); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrNotFound
		}
	}
	if remembered {
		if err := st.putEntity(KindRecording, r.ID, r, &r.Tagging, false); err != nil {
			return nil, err
		}
	}
	return rc, nil
}

// gapReasonKeys are the gap reasons as recap.md says them (kb-day-recap.json); another reason is said as it is.
var gapReasonKeys = map[string]string{GapPaused: "kb-day-recap.gap_paused", GapInterrupted: "kb-day-recap.gap_interrupted",
	GapStopped: "kb-day-recap.gap_stopped", GapPending: "kb-day-recap.gap_pending", GapLost: "kb-day-recap.gap_lost",
	GapErased: "kb-day-recap.gap_erased"}

// gapReason is reason as recap.md in l says it.
func gapReason(l i18n.Lang, reason string) string {
	if key, ok := gapReasonKeys[reason]; ok {
		return i18n.T(l, key)
	}
	return cmp.Or(reason, i18n.T(l, "kb-day-recap.gap_unknown"))
}

// renderDayRecap is recap.md: the recap with clock times in zone, the meetings and what was said outside them apart,
// then the suggested to-dos; sections without anything are left out. Its headings and fixed words are in the
// recap's language (kb-day-recap.json).
func renderDayRecap(rc *DayRecap, start time.Time, zone *time.Location) string {
	l := storedLang(rc.Language)
	clock := func(sec float64) string { return start.Add(seconds(sec)).In(zone).Format("15:04") }
	aside := func(text string) string { return i18n.T(l, "kb-day-recap.aside", i18n.Args{"text": text}) }
	var b strings.Builder
	b.WriteString("# " + i18n.T(l, "kb-day-recap.title", i18n.Args{"day": rc.Day}) + "\n\n")
	if rc.Summary != "" {
		b.WriteString(rc.Summary + "\n\n")
	}
	b.WriteString("- " + i18n.T(l, "kb-day-recap.covers", i18n.Args{"from": clock(rc.Covers.From), "to": clock(rc.Covers.To)}))
	if !rc.Final {
		b.WriteString(i18n.T(l, "kb-day-recap.so_far"))
	}
	b.WriteByte('\n')
	if len(rc.Gaps) > 0 {
		var gaps []string
		for _, g := range rc.Gaps {
			gaps = append(gaps, i18n.T(l, "kb-day-recap.gap", i18n.Args{"from": clock(g.From), "to": clock(g.To), "reason": gapReason(l, g.Reason)}))
		}
		b.WriteString("- " + i18n.T(l, "kb-day-recap.not_recorded", i18n.Args{"gaps": strings.Join(gaps, i18n.T(l, "kb-day-recap.list_sep"))}) + "\n")
	}
	section := func(title, kind string) {
		first := true
		for _, en := range rc.Timeline {
			if en.Kind != kind {
				continue
			}
			if first {
				b.WriteString("\n## " + title + "\n\n")
				first = false
			}
			b.WriteString("- " + clock(en.From) + "–" + clock(en.To) + " " + en.Title)
			if m := en.Meeting; m != nil && m.Name != "" && m.Name != en.Title {
				b.WriteString(aside(m.Name))
			}
			b.WriteByte('\n')
			for _, p := range en.Points {
				b.WriteString("  - [" + clock(p.At) + "] " + p.Text)
				if p.Who != "" {
					b.WriteString(aside(p.Who))
				}
				b.WriteByte('\n')
			}
		}
	}
	section(i18n.T(l, "kb-day-recap.meetings"), RecapMeeting)
	section(i18n.T(l, "kb-day-recap.outside"), RecapOutside)
	if len(rc.Actions) > 0 {
		b.WriteString("\n## " + i18n.T(l, "kb-day-recap.actions") + "\n\n")
		for _, a := range rc.Actions {
			b.WriteString("- [" + clock(a.At) + "] ")
			if a.Who != "" {
				b.WriteString(i18n.T(l, "kb-day-recap.owner", i18n.Args{"who": a.Who}))
			}
			b.WriteString(a.What)
			if by := cmp.Or(a.By, a.DueDate); by != "" {
				b.WriteString(aside(by))
			}
			b.WriteByte('\n')
		}
	}
	if rc.Partial {
		b.WriteString("\n> " + i18n.T(l, "kb-day-recap.partial") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
