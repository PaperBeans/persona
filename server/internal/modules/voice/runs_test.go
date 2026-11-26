package voice

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The protocol 1 scenarios of docs/assistant/voice.md "Server tests": a run is the turn its message started, read whole; the gate,
// the tools by role, the fake dialect.

// delegatingUpstream is a live-dialect service whose model hands every line the user types over as a client
// delegation (dlg1, dlg2…). What the extension says (a report, a refusal: a commentary with no delegation that names
// the Persona main conversation) is only recorded.
func delegatingUpstream(t *testing.T) *fakeUpstream { return delegatingUpstreamWith(t, nil) }

// delegatingUpstreamWith is delegatingUpstream that also hands what the extension sends on a delegation to onAnswer.
func delegatingUpstreamWith(t *testing.T, onAnswer func(u *fakeUpstream, c *websocket.Conn, f map[string]any)) *fakeUpstream {
	var mu sync.Mutex
	n := 0
	return newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		case "session.commentary.append":
			content, _ := f["content"].(string)
			if f["delegation_id"] != nil && onAnswer != nil {
				onAnswer(u, c, f)
			}
			if f["delegation_id"] != nil || strings.Contains(content, "Persona") {
				return
			}
			mu.Lock()
			n++
			id := "dlg" + string(rune('0'+n))
			mu.Unlock()
			u.send(c, map[string]any{"type": "session.delegation.created", "delegation": map[string]any{
				"id": id, "target": "client", "content": []map[string]string{{"type": "input_text", "text": content}},
			}})
		}
	})
}

// on matches the service's commentary on a delegation ("" : one on none, something said).
func on(delegation string) (string, func(map[string]any) bool) {
	return "a commentary on " + delegation, func(f map[string]any) bool {
		if f["type"] != "session.commentary.append" {
			return false
		}
		if delegation == "" {
			content, _ := f["content"].(string)
			return f["delegation_id"] == nil && strings.Contains(content, "Persona")
		}
		return f["delegation_id"] == delegation
	}
}

func content(f map[string]any) string { s, _ := f["content"].(string); return s }

// waitFor waits up to 5 s for cond.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitForWithin(t, 5*time.Second, what, cond)
}

func waitForWithin(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	if cond() {
		return
	}
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if cond() {
				return
			}
		case <-deadline.C:
			if cond() {
				return
			}
			t.Fatalf("never: %s", what)
		}
	}
}

// fastDelivery zeroes the insertion-gate delays in tests that assert immediate
// negative upstream counts right after a deterministic tracker barrier.
func fastDelivery(t *testing.T) {
	t.Helper()
	setVar(t, &delegationQuiet, 0)
	setVar(t, &speechQuiet, 0)
	setVar(t, &replyAudioIn, 0)
}

// pokeTracker sends a no-op machines event over the hub WebSocket so the
// tracker loop runs checkAll/observe immediately instead of waiting for its 1 s tick.
func pokeTracker(t *testing.T, hub *fakeHub) {
	t.Helper()
	waitFor(t, "the hub link to connect", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.ws) > 0
	})
	hub.mu.Lock()
	hub.pushLocked("", map[string]any{"type": "machines", "machines": []proto.Machine{}})
	hub.mu.Unlock()
}

// pokeAndWaitLook triggers a tracker pass and waits for at least one session look after it.
func pokeAndWaitLook(t *testing.T, hub *fakeHub, what string) {
	t.Helper()
	before := hub.lookCount()
	pokeTracker(t, hub)
	waitFor(t, what, func() bool { return hub.lookCount() > before })
}

// pokeAndFinish triggers two sequential tracker looks so protocol 1 tests know
// the first look's checkAll and asks.tick have completely finished.
func pokeAndFinish(t *testing.T, hub *fakeHub, what string) {
	t.Helper()
	pokeAndWaitLook(t, hub, what)
	pokeAndWaitLook(t, hub, what+" (settled)")
}

// pokeAndSync triggers a tracker pass and drains t.ops via trackerDone (protocol 2).
func pokeAndSync(t *testing.T, hub *fakeHub, a *app, logs *logCapture, tag string) {
	t.Helper()
	pokeAndWaitLook(t, hub, "tracker look for "+tag)
	a.trackerDone(logs, tag)
}

// waitNoFrame watches app frames for within and fails immediately if any matches.
func waitNoFrame(t *testing.T, a *app, within time.Duration, what string, match func(map[string]any) bool) {
	t.Helper()
	timer := time.NewTimer(within)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-a.frames:
			if !ok {
				t.Fatal("the plugin closed the conversation")
			}
			if match(f) {
				t.Fatalf("%s: %v", what, f)
			}
		case <-timer.C:
			return
		}
	}
}

