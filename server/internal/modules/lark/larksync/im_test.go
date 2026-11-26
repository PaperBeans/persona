package larksync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Each chat has its own cursor: one with a cursor is read from it less the five-minute overlap, one without from the
// backfill. A thread's replies are read in the same window and a reply the chat listing gave too counts once.
func TestIMReadsEachChatFromItsCursorWithOverlap(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	cursorA := testNow.Add(-time.Hour)
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_a", "name": "Team", "chat_mode": "group"},
		map[string]any{"chat_id": "oc_b", "name": "Peer", "chat_mode": "p2p"},
	}})
	starts := map[string]string{}
	var smu sync.Mutex
	root := textMsg("om_1", "oc_a", "ou_peer", "hello", testNow.Add(-30*time.Minute))
	root["thread_id"] = "omt_1"
	reply := textMsg("om_2", "oc_a", testOwner, "reply in thread", testNow.Add(-20*time.Minute))
	reply["thread_id"] = "omt_1"
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("sort_type") != "ByCreateTimeAsc" || q.Get("end_time") != secs(testNow) {
			t.Errorf("messages query = %v", q)
		}
		smu.Lock()
		starts[q.Get("container_id_type")+":"+q.Get("container_id")] = q.Get("start_time")
		smu.Unlock()
		switch q.Get("container_id") {
		case "oc_a":
			writeData(w, map[string]any{"items": []any{root}})
		case "omt_1":
			replyNoChat := map[string]any{}
			for k, v := range reply {
				replyNoChat[k] = v
			}
			delete(replyNoChat, "chat_id") // a thread listing need not say the chat
			writeData(w, map[string]any{"items": []any{root, replyNoChat}})
		case "oc_b":
			writeData(w, map[string]any{"items": []any{}})
		default:
			t.Errorf("messages of %q", q.Get("container_id"))
		}
	})
	emptySearch(f)
	emptyDocs(f)

	res, err := s.Run(t.Context(), b, staticToken("u-token"), map[string]string{"im:oc_a": msgTime(cursorA)}, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.FormatInt(cursorA.Unix()-300, 10); starts["chat:oc_a"] != want {
		t.Errorf("oc_a start = %s, want cursor less 5 min %s", starts["chat:oc_a"], want)
	}
	if starts["thread:omt_1"] != starts["chat:oc_a"] {
		t.Errorf("thread start = %s, want the chat's window %s", starts["thread:omt_1"], starts["chat:oc_a"])
	}
	if want := secs(testNow.Add(-48 * time.Hour)); starts["chat:oc_b"] != want {
		t.Errorf("oc_b start = %s, want the backfill %s", starts["chat:oc_b"], want)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("messages = %+v, want om_1 and om_2 once each", res.Messages)
	}
	m1, m2 := res.Messages[0], res.Messages[1]
	if m1.ID != "om_1" || m1.Text != "hello" || m1.Mine || m1.ChatName != "Team" || m1.ChatType != "group" || m1.ThreadID != "omt_1" {
		t.Errorf("om_1 = %+v", m1)
	}
	if m2.ID != "om_2" || !m2.Mine || m2.ChatID != "oc_a" {
		t.Errorf("om_2 = %+v, want the owner's reply in oc_a", m2)
	}
	if !m1.At.Equal(testNow.Add(-30 * time.Minute).Truncate(time.Millisecond)) {
		t.Errorf("om_1 at %v", m1.At)
	}
	if len(m1.Sources) != 1 || m1.Sources[0] != (larksyncwire.SourceRef{Channel: larksyncwire.ConnectorID, Binding: b.Key, Account: testAccount, Entry: larksyncwire.EntryChat, SeenAt: testNow}) {
		t.Errorf("om_1 sources = %+v", m1.Sources)
	}
	// The cursor is the newest message the chat listing gave; a quiet chat's is the run's end.
	if got, want := res.Cursors["im:oc_a"], msgTime(testNow.Add(-30*time.Minute)); got != want {
		t.Errorf("cursor oc_a = %s, want %s", got, want)
	}
	if got, want := res.Cursors["im:oc_b"], msgTime(testNow); got != want {
		t.Errorf("cursor oc_b = %s, want %s", got, want)
	}
	if len(res.Errors) != 0 || res.Partial {
		t.Errorf("errors %+v partial %v", res.Errors, res.Partial)
	}
}

// A chat that fails part way (a server error that outlasts the retries on its second page) hands over nothing of that
// chat and keeps its cursor; the other chats go on.
func TestIMFailingChatKeepsItsCursorAndOthersContinue(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_bad", "name": "Bad"},
		map[string]any{"chat_id": "oc_good", "name": "Good"},
	}})
	var badHits atomic.Int32
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("container_id") {
		case "oc_bad":
			if q.Get("page_token") == "" {
				writeData(w, map[string]any{"items": []any{textMsg("om_b1", "oc_bad", "ou_peer", "first page", testNow.Add(-time.Hour))}, "has_more": true, "page_token": "p2"})
				return
			}
			badHits.Add(1)
			writeCode(w, http.StatusInternalServerError, 0, "boom")
		case "oc_good":
			writeData(w, map[string]any{"items": []any{textMsg("om_g1", "oc_good", "ou_peer", "fine", testNow.Add(-time.Hour))}})
		}
	})
	emptySearch(f)
	emptyDocs(f)

	old := msgTime(testNow.Add(-2 * time.Hour))
	res, err := s.Run(t.Context(), b, staticToken("u-token"), map[string]string{"im:oc_bad": old}, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != "om_g1" {
		t.Fatalf("messages = %+v, want only oc_good's", res.Messages)
	}
	if _, moved := res.Cursors["im:oc_bad"]; moved {
		t.Errorf("oc_bad's cursor moved: %v", res.Cursors)
	}
	if res.Cursors["im:oc_good"] == "" {
		t.Errorf("oc_good's cursor did not move: %v", res.Cursors)
	}
	if len(res.Errors) != 1 || res.Errors[0].Resource != "im:oc_bad" || res.Errors[0].Kind != larksyncwire.ErrKindTransient {
		t.Fatalf("errors = %+v", res.Errors)
	}
	if got := badHits.Load(); got != 1+maxTransientRetries {
		t.Errorf("second page tried %d times, want %d", got, 1+maxTransientRetries)
	}
	if got := s.slept(); !slices.Equal(got, []time.Duration{time.Second, 2 * time.Second}) {
		t.Errorf("backoff = %v, want 1s, 2s", got)
	}
}

// A 429 is waited out for as long as Retry-After says (through the sleeper, the clock moves; nothing really sleeps),
// a 99991400 on an HTTP 400 for x-ogw-ratelimit-reset, and then the call succeeds.
func TestRateLimitHonoursRetryAfterThenSucceeds(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	var chatCalls, msgCalls atomic.Int32
	f.handle("/open-apis/im/v1/chats", func(w http.ResponseWriter, _ *http.Request) {
		if chatCalls.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			writeCode(w, http.StatusTooManyRequests, 0, "too many")
			return
		}
		writeData(w, map[string]any{"items": []any{map[string]any{"chat_id": "oc_a", "name": "Team"}}})
	})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		if msgCalls.Add(1) == 1 {
			w.Header().Set("X-Ogw-Ratelimit-Reset", "3")
			writeCode(w, http.StatusBadRequest, codeRateLimited, "frequency limited")
			return
		}
		writeData(w, map[string]any{"items": []any{textMsg("om_1", "oc_a", "ou_peer", "hi", testNow.Add(-time.Minute))}})
	})
	emptySearch(f)
	emptyDocs(f)

	res, err := s.Run(t.Context(), b, staticToken("u-token"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.slept(); !slices.Equal(got, []time.Duration{7 * time.Second, 3 * time.Second}) {
		t.Errorf("waits = %v, want 7s (Retry-After) then 3s (x-ogw-ratelimit-reset)", got)
	}
	if len(res.Errors) != 0 || len(res.Messages) != 1 {
		t.Fatalf("errors %+v, messages %+v", res.Errors, res.Messages)
	}
}

// A chat still limited after the backoff (1 s, 2 s, 4 s, 8 s) is a rate_limited ResourceError; its cursor stays.
func TestPersistentRateLimitIsAResourceError(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	var hits atomic.Int32
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writeCode(w, http.StatusTooManyRequests, 0, "too many")
	})
	emptySearch(f)
	emptyDocs(f)

	res, err := s.Run(t.Context(), b, staticToken("u-token"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Resource != "im:oc_a" || res.Errors[0].Kind != larksyncwire.ErrKindRateLimited {
		t.Fatalf("errors = %+v", res.Errors)
	}
	if got := hits.Load(); got != 1+maxLimitedRetries {
		t.Errorf("tried %d times, want %d", got, 1+maxLimitedRetries)
	}
	if got := s.slept(); !slices.Equal(got, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}) {
		t.Errorf("backoff = %v", got)
	}
	if _, moved := res.Cursors["im:oc_a"]; moved {
		t.Errorf("cursor moved: %v", res.Cursors)
	}
}

// The documents tier never touches chats or message search (the fake answers neither).
func TestDocsTierSkipsIM(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	emptyDocs(f)

	res, err := s.Run(t.Context(), binding(f, "primary~a1", larksyncwire.TierDocs), staticToken("u-token"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if f.count("/open-apis/im/v1/chats") != 0 || f.count("/open-apis/search/v2/message") != 0 {
		t.Fatal("the documents tier read chats")
	}
	if res.Cursors["drive"] == "" {
		t.Errorf("drive cursor = %v", res.Cursors)
	}
}

// Message search adds its source to a message a chat gave, and reads a hit no listed chat holds by its id. A search
// that fails is only its own ResourceError.
func TestSearchAddsSourcesAndFetchesUnlistedMessages(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a", "name": "Team"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{textMsg("om_1", "oc_a", "ou_peer", "in a chat", testNow.Add(-time.Hour))}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || body["query"] != "" || body["end_time"] != secs(testNow) {
			t.Errorf("search %s %v", r.Method, body)
		}
		if body["chat_type"] == searchChatTypeP2P {
			writeData(w, map[string]any{"items": []string{}})
			return
		}
		writeData(w, map[string]any{"items": []string{"om_1", "om_9"}})
	})
	f.data("/open-apis/im/v1/messages/om_9", map[string]any{"items": []any{textMsg("om_9", "oc_left", "ou_peer", "from a chat I left", testNow.Add(-2*time.Hour))}})
	emptyDocs(f)

	res, err := s.Run(t.Context(), b, staticToken("u-token"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("messages = %+v", res.Messages)
	}
	if got := entries(res.Messages[0].Sources); !slices.Equal(got, []string{b.Key + "/chat", b.Key + "/search"}) {
		t.Errorf("om_1 sources = %v", got)
	}
	if m := res.Messages[1]; m.ID != "om_9" || m.ChatID != "oc_left" || !slices.Equal(entries(m.Sources), []string{b.Key + "/search"}) {
		t.Errorf("om_9 = %+v", m)
	}
	if res.Cursors["search"] != secs(testNow) {
		t.Errorf("search cursor = %v", res.Cursors)
	}

	// A failing search: the chats still count.
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 1, "query required")
	})
	res, err = s.Run(t.Context(), b, staticToken("u-token"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Resource != "search" || len(res.Messages) != 1 || res.Cursors["im:oc_a"] == "" {
		t.Fatalf("result = %+v", res)
	}
}

// A refused token is asked for again once (the manager refreshes); the fresh one works.
func TestRefusedTokenRetriesOnceWithAFreshToken(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	f.handle("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			writeCode(w, http.StatusBadRequest, 99991677, "token expired")
			return
		}
		writeData(w, map[string]any{"files": []any{}})
	})
	f.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{}})
	var calls atomic.Int32
	token := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "stale", nil
		}
		return "fresh", nil
	}
	res, err := s.Run(t.Context(), binding(f, "primary~a1", larksyncwire.TierDocs), token, nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.authHeaders(); got[0] != "Bearer stale" || got[1] != "Bearer fresh" {
		t.Errorf("authorization = %v", got)
	}
	if len(res.Errors) != 0 {
		t.Errorf("errors = %+v", res.Errors)
	}
}

