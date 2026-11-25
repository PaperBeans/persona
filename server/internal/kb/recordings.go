package kb

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Limits of one upload.
const (
	maxRecordingSegments = 20000
	maxRecordingText     = 4 << 20
)

// RecordingBookmark is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingBookmark = recordingwire.RecordingBookmark

// RecordingCalendar is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingCalendar = recordingwire.RecordingCalendar

// Limits of an upload's calendar hint.
const (
	maxCalendarTitle     = 200
	maxCalendarAttendees = 100
	maxCalendarName      = 100
)

// RecordingUpload is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingUpload = recordingwire.RecordingUpload

// MeetingRef is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRef = recordingwire.MeetingRef

// RecordingState is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingState = recordingwire.RecordingState

// Re-exported from recordingwire.
const (
	RecordingStored  = recordingwire.RecordingStored
	RecordingStale   = recordingwire.RecordingStale
	RecordingExpired = recordingwire.RecordingExpired
)

// RecordingPut is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecordingPut = recordingwire.RecordingPut

// cleanUpload checks an upload and puts its segments in order (empty ones dropped).
func cleanUpload(u *RecordingUpload) error {
	if u.Revision <= 0 {
		return badRecording("revision must be > 0")
	}
	if u.StartedAt.IsZero() {
		return badRecording("started_at is required")
	}
	if u.Device != "phone" && u.Device != "watch" {
		return badRecording("device must be phone or watch")
	}
	// A day upload over the limits is day_full, not refused here: the hub's count goes with it (recording_day.go).
	day := strings.EqualFold(strings.TrimSpace(u.Kind), RecordingDay)
	if len(u.Segments) > maxRecordingSegments {
		if !day {
			return badRecording("at most %d segments", maxRecordingSegments)
		}
		u.DayFull = true
	}
	u.Next = len(u.Segments)
	// A fresh slice: the caller's upload (its Segments' backing array) is never changed.
	segs := make([]RecordingSegment, 0, len(u.Segments))
	total := 0
	for i, sg := range u.Segments {
		sg.Text, sg.N, sg.Source = strings.TrimSpace(sg.Text), 0, "" // only the hub sets a source
		if day {
			sg.N = i + 1    // its position, empty ones counted
			sg.Echo = false // Persona never speaks in an all-day recording
		}
		if sg.Text == "" {
			continue
		}
		total += len(sg.Text)
		segs = append(segs, sg)
	}
	if total > maxRecordingText {
		if !day {
			return badRecording("the transcript is over %d bytes", maxRecordingText)
		}
		u.DayFull = true
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	u.Segments = segs
	u.Bookmarks = slices.Clone(u.Bookmarks)
	if day {
		u.Spoken = nil
	}
	// Persona's echo: the phone's mark, or the same rule against the stretches it says Persona spoke in.
	markEcho(u.Segments, u.Spoken)
	sort.SliceStable(u.Bookmarks, func(i, j int) bool { return u.Bookmarks[i].At < u.Bookmarks[j].At })
	u.Title = oneLine(u.Title)
	u.Language = strings.TrimSpace(u.Language)
	switch u.Kind = strings.ToLower(strings.TrimSpace(u.Kind)); u.Kind {
	case "", RecordingNote:
		u.Kind = RecordingNote
	case RecordingMeeting:
	case RecordingDay:
		return cleanUploadDay(u)
	default:
		return badRecording("kind must be meeting, note or day")
	}
	u.Day, u.TZ, u.Part, u.Coverage = "", "", 0, nil // a day recording's only
	return cleanUploadCalendar(u)
}

// cleanUploadCalendar trims the calendar hint, dropping it when it says nothing.
func cleanUploadCalendar(u *RecordingUpload) error {
	c := u.Calendar
	if c == nil {
		return nil
	}
	c.Title, c.Location = strings.Join(strings.Fields(c.Title), " "), strings.Join(strings.Fields(c.Location), " ")
	if len([]rune(c.Title)) > maxCalendarTitle || len([]rune(c.Location)) > maxCalendarTitle {
		return badRecording("calendar title and location are at most %d characters", maxCalendarTitle)
	}
	if len(c.Attendees) > maxCalendarAttendees {
		return badRecording("at most %d calendar attendees", maxCalendarAttendees)
	}
	names := c.Attendees[:0]
	for _, n := range c.Attendees {
		n = strings.Join(strings.Fields(n), " ")
		if len([]rune(n)) > maxCalendarName {
			return badRecording("a calendar attendee is at most %d characters", maxCalendarName)
		}
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	c.Attendees = names
	if c.Title == "" && c.Location == "" && len(c.Attendees) == 0 {
		u.Calendar = nil
	}
	return nil
}

// PutRecording stores an upload (the whole state of recording id): RecordingStale when a newer revision is there,
// RecordingExpired when it started before the retention or was trimmed, ErrRecordingDeleted after a delete.
// Attendance fields that are wrong never fail it: attending is then off, and the result says why
// (normalizeAttendance). Engine.LiveChanged hears of a stored upload of a recording that attends or attended until now.
func (e *Engine) PutRecording(rawID string, u RecordingUpload, now time.Time) (RecordingPut, error) {
	put, live, err := e.putRecording(rawID, u, now)
	if err == nil && live && e.LiveChanged != nil {
		e.LiveChanged(put.id)
	}
	return put.RecordingPut, err
}

type recordingPut struct {
	RecordingPut
	id string
}

// putRecording is PutRecording; live reports a stored upload the attending feeder should look at.
func (e *Engine) putRecording(rawID string, u RecordingUpload, now time.Time) (put recordingPut, live bool, err error) {
	put.Attendance = AttendanceOff
	put.Room = u.Room != nil
	// Whatever the upload became (stored, stale, expired), the mode in effect after it.
	defer func() {
		put.Transcription, put.TranscriptionProblem = transcriptionOf(put.Meeting, u.Transcription, u.Kind, u.TranscriptionProblem)
	}()
	if e.Store.ReadOnly() {
		return put, false, ErrReadOnly
	}
	id, ok := RecordingID(rawID)
	if !ok {
		return put, false, badRecording("id must be a UUID")
	}
	put.id = id
	if err := cleanUpload(&u); err != nil {
		return put, false, err
	}
	put.Segments = u.Segments
	put.AttendanceProblem = normalizeAttendance(&u)
	day := u.Kind == RecordingDay
	if day {
		put.AttendanceProblem, put.Room = dropUploadRoom(&u, put.AttendanceProblem), false
	}
	put.Revision = u.Revision
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return put, false, ErrRecordingDeleted
	}
	r, exists := st.Recording(id)
	if exists {
		put.Revision, put.Base, put.Next = r.Revision, r.Base, r.Next
	}
	if u.StartedAt.Before(now.UTC().AddDate(0, 0, -recordingDays(e.RecordingDays()))) {
		put.Status = RecordingExpired
		return put, false, nil
	}
	if exists {
		put.Room, put.Meeting = put.Room || r.RoomMeeting(), r.Conv
	}
	if exists && r.Trimmed() {
		// Its transcript went with the retention (recording_days made longer since lets it back in the window): the
		// minutes stay what they are, and the hub takes no transcript back.
		put.Status = RecordingExpired
		return put, false, nil
	}
	if exists && (r.Kind == RecordingDay) != day {
		return put, false, ErrKindChanged
	}
	if exists && r.Revision > u.Revision {
		put.Status, put.Attendance = RecordingStale, r.AttendanceRole()
		putNamed(&put.RecordingPut, r)
		return put, false, nil
	}
	if u.DayFull {
		return put, false, DayFullError{Next: put.Next}
	}
	rebase, recapped := false, false
	if day && exists && r.Base != u.Revision {
		// A new base: a recap made from the old one loses at once what the new transcript no longer says.
		rebase = true
		if recapped, err = e.repairDayRecap(r, &u); err != nil {
			return put, false, err
		}
	}
	wasAttending := exists && r.Attending()
	meeting, parts, err := e.putMeetingFields(id, &u, now)
	if err != nil {
		return put, false, err
	}
	defer e.meetingChanged(id, parts)
	if meeting != nil && meeting.Room != nil {
		put.Room, put.AttendanceProblem = true, u.RoomProblem
	}
	put.Meeting = meeting
	// A newer transcript (not a retry of the accepted upload): its minutes are pushed as before.
	advanced := !exists || u.Revision > r.Revision
	if !exists {
		r = &Recording{ID: id}
	}
	r.Conv = meeting
	r.Revision, r.StartedAt, r.EndedAt, r.Duration = u.Revision, u.StartedAt.UTC(), u.EndedAt, u.Duration
	if r.EndedAt != nil {
		t := r.EndedAt.UTC()
		r.EndedAt = &t
	}
	// An ended hub-mode meeting is as long as the audio the hub holds when the upload says less (#9587): a phone killed
	// mid-segment loses the segment it was writing and ends the recording before it, while the hub got that audio.
	if r.EndedAt != nil && r.HubTranscribed() {
		if held := e.audioHeldEnd(id); held > r.Duration {
			r.Duration = held
			if end := r.StartedAt.Add(time.Duration(held * float64(time.Second))); end.After(*r.EndedAt) {
				r.EndedAt = &end
			}
		}
	}
	if r.Renamed != "" && u.Title != r.Title { // the phone renamed it since the web did: the newer name wins (#8877)
		r.Renamed, r.RenamedAt = "", nil
	}
	r.Language, r.Device, r.Title, r.Bookmarks, r.Complete = u.Language, u.Device, u.Title, u.Bookmarks, u.Complete
	r.Kind, r.Calendar = u.Kind, u.Calendar
	r.Attendance, r.AttendingSession = u.Attendance, u.AttendingSession // never a room meeting's (putMeetingFields)
	// chars counts the transcript of record without Persona's echo, which is not the meeting's text (a meeting of
	// only that has none, planMinutes): in hub mode the hub's sentences too (a phone that wrote nothing).
	r.Segments, r.Chars = len(u.Segments), recordChars(u.Segments)
	if r.HubTranscribed() {
		r.Chars = recordChars(TranscriptOfRecord(true, u.Segments, st.hubSegmentsOf(id)))
	}
	if day {
		putDay(r, &u)
		if rebase {
			rebaseRecap(r, recapped, now)
		}
	}
	if advanced {
		r.MinutesQuiet = 0 // a retry keeps a generation made again only for a photo quiet
	}
	planMinutes(r) // after Chars: a meeting that completed with no text gets none
	planRecap(r)
	r.Name = e.recordingName(r)
	r.Updated = now.UTC()
	if day && exists {
		// The notes of a chunk whose lines the upload changed (an erase above all) quote what it no longer says: they go
		// now, not with the next note written. Those of the others stay theirs.
		if err := st.pruneRecapNotes(r, u.Segments); err != nil {
			return put, false, err
		}
	}
	if err := st.commitRecording(r, u.Segments); err != nil {
		return put, false, err
	}
	put.Status, put.Attendance = RecordingStored, r.AttendanceRole()
	put.Revision, put.Base, put.Next = r.Revision, r.Base, r.Next
	putNamed(&put.RecordingPut, r)
	put.TitleDue = liveTitleDue(r, now)
	st.notify(KindRecording, id, "metadata")
	st.notify(KindRecording, id, "messages")
	e.index(KindRecording, id, r.Name, r.Tagging, r.OneLine, r.StartedAt)
	if r.EndedAt != nil {
		// The end batch is timed from when the hub first saw the end (live.go); the upload stands either way, and the
		// next one carrying ended_at tries again.
		if err := st.liveEnded(id, r.Attending() || r.RoomMeeting(), now); err != nil {
			e.logf("recording %s: live end: %v", id, err)
		}
	}
	return put, wasAttending || r.Attending() || r.RoomMeeting(), nil
}

// RecordingStates is every recording kept (trimmed ones too) and every tombstone, for the phone to reconcile.
func (e *Engine) RecordingStates() (kept []RecordingState, deleted []string) {
	kept, deleted = []RecordingState{}, []string{}
	for _, r := range e.Store.Recordings() {
		s := RecordingState{ID: r.ID, Revision: r.Revision, Complete: r.Complete, StartedAt: r.StartedAt,
			MinutesState: r.MinutesState, TrimmedAt: r.TrimmedAt}
		if s.NameSource = r.NameSource(); s.NameSource != "" {
			s.Name = r.Name
		}
		if r.Kind == RecordingDay {
			next, base := r.Next, r.Base
			s.Kind, s.Day, s.Part, s.Next, s.Base = r.Kind, r.Day, r.Part, &next, &base
			s.RecapState, s.RecapFinal, s.RecapFinalAt = r.RecapState, r.RecapFinal, r.RecapFinalAt
		}
		kept = append(kept, s)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].StartedAt.After(kept[j].StartedAt) })
	for id := range e.Store.tombstones() {
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	return kept, deleted
}

