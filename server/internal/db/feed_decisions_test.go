package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func draftRows(t *testing.T, d *DB) (FeedRow, TodoDecisionRow) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.pool.Exec(cleanup, `DELETE FROM assistant_feed WHERE id IN ('draft-card', 'repeated-card', 'new-progress-card')`); err != nil {
			t.Error(err)
		}
	})
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	if _, err := d.pool.Exec(ctx, `INSERT INTO todos (id, title, origin) VALUES ('draft-task', 'Synthetic task', 'draft-conversation')`); err != nil {
		t.Fatal(err)
	}
	card := FeedRow{ID: "draft-card", Conversation: "draft-conversation", Kind: "message", Ref: "synthetic-item", At: at,
		Body: json.RawMessage(`{"todo_id":"draft-task","draft":"Verification is in progress.","progress_version":"progress-1","message_version":"message-1"}`)}
	if err := d.AppendFeed(ctx, card); err != nil {
		t.Fatal(err)
	}
	decision := TodoDecisionRow{ID: "draft-decision", TodoID: "draft-task", Source: DecisionCard, SourceMessage: "draft:draft-card:1",
		Question: "Synthetic progress reply", Choice: "Do not handle this draft", CreatedAt: at.Add(time.Minute)}
	return card, decision
}

func dismissedBody(t *testing.T, card FeedRow, id string, at time.Time) json.RawMessage {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(card.Body, &body); err != nil {
		t.Fatal(err)
	}
	body["dismissal_id"], body["dismissed_at"], body["dismissal_updated_at"] = id, at, at
	raw, _ := json.Marshal(body)
	return raw
}