// A token still refused after the retry stops the run with an auth *Error; what finished before it is handed back
// next to it, and nothing after it is read.
func TestTokenRefusedStopsTheRunWithAnAuthError(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{textMsg("om_1", "oc_a", "ou_peer", "hi", testNow.Add(-time.Hour))}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusUnauthorized, 0, "unauthorized")
	})
	var calls atomic.Int32
	token := func(context.Context) (string, error) { calls.Add(1); return "tok", nil }

	res, err := s.Run(t.Context(), b, token, nil, newMemIndex())
	var e *larksyncwire.Error
	if !errors.As(err, &e) || e.Kind != larksyncwire.ErrKindAuth {
		t.Fatalf("err = %v, want an auth *Error", err)
	}
	// Token calls: the chat list, its messages, the sender's name, and search twice (refused, then retried).
	if f.count("/open-apis/search/v2/message") != 2 || calls.Load() != 5 {
		t.Errorf("search tried %d times with %d token calls, want 2 and 5", f.count("/open-apis/search/v2/message"), calls.Load())
	}
	if f.count("/open-apis/drive/v1/files") != 0 {
		t.Error("the run went on after the refused token")
	}
	if len(res.Messages) != 1 || res.Cursors["im:oc_a"] == "" || res.Cursors["search"] != "" {
		t.Errorf("result = %+v", res)
	}

	// The token func's own error (the manager could not refresh) stops the run as it is.
	grant := &larksyncwire.Error{Kind: larksyncwire.ErrKindInvalidGrant, Op: "refresh"}
	_, err = s.Run(t.Context(), b, func(context.Context) (string, error) { return "", grant }, nil, newMemIndex())
	if !errors.As(err, &e) || e.Kind != larksyncwire.ErrKindInvalidGrant {
		t.Fatalf("err = %v, want the invalid grant", err)
	}
}

// The budget runs out on the syncer's clock: the run stops cleanly as Partial, with the cursors of what finished.
func TestBudgetStopsAsPartialWithFinishedCursorsOnly(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	f.step = 40 * time.Second
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Budget = time.Minute
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_a"}, map[string]any{"chat_id": "oc_b"},
	}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if c := r.URL.Query().Get("container_id"); c != "oc_a" {
			t.Errorf("read %s after the budget ran out", c)
		}
		writeData(w, map[string]any{"items": []any{textMsg("om_1", "oc_a", "ou_peer", "hi", testNow.Add(-time.Hour))}})
	})
	// Search and the documents are not answered: past the budget they must not be read.

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial {
		t.Fatal("not partial")
	}
	// The name book (members.go) keeps what the finished part learned.
	delete(res.Cursors, larksyncwire.SenderNamesCursor)
	if res.Cursors["im:oc_a"] == "" || res.Cursors["im:oc_b"] != "" || len(res.Cursors) != 1 {
		t.Errorf("cursors = %v, want only oc_a's", res.Cursors)
	}
	if len(res.Messages) != 1 || len(res.Errors) != 0 {
		t.Errorf("result = %+v", res)
	}
}

// A chat whose budget runs out between pages commits the pages already finished, advances its cursor to the newest
// committed message, and marks the run Partial so the next run resumes forward without livelock.
func TestBudgetInTheMiddleOfAChatCommitsCompletedPages(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	f.step = 40 * time.Second
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Budget = time.Minute
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	t1 := testNow.Add(-2 * time.Hour)
	t2 := testNow.Add(-time.Hour)
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "" {
			writeData(w, map[string]any{"items": []any{textMsg("om_1", "oc_a", "ou_peer", "page 1", t1)}, "has_more": true, "page_token": "p2"})
			return
		}
		writeData(w, map[string]any{"items": []any{textMsg("om_2", "oc_a", "ou_peer", "page 2", t2)}})
	})

	idx := newMemIndex()
	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want partial with no errors", res)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != "om_1" {
		t.Fatalf("messages = %+v, want completed page 1 (om_1)", res.Messages)
	}
	if got := res.Cursors["im:oc_a"]; got != msgTime(t1) {
		t.Fatalf("cursor im:oc_a = %q, want %q", got, msgTime(t1))
	}
	if idx.puts == 0 {
		t.Error("expected committed page 1 to be indexed")
	}

	// Next run with enough budget resumes from im:oc_a cursor and delivers page 2 without re-emitting om_1.
	f.step = 0
	b.Budget = 5 * time.Minute
	emptySearch(f)
	emptyDocs(f)
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query().Get("start_time"), strconv.FormatInt(t1.Unix()-300, 10); got != want {
			t.Errorf("run 2 start_time = %q, want %q", got, want)
		}
		writeData(w, map[string]any{"items": []any{
			textMsg("om_1", "oc_a", "ou_peer", "page 1", t1),
			textMsg("om_2", "oc_a", "ou_peer", "page 2", t2),
		}})
	})
	res2, err := s.Run(t.Context(), b, staticToken("tok"), res.Cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Partial || len(res2.Messages) != 1 || res2.Messages[0].ID != "om_2" {
		t.Fatalf("run 2 = %+v, want only new om_2", res2)
	}
	if got := res2.Cursors["im:oc_a"]; got != msgTime(t2) {
		t.Fatalf("run 2 cursor = %q, want %q", got, msgTime(t2))
	}
}

func TestMessageTextByType(t *testing.T) {
	msg := func(typ string, content any, mentions ...map[string]string) apiMessage {
		raw, _ := json.Marshal(content)
		var am apiMessage
		am.MsgType = typ
		am.Body.Content = string(raw)
		for _, m := range mentions {
			am.Mentions = append(am.Mentions, struct {
				Key    string `json:"key"`
				ID     string `json:"id"`
				IDType string `json:"id_type"`
				Name   string `json:"name"`
			}{Key: m["key"], ID: m["id"], IDType: m["id_type"], Name: m["name"]})
		}
		return am
	}
	cases := []struct {
		name  string
		am    apiMessage
		text  string
		files []larksyncwire.Attachment
	}{
		{"text with a mention", msg("text", map[string]string{"text": "hi @_user_1"}, map[string]string{"key": "@_user_1", "name": "Alex"}), "hi @Alex", nil},
		{"post", msg("post", map[string]any{"zh_cn": map[string]any{"title": "Plan", "content": []any{
			[]any{map[string]any{"tag": "text", "text": "see "}, map[string]any{"tag": "a", "text": "doc", "href": "https://x.feishu.cn/docx/doxA"}},
			[]any{map[string]any{"tag": "at", "user_name": "Sam"}, map[string]any{"tag": "img", "image_key": "img_1"}},
		}}}), "Plan\nsee doc (https://x.feishu.cn/docx/doxA)\n@Sam[image]", []larksyncwire.Attachment{{Ref: "img_1", Kind: "image"}}},
		{"image", msg("image", map[string]string{"image_key": "img_2"}), "[image]", []larksyncwire.Attachment{{Ref: "img_2", Kind: "image"}}},
		{"file", msg("file", map[string]string{"file_key": "file_1", "file_name": "a.pdf"}), "[file a.pdf]", []larksyncwire.Attachment{{Ref: "file_1", Name: "a.pdf", Kind: "file"}}},
		{"card", msg("interactive", map[string]any{"header": map[string]any{"title": map[string]any{"content": "Alert"}}, "elements": []any{map[string]any{"tag": "div", "text": map[string]any{"content": "disk full"}}}}), "Alert\ndisk full", nil},
		{"system", msg("system", map[string]string{}), "[system]", nil},
	}
	for _, c := range cases {
		text, files := messageText(c.am)
		if text != c.text || !slices.Equal(files, c.files) {
			t.Errorf("%s: %q %+v, want %q %+v", c.name, text, files, c.text, c.files)
		}
	}
}

// A message's attachments carry the full Ref, which Media reads back: the binding's own, with the user token.
func TestAttachmentsAndMedia(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	img := map[string]any{"message_id": "om_img", "chat_id": "oc_a", "msg_type": "image", "create_time": msgTime(testNow.Add(-time.Minute)),
		"sender": map[string]string{"id": "ou_peer"}, "body": map[string]string{"content": `{"image_key":"img_k"}`}}
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{img}})
	emptySearch(f)
	emptyDocs(f)
	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || len(res.Messages[0].Files) != 1 {
		t.Fatalf("messages = %+v", res.Messages)
	}
	ref := res.Messages[0].Files[0].Ref
	if ref != "primary~a1|om_img|img_k|image" {
		t.Fatalf("ref = %q", ref)
	}
	f.handle("/open-apis/im/v1/messages/om_img/resources/img_k", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "image" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("media request %s %v", r.URL, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})
	data, mime, err := s.Media(t.Context(), b, staticToken("tok"), ref, 1<<20)
	if err != nil || string(data) != "PNGDATA" || mime != "image/png" {
		t.Fatalf("media = %q %q %v", data, mime, err)
	}
	if _, _, err := s.Media(t.Context(), b, staticToken("tok"), ref, 3); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("over the limit: %v", err)
	}
	if _, _, err := s.Media(t.Context(), b, staticToken("tok"), "other~b|om_img|img_k|image", 1<<20); err == nil {
		t.Error("another binding's ref was fetched")
	}
}