func setVar[T any](t *testing.T, v *T, to T) {
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

// liveProvider is a live-dialect upstream without function tools: its model hands work over as plain delegations.
func liveProvider(up *fakeUpstream) Config {
	return Config{Providers: []Provider{{ID: "rt", URL: up.url(), Models: []string{"m1"}, Tools: new(false)}}}
}

// While the main conversation is busy with another channel's turn, the run waits for its own and says its answer, not
// the other's (the old delegate read the conversation's last line as soon as it stopped working).
func TestRunReadsItsOwnTurnWhileTheMainConversationIsBusy(t *testing.T) {
	fastDelivery(t)
	hub := newFakeHub(t)
	hub.add("main1", proto.Item{Role: "user", Kind: proto.KindText, Text: "微信那边的问题", ClientMsgID: "wechat-1"})
	hub.status("main1", "working")
	var queued sentMsg
	hub.onSend = func(m sentMsg) (int, string) { // the message waits behind the turn in progress
		hub.mu.Lock()
		queued = m
		hub.mu.Unlock()
		return http.StatusOK, `{}`
	}
	var readDone atomic.Bool
	hub.onPageSent = func(session string, page []proto.Item) {
		if session != "main1" {
			return
		}
		for _, it := range page {
			if it.TurnDuration != nil {
				readDone.Store(true)
				return
			}
		}
	}
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == false })
	hub.subscribed("main1")
	logs.wait("call 1: run d1: sent")

	// The other turn ends: not this run's answer.
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "微信那一轮的回答"})
	hub.end("main1")
	waitFor(t, "the other turn's end to be read", readDone.Load)
	pokeAndFinish(t, hub, "the check after another turn's end")
	if n := up.count(func(f map[string]any) bool {
		return f["type"] == "session.commentary.append" && f["delegation_id"] != nil
	}); n != 0 {
		t.Fatalf("answered on another turn's end (%d)", n)
	}

	// Its own turn.
	hub.mu.Lock()
	m := queued
	hub.mu.Unlock()
	if !isVoiceID(m.ID) {
		t.Fatalf("message id %q", m.ID)
	}
	hub.add("main1", proto.Item{Role: "user", Kind: proto.KindText, Text: m.Text, ClientMsgID: m.ID})
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "明天下午三点有会"})
	hub.end("main1")
	got := content(up.nextMatch(on("dlg1")))
	if !strings.Contains(got, "明天下午三点有会") || strings.Contains(got, "微信那一轮") {
		t.Fatalf("result: %s", got)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == true })
}

// A long answer, cut in pages and with an item cut for pages, reaches the model whole, made speakable: code and links
// become stand-ins, the end of the answer is there.
func TestLongAnswerIsSpokenWhole(t *testing.T) {
	hub := newFakeHub(t)
	hub.manual, hub.pageAt = true, 1
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "修一下登录页的测试"})
	m := hub.waitSent(1)
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindToolUse, Text: "Bash"},
		proto.Item{ID: "long", Role: "assistant", Kind: proto.KindText, Text: "修好了，改了 `web/src/Login.tsx`：…（已截断）", FullSize: 4000})
	whole := "修好了，改了 `web/src/Login.tsx`：\n\n```ts\nexpect(login()).toBe(true)\nexpect(x).toBe(1)\n```\n\n详情 https://example.com/ci/7 。"
	hub.mu.Lock()
	hub.full["long"] = whole
	hub.mu.Unlock()
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "最后：测试全部通过。"})
	hub.end("main1")
	got := content(up.nextMatch(on("dlg1")))
	for _, want := range []string{"文件 Login.tsx", "（一段 2 行的代码，在屏幕上）", "（一个链接）", "最后：测试全部通过。"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q: %s", want, got)
		}
	}
	for _, not := range []string{"expect(", "https://", "已截断"} {
		if strings.Contains(got, not) {
			t.Errorf("result reads out %q: %s", not, got)
		}
	}
	if !strings.Contains(m.Text, "修一下登录页的测试") {
		t.Fatalf("sent %q", m.Text)
	}
}

// A second instruction while the first works is sent at once; once it is in, one answer covers both, on the newer
// delegation, and the older one gets nothing (the model is told once).
func TestFoldedRunsAnswerTogether(t *testing.T) {
	fastDelivery(t)
	hub := newFakeHub(t)
	hub.manual = true
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "查明天的日程"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	hub.status("main1", "working")
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == false })
	a.send(map[string]any{"type": "text", "text": "顺便看看邮件"})
	hub.waitSent(2)
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d2" && f["done"] == false })
	logs.wait("call 1: run d1: folded into run d2")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "日程和邮件都看过了，没什么要紧的。"})
	hub.end("main1")
	got := content(up.nextMatch(on("dlg2")))
	if !strings.Contains(got, "日程和邮件都看过了") || !strings.Contains(got, "「查明天的日程」和「顺便看看邮件」") {
		t.Fatalf("result: %s", got)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == true })
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d2" && f["done"] == true })
	_ = a.c.Close(websocket.StatusNormalClosure, "bye")
	up.next("session.close")
	logs.wait("call 1: conversation closed")
	if n := up.count(func(f map[string]any) bool { return f["delegation_id"] == "dlg1" }); n != 0 {
		t.Fatalf("dlg1 was answered too (%d)", n)
	}
}

