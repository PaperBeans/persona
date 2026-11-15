package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestTaskStateNoteEdit(t *testing.T) {
	now := time.Now().UTC()
	task := Task{Repeat: repeatDaily}
	if err := editTaskState(&task, Task{FreshEachRun: true}, map[string]json.RawMessage{
		"fresh_each_run": json.RawMessage("true"),
		"state_text":     json.RawMessage(`"last issue #12"`),
	}, now); err != nil {
		t.Fatal(err)
	}
	if !task.FreshEachRun || task.StateNote.Text != "last issue #12" || task.StateNote.Revision != 1 || !task.StateNote.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected task state: %+v", task.StateNote)
	}
	if err := editTaskState(&task, Task{}, nil, now); err != nil || !task.FreshEachRun || task.StateNote.Revision != 1 {
		t.Fatal("an older client cleared a continuity setting")
	}
	oversize, _ := json.Marshal(strings.Repeat("界", 1366))
	if err := editTaskState(&task, Task{}, map[string]json.RawMessage{"state_text": oversize}, now); err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("oversize note: %v", err)
	}
	if task.StateNote.Revision != 1 {
		t.Fatal("an oversize note changed the saved revision")
	}
}

func TestTaskStateDraftCommitChecksSessionAndRevision(t *testing.T) {
	ctx := context.Background()
	s := newStorelessService(t, newFakeHub())
	task := Task{ID: "test", Repeat: repeatInterval, LastSessionID: "scheduled"}
	setTasks(s, task)

	stage := func(machine, session, text string) {
		task.stateRunKey = s.openTaskStateDraft()
		s.sched.mu.Lock()
		s.sched.stateDrafts[task.stateRunKey] = taskStateDraft{machine: machine, session: session, text: text}
		s.sched.mu.Unlock()
	}
	run := ScheduleRun{MachineID: "machine", SessionID: "scheduled"}
	stage("machine", "another", "wrong session")
	s.finishTaskState(ctx, task, run)
	current, _ := taskByID(s, task.ID)
	if current.StateNote != nil {
		t.Fatal("another conversation changed the task state")
	}
	stage("machine", "scheduled", "last issue #12")
	s.finishTaskState(ctx, task, run)
	current, _ = taskByID(s, task.ID)
	if current.StateNote == nil || current.StateNote.Text != "last issue #12" || current.StateNote.Revision != 1 {
		t.Fatalf("missing state: %+v", current.StateNote)
	}
	stage("machine", "scheduled", "stale update")
	s.finishTaskState(ctx, task, run)
	current, _ = taskByID(s, task.ID)
	if current.StateNote.Text != "last issue #12" || current.StateNote.Revision != 1 {
		t.Fatalf("stale run overwrote state: %+v", current.StateNote)
	}
}

func TestTaskStateFreshRunStartsNewConversationWithSavedNote(t *testing.T) {
	node := newFakeHub().withMachine()
	s := New(Options{Hub: node, Logf: testLogf(t)})
	setTasks(s, Task{
		ID: "patrol", Title: "Patrol", Prompt: "Check for changes", Repeat: repeatInterval,
		EveryMinutes: 30, Enabled: true, FreshEachRun: true,
		StateNote: &TaskStateNote{Text: "last issue #12", Revision: 1},
	})
	first, err := runNow(t, s, "patrol")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runNow(t, s, "patrol")
	if err != nil {
		t.Fatal(err)
	}
	send := node.lastSend(t)
	if send["session_id"] != nil && send["session_id"] != "" || first.SessionID == second.SessionID {
		t.Fatalf("fresh run reused %q: %+v", first.SessionID, send)
	}
	if prompt := send["text"].(string); !strings.Contains(prompt, "last issue #12") || !strings.Contains(prompt, "schedule_state_set") {
		t.Fatalf("fresh run did not receive its note and write instructions: %q", prompt)
	}
}

