package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The companion bots' four routes are the lark module's (hub_routes): with the module switched off or built out the
// manager has none of them and answers 404, and the chat's stored state (the board, the companions) is neither
// rewritten nor dropped by the module being off; the module finds it again when it is back (#13430).

var companionRoutes = []struct{ method, path, body string }{
	{"PUT", "/api/v1/assistant/feishu/companions", companionsBody},
	{"GET", "/api/v1/assistant/feishu/notices?wait=0", ""},
	{"POST", "/api/v1/assistant/feishu/notices/action", `{"action":{"value":{"a":"nav","to":"home"}}}`},
	{"POST", "/api/v1/assistant/feishu/workbench", ""},
}

func TestCompanionRoutesAre404WithTheModuleOff(t *testing.T) {
	f := newFakeHub().withMachine()
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), ModulesOff: []string{"lark"}}))
	for _, r := range companionRoutes {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s with the module off: %d, want 404", r.method, r.path, rec.Code)
		}
	}
	for _, p := range s.Routes() {
		for _, r := range companionRoutes {
			if p == r.method+" "+strings.SplitN(r.path, "?", 2)[0] {
				t.Errorf("the manager registers %q with the module off", p)
			}
		}
	}
}

func TestSwitchingTheModuleOffKeepsTheStoredChatState(t *testing.T) {
	needsLarkLayer(t)
	ctx := context.Background()
	f := newFakeHub().withMachine()
	st := &memStore{}
	pairing := "paired-1"
	seeded := botState{Current: botTarget{Kind: targetMain}, Pairing: &pairing,
		Board:      &botBoardRef{MessageID: "om_board_kept", At: time.Unix(1000, 0).UTC()},
		Companions: []CompanionBot{{Kind: CompanionNotify, Name: "Notices", AppID: "cli_noticebot1"}}}
	if err := st.PutSetting(ctx, botStateKey, seeded); err != nil {
		t.Fatal(err)
	}
	raw := func() []byte { st.mu.Lock(); defer st.mu.Unlock(); return bytes.Clone(st.vals[botStateKey]) }
	before := raw()

	off := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), ModulesOff: []string{"lark"}}))
	off.boardChanged() // what the core does when a card opens or a to-do closes
	off.boardFleetUp()
	if off.workbenchStamp(ctx, nil) != nil || off.divertNotices(ctx, "push", nil, nil) {
		t.Fatal("the module is off: no stamp, no notices")
	}
	for _, r := range companionRoutes {
		rec := httptest.NewRecorder()
		off.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
	}
	if after := raw(); !bytes.Equal(before, after) {
		t.Fatalf("the stored state changed while the module was off:\nbefore %s\nafter  %s", before, after)
	}

	on := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
	if ref := on.boardRef(ctx); ref == nil || ref.MessageID != "om_board_kept" {
		t.Fatalf("the board after the module came back: %+v", ref)
	}
	var bots []assistantwire.CompanionBot
	on.larkModule().Book(ctx, func(b assistantwire.ChatBook) { bots = b.Companions() })
	if len(bots) != 1 || bots[0].AppID != "cli_noticebot1" {
		t.Fatalf("the companions after the module came back: %+v", bots)
	}
	var kept json.RawMessage
	_, _ = st.Setting(ctx, botStateKey, &kept)
}