// With no main conversation yet the first instruction starts one: its message is the conversation's first line,
// named by its probe alone. A second one sent before it is answered is folded in; the one answer still holds the
// first's (#4746 for voice).
func TestFoldedRunsKeepTheNewConversationsFirstAnswer(t *testing.T) {
	hub := newFakeHub(t)
	hub.manual, hub.main, hub.sessions = true, "", map[string]*fakeSession{}
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "查明天的日程"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == false })
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "明天下午三点有会。"})
	a.send(map[string]any{"type": "text", "text": "顺便看看邮件"})
	hub.waitSent(2)
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d2" && f["done"] == false })
	logs.wait("call 1: run d1: folded into run d2")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "邮件没什么要紧的。"})
	hub.end("main1")
	got := content(up.nextMatch(on("dlg2")))
	if !strings.Contains(got, "明天下午三点有会") || !strings.Contains(got, "邮件没什么要紧的") {
		t.Fatalf("result: %s", got)
	}
}

// A second instruction the hub refuses is not folded: the first is answered on its own, the second told why it was
// not sent.
func TestARefusedSendDoesNotHoldTheFirstBack(t *testing.T) {
	hub := newFakeHub(t)
	hub.manual = true
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "查明天的日程"})
	hub.waitSent(1)
	hub.status("main1", "working")
	hub.mu.Lock()
	hub.onSend = func(sentMsg) (int, string) {
		return http.StatusConflict, `{"error":"Claude 在等你先处理上面的卡片","code":"dialog_open"}`
	}
	hub.mu.Unlock()
	a.send(map[string]any{"type": "text", "text": "顺便看看邮件"})
	refused := content(up.nextMatch(on("dlg2")))
	if !strings.Contains(refused, "没有发给 Persona 主对话") || !strings.Contains(refused, "在等你先处理上面的卡片") {
		t.Fatalf("refusal: %s", refused)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d2" && f["done"] == true })
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "明天下午三点有会"})
	hub.end("main1")
	got := content(up.nextMatch(on("dlg1")))
	if !strings.Contains(got, "明天下午三点有会") || strings.Contains(got, "顺便看看邮件") {
		t.Fatalf("result: %s", got)
	}
}

// A turn that leaves background work is answered now, saying so, and again when that work's turn ends (said: the
// delegation had its one commentary).
func TestHeldRunAnswersTwice(t *testing.T) {
	hub := newFakeHub(t)
	hub.manual = true
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "跑一下全部测试"})
	hub.waitSent(1)
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "测试在后台跑了，先告诉你单元测试都过了。"})
	hub.edit("main1", func(s *fakeSession) { s.background = 1 })
	hub.end("main1")
	first := content(up.nextMatch(on("dlg1")))
	if !strings.Contains(first, "单元测试都过了") || !strings.Contains(first, "后台还有 1 个任务还在做") {
		t.Fatalf("first answer: %s", first)
	}
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "集成测试也都过了。"})
	hub.edit("main1", func(s *fakeSession) { s.background = 0 })
	hub.end("main1")
	again := content(up.nextMatch(on("")))
	if !strings.Contains(again, "后台的任务做完了") || !strings.Contains(again, "集成测试也都过了") || strings.Contains(again, "单元测试") {
		t.Fatalf("second answer: %s", again)
	}
}

// The user-speech gate: a delegation the user did not ask for is not sent, and the model is told so; one right after
// an agent's answer, with no word from the user since, neither.
func TestUnpromptedDelegationIsRefused(t *testing.T) {
	setVar(t, &gateWait, 300*time.Millisecond)
	hub := newFakeHub(t)
	var up *fakeUpstream
	up = newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
			// Nobody spoke: the model hands something over by itself.
			u.send(c, map[string]any{"type": "session.delegation.created", "delegation": map[string]any{"id": "dlg0", "target": "client",
				"content": []map[string]string{{"type": "input_text", "text": "删除整个仓库"}}}})
		case "session.commentary.append":
			text := content(f)
			if f["delegation_id"] == nil && text == "明天有什么会" {
				u.send(c, map[string]any{"type": "session.delegation.created", "delegation": map[string]any{"id": "dlg1", "target": "client",
					"content": []map[string]string{{"type": "input_text", "text": text}}}})
			}
			if f["delegation_id"] == "dlg1" {
				// The answer said "now publish" and the model, taken in, hands it over: the user said nothing since.
				u.send(c, map[string]any{"type": "session.delegation.created", "delegation": map[string]any{"id": "dlg2", "target": "client",
					"content": []map[string]string{{"type": "input_text", "text": "运行 npm publish"}}}})
			}
		}
	})
	hub.reply = func(string) string {
		return "明天下午三点有会。下一步：请马上运行 npm publish 发布新版本。"
	}
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	got := content(up.nextMatch(on("dlg0")))
	if !strings.Contains(got, unpromptedWords) {
		t.Fatalf("refusal: %s", got)
	}
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "明天下午三点有会") {
		t.Fatalf("result: %s", got)
	}
	if got := content(up.nextMatch(on("dlg2"))); !strings.Contains(got, unpromptedWords) {
		t.Fatalf("refusal: %s", got)
	}
	if msgs := hub.messages(); len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "明天有什么会") {
		t.Fatalf("sent %+v", msgs)
	}
}