// recordingSummary is the model's answer for a recording (kb/recording).
type recordingSummary struct {
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	OneLine    string     `json:"one_line"`
	Importance string     `json:"importance"`
	Relevance  modelScore `json:"relevance"`
	Tags       []string   `json:"tags"`
	Projects   []struct {
		Name   string `json:"name"`
		Update string `json:"update"`
	} `json:"projects"`
}

// recordingPromptChars bounds the transcript the model reads (the start and the end kept).
const recordingPromptChars = 24000

// SummarizeRecordings writes the summary and tags of complete recordings whose latest revision has none yet, newest
// first, at most max model calls per run (failed ones included; one that needed no call, changed meanwhile, costs none);
// how many it did. A recording whose summary failed waits like a project whose rewrite failed (retryWait) and the next
// one goes on, so one transcript the model keeps failing on neither costs a call every round nor holds back the others;
// a new revision of it is tried at once. A paused connection or no model set up stops the run without counting: the
// rest stay due.
func (e *Engine) SummarizeRecordings(ctx context.Context, max int) int {
	if e.Model == nil && e.ModelFor == nil {
		return 0
	}
	now := time.Now()
	var todo []*Recording
	due := map[string]bool{}
	for _, r := range e.Store.Recordings() {
		if summaryDue(r) {
			due[r.ID] = true
			if e.retries().recordings.waits(r.ID, recordingAbout(r), now) {
				continue
			}
			todo = append(todo, r)
		}
	}
	e.retries().recordings.keep(due) // summarized, trimmed or gone in the meantime
	sort.Slice(todo, func(i, j int) bool { return todo[i].StartedAt.After(todo[j].StartedAt) })
	n, calls := 0, 0
	for _, r := range todo {
		if calls >= max || ctx.Err() != nil {
			break
		}
		called, err := e.summarizeRecording(ctx, r)
		if called {
			calls++
		}
		if err != nil {
			if deferred(ctx, err) { // the connection is held, no model, or the run ended: they stay due
				break
			}
			f := e.retries().recordings.fail(r.ID, recordingAbout(r))
			e.logf("recording %s: %v (failed %d time(s) in a row, next try in %s)", r.ID, err, f.failures, retryWait(f.failures))
			continue
		}
		e.retries().recordings.clear(r.ID)
		if called {
			n++
		}
	}
	return n
}

