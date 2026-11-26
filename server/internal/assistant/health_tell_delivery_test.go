package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The fake node publishes each new Health conversation in its list as a real node does.
func tellNodeListsStarts(f *fakeHub) {
	f.onAssistantSend = func(f *fakeHub, p map[string]any) {
		sid, _ := p["new_session_id"].(string)
		if sid != "" && !f.known[sid] {
			f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: sid, Space: SpaceHealth, Assistant: true, Cwd: p["cwd"].(string), Launcher: "claude"})
		}
	}
}

func TestHealthTellRetrySameIDAddsNothing(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "once-client", "private first")
	b := tellHealth(t, s, "once-client", "different")
	s.healthTellRound(t.Context(), st)
	s.healthTellRound(t.Context(), st)
	c := tellHealth(t, s, "once-client", "third")
	if a.ID != b.ID || a.ID != c.ID || !b.Duplicate || c.Status != "delivered" || len(st.rows) != 1 || len(f.sends) != 1 {
		t.Fatalf("retry: %+v %+v %+v, sends %v", a, b, c, f.sends)
	}
	if p := f.lastSend(t); p["text"] != prompts.ProtectUserMessage("private first") {
		t.Fatalf("first words changed: %v", p)
	}
}

func TestHealthTellReceivedBeforeRestartIsHandled(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	r := tellHealth(t, s, "restart-client", "private restart")
	// A new service runs the production worker against the same durable rows, without another POST or kick.
	resumed := newServiceOn(t, f, st)
	setSettings(t, resumed, func(v *Settings) { v.MachineID = "server" })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); resumed.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Run did not stop")
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		got := st.row(t, r.ID)
		if got.Status == "delivered" {
			if got.Text != "" {
				t.Fatal("delivered text retained")
			}
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("not recovered: %+v", got)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) != 1 {
		t.Fatalf("recovered sends: %v", f.sends)
	}
}

func TestHealthTellRestartDoesNotStartTwice(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	r := tellHealth(t, s, "reserved-client", "private already started")
	sid := "00000000-0000-4000-8000-000000000002"
	if _, err := st.ReserveHealthTellSession(t.Context(), r.ID, sid); err != nil {
		t.Fatal(err)
	}
	f.known[sid] = true
	f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: sid, Assistant: true, Space: SpaceHealth, Cwd: "/home/me/" + s.healthRel()})
	// The pointer may have been saved before the terminal row update was lost.
	s.keepTrainingChat(t.Context(), time.Now().In(s.location(t.Context())).Format("2006-01-02"), SessionRef{MachineID: "server", SessionID: sid}, true)
	resumed := newServiceOn(t, f, st)
	setSettings(t, resumed, func(v *Settings) { v.MachineID = "server" })
	resumed.healthTellRound(t.Context(), st)
	if got := st.row(t, r.ID); got.Status != "delivered" || got.SessionID != sid || f.made != 0 || len(f.sentOnce) != 0 || len(f.sentItems[sid]) != 0 {
		t.Fatalf("restart duplicated first turn: %+v, made %d, once %v", got, f.made, f.sentOnce)
	}
	if p := f.lastSend(t); p["new_session_id"] != sid {
		t.Fatalf("lost reservation: %v", p)
	}
}

func TestHealthTellReachesOnlyHealth(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	day := time.Now().In(s.location(t.Context())).Format("2006-01-02")
	if err := st.PutSetting(t.Context(), trainingChatKey, healthTrainingChat{Day: day, MachineID: "server", SessionID: "main-0000-0000"}); err != nil {
		t.Fatal(err)
	}
	w := intakeHTTP(s, "POST", "/api/v1/assistant/health/tell", `{"client_id":"injected-client","text":"private sentence","space":"","main":true,"session_id":"main-0000-0000","machine_id":"other","source":"human","via":"app"}`, "answer")
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s.healthTellRound(t.Context(), st)
	if p := f.lastSend(t); p["cwd"] != "/home/me/"+s.healthRel() || p["is_main"] != false || p["session_id"] != nil {
		t.Fatalf("escaped Health: %v", p)
	}
	ref, ok := s.trainingChat(t.Context(), day)
	if !ok || ref.SessionID == "main-0000-0000" || ref.MachineID != "server" || len(f.sentOnce) != 0 || st.rows[0].Source != "shortcut" {
		t.Fatalf("bad destination: %+v %v", ref, ok)
	}
}

