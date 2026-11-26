package larksync

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// docxBlocks is a small document: a heading, a paragraph with a bold run and a link, a bullet, two numbered items, a
// to-do, code, a quote, a divider and a 2×2 table.
func docxBlocks(id string) []any {
	text := func(s string) map[string]any {
		return map[string]any{"elements": []any{map[string]any{"text_run": map[string]any{"content": s}}}}
	}
	return []any{
		map[string]any{"block_id": id, "block_type": 1, "children": []string{"h", "p", "b", "o1", "o2", "td", "c", "q", "dv", "tb"}, "page": text("Title")},
		map[string]any{"block_id": "h", "parent_id": id, "block_type": 4, "heading2": text("Goals")},
		map[string]any{"block_id": "p", "parent_id": id, "block_type": 2, "text": map[string]any{"elements": []any{
			map[string]any{"text_run": map[string]any{"content": "Ship "}},
			map[string]any{"text_run": map[string]any{"content": "now", "text_element_style": map[string]any{"bold": true}}},
			map[string]any{"text_run": map[string]any{"content": " spec", "text_element_style": map[string]any{"link": map[string]any{"url": "https%3A%2F%2Fexample.com%2Fspec"}}}},
		}}},
		map[string]any{"block_id": "b", "parent_id": id, "block_type": 12, "bullet": text("one")},
		map[string]any{"block_id": "o1", "parent_id": id, "block_type": 13, "ordered": text("first")},
		map[string]any{"block_id": "o2", "parent_id": id, "block_type": 13, "ordered": text("second")},
		map[string]any{"block_id": "td", "parent_id": id, "block_type": 17, "todo": map[string]any{"elements": text("done it")["elements"], "style": map[string]any{"done": true}}},
		map[string]any{"block_id": "c", "parent_id": id, "block_type": 14, "code": text("go test ./...")},
		map[string]any{"block_id": "q", "parent_id": id, "block_type": 15, "quote": text("wise words")},
		map[string]any{"block_id": "dv", "parent_id": id, "block_type": 22, "divider": map[string]any{}},
		map[string]any{"block_id": "tb", "parent_id": id, "block_type": 31, "children": []string{"c1", "c2", "c3", "c4"},
			"table": map[string]any{"cells": []string{"c1", "c2", "c3", "c4"}, "property": map[string]any{"column_size": 2, "row_size": 2}}},
		map[string]any{"block_id": "c1", "parent_id": "tb", "block_type": 32, "children": []string{"t1"}},
		map[string]any{"block_id": "c2", "parent_id": "tb", "block_type": 32, "children": []string{"t2"}},
		map[string]any{"block_id": "c3", "parent_id": "tb", "block_type": 32, "children": []string{"t3"}},
		map[string]any{"block_id": "c4", "parent_id": "tb", "block_type": 32, "children": []string{"t4"}},
		map[string]any{"block_id": "t1", "parent_id": "c1", "block_type": 2, "text": text("Name")},
		map[string]any{"block_id": "t2", "parent_id": "c2", "block_type": 2, "text": text("Owner")},
		map[string]any{"block_id": "t3", "parent_id": "c3", "block_type": 2, "text": text("Sync")},
		map[string]any{"block_id": "t4", "parent_id": "c4", "block_type": 2, "text": text("Team | A")},
	}
}

const wantDocxMarkdown = "## Goals\n\n" +
	"Ship **now** [spec](https://example.com/spec)\n\n" +
	"- one\n1. first\n2. second\n- [x] done it\n\n" +
	"```\ngo test ./...\n```\n\n" +
	"> wise words\n\n" +
	"---\n\n" +
	"| Name | Owner |\n| --- | --- |\n| Sync | Team \\| A |"

func TestRenderDocx(t *testing.T) {
	raw, _ := json.Marshal(docxBlocks("doxA"))
	var blocks []docxBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if got := renderDocx("doxA", blocks); got != wantDocxMarkdown {
		t.Fatalf("markdown:\n%s\nwant:\n%s", got, wantDocxMarkdown)
	}
}

func TestMarkdownTablesAndBound(t *testing.T) {
	got := markdownTable([][]any{{"a", "b", ""}, {1.5, true, ""}, {"", "", ""}})
	if want := "| a | b |\n| --- | --- |\n| 1.5 | true |\n"; got != want {
		t.Errorf("table = %q, want %q", got, want)
	}
	rich := []any{map[string]any{"text": "Hel", "type": "text"}, map[string]any{"text": "lo", "type": "text"}}
	got = recordsTable([]map[string]any{{"Name": rich, "Tags": []any{"x", "y"}, "Owner": []any{map[string]any{"name": "Sam"}}}})
	if want := "| Name | Owner | Tags |\n| --- | --- | --- |\n| Hello | Sam | x, y |\n"; got != want {
		t.Errorf("records = %q, want %q", got, want)
	}
	long := strings.Repeat("é", 100)
	if b := bound(long, 51); len(b) > 51 || !strings.HasSuffix(b, truncatedMark) || !strings.HasPrefix(b, "é") {
		t.Errorf("bound = %q (%d bytes)", b, len(b))
	}
}

