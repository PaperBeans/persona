package larksync

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// When readLinkedDocs runs out of budget partway through (res.Partial == true) after chat cursors have already
// advanced, unfinished links are persisted in links_queue and drained on the next run (including ModeIM with zero new
// messages). The same applies if an earlier step (such as readDrive) exhausts budget before readLinkedDocs even runs.
func TestPendingLinkedDocsResumeAcrossBudgetTruncationAndModeIM(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Budget = 5 * time.Minute
	idx := newMemIndex()

	edited := secs(testNow.Add(-5 * time.Minute))
	var messagesServed atomic.Bool
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_1", "name": "Team", "chat_mode": "group"},
	}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		if messagesServed.Swap(true) {
			writeData(w, map[string]any{"items": []any{}})
			return
		}
		writeData(w, map[string]any{"items": []any{
			textMsg("om_1", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxOld1", testNow.Add(-10*24*time.Hour)),
			textMsg("om_2", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxOld2", testNow.Add(-5*24*time.Hour)),
			textMsg("om_3", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxMid", testNow.Add(-2*time.Hour)),
			textMsg("om_4", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxNew", testNow.Add(-10*time.Minute)),
		}})
	})
	emptySearch(f)
	emptyDocs(f)

	allTokens := []string{"doxOld1", "doxOld2", "doxMid", "doxNew"}
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RequestDocs []struct {
				DocToken string `json:"doc_token"`
			} `json:"request_docs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var metas []any
		for _, rd := range body.RequestDocs {
			metas = append(metas, map[string]any{
				"doc_token":          rd.DocToken,
				"doc_type":           "docx",
				"title":              "Title " + rd.DocToken,
				"latest_modify_time": edited,
				"url":                "https://my.feishu.cn/docx/" + rd.DocToken,
			})
		}
		writeData(w, map[string]any{"metas": metas})
	})

	var orderMu sync.Mutex
	var fetchOrder []string
	var run1DocsFetched atomic.Int32
	for _, tok := range allTokens {
		tok := tok
		f.handle("/open-apis/docx/v1/documents/"+tok, func(w http.ResponseWriter, _ *http.Request) {
			orderMu.Lock()
			fetchOrder = append(fetchOrder, tok)
			orderMu.Unlock()
			writeData(w, map[string]any{"document": map[string]any{"revision_id": 2, "title": "Title " + tok}})
		})
		f.handle("/open-apis/docx/v1/documents/"+tok+"/blocks", func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, map[string]any{"items": []any{
				map[string]any{"block_id": tok, "block_type": 1, "children": []string{"p1"}},
				map[string]any{"block_id": "p1", "parent_id": tok, "block_type": 2, "text": map[string]any{"elements": []any{
					map[string]any{"text_run": map[string]any{"content": "body " + tok}},
				}}},
			}})
			// Exhaust Run 1 budget right after the 2nd doc is fetched.
			if run1DocsFetched.Add(1) == 2 {
				clk.advance(6 * time.Minute)
			}
		})
	}

	// Run 1: ModeFull fetches the 2 newest links (doxNew, doxMid) and stops on budget; im:oc_1 cursor already moved.
	res1, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Partial {
		t.Fatalf("run 1 should be partial when readLinkedDocs hits budget")
	}
	if res1.Cursors["im:oc_1"] == "" {
		t.Fatalf("im:oc_1 cursor did not advance: %v", res1.Cursors)
	}
	if len(res1.Docs) != 2 {
		t.Fatalf("run 1 docs = %+v, want 2 newest docs", res1.Docs)
	}
	if _, ok := docByKey(res1.Docs, larksyncwire.KeyDoc+"doxNew"); !ok {
		t.Fatalf("run 1 missing doxNew: %+v", res1.Docs)
	}
	if _, ok := docByKey(res1.Docs, larksyncwire.KeyDoc+"doxMid"); !ok {
		t.Fatalf("run 1 missing doxMid: %+v", res1.Docs)
	}
	qRaw := res1.Cursors[larksyncwire.PendingLinksCursor]
	st := parsePendingLinksState(qRaw)
	if len(st.Items) != 2 || st.Items[0].Token != "doxOld2" || st.Items[1].Token != "doxOld1" {
		t.Fatalf("persisted links_queue after run 1 = %+v, want [doxOld2, doxOld1]", st.Items)
	}

	// Run 2 (ModeIM, 15 minutes later): zero new messages arrive, yet ModeIM resumes and drains links_queue in
	// newest-first order (doxOld2 then doxOld1) without calling Drive/Wiki.
	clk.advance(15 * time.Minute)
	bIM := b
	bIM.Mode = larksyncwire.ModeIM
	bIM.Budget = 5 * time.Minute
	driveBefore := f.count("/open-apis/drive/v1/files")
	wikiBefore := f.count("/open-apis/wiki/v2/spaces")

	res2, err := s.Run(t.Context(), bIM, staticToken("tok"), res1.Cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Partial {
		t.Fatalf("run 2 should finish draining links_queue without partial")
	}
	if len(res2.Docs) != 2 {
		t.Fatalf("run 2 docs = %+v, want 2 remaining docs", res2.Docs)
	}
	if _, ok := docByKey(res2.Docs, larksyncwire.KeyDoc+"doxOld2"); !ok {
		t.Fatalf("run 2 missing doxOld2: %+v", res2.Docs)
	}
	if _, ok := docByKey(res2.Docs, larksyncwire.KeyDoc+"doxOld1"); !ok {
		t.Fatalf("run 2 missing doxOld1: %+v", res2.Docs)
	}
	if res2.Cursors[larksyncwire.PendingLinksCursor] != "{}" {
		t.Fatalf("run 2 links_queue = %q, want cleared \"{}\"", res2.Cursors[larksyncwire.PendingLinksCursor])
	}
	if f.count("/open-apis/drive/v1/files") != driveBefore || f.count("/open-apis/wiki/v2/spaces") != wikiBefore {
		t.Fatalf("ModeIM unexpectedly walked Drive or Wiki")
	}

	orderMu.Lock()
	gotOrder := slices.Clone(fetchOrder)
	orderMu.Unlock()
	if want := []string{"doxNew", "doxMid", "doxOld2", "doxOld1"}; !slices.Equal(gotOrder, want) {
		t.Fatalf("fetch order = %v, want %v (newest first)", gotOrder, want)
	}
}

// Even if ModeFull runs out of budget in readDrive before readLinkedDocs starts, links discovered in committed chat
// messages are still persisted into links_queue during finish() and drained on the next ModeIM run.
func TestPendingLinkedDocsPersistedWhenBudgetExhaustedBeforeReadLinkedDocs(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Budget = 5 * time.Minute
	idx := newMemIndex()

	var served atomic.Bool
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_1"}}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		if served.Swap(true) {
			writeData(w, map[string]any{"items": []any{}})
			return
		}
		writeData(w, map[string]any{"items": []any{
			textMsg("om_1", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxSaved", testNow.Add(-5*time.Minute)),
		}})
	})
	emptySearch(f)
	f.handle("/open-apis/drive/v1/files", func(w http.ResponseWriter, _ *http.Request) {
		// Burn budget during readDrive so readLinkedDocs never even runs in Run 1.
		clk.advance(10 * time.Minute)
		writeData(w, map[string]any{"files": []any{}})
	})
	f.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{}})
	f.data("/open-apis/drive/v1/metas/batch_query", map[string]any{"metas": []any{
		map[string]any{"doc_token": "doxSaved", "doc_type": "docx", "title": "Saved Doc", "latest_modify_time": secs(testNow.Add(-5 * time.Minute)), "url": "https://my.feishu.cn/docx/doxSaved"},
	}})
	f.data("/open-apis/docx/v1/documents/doxSaved", map[string]any{"document": map[string]any{"revision_id": 1, "title": "Saved Doc"}})
	f.data("/open-apis/docx/v1/documents/doxSaved/blocks", map[string]any{"items": []any{
		map[string]any{"block_id": "doxSaved", "block_type": 1, "children": []string{"p1"}},
		map[string]any{"block_id": "p1", "parent_id": "doxSaved", "block_type": 2, "text": map[string]any{"elements": []any{
			map[string]any{"text_run": map[string]any{"content": "saved body"}},
		}}},
	}})

	res1, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Partial || len(res1.Docs) != 0 {
		t.Fatalf("run 1 partial=%v docs=%+v, want partial with 0 docs", res1.Partial, res1.Docs)
	}
	st := parsePendingLinksState(res1.Cursors[larksyncwire.PendingLinksCursor])
	if len(st.Items) != 1 || st.Items[0].Token != "doxSaved" {
		t.Fatalf("run 1 failed to persist doxSaved into links_queue: %+v", st.Items)
	}

	clk.advance(15 * time.Minute)
	bIM := b
	bIM.Mode = larksyncwire.ModeIM
	res2, err := s.Run(t.Context(), bIM, staticToken("tok"), res1.Cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := docByKey(res2.Docs, larksyncwire.KeyDoc+"doxSaved")
	if !ok || d.Body != "saved body" || res2.Cursors[larksyncwire.PendingLinksCursor] != "{}" {
		t.Fatalf("run 2 doc=%+v cursors=%v", d, res2.Cursors)
	}
}

// Live links from recent messages always run ahead of historical backfill links, and within each tier newer messages
// run before older ones.
func TestPendingLinkedDocsNewerLivePriorityOverHistoricalBackfill(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute
	idx := newMemIndex()

	// Seed links_queue with two historical backfill links.
	seeded, added := s.MergePendingLinks("", []larksyncwire.PendingLinkCandidate{
		{Token: "doxHistOlder", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxHistOlder", MessageAt: testNow.Add(-15 * 24 * time.Hour)},
		{Token: "doxHistNewer", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxHistNewer", MessageAt: testNow.Add(-5 * 24 * time.Hour)},
	})
	if added != 2 {
		t.Fatalf("seeded added = %d, want 2", added)
	}

	var msgRound atomic.Int32
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_1"}}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		if msgRound.Add(1) == 1 {
			writeData(w, map[string]any{"items": []any{
				textMsg("om_live1", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxLiveOld", testNow.Add(-10*time.Minute)),
				textMsg("om_live2", "oc_1", "ou_peer", "https://my.feishu.cn/docx/doxLiveNew", testNow.Add(-1*time.Minute)),
			}})
			return
		}
		writeData(w, map[string]any{"items": []any{}})
	})
	emptySearch(f)
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RequestDocs []struct {
				DocToken string `json:"doc_token"`
			} `json:"request_docs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var metas []any
		for _, rd := range body.RequestDocs {
			metas = append(metas, map[string]any{
				"doc_token":          rd.DocToken,
				"doc_type":           "docx",
				"title":              rd.DocToken,
				"latest_modify_time": secs(testNow),
				"url":                "https://my.feishu.cn/docx/" + rd.DocToken,
			})
		}
		writeData(w, map[string]any{"metas": metas})
	})

	var orderMu sync.Mutex
	var fetchOrder []string
	var stopAfter atomic.Int32
	var fetchedThisRound atomic.Int32
	for _, tok := range []string{"doxHistOlder", "doxHistNewer", "doxLiveOld", "doxLiveNew"} {
		tok := tok
		f.handle("/open-apis/docx/v1/documents/"+tok, func(w http.ResponseWriter, _ *http.Request) {
			orderMu.Lock()
			fetchOrder = append(fetchOrder, tok)
			orderMu.Unlock()
			writeData(w, map[string]any{"document": map[string]any{"revision_id": 1, "title": tok}})
		})
		f.handle("/open-apis/docx/v1/documents/"+tok+"/blocks", func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, map[string]any{"items": []any{
				map[string]any{"block_id": tok, "block_type": 1, "children": []string{"p1"}},
				map[string]any{"block_id": "p1", "parent_id": tok, "block_type": 2, "text": map[string]any{"elements": []any{
					map[string]any{"text_run": map[string]any{"content": "content " + tok}},
				}}},
			}})
			if limit := stopAfter.Load(); limit > 0 && fetchedThisRound.Add(1) >= limit {
				clk.advance(6 * time.Minute)
			}
		})
	}

	cursors := map[string]string{
		"im:oc_1":                       msgTime(testNow.Add(-20 * time.Minute)),
		"search:p2p":                    secs(testNow.Add(-20 * time.Minute)),
		larksyncwire.PendingLinksCursor: seeded,
	}

	// Round 1: budget fits 2 docs -> fetches both live links (doxLiveNew, doxLiveOld) before historical backfill.
	stopAfter.Store(2)
	res1, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Partial || len(res1.Docs) != 2 {
		t.Fatalf("round 1 partial=%v docs=%d", res1.Partial, len(res1.Docs))
	}
	cursors = mergeTestCursors(cursors, res1.Cursors)

	// Round 2: budget fits 1 doc -> fetches newer backfill link (doxHistNewer) before doxHistOlder.
	clk.advance(15 * time.Minute)
	fetchedThisRound.Store(0)
	stopAfter.Store(1)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Partial || len(res2.Docs) != 1 || res2.Docs[0].ObjToken != "doxHistNewer" {
		t.Fatalf("round 2 partial=%v docs=%+v, want [doxHistNewer]", res2.Partial, res2.Docs)
	}
	cursors = mergeTestCursors(cursors, res2.Cursors)

	// Round 3: drains final backfill link (doxHistOlder) and empties links_queue.
	clk.advance(15 * time.Minute)
	fetchedThisRound.Store(0)
	stopAfter.Store(0)
	res3, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Partial || len(res3.Docs) != 1 || res3.Docs[0].ObjToken != "doxHistOlder" || res3.Cursors[larksyncwire.PendingLinksCursor] != "{}" {
		t.Fatalf("round 3 partial=%v docs=%+v cursors=%v", res3.Partial, res3.Docs, res3.Cursors)
	}

	orderMu.Lock()
	gotOrder := slices.Clone(fetchOrder)
	orderMu.Unlock()
	if want := []string{"doxLiveNew", "doxLiveOld", "doxHistNewer", "doxHistOlder"}; !slices.Equal(gotOrder, want) {
		t.Fatalf("fetch order = %v, want %v", gotOrder, want)
	}
}

