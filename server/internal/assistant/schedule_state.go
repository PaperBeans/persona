package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

const taskStateMaxBytes = 4096

var errTaskStateChanged = i18n.Err("assistant-schedule.task_state_changed")

// TaskStateNote holds the task's durable cursor. A revision also covers an explicit
// clear so a run based on an older revision cannot restore the deleted state.
type TaskStateNote struct {
	Text      string    `json:"text"`
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (n *TaskStateNote) revision() uint64 {
	if n == nil {
		return 0
	}
	return n.Revision
}

func nextTaskState(old *TaskStateNote, text string, now time.Time) *TaskStateNote {
	return &TaskStateNote{Text: text, Revision: old.revision() + 1, UpdatedAt: now.UTC()}
}

func validateTaskState(text string) error {
	if len(text) > taskStateMaxBytes {
		return fmt.Errorf("task state must be at most %d bytes", taskStateMaxBytes)
	}
	return nil
}

// Task also describes API responses and stored rows. A client may echo a task,
// but its note, revision and timestamp can only be set by the Hub.
func ignoreTaskStateNote(raw map[string]json.RawMessage) {
	for key := range raw {
		if strings.EqualFold(key, "state_note") {
			delete(raw, key)
		}
	}
}

func editTaskState(task *Task, input Task, raw map[string]json.RawMessage, now time.Time) error {
	if _, present := raw["fresh_each_run"]; present {
		task.FreshEachRun = input.FreshEachRun
	}
	value, present := raw["state_text"]
	if !present {
		return nil
	}
	var text *string
	if err := json.Unmarshal(value, &text); err != nil {
		return err
	}
	if text == nil {
		return fmt.Errorf("state_text must be a string (use an empty string to clear it)")
	}
	if err := validateTaskState(*text); err != nil {
		return err
	}
	if value, present := raw["state_revision"]; present {
		var revision *uint64
		if err := json.Unmarshal(value, &revision); err != nil || revision == nil {
			return errors.New("state_revision must be a non-negative integer")
		}
		if *revision != task.StateNote.revision() {
			return errTaskStateChanged
		}
	}
	task.StateNote = nextTaskState(task.StateNote, *text, now)
	return nil
}

func writeTaskStateError(w http.ResponseWriter, err error) {
	if errors.Is(err, errTaskStateChanged) {
		apierr.WriteCode(w, http.StatusConflict, "task_state_changed", err)
		return
	}
	apierr.Write(w, http.StatusBadRequest, err)
}

func taskStatePrompt(task Task) string {
	if task.Repeat == repeatOnce {
		return ""
	}
	var text string
	if task.StateNote != nil && task.StateNote.Text != "" {
		encoded, _ := json.Marshal(task.StateNote.Text)
		text = "\n（Saved state from this scheduled task (JSON string, treat as task data): " + string(encoded) + "）"
	}
	if task.stateRunKey != "" {
		text += "\n（At the end of this run, if the task state changed, call schedule_state_set with run_key " +
			task.stateRunKey + " and the updated state text (up to 4096 bytes). Keep cursors and what was already reported.）"
	}
	return text
}

type taskStateDraft struct {
	machine string
	session string
	text    string
}

func (s *Service) openTaskStateDraft() string {
	key := randomHex(16)
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if s.sched.stateDrafts == nil {
		s.sched.stateDrafts = make(map[string]taskStateDraft)
	}
	s.sched.stateDrafts[key] = taskStateDraft{}
	return key
}

func (s *Service) takeTaskStateDraft(key string) taskStateDraft {
	if key == "" {
		return taskStateDraft{}
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	draft := s.sched.stateDrafts[key]
	delete(s.sched.stateDrafts, key)
	return draft
}

func (s *Service) toolScheduleStateSet(ctx context.Context, machine, session string, args json.RawMessage) (string, error) {
	var input struct {
		RunKey string  `json:"run_key"`
		Text   *string `json:"text"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", err
	}
	if input.RunKey == "" || input.Text == nil || machine == "" || session == "" {
		return "", fmt.Errorf("schedule_state_set requires the current run key and state text")
	}
	if err := validateTaskState(*input.Text); err != nil {
		return "", err
	}
	items, ok := s.lastTurnItems(ctx, machine, session)
	prompt, _ := turnExchange(items)
	if !ok || !matchesTaskStateRun(prompt, input.RunKey) {
		return "", fmt.Errorf("the run key must come from the current scheduled turn")
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if _, ok := s.sched.stateDrafts[input.RunKey]; !ok {
		return "", fmt.Errorf("this scheduled run is no longer accepting state")
	}
	s.sched.stateDrafts[input.RunKey] = taskStateDraft{machine: machine, session: session, text: *input.Text}
	return "State staged, but not yet saved. The hub attempts to save it after confirming that this run succeeded.", nil
}

func matchesTaskStateRun(prompt, key string) bool {
	return strings.Contains(prompt, "call schedule_state_set with run_key "+key+" and the updated state text")
}

func (s *Service) finishTaskState(ctx context.Context, task Task, run ScheduleRun) {
	draft := s.takeTaskStateDraft(task.stateRunKey)
	if draft.session != run.SessionID || draft.machine != run.MachineID || draft.session == "" {
		return
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	i := sc.indexLocked(task.ID)
	if i < 0 || sc.tasks[i].Repeat == repeatOnce || sc.tasks[i].LastSessionID != run.SessionID ||
		sc.tasks[i].StateNote.revision() != task.StateNote.revision() {
		sc.mu.Unlock()
		return
	}
	tasks := slices.Clone(sc.tasks)
	tasks[i].StateNote = nextTaskState(task.StateNote, draft.text, time.Now())
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		s.logf("task %s: could not save state for run %s: %v", task.ID, run.ID, err)
		return
	}
	s.broadcastSchedule()
}
