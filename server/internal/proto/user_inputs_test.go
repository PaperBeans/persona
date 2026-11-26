package proto

import (
	"encoding/json"
	"testing"
)

func TestUserInputPositionJSONCompatibility(t *testing.T) {
	type oldPosition struct {
		ID  string `json:"id"`
		Seq int    `json:"seq"`
	}
	const oldJSON = `{"id":"input#0","seq":3}`
	var current UserInputPosition
	if err := json.Unmarshal([]byte(oldJSON), &current); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(current)
	if err != nil || string(encoded) != oldJSON {
		t.Fatalf("old position round trip: %s, %v", encoded, err)
	}
	const newJSON = `{"id":"input#0","seq":3,"preview":"Question","answer":"Answer","attachments":2}`
	original := UserInputPosition{ID: "input#0", Seq: 3, Preview: "Question", Answer: "Answer", Attachments: 2}
	encoded, err = json.Marshal(original)
	if err != nil || string(encoded) != newJSON {
		t.Fatalf("new position: %s, %v", encoded, err)
	}
	var old oldPosition
	if err := json.Unmarshal(encoded, &old); err != nil || old != (oldPosition{"input#0", 3}) {
		t.Fatalf("old client read: %+v, %v", old, err)
	}
}