func TestFeedDraftDecisionDurableUndo(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	card, decision := draftRows(t, d)
	body := dismissedBody(t, card, decision.ID, decision.CreatedAt)
	if ok, err := d.ApplyFeedDecision(ctx, card, body, decision, decision.CreatedAt); err != nil || !ok {
		t.Fatalf("dismiss: %v %v", ok, err)
	}
	// A fresh pool observes both the card and its task audit; this is a manager
	// reload, not a database-server restart.
	d2, err := Open(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	current, err := d2.FeedItem(ctx, card.ID)
	if err != nil || current == nil {
		t.Fatalf("reload: %+v %v", current, err)
	}
	list, err := d2.ListTodoDecisions(ctx, decision.TodoID, true)
	if err != nil || len(list) != 1 || list[0].ID != decision.ID {
		t.Fatalf("audit: %+v %v", list, err)
	}
	rows, err := d2.Feed(ctx, card.Conversation, card.At, 100)
	if err != nil || len(rows) != 1 || rows[0].ID != card.ID {
		t.Fatalf("incremental dismiss: %+v %v", rows, err)
	}
	undoAt := decision.CreatedAt.Add(time.Minute)
	undo := decision
	undo.ID, undo.SourceMessage, undo.Choice, undo.SupersedesID, undo.CreatedAt = "undo-decision", "draft:draft-card:2", "Return this draft to pending", decision.ID, undoAt
	var restored map[string]any
	_ = json.Unmarshal(current.Body, &restored)
	delete(restored, "dismissal_id")
	delete(restored, "dismissed_at")
	restored["dismissal_updated_at"] = undoAt
	raw, _ := json.Marshal(restored)
	if ok, err := d2.ApplyFeedDecision(ctx, *current, raw, undo, undoAt); err != nil || !ok {
		t.Fatalf("Undo: %v %v", ok, err)
	}
	rows, err = d.Feed(ctx, card.Conversation, decision.CreatedAt, 100)
	if err != nil || len(rows) != 1 || string(rows[0].Body) == string(current.Body) {
		t.Fatalf("incremental Undo: %+v %v", rows, err)
	}
	list, err = d.ListTodoDecisions(ctx, decision.TodoID, false)
	if err != nil || len(list) != 1 || list[0].ID != undo.ID {
		t.Fatalf("superseding audit: %+v %v", list, err)
	}
	var doneAt *time.Time
	if err := d.pool.QueryRow(ctx, `SELECT done_at FROM todos WHERE id = $1`, decision.TodoID).Scan(&doneAt); err != nil || doneAt != nil {
		t.Fatalf("task closed: %v %v", doneAt, err)
	}
}

func TestFeedDraftDecisionConcurrentCAS(t *testing.T) {
	d := open(t)
	card, decision := draftRows(t, d)
	d2, err := Open(context.Background(), os.Getenv("PERSONA_TEST_DATABASE_URL"), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var results [2]bool
	var errs [2]error
	for i, st := range []*DB{d, d2} {
		wg.Go(func() {
			<-start
			row := decision
			if i == 1 {
				row.ID, row.SourceMessage = "other-decision", "draft:draft-card:other"
			}
			results[i], errs[i] = st.ApplyFeedDecision(context.Background(), card, dismissedBody(t, card, row.ID, row.CreatedAt), row, row.CreatedAt)
		})
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0] == results[1] {
		t.Fatalf("CAS: %v %v", results, errs)
	}
	list, err := d.ListTodoDecisions(context.Background(), decision.TodoID, true)
	if err != nil || len(list) != 1 {
		t.Fatalf("duplicate audit: %+v %v", list, err)
	}
}

func TestFeedDraftOlderDismissDoesNotChangeNewerCard(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	card, decision := draftRows(t, d)
	newer := card
	newer.ID, newer.At = "new-progress-card", decision.CreatedAt
	newer.Body = json.RawMessage(`{"todo_id":"draft-task","draft":"Verification passed.","progress_version":"progress-2","message_version":"message-1"}`)
	if ok, err := d.AppendDraftFeed(ctx, newer); err != nil || !ok {
		t.Fatalf("new draft: %v %v", ok, err)
	}
	if ok, err := d.ApplyFeedDecision(ctx, card, dismissedBody(t, card, decision.ID, decision.CreatedAt), decision, decision.CreatedAt); err != nil || !ok {
		t.Fatalf("older exact dismissal: %v %v", ok, err)
	}
	current, err := d.FeedItem(ctx, newer.ID)
	if err != nil || current == nil {
		t.Fatalf("newer card: %+v %v", current, err)
	}
	var body map[string]any
	_ = json.Unmarshal(current.Body, &body)
	if body["draft"] != "Verification passed." || body["dismissal_id"] != nil {
		t.Fatalf("newer card changed: %s", current.Body)
	}
	older, _ := d.FeedItem(ctx, card.ID)
	undo := decision
	undo.ID, undo.SourceMessage, undo.SupersedesID, undo.CreatedAt = "stale-undo", "draft:draft-card:undo", decision.ID, decision.CreatedAt.Add(time.Minute)
	if ok, err := d.ApplyFeedDecision(ctx, *older, card.Body, undo, undo.CreatedAt); err != nil || ok {
		t.Fatalf("older Undo: %v %v", ok, err)
	}
}

func TestFeedDraftDecisionRollbackAndNewProgress(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	card, decision := draftRows(t, d)
	body := dismissedBody(t, card, decision.ID, decision.CreatedAt)
	bad := decision
	bad.SupersedesID = "missing-decision"
	if ok, err := d.ApplyFeedDecision(ctx, card, body, bad, bad.CreatedAt); ok || !errors.Is(err, ErrDecisionSupersede) {
		t.Fatalf("audit failure: %v %v", ok, err)
	}
	current, _ := d.FeedItem(ctx, card.ID)
	var decoded map[string]any
	_ = json.Unmarshal(current.Body, &decoded)
	if decoded["dismissal_id"] != nil {
		t.Fatalf("card committed without audit: %s", current.Body)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if ok, err := d.ApplyFeedDecision(cancelled, card, body, decision, decision.CreatedAt); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v %v", ok, err)
	}
	if ok, err := d.ApplyFeedDecision(ctx, card, body, decision, decision.CreatedAt); !ok || err != nil {
		t.Fatalf("retry: %v %v", ok, err)
	}
	// Producer suppression survives a reload and ignores wording changes until
	// there is a new source message or actual Agent progress generation.
	repeated := card
	repeated.ID, repeated.At = "repeated-card", decision.CreatedAt.Add(time.Minute)
	repeated.Body = json.RawMessage(`{"todo_id":"draft-task","draft":"Still verifying.","progress_version":"progress-1","message_version":"message-1"}`)
	if ok, err := d.AppendDraftFeed(ctx, repeated); err != nil || ok {
		t.Fatalf("same progress resurfaced: %v %v", ok, err)
	}
	repeated.ID, repeated.Body = "new-progress-card", json.RawMessage(`{"todo_id":"draft-task","draft":"Verification passed.","progress_version":"progress-2","message_version":"message-1"}`)
	if ok, err := d.AppendDraftFeed(ctx, repeated); err != nil || !ok {
		t.Fatalf("new progress hidden: %v %v", ok, err)
	}
	current, _ = d.FeedItem(ctx, card.ID)
	undo := decision
	undo.ID, undo.SourceMessage, undo.SupersedesID, undo.CreatedAt = "stale-undo", "draft:draft-card:undo", decision.ID, repeated.At.Add(time.Minute)
	if ok, err := d.ApplyFeedDecision(ctx, *current, card.Body, undo, undo.CreatedAt); err != nil || ok {
		t.Fatalf("Undo covered new progress: %v %v", ok, err)
	}
	list, err := d.ListTodoDecisions(ctx, decision.TodoID, true)
	if err != nil || len(list) != 1 {
		t.Fatalf("stale Undo audit: %+v %v", list, err)
	}
}