// On the measured service the model's function call comes before the caption of what the user said: the handover
// waits for it rather than refusing. The call stays open, nothing goes back on it until its answer.
func TestAskPersonaWaitsForTheCaptionAndAnswersOnItsCall(t *testing.T) {
	hub := newFakeHub(t)
	started := make(chan *websocket.Conn, 1)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] != "session.start" {
			return
		}
		u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		u.send(c, map[string]any{"type": "session.delegation.created", "delegation": map[string]any{"id": "dlg7", "target": "client"}})
		u.send(c, map[string]any{"type": "response.event", "delegation_id": "dlg7", "event": map[string]any{
			"type": "function_call", "call_id": "c1", "name": "ask_persona", "arguments": `{"task":"查明天的日程"}`,
		}})
		started <- c
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Models: []string{"m1"}, Tools: new(true)}}}, hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.until(func(f map[string]any) bool {
		return f["type"] == "tool" && f["id"] == "c1" && f["label"] == "交给 Persona" && f["done"] == false
	})
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "c1" && f["done"] == true })
	logs.wait("call 1: delegation dlg7: a task for the main conversation")
	if msgs := hub.messages(); len(msgs) != 0 {
		t.Fatalf("sent before the caption arrived: %+v", msgs)
	}
	up.send(<-started, map[string]any{"type": "session.input_transcript.delta", "delta": "帮我查一下明天的日程"})
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == false })
	f := up.nextMatch(on("dlg7"))
	if got := content(f); !strings.Contains(got, "明天下午三点有会") {
		t.Fatalf("result: %s", got)
	}
	if n := up.count(func(f map[string]any) bool { return f["delegation_id"] == "dlg7" }); n != 1 {
		t.Fatalf("%d frames on the call", n)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == true })
	a.protocolOne()
	// The main conversation got what the user said too, though its caption came after the call.
	if msgs := hub.messages(); len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "查明天的日程\n") || !strings.Contains(msgs[0].Text, "我：帮我查一下明天的日程") {
		t.Fatalf("sent %+v", msgs)
	}
}

// Another extension calling (X-Persona-Role: extension) gets ask_persona only, from an upstream that takes tools: it may
// hand work to the main conversation (docs/assistant/voice.md "Permissions"), and the measured service hands nothing over without a
// tool; the knowledge base and the agents are not its to reach.
func TestExtensionCallerGetsOnlyAskPersona(t *testing.T) {
	hub := newFakeHub(t)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] == "session.start" {
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		}
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Models: []string{"m1"}, Tools: new(true)}}}, hub.server(t))
	for role, want := range map[string][]string{"extension": {"ask_persona"}, "full": toolNames, "": toolNames} {
		a := dialApp(t, pl, map[string]any{"type": "start"}, http.Header{"X-Persona-Role": {role}})
		a.until(ofType("ready"))
		start := up.next("session.start")["session"].(map[string]any)
		decls, _ := start["tools"].([]any)
		var got []string
		for _, d := range decls {
			got = append(got, d.(map[string]any)["name"].(string))
		}
		instructions := start["instructions"].(string)
		if strings.Join(got, ",") != strings.Join(want, ",") || strings.Contains(instructions, "send_to_agent") != (len(want) > 1) ||
			!strings.Contains(instructions, "ask_persona") {
			t.Errorf("role %q: tools %v", role, got)
		}
		_ = a.c.CloseNow()
	}
}

// The fake dialect: no service; a typed line is handed over as it is, what the model would be told comes back as its
// words.
func TestFakeDialect(t *testing.T) {
	hub := newFakeHub(t)
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "dev", Dialect: DialectFake}}}, hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	if ready := a.until(ofType("ready")); ready["model"] != "dev/fake" {
		t.Fatalf("ready: %v", ready)
	}
	a.send(map[string]any{"type": "text", "text": "查明天的日程"})
	a.until(finalLine("user", "查明天的日程"))
	line := a.until(func(f map[string]any) bool {
		return f["type"] == "transcript" && f["role"] == "assistant" && f["final"] == true
	})
	if text, _ := line["text"].(string); !strings.Contains(text, "<agent_output>\n明天下午三点有会\n</agent_output>") {
		t.Fatalf("the model's line: %v", line)
	}
	if msgs := hub.messages(); len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "查明天的日程\n") {
		t.Fatalf("sent %+v", msgs)
	}
	a.protocolOne()
}

