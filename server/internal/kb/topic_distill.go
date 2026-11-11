package kb

// The distill step of the knowledge points curation (topics.go): a project's new timeline lines, or a chat's changed
// summary, go to the model, which answers with candidate points, each filed under a topic (an existing one by name, or
// a new one) and a subtopic path. The candidates wait in the topic's points.json until the merge step folds them in
// (topic_merge.go). How far a source was distilled is its mark, .topics.json in its directory (bookkeeping, not
// searched): a project's count of timeline lines, a chat's summary hash. The first pass over existing timelines is the
// same step from line one, a few calls a round.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

const (
	topicMarkFile = ".topics.json"
	// distillInput: bytes of timeline lines per call, each line clipped to distillLineRunes. Kept small so a call's
	// answer (at most distillMaxPoints points) comes back quickly.
	distillInput     = 12000
	distillLineRunes = 600
	// A project is due once it has distillBatch new lines, or fewer and its timeline has been quiet distillQuiet.
	distillBatch = 8
	distillQuiet = 30 * time.Minute
	// distillChatEvery: a chat whose summary changed is distilled again at most this often (compression rewrites the
	// summary with every batch).
	distillChatEvery    = 12 * time.Hour
	distillMaxPoints    = 25
	distillMaxNewTopics = 4   // new topics one call may open; the rest of its points for new topics are dropped
	maxTopics           = 150 // no new topic past this many
	pendingMax          = 200 // candidates a topic holds at most; the oldest go first
)

// topicMark is how far a source was distilled.
type topicMark struct {
	Lines   int       `json:"lines,omitempty"`   // a project: timeline lines distilled
	Size    int64     `json:"size,omitempty"`    // a project: log.md's size then (unchanged: nothing new, not read again)
	Summary string    `json:"summary,omitempty"` // a chat: the summary distilled (sha256, hex)
	At      time.Time `json:"at"`
}

// topicPath is a point's subtopic path as the model writes it: a list, or one string with " / " between the names.
type topicPath []string

func (p *topicPath) UnmarshalJSON(raw []byte) error {
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		*p = list
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err != nil {
		*p = nil // anything else: no path, the point stays
		return nil
	}
	*p = nil
	for _, x := range strings.Split(one, "/") {
		if x = strings.TrimSpace(x); x != "" {
			*p = append(*p, x)
		}
	}
	return nil
}

