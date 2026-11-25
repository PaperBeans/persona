package voice

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestReplyAudioLogLine(t *testing.T) {
	base := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

	t.Run("empty", func(t *testing.T) {
		var r replyAudio
		if got := r.logLine("peach", false); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		r.start(base)
		if got := r.logLine("peach", false); got != "" {
			t.Fatalf("silent reply logged %q, want empty", got)
		}
	})

	t.Run("single short chunk", func(t *testing.T) {
		var r replyAudio
		r.start(base)
		// 384 ms at 16 kHz PCM16 mono = 12288 bytes.
		r.add(base.Add(180*time.Millisecond), 12288, 16000)
		got := r.logLine("", true)
		want := `upstream audio (voice "default", 16000 Hz): 384ms in 0ms (too quickly to measure), 1 chunk of 384ms, ttfa 180ms, cut off`
		if got != want {
			t.Fatalf("got:\n  %s\nwant:\n  %s", got, want)
		}
	})

	t.Run("fast 4x stream", func(t *testing.T) {
		var r replyAudio
		r.start(base)
		// 40 chunks of 384 ms (12288 B at 16 kHz) arriving every 96 ms (~4x real time).
		// Span = 39 * 96 ms = 3744 ms; lead = 39 * 384 ms = 14976 ms -> 4.00x.
		for i := 0; i < 40; i++ {
			at := base.Add(240*time.Millisecond + time.Duration(i)*96*time.Millisecond)
			r.add(at, 12288, 16000)
		}
		got := r.logLine("peach_v1", false)
		want := `upstream audio (voice "peach_v1", 16000 Hz): 15360ms in 3744ms (4.00x), 40 chunks of 384ms, max gap 96ms, ttfa 240ms, over`
		if got != want {
			t.Fatalf("got:\n  %s\nwant:\n  %s", got, want)
		}
	})

	t.Run("slow bursty v2 stream at 24 kHz", func(t *testing.T) {
		var r replyAudio
		r.start(base)
		// Variable chunks at 24 kHz PCM16 mono: 320 ms (15360 B) and 480 ms (23040 B),
		// with a 1620 ms mid-reply synthesis gap and overall arrival slower than real time.
		chunks := []struct {
			at    time.Duration
			bytes int
		}{
			{680 * time.Millisecond, 15360},  // 320 ms
			{1300 * time.Millisecond, 23040}, // 480 ms, gap 620 ms
			{2920 * time.Millisecond, 15360}, // 320 ms, gap 1620 ms
			{3580 * time.Millisecond, 23040}, // 480 ms, gap 660 ms
			{4280 * time.Millisecond, 15360}, // 320 ms, gap 700 ms
		}
		for _, c := range chunks {
			r.add(base.Add(c.at), c.bytes, 24000)
		}
		// Total = 1920 ms, lead (excluding last 320 ms) = 1600 ms, span = 3600 ms -> 0.44x.
		got := r.logLine("peach_v2", false)
		want := `upstream audio (voice "peach_v2", 24000 Hz): 1920ms in 3600ms (0.44x), 5 chunks 320-480ms, max gap 1620ms, ttfa 680ms, over`
		if got != want {
			t.Fatalf("got:\n  %s\nwant:\n  %s", got, want)
		}
	})

	t.Run("no evReply omits ttfa", func(t *testing.T) {
		var r replyAudio
		r.add(base, 12288, 16000)
		r.add(base.Add(2000*time.Millisecond), 12288, 16000)
		got := r.logLine("peach_v2", false)
		want := `upstream audio (voice "peach_v2", 16000 Hz): 768ms in 2000ms (0.19x), 2 chunks of 384ms, max gap 2000ms, over`
		if got != want {
			t.Fatalf("got:\n  %s\nwant:\n  %s", got, want)
		}
	})
}

func pcmChunkB64(bytes int) string {
	return base64.StdEncoding.EncodeToString(make([]byte, bytes))
}

func replyLifecycleEvent(id, typ string) map[string]any {
	return map[string]any{
		"type":          "response.event",
		"delegation_id": id,
		"event":         map[string]any{"type": typ},
	}
}