// Senders get their display names (the gateway's copy of a message carries names, and the knowledge base matches on
// them); a single chat without a name is named after the other person; a refused name lookup stops the lookups for
// the run while reporting the missing permission without discarding readable messages.
func TestIMNamesSendersAndSingleChats(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	emptyDocs(f)
	f.data("/open-apis/search/v2/message", map[string]any{"items": []any{}})
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_p", "chat_mode": "p2p"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_p", "ou_peer", "hello", testNow.Add(-2*time.Hour)),
		textMsg("om_2", "oc_p", "ou_peer", "again", testNow.Add(-time.Hour)),
	}})
	res, err := s.Run(t.Context(), binding(f, "primary~a1", larksyncwire.TierFull), staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("messages = %+v", res.Messages)
	}
	for _, m := range res.Messages {
		if m.SenderName != "Name ou_peer" || m.ChatName != "Name ou_peer" {
			t.Errorf("message %s: sender %q chat %q", m.ID, m.SenderName, m.ChatName)
		}
	}
	if n := f.count("/open-apis/contact/v3/users/ou_peer"); n != 1 {
		t.Errorf("name looked up %d times, want once per run", n)
	}

	// Without the contact scope: names stay empty, one refused lookup, the messages still arrive.
	f2 := newFakeOpen(t, clk)
	emptyDocs(f2)
	f2.data("/open-apis/search/v2/message", map[string]any{"items": []any{}})
	f2.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_g", "name": "Team", "chat_mode": "group"}}})
	f2.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_3", "oc_g", "ou_a", "one", testNow.Add(-2*time.Hour)),
		textMsg("om_4", "oc_g", "ou_b", "two", testNow.Add(-time.Hour)),
	}})
	for _, id := range []string{"ou_a", "ou_b"} {
		f2.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			writeCode(w, http.StatusBadRequest, 99991672, "no permission")
		})
	}
	res, err = s.Run(t.Context(), binding(f2, "primary~a1", larksyncwire.TierFull), staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || len(res.Errors) != 1 || res.Errors[0].Resource != "sender_names" || res.Errors[0].Kind != larksyncwire.ErrKindScope {
		t.Fatalf("result = %+v", res)
	}
	if res.Cursors["im:oc_g"] == "" {
		t.Fatal("a missing name permission discarded the readable messages' cursor")
	}
	for _, m := range res.Messages {
		if m.SenderName != "" || m.ChatName != "Team" {
			t.Errorf("message %s: sender %q chat %q", m.ID, m.SenderName, m.ChatName)
		}
	}
	if n := f2.count("/open-apis/contact/v3/users/ou_a") + f2.count("/open-apis/contact/v3/users/ou_b"); n != 1 {
		t.Errorf("refused lookups = %d, want 1", n)
	}
}

// Bot/app senders (sender_type="app" or cli_* ids) and other non-user senders skip contact lookups completely, and a
// single user lookup failure (non-scope error or transient error) leaves only that sender unnamed without disabling
// name resolution for everyone else in the run.
func TestIMNameSendersSkipsBotsAndIsolatesPerSenderFailures(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	emptyDocs(f)
	emptySearch(f)

	msgWithType := func(id, chat, sender, senderType, text string, at time.Time) map[string]any {
		m := textMsg(id, chat, sender, text, at)
		senderMap := map[string]string{"id": sender}
		if senderType != "" {
			senderMap["sender_type"] = senderType
		}
		m["sender"] = senderMap
		return m
	}

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_mix", "name": "Example Group", "chat_mode": "group"},
		map[string]any{"chat_id": "oc_dm", "chat_mode": "p2p"},
	}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("container_id") {
		case "oc_mix":
			writeData(w, map[string]any{"items": []any{
				// Bot/app senders first (previously poisoned r.namesOff for the entire run).
				msgWithType("om_bot_app", "oc_mix", "cli_example_bot", "app", "deploy started", testNow.Add(-6*time.Minute)),
				msgWithType("om_bot_prefix", "oc_mix", "cli_example_legacy", "", "legacy alert", testNow.Add(-5*time.Minute)),
				msgWithType("om_anon", "oc_mix", "ou_anon", "anonymous", "anon note", testNow.Add(-4*time.Minute)),
				// Per-sender non-scope failures: must not disable lookups for later users.
				msgWithType("om_missing_1", "oc_mix", "ou_missing", "user", "first from missing", testNow.Add(-3*time.Minute)),
				msgWithType("om_missing_2", "oc_mix", "ou_missing", "user", "second from missing", testNow.Add(-2*time.Minute)),
				msgWithType("om_flaky", "oc_mix", "ou_flaky", "user", "transient fail", testNow.Add(-90*time.Second)),
				// Ordinary human user after all the above.
				msgWithType("om_alice", "oc_mix", "ou_alice", "user", "hello from alice", testNow.Add(-time.Minute)),
			}})
		case "oc_dm":
			writeData(w, map[string]any{"items": []any{
				msgWithType("om_bob", "oc_dm", "ou_bob", "user", "hello in dm", testNow.Add(-30*time.Second)),
			}})
		default:
			t.Errorf("unexpected container %q", r.URL.Query().Get("container_id"))
		}
	})
	f.handle("/open-apis/contact/v3/users/ou_missing", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 41001, "user not found")
	})
	f.handle("/open-apis/contact/v3/users/ou_flaky", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusInternalServerError, 0, "temporary error")
	})
	// Bot/non-user contact paths must never be called at all.
	for _, skip := range []string{"cli_example_bot", "cli_example_legacy", "ou_anon"} {
		id := skip
		f.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			t.Errorf("contact lookup called for non-user sender %s", id)
			writeCode(w, http.StatusBadRequest, 99992402, "field validation failed")
		})
	}

	res, err := s.Run(t.Context(), binding(f, "primary~a1", larksyncwire.TierFull), staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	wantNames := map[string]string{
		"om_bot_app":    "",
		"om_bot_prefix": "",
		"om_anon":       "",
		"om_missing_1":  "",
		"om_missing_2":  "",
		"om_flaky":      "",
		"om_alice":      "Name ou_alice",
		"om_bob":        "Name ou_bob",
	}
	if len(byID) != len(wantNames) {
		t.Fatalf("got %d messages, want %d", len(byID), len(wantNames))
	}
	for id, want := range wantNames {
		if got := byID[id].SenderName; got != want {
			t.Errorf("%s: SenderName = %q, want %q", id, got, want)
		}
	}
	if got := byID["om_bob"].ChatName; got != "Name ou_bob" {
		t.Errorf("dm ChatName = %q, want %q", got, "Name ou_bob")
	}
	if n := f.count("/open-apis/contact/v3/users/ou_missing"); n != 1 {
		t.Errorf("ou_missing looked up %d times, want 1 (cached miss)", n)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_alice"); n != 1 {
		t.Errorf("ou_alice looked up %d times, want 1", n)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_bob"); n != 1 {
		t.Errorf("ou_bob looked up %d times, want 1", n)
	}
}

// A missing contact scope disables further uncached lookups for the rest of the run while still serving names that
// were already cached earlier in the same run.
func TestIMNameSendersScopeErrorStopsRemainingLookupsButKeepsCache(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	emptyDocs(f)
	emptySearch(f)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_g", "name": "Example Group", "chat_mode": "group"},
	}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_g", "ou_ok", "first", testNow.Add(-4*time.Minute)),
		textMsg("om_2", "oc_g", "ou_denied", "second", testNow.Add(-3*time.Minute)),
		textMsg("om_3", "oc_g", "ou_never", "third", testNow.Add(-2*time.Minute)),
		textMsg("om_4", "oc_g", "ou_ok", "fourth", testNow.Add(-time.Minute)),
	}})
	f.handle("/open-apis/contact/v3/users/ou_denied", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 99991679, "missing contact scope")
	})

	res, err := s.Run(t.Context(), binding(f, "primary~a1", larksyncwire.TierFull), staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	if byID["om_1"].SenderName != "Name ou_ok" || byID["om_4"].SenderName != "Name ou_ok" {
		t.Errorf("cached user names: om_1=%q om_4=%q", byID["om_1"].SenderName, byID["om_4"].SenderName)
	}
	if byID["om_2"].SenderName != "" || byID["om_3"].SenderName != "" {
		t.Errorf("post-scope names: om_2=%q om_3=%q", byID["om_2"].SenderName, byID["om_3"].SenderName)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_never"); n != 0 {
		t.Errorf("ou_never looked up %d times after scope refusal, want 0", n)
	}
}