// docPlatform answers a Drive holding docx doxA, sheet shtS and bitable bscB, and a wiki space whose node wikN wraps
// doxA and whose node wikM wraps docx doxM. Newest edits first: "old" ends the root's listing, so an entry after it
// would never be read. rev is doxA's revision (changed by the test between runs).
type docPlatform struct {
	*fakeOpen
	rev, blocks atomic.Int64
}

func newDocPlatform(t *testing.T, clk *fakeClock) *docPlatform {
	p := &docPlatform{fakeOpen: newFakeOpen(t, clk)}
	p.rev.Store(1)
	edited := secs(testNow.Add(-10 * time.Minute))
	p.handle("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("order_by") != "EditedTime" || q.Get("direction") != "DESC" {
			t.Errorf("drive query %v", q)
		}
		switch q.Get("folder_token") {
		case "":
			writeData(w, map[string]any{"files": []any{
				map[string]any{"token": "doxA", "name": "Plan", "type": "docx", "url": "https://tenant.feishu.cn/docx/doxA", "modified_time": edited},
				map[string]any{"token": "fldF", "name": "Folder", "type": "folder", "modified_time": edited},
				map[string]any{"token": "never", "name": "Never read", "type": "docx", "modified_time": edited},
				map[string]any{"token": "old", "name": "Old", "type": "docx", "modified_time": secs(testNow.Add(-100 * 24 * time.Hour))},
			}})
		case "fldF":
			writeData(w, map[string]any{"files": []any{
				map[string]any{"token": "shtS", "name": "Numbers", "type": "sheet", "url": "https://tenant.feishu.cn/sheets/shtS", "modified_time": edited},
				map[string]any{"token": "cutB", "name": "Base", "type": "shortcut", "modified_time": edited,
					"shortcut_info": map[string]string{"target_type": "bitable", "target_token": "bscB"}},
			}})
		default:
			t.Errorf("folder %q", q.Get("folder_token"))
		}
	})
	p.handle("/open-apis/docx/v1/documents/doxA", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, map[string]any{"document": map[string]any{"revision_id": p.rev.Load(), "title": "Plan"}})
	})
	p.handle("/open-apis/docx/v1/documents/doxA/blocks", func(w http.ResponseWriter, _ *http.Request) {
		p.blocks.Add(1)
		blocks := docxBlocks("doxA")
		if p.rev.Load() > 1 {
			blocks = blocks[:2] // revision 2 is only its heading
		}
		writeData(w, map[string]any{"items": blocks})
	})
	p.data("/open-apis/sheets/v3/spreadsheets/shtS/sheets/query", map[string]any{"sheets": []any{map[string]any{"sheet_id": "s1", "title": "Q1", "resource_type": "sheet"}}})
	p.data("/open-apis/sheets/v2/spreadsheets/shtS/values/s1!A1:T200", map[string]any{"valueRange": map[string]any{"values": [][]any{{"Month", "Sales"}, {"Jan", 10}}}})
	p.data("/open-apis/bitable/v1/apps/bscB/tables", map[string]any{"items": []any{map[string]any{"table_id": "tbl1", "name": "Tasks"}}})
	p.data("/open-apis/bitable/v1/apps/bscB/tables/tbl1/records", map[string]any{"items": []any{map[string]any{"fields": map[string]any{"Task": "Write tests", "Done": true}}}})
	p.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{map[string]any{"space_id": "sp1"}}})
	p.handle("/open-apis/wiki/v2/spaces/sp1/nodes", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("parent_node_token") {
		case "":
			writeData(w, map[string]any{"items": []any{
				map[string]any{"node_token": "wikN", "obj_token": "doxA", "obj_type": "docx", "title": "Plan", "has_child": true, "obj_edit_time": edited},
			}})
		case "wikN":
			writeData(w, map[string]any{"items": []any{
				map[string]any{"node_token": "wikM", "obj_token": "doxM", "obj_type": "docx", "title": "Child", "obj_edit_time": edited},
			}})
		default:
			t.Errorf("wiki parent %q", r.URL.Query().Get("parent_node_token"))
		}
	})
	p.data("/open-apis/docx/v1/documents/doxM", map[string]any{"document": map[string]any{"revision_id": 4, "title": "Child"}})
	p.data("/open-apis/docx/v1/documents/doxM/blocks", map[string]any{"items": []any{
		map[string]any{"block_id": "doxM", "block_type": 1, "children": []string{"x"}},
		map[string]any{"block_id": "x", "parent_id": "doxM", "block_type": 2, "text": map[string]any{"elements": []any{map[string]any{"text_run": map[string]any{"content": "child body"}}}}},
	}})
	// "never" cannot be read: no permission on that one document. It is kept as its title only.
	p.handle("/open-apis/docx/v1/documents/never", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	p.handle("/open-apis/docx/v1/documents/never/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	p.handle("/open-apis/docx/v1/documents/never/raw_content", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	return p
}

func docByKey(docs []larksyncwire.Doc, key string) (larksyncwire.Doc, bool) {
	for _, d := range docs {
		if d.Key == key {
			return d, true
		}
	}
	return larksyncwire.Doc{}, false
}

// Mock suite A, module part: one run reads every kind of document; the same docx reached through Drive and through
// a wiki node is one Doc with both sources, keyed by its obj_token and known by its node token too.
func TestDocumentsDriveWikiAndContent(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs)
	idx := newMemIndex()

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Partial {
		t.Fatalf("errors %+v partial %v", res.Errors, res.Partial)
	}
	a, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxA")
	if !ok {
		t.Fatalf("docs = %+v", res.Docs)
	}
	if a.Kind != "docs" || a.ObjType != "docx" || a.Revision != 1 || a.Title != "Plan" || a.Body != wantDocxMarkdown || a.BodyHash != hashOf(wantDocxMarkdown) {
		t.Errorf("doxA = %+v", a)
	}
	if a.URL != "https://tenant.feishu.cn/docx/doxA" || !slices.Equal(a.Aliases, []string{"wikN"}) {
		t.Errorf("doxA url %q aliases %v, want its own link and the node token", a.URL, a.Aliases)
	}
	if got := entries(a.Sources); !slices.Equal(got, []string{b.Key + "/drive", b.Key + "/wiki"}) {
		t.Errorf("doxA sources = %v", got)
	}
	if p.blocks.Load() != 1 || p.count("/open-apis/docx/v1/documents/doxA") != 1 {
		t.Errorf("doxA read %d times, meta %d times, want once each", p.blocks.Load(), p.count("/open-apis/docx/v1/documents/doxA"))
	}
	m, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxM")
	if !ok || m.URL != "https://tenant.feishu.cn/wiki/wikM" || !slices.Equal(m.Aliases, []string{"wikM"}) || m.Body != "child body" || m.Revision != 4 {
		t.Errorf("doxM = %+v", m)
	}
	sh, _ := docByKey(res.Docs, larksyncwire.KeyDoc+"shtS")
	if want := "## Q1\n\n| Month | Sales |\n| --- | --- |\n| Jan | 10 |\n"; sh.Body != want {
		t.Errorf("sheet body = %q, want %q", sh.Body, want)
	}
	bt, _ := docByKey(res.Docs, larksyncwire.KeyDoc+"bscB")
	if want := "## Tasks\n\n| Done | Task |\n| --- | --- |\n| true | Write tests |\n"; bt.Body != want || bt.ObjType != "bitable" {
		t.Errorf("bitable = %+v", bt)
	}
	nv, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"never")
	if !ok || nv.Body != "" || nv.Revision != 0 || nv.Title != "Never read" {
		t.Errorf("unreadable doc = %+v, want its title at revision 0", nv)
	}
	if _, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"old"); ok {
		t.Error("a document older than the backfill was read")
	}
	if res.Cursors["drive"] != secs(testNow.Add(-10*time.Minute)) || res.Cursors["wiki:sp1"] != secs(testNow.Add(-10*time.Minute)) {
		t.Errorf("cursors = %v", res.Cursors)
	}
	rec, ok := idx.get(larksyncwire.KeyDoc + "doxA")
	if !ok || rec.Revision != 1 || rec.BodyHash != a.BodyHash || rec.Kind != "doc" || len(rec.Sources) != 2 {
		t.Errorf("index record = %+v", rec)
	}
}