// Protocol 2 calls advertise "reply", emit paired reply{start/end} frames around model replies, send clear before
// reply{end} on interruptions (both upstream incomplete and user interrupt), ignore superseded incomplete frames
// without duplicate end frames, and log per-reply upstream audio pacing without any speech text (#14932).
func TestReplyFramesAndUpstreamAudioLog(t *testing.T) {
	const secretWords = "secret-transcript-phrase-91"
	hub := agentHub(t)
	chunk := pcmChunkB64(12288) // 384 ms at 16 kHz

	type step struct {
		fn func(u *fakeUpstream, c *websocket.Conn)
	}
	steps := make(chan step, 16)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] != "session.start" {
			return
		}
		u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		go func() {
			for st := range steps {
				st.fn(u, c)
			}
		}()
	})
	t.Cleanup(func() { close(steps) })

	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", map[string]any{"voice": "peach_v2"}))
	ready := a.until(ofType("ready"))
	if feats, _ := ready["features"].([]any); !slices.Contains(feats, any("reply")) {
		t.Fatalf("ready lacks reply feature: %v", ready["features"])
	}

	// Reply 1: normal reply with two audio chunks, followed by a superseded incomplete (must emit no clear / no duplicate end).
	steps <- step{func(u *fakeUpstream, c *websocket.Conn) {
		u.send(c, replyLifecycleEvent("r1", "response.created"))
		u.send(c, map[string]any{"type": "session.output_transcript.delta", "delta": secretWords})
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
		u.send(c, replyLifecycleEvent("r1", "response.completed"))
		u.send(c, replyLifecycleEvent("r1", "response.incomplete")) // superseded
	}}

	nextFrame := func() map[string]any {
		t.Helper()
		select {
		case f, ok := <-a.frames:
			if !ok {
				t.Fatal("app socket closed unexpectedly")
			}
			return f
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for frame")
			return nil
		}
	}
	readAudio := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			select {
			case b := <-a.audio:
				if len(b) != 12288 {
					t.Fatalf("audio chunk %d len = %d, want 12288", i, len(b))
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("timed out waiting for audio chunk %d", i)
			}
		}
	}

	// Collect non-transcript frames through reply 1 end.
	var r1 []string
	for len(r1) < 2 {
		f := nextFrame()
		if f["type"] == "transcript" {
			continue
		}
		r1 = append(r1, fmt.Sprintf("%s:%v", f["type"], f["state"]))
	}
	readAudio(2)
	if !slices.Equal(r1, []string{"reply:start", "reply:end"}) {
		t.Fatalf("reply 1 frames = %v, want [reply:start reply:end]", r1)
	}
	logs.wait(`call 1: upstream audio (voice "peach_v2", 16000 Hz): 768ms in`, "2 chunks of 384ms", "max gap", "ttfa", ", over")

	// Reply 2: interrupted by upstream (response.incomplete) -> must send clear BEFORE reply{end}.
	// Any stray frame from r1's superseded incomplete would appear right before r2's reply:start and fail this check.
	steps <- step{func(u *fakeUpstream, c *websocket.Conn) {
		u.send(c, replyLifecycleEvent("r2", "response.created"))
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
		u.send(c, replyLifecycleEvent("r2", "response.incomplete"))
	}}
	var r2 []string
	for len(r2) < 3 {
		f := nextFrame()
		if f["type"] == "transcript" {
			continue
		}
		r2 = append(r2, fmt.Sprintf("%s:%v", f["type"], f["state"]))
	}
	readAudio(1)
	if !slices.Equal(r2, []string{"reply:start", "clear:<nil>", "reply:end"}) {
		t.Fatalf("reply 2 frames = %v, want [reply:start clear:<nil> reply:end]", r2)
	}
	logs.wait(`call 1: upstream audio (voice "peach_v2", 16000 Hz): 384ms in`, "1 chunk of 384ms", "ttfa", ", cut off")

	// Reply 3: interrupted by app {"type":"interrupt"} -> clear BEFORE reply{end}, then late response.completed is a no-op.
	steps <- step{func(u *fakeUpstream, c *websocket.Conn) {
		u.send(c, replyLifecycleEvent("r3", "response.created"))
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
	}}
	if f := nextFrame(); f["type"] != "reply" || f["state"] != "start" {
		t.Fatalf("reply 3 start = %v", f)
	}
	readAudio(1)
	a.send(map[string]any{"type": "interrupt"})
	var r3 []string
	for len(r3) < 2 {
		f := nextFrame()
		if f["type"] == "transcript" {
			continue
		}
		r3 = append(r3, fmt.Sprintf("%s:%v", f["type"], f["state"]))
	}
	if !slices.Equal(r3, []string{"clear:<nil>", "reply:end"}) {
		t.Fatalf("reply 3 interrupt frames = %v, want [clear:<nil> reply:end]", r3)
	}
	// Late completed for r3 followed by r4: verifies r3's late completed emits no extra reply:end.
	steps <- step{func(u *fakeUpstream, c *websocket.Conn) {
		u.send(c, replyLifecycleEvent("r3", "response.completed"))
		u.send(c, replyLifecycleEvent("r4", "response.created"))
		u.send(c, replyLifecycleEvent("r4", "response.completed"))
	}}
	var r4 []string
	for len(r4) < 2 {
		f := nextFrame()
		if f["type"] == "transcript" {
			continue
		}
		r4 = append(r4, fmt.Sprintf("%s:%v", f["type"], f["state"]))
	}
	if !slices.Equal(r4, []string{"reply:start", "reply:end"}) {
		t.Fatalf("reply 4 frames = %v, want [reply:start reply:end] (no duplicate end from r3)", r4)
	}

	_ = a.c.Close(websocket.StatusNormalClosure, "bye")
	logs.wait("call 1: conversation closed")
	if strings.Contains(logs.String(), secretWords) {
		t.Fatalf("call log leaked transcript words:\n%s", logs.String())
	}
}