// Single-chat discovery searches with chat_type=p2p_chat across all pages so high-volume group chats cannot crowd p2p
// chats out, backfills each discovered p2p chat's full history via im/v1/messages with per-chat cursors, skips
// redundant message fetches for hits already read through that chat, labels every message p2p, and names the chat
// after the peer.
func TestP2PDiscoveryFiltersChatTypePaginatesBackfillsAndNamesPeer(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	emptyDocs(f)

	// im/v1/chats returns only the busy group chat, matching Feishu's user-token behavior.
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_busy", "name": "Busy Group", "chat_mode": "group"},
	}})

	var p2pTokens []string
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || body["query"] != "" || body["start_time"] != secs(testNow.Add(-48*time.Hour)) || body["end_time"] != secs(testNow) {
			t.Errorf("search %s %v", r.Method, body)
		}
		if body["chat_type"] == searchChatTypeP2P {
			tok := r.URL.Query().Get("page_token")
			p2pTokens = append(p2pTokens, tok)
			switch tok {
			case "":
				writeData(w, map[string]any{
					"items":      []string{"om_p1_recent", "om_p1_older", "om_p2_hit"},
					"has_more":   true,
					"page_token": "p2p_pg2",
				})
			case "p2p_pg2":
				writeData(w, map[string]any{
					"items":    []string{"om_p1_dup", "om_p3_hit"},
					"has_more": false,
				})
			default:
				t.Errorf("unexpected p2p page_token %q", tok)
			}
			return
		}
		// Unfiltered search is flooded by the busy group chat and never returns p2p messages.
		writeData(w, map[string]any{"items": []string{"om_busy_1"}})
	})

	// Only the first unseen hit per discovered p2p chat should need GET /im/v1/messages/:id; om_p1_older and om_p1_dup
	// are already read by readChat(oc_p1) and must not trigger extra get-message calls.
	f.data("/open-apis/im/v1/messages/om_p1_recent", map[string]any{"items": []any{textMsg("om_p1_recent", "oc_p1", "ou_alice", "latest from alice", testNow.Add(-10*time.Minute))}})
	f.data("/open-apis/im/v1/messages/om_p2_hit", map[string]any{"items": []any{textMsg("om_p2_hit", "oc_p2", testOwner, "ping bob", testNow.Add(-15*time.Minute))}})
	f.data("/open-apis/im/v1/messages/om_p3_hit", map[string]any{"items": []any{textMsg("om_p3_hit", "oc_p3", "ou_carol", "hi from carol", testNow.Add(-20*time.Minute))}})

	p1Root := textMsg("om_p1_older", "oc_p1", "ou_alice", "earlier topic", testNow.Add(-12*time.Hour))
	p1Root["thread_id"] = "omt_p1"
	p1Reply := textMsg("om_p1_reply", "oc_p1", testOwner, "thread reply", testNow.Add(-11*time.Hour))
	p1Reply["thread_id"] = "omt_p1"

	var histStarts = map[string]string{}
	var hmu sync.Mutex
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		hmu.Lock()
		histStarts[q.Get("container_id_type")+":"+q.Get("container_id")] = q.Get("start_time")
		hmu.Unlock()
		switch q.Get("container_id") {
		case "oc_busy":
			writeData(w, map[string]any{"items": []any{textMsg("om_busy_1", "oc_busy", "ou_spam", "group noise", testNow.Add(-5*time.Minute))}})
		case "oc_p1":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p1_hist", "oc_p1", "ou_alice", "missed by search", testNow.Add(-36*time.Hour)),
				p1Root,
				textMsg("om_p1_dup", "oc_p1", testOwner, "my follow up", testNow.Add(-2*time.Hour)),
				textMsg("om_p1_recent", "oc_p1", "ou_alice", "latest from alice", testNow.Add(-10*time.Minute)),
			}})
		case "omt_p1":
			writeData(w, map[string]any{"items": []any{p1Root, p1Reply}})
		case "oc_p2":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p2_hist", "oc_p2", "ou_bob", "bob earlier", testNow.Add(-24*time.Hour)),
				textMsg("om_p2_hit", "oc_p2", testOwner, "ping bob", testNow.Add(-15*time.Minute)),
			}})
		case "oc_p3":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p3_hist", "oc_p3", "ou_carol", "carol earlier", testNow.Add(-30*time.Hour)),
				textMsg("om_p3_hit", "oc_p3", "ou_carol", "hi from carol", testNow.Add(-20*time.Minute)),
			}})
		default:
			t.Errorf("unexpected container_id %q", q.Get("container_id"))
		}
	})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Partial {
		t.Fatalf("errors %+v partial %v", res.Errors, res.Partial)
	}
	if !slices.Equal(p2pTokens, []string{"", "p2p_pg2"}) {
		t.Errorf("p2p search pages = %v, want ['', 'p2p_pg2']", p2pTokens)
	}
	if f.count("/open-apis/im/v1/messages/om_p1_older") != 0 || f.count("/open-apis/im/v1/messages/om_p1_dup") != 0 {
		t.Errorf("redundant fetchMessage calls for already-read p2p chat oc_p1")
	}
	wantBackfill := secs(testNow.Add(-48 * time.Hour))
	for _, cid := range []string{"chat:oc_p1", "chat:oc_p2", "chat:oc_p3"} {
		if histStarts[cid] != wantBackfill {
			t.Errorf("%s backfill start = %q, want %q", cid, histStarts[cid], wantBackfill)
		}
	}

	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	wantPeers := map[string]string{
		"om_p1_hist":   "Name ou_alice",
		"om_p1_older":  "Name ou_alice",
		"om_p1_reply":  "Name ou_alice",
		"om_p1_dup":    "Name ou_alice",
		"om_p1_recent": "Name ou_alice",
		"om_p2_hist":   "Name ou_bob",
		"om_p2_hit":    "Name ou_bob",
		"om_p3_hist":   "Name ou_carol",
		"om_p3_hit":    "Name ou_carol",
	}
	for id, wantName := range wantPeers {
		m, ok := byID[id]
		if !ok {
			t.Errorf("missing p2p message %s", id)
			continue
		}
		if m.ChatType != "p2p" || m.ChatName != wantName {
			t.Errorf("%s: ChatType=%q ChatName=%q, want p2p / %q", id, m.ChatType, m.ChatName, wantName)
		}
		if !slices.Equal(entries(m.Sources), []string{b.Key + "/chat"}) {
			t.Errorf("%s sources = %v, want [%s/chat]", id, entries(m.Sources), b.Key)
		}
	}
	if got := res.Cursors["im:oc_p1"]; got != msgTime(testNow.Add(-10*time.Minute)) {
		t.Errorf("im:oc_p1 cursor = %q, want %q", got, msgTime(testNow.Add(-10*time.Minute)))
	}
	if got := res.Cursors["im:oc_p2"]; got != msgTime(testNow.Add(-15*time.Minute)) {
		t.Errorf("im:oc_p2 cursor = %q, want %q", got, msgTime(testNow.Add(-15*time.Minute)))
	}
	if got := res.Cursors["im:oc_p3"]; got != msgTime(testNow.Add(-20*time.Minute)) {
		t.Errorf("im:oc_p3 cursor = %q, want %q", got, msgTime(testNow.Add(-20*time.Minute)))
	}
	if got := res.Cursors[p2pSearchResource]; got != secs(testNow) {
		t.Errorf("search:p2p cursor = %q, want %q", got, secs(testNow))
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(res.Cursors[p2pChatsResource]), &saved); err != nil {
		t.Fatalf("p2p_chats cursor %q: %v", res.Cursors[p2pChatsResource], err)
	}
	wantSaved := map[string]string{"oc_p1": "Name ou_alice", "oc_p2": "Name ou_bob", "oc_p3": "Name ou_carol"}
	if len(saved) != len(wantSaved) {
		t.Fatalf("saved p2p_chats = %v, want %v", saved, wantSaved)
	}
	for k, v := range wantSaved {
		if saved[k] != v {
			t.Errorf("saved[%s] = %q, want %q", k, saved[k], v)
		}
	}
}

// Previously discovered p2p chats sync incrementally on subsequent runs directly through im/v1/messages (even when
// search fails or only the owner sent messages), keeping ChatType="p2p" and the remembered peer name, while newly
// discovered p2p chats backfill their full window.
func TestP2PKnownChatsSyncIncrementallyWithoutSearchAndKeepPeerName(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	emptyDocs(f)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{}})
	// Run 2: search fails, yet known oc_p1 still reads incrementally from its cursor and keeps peer name on an
	// owner-only message.
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 1, "search unavailable")
	})
	oldP1 := testNow.Add(-2 * time.Hour)
	var starts = map[string]string{}
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		starts[q.Get("container_id")] = q.Get("start_time")
		switch q.Get("container_id") {
		case "oc_p1":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p1_mine", "oc_p1", testOwner, "checking in", testNow.Add(-5*time.Minute)),
			}})
		default:
			t.Errorf("unexpected container %q", q.Get("container_id"))
		}
	})

	cursors := map[string]string{
		"im:oc_p1":        msgTime(oldP1),
		p2pChatsResource:  `{"oc_p1":"Name ou_alice"}`,
		p2pSearchResource: secs(oldP1),
		searchResource:    secs(oldP1),
	}
	res, err := s.Run(t.Context(), b, staticToken("tok"), cursors, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Resource != searchResource {
		t.Fatalf("errors = %+v, want only search error", res.Errors)
	}
	if want := strconv.FormatInt(oldP1.Unix()-300, 10); starts["oc_p1"] != want {
		t.Errorf("oc_p1 start = %q, want cursor less 5 min %q", starts["oc_p1"], want)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %+v, want 1", res.Messages)
	}
	if m := res.Messages[0]; m.ID != "om_p1_mine" || !m.Mine || m.ChatType != "p2p" || m.ChatName != "Name ou_alice" {
		t.Errorf("owner-only incremental p2p message = %+v", m)
	}
	if got := res.Cursors["im:oc_p1"]; got != msgTime(testNow.Add(-5*time.Minute)) {
		t.Errorf("im:oc_p1 cursor = %q", got)
	}

	// Run 3: search succeeds and discovers a brand new p2p chat oc_p2 while oc_p1 is quiet.
	clk.advance(time.Hour)
	run3Now := clk.now()
	nextCursors := map[string]string{
		"im:oc_p1":        res.Cursors["im:oc_p1"],
		p2pChatsResource:  `{"oc_p1":"Name ou_alice"}`,
		p2pSearchResource: secs(oldP1),
		searchResource:    secs(oldP1),
	}
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] == searchChatTypeP2P {
			if body["start_time"] != strconv.FormatInt(oldP1.Unix()-300, 10) {
				t.Errorf("p2p search start = %q, want %d", body["start_time"], oldP1.Unix()-300)
			}
			writeData(w, map[string]any{"items": []string{"om_p2_new"}})
			return
		}
		writeData(w, map[string]any{"items": []string{}})
	})
	f.data("/open-apis/im/v1/messages/om_p2_new", map[string]any{"items": []any{textMsg("om_p2_new", "oc_p2", "ou_bob", "hello", run3Now.Add(-2*time.Minute))}})
	starts = map[string]string{}
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		starts[q.Get("container_id")] = q.Get("start_time")
		switch q.Get("container_id") {
		case "oc_p1":
			writeData(w, map[string]any{"items": []any{}})
		case "oc_p2":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p2_earlier", "oc_p2", "ou_bob", "earlier", run3Now.Add(-20*time.Hour)),
				textMsg("om_p2_new", "oc_p2", "ou_bob", "hello", run3Now.Add(-2*time.Minute)),
			}})
		default:
			t.Errorf("unexpected container %q", q.Get("container_id"))
		}
	})
	res, err = s.Run(t.Context(), b, staticToken("tok"), nextCursors, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	if starts["oc_p2"] != secs(run3Now.Add(-48*time.Hour)) {
		t.Errorf("new p2p oc_p2 start = %q, want full backfill %q", starts["oc_p2"], secs(run3Now.Add(-48*time.Hour)))
	}
	if len(res.Messages) != 2 {
		t.Fatalf("messages = %+v, want 2 from oc_p2", res.Messages)
	}
	for _, m := range res.Messages {
		if m.ChatType != "p2p" || m.ChatName != "Name ou_bob" {
			t.Errorf("oc_p2 message = %+v", m)
		}
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(res.Cursors[p2pChatsResource]), &saved); err != nil {
		t.Fatalf("p2p_chats = %q: %v", res.Cursors[p2pChatsResource], err)
	}
	if saved["oc_p1"] != "Name ou_alice" || saved["oc_p2"] != "Name ou_bob" {
		t.Errorf("saved p2p_chats = %v", saved)
	}
}

