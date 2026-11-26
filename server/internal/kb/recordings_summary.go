package kb

// The summary round of recordings: the knowledge base's own summarise, tag and link-to-projects of an entry, applied
// to a complete recording (docs/plans/plan-kb-recording-port.md, PR 5: it stays in Core). It names the retry book, the
// tag and project machinery and a project's log, none of which is about recordings, so it did not move with the
// upload; it still runs only with the recording module (recordingwire.WritersInCore, kb.RecordingWriters), which
// carries it as its provider's summary round.

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// What wrote a recording's AITitle (Recording.AITitleFrom) that the summary round names; the title writers have the
// rest (internal/modules/recording/writers).
const (
	AITitleSummary = "summary"
	AITitleMinutes = recordingwire.AITitleMinutes
)

// titleRunes is the most of a model's title kept (a cut ends on "…"): a list row shows one line of it.
const titleRunes = 60

// titleLine is a title the model wrote, on one line, without marks or the quotes around it, at most titleRunes.
func titleLine(s string) string {
	s = strings.Join(strings.Fields(stripMarks(s)), " ")
	s = strings.TrimSpace(strings.Trim(s, "\"'“”‘’「」『』《》"))
	return clipAtMost(s, titleRunes)
}

// stripMarks and clipAtMost are the minutes overview's helpers titleLine uses; the overview moved to the recording
// module with the minutes' writers, and these copies go there with the title writers (plan-kb-recording-port.md, PR 4).
func stripMarks(s string) string { return strings.ReplaceAll(s, "**", "") }

func clipAtMost(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return clip(s, n-1)
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