func TestTaskStateSurvivesRestartAndSkippedRun(t *testing.T) {
	s, _, store := scheduleStorageFixture(t)
	body := `{"title":"Daily","prompt":"Example","repeat":"daily","time":"09:00","enabled":true,"fresh_each_run":true,"state_text":"last issue #12"}`
	if response := scheduleRequest(s, "PUT", "tasks/daily", body); response.Code != 200 {
		t.Fatalf("saving note: %d %s", response.Code, response.Body)
	}
	s.recordRun(ScheduleRun{RefID: "daily", Status: runSkipped})
	s.recordRun(ScheduleRun{RefID: "daily", Status: runFailed, SessionID: "failed"})
	assertScheduleStored(t, s, store)
	restarted := newServiceOn(t, newFakeHub(), store)
	task, ok := taskByID(restarted, "daily")
	if !ok || !task.FreshEachRun || task.StateNote == nil || task.StateNote.Text != "last issue #12" || task.StateNote.Revision != 1 {
		t.Fatalf("reloaded state: %+v", task)
	}
}

func TestTaskStateDiscardsDraftWhenRunCannotBeRegistered(t *testing.T) {
	ctx := t.Context()
	store := &flakyScheduleStore{data: map[string][]byte{}}
	task := Task{ID: "daily", Repeat: repeatDaily, StateNote: &TaskStateNote{Text: "previous cursor", Revision: 1}}
	if err := store.PutSetting(ctx, tasksKey, []Task{task}); err != nil {
		t.Fatal(err)
	}
	node := newFakeHub().withMachine()
	s := newServiceOn(t, node, store)
	store.failReads(true)
	task.stateRunKey = s.openTaskStateDraft()
	node.setItems(proto.Item{Role: "user", Kind: proto.KindText, Text: taskStatePrompt(task)})
	args, _ := json.Marshal(map[string]string{"run_key": task.stateRunKey, "text": "new cursor"})
	result, err := s.toolScheduleStateSet(ctx, "server", "scheduled", args)
	if err != nil || !strings.Contains(result, "not yet saved") {
		t.Fatalf("staging an already dispatched turn: %q, %v", result, err)
	}
	s.startScheduledRun(ScheduleRun{RefID: task.ID, MachineID: "server", SessionID: "scheduled"}, task, &Device{ID: "server"})
	if _, err := s.toolScheduleStateSet(ctx, "server", "scheduled", args); err == nil || !strings.Contains(err.Error(), "no longer accepting state") {
		t.Fatalf("the unregistered run still accepts state: %v", err)
	}
	store.failReads(false)
	tasks, runs := scheduleState(s)
	if len(runs) != 0 || len(tasks) != 1 || tasks[0].StateNote == nil ||
		tasks[0].StateNote.Text != "previous cursor" || tasks[0].StateNote.Revision != 1 {
		t.Fatalf("reloaded tasks and runs after storage recovered: %+v, %+v", tasks, runs)
	}
}

func TestTaskStateDiscardsDraftWhenPreviousRunIsBusy(t *testing.T) {
	node := readyHub()
	node.setSessions("server", proto.Session{SessionID: "busy", Status: proto.StatusWorking, State: proto.StateWorking})
	s := newStorelessService(t, node)
	task := Task{ID: "patrol", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, FreshEachRun: true,
		RunMachineID: "server", RunSessionID: "busy", RunLauncher: "claude"}
	sc := setTasks(s, task)
	now := time.Now()
	for _, manual := range []bool{false, true} {
		if _, err := s.runTask(task, now, now, manual, false); !errors.Is(err, errTaskBusy) {
			t.Fatalf("manual=%v: run did not skip the busy chat: %v", manual, err)
		}
		sc.mu.Lock()
		drafts := len(sc.stateDrafts)
		sc.mu.Unlock()
		if drafts != 0 {
			t.Fatalf("manual=%v: skipped run left %d state draft(s)", manual, drafts)
		}
	}
	_, runs := scheduleState(s)
	if len(runs) != 2 || runs[0].SessionID != "busy" || runs[1].SessionID != "busy" {
		t.Fatalf("skipped runs should still link to the busy chat: %+v", runs)
	}
}

