package voice

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nwverse/persona/server/internal/proto"
)

// Both a live completion and a delayed read of an already completed turn must deliver the full
// answer after all the caller's PCM frames made it to the real upstream connection.
func requireSpokenAfterVoiceFrames(t *testing.T, up *fakeUpstream, want string, pcm []byte, frames int) time.Time {
	t.Helper()
	answer := up.nextMatch("the answer, said", said)
	received := time.Now()
	if got := content(answer); !strings.Contains(got, want) {
		t.Fatalf("wrong answer: %s", got)
	}
	encoded := b64(pcm)
	if n := up.count(func(f map[string]any) bool { return f["type"] == "session.input_audio.append" && f["audio"] == encoded }); n != frames {
		t.Fatalf("the upstream received %d voiced frames, want %d", n, frames)
	}
	if n := up.count(said); n != 1 {
		t.Fatalf("the upstream received %d spoken answers, want one", n)
	}
	return received
}

// Here the Agent finished before the call resumed: its pending items read returns only after
// the first real microphone frame. The speaker must wait a full quiet interval after the last frame.
func TestResumedAnswerReadDuringSpeechWaitsForTheLastFrame(t *testing.T) {
	setVar(t, &userQuiet, 400*time.Millisecond)
	hub := agentHub(t)
	id := voiceMessageID()
	hub.add("a1", proto.Item{Role: "user", Kind: proto.KindText, Text: "跑一下全部测试", ClientMsgID: id},
		proto.Item{Role: "assistant", Kind: proto.KindText, Text: "全部测试都过了。"})
	hub.end("a1")
	// A resumed answer may go before the microphone has sent anything. Let this transcript read finish during speech.
	firstVoice := make(chan struct{})
	var reading atomic.Bool
	hub.onRead = func() {
		reading.Store(true)
		select {
		case <-firstVoice:
		case <-t.Context().Done():
		}
	}
	up := boundUpstream(t, false)
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	a := dialApp(t, pl, boundStart("a1", map[string]any{"resume": map[string]any{"runs": []map[string]any{
		{"id": id, "msg_ids": []string{id}, "task": "跑一下全部测试", "base_seq": 0, "started_at": stamp(time.Now().Add(-time.Minute)), "state": runSent},
	}}}))
	a.until(ofType("ready"))
	waitFor(t, "the pending resumed transcript read", reading.Load)
	pcm := tone(1600, 3000)
	encoded := b64(pcm)
	voiceFrame := func(f map[string]any) bool { return f["type"] == "session.input_audio.append" && f["audio"] == encoded }
	var lastFrame time.Time
	frames := time.NewTicker(100 * time.Millisecond)
	defer frames.Stop()
	for i := range 25 { // 2.5 seconds of PCM16 at 16 kHz
		if i != 0 {
			<-frames.C
		}
		lastFrame = time.Now()
		if err := a.c.Write(t.Context(), websocket.MessageBinary, pcm); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			waitFor(t, "the first voiced frame at the upstream", func() bool { return up.count(voiceFrame) == 1 })
			close(firstVoice)
		}
	}
	if n := up.count(said); n != 0 {
		t.Fatal("said while the user talked")
	}
	received := requireSpokenAfterVoiceFrames(t, up, "全部测试都过了", pcm, 25)
	if took := received.Sub(lastFrame); took < userQuiet {
		t.Fatalf("said %v after the user stopped", took)
	}
}