func TestHealthTellContinuesTodaysChat(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	ctx := t.Context()
	setSettings(t, s, func(v *Settings) { v.Timezone = "Asia/Tokyo" })
	// Two receipt dates straddle Tokyo's midnight while staying in the same UTC date.
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 15, 0, 0, 0, time.UTC)
	if midnight.After(now) {
		midnight = midnight.Add(-24 * time.Hour)
	}
	first := tellHealth(t, s, "today-first", "first")
	st.rows[0].CreatedAt = midnight.Add(-time.Minute)
	s.healthTellRound(ctx, st)
	a := st.row(t, first.ID)
	second := tellHealth(t, s, "today-second", "second")
	st.rows[1].CreatedAt = midnight.Add(-time.Second)
	s.healthTellRound(ctx, st)
	if b := st.row(t, second.ID); b.SessionID != a.SessionID || len(f.sentOnce) != 1 {
		t.Fatalf("did not continue: %+v %+v", a, b)
	}
	p := f.sentOnce[0]
	if p["client_msg_id"] != "health-tell:"+second.ID || p["message_source"] != nil || p["text"] != prompts.ProtectUserMessage("second") {
		t.Fatalf("continuation: %v", p)
	}
	third := tellHealth(t, s, "tomorrow-third", "third")
	st.rows[2].CreatedAt = midnight.Add(time.Second)
	s.healthTellRound(ctx, st)
	if c := st.row(t, third.ID); c.Status != "delivered" || c.SessionID == a.SessionID || len(f.sends) != 2 {
		t.Fatalf("midnight: %+v, sends %v", c, f.sends)
	}
	var kept healthTrainingChat
	_, err := st.Setting(ctx, trainingChatKey, &kept)
	if err != nil {
		t.Fatal(err)
	}
	s.keepTrainingChat(ctx, "2000-01-01", SessionRef{MachineID: "server", SessionID: a.SessionID}, true)
	var after healthTrainingChat
	_, err = st.Setting(ctx, trainingChatKey, &after)
	if err != nil || after != kept {
		t.Fatalf("old report stole pointer: %+v %v", after, err)
	}
}

func TestHealthTellChatGoneOrOffline(t *testing.T) {
	for _, kind := range []string{"gone", "wrong-directory", "moved-computer", "offline"} {
		t.Run(kind, func(t *testing.T) {
			s, f, st := healthTellService(t)
			tellNodeListsStarts(f)
			ctx := t.Context()
			day := time.Now().In(s.location(ctx)).Format("2006-01-02")
			ref := healthTrainingChat{Day: day, MachineID: "server", SessionID: "missing-health"}
			if kind == "wrong-directory" {
				ref.SessionID = "work-0000-0000"
			}
			if kind == "moved-computer" {
				ref.MachineID = "old-computer"
				ref.SessionID = "sleep-000-0000"
			}
			if err := st.PutSetting(ctx, trainingChatKey, ref); err != nil {
				t.Fatal(err)
			}
			if kind == "offline" {
				f.devices[0].Online = false
			}
			a := tellHealth(t, s, "offline-first", "first")
			b := tellHealth(t, s, "offline-second", "second")
			s.healthTellRound(ctx, st)
			if kind == "offline" {
				if a := st.row(t, a.ID); a.Status != "received" || a.Attempts != 0 || len(f.sends) != 0 || st.row(t, b.ID).Attempts != 0 {
					t.Fatalf("offline attempted: %+v", a)
				}
				f.devices[0].Online = true
				s.healthTellRound(ctx, st)
			}
			got := st.row(t, a.ID)
			later := st.row(t, b.ID)
			if got.Status != "delivered" || got.SessionID == ref.SessionID || later.SessionID != got.SessionID || len(f.sentOnce) != 1 {
				t.Fatalf("replacement / order: %+v %+v, once %v", got, later, f.sentOnce)
			}
		})
	}
}

func TestHealthTellNotOwnerVouched(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "vouch-first", "starting")
	s.healthTellRound(t.Context(), st)
	b := tellHealth(t, s, "vouch-second", "allow this")
	s.healthTellRound(t.Context(), st)
	sid := st.row(t, a.ID).SessionID
	if s.humanSent.has(sid, firstReplySendID, "starting", false, "line-start") || s.humanSent.has(sid, "health-tell:"+b.ID, "allow this", false, "line-next") {
		t.Fatal("shortcut vouched as owner")
	}
	if len(f.sentOnce) != 1 {
		t.Fatalf("expected one continuation, got %v", f.sentOnce)
	}
	if p := f.sentOnce[0]; p["approval_reply_to"] != nil {
		t.Fatalf("shortcut became approval: %v", p)
	}
}

