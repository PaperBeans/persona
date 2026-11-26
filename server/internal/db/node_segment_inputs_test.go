package db

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestNodeSegmentInputsArePersistedAndMerged(t *testing.T) {
	var state NodeSession
	if err := json.Unmarshal([]byte(`{"launcher":"agy","cwd":"/tmp/assistant","segment_inputs":["segment-once:relay-1","segment-input:a"]}`), &state); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.SegmentInputs, []string{"segment-once:relay-1", "segment-input:a"}) {
		t.Fatalf("segment_inputs lost at manager boundary: %+v", state.SegmentInputs)
	}
	d, ctx := open(t), context.Background()
	if err := d.PutNodeSession(ctx, "machine", "session", state); err != nil {
		t.Fatal(err)
	}
	sessions, err := d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	got := sessions["session"]
	if !slices.Equal(got.SegmentInputs, []string{"segment-once:relay-1", "segment-input:a"}) {
		t.Fatalf("persisted SegmentInputs = %v, want [segment-once:relay-1 segment-input:a]", got.SegmentInputs)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		SegmentInputs []string `json:"segment_inputs"`
	}
	if err := json.Unmarshal(raw, &back); err != nil || !slices.Equal(back.SegmentInputs, got.SegmentInputs) {
		t.Fatalf("state.load JSON = %s (err=%v)", raw, err)
	}

	// A later put with empty SegmentInputs (e.g., before state.load) must preserve persisted receipts.
	empty := state
	empty.SegmentInputs = nil
	if err := d.PutNodeSession(ctx, "machine", "session", empty); err != nil {
		t.Fatal(err)
	}
	sessions, err = d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions["session"].SegmentInputs, []string{"segment-once:relay-1", "segment-input:a"}) {
		t.Fatalf("empty put wiped SegmentInputs: %v", sessions["session"].SegmentInputs)
	}

	// A put carrying only a newly accepted receipt unions with persisted receipts rather than wiping them.
	partial := state
	partial.SegmentInputs = []string{"segment-input:b"}
	if err := d.PutNodeSession(ctx, "machine", "session", partial); err != nil {
		t.Fatal(err)
	}
	sessions, err = d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions["session"].SegmentInputs, []string{"segment-once:relay-1", "segment-input:a", "segment-input:b"}) {
		t.Fatalf("partial put did not union SegmentInputs: %v", sessions["session"].SegmentInputs)
	}

	// Overflow past maxNodeSegmentInputs retains the newest 64 receipts in order.
	many := make([]string, 0, maxNodeSegmentInputs)
	for i := range maxNodeSegmentInputs {
		many = append(many, fmt.Sprintf("segment-input:%03d", i))
	}
	next := state
	next.SegmentInputs = many
	if err := d.PutNodeSession(ctx, "machine", "session", next); err != nil {
		t.Fatal(err)
	}
	sessions, err = d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions["session"].SegmentInputs, many) {
		t.Fatalf("capped merge = len %d first=%q last=%q, want len %d first=%q last=%q",
			len(sessions["session"].SegmentInputs), sessions["session"].SegmentInputs[0], sessions["session"].SegmentInputs[len(sessions["session"].SegmentInputs)-1],
			len(many), many[0], many[len(many)-1])
	}
}