// A single chat whose window contains only the owner's own messages (no peer message to infer a name from) falls back
// to GET /open-apis/im/v1/chats/:chat_id (either its name or p2p_target_id -> contact lookup).
func TestP2POneWayDMFallsBackToChatMetadataForPeerName(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	emptyDocs(f)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] == searchChatTypeP2P {
			writeData(w, map[string]any{"items": []string{"om_bot_dm", "om_user_dm"}})
			return
		}
		writeData(w, map[string]any{"items": []string{}})
	})
	f.data("/open-apis/im/v1/messages/om_bot_dm", map[string]any{"items": []any{textMsg("om_bot_dm", "oc_bot_dm", testOwner, "/status", testNow.Add(-10*time.Minute))}})
	f.data("/open-apis/im/v1/messages/om_user_dm", map[string]any{"items": []any{textMsg("om_user_dm", "oc_user_dm", testOwner, "happy birthday", testNow.Add(-5*time.Minute))}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("container_id") {
		case "oc_bot_dm":
			writeData(w, map[string]any{"items": []any{textMsg("om_bot_dm", "oc_bot_dm", testOwner, "/status", testNow.Add(-10*time.Minute))}})
		case "oc_user_dm":
			writeData(w, map[string]any{"items": []any{textMsg("om_user_dm", "oc_user_dm", testOwner, "happy birthday", testNow.Add(-5*time.Minute))}})
		default:
			t.Errorf("unexpected container %q", r.URL.Query().Get("container_id"))
		}
	})
	f.data("/open-apis/im/v1/chats/oc_bot_dm", map[string]any{"chat_mode": "p2p", "name": "Deploy Bot"})
	f.data("/open-apis/im/v1/chats/oc_user_dm", map[string]any{"chat_mode": "p2p", "name": "", "p2p_target_id": "ou_dave", "p2p_target_type": "user"})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	if m := byID["om_bot_dm"]; m.ChatType != "p2p" || m.ChatName != "Deploy Bot" {
		t.Errorf("bot dm = %+v", m)
	}
	if m := byID["om_user_dm"]; m.ChatType != "p2p" || m.ChatName != "Name ou_dave" {
		t.Errorf("user dm = %+v", m)
	}
}

// A discovered p2p chat whose history read fails (for example missing im:message.p2p_msg:get_as_user) fails only its
// own resource while still being remembered for the next run, other p2p chats succeed, and a known group chat is never
// reclassified as p2p.
func TestP2PDiscoveryIsolatesFailingChatAndProtectsGroupChatType(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	emptyDocs(f)

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_group", "name": "Eng", "chat_mode": "group"},
	}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] == searchChatTypeP2P {
			writeData(w, map[string]any{"items": []string{"om_group_unseen", "om_p2p_bad", "om_p2p_ok"}})
			return
		}
		writeData(w, map[string]any{"items": []string{}})
	})
	f.data("/open-apis/im/v1/messages/om_group_unseen", map[string]any{"items": []any{textMsg("om_group_unseen", "oc_group", "ou_x", "late", testNow.Add(-time.Minute))}})
	f.data("/open-apis/im/v1/messages/om_p2p_bad", map[string]any{"items": []any{textMsg("om_p2p_bad", "oc_p2p_bad", "ou_bad", "denied", testNow.Add(-2*time.Minute))}})
	f.data("/open-apis/im/v1/messages/om_p2p_ok", map[string]any{"items": []any{textMsg("om_p2p_ok", "oc_p2p_ok", "ou_ok", "hello", testNow.Add(-3*time.Minute))}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("container_id") {
		case "oc_group":
			writeData(w, map[string]any{"items": []any{textMsg("om_g1", "oc_group", "ou_x", "in eng", testNow.Add(-10*time.Minute))}})
		case "oc_p2p_bad":
			writeCode(w, http.StatusBadRequest, 99991672, "missing im:message.p2p_msg:get_as_user")
		case "oc_p2p_ok":
			writeData(w, map[string]any{"items": []any{textMsg("om_p2p_ok", "oc_p2p_ok", "ou_ok", "hello", testNow.Add(-3*time.Minute))}})
		default:
			t.Errorf("unexpected container %q", r.URL.Query().Get("container_id"))
		}
	})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Resource != "im:oc_p2p_bad" || res.Errors[0].Kind != larksyncwire.ErrKindScope {
		t.Fatalf("errors = %+v", res.Errors)
	}
	if _, moved := res.Cursors["im:oc_p2p_bad"]; moved {
		t.Errorf("failed p2p chat cursor moved: %v", res.Cursors)
	}
	if res.Cursors["im:oc_p2p_ok"] == "" {
		t.Errorf("oc_p2p_ok cursor missing: %v", res.Cursors)
	}
	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	if m := byID["om_g1"]; m.ChatType != "group" || m.ChatName != "Eng" {
		t.Errorf("group message = %+v", m)
	}
	if m := byID["om_p2p_ok"]; m.ChatType != "p2p" || m.ChatName != "Name ou_ok" {
		t.Errorf("p2p ok message = %+v", m)
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(res.Cursors[p2pChatsResource]), &saved); err != nil {
		t.Fatalf("p2p_chats = %q: %v", res.Cursors[p2pChatsResource], err)
	}
	if _, leaked := saved["oc_group"]; leaked {
		t.Errorf("group chat leaked into p2p_chats: %v", saved)
	}
	if _, remembered := saved["oc_p2p_bad"]; !remembered {
		t.Errorf("failed p2p chat not remembered for retry: %v", saved)
	}
	if saved["oc_p2p_ok"] != "Name ou_ok" {
		t.Errorf("oc_p2p_ok name = %q", saved["oc_p2p_ok"])
	}
}

// ModeIM hitting a large backlog topic group commits completed pages/threads up to the per-chat cap, advances its
// cursor, continues forward on the next run without livelocking, and never starves subsequent group chats, known p2p
// chats, p2p discovery, or linked cloud docs.
func TestModeIMLargeTopicBacklogAdvancesCursorAndNeverStarvesOtherChatsOrSteps(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute

	// 8 pages of a large backlog topic group (exceeds imChatPagesMost=5) plus a second group chat and a p2p chat
	// discovered via search:p2p and a cloud doc link in the second group chat.
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_big", "name": "Backlog Topic", "chat_mode": "topic"},
		map[string]any{"chat_id": "oc_after", "name": "Later Group", "chat_mode": "group"},
	}})

	baseAt := testNow.Add(-10 * time.Hour)
	bigMsgs := make([]map[string]any, 8)
	bigReplies := map[string][]any{}
	for i := range bigMsgs {
		at := baseAt.Add(time.Duration(i+1) * time.Hour)
		id := "om_big_" + strconv.Itoa(i+1)
		th := "omt_big_" + strconv.Itoa(i+1)
		m := textMsg(id, "oc_big", "ou_poster", "topic "+strconv.Itoa(i+1), at)
		m["thread_id"] = th
		bigMsgs[i] = m
		rep := textMsg("om_rep_"+strconv.Itoa(i+1), "oc_big", "ou_reply", "reply "+strconv.Itoa(i+1), at.Add(time.Minute))
		rep["thread_id"] = th
		bigReplies[th] = []any{m, rep}
	}
	afterAt := testNow.Add(-5 * time.Minute)
	p2pAt := testNow.Add(-3 * time.Minute)

	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		cid := q.Get("container_id")
		switch {
		case cid == "oc_big":
			startSec, _ := strconv.ParseInt(q.Get("start_time"), 10, 64)
			var filtered []map[string]any
			for _, m := range bigMsgs {
				ms := parseUnix(m["create_time"].(string))
				if ms/1000 >= startSec {
					filtered = append(filtered, m)
				}
			}
			idx := 0
			if tok := q.Get("page_token"); tok != "" {
				idx, _ = strconv.Atoi(tok)
			}
			if idx >= len(filtered) {
				writeData(w, map[string]any{"items": []any{}})
				return
			}
			hasMore := idx+1 < len(filtered)
			nextTok := ""
			if hasMore {
				nextTok = strconv.Itoa(idx + 1)
			}
			writeData(w, map[string]any{"items": []any{filtered[idx]}, "has_more": hasMore, "page_token": nextTok})
		case strings.HasPrefix(cid, "omt_big_"):
			writeData(w, map[string]any{"items": bigReplies[cid]})
		case cid == "oc_after":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_after_1", "oc_after", "ou_other", "doc: https://my.feishu.cn/file/boxAfter1", afterAt),
			}})
		case cid == "oc_p2p_new":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p2p_1", "oc_p2p_new", "ou_friend", "hello dm", p2pAt),
			}})
		default:
			t.Errorf("unexpected container %q", cid)
		}
	})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] != searchChatTypeP2P {
			t.Errorf("ModeIM unexpected search body %v", body)
		}
		writeData(w, map[string]any{"items": []string{"om_p2p_1"}})
	})
	f.data("/open-apis/im/v1/messages/om_p2p_1", map[string]any{"items": []any{
		textMsg("om_p2p_1", "oc_p2p_new", "ou_friend", "hello dm", p2pAt),
	}})
	f.data("/open-apis/drive/v1/metas/batch_query", map[string]any{"metas": []any{
		map[string]any{"doc_token": "boxAfter1", "doc_type": "file", "title": "notes.md", "latest_modify_time": secs(afterAt), "url": "https://my.feishu.cn/file/boxAfter1"},
	}})
	f.handle("/open-apis/drive/v1/files/boxAfter1/download", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("linked doc body"))
	})

	idx := newMemIndex()
	cursors := map[string]string{
		"im:oc_big":   msgTime(baseAt),
		"im:oc_after": msgTime(testNow.Add(-15 * time.Minute)),
	}

	// Run 1: oc_big hits imChatPagesMost (5 pages) and commits pages 1..5 + their replies; oc_after, readP2PDiscovery,
	// and readLinkedDocs ALL still run in the same round!
	res1, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Errors) != 0 {
		t.Fatalf("run 1 errors = %+v", res1.Errors)
	}
	wantCur1 := msgTime(baseAt.Add(time.Duration(imChatPagesMost) * time.Hour))
	if got := res1.Cursors["im:oc_big"]; got != wantCur1 {
		t.Fatalf("run 1 im:oc_big cursor = %q, want %q", got, wantCur1)
	}
	byID1 := map[string]larksyncwire.Message{}
	for _, m := range res1.Messages {
		byID1[m.ID] = m
	}
	for i := 1; i <= imChatPagesMost; i++ {
		if _, ok := byID1["om_big_"+strconv.Itoa(i)]; !ok {
			t.Errorf("run 1 missing om_big_%d", i)
		}
		if _, ok := byID1["om_rep_"+strconv.Itoa(i)]; !ok {
			t.Errorf("run 1 missing om_rep_%d", i)
		}
	}
	if _, ok := byID1["om_after_1"]; !ok {
		t.Errorf("run 1 starved later group chat oc_after")
	}
	if _, ok := byID1["om_p2p_1"]; !ok {
		t.Errorf("run 1 starved readP2PDiscovery")
	}
	if len(res1.Docs) != 1 || res1.Docs[0].ObjToken != "boxAfter1" || res1.Docs[0].Body != "linked doc body" {
		t.Fatalf("run 1 starved readLinkedDocs: docs = %+v", res1.Docs)
	}

	// Run 2: merges cursors and continues forward on oc_big to read pages 6..8!
	nextCursors := map[string]string{}
	for k, v := range cursors {
		nextCursors[k] = v
	}
	for k, v := range res1.Cursors {
		nextCursors[k] = v
	}
	clk.advance(15 * time.Minute)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), nextCursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	wantCur2 := msgTime(baseAt.Add(8 * time.Hour))
	if got := res2.Cursors["im:oc_big"]; got != wantCur2 {
		t.Fatalf("run 2 im:oc_big cursor = %q, want %q", got, wantCur2)
	}
	byID2 := map[string]larksyncwire.Message{}
	for _, m := range res2.Messages {
		byID2[m.ID] = m
	}
	for i := imChatPagesMost + 1; i <= 8; i++ {
		if _, ok := byID2["om_big_"+strconv.Itoa(i)]; !ok {
			t.Errorf("run 2 missing om_big_%d", i)
		}
		if _, ok := byID2["om_rep_"+strconv.Itoa(i)]; !ok {
			t.Errorf("run 2 missing om_rep_%d", i)
		}
	}
}