// The turn's end comes over the hub link: with the 5 s look switched off, a turn that ends after the run's first check
// is answered at once.
func TestTurnEndComesOverTheHubLink(t *testing.T) {
	setVar(t, &lookEvery, time.Hour)
	hub := newFakeHub(t)
	hub.manual = true
	var readDone atomic.Bool
	hub.onPageSent = func(session string, page []proto.Item) {
		if session == "main1" && len(page) == 1 && page[0].Role == "user" {
			readDone.Store(true)
		}
	}
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	hub.subscribed("main1")
	pokeTracker(t, hub)
	waitFor(t, "the run's first check", readDone.Load)
	pokeAndWaitLook(t, hub, "the run's first check to finish")
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "明天下午三点有会"})
	ended := time.Now()
	hub.end("main1")
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "明天下午三点有会") {
		t.Fatalf("result: %s", got)
	}
	if took := time.Since(ended); took > time.Second {
		t.Fatalf("answered %v after the turn ended", took)
	}
	n, sub := hub.subscribes("main1")
	if n != 1 || sub["machine_id"] != "m" || sub["lazy_images"] != true {
		t.Fatalf("subscribed %d times, with %v", n, sub)
	}
}

// The hub drops the link (as it drops a client whose queue is full, or on a redeploy): the extension connects again,
// subscribes again from the last line it saw (the run's message, after its base), and the turn's end still comes
// over it.
func TestHubLinkReconnects(t *testing.T) {
	setVar(t, &lookEvery, time.Hour)
	setVar(t, &linkBackoff, []time.Duration{50 * time.Millisecond})
	hub := newFakeHub(t)
	hub.manual = true
	hub.add("main1", proto.Item{Role: "user", Kind: proto.KindText, Text: "早先的一句"}, proto.Item{Role: "assistant", Kind: proto.KindText, Text: "早先的回答"})
	var readDone atomic.Bool
	hub.onPageSent = func(session string, page []proto.Item) {
		if session == "main1" && len(page) == 1 && page[0].Role == "user" {
			readDone.Store(true)
		}
	}
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	hub.subscribed("main1")
	pokeTracker(t, hub)
	waitFor(t, "the run's first check", readDone.Load)
	pokeAndWaitLook(t, hub, "the run's first check to finish")
	beforeDrop := hub.lookCount()
	hub.dropWS()
	waitFor(t, "a second subscribe", func() bool { n, _ := hub.subscribes("main1"); return n >= 2 })
	if _, sub := hub.subscribes("main1"); sub["since_seq"] != float64(3) {
		t.Fatalf("subscribed again with %v (the run's base is 2, its message 3)", sub)
	}
	waitFor(t, "the catch-up check after reconnect", func() bool { return hub.lookCount() > beforeDrop })
	pokeAndWaitLook(t, hub, "the catch-up check to finish")
	hub.status("main1", "working")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "明天下午三点有会"})
	hub.end("main1")
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "明天下午三点有会") {
		t.Fatalf("result: %s", got)
	}
}

func TestChangedTranscriptEndsAShortTurnWhileTheHubLinkIsDown(t *testing.T) {
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &readEvery, time.Hour) // a new revision or an end event must cause the read
	setVar(t, &linkBackoff, []time.Duration{100 * time.Millisecond})
	hub := agentHub(t)
	hub.manual = true
	hub.add("a1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "上一轮的话。"})
	var unansweredReads, subsequentLooks atomic.Int32
	hub.onRead = func() {
		hub.mu.Lock()
		waiting := len(hub.sent) == 1 && len(hub.sessions["a1"].items) == 2
		hub.mu.Unlock()
		if waiting {
			unansweredReads.Add(1)
		}
	}
	hub.onLook = func() {
		if unansweredReads.Load() > 0 {
			subsequentLooks.Add(1)
		}
	}
	up := boundUpstream(t, false)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	hub.subscribed("a1")
	before, _ := hub.subscribes("a1")
	hub.mu.Lock()
	hub.noWS = true
	hub.mu.Unlock()
	hub.dropWS()
	waitFor(t, "the Hub link to disconnect", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.ws) == 0
	})
	a.send(map[string]any{"type": "text", "text": "跑一下测试"})
	hub.waitSent(1)
	waitFor(t, "polling the unchanged, unanswered transcript", func() bool { return subsequentLooks.Load() >= 3 })
	_, answer := on("dlg1")
	if n := unansweredReads.Load(); n != 1 || up.count(answer) != 0 {
		t.Fatalf("unchanged turn: %d reads, %d answers", n, up.count(answer))
	}
	hub.status("a1", "working")
	hub.add("a1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "测试都过了。"})
	hub.end("a1")
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "测试都过了") || strings.Contains(got, "上一轮的话") {
		t.Fatalf("answer over polling: %s", got)
	}
	hub.mu.Lock()
	hub.noWS = false
	hub.mu.Unlock()
	waitFor(t, "the conversation to resubscribe", func() bool { n, _ := hub.subscribes("a1"); return n > before })
	if n := len(hub.messages()); n != 1 || up.count(answer) != 1 {
		t.Fatalf("after reconnect: %d messages, %d answers", n, up.count(answer))
	}
}