// Pending links deduplicate by obj_token (including when a wiki link resolves to the same obj_token as a direct link,
// or when a backfill link is referenced again in a newer live message), and MergePendingLinks is idempotent.
func TestPendingLinkedDocsDeduplicationByObjTokenAndWikiAlias(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	idx := newMemIndex()

	// Seed a backfill entry for doxDup1; a fresh message links both /docx/doxDup1 and /wiki/wikDup1 -> doxDup1.
	seeded, added := s.MergePendingLinks("", []larksyncwire.PendingLinkCandidate{
		{Token: "doxDup1", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxDup1", MessageAt: testNow.Add(-10 * 24 * time.Hour)},
	})
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	// Repeating MergePendingLinks with the same token is a no-op (idempotent).
	again, addedAgain := s.MergePendingLinks(seeded, []larksyncwire.PendingLinkCandidate{
		{Token: "doxDup1", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxDup1", MessageAt: testNow.Add(-10 * 24 * time.Hour)},
	})
	if addedAgain != 0 || again != seeded {
		t.Fatalf("duplicate MergePendingLinks added=%d cursor=%q", addedAgain, again)
	}

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_1"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_1", "ou_peer",
			"https://my.feishu.cn/docx/doxDup1 and https://my.feishu.cn/wiki/wikDup1 and https://my.feishu.cn/docx/doxDup1",
			testNow.Add(-2*time.Minute)),
	}})
	emptySearch(f)
	f.data("/open-apis/wiki/v2/spaces/get_node", map[string]any{"node": map[string]any{
		"node_token": "wikDup1", "obj_token": "doxDup1", "obj_type": "docx", "title": "Unified Doc",
		"obj_edit_time": secs(testNow.Add(-2 * time.Minute)),
	}})
	f.data("/open-apis/drive/v1/metas/batch_query", map[string]any{"metas": []any{
		map[string]any{"doc_token": "doxDup1", "doc_type": "docx", "title": "Unified Doc", "latest_modify_time": secs(testNow.Add(-2 * time.Minute)), "url": "https://my.feishu.cn/docx/doxDup1"},
	}})
	f.data("/open-apis/docx/v1/documents/doxDup1", map[string]any{"document": map[string]any{"revision_id": 3, "title": "Unified Doc"}})
	f.data("/open-apis/docx/v1/documents/doxDup1/blocks", map[string]any{"items": []any{
		map[string]any{"block_id": "doxDup1", "block_type": 1, "children": []string{"p1"}},
		map[string]any{"block_id": "p1", "parent_id": "doxDup1", "block_type": 2, "text": map[string]any{"elements": []any{
			map[string]any{"text_run": map[string]any{"content": "deduped body"}},
		}}},
	}})

	cursors := map[string]string{
		"im:oc_1":                       msgTime(testNow.Add(-10 * time.Minute)),
		"search:p2p":                    secs(testNow.Add(-10 * time.Minute)),
		larksyncwire.PendingLinksCursor: seeded,
	}
	res, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Docs) != 1 || res.Docs[0].ObjToken != "doxDup1" || !slices.Equal(res.Docs[0].Aliases, []string{"wikDup1"}) {
		t.Fatalf("docs = %+v, want 1 unified doxDup1 with alias wikDup1", res.Docs)
	}
	if got := f.count("/open-apis/docx/v1/documents/doxDup1/blocks"); got != 1 {
		t.Fatalf("doxDup1 blocks fetched %d times, want 1", got)
	}
	if res.Cursors[larksyncwire.PendingLinksCursor] != "{}" {
		t.Fatalf("links_queue = %q, want \"{}\"", res.Cursors[larksyncwire.PendingLinksCursor])
	}
}