type distillPoint struct {
	Topic string    `json:"topic"`
	Path  topicPath `json:"path"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	Why   string    `json:"why"`
	When  string    `json:"when"`
}

type distillDigest struct {
	Points []distillPoint `json:"points"`
}

// distillSource is a project or chat with something to distill.
type distillSource struct {
	kind, id  string
	relevance int
}

// topicsOn: the curation runs (assistant.kb_topics, on unless turned off).
func (e *Engine) topicsOn() bool { return e.TopicsOn == nil || e.TopicsOn() }

func (s *Store) topicMark(kind, id string) topicMark {
	var m topicMark
	_, _ = readJSON(s.path(kind, id, topicMarkFile), &m)
	return m
}

func (s *Store) putTopicMark(kind, id string, m topicMark) error {
	return writeJSON(s.path(kind, id, topicMarkFile), m)
}

// timeline is a project's timeline lines, oldest first.
func (s *Store) timeline(slug string) []string {
	var lines []string
	s.scanLog(slug, func(l []byte) bool {
		lines = append(lines, string(l))
		return true
	})
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

func summaryHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// dueForDistill are the sources with something new, most relevant first.
func (e *Engine) dueForDistill(now time.Time) []distillSource {
	var out []distillSource
	for _, p := range e.Store.Projects() {
		if !p.Live() || p.Status == "ignored" {
			continue
		}
		mark := e.Store.topicMark(KindProject, p.Slug)
		info, err := os.Stat(e.Store.path(KindProject, p.Slug, "log.md"))
		if err != nil || (mark.Size > 0 && info.Size() == mark.Size) {
			continue
		}
		fresh := len(e.Store.timeline(p.Slug)) - mark.Lines
		if fresh <= 0 {
			continue
		}
		quiet := now.Sub(info.ModTime()) >= distillQuiet
		if fresh >= distillBatch || quiet || mark.At.IsZero() {
			out = append(out, distillSource{KindProject, p.Slug, p.Relevance})
		}
	}
	for _, c := range e.Store.Chats() {
		if c.Importance == "P3" || !HasPeople(c) {
			continue // notifications and bots hold no knowledge of the owner's
		}
		sum := strings.TrimSpace(e.Store.Summary(KindChat, c.ID))
		if sum == "" {
			continue
		}
		mark := e.Store.topicMark(KindChat, c.ID)
		if mark.Summary == summaryHash(sum) || (!mark.At.IsZero() && now.Sub(mark.At) < distillChatEvery) {
			continue
		}
		out = append(out, distillSource{KindChat, c.ID, c.Relevance})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].relevance > out[j].relevance })
	return out
}

// DistillTopics distills at most max sources. A source whose call failed waits like a project whose rewrite failed
// (retryWait), in the engine's memory. Only the knowledge base loop calls it.
func (e *Engine) DistillTopics(ctx context.Context, max int) int {
	if e.Model == nil || !e.topicsOn() || e.Store.ReadOnly() {
		return 0
	}
	now := time.Now()
	n := 0
	for _, src := range e.dueForDistill(now) {
		if n >= max || ctx.Err() != nil {
			break
		}
		key := src.kind + "/" + src.id
		if now.Before(e.topicRetries[key].at) {
			continue
		}
		if err := e.distill(ctx, src, now); err != nil {
			if ai.IsPaused(err) || ctx.Err() != nil {
				break
			}
			e.topicRetryLater(key, err, "distill")
			n++ // a failed call is a call
			continue
		}
		delete(e.topicRetries, key)
		n++
	}
	return n
}

func (e *Engine) topicRetryLater(key string, err error, step string) {
	if e.topicRetries == nil {
		e.topicRetries = map[string]projectRetry{}
	}
	r := e.topicRetries[key]
	r.failures++
	wait := retryWait(r.failures)
	r.at = time.Now().Add(wait)
	e.topicRetries[key] = r
	e.logf("topics: %s %s: %v (failed %d time(s) in a row, next try in %s)", step, key, err, r.failures, wait)
}

// errSourceGone: the source went (forgotten, merged) while it was being distilled.
var errSourceGone = errors.New("the source is gone")

func (e *Engine) distill(ctx context.Context, src distillSource, now time.Time) error {
	in := prompts.KBTopicDistillInput{Topics: e.topicRefs()}
	mark := e.Store.topicMark(src.kind, src.id)
	switch src.kind {
	case KindProject:
		p, ok := e.Store.Project(src.id)
		if !ok {
			return nil
		}
		lines := e.Store.timeline(src.id)
		from := min(mark.Lines, len(lines))
		size := 0
		to := from
		for to < len(lines) {
			l := clipRunes(lines[to], distillLineRunes)
			if size > 0 && size+len(l) > distillInput {
				break
			}
			in.Lines = append(in.Lines, l)
			size += len(l)
			to++
		}
		in.Project, in.Name, in.Goal = true, p.Name, p.Goal
		in.From, in.To, in.Total = from+1, to, len(lines)
		mark = topicMark{Lines: to, At: now}
		if info, err := os.Stat(e.Store.path(KindProject, src.id, "log.md")); err == nil && to == len(lines) {
			mark.Size = info.Size() // read to the end: unchanged from here means nothing new
		}
	case KindChat:
		c, ok := e.Store.Chat(src.id)
		if !ok {
			return nil
		}
		sum := strings.TrimSpace(e.Store.Summary(KindChat, src.id))
		in.Name, in.Summary = c.Name, clip(sum, 6000)
		mark = topicMark{Summary: summaryHash(sum), At: now}
	}
	var d distillDigest
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := e.modelFor("kb.topics").JSONMax(cctx, prompts.RenderKBTopicDistill(prompts.KBOwner{Owner: e.Owner}), prompts.RenderKBTopicDistillInput(in), 12000, &d); err != nil {
		return err
	}
	return e.fileCandidates(src, d.Points, mark, now)
}

// topicRefs are the existing topics the model may file points under, the biggest first.
func (e *Engine) topicRefs() []prompts.KBTopicRef {
	topics := e.Store.Topics()
	sort.SliceStable(topics, func(i, j int) bool { return topics[i].Points > topics[j].Points })
	var out []prompts.KBTopicRef
	for _, t := range topics {
		if len(out) == 80 {
			break
		}
		out = append(out, prompts.KBTopicRef{Name: t.Name, Aliases: t.Aliases, Note: t.Note})
	}
	return out
}

// fileCandidates files a source's candidate points under their topics, then moves the source's mark: a crash between
// the two only files the same candidates again, which the merge folds into the points they match.
func (e *Engine) fileCandidates(src distillSource, points []distillPoint, mark topicMark, now time.Time) error {
	s := e.Store
	s.topicMu.Lock()
	defer s.topicMu.Unlock()
	if !s.sourceExists(src) {
		return errSourceGone
	}
	day := now.In(s.Zone()).Format("2006-01-02")
	topics := s.Topics()
	opened, dropped := 0, 0
	filed := map[string][]TopicPoint{}
	byslug := map[string]*Topic{}
	var order []string
	for i, raw := range points {
		if i >= distillMaxPoints {
			break
		}
		p, ok := cleanTopicPoint(TopicPoint{Path: raw.Path, Kind: raw.Kind, Text: raw.Text, Why: raw.Why, When: raw.When})
		name := clipRunes(raw.Topic, topicPathRunes)
		if !ok || name == "" {
			continue
		}
		t := s.findTopic(name, topics)
		if t == nil {
			if opened >= distillMaxNewTopics || len(topics) >= maxTopics {
				dropped++
				continue
			}
			slug := s.newTopicSlug(name)
			if slug == "" {
				continue
			}
			t = &Topic{Slug: slug, Name: name, Created: now.UTC()}
			topics = append(topics, t)
			opened++
		}
		if p.When == "" {
			p.When = day
		}
		p.Sources = []TopicSource{{Kind: src.kind, ID: src.id, Day: p.When}}
		if _, ok := byslug[t.Slug]; !ok {
			order = append(order, t.Slug)
		}
		byslug[t.Slug] = t
		filed[t.Slug] = append(filed[t.Slug], p)
	}
	if dropped > 0 {
		e.logf("topics: %s/%s: %d point(s) for new topics dropped (at most %d new a call, %d in all)", src.kind, src.id, dropped, distillMaxNewTopics, maxTopics)
	}
	for _, slug := range order {
		t := byslug[slug]
		f, err := s.topicFile(slug)
		if err != nil {
			return err
		}
		f.Pending = append(f.Pending, filed[slug]...)
		if over := len(f.Pending) - pendingMax; over > 0 {
			f.Pending = f.Pending[over:]
		}
		if err := s.writeTopic(t, f, now.UTC()); err != nil {
			return err
		}
	}
	return s.putTopicMark(src.kind, src.id, mark)
}

func (s *Store) sourceExists(src distillSource) bool {
	switch src.kind {
	case KindProject:
		p, ok := s.Project(src.id)
		return ok && p.Live()
	case KindChat:
		_, ok := s.Chat(src.id)
		return ok
	}
	return false
}
