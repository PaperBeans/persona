package assistant

import (
	"context"
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