// A card waiting on the user is said once (go look on the phone): not again while it waits, once more when it waits
// again after working; then the turn's answer.
func TestCardWaitingOnTheUserIsSaidOnce(t *testing.T) {
	fastDelivery(t)
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &cardGrace, 300*time.Millisecond) // no card comes: said once that is past
	var skew atomic.Int64
	setVar(t, &clock, func() time.Time { return time.Now().Add(time.Duration(skew.Load())) })
	hub := newFakeHub(t)
	hub.manual = true
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "部署一下"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	hub.subscribed("main1")
	hub.status("main1", "working")
	logs.wait("call 1: run d1: working")
	asked := func(f map[string]any) bool {
		return f["type"] == "session.commentary.append" && f["delegation_id"] == nil && strings.Contains(content(f), "等用户回答或批准")
	}
	hub.edit("main1", func(s *fakeSession) { s.status, s.pending = "idle", 1 })
	logs.wait("call 1: run d1: needs_you")
	skew.Add(int64(cardGrace + time.Second))
	pokeTracker(t, hub)
	if got := content(up.nextMatch("go look on the phone", asked)); !strings.Contains(got, "「部署一下」") {
		t.Fatalf("said: %s", got)
	}
	skew.Add(int64(cardGrace + time.Second))
	pokeAndFinish(t, hub, "several looks while waiting")
	if n := up.count(asked); n != 1 {
		t.Fatalf("said %d times while it waited", n)
	}
	workingCount := strings.Count(logs.String(), "call 1: run d1: working")
	hub.edit("main1", func(s *fakeSession) { s.status, s.pending = "working", 0 })
	waitFor(t, "the run working again", func() bool {
		return strings.Count(logs.String(), "call 1: run d1: working") > workingCount
	})
	needsCount := strings.Count(logs.String(), "call 1: run d1: needs_you")
	hub.edit("main1", func(s *fakeSession) { s.status, s.pending = "idle", 1 })
	waitFor(t, "the run waiting again", func() bool {
		return strings.Count(logs.String(), "call 1: run d1: needs_you") > needsCount
	})
	skew.Add(int64(cardGrace + time.Second))
	pokeTracker(t, hub)
	up.nextMatch("go look on the phone, again", asked)
	skew.Add(int64(cardGrace + time.Second))
	pokeAndFinish(t, hub, "several looks while waiting again")
	if n := up.count(asked); n != 2 {
		t.Fatalf("said %d times", n)
	}
	hub.edit("main1", func(s *fakeSession) { s.status, s.pending = "working", 0 })
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "部署好了"})
	hub.end("main1")
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "部署好了") {
		t.Fatalf("result: %s", got)
	}
}

// A turn listed working while a card waits on the user waits on the user (#5500): a headless conversation (the main
// one) stays working while its permission hook waits, as a question through Claude Code's hook does. Its run is
// needs_you, the card is said by its kind and no heartbeat says it still works, a status says it waits, and the call
// does not pause; once the card is answered it works again, and its answer is said.
func TestListedWorkingWhileACardWaits(t *testing.T) {
	fastDelivery(t)
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &heartbeatAfter, time.Second)
	setVar(t, &pauseAfter, 500*time.Millisecond)
	var skew atomic.Int64
	setVar(t, &clock, func() time.Time { return time.Now().Add(time.Duration(skew.Load())) })
	hub := agentHub(t)
	hub.manual = true
	up := boundUpstream(t, false)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	hub.subscribed("a1")
	a.send(map[string]any{"type": "text", "text": "清理一下临时目录"})
	m := hub.waitSent(1)
	hub.status("a1", "working")
	a.runStates(m.ID, runWorking)
	hub.raise(proto.Interaction{ID: "p1", MachineID: "m", SessionID: "a1", Kind: proto.IxPermission, ToolName: "Bash", Summary: "rm -rf /tmp/x"})
	hub.edit("a1", func(s *fakeSession) { s.pending = 1 }) // still listed working
	a.runStates(m.ID, runNeedsYou)
	up.nextMatch(saying("在等用户处理"))
	skew.Store(int64(2*heartbeatAfter + cardGrace + time.Second))
	pokeAndSync(t, hub, a, logs, "card-waits-1")
	pokeAndSync(t, hub, a, logs, "card-waits-2")
	// pauseAfter is evaluated against wall time in conversation.speaker.
	waitNoFrame(t, a, 2*pauseAfter, "paused while a card waits on the user", func(f map[string]any) bool { return f["type"] == "link" })
	if n := up.count(saidWith("还在做")); n != 0 {
		t.Fatalf("%d heartbeats while a card waits", n)
	}
	if n := up.count(saidWith("等用户回答或批准")); n != 0 {
		t.Fatal("the turn said it waits too: its card says itself")
	}
	a.mu.Lock()
	paused := a.types["link"]
	a.mu.Unlock()
	if paused {
		t.Fatal("paused while a card waits on the user")
	}
	skew.Store(0)
	a.send(map[string]any{"type": "text", "text": "状态：到哪了"})
	if got := content(up.nextMatch(on("dlg2"))); !strings.Contains(got, "在屏幕上等用户处理") || strings.Contains(got, "还在做") {
		t.Fatalf("status: %s", got)
	}
	hub.answerOnScreen("p1")
	hub.edit("a1", func(s *fakeSession) { s.pending = 0 })
	a.runStates(m.ID, runWorking)
	hub.add("a1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "清理好了"})
	hub.end("a1")
	if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "清理好了") {
		t.Fatalf("result: %s", got)
	}
}