// An upstream drop mid-reply sends clear, closes the open reply{end}, then sends link{reconnecting},
// and logs the partial reply's audio as cut off.
func TestReplyFrameClosedOnUpstreamDrop(t *testing.T) {
	hub := agentHub(t)
	chunk := pcmChunkB64(12288)
	sess := 0
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] != "session.start" {
			return
		}
		sess++
		u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		if sess == 1 {
			u.send(c, replyLifecycleEvent("r1", "response.created"))
			u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
			_ = c.Close(websocket.StatusAbnormalClosure, "drop")
		}
	})
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", map[string]any{"voice": "peach_v2"}))
	a.until(ofType("ready"))

	var seq []string
	for {
		var f map[string]any
		select {
		case f = <-a.frames:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for link:reconnecting, got %v", seq)
		}
		seq = append(seq, fmt.Sprintf("%s:%v", f["type"], f["state"]))
		if f["type"] == "link" && f["state"] == linkReconnecting {
			break
		}
	}
	want := []string{"reply:start", "clear:<nil>", "reply:end", "link:reconnecting"}
	if !slices.Equal(seq, want) {
		t.Fatalf("drop sequence = %v, want %v", seq, want)
	}
	logs.wait(`call 1: upstream audio (voice "peach_v2", 16000 Hz): 384ms in`, "1 chunk of 384ms", ", cut off")
}

// Putting a call on hold mid-reply closes the open reply{end} before link{paused} and logs the audio as cut off.
func TestReplyFrameClosedOnHold(t *testing.T) {
	hub := agentHub(t)
	chunk := pcmChunkB64(12288)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] != "session.start" {
			return
		}
		u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		u.send(c, replyLifecycleEvent("r1", "response.created"))
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
	})
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", map[string]any{"voice": "peach_v2"}))
	a.until(ofType("ready"))
	if f := a.until(ofType("reply")); f["state"] != "start" {
		t.Fatalf("expected reply start, got %v", f)
	}
	select {
	case <-a.audio:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for audio")
	}
	a.send(map[string]any{"type": "hold", "on": true})

	var seq []string
	for {
		var f map[string]any
		select {
		case f = <-a.frames:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for link:paused, got %v", seq)
		}
		seq = append(seq, fmt.Sprintf("%s:%v", f["type"], f["state"]))
		if f["type"] == "link" && f["state"] == linkPaused {
			break
		}
	}
	want := []string{"reply:end", "link:paused"}
	if !slices.Equal(seq, want) {
		t.Fatalf("hold sequence = %v, want %v", seq, want)
	}
	logs.wait(`call 1: upstream audio (voice "peach_v2", 16000 Hz): 384ms in`, "1 chunk of 384ms", ", cut off")
}