// Even when readIM exhausts its 60% budget slice in ModeIM on a slow chat, readP2PDiscovery and readLinkedDocs still
// execute inside their reserved slices, and the next ModeIM round rotates past the slow chat via im:rr so subsequent
// chats are read first.
func TestModeIMReservedSlicesAndRoundRobinRotation(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	// Each API call advances 1 minute; total budget 5 minutes -> readIM slice is 3 minutes (60%).
	f.step = time.Minute
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_slow", "name": "Slow Group", "chat_mode": "group"},
		map[string]any{"chat_id": "oc_next", "name": "Next Group", "chat_mode": "group"},
	}})
	tSlow1 := testNow.Add(-5 * time.Hour)
	tSlow2 := testNow.Add(-4 * time.Hour)
	tNext := testNow.Add(-10 * time.Minute)
	tP2P := testNow.Add(-5 * time.Minute)

	var seenOrder []string
	var omu sync.Mutex
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		cid := q.Get("container_id")
		omu.Lock()
		seenOrder = append(seenOrder, cid)
		omu.Unlock()
		switch cid {
		case "oc_slow":
			if q.Get("page_token") == "" {
				writeData(w, map[string]any{
					"items":      []any{textMsg("om_s1", "oc_slow", "ou_a", "slow p1 https://my.feishu.cn/file/boxSlow1", tSlow1)},
					"has_more":   true,
					"page_token": "p2",
				})
				return
			}
			writeData(w, map[string]any{"items": []any{textMsg("om_s2", "oc_slow", "ou_a", "slow p2", tSlow2)}})
		case "oc_next":
			writeData(w, map[string]any{"items": []any{textMsg("om_n1", "oc_next", "ou_b", "next msg", tNext)}})
		case "oc_p2p":
			writeData(w, map[string]any{"items": []any{textMsg("om_p1", "oc_p2p", "ou_c", "p2p msg", tP2P)}})
		default:
			t.Errorf("unexpected container %q", cid)
		}
	})
	p2pRan := atomic.Bool{}
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, _ *http.Request) {
		p2pRan.Store(true)
		writeData(w, map[string]any{"items": []string{}})
	})
	linkedMetaRan := atomic.Bool{}
	f.handle("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, _ *http.Request) {
		linkedMetaRan.Store(true)
		writeData(w, map[string]any{"metas": []any{
			map[string]any{"doc_token": "boxSlow1", "doc_type": "file", "title": "info.bin", "latest_modify_time": secs(tSlow1)},
		}})
	})

	idx := newMemIndex()
	// Run 1: listChats (1m) + oc_slow p1 (2m) + oc_slow p2 (3m -> hits readIM 60% slice = 3m!).
	// readIM stops before oc_next and saves im:rr="oc_slow"; readP2PDiscovery runs at t=3m..4m; readLinkedDocs at t=4m..5m!
	res1, err := s.Run(t.Context(), b, staticToken("tok"), map[string]string{
		"im:oc_slow": msgTime(testNow.Add(-6 * time.Hour)),
		"im:oc_next": msgTime(testNow.Add(-20 * time.Minute)),
	}, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Partial {
		t.Fatalf("expected run 1 to be partial after readIM hit its slice")
	}
	if !p2pRan.Load() {
		t.Fatalf("readP2PDiscovery was starved when readIM hit its 60%% budget slice")
	}
	if !linkedMetaRan.Load() {
		t.Fatalf("readLinkedDocs was starved when readIM hit its 60%% budget slice")
	}
	if got := res1.Cursors[imRoundRobinCursor]; got != "oc_slow" {
		t.Fatalf("im:rr = %q, want oc_slow", got)
	}

	// Run 2: even though oc_slow STILL has an older cursor than oc_next, im:rr="oc_slow" rotates oc_next ahead of oc_slow.
	clk.advance(15 * time.Minute)
	f.step = 0
	omu.Lock()
	seenOrder = nil
	omu.Unlock()
	res2, err := s.Run(t.Context(), b, staticToken("tok"), map[string]string{
		"im:oc_slow":       res1.Cursors["im:oc_slow"],
		"im:oc_next":       msgTime(testNow.Add(-20 * time.Minute)),
		imRoundRobinCursor: res1.Cursors[imRoundRobinCursor],
	}, idx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Partial {
		t.Fatalf("run 2 unexpectedly partial")
	}
	omu.Lock()
	order := append([]string(nil), seenOrder...)
	omu.Unlock()
	if len(order) < 2 || order[0] != "oc_next" {
		t.Fatalf("run 2 chat order = %v, want oc_next first due to round-robin rotation past oc_slow", order)
	}
}

// ResolveUserNames skips non-user/bot ids, deduplicates ids, ignores per-user lookup failures, and reports scope errors.
func TestResolveUserNames(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	f.handle("/open-apis/contact/v3/users/ou_missing", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 41001, "not found")
	})
	f.handle("/open-apis/contact/v3/users/cli_bot", func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("contact lookup must skip cli_* bots")
		writeCode(w, http.StatusBadRequest, 99992402, "invalid")
	})

	names, err := s.ResolveUserNames(t.Context(), b, staticToken("tok"), []string{"cli_bot", "ou_a", "ou_a", "ou_missing", "ou_b"})
	if err != nil {
		t.Fatal(err)
	}
	if names["ou_a"] != "Name ou_a" || names["ou_b"] != "Name ou_b" || len(names) != 2 {
		t.Fatalf("names = %v, want ou_a and ou_b", names)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_a"); n != 1 {
		t.Errorf("ou_a looked up %d times, want 1", n)
	}

	// Missing contact scope returns ErrKindScope alongside already-resolved names.
	f.handle("/open-apis/contact/v3/users/ou_denied", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 99991679, "missing scope")
	})
	names, err = s.ResolveUserNames(t.Context(), b, staticToken("tok"), []string{"ou_a", "ou_denied", "ou_never"})
	if e := asSyncError(err); e == nil || e.Kind != larksyncwire.ErrKindScope {
		t.Fatalf("err = %v, want ErrKindScope", err)
	}
	if names["ou_a"] != "Name ou_a" || f.count("/open-apis/contact/v3/users/ou_never") != 0 {
		t.Fatalf("after scope error: names=%v neverCount=%d", names, f.count("/open-apis/contact/v3/users/ou_never"))
	}
}

// A ModeIM topic chat with a multi-page thread (exceeding imThreadPagesMost=3) commits the completed thread pages,
// records im_thread:<th>, and resumes the remaining replies on the next run; similarly, a burst of >imChatThreadsMost
// topics within the 5-minute overlap skips already-indexed overlap roots on the next run so it never livelocks.
func TestModeIMThreadPaginationResumeAndOverlapBurstProgress(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_th", "name": "Mega Thread", "chat_mode": "topic"},
	}})
	f.data("/open-apis/search/v2/message", map[string]any{"items": []string{}})

	baseAt := testNow.Add(-2 * time.Hour)
	root := textMsg("om_root", "oc_th", "ou_a", "root topic", baseAt)
	root["thread_id"] = "omt_mega"
	// 5 pages of replies (1 reply per page), exceeding imThreadPagesMost=3.
	threadPages := make([]map[string]any, 5)
	for i := range threadPages {
		m := textMsg("om_tr_"+strconv.Itoa(i+1), "oc_th", "ou_b", "reply "+strconv.Itoa(i+1), baseAt.Add(time.Duration(i+1)*10*time.Minute))
		m["thread_id"] = "omt_mega"
		threadPages[i] = m
	}

	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("container_id") {
		case "oc_th":
			writeData(w, map[string]any{"items": []any{root}})
		case "omt_mega":
			startSec, _ := strconv.ParseInt(q.Get("start_time"), 10, 64)
			var filtered []map[string]any
			for _, m := range threadPages {
				if parseUnix(m["create_time"].(string))/1000 >= startSec {
					filtered = append(filtered, m)
				}
			}
			idx := 0
			if tok := q.Get("page_token"); tok != "" {
				idx, _ = strconv.Atoi(tok)
			}
			if idx >= len(filtered) {
				writeData(w, map[string]any{"items": []any{}})
				return
			}
			hasMore := idx+1 < len(filtered)
			nextTok := ""
			if hasMore {
				nextTok = strconv.Itoa(idx + 1)
			}
			writeData(w, map[string]any{"items": []any{filtered[idx]}, "has_more": hasMore, "page_token": nextTok})
		default:
			t.Errorf("unexpected container %q", q.Get("container_id"))
		}
	})

	idx := newMemIndex()
	res1, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	wantThCur := msgTime(baseAt.Add(time.Duration(imThreadPagesMost) * 10 * time.Minute))
	if got := res1.Cursors[threadCursorStem+"omt_mega"]; got != wantThCur {
		t.Fatalf("run 1 thread cursor = %q, want %q", got, wantThCur)
	}
	byID1 := map[string]bool{}
	for _, m := range res1.Messages {
		byID1[m.ID] = true
	}
	for _, want := range []string{"om_root", "om_tr_1", "om_tr_2", "om_tr_3"} {
		if !byID1[want] {
			t.Errorf("run 1 missing %s", want)
		}
	}

	// Run 2 resumes omt_mega from im_thread:omt_mega, delivers om_tr_4 & om_tr_5, marks im_thread:omt_mega "done",
	// and advances im:oc_th.
	clk.advance(15 * time.Minute)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), res1.Cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if got := res2.Cursors[threadCursorStem+"omt_mega"]; got != "done" {
		t.Fatalf("run 2 thread cursor = %q, want done", got)
	}
	if got := res2.Cursors["im:oc_th"]; got != msgTime(baseAt) {
		t.Fatalf("run 2 chat cursor = %q, want %q", got, msgTime(baseAt))
	}
	byID2 := map[string]bool{}
	for _, m := range res2.Messages {
		byID2[m.ID] = true
	}
	if !byID2["om_tr_4"] || !byID2["om_tr_5"] {
		t.Fatalf("run 2 missing remaining replies: %v", byID2)
	}
}