// Mock suite A, module part: the revision state machine across runs and bindings. Revision 1 is new; revision 2 is
// newer and handed over again; a binding that still reads revision 1 (late) hands nothing over and only adds its
// source; the same revision through another binding only adds its source.
func TestRevisionsAcrossRunsAndBindings(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	bA := binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs)
	bB := binding(p.fakeOpen, "sync2~a2", larksyncwire.TierDocs)
	bB.AccountID = "feishu:tenant_y:ou_other"
	key := larksyncwire.KeyDoc + "doxA"
	idx := newMemIndex()

	res, err := s.Run(t.Context(), bA, staticToken("tok"), nil, idx)
	if _, ok := docByKey(res.Docs, key); err != nil || !ok {
		t.Fatalf("run 1: %v %+v", err, res.Docs)
	}
	cursors := res.Cursors

	p.rev.Store(2)
	res, err = s.Run(t.Context(), bA, staticToken("tok"), cursors, idx)
	d, ok := docByKey(res.Docs, key)
	if err != nil || !ok || d.Revision != 2 || d.Body != "## Goals" {
		t.Fatalf("run 2 (newer): %v %+v", err, d)
	}
	if rec, _ := idx.get(key); rec.Revision != 2 {
		t.Fatalf("index after run 2 = %+v", rec)
	}
	if _, again := docByKey(res.Docs, larksyncwire.KeyDoc+"doxM"); again {
		t.Error("an unchanged document was handed over again")
	}

	// Binding B still sees revision 1.
	p.rev.Store(1)
	blocks := p.blocks.Load()
	res, err = s.Run(t.Context(), bB, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := docByKey(res.Docs, key); ok {
		t.Fatal("a stale revision was handed over")
	}
	if p.blocks.Load() != blocks {
		t.Error("a stale revision's body was read")
	}
	rec, _ := idx.get(key)
	if rec.Revision != 2 || rec.BodyHash != hashOf("## Goals") {
		t.Errorf("a stale revision overwrote the index: %+v", rec)
	}
	if got := entries(rec.Sources); !slices.Equal(got, []string{bA.Key + "/drive", bA.Key + "/wiki", bB.Key + "/drive", bB.Key + "/wiki"}) {
		t.Errorf("sources = %v", got)
	}

	// The same revision through B again: nothing handed over, nothing new to write for doxA.
	p.rev.Store(2)
	res, err = s.Run(t.Context(), bB, staticToken("tok"), nil, idx)
	if _, ok := docByKey(res.Docs, key); err != nil || ok {
		t.Fatalf("same revision: %v %+v", err, res.Docs)
	}
	if got, _ := idx.get(key); len(got.Sources) != 4 || got.Revision != 2 {
		t.Errorf("record = %+v", got)
	}
}

