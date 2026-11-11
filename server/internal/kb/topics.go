package kb

// Topics: the knowledge points curation (docs/assistant/knowledge.md "Topics"). Durable knowledge (concepts, decisions,
// practices, pitfalls, facts, open questions) is pulled out of the projects' timelines and the chats' summaries
// (topic_distill.go) and filed by subject: a topic (a system, a product, an area) holds points under up to two levels
// of subtopics, merged into it by the model one edit at a time (topic_merge.go).
//
// On disk, topics/<slug>/:
//   - meta.json: the Topic (name, one line, aliases, counts, the entries its points came from, tagging);
//   - points.json: the points and the candidates waiting to be merged (bookkeeping: not searched, written only here);
//   - summary.md: the points as Markdown, written from points.json after every change (what search and the assistant read).

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

const KindTopic = "topics"

type (
	TopicPoint  = kbwire.TopicPoint
	TopicSource = kbwire.TopicSource
)

// The kinds of knowledge points.
var topicPointKinds = []string{"concept", "decision", "practice", "pitfall", "fact", "question"}

const (
	topicPointsFile = "points.json"
	// maxTopicPoints: a topic keeps at most this many points; the merge is asked to stay under it and, past a margin,
	// the oldest go (topic_merge.go).
	maxTopicPoints = 300
	// maxTopicLinks: at most this many projects and chats a topic names as where its points came from.
	maxTopicLinks  = 30
	topicTextRunes = 200
	topicPathRunes = 40
)