// A transient error on a later thread in the same chat page must not persist an earlier thread's
// im_thread:<th> cursor while readChat discards the uncommitted thread replies; otherwise the next
// run skips the already-indexed overlap root and permanently loses those replies.
func TestReadChatThreadErrorDoesNotPrematurelyCommitThreadCursor(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_c", "name": "Topic Chat", "chat_mode": "topic"},
	}})
	f.data("/open-apis/search/v2/message", map[string]any{"items": []string{}})

	baseAt := testNow.Add(-2 * time.Hour)
	// om_0 precedes om_1 at the same timestamp so run 1 advances im:oc_c to baseAt while om_1's
	// thread stops partway at imThreadPagesMost=3.
	msg0 := textMsg("om_0", "oc_c", "ou_a", "preamble", baseAt)
	root1 := textMsg("om_1", "oc_c", "ou_a", "thread one root", baseAt)
	root1["thread_id"] = "omt_1"
	root2 := textMsg("om_2", "oc_c", "ou_a", "thread two root", baseAt.Add(2*time.Minute))
	root2["thread_id"] = "omt_2"

	th1Pages := make([]map[string]any, 5)
	for i := range th1Pages {
		m := textMsg("om_r"+strconv.Itoa(i+1), "oc_c", "ou_b", "reply "+strconv.Itoa(i+1), baseAt.Add(time.Duration(i+1)*10*time.Minute))
		m["thread_id"] = "omt_1"
		th1Pages[i] = m
	}
	th2Reply := textMsg("om_2_r1", "oc_c", "ou_b", "thread two reply", baseAt.Add(3*time.Minute))
	th2Reply["thread_id"] = "omt_2"

	includeSecondThread := false
	failSecondThread := false
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("container_id") {
		case "oc_c":
			items := []any{msg0, root1}
			if includeSecondThread {
				items = append(items, root2)
			}
			writeData(w, map[string]any{"items": items})
		case "omt_1":
			startSec, _ := strconv.ParseInt(q.Get("start_time"), 10, 64)
			var filtered []map[string]any
			for _, m := range th1Pages {
				if parseUnix(m["create_time"].(string))/1000 >= startSec {
					filtered = append(filtered, m)
				}
			}
			idx := 0
			if tok := q.Get("page_token"); tok != "" {
				idx, _ = strconv.Atoi(tok)
			}
			if idx >= len(filtered) {
				writeData(w, map[string]any{"items": []any{}})
				return
			}
			hasMore := idx+1 < len(filtered)
			nextTok := ""
			if hasMore {
				nextTok = strconv.Itoa(idx + 1)
			}
			writeData(w, map[string]any{"items": []any{filtered[idx]}, "has_more": hasMore, "page_token": nextTok})
		case "omt_2":
			if failSecondThread {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"code":99991400,"msg":"temporary error"}`))
				return
			}
			writeData(w, map[string]any{"items": []any{th2Reply}})
		default:
			t.Errorf("unexpected container %q", q.Get("container_id"))
		}
	})

	idx := newMemIndex()
	cursors := map[string]string{}
	mergeCursors := func(moved map[string]string) {
		for k, v := range moved {
			cursors[k] = v
		}
	}

	// Run 1: reads om_0, om_1, and first 3 pages of omt_1; persists im:oc_c and im_thread:omt_1.
	res1, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	mergeCursors(res1.Cursors)
	wantThCur := msgTime(baseAt.Add(time.Duration(imThreadPagesMost) * 10 * time.Minute))
	if got := cursors[threadCursorStem+"omt_1"]; got != wantThCur {
		t.Fatalf("run 1 thread cursor = %q, want %q", got, wantThCur)
	}

	// Run 2: omt_1 completes (reading om_r4 & om_r5), then omt_2 fails transiently with HTTP 500.
	// readChat discards all staged messages; it must NOT advance im_thread:omt_1 to "done".
	includeSecondThread = true
	failSecondThread = true
	clk.advance(15 * time.Minute)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Errors) == 0 {
		t.Fatal("run 2 expected chat error when omt_2 returns 500")
	}
	for _, m := range res2.Messages {
		if m.ID == "om_r4" || m.ID == "om_r5" {
			t.Fatalf("run 2 unexpectedly committed message %s from failed chat", m.ID)
		}
	}
	if moved, ok := res2.Cursors[threadCursorStem+"omt_1"]; ok {
		t.Fatalf("run 2 prematurely moved %s to %q despite failed readChat", threadCursorStem+"omt_1", moved)
	}
	mergeCursors(res2.Cursors)

	// Run 3: transient error clears. Both omt_1's remaining replies (om_r4, om_r5) and omt_2 must be delivered.
	failSecondThread = false
	clk.advance(15 * time.Minute)
	res3, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Errors) != 0 {
		t.Fatalf("run 3 unexpected errors: %+v", res3.Errors)
	}
	byID3 := map[string]bool{}
	for _, m := range res3.Messages {
		byID3[m.ID] = true
	}
	for _, want := range []string{"om_r4", "om_r5", "om_2", "om_2_r1"} {
		if !byID3[want] {
			t.Errorf("run 3 lost message %s (got %v)", want, byID3)
		}
	}
	if got := res3.Cursors[threadCursorStem+"omt_1"]; got != "done" {
		t.Fatalf("run 3 thread cursor = %q, want done", got)
	}
}

// A transient error fetching page 2 of a chat must not commit im_thread:<th> cursors from page 1.
func TestReadChatNextPageErrorDoesNotPrematurelyCommitThreadCursor(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	b.Budget = 5 * time.Minute

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_p", "name": "Multi Page Chat", "chat_mode": "topic"},
	}})
	f.data("/open-apis/search/v2/message", map[string]any{"items": []string{}})

	baseAt := testNow.Add(-2 * time.Hour)
	root1 := textMsg("om_p1_root", "oc_p", "ou_a", "page 1 thread", baseAt)
	root1["thread_id"] = "omt_p1"
	replyLate := textMsg("om_p1_late", "oc_p", "ou_b", "page 1 late reply", baseAt.Add(40*time.Minute))
	replyLate["thread_id"] = "omt_p1"
	page2Msg := textMsg("om_p2_msg", "oc_p", "ou_a", "page 2 message", baseAt.Add(50*time.Minute))

	failPage2 := true
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("container_id") {
		case "oc_p":
			if q.Get("page_token") == "" {
				writeData(w, map[string]any{"items": []any{root1}, "has_more": true, "page_token": "p2"})
				return
			}
			if failPage2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"code":99991400,"msg":"page 2 error"}`))
				return
			}
			writeData(w, map[string]any{"items": []any{page2Msg}})
		case "omt_p1":
			writeData(w, map[string]any{"items": []any{replyLate}})
		default:
			t.Errorf("unexpected container %q", q.Get("container_id"))
		}
	})

	idx := newMemIndex()
	cursors := map[string]string{
		"im:oc_p":                   msgTime(baseAt),
		threadCursorStem + "omt_p1": msgTime(baseAt.Add(30 * time.Minute)),
	}

	// Run 1: page 1 finishes omt_p1, then page 2 fails with 500. Neither om_p1_late nor im_thread:omt_p1="done" may commit.
	res1, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Errors) == 0 {
		t.Fatal("run 1 expected page 2 error")
	}
	if moved, ok := res1.Cursors[threadCursorStem+"omt_p1"]; ok {
		t.Fatalf("run 1 prematurely moved %s to %q despite page 2 error", threadCursorStem+"omt_p1", moved)
	}
	for k, v := range res1.Cursors {
		cursors[k] = v
	}

	// Run 2: page 2 succeeds; om_p1_late and om_p2_msg are both delivered and im_thread:omt_p1 becomes "done".
	failPage2 = false
	clk.advance(15 * time.Minute)
	res2, err := s.Run(t.Context(), b, staticToken("tok"), cursors, idx)
	if err != nil {
		t.Fatal(err)
	}
	byID2 := map[string]bool{}
	for _, m := range res2.Messages {
		byID2[m.ID] = true
	}
	if !byID2["om_p1_late"] || !byID2["om_p2_msg"] {
		t.Fatalf("run 2 missing messages: %v", byID2)
	}
	if got := res2.Cursors[threadCursorStem+"omt_p1"]; got != "done" {
		t.Fatalf("run 2 thread cursor = %q, want done", got)
	}
}

