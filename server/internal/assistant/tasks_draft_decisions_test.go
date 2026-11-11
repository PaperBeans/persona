package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func draftAction(t *testing.T, h *inboxHub, card FeedItem, action string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return inboxCall(t, h.Service, http.MethodPost, "/api/v1/assistant/feed/"+card.ID+"/"+action, string(raw))
}

func TestMessageDraftDismissUndo(t *testing.T) {
	h, rc, inbox := cardHub(t)
	draft(t, h, "The fix is in progress. I will report the verification result.")
	card := cards(t, h, time.Time{})[0]
	original := cardBody(t, card).Draft
	before := onlyTodo(h.Service)
	code, out := draftAction(t, h, card, "dismiss", map[string]string{"draft": original})
	if code != http.StatusOK || out["dismissal_id"] == "" {
		t.Fatalf("dismiss: %d %v", code, out)
	}
	id := out["dismissal_id"].(string)
	for _, since := range []time.Time{{}, card.At} {
		got := cards(t, h, since)
		if len(got) != 1 || cardBody(t, got[0]).DismissalID != id || cardBody(t, got[0]).Draft != original {
			t.Fatalf("dismissed card since %v: %+v", since, got)
		}
	}
	if code, replay := draftAction(t, h, card, "dismiss", map[string]string{"draft": original}); code != 200 || replay["dismissal_id"] != id {
		t.Fatalf("dismiss replay: %d %v", code, replay)
	}
	if code, _ := draftAction(t, h, card, "dismiss", map[string]string{"draft": "A stale edited draft"}); code != 409 {
		t.Fatalf("stale draft: %d", code)
	}
	send, _ := json.Marshal(map[string]string{"card": card.ID, "text": original})
	if code, out := inboxCall(t, h.Service, "POST", "/api/v1/inbox/example:1/reply", string(send)); code != 409 || out["code"] != "task_draft_dismissed" {
		t.Fatalf("stale Send: %d %v", code, out)
	}
	if len(rc.got) != 0 || inbox.DoneAt != nil {
		t.Fatalf("dismiss sent or filed: %+v %+v", rc.got, inbox)
	}
	after := onlyTodo(h.Service)
	if after.DoneAt != nil || len(after.Agents) != len(before.Agents) || after.Agents[0].ReleasedAt != nil {
		t.Fatalf("dismiss changed the task: %+v", after)
	}
	if code, _ := draftAction(t, h, card, "undo-dismiss", map[string]string{"dismissal_id": "wrong"}); code != 409 {
		t.Fatalf("wrong Undo: %d", code)
	}
	if code, out := draftAction(t, h, card, "undo-dismiss", map[string]string{"dismissal_id": id}); code != 200 {
		t.Fatalf("Undo: %d %v", code, out)
	}
	got := cards(t, h, card.At)
	if len(got) != 1 || cardBody(t, got[0]).DismissalID != "" || cardBody(t, got[0]).DismissedAt != nil || len(rc.got) != 0 {
		t.Fatalf("Undo did not restore the unsent card: %+v", got)
	}
	if code, _ := draftAction(t, h, card, "dismiss", map[string]string{"draft": original}); code != 200 {
		t.Fatalf("dismiss again: %d", code)
	}
	if code, _ := draftAction(t, h, card, "undo-dismiss", map[string]string{"dismissal_id": id}); code != 409 {
		t.Fatalf("old Undo replaced a newer decision: %d", code)
	}
}

func TestMessageDraftNewProgressFencesUndo(t *testing.T) {
	h, rc, _ := cardHub(t)
	h.f.mu.Lock()
	h.f.sessions["box"] = []proto.Session{{MachineID: "box", SessionID: "s1", Rev: 1, LastText: "The first verification is in progress."}}
	h.f.mu.Unlock()
	draft(t, h, "The first verification is in progress.")
	card := cards(t, h, time.Time{})[0]
	_, out := draftAction(t, h, card, "dismiss", map[string]string{"draft": cardBody(t, card).Draft})
	// Re-submission, including rewording, is not new Agent progress.
	draft(t, h, "The first verification is in progress.")
	draft(t, h, "Verification is still running.")
	if got := cards(t, h, time.Time{}); len(got) != 1 {
		t.Fatalf("dismissed progress was drafted again: %+v", got)
	}
	h.f.mu.Lock()
	for i := range h.f.sessions["box"] {
		if h.f.sessions["box"][i].SessionID == "s1" {
			h.f.sessions["box"][i].Rev++
			h.f.sessions["box"][i].LastText = "Verification passed. Ready for review."
		}
	}
	h.f.mu.Unlock()
	draft(t, h, "Verification passed. The next change is ready to review.")
	list := cards(t, h, time.Time{})
	if len(list) != 2 || cardBody(t, list[1]).DismissalID != "" {
		t.Fatalf("new progress hidden: %+v", list)
	}
	if code, _ := draftAction(t, h, card, "undo-dismiss", map[string]string{"dismissal_id": out["dismissal_id"].(string)}); code != 409 {
		t.Fatalf("Undo over new progress: %d", code)
	}
	if cardBody(t, cards(t, h, time.Time{})[0]).DismissalID == "" || len(rc.got) != 0 {
		t.Fatal("stale Undo changed the old card or sent a reply")
	}
	// The new pending draft can also be dismissed without affecting the old one.
	if code, _ := draftAction(t, h, list[1], "dismiss", map[string]string{"draft": cardBody(t, list[1]).Draft}); code != 200 {
		t.Fatalf("new draft dismiss: %d", code)
	}
}