// Protocol 1 calls never receive reply frames (preserving backward compatibility), while still logging upstream
// audio pacing on the hub. Dictation sessions also never emit reply frames.
func TestReplyFrameNotSentOnProtocol1OrDictation(t *testing.T) {
	chunk := pcmChunkB64(12288)

	t.Run("protocol 1", func(t *testing.T) {
		hub := newFakeHub(t)
		up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
			if f["type"] != "session.start" {
				return
			}
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
			u.send(c, replyLifecycleEvent("r1", "response.created"))
			u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
			u.send(c, map[string]any{"type": "session.output_transcript.delta", "delta": "hi"})
			u.send(c, replyLifecycleEvent("r1", "response.completed"))
		})
		pl := testPlugin(t, liveProvider(up), hub.server(t))
		logs := captureLog(t, pl)
		a := dialApp(t, pl, map[string]any{"type": "start", "voice": "peach_v2"})
		a.until(ofType("ready"))
		a.until(finalLine("assistant", "hi"))
		a.protocolOne()
		logs.wait(`call 1: upstream audio (voice "peach_v2", 16000 Hz): 384ms in`, "1 chunk of 384ms", ", over")
	})

	t.Run("dictation", func(t *testing.T) {
		hub := newFakeHub(t)
		up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
			if f["type"] != "session.start" {
				return
			}
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
			u.send(c, replyLifecycleEvent("r1", "response.created"))
			u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
			u.send(c, replyLifecycleEvent("r1", "response.completed"))
			u.send(c, map[string]any{"type": "session.input_transcript.delta", "delta": "spoken"})
		})
		pl := testPlugin(t, liveProvider(up), hub.server(t))
		a := dialApp(t, pl, map[string]any{"type": "start", "protocol": 2, "mode": modeDictation})
		a.until(ofType("ready"))
		a.until(func(f map[string]any) bool { return f["type"] == "transcript" && f["text"] == "spoken" })
		a.mu.Lock()
		sawReply := a.types["reply"]
		a.mu.Unlock()
		if sawReply {
			t.Fatal("dictation emitted a reply frame")
		}
	})
}

// If an upstream sends two response.created events without an intervening completion, the extension closes the first
// reply before opening the second so start/end frames remain strictly paired.
func TestReplyFrameOpenTwiceClosesPrevious(t *testing.T) {
	hub := agentHub(t)
	chunk := pcmChunkB64(12288)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] != "session.start" {
			return
		}
		u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		u.send(c, replyLifecycleEvent("r1", "response.created"))
		u.send(c, map[string]any{"type": "session.output_audio.delta", "delta": chunk})
		u.send(c, replyLifecycleEvent("r2", "response.created"))
		u.send(c, replyLifecycleEvent("r2", "response.completed"))
	})
	pl := testPlugin(t, liveProvider(up), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))

	var seq []string
	for len(seq) < 4 {
		var f map[string]any
		select {
		case f = <-a.frames:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for paired reply frames, got %v", seq)
		}
		seq = append(seq, fmt.Sprintf("%s:%v", f["type"], f["state"]))
	}
	want := []string{"reply:start", "reply:end", "reply:start", "reply:end"}
	if !slices.Equal(seq, want) {
		t.Fatalf("sequence = %v, want %v", seq, want)
	}
	logs.wait(`call 1: upstream audio (voice "default", 16000 Hz): 384ms in`, "1 chunk of 384ms", ", over")
}