func TestTaskStateFiveFreshRunsCarryOnlyNewFindings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		node := newFakeHub().withMachine()
		s := New(Options{Hub: node, Logf: testLogf(t)})
		s.turnPoll = time.Second
		s.runRetry = time.Minute
		setTasks(s, Task{
			ID: "patrol", Title: "Patrol", Prompt: "Report only new issues", Repeat: repeatInterval,
			EveryMinutes: 30, Enabled: true, FreshEachRun: true,
		})
		seenSessions := map[string]bool{}
		for issue := 101; issue <= 105; issue++ {
			session, err := runNow(t, s, "patrol")
			if err != nil {
				t.Fatal(err)
			}
			if seenSessions[session.SessionID] {
				t.Fatalf("reused session %s", session.SessionID)
			}
			seenSessions[session.SessionID] = true
			prompt := node.lastSend(t)["text"].(string)
			if issue > 101 && !strings.Contains(prompt, fmt.Sprintf("last reported #%d", issue-1)) {
				t.Fatalf("run %d did not receive the prior cursor: %s", issue, prompt)
			}
			_, suffix, ok := strings.Cut(prompt, "call schedule_state_set with run_key ")
			if !ok {
				t.Fatalf("run %d lacks a state key", issue)
			}
			key, _, _ := strings.Cut(suffix, " ")
			user := proto.Item{ID: "scheduled", Role: "user", Kind: proto.KindText, Text: prompt}
			node.setItems(user)
			node.setSessions("server", proto.Session{SessionID: session.SessionID, Status: proto.StatusWorking, State: proto.StateWorking})
			synctest.Wait()
			<-time.After(2 * s.turnPoll)
			synctest.Wait()
			note := fmt.Sprintf("last reported #%d", issue)
			args, _ := json.Marshal(map[string]string{"run_key": key, "text": note})
			if _, err := s.runTool(t.Context(), "server", session.SessionID, "schedule_state_set", args); err != nil {
				t.Fatal(err)
			}
			staged, _ := taskByID(s, "patrol")
			if staged.StateNote.revision() != uint64(issue-101) {
				t.Fatal("a working turn committed its draft")
			}
			node.setItems(user, proto.Item{ID: "report", Role: "assistant", Kind: proto.KindText, Text: fmt.Sprintf("New issue #%d", issue)})
			node.setSessions("server", proto.Session{SessionID: session.SessionID, Status: proto.StatusDone, State: proto.StateIdle})
			<-time.After(3 * s.turnPoll)
			synctest.Wait()
			task, _ := taskByID(s, "patrol")
			if task.StateNote == nil || task.StateNote.Text != note || task.StateNote.Revision != uint64(issue-100) {
				t.Fatalf("run %d did not commit the cursor: %+v", issue, task.StateNote)
			}
		}
	})
}

func TestTaskStateRecurringFollowupLeavesMainChat(t *testing.T) {
	node := readyHub()
	node.settings = &Settings{Main: "main", Timezone: "UTC"}
	s := newStorelessService(t, node)
	before := len(s.tasksView(t.Context(), nil))
	if _, err := s.toolScheduleFollowup(t.Context(), "server", "main", json.RawMessage(
		`{"title":"Public issue patrol","prompt":"Report only changes","every":"30m"}`,
	)); err != nil {
		t.Fatal(err)
	}
	tasks := s.tasksView(t.Context(), nil)
	if len(tasks) != before+1 {
		t.Fatalf("expected one new task, got %d", len(tasks)-before)
	}
	for _, task := range tasks {
		if task.Title != "Public issue patrol" {
			continue
		}
		if task.SessionID != "" || task.FreshEachRun {
			t.Fatalf("expected the task's own reusable chat: %+v", task)
		}
		first, err := runNow(t, s, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		second, err := runNow(t, s, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if first.SessionID == "" || first.SessionID == "main" || second.SessionID != first.SessionID {
			t.Fatalf("recurring runs should share a task chat: first %q, second %q", first.SessionID, second.SessionID)
		}
		return
	}
	t.Fatal("the patrol was not saved")
}