// In a personal 1-person tenant where contact/v3 cannot see external users, live sync falls back to per-chat cached
// GET /open-apis/im/v1/chats/:chat_id/members, p2p chat info + members, and message-carried names (mentions / sender
// name / post @-nodes), naming both senders and single chats without repeating failed contact/v3 or members calls.
func TestIMExternalGroupAndP2PNameFallbacksCachedPerChat(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	emptyDocs(f)

	// contact/v3 cannot see any external user in a 1-person personal tenant.
	for _, id := range []string{"ou_ext1", "ou_ext2", "ou_ext3", "ou_left", "ou_p2p_peer"} {
		f.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			writeCode(w, http.StatusBadRequest, 41001, "user not visible in personal tenant")
		})
	}

	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{
		map[string]any{"chat_id": "oc_ext_grp", "name": "External Group", "chat_mode": "group"},
	}})
	f.handle("/open-apis/search/v2/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_type"] == searchChatTypeP2P {
			writeData(w, map[string]any{"items": []string{"om_p2p_human", "om_p2p_bot"}})
			return
		}
		writeData(w, map[string]any{"items": []string{}})
	})

	// Paginated group members list for oc_ext_grp (2 pages).
	f.handle("/open-apis/im/v1/chats/oc_ext_grp/members", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("member_id_type") != "open_id" {
			t.Errorf("member_id_type = %q, want open_id", r.URL.Query().Get("member_id_type"))
		}
		if r.URL.Query().Get("page_token") == "" {
			writeData(w, map[string]any{
				"items":      []any{map[string]any{"member_id": "ou_ext1", "name": "External One"}, map[string]any{"member_id": "ou_ext2", "name": "External Two"}},
				"has_more":   true,
				"page_token": "m2",
			})
			return
		}
		writeData(w, map[string]any{
			"items":    []any{map[string]any{"member_id": "ou_ext3", "name": "External Three"}},
			"has_more": false,
		})
	})

	// P2P human chat: chats/:id has empty name and p2p_target_id; members returns owner + peer.
	f.data("/open-apis/im/v1/chats/oc_p2p_human", map[string]any{"chat_mode": "p2p", "name": "", "p2p_target_id": "ou_p2p_peer", "p2p_target_type": "user"})
	f.data("/open-apis/im/v1/chats/oc_p2p_human/members", map[string]any{
		"items": []any{
			map[string]any{"member_id": testOwner, "name": "Owner"},
			map[string]any{"member_id": "ou_p2p_peer", "name": "External Friend"},
		},
	})
	// P2P bot chat: chats/:id has bot's display name.
	f.data("/open-apis/im/v1/chats/oc_p2p_bot", map[string]any{"chat_mode": "p2p", "name": "Helper Bot"})

	msgMentionLeft := textMsg("om_m1", "oc_ext_grp", "ou_ext1", "hi @_user_1", testNow.Add(-5*time.Minute))
	msgMentionLeft["mentions"] = []any{map[string]any{"key": "@_user_1", "id": "ou_left", "id_type": "open_id", "name": "Former Member"}}
	msgBotDM := textMsg("om_p2p_bot", "oc_p2p_bot", "cli_helper", "bot reply", testNow.Add(-2*time.Minute))
	msgBotDM["sender"] = map[string]any{"id": "cli_helper", "sender_type": "app"}

	f.data("/open-apis/im/v1/messages/om_p2p_human", map[string]any{"items": []any{
		textMsg("om_p2p_human", "oc_p2p_human", "ou_p2p_peer", "hello friend", testNow.Add(-3*time.Minute)),
	}})
	f.data("/open-apis/im/v1/messages/om_p2p_bot", map[string]any{"items": []any{msgBotDM}})

	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("container_id") {
		case "oc_ext_grp":
			writeData(w, map[string]any{"items": []any{
				msgMentionLeft,
				textMsg("om_m2", "oc_ext_grp", "ou_ext2", "second external", testNow.Add(-4*time.Minute)),
				textMsg("om_m3", "oc_ext_grp", "ou_ext3", "third external", testNow.Add(-3*time.Minute)),
				textMsg("om_left", "oc_ext_grp", "ou_left", "before leaving", testNow.Add(-2*time.Minute)),
			}})
		case "oc_p2p_human":
			writeData(w, map[string]any{"items": []any{
				textMsg("om_p2p_human", "oc_p2p_human", "ou_p2p_peer", "hello friend", testNow.Add(-3*time.Minute)),
				textMsg("om_p2p_mine", "oc_p2p_human", testOwner, "my reply", testNow.Add(-1*time.Minute)),
			}})
		case "oc_p2p_bot":
			writeData(w, map[string]any{"items": []any{msgBotDM}})
		default:
			t.Errorf("unexpected container %q", r.URL.Query().Get("container_id"))
		}
	})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	byID := map[string]larksyncwire.Message{}
	for _, m := range res.Messages {
		byID[m.ID] = m
	}
	wantSenders := map[string]string{
		"om_m1":        "External One",
		"om_m2":        "External Two",
		"om_m3":        "External Three",
		"om_left":      "Former Member",
		"om_p2p_human": "External Friend",
		"om_p2p_bot":   "Helper Bot",
	}
	for id, want := range wantSenders {
		if got := byID[id].SenderName; got != want {
			t.Errorf("%s: SenderName = %q, want %q", id, got, want)
		}
	}
	if byID["om_p2p_human"].ChatName != "External Friend" || byID["om_p2p_mine"].ChatName != "External Friend" {
		t.Errorf("human p2p ChatName: peer=%q mine=%q", byID["om_p2p_human"].ChatName, byID["om_p2p_mine"].ChatName)
	}
	if byID["om_p2p_bot"].ChatName != "Helper Bot" {
		t.Errorf("bot p2p ChatName = %q, want Helper Bot", byID["om_p2p_bot"].ChatName)
	}
	// Group members fetched once (2 pages) and cached; ou_ext2 & ou_ext3 served from cache without hitting contact/v3.
	if n := f.count("/open-apis/im/v1/chats/oc_ext_grp/members"); n != 2 {
		t.Errorf("oc_ext_grp members calls = %d, want 2 (1 paginated fetch)", n)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_ext2") + f.count("/open-apis/contact/v3/users/ou_ext3"); n != 0 {
		t.Errorf("cached external members still hit contact/v3 %d times", n)
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(res.Cursors[p2pChatsResource]), &saved); err != nil {
		t.Fatalf("p2p_chats cursor: %v", err)
	}
	if saved["oc_p2p_human"] != "External Friend" || saved["oc_p2p_bot"] != "Helper Bot" {
		t.Errorf("saved p2p_chats = %v", saved)
	}
}

// ResolveNamesWithFallback resolves external group members (cached per chat), unnamed human/bot p2p chats, and
// message-carried sender/mention names while isolating scope refusals on optional chat-members lookups.
func TestResolveNamesWithFallbackCoversExternalGroupsP2PAndMessageInfo(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)

	for _, id := range []string{"ou_g1", "ou_g2", "ou_msg_only", "ou_dm_user"} {
		f.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			writeCode(w, http.StatusBadRequest, 41001, "external user")
		})
	}
	f.data("/open-apis/im/v1/chats/oc_g/members", map[string]any{
		"items": []any{
			map[string]any{"member_id": "ou_g1", "name": "Group One"},
			map[string]any{"member_id": "ou_g2", "name": "Group Two"},
		},
	})
	f.data("/open-apis/im/v1/chats/oc_dm_user", map[string]any{"chat_mode": "p2p", "name": "", "p2p_target_id": "ou_dm_user", "p2p_target_type": "user"})
	f.data("/open-apis/im/v1/chats/oc_dm_user/members", map[string]any{
		"items": []any{
			map[string]any{"member_id": testOwner, "name": "Owner"},
			map[string]any{"member_id": "ou_dm_user", "name": "DM Friend"},
		},
	})
	f.data("/open-apis/im/v1/chats/oc_dm_bot", map[string]any{"chat_mode": "p2p", "name": "Alert Bot"})
	f.data("/open-apis/im/v1/chats/oc_dm_owner_only", map[string]any{"chat_mode": "p2p", "name": ""})
	f.data("/open-apis/im/v1/chats/oc_dm_owner_only/members", map[string]any{
		"items": []any{
			map[string]any{"member_id": testOwner, "name": "Owner"},
			map[string]any{"member_id": "ou_quiet_peer", "name": "Quiet Peer"},
		},
	})
	msgWithSenderName := textMsg("om_sample", "oc_g", "ou_msg_only", "hi", testNow.Add(-time.Minute))
	msgWithSenderName["sender"] = map[string]any{"id": "ou_msg_only", "sender_type": "user", "name": "Inline Sender"}
	f.data("/open-apis/im/v1/messages/om_sample", map[string]any{"items": []any{msgWithSenderName}})

	got, err := s.ResolveNamesWithFallback(t.Context(), b, staticToken("tok"), larksyncwire.NameResolveRequest{
		OpenIDs: []larksyncwire.OpenID{"ou_g1", "ou_g2", "ou_msg_only", "ou_dm_user"},
		ChatSenders: map[string][]string{
			"oc_g":       {"ou_g1", "ou_g2", "ou_msg_only"},
			"oc_dm_user": {"ou_dm_user"},
			"oc_dm_bot":  {"cli_alert"},
		},
		P2PChatIDs:     []larksyncwire.ChatID{"oc_dm_user", "oc_dm_bot", "oc_dm_owner_only"},
		SampleMessages: map[string]string{"ou_msg_only": "om_sample"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantUsers := map[string]string{
		"ou_g1":       "Group One",
		"ou_g2":       "Group Two",
		"ou_msg_only": "Inline Sender",
		"ou_dm_user":  "DM Friend",
		"cli_alert":   "Alert Bot",
	}
	for id, want := range wantUsers {
		if got.UserNames[id] != want {
			t.Errorf("UserNames[%s] = %q, want %q", id, got.UserNames[id], want)
		}
	}
	wantP2P := map[string]string{
		"oc_dm_user":       "DM Friend",
		"oc_dm_bot":        "Alert Bot",
		"oc_dm_owner_only": "Quiet Peer",
	}
	for cid, want := range wantP2P {
		if got.P2PChatNames[cid] != want {
			t.Errorf("P2PChatNames[%s] = %q, want %q", cid, got.P2PChatNames[cid], want)
		}
	}
	if n := f.count("/open-apis/im/v1/chats/oc_g/members"); n != 1 {
		t.Errorf("oc_g members called %d times, want 1", n)
	}
	if n := f.count("/open-apis/contact/v3/users/ou_g2"); n != 0 {
		t.Errorf("ou_g2 should have hit oc_g member cache without calling contact/v3 (%d calls)", n)
	}

	// Missing chat-members scope degrades gracefully without returning ErrKindScope when contact/v3 has no scope error.
	f2 := newFakeOpen(t, clk)
	f2.handle("/open-apis/contact/v3/users/ou_ext", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 41001, "external user")
	})
	f2.handle("/open-apis/im/v1/chats/oc_denied/members", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 99991672, "missing im:chat.members:read")
	})
	f2.handle("/open-apis/im/v1/chats/oc_second/members", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("members fallback should stop after ErrKindScope")
	})
	got2, err := s.ResolveNamesWithFallback(t.Context(), binding(f2, "primary~a1", larksyncwire.TierFull), staticToken("tok"), larksyncwire.NameResolveRequest{
		OpenIDs:     []larksyncwire.OpenID{"ou_ext"},
		ChatSenders: map[string][]string{"oc_denied": {"ou_ext"}, "oc_second": {"ou_ext"}},
	})
	if err != nil {
		t.Fatalf("optional chat-members scope denial must not fail backfill: %v", err)
	}
	if len(got2.UserNames) != 0 {
		t.Fatalf("unexpected names: %v", got2.UserNames)
	}
}