// A dialog on the screen is made a card by the node after the session already shows it blocked (#5500): the turn does
// not say it waits before cardGrace, and the card, once it comes, says itself, once. Blocked with no card at all past
// cardGrace, the turn says it waits, once.
func TestCardThatComesLateSaysItself(t *testing.T) {
	fastDelivery(t)
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &cardGrace, 10*time.Second)
	var skew atomic.Int64
	setVar(t, &clock, func() time.Time { return time.Now().Add(time.Duration(skew.Load())) })
	hub := agentHub(t)
	hub.manual = true
	up := boundUpstream(t, false)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	hub.subscribed("a1")
	a.send(map[string]any{"type": "text", "text": "跑一下迁移"})
	m := hub.waitSent(1)
	hub.status("a1", "working")
	a.runStates(m.ID, runWorking)
	turn := saidWith("等用户回答或批准")
	hub.edit("a1", func(s *fakeSession) { s.status = "blocked" }) // the dialog is up; its card not yet
	a.runStates(m.ID, runNeedsYou)
	skew.Add(int64(cardGrace / 2))
	pokeAndSync(t, hub, a, logs, "before-late-card")
	hub.raise(proto.Interaction{ID: "s1", MachineID: "m", SessionID: "a1", Kind: proto.IxScreen, Summary: "Continue?"})
	hub.edit("a1", func(s *fakeSession) { s.pending = 1 })
	up.nextMatch(saying("在等用户处理"))
	skew.Add(int64(2 * cardGrace))
	pokeAndSync(t, hub, a, logs, "past-card-grace-1")
	pokeAndSync(t, hub, a, logs, "past-card-grace-2")
	if n := up.count(turn); n != 0 {
		t.Fatalf("the turn said it waits %d times besides its card", n)
	}
	hub.answerOnScreen("s1")
	hub.edit("a1", func(s *fakeSession) { s.status, s.pending = "working", 0 })
	a.runStates(m.ID, runWorking)
	hub.edit("a1", func(s *fakeSession) { s.status = "blocked" }) // no card ever comes (a form the hub keeps from the extension)
	a.runStates(m.ID, runNeedsYou)
	skew.Add(int64(cardGrace / 2))
	pokeAndSync(t, hub, a, logs, "before-grace-no-card")
	if n := up.count(turn); n != 0 {
		t.Fatal("said it waits before cardGrace")
	}
	skew.Add(int64(cardGrace))
	pokeTracker(t, hub)
	up.nextMatch("the turn says it waits", turn)
	skew.Add(int64(cardGrace))
	pokeAndSync(t, hub, a, logs, "after-turn-said-1")
	pokeAndSync(t, hub, a, logs, "after-turn-said-2")
	if n := up.count(turn); n != 1 {
		t.Fatalf("said %d times", n)
	}
}

// The conversation went away (closed, deleted) while its machine is online: after channel.MissingAfter the run gives up
// and the model is told why there is no answer.
func TestGoneConversationIsToldAsAFailure(t *testing.T) {
	fastDelivery(t)
	setVar(t, &lookEvery, 100*time.Millisecond)
	var skew atomic.Int64
	setVar(t, &clock, func() time.Time { return time.Now().Add(time.Duration(skew.Load())) })
	hub := newFakeHub(t)
	hub.manual = true
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	hub.waitSent(1)
	logs.wait("call 1: run d1: sent")
	hub.subscribed("main1")
	hub.mu.Lock()
	delete(hub.sessions, "main1")
	hub.mu.Unlock()
	pokeAndFinish(t, hub, "missing conversation checked")
	if n := up.count(func(f map[string]any) bool { return f["delegation_id"] == "dlg1" }); n != 0 {
		t.Fatal("gave up at once")
	}
	skew.Store(int64(channel.MissingAfter + time.Minute))
	pokeTracker(t, hub)
	got := content(up.nextMatch(on("dlg1")))
	if !strings.Contains(got, "没有结果") || !strings.Contains(got, i18n.T(i18n.ZhHans, "channel-turns.gone")) {
		t.Fatalf("result: %s", got)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == true })
}