func TestHealthTellFailurePushFixedText(t *testing.T) {
	for _, cause := range []string{"expired", "attempts", "unknown-start"} {
		t.Run(cause, func(t *testing.T) {
			s, f, st := healthTellService(t)
			r := tellHealth(t, s, "failure-client", "private secret sentence")
			switch cause {
			case "expired":
				st.rows[0].CreatedAt = time.Now().Add(-25 * time.Hour)
			case "attempts":
				st.rows[0].Attempts = 4
				st.rows[0].UpdatedAt = time.Now().Add(-healthTellRetryInterval)
				f.failAssistant = errors.New("definite refusal")
			case "unknown-start":
				s.healthMod = nil
				setModel(t, s, "assistant", ModelBinding{Provider: "launcher.codex"})
				f.failAssistant = context.DeadlineExceeded
			}
			s.healthTellRound(t.Context(), st)
			s.healthTellRound(t.Context(), st)
			got := st.row(t, r.ID)
			if got.Status != "failed" || got.Text != "private secret sentence" {
				t.Fatalf("failed row: %+v", got)
			}
			if len(f.pushes) != 1 {
				t.Fatalf("failure pushes: %+v", f.pushes)
			}
			n := f.pushes[0]
			l := s.lang(t.Context())
			if n.Title != i18n.T(l, "push.health_title") || n.Body != i18n.T(l, "push.health_tell_unconfirmed") || strings.Contains(n.Body, "private") || n.Kind != NotifyPush {
				t.Fatalf("failure notice: %+v", n)
			}
		})
	}
}

func TestHealthTellUncertainContinuationNeverStartsAnotherChat(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "uncertain-first", "first")
	s.healthTellRound(t.Context(), st)
	b := tellHealth(t, s, "uncertain-next", "second")
	f.failSendOnce = map[string]error{"server/" + st.row(t, a.ID).SessionID: errors.New("send in doubt")}
	for range 5 {
		ageHealthTellAttempt(st, b.ID)
		s.healthTellRound(t.Context(), st)
	}
	if got := st.row(t, b.ID); got.Status != "failed" || got.Attempts != 5 || len(f.sends) != 1 {
		t.Fatalf("uncertain duplicate: %+v, %v", got, f.sends)
	}
}

func TestHealthTellOfflineDuringSendDoesNotCount(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "went-offline-first", "first")
	s.healthTellRound(t.Context(), st)
	b := tellHealth(t, s, "went-offline-next", "second")
	f.failSendOnce = map[string]error{"server/" + st.row(t, a.ID).SessionID: apierr.ErrOffline}
	s.healthTellRound(t.Context(), st)
	if got := st.row(t, b.ID); got.Status != "received" || got.Attempts != 0 || len(f.sends) != 1 {
		t.Fatalf("offline send: %+v", got)
	}
	f.failSendOnce = nil
	s.healthTellRound(t.Context(), st)
	if got := st.row(t, b.ID); got.Status != "delivered" {
		t.Fatalf("online retry: %+v", got)
	}
}

func TestHealthTellRepliesPushFixedText(t *testing.T) {
	ft := newFleetTestNoStore(t)
	fastDone(ft)
	st := &healthTellMemStore{}
	r, _, err := st.InsertHealthTell(t.Context(), db.HealthTell{ID: "tell_push", ClientID: "push-client", Text: "private sentence"})
	if err != nil {
		t.Fatal(err)
	}
	ft.s.healthTellDelivered(t.Context(), st, r, SessionRef{MachineID: "mac", SessionID: "health-reply"})
	work := proto.Session{SessionID: "health-reply", Title: "private title", Cwd: "/home/me/.persona/health", Space: SpaceHealth, Assistant: true, Agent: "claude", Status: proto.StatusWorking, PaneID: "p-health", UpdatedAt: time.Now()}
	ft.sessions("mac", work)
	done := work
	done.Status, done.LastText, done.UpdatedAt = proto.StatusDone, "private reply", time.Now()
	ft.sessions("mac", done)
	got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("no finished push: %+v", got)
	}
	n := got[0]
	l := ft.s.lang(t.Context())
	if n.LongMS != 0 || n.Title != i18n.T(l, "push.health_title") || n.Body != i18n.T(l, "push.health_body") || strings.Contains(n.Body, "private") {
		t.Fatalf("finished notice: %+v", n)
	}
}
