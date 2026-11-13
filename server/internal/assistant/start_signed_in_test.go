package assistant

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The conversations the hub starts by itself (the background supervisor, a scheduled task's, conversation_new's) have
// nobody in front of them to sign in: they start in the profile the assistant's conversations had, as a meeting's do,
// and on a node too old to be told a profile as before (#6015).
func TestHubStartedConversationsStartSignedIn(t *testing.T) {
	for _, outdated := range []bool{false, true} {
		f := newFakeHub().withMachine()
		f.settings = &Settings{MachineID: "server"}
		f.refuseConfigDir = outdated
		s := newTestService(t, f)
		if _, err := s.startConversation(context.Background(), startParams{Text: "hello", Origin: OriginSpawned}); err != nil {
			t.Fatalf("outdated=%v: %v", outdated, err)
		}
		if len(f.sends) != 1 {
			t.Fatalf("outdated=%v: %d starts", outdated, len(f.sends))
		}
		got, set := f.sends[0]["config_dir"]
		if !outdated && got != meetingProfile || outdated && set {
			t.Fatalf("outdated=%v: config_dir = %v (set=%v)", outdated, got, set)
		}
	}
}

// A background supervisor started before this manager whose last turn ended "Not logged in" was made in a private
// profile with no login and cannot move: it is gone, and its replacement starts signed in. One this manager started is
// not replaced for it: it already is in the signed-in profile, and a new one would only be signed out too.
func TestBackgroundSupervisorSignedOutIsReplaced(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	signedOut := proto.Session{SessionID: "bg1", MachineID: "server", Assistant: true, Status: proto.StatusDone,
		TurnErrorCode: proto.TurnErrorNotLoggedIn}
	main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	f.setSessions("server", main, signedOut)

	// Started by this manager (after it began, past the grace): there, though signed out.
	s.born = time.Now().Add(-time.Hour)
	mine := time.Now().Add(-30 * time.Minute).UTC()
	setSettings(t, s, func(v *Settings) {
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = "bg1", "server", &mine
	})
	if !s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("a background supervisor this manager started counted as gone")
	}
	// Answering again: there.
	before := time.Now().Add(-2 * time.Hour).UTC()
	setSettings(t, s, func(v *Settings) { v.BackgroundSupervisorAt = &before })
	f.setSessions("server", main, proto.Session{SessionID: "bg1", MachineID: "server", Assistant: true, Status: proto.StatusDone})
	if !s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("a signed-in background supervisor counted as gone")
	}

	// Started before this manager and signed out: gone, and replaced by one that starts signed in.
	f.setSessions("server", main, signedOut)
	if s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("a signed-out background supervisor from before this manager is still there")
	}
	id, gone := s.ensureBackground(ctx)
	if id == "" || id == "bg1" || gone != "bg1" {
		t.Fatalf("ensureBackground = %q, gone %q", id, gone)
	}
	starts := conversationStarts(f)
	if len(starts) != 1 || starts[0]["config_dir"] != meetingProfile {
		t.Fatalf("the replacement started as %v", starts)
	}
	if got := s.settings(ctx).BackgroundSupervisor; got != id {
		t.Fatalf("settings keep %q, want %q", got, id)
	}
}

// How the node lists a conversation whose last turn ended "Not logged in": Claude Code's own reply as the turn's error,
// with no code (session.get answers the node's session as it is; the lists work the code out of the words).
func signedOutSession(id string) proto.Session {
	return proto.Session{SessionID: id, MachineID: "server", Assistant: true, Status: proto.StatusDone,
		TurnError: "Not logged in · Please run /login", TurnErrorKind: proto.TurnErrorUser}
}

// The same conversation with no process, as the node lists it from its transcript (a task's conversation between runs,
// any conversation after the node restarted): that reply is only its last words, with no turn error (scanOffline).
func offlineSignedOutSession(id string) proto.Session {
	return proto.Session{SessionID: id, MachineID: "server", Assistant: true, Status: proto.StatusOffline,
		LastText: "Not logged in · Please run /login"}
}

