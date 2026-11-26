package kb

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// scriptModel answers each call with the next reply.
type scriptModel struct {
	replies []string
	calls   int
}

func (m *scriptModel) JSONMax(_ context.Context, _, _ string, _ int, out any) error {
	r := m.replies[min(m.calls, len(m.replies)-1)]
	m.calls++
	return json.Unmarshal([]byte(r), out)
}

func testEngine(t *testing.T, m Model) *Engine {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: st, Model: m, Owner: "我", Logf: func(string, ...any) {}}
}

// Two projects for the same work from two groups (people in common, no word in common) are merged into the one the
// model keeps; everything that named the other names it; the pair is not asked again.
func TestDedupeMergesTheSameWork(t *testing.T) {
	m := &scriptModel{replies: []string{`{"same":true,"sure":true,"keep":"B","reason":"同一个需求"}`}}
	e := testEngine(t, m)
	st := e.Store
	people := []string{"lark-u1", "lark-u2", "lark-u3", "lark-u4"}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.Local)
	_ = st.PutChat(&Chat{ID: "lark-f1", Kind: "group", Name: "需求群", Projects: []string{"ss-codex", "other"}})
	_ = st.PutChat(&Chat{ID: "lark-f2", Kind: "group", Name: "内部群", Projects: []string{"ss-multi"}})
	_ = st.PutPerson(&Person{ID: "lark-u1", Name: "甲", Projects: []string{"ss-codex"}})
	_ = st.PutItem(&Item{Kind: KindDoc, ID: "d1", Title: "文档", Projects: []string{"ss-codex"}})
	_ = st.PutProject(&Project{Slug: "ss-codex", Name: "SS codex 方案", Aliases: []string{"codex 二期"}, Status: "active",
		Chats: []string{"lark-f1"}, People: people, Items: []string{"docs/d1"}, Keywords: []string{"Codex"},
		Tagging: Tagging{Importance: "P0", Relevance: 95, Tags: []string{"沙箱"}}})
	_ = st.PutProject(&Project{Slug: "ss-multi", Name: "SS 支持单 agent 多 Session 窗口", Status: "active", Kind: "long",
		Chats: []string{"lark-f2"}, People: people[:3], Tagging: Tagging{Importance: "P1", Relevance: 90}})
	_ = st.PutProject(&Project{Slug: "other", Name: "别的", Status: "active", People: []string{"lark-u9"}})
	_ = st.AppendLog("ss-codex", at.Add(time.Hour), "内部群", "二期提测")
	_ = st.AppendLog("ss-multi", at, "需求群", "开始走查")

	if n := e.DedupeProjects(context.Background(), 5); n != 1 || m.calls != 1 {
		t.Fatalf("merged %d with %d call(s)", n, m.calls)
	}
	keep, _ := st.Project("ss-multi")
	drop, _ := st.Project("ss-codex")
	if drop.Status != "merged" || drop.MergedInto != "ss-multi" {
		t.Fatalf("drop = %+v", drop)
	}
	if !strings.Contains(strings.Join(keep.Aliases, "|"), "SS codex 方案") || !strings.Contains(strings.Join(keep.Aliases, "|"), "codex 二期") ||
		len(keep.Chats) != 2 || len(keep.People) != 4 || len(keep.Items) != 1 || keep.Importance != "P0" || keep.Relevance != 95 ||
		keep.Kind != "long" || !keep.Dirty || len(keep.Keywords) != 1 {
		t.Fatalf("keep = %+v", keep)
	}
	if log := st.Log("ss-multi", 10); len(log) != 3 || !strings.Contains(log[0], "开始走查") || !strings.Contains(log[1], "二期提测") || !strings.Contains(log[2], "合并了项目") {
		t.Fatalf("log = %v", log)
	}
	if c, _ := st.Chat("lark-f1"); strings.Join(c.Projects, ",") != "ss-multi,other" {
		t.Fatalf("chat projects = %v", c.Projects)
	}
	if p, _ := st.Person("lark-u1"); strings.Join(p.Projects, ",") != "ss-multi" {
		t.Fatalf("person projects = %v", p.Projects)
	}
	if it, _ := st.Item(KindDoc, "d1"); strings.Join(it.Projects, ",") != "ss-multi" {
		t.Fatalf("item projects = %v", it.Projects)
	}
	// The old name leads to the merged project; lists leave the merged one out.
	if p := e.FindProject("SS codex 方案"); p == nil || p.Slug != "ss-multi" {
		t.Fatalf("find = %+v", p)
	}
	for _, en := range e.List(KindProject, "") {
		if en.ID == "ss-codex" {
			t.Fatal("a merged project is listed")
		}
	}
	for _, k := range e.knownProjects() {
		if k.Name == "SS codex 方案" {
			t.Fatal("a merged project is offered to the model")
		}
	}
	// Nothing more to ask: "other" shares nothing; the merged pair is gone.
	if n := e.DedupeProjects(context.Background(), 5); n != 0 || m.calls != 1 {
		t.Fatalf("second run: merged %d, %d call(s)", n, m.calls)
	}
}