// Links that fail with 403 forbidden or scope_missing record their reason, remain in links_queue without permanent
// abandonment, skip redundant retries during backoff, and automatically retry and succeed once permissions recover
// (either immediately via updated GrantedScopes/AuthorizedAt or after the retry backoff elapses for 403 docs).
func TestPendingLinkedDocsForbiddenAndScopeMissingAutoRetryOnRecovery(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.AuthorizedAt = testNow.Add(-time.Hour)
	b.GrantedScopes = []string{"drive:drive:readonly", "docx:document:readonly"}
	idx := newMemIndex()

	var allowDoc403, allowSheetScope, allowWikiScope, allowMetaScope atomic.Bool
	var msgsServed atomic.Bool

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_1"}}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		if msgsServed.Swap(true) {
			writeData(w, map[string]any{"items": []any{}})
			return
		}
		writeData(w, map[string]any{"items": []any{
			textMsg("om_1", "oc_1", "ou_peer",
				"1. https://my.feishu.cn/docx/dox403\n"+
					"2. https://my.feishu.cn/sheets/shtScope1\n"+
					"3. https://my.feishu.cn/wiki/wikScope1\n"+
					"4. https://my.feishu.cn/file/boxMetaScope1\n"+
					"5. https://my.feishu.cn/docx/doxOK",
				testNow.Add(-5*time.Minute)),
		}})
	})
	emptySearch(f)
	emptyDocs(f)

	f.handle("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, _ *http.Request) {
		if !allowWikiScope.Load() {
			writeCode(w, http.StatusBadRequest, 99991672, "missing wiki scope")
			return
		}
		writeData(w, map[string]any{"node": map[string]any{
			"node_token": "wikScope1", "obj_token": "doxFromWik1", "obj_type": "docx",
			"title": "Recovered Wiki Doc", "obj_edit_time": secs(testNow),
		}})
	})
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, _ *http.Request) {
		if !allowMetaScope.Load() {
			writeCode(w, http.StatusBadRequest, 99991672, "missing drive metadata scope")
			return
		}
		writeData(w, map[string]any{"metas": []any{
			map[string]any{"doc_token": "boxMetaScope1", "doc_type": "file", "title": "notes.md", "latest_modify_time": secs(testNow), "url": "https://my.feishu.cn/file/boxMetaScope1"},
			map[string]any{"doc_token": "shtScope1", "doc_type": "sheet", "title": "Metrics", "latest_modify_time": "0", "url": "https://my.feishu.cn/sheets/shtScope1"},
		}})
	})
	f.handle("/open-apis/docx/v1/documents/dox403", func(w http.ResponseWriter, _ *http.Request) {
		if !allowDoc403.Load() {
			writeCode(w, http.StatusForbidden, 1770032, "forbidden")
			return
		}
		writeData(w, map[string]any{"document": map[string]any{"revision_id": 5, "title": "Unlocked Doc"}})
	})
	f.handle("/open-apis/docx/v1/documents/dox403/blocks", func(w http.ResponseWriter, _ *http.Request) {
		if !allowDoc403.Load() {
			writeCode(w, http.StatusForbidden, 1770032, "forbidden")
			return
		}
		writeData(w, map[string]any{"items": []any{
			map[string]any{"block_id": "dox403", "block_type": 1, "children": []string{"p1"}},
			map[string]any{"block_id": "p1", "parent_id": "dox403", "block_type": 2, "text": map[string]any{"elements": []any{
				map[string]any{"text_run": map[string]any{"content": "now unlocked"}},
			}}},
		}})
	})
	f.handle("/open-apis/docx/v1/documents/dox403/raw_content", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	for _, pair := range [][2]string{{"doxOK", "ok body"}, {"doxFromWik1", "wiki recovered body"}} {
		id, text := pair[0], pair[1]
		f.data("/open-apis/docx/v1/documents/"+id, map[string]any{"document": map[string]any{"revision_id": 2, "title": id}})
		f.data("/open-apis/docx/v1/documents/"+id+"/blocks", map[string]any{"items": []any{
			map[string]any{"block_id": id, "block_type": 1, "children": []string{"p1"}},
			map[string]any{"block_id": "p1", "parent_id": id, "block_type": 2, "text": map[string]any{"elements": []any{
				map[string]any{"text_run": map[string]any{"content": text}},
			}}},
		}})
	}
	f.handle("/open-apis/sheets/v3/spreadsheets/shtScope1/sheets/query", func(w http.ResponseWriter, _ *http.Request) {
		if !allowSheetScope.Load() {
			writeCode(w, http.StatusBadRequest, 99991672, "missing sheets scope")
			return
		}
		writeData(w, map[string]any{"sheets": []any{
			map[string]any{"sheet_id": "s1", "title": "Summary", "resource_type": "sheet"},
		}})
	})
	f.data("/open-apis/sheets/v2/spreadsheets/shtScope1/values/s1!A1:T200", map[string]any{
		"valueRange": map[string]any{"values": [][]any{{"Col"}, {"Val"}}},
	})
	f.handle("/open-apis/drive/v1/files/boxMetaScope1/download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("recovered markdown file"))
	})

	// Run 1: doxOK succeeds; all 4 blocked links record their reason (rev-0 stubs + links_queue entries).
	res1, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := docByKey(res1.Docs, larksyncwire.KeyDoc+"doxOK"); !ok || d.Body != "ok body" {
		t.Fatalf("run 1 doxOK = %+v", d)
	}
	q1 := parsePendingLinksState(res1.Cursors[larksyncwire.PendingLinksCursor])
	byTok1 := map[string]pendingLink{}
	for _, it := range q1.Items {
		byTok1[it.Token] = it
	}
	if len(byTok1) != 4 {
		t.Fatalf("run 1 links_queue = %+v, want 4 blocked links", q1.Items)
	}
	if byTok1["dox403"].Status != linkStatusForbidden || !strings.Contains(byTok1["dox403"].Reason, "forbidden") {
		t.Fatalf("dox403 queued state = %+v", byTok1["dox403"])
	}
	for _, tok := range []string{"shtScope1", "wikScope1", "boxMetaScope1"} {
		if byTok1[tok].Status != linkStatusScopeMissing || byTok1[tok].Reason == "" {
			t.Fatalf("%s queued state = %+v, want scope_missing with reason", tok, byTok1[tok])
		}
	}

	// Run 2 (5 minutes later, ModeIM, NO permission change): all 4 are in retry backoff -> zero doc/wiki/sheet calls.
	clk.advance(5 * time.Minute)
	bIM := b
	bIM.Mode = larksyncwire.ModeIM
	cursors := res1.Cursors
	dox403Before := f.count("/open-apis/docx/v1/documents/dox403")
	sheetBefore := f.count("/open-apis/sheets/v3/spreadsheets/shtScope1/sheets/query")
	wikiBefore := f.count("/open-apis/wiki/v2/spaces/get_node")
	res2, err := s.Run(t.Context(), bIM, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Partial || len(res2.Docs) != 0 {
		t.Fatalf("run 2 during backoff: partial=%v docs=%+v", res2.Partial, res2.Docs)
	}
	if f.count("/open-apis/docx/v1/documents/dox403") != dox403Before ||
		f.count("/open-apis/sheets/v3/spreadsheets/shtScope1/sheets/query") != sheetBefore ||
		f.count("/open-apis/wiki/v2/spaces/get_node") != wikiBefore {
		t.Fatalf("run 2 retried blocked links before RetryAfter elapsed")
	}

	// Run 3 (10 minutes later, scopes recovered via re-auth / expanded GrantedScopes):
	// shtScope1, wikScope1, and boxMetaScope1 immediately wake up and succeed (even with edited==0 on shtScope1!),
	// while dox403 is still 403 and remains queued without emitting a duplicate rev-0 stub.
	clk.advance(10 * time.Minute)
	allowSheetScope.Store(true)
	allowWikiScope.Store(true)
	allowMetaScope.Store(true)
	bIM.AuthorizedAt = clk.now()
	bIM.GrantedScopes = append(slices.Clone(bIM.GrantedScopes), "sheets:spreadsheet:readonly", "wiki:node:retrieve", "drive:drive.metadata:readonly")

	res3, err := s.Run(t.Context(), bIM, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Errors) != 0 {
		t.Fatalf("run 3 errors = %+v", res3.Errors)
	}
	if d, ok := docByKey(res3.Docs, larksyncwire.KeyDoc+"shtScope1"); !ok || !strings.Contains(d.Body, "## Summary") || d.Revision < 1 {
		t.Fatalf("run 3 recovered shtScope1 = %+v", d)
	}
	if d, ok := docByKey(res3.Docs, larksyncwire.KeyDoc+"doxFromWik1"); !ok || d.Body != "wiki recovered body" || !slices.Equal(d.Aliases, []string{"wikScope1"}) {
		t.Fatalf("run 3 recovered wikScope1 = %+v", d)
	}
	if d, ok := docByKey(res3.Docs, larksyncwire.KeyDoc+"boxMetaScope1"); !ok || d.Body != "recovered markdown file" {
		t.Fatalf("run 3 recovered boxMetaScope1 = %+v", d)
	}
	if d, ok := docByKey(res3.Docs, larksyncwire.KeyDoc+"dox403"); ok {
		t.Fatalf("still-403 doc should not emit duplicate rev-0 stub: %+v", d)
	}
	cursors = mergeTestCursors(cursors, res3.Cursors)
	q3 := parsePendingLinksState(cursors[larksyncwire.PendingLinksCursor])
	if len(q3.Items) != 1 || q3.Items[0].Token != "dox403" || q3.Items[0].Status != linkStatusForbidden {
		t.Fatalf("run 3 remaining links_queue = %+v, want only [dox403]", q3.Items)
	}

	// Run 4 (after dox403's RetryAfter elapses, without any OAuth change, doc owner grants access to dox403):
	// dox403 automatically retries, supersedes its rev-0 stub, and clears links_queue.
	wait := time.Until(time.Unix(q3.Items[0].RetryAfter, 0))
	_ = wait
	clk.advance(time.Duration(q3.Items[0].RetryAfter-clk.now().Unix())*time.Second + time.Minute)
	allowDoc403.Store(true)

	res4, err := s.Run(t.Context(), bIM, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	d403, ok := docByKey(res4.Docs, larksyncwire.KeyDoc+"dox403")
	if !ok || d403.Revision != 5 || d403.Body != "now unlocked" || d403.Note != "" {
		t.Fatalf("run 4 unlocked dox403 = %+v", d403)
	}
	if res4.Cursors[larksyncwire.PendingLinksCursor] != "{}" {
		t.Fatalf("run 4 links_queue = %q, want \"{}\"", res4.Cursors[larksyncwire.PendingLinksCursor])
	}
}

// ExtractDocLinks recognizes cloud document and wiki links (excluding minutes and non-Feishu links), and
// MergePendingLinks preserves existing live entries and resolved wiki aliases idempotently.
func TestExtractDocLinksAndMergePendingLinksIdempotent(t *testing.T) {
	s := NewSyncer(nil, nil)
	got := s.ExtractDocLinks("see https://my.feishu.cn/docx/dox1, https://my.larksuite.com/sheets/sht1, " +
		"https://my.feishu.cn/wiki/wik1, https://my.feishu.cn/minutes/obcn1, and https://example.com/docx/nope")
	var tokens []string
	for _, c := range got {
		tokens = append(tokens, string(c.ObjType)+":"+c.Token)
	}
	if want := []string{"docx:dox1", "sheet:sht1", "wiki:wik1"}; !slices.Equal(tokens, want) {
		t.Fatalf("ExtractDocLinks = %v, want %v", tokens, want)
	}

	existing, err := marshalPendingLinksState("ep1", []pendingLink{
		{Token: "doxLive", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxLive", Alias: "wikResolved", MessageAt: testNow.Unix(), Backfill: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	cands := []larksyncwire.PendingLinkCandidate{
		{Token: "doxLive", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxLive", MessageAt: testNow.Add(-24 * time.Hour)},
		{Token: "wikResolved", ObjType: "wiki", URL: "https://my.feishu.cn/wiki/wikResolved", MessageAt: testNow.Add(-24 * time.Hour)},
		{Token: "doxDropped", ObjType: "docx", URL: "https://my.feishu.cn/docx/doxDropped", MessageAt: testNow.Add(-48 * time.Hour)},
	}
	merged, added := s.MergePendingLinks(existing, cands)
	if added != 1 {
		t.Fatalf("first MergePendingLinks added = %d, want 1", added)
	}
	st := parsePendingLinksState(merged)
	if len(st.Items) != 2 || st.Items[0].Token != "doxLive" || st.Items[0].Backfill || st.Items[1].Token != "doxDropped" || !st.Items[1].Backfill {
		t.Fatalf("merged queue = %+v", st.Items)
	}
	merged2, added2 := s.MergePendingLinks(merged, cands)
	if added2 != 0 || merged2 != merged {
		t.Fatalf("second MergePendingLinks not idempotent: added=%d", added2)
	}
}

func mergeTestCursors(base, moved map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range moved {
		out[k] = v
	}
	return out
}
