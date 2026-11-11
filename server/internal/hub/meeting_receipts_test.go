package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// reclaimingStore is clientOpsStore that takes back an unknown operation (db.DB.ReclaimClientOp).
type reclaimingStore struct{ *clientOpsStore }

func (s reclaimingStore) ReclaimClientOp(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[id]
	if !ok || op.State != "unknown" {
		return false, nil
	}
	op.State = "pending"
	s.ops[id] = op
	return true, nil
}

// Just me and the follow-up keep a receipt derived from the body's id: a repeat gets the first answer without the
// manager hearing it again; another body under the id is 409 op_id_reused; a body without a UUID id goes on as it is.
// A follow-up's call a restart left unknown runs again.
func TestMeetingBodyIDReceipts(t *testing.T) {
	store := reclaimingStore{&clientOpsStore{ops: map[string]db.ClientOp{}}}
	h := &Hub{store: store}
	var calls atomic.Int32
	handler := h.withBodyIDReceipt(meetingJustMeOp, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_msg_id":"just-me-x"}`))
	})
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return rec
	}
	const p = "/api/v1/recordings/0f8fad5b-d9cb-469f-a165-70867728950e/just-me"
	body := `{"id":"A0000000-0000-4000-8000-000000000001","text":"hello"}`
	if rec := post(p, body); rec.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("first: %d %d", rec.Code, calls.Load())
	}
	if rec := post(p, body); rec.Code != http.StatusOK || calls.Load() != 1 || !strings.Contains(rec.Body.String(), "just-me-x") {
		t.Fatalf("repeat: %d %s, %d calls", rec.Code, rec.Body, calls.Load())
	}
	if rec := post(p, `{"id":"a0000000-0000-4000-8000-000000000001","text":"another"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "op_id_reused") {
		t.Fatalf("another body: %d %s", rec.Code, rec.Body)
	}
	if rec := post(strings.Replace(p, "0f8fad5b", "1f8fad5b", 1), body); rec.Code != http.StatusConflict {
		t.Fatalf("another recording: %d", rec.Code)
	}
	if rec := post(p, `{"id":"not-a-uuid"}`); rec.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("no id: %d %d", rec.Code, calls.Load())
	}

	// A follow-up's call left unknown runs again.
	fu := h.withBodyIDReceipt(meetingFollowupOp, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"created":true}`))
	})
	rec := httptest.NewRecorder()
	fbody := `{"id":"b0000000-0000-4000-8000-000000000001","text":"draft it"}`
	fu(rec, httptest.NewRequest("POST", "/api/v1/recordings/0f8fad5b-d9cb-469f-a165-70867728950e/followup", strings.NewReader(fbody)))
	n := calls.Load()
	store.mu.Lock()
	for id, op := range store.ops {
		if op.Kind == meetingFollowupOp {
			op.State, op.Result = "unknown", nil
			store.ops[id] = op
		}
	}
	store.mu.Unlock()
	if !strings.Contains(rec.Body.String(), "created") {
		t.Fatalf("follow-up: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	fu(rec, httptest.NewRequest("POST", "/api/v1/recordings/0f8fad5b-d9cb-469f-a165-70867728950e/followup", strings.NewReader(fbody)))
	if rec.Code != http.StatusOK || calls.Load() != n+1 {
		t.Fatalf("an unknown follow-up did not run again: %d, %d calls", rec.Code, calls.Load()-n)
	}
	if !clientOpRetry(meetingJustMeOp, []byte(`{"code":"assistant_offline"}`)) || clientOpRetry(meetingJustMeOp, []byte(`{"code":"op_id_reused"}`)) {
		t.Fatal("an offline manager's refusal must not be kept")
	}
}

// A Just me call the manager refused for sure (its computer offline, the recording gone, a relay error) is kept with
// its answer: the repeat gets that answer, not 202 {"state":"unknown"}, and the phone asks again with a new id.
func TestMeetingJustMeReceiptReplaysFailures(t *testing.T) {
	for _, c := range []struct {
		status     int
		body, want string
	}{
		{http.StatusServiceUnavailable, `{"code":"machine_offline","error":"offline"}`, "machine_offline"},
		{http.StatusNotFound, `{"code":"recording_not_found","error":"no such recording"}`, "recording_not_found"},
		{http.StatusBadGateway, `{"error":"relay failed"}`, "relay failed"},
	} {
		store := reclaimingStore{&clientOpsStore{ops: map[string]db.ClientOp{}}}
		h := &Hub{store: store}
		var calls atomic.Int32
		handler := h.withBodyIDReceipt(meetingJustMeOp, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		})
		const p = "/api/v1/recordings/0f8fad5b-d9cb-469f-a165-70867728950e/just-me"
		body := `{"id":"a0000000-0000-4000-8000-000000000001","text":"hello"}`
		for i := range 2 {
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest("POST", p, strings.NewReader(body)))
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.want) || calls.Load() != 1 {
				t.Fatalf("%d, call %d: %d %s, %d calls", c.status, i+1, rec.Code, rec.Body, calls.Load())
			}
		}
	}
}

// A Just me turn's card (proto.Interaction.Private) is pushed as a health conversation's is: nothing of what it asks
// on the Lock Screen, and no answer buttons that would show its options.
func TestPrivateCardPush(t *testing.T) {
	ix := proto.Interaction{ID: "ix1", Kind: proto.IxQuestion, Summary: "SECRET question", SessionTitle: "SECRET title", Private: true,
		Questions: []proto.Question{{Question: "SECRET?", Options: []proto.QuestionOption{{Label: "SECRET yes"}}}}}
	out := interactionPayloadIn(i18n.En, ix, "server")
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("a private card's push says what it asks: %s", raw)
	}
	if _, ok := askQuestion(ix); ok {
		t.Fatal("a private card's push can answer it")
	}
}