// A pair judged different is not asked again until dedupeRecheck has passed; by hand, merge_into merges.
func TestDedupeRemembersAndMergesByHand(t *testing.T) {
	m := &scriptModel{replies: []string{`{"same":false,"reason":"两个需求"}`}}
	e := testEngine(t, m)
	people := []string{"a", "b", "c"}
	_ = e.Store.PutProject(&Project{Slug: "x", Name: "示例 甲", Status: "active", People: people})
	_ = e.Store.PutProject(&Project{Slug: "y", Name: "示例 乙", Status: "active", People: people})
	e.DedupeProjects(context.Background(), 5)
	e.DedupeProjects(context.Background(), 5)
	if m.calls != 1 {
		t.Fatalf("%d calls", m.calls)
	}
	v := e.Store.dedupeVerdicts()
	if got := v[pairKey("x", "y")]; got.Same || got.Reason != "两个需求" {
		t.Fatalf("verdict = %+v", got)
	}
	if id, err := e.Apply(KindProject, "示例 乙", Update{MergeInto: "x"}); err != nil || id != "x" {
		t.Fatalf("merge_into = %q %v", id, err)
	}
	if p, _ := e.Store.Project("y"); p.MergedInto != "x" {
		t.Fatalf("y = %+v", p)
	}
	if err := e.MergeProjects("x", "x", ""); err == nil {
		t.Fatal("merged into itself")
	}
}

// Posts found by a project's keywords go to the model once; the related ones are linked (a recent one also on the
// timeline), every verdict remembered, and the detail lists them.
func TestLinkMoments(t *testing.T) {
	m := &scriptModel{replies: []string{`{"posts":[{"id":"m1","related":true,"note":"用户反馈"},{"id":"m2","related":false}]}`}}
	e := testEngine(t, m)
	st := e.Store
	now := time.Now()
	_ = st.PutProject(&Project{Slug: "p", Name: "示例发布", Status: "active", Keywords: []string{"示例App", "MA"}})
	_ = st.PutProject(&Project{Slug: "nokw", Name: "没有关键词", Status: "active"})
	_ = st.PutMomentPost(&MomentPost{ID: "m1", Author: "王五", Text: "示例App 卡顿", CommentCount: 5, At: now.Add(-24 * time.Hour)}, nil)
	_ = st.PutMomentPost(&MomentPost{ID: "m2", Author: "赵六", Text: "示例app 招人", At: now.Add(-48 * time.Hour)}, nil)
	_ = st.PutMomentPost(&MomentPost{ID: "m3", Author: "孙七", Text: "MA 是什么", At: now}, nil) // "MA" is too short a keyword
	_ = st.PutMomentPost(&MomentPost{ID: "m4", Author: "周八", Text: "示例App 去年", At: now.AddDate(-2, 0, 0)}, nil)

	if n := e.LinkMoments(context.Background(), 3); n != 1 || m.calls != 1 {
		t.Fatalf("linked %d with %d call(s)", n, m.calls)
	}
	p, _ := st.Project("p")
	if len(p.Moments) != 1 || p.Moments[0].ID != "m1" || p.Moments[0].Note != "用户反馈" || !p.Dirty {
		t.Fatalf("project = %+v", p)
	}
	if log := st.Log("p", 5); len(log) != 1 || !strings.Contains(log[0], "公司圈 · 王五") {
		t.Fatalf("log = %v", log)
	}
	j := st.judged("p")
	if !j["m1"] || !j["m2"] || j["m3"] || j["m4"] {
		t.Fatalf("judged = %v", j)
	}
	// Nothing new: no call.
	if e.LinkMoments(context.Background(), 3); m.calls != 1 {
		t.Fatalf("%d calls", m.calls)
	}
	d, err := e.Get(KindProject, "p", 0)
	if err != nil || len(d.Moments) != 1 || d.Moments[0].Author != "王五" || d.Moments[0].Comments != 5 {
		t.Fatalf("detail = %+v %v", d, err)
	}
	if post, md, ok := st.MomentArchived("m1"); !ok || post.ID != "m1" || !strings.Contains(md, "示例App 卡顿") {
		t.Fatalf("archived = %+v %q", post, md)
	}
	if kw := cleanKeywords([]string{"MA", "AI", "即梦", "Seedance", " ", "Seedance"}); strings.Join(kw, ",") != "即梦,Seedance" {
		t.Fatalf("keywords = %v", kw)
	}
}

