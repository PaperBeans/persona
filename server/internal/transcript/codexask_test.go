package transcript

import (
	"strings"
	"testing"
)

// Rollout lines as codex-cli 0.153.4 / 0.163.0 write them (shapes from recorded rollouts, content made up).
const (
	askAsyncCall = `{"timestamp":"2026-11-01T13:40:29.600Z","type":"response_item","payload":{"type":"function_call","status":"completed","name":"request_user_input_async","arguments":"{\"questions\":[{\"title\":\"Which color should the banner be?\",\"options\":[\"Red\",\"Green\",\"Blue\"]}]}","call_id":"call_a1"}}`
	askAsyncItem = `{"timestamp":"2026-11-01T13:40:29.650Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"call_a1","content":[{"type":"Text","text":"Which color should the banner be?\n- Red\n- Green\n- Blue"}],"phase":"final_answer","delivery":"async","questions":[{"title":"Which color should the banner be?","options":["Red","Green","Blue"]}]}}}`
	askAsyncOut  = `{"timestamp":"2026-11-01T13:40:29.700Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_a1","output":"{\"accepted\":true}"}}`
	askDone      = `{"timestamp":"2026-11-01T13:40:31.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1"}}`
	askSyncCall  = `{"timestamp":"2026-11-01T13:49:35.000Z","type":"response_item","payload":{"type":"function_call","status":"completed","name":"request_user_input","arguments":"{\"questions\":[{\"header\":\"Color\",\"id\":\"color\",\"question\":\"Which color should the banner be?\",\"options\":[{\"label\":\"Red\",\"description\":\"warm\"},{\"label\":\"Blue\",\"description\":\"calm\"}]},{\"header\":\"Size\",\"id\":\"size\",\"question\":\"How tall should it be?\",\"options\":[{\"label\":\"Small\",\"description\":\"one line\"},{\"label\":\"Large\",\"description\":\"two lines\"}]}]}","call_id":"call_s1"}}`
	askSyncOut   = `{"timestamp":"2026-11-01T13:49:55.788Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_s1","output":"{\"answers\":{\"size\":{\"answers\":[\"Small\"]},\"color\":{\"answers\":[\"None of the above\",\"user_note: Purple please\"]}}}"}}`
	askAborted   = `{"timestamp":"2026-11-01T13:50:00.000Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"t2","reason":"interrupted"}}`
)