// An index that stages (the manager's) gets the records through Stage, never Put: the manager writes them with the
// cursors and the handed-over content in one transaction.
func TestRunStagesIndexRecordsWhenTheIndexCan(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	idx := &stagingIndex{memIndex: newMemIndex(), t: t}
	res, err := s.Run(t.Context(), binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs), staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.staged) != len(res.Docs) || len(idx.recs) != 0 {
		t.Fatalf("staged %d records for %d docs, index holds %d", len(idx.staged), len(res.Docs), len(idx.recs))
	}
}

// Minutes are found through links in messages (and Drive), read with their transcript, and keyed by their token.
func TestMinutesFromAMessageLink(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_a", "ou_peer", "notes: https://tenant.feishu.cn/minutes/obcnM1.", testNow.Add(-time.Hour)),
	}})
	emptySearch(f)
	f.data("/open-apis/drive/v1/files", map[string]any{"files": []any{
		map[string]any{"token": "obcnM2", "name": "Standup", "type": "minutes", "url": "https://tenant.feishu.cn/minutes/obcnM2", "modified_time": secs(testNow.Add(-time.Hour))},
	}})
	f.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{}})
	created := testNow.Add(-2 * time.Hour)
	for _, tok := range []string{"obcnM1", "obcnM2"} {
		f.data("/open-apis/minutes/v1/minutes/"+tok, map[string]any{"minute": map[string]any{"title": "Meeting " + tok, "create_time": msgTime(created), "url": "https://tenant.feishu.cn/minutes/" + tok}})
		f.handle("/open-apis/minutes/v1/minutes/"+tok+"/transcript", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("need_speaker") != "true" || r.URL.Query().Get("file_format") != "txt" {
				t.Errorf("transcript query %v", r.URL.Query())
			}
			_, _ = w.Write([]byte("Speaker 1: hello\n"))
		})
	}

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	m1, ok := docByKey(res.Docs, larksyncwire.KeyMinute+"obcnM1")
	if !ok || m1.Kind != "meetings" || m1.ObjType != "minutes" || m1.Body != "Speaker 1: hello" || m1.Revision != created.Unix() || m1.Title != "Meeting obcnM1" {
		t.Fatalf("minute 1 = %+v (docs %+v)", m1, res.Docs)
	}
	if got := entries(m1.Sources); !slices.Equal(got, []string{b.Key + "/message_link"}) {
		t.Errorf("minute 1 sources = %v", got)
	}
	m2, ok := docByKey(res.Docs, larksyncwire.KeyMinute+"obcnM2")
	if !ok || !slices.Equal(entries(m2.Sources), []string{b.Key + "/drive"}) {
		t.Errorf("minute 2 = %+v", m2)
	}
}

