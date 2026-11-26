package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func trainingReportFixture(t *testing.T) (*Service, *fakeHub, *db.DB, Task, WorkoutReport) {
	t.Helper()
	needsHealthModule(t)
	d := strengthReportTestDB(t)
	s, f, _ := healthExecutionService(t)
	s.setStore(func() Store { return d })
	setSettings(t, s, func(v *Settings) { v.Timezone = "Asia/Tokyo" })
	if err := d.PutSetting(t.Context(), "health.items", map[string]any{"items": []string{"strength"}, "revision": 1}); err != nil {
		t.Fatal(err)
	}
	w := sampleWorkout()
	w.ID = strengthReportTestID("training-report-" + randomHex(6))
	w.Activity = assistantwire.WorkoutActivityStrength
	// The report arrives after local midnight, but belongs to the workout's start day.
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 15, 0, 0, 0, time.UTC)
	if midnight.After(now) {
		midnight = midnight.Add(-24 * time.Hour)
	}
	w.Start, w.End = midnight.Add(-30*time.Minute), midnight.Add(40*time.Minute)
	w.EnabledAt = w.Start.Add(-time.Hour)
	w.Duration = w.End.Sub(w.Start).Seconds()
	if err := s.acceptWorkout(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	task, ok := taskByID(s, "workout-"+w.ID)
	if !ok {
		t.Fatal("report task missing")
	}
	return s, f, d, task, w
}

func putTrainingReportChat(t *testing.T, s *Service, f *fakeHub, day, sid string) {
	t.Helper()
	f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: sid, Space: SpaceHealth, Assistant: true, Cwd: "/home/me/" + s.healthRel(), Launcher: "claude", Agent: "claude"})
	f.known[sid] = true
	s.keepTrainingChat(t.Context(), day, SessionRef{MachineID: "server", SessionID: sid}, true)
}

func TestStrengthReportContinuesTrainingChat(t *testing.T) {
	s, f, _, task, w := trainingReportFixture(t)
	day := w.Start.In(s.location(t.Context())).Format("2006-01-02")
	putTrainingReportChat(t, s, f, day, "training-source")
	before, _ := taskByID(s, task.ID)
	_, err := s.runTask(task, time.Now(), time.Now(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	p := f.lastSend(t)
	if p["session_id"] != "training-source" || p["resume_only"] != true || p["cwd"] != "/home/me/"+s.healthRel() {
		t.Fatalf("report target: %v", p)
	}
	runs := waitRuns(t, s, 1)
	if runs[0].Conversation != "source" {
		t.Fatalf("report run: %+v", runs[0])
	}
	after, _ := taskByID(s, task.ID)
	if before.SessionID != after.SessionID || before.Prompt != after.Prompt {
		t.Fatal("transient selection changed stored task")
	}
	s.fleet.mu.Lock()
	marked := !s.fleet.reportSent["training-source"].IsZero()
	s.fleet.mu.Unlock()
	if !marked {
		t.Fatal("report completion push not marked")
	}
}

func TestStrengthReportStartsTrainingChat(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-pointer", true: "source-refused"}[gone], func(t *testing.T) {
			s, f, d, task, w := trainingReportFixture(t)
			day := w.Start.In(s.location(t.Context())).Format("2006-01-02")
			if gone {
				putTrainingReportChat(t, s, f, day, "gone-training")
				f.known["gone-training"] = false
			}
			sess, err := s.runTask(task, time.Now(), time.Now(), true, false)
			if err != nil {
				t.Fatal(err)
			}
			var kept healthTrainingChat
			ok, err := d.Setting(t.Context(), trainingChatKey, &kept)
			if err != nil || !ok || kept.Day != day || kept.SessionID != sess.SessionID || kept.SessionID == "gone-training" || kept.MachineID != "server" {
				t.Fatalf("new training pointer: %+v %v", kept, err)
			}
			if p := f.lastSend(t); p["session_id"] != nil || p["cwd"] != "/home/me/"+s.healthRel() {
				t.Fatalf("new report chat: %v", p)
			}
			runs := waitRuns(t, s, 1)
			if runs[0].Conversation != "new" || gone && runs[0].ConversationReason != "source_unavailable" {
				t.Fatalf("report fallback: %+v", runs[0])
			}
		})
	}
}

func TestStrengthReportReplacesDeletedTrainingChat(t *testing.T) {
	s, f, d, task, workout := trainingReportFixture(t)
	day := workout.Start.In(s.location(t.Context())).Format("2006-01-02")
	putTrainingReportChat(t, s, f, day, "deleted-training-report")
	if w := serve(s, "DELETE", "/api/v1/assistant/conversations/deleted-training-report", `{"machine_id":"server"}`); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	sess, err := s.runTask(task, time.Now(), time.Now(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	var kept healthTrainingChat
	if ok, err := d.Setting(t.Context(), trainingChatKey, &kept); err != nil || !ok || kept.SessionID != sess.SessionID || kept.SessionID == "deleted-training-report" || kept.Day != day {
		t.Fatalf("deleted report chat was not replaced: %+v %v", kept, err)
	}
	if p := f.lastSend(t); p["session_id"] != nil {
		t.Fatalf("report continued deleted chat: %v", p)
	}
}

func TestNonStrengthReportUnchanged(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "strength-without-module"}[absent], func(t *testing.T) {
			s, f, _ := healthExecutionService(t)
			if absent {
				s.healthMod = nil
			}
			w := sampleWorkout()
			if absent {
				w.Activity = assistantwire.WorkoutActivityStrength
			}
			if err := s.acceptWorkout(context.Background(), w); err != nil {
				t.Fatal(err)
			}
			task, ok := taskByID(s, "workout-"+w.ID)
			if !ok {
				t.Fatal("task missing")
			}
			day := w.Start.In(s.location(t.Context())).Format("2006-01-02")
			putTrainingReportChat(t, s, f, day, "unrelated-training")
			var kept healthTrainingChat
			if _, err := s.store().Setting(t.Context(), trainingChatKey, &kept); err != nil {
				t.Fatal(err)
			}
			got, selectedDay := s.workoutPromptNow(t.Context(), task)
			if got.Prompt != task.Prompt || selectedDay != "" {
				t.Fatal("ordinary stored prompt changed")
			}
			if _, err := s.runTask(task, time.Now(), time.Now(), true, false); err != nil {
				t.Fatal(err)
			}
			if p := f.lastSend(t); p["session_id"] != nil {
				t.Fatalf("non-strength used training pointer: %v", p)
			}
			var after healthTrainingChat
			if _, err := s.store().Setting(t.Context(), trainingChatKey, &after); err != nil || after != kept {
				t.Fatalf("ordinary report changed pointer: %+v %v", after, err)
			}
		})
	}
}