// recordingAbout is what a recording's summary is asked about: its revision, and in hub mode whether its final
// transcript is in (a new upload, or the final pass's end, is tried at once).
func recordingAbout(r *Recording) string {
	about := strconv.FormatInt(r.Revision, 10)
	if hubFinalIn(r) {
		about += "/final"
	}
	return about
}

// summaryDue: recording r's short summary is to be written (again): a complete recording with text, not trimmed nor
// an all-day one, whose latest revision has none, or, in hub mode, whose summary was written before the final pass
// stored its transcript ("the short summary (made again after the final pass)"). r's Conv is read.
func summaryDue(r *Recording) bool {
	if !r.Complete || r.Chars == 0 || r.Trimmed() || r.Kind == RecordingDay {
		return false
	}
	return r.SummarizedRev < r.Revision || hubFinalIn(r) && !r.SummarizedFinal
}

// hubFinalIn: r is transcribed on the hub and its final pass ended (final or failed): its transcript of record is the
// final one.
func hubFinalIn(r *Recording) bool {
	t := hubTranscription(r)
	return t != nil && t.Done()
}

// summarizeRecording writes r's summary; called: the model was asked (not when r changed meanwhile so that it needs
// none, or could not be read).
func (e *Engine) summarizeRecording(ctx context.Context, r *Recording) (called bool, err error) {
	files, meeting, hub, err := e.Store.recordSnapshot(r.ID)
	if err != nil {
		return false, err
	}
	var snapshot Recording
	if files["meta.json"] == nil {
		return false, nil
	}
	if err := json.Unmarshal(files["meta.json"], &snapshot); err != nil {
		return false, err
	}
	snapshot.Conv = meeting
	r = &snapshot
	if !summaryDue(r) {
		return false, nil // a day recording's summary is its recap's (day_recap.go): it would read the whole day
	}
	var device []RecordingSegment
	if err := json.Unmarshal(files["segments.json"], &device); err != nil {
		return false, err
	}
	segs := TranscriptOfRecord(hubMode(meeting), device, hub.Segments) // in hub mode the hub's sentences too
	called = true
	zone := e.Store.Zone()
	in := prompts.KBRecordingInput{Pinned: r.Pinned, Title: r.Title, Device: r.Device, Language: r.Language,
		Start: r.StartedAt.In(zone), Minutes: int(r.Duration / 60), Projects: e.knownProjects(), Tags: e.TopTags(40),
		Transcript: clipMiddle(e.Store.renderTranscript(r, MeetingLines(segs)), recordingPromptChars)}
	var out recordingSummary
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	err = e.modelFor("kb.compress").JSONMax(cctx, prompts.RenderKBRecording(e.writingOwner(e.outputLang(r))), prompts.RenderKBRecordingInput(in), 4000, &out)
	cancel()
	if err != nil {
		return true, err
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	cur, ok := st.Recording(r.ID)
	if !ok || cur.Trimmed() {
		return true, nil // deleted or trimmed meanwhile: no summary of a transcript that is gone
	}
	now := time.Now().UTC()
	if s := strings.TrimSpace(out.Summary); s != "" && !taggingFieldLocked(&cur.Tagging, "summary") {
		if err := st.putSummary(KindRecording, cur.ID, s+"\n", false); err != nil {
			return true, err
		}
	}
	applyTags(&cur.Tagging, out.Importance, int(out.Relevance), out.Tags, now)
	cur.OneLine = oneLine(out.OneLine)
	if t := titleLine(out.Title); t != "" && cur.AITitleFrom != AITitleMinutes { // the minutes' title settles it
		cur.AITitle, cur.AITitleFrom = t, AITitleSummary
	}
	cur.Name = st.recordingName(cur)
	// It summarized r's revision, and r's final transcript when it was in: a newer upload meanwhile, or the final pass
	// ending meanwhile, has it summarized again.
	cur.SummarizedRev, cur.SummarizedFinal = r.Revision, hubFinalIn(r)
	for _, p := range out.Projects {
		pr := e.FindProject(p.Name)
		if pr == nil || !pr.Live() {
			continue
		}
		cur.Projects = addUnique(cur.Projects, pr.Slug)
		pr.Items = addUnique(pr.Items, KindRecording+"/"+cur.ID)
		if u := oneLine(p.Update); u != "" {
			if err := st.AppendLog(pr.Slug, now, "录音 · "+cur.Name, u); err != nil {
				e.logf("project %s: %v", pr.Slug, err)
			} else {
				pr.Dirty = true
			}
		}
		_ = st.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, false)
	}
	if err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false); err != nil {
		return true, err
	}
	e.index(KindRecording, cur.ID, cur.Name, cur.Tagging, cur.OneLine, cur.StartedAt)
	return true, nil
}

// clipMiddle keeps the first and last of s within n runes, marking the cut.
func clipMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := n * 2 / 3
	return string(r[:head]) + "\n…（中间省略）…\n" + string(r[len(r)-(n-head):])
}
