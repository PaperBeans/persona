package assistant

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestLogicalOfflinePlaceholderSurvivesDecorationAndTools(t *testing.T) {
	s, _, d, _ := logicalPair(t, "codex")
	ctx := t.Context()
	activated := activatePair(t, d)
	for _, page := range []db.ConversationPage{
		{MachineID: "server", SessionID: "side-chat", History: "h1", Revision: activated.Revision, ThroughSeq: 6, Tail: true, Items: []proto.Item{
			{ID: "line1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "original question"},
			{ID: "line6#0", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "retired answer"},
		}},
		{MachineID: "server", SessionID: "target-chat", History: "h2", Revision: activated.Revision, ThroughSeq: 1, Tail: true, Items: []proto.Item{
			{ID: "line1#0", Seq: 1, Role: "assistant", Kind: proto.KindText, Text: "target answer"},
		}},
	} {
		if err := d.PutConversationPage(ctx, page); err != nil {
			t.Fatal(err)
		}
	}

	// The active physical segment is gone, while the logical root and both segments' durable history remain.
	physical := newFleetDevs()
	physical.add("server", "Computer")
	physical.setSessions("server", []proto.Session{{SessionID: "main-chat", Cwd: "/home/u/.persona/assistant", Assistant: true, Rev: 100}})
	s.devs = &logicalDevices{Devices: physical, s: s}
	s.logicalChanged()

	listed, err := s.toolConversationsList(ctx, "main-chat", []byte(`{}`))
	if err != nil {
		t.Errorf("list the logical root: %v", err)
	} else if !strings.Contains(listed, "side-chat") {
		t.Errorf("logical root missing from the decorated conversation list: %s", listed)
	}

	read, err := s.toolConversationRead(ctx, "main-chat", []byte(`{"id":"side-chat","turns":10}`))
	if err != nil {
		t.Errorf("read durable history through the logical root: %v", err)
	} else if !strings.Contains(read, "original question") || !strings.Contains(read, "target answer") {
		t.Errorf("durable segments missing from logical read: %s", read)
	}

	unavailable := i18n.T(i18n.From(ctx), "assistant-conversation_segments.unavailable")
	_, err = s.toolConversationSend(ctx, "main-chat", []byte(`{"id":"side-chat","text":"do not start anything"}`))
	if err == nil || strings.Contains(err.Error(), "no conversation") || !strings.Contains(err.Error(), unavailable) {
		t.Errorf("send to the vanished active segment: %v", err)
	}
	for _, method := range []string{DeviceSessionSend, DeviceSessionSendOnce, DeviceAssistantSend} {
		if calls := physical.callsTo("server", method); len(calls) != 0 {
			t.Errorf("%s dispatched to a vanished segment: %s", method, calls)
		}
	}
	conversation, err := d.LogicalConversation(ctx, "server", "side-chat")
	if err != nil || conversation == nil || len(conversation.Segments) != 2 {
		t.Fatalf("send implicitly created a replacement segment: %+v %v", conversation, err)
	}
}

func TestLogicalOfflinePlaceholderSessionGetKeepsAssistantForRestore(t *testing.T) {
	s, _, d, _ := logicalPair(t, "codex")
	ctx := t.Context()
	activated := activatePair(t, d)

	physical := newFleetDevs()
	physical.add("server", "Computer")
	physical.setSessions("server", []proto.Session{
		{SessionID: "main-chat", Cwd: "/home/u/.persona/assistant", Rev: 100},
		{SessionID: "ordinary", Cwd: "/home/u/project", Assistant: true, Rev: 100},
	})
	s.devs = &logicalDevices{Devices: physical, s: s}
	s.logicalChanged()

	placeholder, known := s.nodeSession("server", "side-chat")
	if !known || !placeholder.LogicalPlaceholder || !placeholder.Assistant {
		t.Fatalf("logical placeholder before session.get: %+v, known=%v", placeholder, known)
	}
	got, known := s.session(ctx, "server", "side-chat")
	if !known || !got.Assistant {
		t.Errorf("session.get cleared the placeholder's Assistant identity: %+v, known=%v", got, known)
	}
	for _, tc := range []struct {
		id            string
		wantAssistant bool
	}{
		{id: "main-chat", wantAssistant: true},
		{id: "ordinary", wantAssistant: false},
	} {
		row, known := s.session(ctx, "server", tc.id)
		if !known || row.Assistant != tc.wantAssistant {
			t.Errorf("ordinary session.get recomputation for %s: %+v, known=%v, want Assistant=%v", tc.id, row, known, tc.wantAssistant)
		}
	}

	// Stop at the next, independent epoch fence: reaching it proves restore did not reject the placeholder as unavailable.
	item := conversationSwitchItem{
		Machine: "server", ConversationID: "side-chat", ExpectedEpoch: activated.Epoch + 1,
		OperationID: "restore-availability-check", Outcome: conversationSwitchReady,
	}
	batch := conversationSwitchBatch{RestoreOf: "earlier-fallback", FirstTurn: true}
	if changed := s.runConversationSwitchItem(ctx, d, &conversationSwitchState{}, &batch, &item); !changed ||
		item.Outcome != conversationSwitchRefused || item.Reason != "the conversation changed independently; its newer settings were left untouched" {
		t.Errorf("restore did not reach the epoch fence after the availability check: %+v, changed=%v", item, changed)
	}
}