func setDraftProgress(h *inboxHub, rev int64, text string) {
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	h.f.sessions["box"] = []proto.Session{{MachineID: "box", SessionID: "s1", Rev: rev, LastText: text}}
}

func TestMessageDraftDismissAfterAgentProgressWithoutNewCard(t *testing.T) {
	h, rc, _ := cardHub(t)
	setDraftProgress(h, 1, "Verification started.")
	draft(t, h, "The first verification is in progress.")
	card := cards(t, h, time.Time{})[0]
	original := cardBody(t, card)
	// The Agent continues normally, but the displayed draft has not changed.
	setDraftProgress(h, 2, "Verification passed; checking the next target.")
	code, out := draftAction(t, h, card, "dismiss", map[string]string{"draft": original.Draft})
	if code != 200 {
		t.Fatalf("unchanged card after Agent progress: %d %v", code, out)
	}
	ignored := cardBody(t, cards(t, h, time.Time{})[0])
	if ignored.ProgressVersion != original.ProgressVersion || ignored.Draft != original.Draft {
		t.Fatalf("dismissal changed the draft generation: %+v", ignored)
	}
	// Continued Agent activity must not invalidate a toast Undo when no new
	// draft card was produced. Undo is local to this card and never sends.
	setDraftProgress(h, 3, "The next target also passed.")
	if code, out := draftAction(t, h, card, "undo-dismiss", map[string]string{"dismissal_id": out["dismissal_id"].(string)}); code != 200 {
		t.Fatalf("Undo without a newer card: %d %v", code, out)
	}
	if len(rc.got) != 0 {
		t.Fatal("dismiss or Undo sent a reply")
	}
	if code, _ := draftAction(t, h, card, "dismiss", map[string]string{"draft": original.Draft}); code != 200 {
		t.Fatalf("dismiss again: %d", code)
	}
	// Ignoring the original generation must not suppress the Agent's genuinely
	// new progress. Submission goes through the real inbox draft producer.
	draft(t, h, "Both verification targets passed.")
	list := cards(t, h, time.Time{})
	if len(list) != 2 || cardBody(t, list[1]).DismissalID != "" {
		t.Fatalf("new progress was suppressed: %+v", list)
	}
}

func TestMessageDraftDismissOlderCardKeepsNewerPending(t *testing.T) {
	h, rc, _ := cardHub(t)
	setDraftProgress(h, 1, "Verification started.")
	draft(t, h, "The first verification is in progress.")
	old := cards(t, h, time.Time{})[0]
	setDraftProgress(h, 2, "Verification passed.")
	draft(t, h, "Verification passed. Ready for review.")
	before := cards(t, h, time.Time{})
	if len(before) != 2 {
		t.Fatalf("drafts: %+v", before)
	}
	code, out := draftAction(t, h, old, "dismiss", map[string]string{"draft": cardBody(t, old).Draft})
	if code != 200 {
		t.Fatalf("dismiss exact older card: %d %v", code, out)
	}
	after := cards(t, h, time.Time{})
	if len(after) != 2 || cardBody(t, after[0]).DismissalID == "" || string(after[1].Body) != string(before[1].Body) {
		t.Fatalf("older dismissal affected the newer card: %+v", after)
	}
	if code, _ := draftAction(t, h, old, "undo-dismiss", map[string]string{"dismissal_id": out["dismissal_id"].(string)}); code != 409 {
		t.Fatalf("Undo over a newer card: %d", code)
	}
	if len(rc.got) != 0 || onlyTodo(h.Service).DoneAt != nil {
		t.Fatal("older dismissal sent or completed the task")
	}
}

func TestMessageDraftDecisionValidation(t *testing.T) {
	h, _, _ := cardHub(t)
	draft(t, h, "A synthetic pending reply.")
	card := cards(t, h, time.Time{})[0]
	for _, tc := range []struct {
		id, action, body string
		want             int
	}{
		{card.ID, "dismiss", `{}`, 400},
		{card.ID, "undo-dismiss", `{}`, 400},
		{"missing", "dismiss", `{"draft":"A synthetic pending reply."}`, 404},
	} {
		if code, _ := inboxCall(t, h.Service, "POST", "/api/v1/assistant/feed/"+tc.id+"/"+tc.action, tc.body); code != tc.want {
			t.Fatalf("%+v: %d", tc, code)
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/assistant/feed/"+card.ID+"/dismiss", strings.NewReader(`{"draft":"A synthetic pending reply."}`))
	r.Header.Set(RoleHeader, "readonly")
	w := httptest.NewRecorder()
	h.Service.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("readonly: %d", w.Code)
	}
	// The exact card is still pending, not rewritten by an invalid request.
	if got, err := h.feedItem(context.Background(), card.ID); err != nil || cardDismissalID(*got) != "" {
		t.Fatalf("invalid request mutated card: %+v %v", got, err)
	}
}