// A Moments post archived long after it was written is compressed whole (back to a year), not only its last 30 days.
func TestCompressAnOldMomentChat(t *testing.T) {
	m := &scriptModel{replies: []string{`{"summary":"## 这是什么\n帖子","one_line":"一条帖子","importance":"P2","relevance":40}`}}
	e := testEngine(t, m)
	created := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	c := &Chat{ID: "moments-p1", Source: "moments", Kind: "moment", Name: "公司圈 · 帖子", Created: created, LastAt: created.AddDate(0, -3, 0), Pending: 3}
	_ = e.Store.PutChat(c)
	_ = e.Store.AppendMessages(c, []Line{{At: created.AddDate(0, -3, 0), Sender: "王五", Text: "（发帖）三个月前的帖子"}})
	if err := e.Compress(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 {
		t.Fatal("the post was settled without a model call")
	}
	if got, _ := e.Store.Chat("moments-p1"); got.Importance != "P2" || got.OneLine != "一条帖子" {
		t.Fatalf("chat = %+v", got)
	}
	if _, err := os.Stat(e.Store.path(KindChat, "moments-p1", "summary.md")); err != nil {
		t.Fatal(err)
	}
}

func TestManualMergeRetargetsLockedRelationshipsAndKeepsCorrections(t *testing.T) {
	e := testEngine(t, nil)
	for _, slug := range []string{"keep", "drop"} {
		if _, err := e.Apply(KindProject, slug, Update{Name: slug, Goal: slug + " goal"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.Store.PutChat(&Chat{ID: "chat"})
	_ = e.Store.PutPerson(&Person{ID: "person"})
	for _, kind := range []string{KindDoc, KindMeeting, KindEvent} {
		_ = e.Store.PutItem(&Item{Kind: kind, ID: "item"})
		if _, err := e.Apply(kind, "item", Update{Projects: []string{"drop"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Apply(KindChat, "chat", Update{Projects: []string{"drop"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(KindPerson, "person", Update{Projects: []string{"drop"}}); err != nil {
		t.Fatal(err)
	}
	keepFact, dropFact := "Target confirmed fact", "Source confirmed fact"
	if _, err := e.Apply(KindProject, "keep", Update{Pinned: &keepFact}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(KindProject, "drop", Update{Pinned: &dropFact}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(KindProject, "drop", Update{MergeInto: "keep"}); err != nil {
		t.Fatal(err)
	}
	c, _ := e.Store.Chat("chat")
	person, _ := e.Store.Person("person")
	if strings.Join(c.Projects, ",") != "keep" || strings.Join(person.Projects, ",") != "keep" || !taggingFieldLocked(&c.Tagging, "projects") || !taggingFieldLocked(&person.Tagging, "projects") {
		t.Fatalf("locked reverse relationships not retargeted: %+v %+v", c, person)
	}
	for _, kind := range []string{KindDoc, KindMeeting, KindEvent} {
		it, _ := e.Store.Item(kind, "item")
		if strings.Join(it.Projects, ",") != "keep" || !taggingFieldLocked(&it.Tagging, "projects") {
			t.Fatal("item relationship not retargeted", kind, it)
		}
	}
	keep, _ := e.Store.Project("keep")
	drop, _ := e.Store.Project("drop")
	if !strings.Contains(keep.Pinned, keepFact) || !strings.Contains(keep.Pinned, dropFact) || keep.Goal != "keep goal" || !taggingFieldLocked(&keep.Tagging, "goal") || drop.Pinned != dropFact || drop.Goal != "drop goal" || drop.MergedInto != "keep" {
		t.Fatalf("corrections lost: keep=%+v drop=%+v", keep, drop)
	}
}

func TestAutoMergeDoesNotHideProtectedProjects(t *testing.T) {
	for _, field := range []string{"pinned", "locked_fields", "locked"} {
		t.Run(field, func(t *testing.T) {
			m := &scriptModel{replies: []string{`{"same":true,"sure":true,"keep":"A"}`}}
			e := testEngine(t, m)
			_ = e.Store.PutProject(&Project{Slug: "keep", Name: "Shared target", People: []string{"a", "b", "c"}})
			tag := Tagging{}
			switch field {
			case "pinned":
				tag.Pinned = "Confirmed"
			case "locked_fields":
				tag.LockedFields = []string{"goal"}
			case "locked":
				tag.Locked = true
			}
			_ = e.Store.PutProject(&Project{Slug: "drop", Name: "Shared source", People: []string{"a", "b", "c"}, Tagging: tag})
			if n := e.DedupeProjects(context.Background(), 5); n != 0 || m.calls != 0 {
				t.Fatal("protected project sent for automatic dedupe", n, m.calls)
			}
			if err := e.MergeProjects("keep", "drop", "automatic"); err == nil {
				t.Fatal("automatic merge accepted protected project")
			}
			p, _ := e.Store.Project("drop")
			if !p.Live() {
				t.Fatal("protected source was hidden")
			}
		})
	}
}

func TestAutoMergeDoesNotRewriteLockedReverseRelationships(t *testing.T) {
	e := testEngine(t, nil)
	_ = e.Store.PutProject(&Project{Slug: "keep", Name: "Keep"})
	_ = e.Store.PutProject(&Project{Slug: "drop", Name: "Drop", Chats: []string{"chat"}})
	_ = e.Store.PutChat(&Chat{ID: "chat", Projects: []string{"drop"}, Tagging: Tagging{LockedFields: []string{"projects"}}})
	if err := e.MergeProjects("keep", "drop", "automatic"); err == nil {
		t.Fatal("automatic merge accepted locked reverse relationship")
	}
	p, _ := e.Store.Project("drop")
	c, _ := e.Store.Chat("chat")
	if !p.Live() || strings.Join(c.Projects, ",") != "drop" {
		t.Fatal("automatic merge changed protected relationship")
	}
}