// Topic is a subject knowledge points are filed under.
type Topic struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Note: one line on what the topic is (the merge writes it; a hand edit locks it).
	Note    string   `json:"note,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
	Points  int      `json:"points"` // how many points points.json holds
	// Projects and Chats: the entries the points came from (most points first), shown as the topic's links.
	Projects []string  `json:"projects,omitempty"`
	Chats    []string  `json:"chats,omitempty"`
	Dirty    bool      `json:"dirty,omitempty"` // has candidates not merged yet
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Tagging
}

// topicFile is points.json: the points, the next point number, and the candidates the distill step filed here.
type topicFile struct {
	Next    int          `json:"next"`
	Points  []TopicPoint `json:"points"`
	Pending []TopicPoint `json:"pending,omitempty"`
}

func (s *Store) Topic(slug string) (*Topic, bool) {
	var t Topic
	ok, err := readJSON(s.path(KindTopic, slug, "meta.json"), &t)
	if !ok || err != nil || t.Slug == "" {
		return nil, false
	}
	t.persisted = true
	return &t, true
}

func (s *Store) Topics() []*Topic {
	var out []*Topic
	for _, id := range s.IDs(KindTopic) {
		if t, ok := s.Topic(id); ok {
			out = append(out, t)
		}
	}
	return out
}

// topicFile reads a topic's points.json (empty when it has none yet). Callers hold topicMu.
func (s *Store) topicFile(slug string) (topicFile, error) {
	var f topicFile
	if _, err := readJSON(s.path(KindTopic, slug, topicPointsFile), &f); err != nil {
		return topicFile{}, err
	}
	return f, nil
}

// TopicPoints are a topic's points, as stored.
func (s *Store) TopicPoints(slug string) []TopicPoint {
	s.topicMu.Lock()
	defer s.topicMu.Unlock()
	f, _ := s.topicFile(slug)
	return f.Points
}

// writeTopic writes a topic's points.json, its summary.md from the points, and its meta.json with the counts and the
// links the points give, in that order (the points are the record). Callers hold topicMu; t is the topic as the
// caller read it, its user-held fields kept by putEntity.
func (s *Store) writeTopic(t *Topic, f topicFile, now time.Time) error {
	if f.Points == nil {
		f.Points = []TopicPoint{}
	}
	raw, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	if err := writeFile(s.path(KindTopic, t.Slug, topicPointsFile), append(raw, '\n')); err != nil {
		return err
	}
	if err := s.writeTopicSummary(t, f.Points); err != nil {
		return err
	}
	t.Points = len(f.Points)
	t.Projects, t.Chats = topicLinks(f.Points)
	t.Dirty = len(f.Pending) > 0
	t.Updated = now
	if t.Created.IsZero() {
		t.Created = now
	}
	return s.putEntity(KindTopic, t.Slug, t, &t.Tagging, false)
}

// writeTopicSummary writes summary.md from the points when it changes.
func (s *Store) writeTopicSummary(t *Topic, points []TopicPoint) error {
	path := s.path(KindTopic, t.Slug, "summary.md")
	next := topicSummary(t, points)
	if old, _ := os.ReadFile(path); string(old) == next {
		return nil
	}
	if err := writeFile(path, []byte(next)); err != nil {
		return err
	}
	s.notify(KindTopic, t.Slug, "summary")
	return nil
}

// topicLinks are the projects and chats the points came from, those with the most points first.
func topicLinks(points []TopicPoint) (projects, chats []string) {
	count := map[string]int{}
	for _, p := range points {
		for _, src := range p.Sources {
			count[src.Kind+"/"+src.ID]++
		}
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if count[keys[i]] != count[keys[j]] {
			return count[keys[i]] > count[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		kind, id, _ := strings.Cut(k, "/")
		switch {
		case kind == KindProject && len(projects) < maxTopicLinks:
			projects = append(projects, id)
		case kind == KindChat && len(chats) < maxTopicLinks:
			chats = append(chats, id)
		}
	}
	return projects, chats
}

// topicSummary is a topic's summary.md: its one line, then its points, those without a subtopic first, then each
// subtopic (in the order it first comes) with its own points before those of its sub-subtopics.
func topicSummary(t *Topic, points []TopicPoint) string {
	var loose []TopicPoint
	var subs []string // first-level subtopics in order
	byPath := map[string][]TopicPoint{}
	var paths = map[string][]string{} // a subtopic's second-level paths in order
	for _, p := range points {
		if len(p.Path) == 0 {
			loose = append(loose, p)
			continue
		}
		top := p.Path[0]
		if _, ok := paths[top]; !ok {
			subs = append(subs, top)
			paths[top] = nil
		}
		key := strings.Join(p.Path, " / ")
		if len(p.Path) > 1 && byPath[key] == nil {
			paths[top] = append(paths[top], key)
		}
		byPath[key] = append(byPath[key], p)
	}
	var groups []prompts.KBTopicGroup
	if len(loose) > 0 {
		groups = append(groups, prompts.KBTopicGroup{Points: loose})
	}
	for _, top := range subs {
		groups = append(groups, prompts.KBTopicGroup{Heading: "## " + top, Points: byPath[top]})
		for _, key := range paths[top] {
			groups = append(groups, prompts.KBTopicGroup{Heading: "### " + key, Points: byPath[key]})
		}
	}
	return prompts.RenderKBTopicSummary(prompts.KBTopicSummary{Name: t.Name, Note: t.Note, Groups: groups})
}

// findTopic matches a name against the topics' names and aliases, as projects are matched (normName: case and
// punctuation do not count).
func (s *Store) findTopic(name string, topics []*Topic) *Topic {
	key := normName(name)
	if key == "" {
		return nil
	}
	for _, t := range topics {
		if normName(t.Name) == key || normName(t.Slug) == key {
			return t
		}
	}
	for _, t := range topics {
		for _, a := range t.Aliases {
			if normName(a) == key {
				return t
			}
		}
	}
	return nil
}

// newTopicSlug is the slug for a new topic named name, not taken by another.
func (s *Store) newTopicSlug(name string) string {
	base := Slug(name)
	if base == "" {
		return ""
	}
	slug := base
	for i := 2; ; i++ {
		if _, err := os.Stat(s.path(KindTopic, slug)); os.IsNotExist(err) {
			return slug
		}
		slug = base + "-" + strconv.Itoa(i)
	}
}

// cleanTopicPoint is a point as the model wrote it, checked: a known kind, text, a path of at most two short names.
func cleanTopicPoint(p TopicPoint) (TopicPoint, bool) {
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	if !slices.Contains(topicPointKinds, p.Kind) {
		return TopicPoint{}, false
	}
	p.Text = clipRunes(p.Text, topicTextRunes)
	if p.Text == "" {
		return TopicPoint{}, false
	}
	p.Why = clipRunes(p.Why, topicTextRunes)
	p.Path = cleanTopicPath(p.Path)
	if _, err := time.Parse("2006-01-02", p.When); err != nil {
		p.When = ""
	}
	return p, true
}

func cleanTopicPath(path []string) []string {
	var out []string
	for _, x := range path {
		if x = clipRunes(x, topicPathRunes); x != "" && len(out) < 2 {
			out = append(out, x)
		}
	}
	return out
}

// mergeSources adds the sources of from to into, one per entry, keeping the latest day.
func mergeSources(into []TopicSource, from ...TopicSource) []TopicSource {
	for _, src := range from {
		i := slices.IndexFunc(into, func(x TopicSource) bool { return x.Kind == src.Kind && x.ID == src.ID })
		switch {
		case i < 0:
			into = append(into, src)
		case src.Day > into[i].Day:
			into[i].Day = src.Day
		}
	}
	return into
}

// topicEntry is a topic as lists show it: Extra is how many points it holds.
func topicEntry(t *Topic) Entry {
	return Entry{Kind: KindTopic, ID: t.Slug, Name: t.Name, OneLine: t.Note, Importance: t.Importance, Relevance: t.Relevance,
		Tags: t.Tags, Updated: t.Updated, Extra: strconv.Itoa(t.Points)}
}

// listTopics are the topics that hold points, those with the most first (they have no importance of their own).
func (e *Engine) listTopics(has func([]string) bool) []Entry {
	var topics []*Topic
	for _, t := range e.Store.Topics() {
		if t.Points > 0 && has(t.Tags) {
			topics = append(topics, t)
		}
	}
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Points != topics[j].Points {
			return topics[i].Points > topics[j].Points
		}
		return topics[i].Updated.After(topics[j].Updated)
	})
	out := make([]Entry, 0, len(topics))
	for _, t := range topics {
		out = append(out, topicEntry(t))
	}
	return out
}

// forgetTopicSources takes deleted sources out of the knowledge points: the forgotten chats, and the timelines of the
// projects that lost lines to the forgetting (a point does not say which line it came from, so every point of such a
// project's timeline loses it; the project's mark goes back to zero and what is left of its timeline is distilled
// again). A point or candidate left with no source goes, and a topic left with neither is removed; its slug is
// returned so its index row goes too. Callers hold neither topicMu nor metaMu.
func (e *Engine) forgetTopicSources(chats, projects []string) ([]string, error) {
	if len(chats) == 0 && len(projects) == 0 {
		return nil, nil
	}
	s := e.Store
	s.topicMu.Lock()
	defer s.topicMu.Unlock()
	gone := func(src TopicSource) bool {
		return src.Kind == KindChat && slices.Contains(chats, src.ID) || src.Kind == KindProject && slices.Contains(projects, src.ID)
	}
	strip := func(list []TopicPoint) ([]TopicPoint, bool) {
		changed := false
		var out []TopicPoint
		for _, p := range list {
			kept := slices.DeleteFunc(slices.Clone(p.Sources), gone)
			if len(kept) != len(p.Sources) {
				changed = true
			}
			if len(kept) > 0 {
				p.Sources = kept
				out = append(out, p)
			}
		}
		return out, changed
	}
	var removed []string
	now := time.Now().UTC()
	for _, t := range s.Topics() {
		f, err := s.topicFile(t.Slug)
		if err != nil {
			return removed, err
		}
		var a, b bool
		f.Points, a = strip(f.Points)
		f.Pending, b = strip(f.Pending)
		if !a && !b {
			continue
		}
		if len(f.Points) == 0 && len(f.Pending) == 0 {
			if err := os.RemoveAll(s.path(KindTopic, t.Slug)); err != nil {
				return removed, err
			}
			removed = append(removed, t.Slug)
			continue
		}
		if err := s.writeTopic(t, f, now); err != nil {
			return removed, err
		}
	}
	for _, slug := range projects {
		if err := s.putTopicMark(KindProject, slug, topicMark{}); err != nil {
			return removed, err
		}
	}
	return removed, nil
}