// Cloud document links shared in messages (including docs outside the owner's Drive root) are followed with the user
// token: docx/sheet/bitable/wiki convert to Markdown, readable text files (.md/.txt) download into the body, non-text
// files keep metadata only, all deduplicate by obj_token + revision and record message_link in sources[].
func TestLinkedCloudDocsInMessages(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	idx := newMemIndex()

	edited := secs(testNow.Add(-10 * time.Minute))
	var docxRev atomic.Int64
	docxRev.Store(3)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_dm", "name": "Peer", "chat_mode": "p2p"},
	}})
	cardContent, _ := json.Marshal(map[string]any{
		"header": map[string]any{"title": map[string]any{"content": "Shared doc"}},
		"elements": []any{
			map[string]any{"tag": "action", "actions": []any{
				map[string]any{"tag": "button", "url": "https://my.feishu.cn/docx/doxCard1"},
			}},
		},
	})
	cardMsg := map[string]any{
		"message_id":  "om_card",
		"chat_id":     "oc_dm",
		"msg_type":    "interactive",
		"create_time": msgTime(testNow.Add(-25 * time.Minute)),
		"sender":      map[string]string{"id": "ou_peer", "sender_type": "user"},
		"body":        map[string]string{"content": string(cardContent)},
	}
	shareText := "Cloud docs shared:\n" +
		"1. https://my.feishu.cn/docx/doxShare1，请查收\n" +
		"2. https://my.feishu.cn/file/boxMd1\n" +
		"3. https://my.feishu.cn/file/boxTxt1\n" +
		"4. https://my.feishu.cn/file/boxPdf1\n" +
		"5. https://my.feishu.cn/sheets/shtShare1?sheet=s1\n" +
		"6. https://my.feishu.cn/base/bscShare1\n" +
		"7. https://my.feishu.cn/wiki/wikShare1\n"
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_share", "oc_dm", "ou_peer", shareText, testNow.Add(-30*time.Minute)),
		cardMsg,
	}})
	emptySearch(f)
	emptyDocs(f) // My Space root is empty: all linked docs are shared from elsewhere.

	f.handle("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "wikShare1" {
			t.Errorf("unexpected wiki token %q", r.URL.Query().Get("token"))
		}
		writeData(w, map[string]any{"node": map[string]any{
			"node_token":    "wikShare1",
			"obj_token":     "doxWiki1",
			"obj_type":      "docx",
			"title":         "Shared Wiki Doc",
			"obj_edit_time": edited,
		}})
	})
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RequestDocs []struct {
				DocToken string `json:"doc_token"`
				DocType  string `json:"doc_type"`
			} `json:"request_docs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		all := map[string]map[string]any{
			"doxShare1": {"doc_token": "doxShare1", "doc_type": "docx", "title": "Shared Report", "latest_modify_time": edited, "url": "https://my.feishu.cn/docx/doxShare1"},
			"doxCard1":  {"doc_token": "doxCard1", "doc_type": "docx", "title": "Card Doc", "latest_modify_time": edited, "url": "https://my.feishu.cn/docx/doxCard1"},
			"boxMd1":    {"doc_token": "boxMd1", "doc_type": "file", "title": "summary.md", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxMd1"},
			"boxTxt1":   {"doc_token": "boxTxt1", "doc_type": "file", "title": "notes.TXT", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxTxt1"},
			"boxPdf1":   {"doc_token": "boxPdf1", "doc_type": "file", "title": "slides.pdf", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxPdf1"},
			"shtShare1": {"doc_token": "shtShare1", "doc_type": "sheet", "title": "Metrics", "latest_modify_time": edited, "url": "https://my.feishu.cn/sheets/shtShare1"},
			"bscShare1": {"doc_token": "bscShare1", "doc_type": "bitable", "title": "Tracker", "latest_modify_time": edited, "url": "https://my.feishu.cn/base/bscShare1"},
		}
		var metas []any
		for _, rd := range body.RequestDocs {
			if m, ok := all[rd.DocToken]; ok {
				metas = append(metas, m)
			}
		}
		writeData(w, map[string]any{"metas": metas})
	})
	f.handle("/open-apis/docx/v1/documents/doxShare1", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, map[string]any{"document": map[string]any{"revision_id": docxRev.Load(), "title": "Shared Report"}})
	})
	f.handle("/open-apis/docx/v1/documents/doxShare1/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, map[string]any{"items": []any{
			map[string]any{"block_id": "doxShare1", "block_type": 1, "children": []string{"p1"}},
			map[string]any{"block_id": "p1", "parent_id": "doxShare1", "block_type": 2, "text": map[string]any{"elements": []any{
				map[string]any{"text_run": map[string]any{"content": "shared docx body"}},
			}}},
		}})
	})
	for _, pair := range [][2]string{{"doxCard1", "card docx body"}, {"doxWiki1", "wiki docx body"}} {
		id, text := pair[0], pair[1]
		f.data("/open-apis/docx/v1/documents/"+id, map[string]any{"document": map[string]any{"revision_id": 2, "title": id}})
		f.data("/open-apis/docx/v1/documents/"+id+"/blocks", map[string]any{"items": []any{
			map[string]any{"block_id": id, "block_type": 1, "children": []string{"p1"}},
			map[string]any{"block_id": "p1", "parent_id": id, "block_type": 2, "text": map[string]any{"elements": []any{
				map[string]any{"text_run": map[string]any{"content": text}},
			}}},
		}})
	}
	f.handle("/open-apis/drive/v1/files/boxMd1/download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("\xef\xbb\xbf# Daily Notes\n\nAll good.\n"))
	})
	f.handle("/open-apis/drive/v1/files/boxTxt1/download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain text line"))
	})
	f.data("/open-apis/sheets/v3/spreadsheets/shtShare1/sheets/query", map[string]any{"sheets": []any{
		map[string]any{"sheet_id": "s1", "title": "Summary", "resource_type": "sheet"},
	}})
	f.data("/open-apis/sheets/v2/spreadsheets/shtShare1/values/s1!A1:T200", map[string]any{"valueRange": map[string]any{"values": [][]any{{"Key", "Val"}, {"A", 1}}}})
	f.data("/open-apis/bitable/v1/apps/bscShare1/tables", map[string]any{"items": []any{map[string]any{"table_id": "t1", "name": "Items"}}})
	f.data("/open-apis/bitable/v1/apps/bscShare1/tables/t1/records", map[string]any{"items": []any{map[string]any{"fields": map[string]any{"Name": "Row1"}}}})

	res, err := s.Run(t.Context(), b, staticToken("user-tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Partial {
		t.Fatalf("errors %+v partial %v", res.Errors, res.Partial)
	}
	for _, hdr := range f.authHeaders() {
		if hdr != "Bearer user-tok" {
			t.Fatalf("unexpected auth header %q", hdr)
		}
	}

	d1, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxShare1")
	if !ok || d1.ObjType != "docx" || d1.Title != "Shared Report" || d1.Body != "shared docx body" || d1.Revision != 3 || d1.URL != "https://my.feishu.cn/docx/doxShare1" {
		t.Fatalf("doxShare1 = %+v", d1)
	}
	if got := entries(d1.Sources); !slices.Equal(got, []string{b.Key + "/message_link"}) {
		t.Errorf("doxShare1 sources = %v", got)
	}
	dc, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxCard1")
	if !ok || dc.Body != "card docx body" {
		t.Fatalf("doxCard1 = %+v", dc)
	}
	dw, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxWiki1")
	if !ok || dw.Body != "wiki docx body" || !slices.Equal(dw.Aliases, []string{"wikShare1"}) || !slices.Equal(entries(dw.Sources), []string{b.Key + "/message_link"}) {
		t.Fatalf("doxWiki1 = %+v", dw)
	}
	md, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"boxMd1")
	if !ok || md.ObjType != "file" || md.Title != "summary.md" || md.Body != "# Daily Notes\n\nAll good." || md.Note != "" {
		t.Fatalf("boxMd1 = %+v", md)
	}
	txt, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"boxTxt1")
	if !ok || txt.Body != "plain text line" {
		t.Fatalf("boxTxt1 = %+v", txt)
	}
	pdf, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"boxPdf1")
	if !ok || pdf.ObjType != "file" || pdf.Title != "slides.pdf" || pdf.Body != "" || !strings.Contains(pdf.Note, "non-text") {
		t.Fatalf("boxPdf1 = %+v", pdf)
	}
	if n := f.count("/open-apis/drive/v1/files/boxPdf1/download"); n != 0 {
		t.Errorf("non-text pdf was downloaded %d times", n)
	}
	sh, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"shtShare1")
	if !ok || !strings.Contains(sh.Body, "## Summary") {
		t.Fatalf("shtShare1 = %+v", sh)
	}
	bt, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"bscShare1")
	if !ok || !strings.Contains(bt.Body, "Row1") {
		t.Fatalf("bscShare1 = %+v", bt)
	}

	// Second run with the same revisions: deduplicated by obj_token + revision, zero downloads repeated.
	mdBefore := f.count("/open-apis/drive/v1/files/boxMd1/download")
	blocksBefore := f.count("/open-apis/docx/v1/documents/doxShare1/blocks")
	res2, err := s.Run(t.Context(), b, staticToken("user-tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Docs) != 0 {
		t.Fatalf("second run emitted duplicate docs: %+v", res2.Docs)
	}
	if f.count("/open-apis/drive/v1/files/boxMd1/download") != mdBefore || f.count("/open-apis/docx/v1/documents/doxShare1/blocks") != blocksBefore {
		t.Errorf("unchanged linked docs were re-downloaded")
	}

	// Revision bump on doxShare1: only doxShare1 is re-read and emitted.
	docxRev.Store(4)
	res3, err := s.Run(t.Context(), b, staticToken("user-tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Docs) != 1 || res3.Docs[0].Key != larksyncwire.KeyDoc+"doxShare1" || res3.Docs[0].Revision != 4 {
		t.Fatalf("revision bump docs = %+v", res3.Docs)
	}
}

// Unreadable linked documents (403 forbidden, download/export disabled, wiki forbidden, oversized file) record their
// reason in Note at revision 0 without blocking other documents in the same run; once permission is granted later, the
// readable revision replaces the stub.
func TestLinkedCloudDocsUnreadableRecordedWithoutBlocking(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	idx := newMemIndex()

	edited := secs(testNow.Add(-5 * time.Minute))
	var allowDoc atomic.Bool

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_dm", "name": "Peer", "chat_mode": "p2p"},
	}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_dm", "ou_peer",
			"1. https://my.feishu.cn/docx/dox403\n"+
				"2. https://my.feishu.cn/file/boxNoExport\n"+
				"3. https://my.feishu.cn/wiki/wik403\n"+
				"4. https://my.feishu.cn/file/boxHuge\n"+
				"5. https://my.feishu.cn/docx/doxOK",
			testNow.Add(-10*time.Minute)),
	}})
	emptySearch(f)
	emptyDocs(f)

	f.handle("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 131006, "permission denied")
	})
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, map[string]any{
			"metas": []any{
				map[string]any{"doc_token": "boxNoExport", "doc_type": "file", "title": "secret.md", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxNoExport"},
				map[string]any{"doc_token": "boxHuge", "doc_type": "file", "title": "huge.txt", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxHuge"},
				map[string]any{"doc_token": "doxOK", "doc_type": "docx", "title": "Readable Doc", "latest_modify_time": edited, "url": "https://my.feishu.cn/docx/doxOK"},
			},
			"failed_list": []any{
				map[string]any{"token": "dox403", "code": 1061004},
			},
		})
	})
	f.handle("/open-apis/docx/v1/documents/dox403", func(w http.ResponseWriter, _ *http.Request) {
		if !allowDoc.Load() {
			writeCode(w, http.StatusForbidden, 1770032, "forbidden")
			return
		}
		writeData(w, map[string]any{"document": map[string]any{"revision_id": 5, "title": "Unlocked Doc"}})
	})
	f.handle("/open-apis/docx/v1/documents/dox403/blocks", func(w http.ResponseWriter, _ *http.Request) {
		if !allowDoc.Load() {
			writeCode(w, http.StatusForbidden, 1770032, "forbidden")
			return
		}
		writeData(w, map[string]any{"items": []any{
			map[string]any{"block_id": "dox403", "block_type": 1, "children": []string{"p1"}},
			map[string]any{"block_id": "p1", "parent_id": "dox403", "block_type": 2, "text": map[string]any{"elements": []any{
				map[string]any{"text_run": map[string]any{"content": "now readable"}},
			}}},
		}})
	})
	f.handle("/open-apis/docx/v1/documents/dox403/raw_content", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	f.handle("/open-apis/drive/v1/files/boxNoExport/download", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1061004, "export disabled")
	})
	f.handle("/open-apis/drive/v1/files/boxHuge/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "99999999")
		w.WriteHeader(http.StatusOK)
	})
	f.data("/open-apis/docx/v1/documents/doxOK", map[string]any{"document": map[string]any{"revision_id": 2, "title": "Readable Doc"}})
	f.data("/open-apis/docx/v1/documents/doxOK/blocks", map[string]any{"items": []any{
		map[string]any{"block_id": "doxOK", "block_type": 1, "children": []string{"p1"}},
		map[string]any{"block_id": "p1", "parent_id": "doxOK", "block_type": 2, "text": map[string]any{"elements": []any{
			map[string]any{"text_run": map[string]any{"content": "still read"}},
		}}},
	}})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("per-document unreadable errors should not fail the run: %+v", res.Errors)
	}
	for _, want := range []struct{ key, substr string }{
		{larksyncwire.KeyDoc + "dox403", "forbidden"},
		{larksyncwire.KeyDoc + "boxNoExport", "export disabled"},
		{larksyncwire.KeyDoc + "wik403", "permission denied"},
		{larksyncwire.KeyDoc + "boxHuge", "too large"},
	} {
		d, ok := docByKey(res.Docs, want.key)
		if !ok || d.Revision != 0 || d.Body != "" || !strings.Contains(d.Note, want.substr) {
			t.Errorf("%s = %+v, want rev 0 and Note containing %q", want.key, d, want.substr)
		}
	}
	okDoc, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxOK")
	if !ok || okDoc.Body != "still read" || okDoc.Note != "" {
		t.Fatalf("doxOK = %+v", okDoc)
	}

	// Once permission is granted later, dox403 supersedes the revision-0 stub cleanly.
	allowDoc.Store(true)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	unlocked, ok := docByKey(res2.Docs, larksyncwire.KeyDoc+"dox403")
	if !ok || unlocked.Revision != 5 || unlocked.Body != "now readable" || unlocked.Note != "" {
		t.Fatalf("unlocked dox403 = %+v", unlocked)
	}
}

// Drive file download rate limits (1061045) back off and retry, and a missing scope on one linked doc type reports
// ErrKindScope without blocking other link types in the same run.
func TestLinkedCloudDocsDownloadRateLimitAndScopeIsolation(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	edited := secs(testNow.Add(-5 * time.Minute))
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_1"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_1", "ou_peer",
			"https://my.feishu.cn/sheets/shtScope1 https://my.feishu.cn/file/boxRetry1",
			testNow.Add(-5*time.Minute)),
	}})
	emptySearch(f)
	emptyDocs(f)
	f.data("/open-apis/drive/v1/metas/batch_query", map[string]any{"metas": []any{
		map[string]any{"doc_token": "shtScope1", "doc_type": "sheet", "title": "Sheet", "latest_modify_time": edited},
		map[string]any{"doc_token": "boxRetry1", "doc_type": "file", "title": "memo.md", "latest_modify_time": edited},
	}})
	f.handle("/open-apis/sheets/v3/spreadsheets/shtScope1/sheets/query", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 99991672, "missing sheets scope")
	})
	var dlCalls atomic.Int32
	f.handle("/open-apis/drive/v1/files/boxRetry1/download", func(w http.ResponseWriter, _ *http.Request) {
		if dlCalls.Add(1) == 1 {
			writeCode(w, http.StatusTooManyRequests, codeDriveDownloadRateLimited, "rate limited")
			return
		}
		_, _ = w.Write([]byte("after retry"))
	})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Kind != larksyncwire.ErrKindScope || res.Errors[0].Resource != "links:sheet" {
		t.Fatalf("errors = %+v, want links:sheet scope error", res.Errors)
	}
	md, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"boxRetry1")
	if !ok || md.Body != "after retry" || dlCalls.Load() != 2 {
		t.Fatalf("boxRetry1 = %+v (calls %d)", md, dlCalls.Load())
	}
}

// ModeIM runs only IM chats, p2p discovery, and linked cloud docs / minutes in new messages — never Drive or Wiki walks.
func TestIMIncrementalModeSkipsDriveAndWikiAndFollowsLinks(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM

	cursorAt := testNow.Add(-20 * time.Minute)
	edited := secs(testNow.Add(-2 * time.Minute))
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_team", "name": "Team", "chat_mode": "group"},
	}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_new", "oc_team", "ou_peer", "new file: https://my.feishu.cn/file/boxInc1", testNow.Add(-2*time.Minute)),
	}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] != searchChatTypeP2P {
			t.Errorf("ModeIM should only run p2p discovery search, got body %v", body)
		}
		writeData(w, map[string]any{"items": []any{}})
	})
	// Note: /open-apis/drive/v1/files and /open-apis/wiki/v2/spaces are intentionally NOT registered on fakeOpen,
	// so any call to them fails the test.
	f.data("/open-apis/drive/v1/metas/batch_query", map[string]any{"metas": []any{
		map[string]any{"doc_token": "boxInc1", "doc_type": "file", "title": "update.md", "latest_modify_time": edited, "url": "https://my.feishu.cn/file/boxInc1"},
	}})
	f.handle("/open-apis/drive/v1/files/boxInc1/download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("incremental body"))
	})

	cursors := map[string]string{
		"im:oc_team": msgTime(cursorAt),
		"search:p2p": secs(cursorAt),
	}
	res, err := s.Run(t.Context(), b, staticToken("tok"), cursors, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != "om_new" {
		t.Fatalf("messages = %+v", res.Messages)
	}
	d, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"boxInc1")
	if !ok || d.Body != "incremental body" || !slices.Equal(entries(d.Sources), []string{b.Key + "/message_link"}) {
		t.Fatalf("linked doc = %+v", d)
	}
	if f.count("/open-apis/drive/v1/files") != 0 || f.count("/open-apis/wiki/v2/spaces") != 0 {
		t.Errorf("Drive or Wiki was walked in ModeIM")
	}

	// ModeIM on a docs_standard binding does nothing at all.
	bDocs := binding(f, "primary~docs", larksyncwire.TierDocs)
	bDocs.Mode = larksyncwire.ModeIM
	resDocs, err := s.Run(t.Context(), bDocs, staticToken("tok"), nil, newMemIndex())
	if err != nil || len(resDocs.Messages) != 0 || len(resDocs.Docs) != 0 {
		t.Fatalf("docs_standard ModeIM = %+v, %v", resDocs, err)
	}
}
