package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestModelRoutingRecordsDecisions(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	rows := []ModelRoutingRow{
		{At: at, TodoID: "todo-1", PlanID: "plan-1", TaskIndex: 2, SessionID: "s-1", ProfileID: "performance", Active: true,
			Tier: "dev", Candidate: 1, Of: 3, Launcher: "second", Model: "model-b", Effort: "high", Reason: "unavailable",
			Detail: json.RawMessage(`{"candidates":[{"index":0,"launcher":"first","available":false}]}`)},
		{At: at.Add(time.Second), ProfileID: "economy", Tier: "dev", Of: 1, Launcher: "first", Reason: "only"},
	}
	for _, r := range rows {
		if err := d.PutModelRouting(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.ModelRoutings(ctx, "performance", at.Add(-time.Minute))
	if err != nil || len(got) != 1 {
		t.Fatalf("performance rows = %+v, %v", got, err)
	}
	r := got[0]
	if r.ID == 0 || !r.At.Equal(at) || r.TodoID != "todo-1" || r.PlanID != "plan-1" || r.TaskIndex != 2 || r.SessionID != "s-1" ||
		!r.Active || r.Tier != "dev" || r.Candidate != 1 || r.Of != 3 || r.Launcher != "second" || r.Model != "model-b" ||
		r.Effort != "high" || r.Reason != "unavailable" {
		t.Fatalf("row = %+v", r)
	}
	var detail struct {
		Candidates []struct {
			Launcher string `json:"launcher"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(r.Detail, &detail); err != nil || len(detail.Candidates) != 1 || detail.Candidates[0].Launcher != "first" {
		t.Fatalf("detail = %s, %v", r.Detail, err)
	}
	all, err := d.ModelRoutings(ctx, "", at.Add(-time.Minute))
	if err != nil || len(all) != 2 || all[1].Active || all[1].Reason != "only" || string(all[1].Detail) != "{}" {
		t.Fatalf("all rows = %+v, %v", all, err)
	}
}