// A background supervisor from before this manager that the node lists signed out, in Claude Code's words and with no
// code, is gone as one with the code is, also when it has no process and those words are only its last (#6186).
func TestBackgroundSupervisorSignedOutInItsWordsIsReplaced(t *testing.T) {
	for _, listed := range []proto.Session{signedOutSession("bg1"), offlineSignedOutSession("bg1")} {
		s, f, _ := newBackgroundService(t)
		ctx := context.Background()
		f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}, listed)
		s.born = time.Now().Add(-time.Hour)
		before := time.Now().Add(-2 * time.Hour).UTC()
		setSettings(t, s, func(v *Settings) {
			v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = "bg1", "server", &before
		})
		if s.backgroundThere(ctx, s.settings(ctx), "bg1") {
			t.Fatalf("a background supervisor the node lists signed out (%s) is still there", listed.Status)
		}
	}
}

// A scheduled task whose own conversation was made in a profile with no login (every conversation the hub started was,
// until #6015) had every later run end "Not logged in" there: a conversation cannot move to another profile. A run into
// one whose last turn ended so asks for the profile the hub's conversations start in; the node refuses it when it is
// in another one, and the run starts a new conversation, signed in, that the task keeps. One already in that profile is
// resumed, and so is one on a node too old to be told a profile; Codex's (no profiles) and one whose last turn went well
// are resumed as before (#6186).
func TestATaskConversationSignedOutElsewhereIsReplaced(t *testing.T) {
	setup := func(t *testing.T) (*Service, *fakeHub, proto.Session, *[]map[string]any) {
		t.Helper()
		f := newFakeHub().withMachine()
		s := newStorelessService(t, f)
		setTasks(s, Task{ID: "p", Title: "巡检", Prompt: "巡检", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true})
		first, err := runNow(t, s, "p")
		if err != nil {
			t.Fatal(err)
		}
		asked := &[]map[string]any{} // every assistant.send from here on, refused or not (written under f.mu)
		f.mu.Lock()
		f.onAssistantSend = func(_ *fakeHub, p map[string]any) { *asked = append(*asked, p) }
		f.mu.Unlock()
		return s, f, first, asked
	}
	sendsOf := func(f *fakeHub, asked *[]map[string]any) []map[string]any {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Clone(*asked)
	}

	// Between runs the conversation usually has no process: the node lists it from its transcript, with the reply only as
	// its last words.
	for _, listed := range []struct {
		name    string
		session func(id string) proto.Session
	}{{"in another profile", signedOutSession}, {"in another profile with no process", offlineSignedOutSession}} {
		t.Run(listed.name, func(t *testing.T) {
			s, f, first, asked := setup(t)
			f.setSessions("server", listed.session(first.SessionID))
			f.mu.Lock()
			f.elsewhere = map[string]bool{first.SessionID: true}
			f.mu.Unlock()
			next, err := runNow(t, s, "p")
			if err != nil {
				t.Fatal(err)
			}
			sends := sendsOf(f, asked)
			if len(sends) != 2 || sends[0]["session_id"] != first.SessionID || sends[0]["config_dir"] != meetingProfile {
				t.Fatalf("the run asks to resume in %q first: %v", meetingProfile, sends)
			}
			if id, _ := sends[1]["session_id"].(string); id != "" || sends[1]["config_dir"] != meetingProfile {
				t.Fatalf("refused, it starts a new conversation signed in: %v", sends[1])
			}
			if next.SessionID == "" || next.SessionID == first.SessionID {
				t.Fatalf("the run went to %q", next.SessionID)
			}
			if task, _ := taskByID(s, "p"); task.RunSessionID != next.SessionID {
				t.Fatalf("the task keeps the new conversation: %+v", task)
			}
		})
	}

	t.Run("in that profile", func(t *testing.T) {
		s, f, first, asked := setup(t)
		f.setSessions("server", signedOutSession(first.SessionID))
		next, err := runNow(t, s, "p")
		if err != nil {
			t.Fatal(err)
		}
		sends := sendsOf(f, asked)
		if len(sends) != 1 || sends[0]["session_id"] != first.SessionID || sends[0]["config_dir"] != meetingProfile || next.SessionID != first.SessionID {
			t.Fatalf("a conversation already in %q is resumed there: %q %v", meetingProfile, next.SessionID, sends)
		}
	})

	t.Run("on a node too old for a profile", func(t *testing.T) {
		s, f, first, asked := setup(t)
		f.setSessions("server", signedOutSession(first.SessionID))
		f.mu.Lock()
		f.refuseConfigDir = true
		f.mu.Unlock()
		next, err := runNow(t, s, "p")
		if err != nil {
			t.Fatal(err)
		}
		sends := sendsOf(f, asked)
		if len(sends) != 2 || sends[1]["session_id"] != first.SessionID || sends[1]["config_dir"] != nil || next.SessionID != first.SessionID {
			t.Fatalf("an old node's conversation is resumed as before: %q %v", next.SessionID, sends)
		}
	})

	t.Run("not Claude Code", func(t *testing.T) {
		s, f, first, asked := setup(t)
		codex := signedOutSession(first.SessionID)
		codex.Agent = "codex" // no profiles: the node would refuse a config_dir for it at every run
		f.setSessions("server", codex)
		next, err := runNow(t, s, "p")
		if err != nil {
			t.Fatal(err)
		}
		sends := sendsOf(f, asked)
		if len(sends) != 1 || sends[0]["session_id"] != first.SessionID || sends[0]["config_dir"] != nil || next.SessionID != first.SessionID {
			t.Fatalf("a Codex conversation is resumed as before: %q %v", next.SessionID, sends)
		}
	})

	t.Run("signed in", func(t *testing.T) {
		s, f, first, asked := setup(t)
		f.setSessions("server", proto.Session{SessionID: first.SessionID, MachineID: "server", Assistant: true, Status: proto.StatusDone})
		f.mu.Lock()
		f.elsewhere = map[string]bool{first.SessionID: true} // a launcher's own login there: nothing to change
		f.mu.Unlock()
		next, err := runNow(t, s, "p")
		if err != nil {
			t.Fatal(err)
		}
		sends := sendsOf(f, asked)
		if len(sends) != 1 || sends[0]["session_id"] != first.SessionID || sends[0]["config_dir"] != nil || next.SessionID != first.SessionID {
			t.Fatalf("a conversation whose last turn went well is resumed as before: %q %v", next.SessionID, sends)
		}
	})
}

// The implicit signed-in profile is Claude Code's: a hub-started conversation whose launcher ends up Codex (asked for,
// or fallen back to) must not carry it, or the node refuses the start before creating anything (#6467).
func TestHubStartedCodexConversationOmitsConfigDir(t *testing.T) {
	for _, tc := range []struct {
		launcher string
		profile  bool
	}{{"codex", false}, {"", true}, {"claude", true}} {
		f := newFakeHub().withMachine()
		f.settings = &Settings{MachineID: "server"}
		s := newTestService(t, f)
		if _, err := s.startSignedIn(context.Background(), StartParams{Text: "hello", Origin: OriginSpawned, Launcher: tc.launcher}); err != nil {
			t.Fatalf("launcher %q: %v", tc.launcher, err)
		}
		if len(f.sends) != 1 {
			t.Fatalf("launcher %q: %d starts", tc.launcher, len(f.sends))
		}
		got, set := f.sends[0]["config_dir"]
		if tc.profile && got != meetingProfile || !tc.profile && set {
			t.Fatalf("launcher %q: config_dir = %v (set=%v)", tc.launcher, got, set)
		}
	}
}
