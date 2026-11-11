package extmgr

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRecordNodeSentKeepsBypassProvenance(t *testing.T) {
	h, st := larkSentManager(t)
	report := proto.LarkSent{Command: "ex", Method: "POST", Path: "/notes/one", SessionID: "conversation",
		LetThrough: proto.LarkLetThrough{Kind: proto.LarkThroughBypass}}
	if _, err := h.RecordNodeSent(context.Background(), "box", larkSentParams(t, report, nil)); err != nil {
		t.Fatal(err)
	}
	rows := st.all()
	if len(rows) != 1 || string(rows[0].LetThrough) != `{"kind":"permission_mode"}` {
		t.Fatalf("bypass provenance not recorded: %+v", rows)
	}
}