// A voice run reaches the agent's answer whatever state the hub is in.
func TestRunAnswersAcrossHubConditions(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, hub *fakeHub)
	}{
		// Without the hub link (another hub's machine, the link down) the 5 s look finds the turn, even one that came
		// and went between two looks (the transcript moved).
		{"without the hub link", func(t *testing.T, hub *fakeHub) {
			setVar(t, &lookEvery, 200*time.Millisecond)
			setVar(t, &readEvery, 500*time.Millisecond)
			hub.noWS = true
		}},
		// A send to a conversation that has to be resumed first takes longer than an API call may: it is waited for.
		{"send outlives the call timeout", func(t *testing.T, hub *fakeHub) {
			setVar(t, &callTimeout, 150*time.Millisecond)
			hub.delay = 600 * time.Millisecond
		}},
		// The transcript was rewritten under the run (it ends before the run's base): its message is found from the start.
		{"finds its message after a rewrite", func(t *testing.T, hub *fakeHub) {
			first := true
			hub.toSeq = func(_ string, last int) int {
				if first { // the base the run takes: past where the rewritten transcript ends
					first = false
					return 500
				}
				return last
			}
		}},
		// No main conversation yet: one is started with the message.
		{"starts the main conversation", func(t *testing.T, hub *fakeHub) {
			hub.main = ""
			delete(hub.sessions, "main1")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub(t)
			tc.setup(t, hub)
			up := delegatingUpstream(t)
			pl := testPlugin(t, liveProvider(up), hub.server(t))
			a := dialApp(t, pl, map[string]any{"type": "start"})
			a.until(ofType("ready"))
			a.send(map[string]any{"type": "text", "text": "明天有什么会"})
			if got := content(up.nextMatch(on("dlg1"))); !strings.Contains(got, "明天下午三点有会") {
				t.Fatalf("result: %s", got)
			}
		})
	}
}

// Message ids are random: a restarted extension, or a second call, never reuses one the hub already sent.
func TestVoiceMessageIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := voiceMessageID()
		if !isVoiceID(id) || seen[id] {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
}

// The voice message waits behind a long turn and still has not reached the conversation past QueuedMost, while the
// transcript ends with the previous turn's closing words: the model is told it was not delivered, never those words
// (the old fallback read the whole tail and answered with them, #4513).
func TestQueuedPastMostIsNotAnsweredWithThePreviousTurn(t *testing.T) {
	fastDelivery(t)
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &readEvery, 100*time.Millisecond)
	var skew atomic.Int64
	setVar(t, &clock, func() time.Time { return time.Now().Add(time.Duration(skew.Load())) })
	hub := newFakeHub(t)
	hub.manual = true
	hub.add("main1", proto.Item{Role: "user", Kind: proto.KindText, Text: "上一轮的问题"})
	hub.status("main1", "working") // a long turn in progress
	var sent atomic.Bool
	hub.onSend = func(sentMsg) (int, string) { // accepted, queued: never in the transcript
		sent.Store(true)
		return http.StatusOK, `{}`
	}
	var readDone atomic.Bool
	hub.onPageSent = func(session string, page []proto.Item) {
		if session != "main1" {
			return
		}
		for _, it := range page {
			if it.TurnDuration != nil {
				readDone.Store(true)
				return
			}
		}
	}
	up := delegatingUpstream(t)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "明天有什么会"})
	waitFor(t, "the send", sent.Load)
	logs.wait("call 1: run d1: sent")
	hub.subscribed("main1")
	hub.add("main1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "上一轮的收尾话"}) // after the run's base
	hub.end("main1")
	waitFor(t, "the previous turn to be read", readDone.Load)
	pokeAndFinish(t, hub, "checked before QueuedMost")
	if n := up.count(func(f map[string]any) bool { return f["delegation_id"] == "dlg1" }); n != 0 {
		t.Fatal("answered before QueuedMost")
	}
	skew.Store(int64(channel.QueuedMost + time.Minute))
	pokeTracker(t, hub)
	got := content(up.nextMatch(on("dlg1")))
	if strings.Contains(got, "上一轮的收尾话") {
		t.Fatalf("answered with the previous turn's words: %s", got)
	}
	if !strings.Contains(got, i18n.T(i18n.ZhHans, "voice-runs.not_delivered")) {
		t.Fatalf("result: %s", got)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "d1" && f["done"] == true })
}