func userLine(text string) string {
	return `{"timestamp":"2026-11-01T13:41:00.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":` +
		quote(text) + `}]}}}`
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func feed(lines ...string) *CodexAsks {
	a := &CodexAsks{}
	for _, l := range lines {
		a.Feed([]byte(l))
	}
	return a
}

func TestCodexAsksAsync(t *testing.T) {
	a := feed(askAsyncCall, askAsyncItem, askAsyncOut, askDone)
	open := a.Open()
	if len(open) != 1 || !open[0].Async || open[0].CallID != "call_a1" || len(open[0].Questions) != 1 {
		t.Fatalf("open %+v", open)
	}
	q := open[0].Questions[0]
	if q.Question != "Which color should the banner be?" || len(q.Options) != 3 || q.Options[1].Label != "Green" {
		t.Errorf("question %+v", q)
	}
	// Its {"accepted":true} output and the turn's end leave it open; the reply closes it.
	reply := CodexReplyMessage([]CodexReply{{QuestionItemID: QuestionItemID("call_a1", 0), Question: q.Question, Answer: "Green"}})
	a.Feed([]byte(userLine(reply)))
	if len(a.Open()) != 0 {
		t.Errorf("still open after the reply: %+v", a.Open())
	}
}

// The item alone (a compatible CLI, or a rollout without the function_call line) opens it too.
func TestCodexAsksItemOnly(t *testing.T) {
	if open := feed(askAsyncItem).Open(); len(open) != 1 || open[0].Questions[0].Options[2].Label != "Blue" {
		t.Errorf("%+v", open)
	}
	traex := `{"timestamp":"2026-11-01T13:40:29.650Z","type":"history_mutation","payload":{"display_completions":[{"item":{"type":"AgentMessage","id":"call_t1","delivery":"async","questions":[{"title":"Ship it?"}]}}]}}`
	a := feed(traex)
	if open := a.Open(); len(open) != 1 || open[0].Questions[0].Question != "Ship it?" || len(open[0].Questions[0].Options) != 0 {
		t.Errorf("traex %+v", open)
	}
	a.Feed([]byte(`{"type":"history_mutation","payload":{"display_completions":[{"item":{"type":"UserMessage","content":[{"type":"text","text":"yes"}]}}]}}`))
	if len(a.Open()) != 0 {
		t.Errorf("traex reply: %+v", a.Open())
	}
}

// Two questions in one call: the TUI answers each with its own message; the call stays open for the other.
func TestCodexAsksPartialReply(t *testing.T) {
	two := strings.Replace(askAsyncItem, `"options":["Red","Green","Blue"]}]`, `"options":["Red","Green","Blue"]},{"title":"How tall?"}]`, 1)
	a := feed(askAsyncCall, two)
	if open := a.Open(); len(open) != 1 || len(open[0].Questions) != 2 {
		t.Fatalf("%+v", open)
	}
	a.Feed([]byte(userLine(CodexReplyMessage([]CodexReply{{QuestionItemID: QuestionItemID("call_a1", 1), Question: "How tall?", Answer: "Small"}}))))
	open := a.Open()
	if len(open) != 1 || len(open[0].Questions) != 1 || open[0].Questions[0].Index != 0 {
		t.Fatalf("after the second is answered: %+v", open)
	}
	a.Feed([]byte(userLine(CodexReplyMessage([]CodexReply{{QuestionItemID: QuestionItemID("call_a1", 0), Answer: "Red"}}))))
	if len(a.Open()) != 0 {
		t.Errorf("after both: %+v", a.Open())
	}
}

func TestCodexAsksMovedOn(t *testing.T) {
	// Any other message from the user (typed at the computer, a 0.153 TUI without the question pane) closes it.
	if open := feed(askAsyncCall, askAsyncItem, userLine("use green, and also fix the footer")).Open(); len(open) != 0 {
		t.Errorf("%+v", open)
	}
	// An older desktop reply names the whole item.
	old := `<send_user_message_question_reply>[{"questionItemId":"call_a1","question":"Which color?","answer":"Red"}]</send_user_message_question_reply>`
	if open := feed(askAsyncItem, userLine(old)).Open(); len(open) != 0 {
		t.Errorf("item-wide reply: %+v", open)
	}
	// A reply to another call leaves this one.
	other := CodexReplyMessage([]CodexReply{{QuestionItemID: QuestionItemID("call_zz", 0), Answer: "x"}})
	if open := feed(askAsyncItem, userLine(other)).Open(); len(open) != 1 {
		t.Errorf("other call's reply closed it: %+v", open)
	}
	if open := feed(askAsyncItem, askAborted).Open(); len(open) != 0 {
		t.Errorf("aborted: %+v", open)
	}
}

func TestCodexAsksPlanMode(t *testing.T) {
	a := feed(askSyncCall)
	open := a.Open()
	if len(open) != 1 || open[0].Async || len(open[0].Questions) != 2 {
		t.Fatalf("%+v", open)
	}
	q := open[0].Questions[1]
	if q.ID != "size" || q.Header != "Size" || q.Question != "How tall should it be?" || q.Options[1].Description != "two lines" {
		t.Errorf("%+v", q)
	}
	// A user message does not answer the overlay; the call's output does.
	a.Feed([]byte(userLine("hold on")))
	if len(a.Open()) != 1 {
		t.Fatalf("closed by a message: %+v", a.Open())
	}
	a.Feed([]byte(askSyncOut))
	if len(a.Open()) != 0 {
		t.Errorf("open after its output: %+v", a.Open())
	}
	if open := feed(askSyncCall, askAborted).Open(); len(open) != 0 {
		t.Errorf("aborted: %+v", open)
	}
	// In Default mode the call fails at once: its output closes it.
	failed := `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_s1","output":"request_user_input is unavailable in Default mode"}}`
	if open := feed(askSyncCall, failed).Open(); len(open) != 0 {
		t.Errorf("failed call: %+v", open)
	}
}

func TestCodexReplyMessage(t *testing.T) {
	m := CodexReplyMessage([]CodexReply{{QuestionItemID: QuestionItemID("call_a1", 0), Question: "Tags <b> & co?", Answer: "a\nb @x"}})
	if strings.Contains(m, "\n") || strings.Contains(m, "\\u003c") || !strings.Contains(m, "<b> & co") || !strings.HasPrefix(m, "<send_user_message_question_reply>[{") {
		t.Errorf("%s", m)
	}
	if got, _ := codexUserPart(m); got != "a\nb @x" {
		t.Errorf("reads back as %q", got)
	}
	if call, i := ParseQuestionItemID(QuestionItemID("call_a1", 2)); call != "call_a1" || i != 2 {
		t.Errorf("%s %d", call, i)
	}
}

// A call Codex rejects (the model wrote arguments it cannot parse, then retries) asks nothing: only the retry is open.
func TestCodexAsksRejectedCall(t *testing.T) {
	bad := `{"timestamp":"2026-11-02T11:34:07.199Z","type":"response_item","payload":{"type":"function_call","name":"request_user_input_async","arguments":"{\"questions\":[{\"question\":\"Which fruit?\"}]}","call_id":"call_bad"}}`
	badOut := `{"timestamp":"2026-11-02T11:34:07.249Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_bad","output":"failed to parse function arguments: unknown field ` + "`question`" + `"}}`
	open := feed(bad, badOut, askAsyncCall, askAsyncItem, askAsyncOut).Open()
	if len(open) != 1 || open[0].CallID != "call_a1" {
		t.Fatalf("%+v", open)
	}
	// Even if a rejected call's item showed up, its error output closes it.
	item := strings.Replace(askAsyncItem, "call_a1", "call_bad", 1)
	if open := feed(item, badOut).Open(); len(open) != 0 {
		t.Errorf("rejected yet open: %+v", open)
	}
}
