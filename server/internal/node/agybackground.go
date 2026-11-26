package node

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	agyTaskPrefix           = "Tool is running as a background task with task id:"
	agyBackgroundEndedLimit = 20
)

var (
	agyTaskName      = lazyre.New(`^task-[0-9]+$`)
	agyTaskEnd       = lazyre.New(`(?i)\bTask id "([^"\n]+)" (?:was )?(finished|failed|canceled|cancelled|killed|stopped)\b`)
	agyTaskMessage   = lazyre.New(`(?m)^[ \t]*\[Message\] timestamp=\S+ sender=(\S+) priority=\S+ content=([\s\S]*)`)
	agyCronIteration = lazyre.New(`\(iteration [0-9]+\)$`)
)

func agyBackgroundNotice(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "Created At:") {
		_, content, _ = strings.Cut(content, "\n")
	}
	if content, ok := strings.CutPrefix(strings.TrimSpace(content), agyTaskPrefix); ok {
		return content
	}
	return ""
}

func agyToolString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	// Native exports can encode a tool argument as JSON inside a string.
	var decoded string
	if strings.HasPrefix(value, `"`) && json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}

type agyBackgroundTasks struct {
	running []proto.BackgroundTask
	ended   []proto.BackgroundTask
}

func (b agyBackgroundTasks) apply(s *proto.Session) {
	s.Background = nil
	if !agyStopped(s.Status) {
		s.Background = b.running
	}
	s.SetBackgroundEnded(recentBackgroundEnded(b.ended))
}

type agyTaskRecord struct {
	task     proto.BackgroundTask
	inferred bool
	oneShot  bool
}

func (r *agyTaskRecord) finish(status string, at time.Time, inferred bool) {
	if r == nil || (r.task.Status != "" && (!r.inferred || inferred)) {
		return
	}
	r.task.Status, r.task.EndedAt, r.inferred = status, &at, inferred
}

func agyBackground(steps []agyStep, conversation, dir string, brain *os.Root) agyBackgroundTasks {
	byID := make(map[string]*agyTaskRecord)
	var ordered []*agyTaskRecord
	var planner agyStep
	for _, step := range steps {
		switch step.Type {
		case "PLANNER_RESPONSE":
			planner = step
			for _, call := range step.ToolCalls {
				if call == nil || call.Name != "manage_task" {
					continue
				}
				var args map[string]json.RawMessage
				if json.Unmarshal(call.Args, &args) == nil && strings.EqualFold(agyToolString(args["Action"]), "kill") {
					at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
					byID[agyToolString(args["TaskId"])].finish(proto.WorkflowKilled, at, false)
				}
			}
		case "USER_INPUT":
			planner = agyStep{}
		case "SYSTEM_MESSAGE":
			if m := agyTaskEnd.FindStringSubmatch(step.Content); m != nil {
				at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
				status := proto.WorkflowKilled
				switch strings.ToLower(m[2]) {
				case "finished":
					status = proto.WorkflowCompleted
				case "failed":
					status = proto.WorkflowFailed
				}
				byID[m[1]].finish(status, at, false)
			} else if m := agyTaskMessage.FindStringSubmatch(step.Content); m != nil {
				record := byID[m[1]]
				if record == nil || !record.oneShot {
					continue
				}
				message := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[2]), "</SYSTEM_MESSAGE>"))
				if agyCronIteration.MatchString(message) {
					record.oneShot = false
				} else if !strings.Contains(strings.ToLower(message), "still running") {
					at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
					record.finish(proto.WorkflowCompleted, at, false)
				}
			}
		case "GENERIC":
			notice := agyBackgroundNotice(firstNonEmpty(step.backgroundStart, step.Content))
			id, details, _ := strings.Cut(notice, "\n")
			id = strings.TrimSpace(id)
			name, ours := strings.CutPrefix(id, conversation+"/")
			if !ours || !agyTaskName.MatchString(name) {
				continue
			}
			at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
			record := byID[id]
			if record == nil {
				task := proto.BackgroundTask{ID: id, Kind: "shell", StartedAt: at}
				for line := range strings.SplitSeq(details, "\n") {
					line = strings.TrimSpace(line)
					if value, ok := strings.CutPrefix(line, "Task Description:"); ok && task.Description == "" {
						task.Description = strings.Clone(strings.TrimSpace(value))
					}
					if value, ok := strings.CutPrefix(line, "Task logs are available at:"); ok && task.OutputFile == "" {
						task.OutputFile = agyTaskLog(strings.TrimSpace(value), dir, conversation, name, brain)
					}
				}
				// Result steps have one slot per planner tool call, even when they are exported out of order.
				slot := step.seq - planner.seq - 1
				var call *agyToolCall
				if slot >= 0 && slot < len(planner.ToolCalls) {
					call = planner.ToolCalls[slot]
					if call != nil {
						setAgyTaskKind(&task, call.Name, call.Args)
					}
				}
				record = &agyTaskRecord{task: task, oneShot: agyOneShotSchedule(task.Description, call)}
				byID[id] = record
				ordered = append(ordered, record)
			}
			if updated, err := time.Parse(time.RFC3339Nano, step.UpdatedAt); err == nil {
				at = updated
			}
			switch strings.ToUpper(step.Status) {
			case "DONE":
				record.finish(proto.WorkflowCompleted, at, true)
			case "ERROR":
				record.finish(proto.WorkflowFailed, at, true)
			case "CANCELED", "CANCELLED":
				record.finish(proto.WorkflowKilled, at, true)
			}
		}
	}
	var result agyBackgroundTasks
	for _, record := range ordered {
		if record.task.EndedAt != nil {
			result.ended = append(result.ended, record.task)
		} else if len(result.running) < maxRuntimeBackground {
			result.running = append(result.running, record.task)
		}
	}
	slices.SortStableFunc(result.ended, func(a, b proto.BackgroundTask) int { return a.EndedAt.Compare(*b.EndedAt) })
	if extra := len(result.ended) - agyBackgroundEndedLimit; extra > 0 {
		result.ended = slices.Clone(result.ended[extra:])
	}
	return result
}

// agyOneShotSchedule recognizes timers from either the tool arguments or the task description.
// A cron expression or description takes precedence when both forms are present.
func agyOneShotSchedule(description string, call *agyToolCall) bool {
	if strings.HasPrefix(description, "Cron:") {
		return false
	}
	var args map[string]json.RawMessage
	if call != nil && call.Name == "schedule" && json.Unmarshal(call.Args, &args) == nil {
		if strings.TrimSpace(agyToolString(args["CronExpression"])) != "" {
			return false
		}
		if duration := args["DurationSeconds"]; duration != nil && string(duration) != "null" {
			return true
		}
	}
	return strings.HasPrefix(description, "Timer:")
}

func setAgyTaskKind(task *proto.BackgroundTask, tool string, raw json.RawMessage) {
	switch tool {
	case "schedule":
		task.Kind = "monitor"
	case "run_workflow":
		task.Kind = "workflow"
	case "invoke_subagent":
		task.Kind = "agent"
	case "run_command":
		var args map[string]json.RawMessage
		if json.Unmarshal(raw, &args) == nil {
			task.Command = firstNonEmpty(agyToolString(args["CommandLine"]), agyToolString(args["command"]))
		}
	}
}

// The export can name only this task's log inside its conversation directory.
func agyTaskLog(uri, dir, conversation, name string, brain *os.Root) string {
	path, err := url.PathUnescape(agyLocalFileURLToPath(uri))
	rel := filepath.Join(".system_generated", "tasks", name+".log")
	expected, absErr := filepath.Abs(filepath.Join(dir, "brain", conversation, rel))
	// The log can be announced before it is created.
	if err != nil || absErr != nil || !filepath.IsAbs(path) || filepath.Clean(path) != expected {
		return ""
	}
	info, err := brain.Stat(rel)
	if os.IsNotExist(err) || (err == nil && info.Mode().IsRegular()) {
		return expected
	}
	return ""
}

func agyStopped(status string) bool {
	return status == proto.StatusOffline || status == proto.StatusInterrupted
}

// agyPaneBackgroundFromRows reads the tasks the companion hides on ACP history rows.
func agyPaneBackgroundFromRows(params json.RawMessage) map[string][]proto.BackgroundTask {
	var push struct {
		Sessions []struct {
			ID    string                 `json:"session_id"`
			Tasks []proto.BackgroundTask `json:"agy_pane_background"`
		} `json:"sessions"`
	}
	if json.Unmarshal(params, &push) != nil {
		return nil
	}
	var background map[string][]proto.BackgroundTask
	for _, row := range push.Sessions {
		if len(row.Tasks) == 0 || !runtimeIDRe.MatchString(row.ID) {
			continue
		}
		if background == nil {
			background = make(map[string][]proto.BackgroundTask)
		}
		background[row.ID] = runtimeBackground(row.Tasks, maxRuntimeBackground, true)
	}
	return background
}

// agyPaneRuntimeRow reads the companion row and its pane inventory from the same list update.
func (b *runtimeRegistry) agyPaneRuntimeRow(id string) (proto.Session, bool) {
	h := b.ownerOf(id)
	if h == nil {
		return proto.Session{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hello.Runtime != agyRuntimeID || h.daemon != "" {
		return proto.Session{}, false
	}
	row, ok := h.rowLocked(id)
	if tasks := h.agyPaneBackground[id]; len(row.Background) == 0 && len(tasks) > 0 {
		row.Background = tasks
	}
	return row, ok
}

// Runtime task metadata cannot grant access to node-local files. Join the log
// paths independently confirmed by the local conversation's transcript.
func (n *Node) agyRuntimeTaskLogs(s *proto.Session, conversation string) {
	if len(s.Background) == 0 && len(s.BackgroundEnded()) == 0 {
		return
	}
	meta, ok := n.agyTranscriptMeta(conversation)
	if !ok {
		return
	}
	logs := make(map[string]string)
	for _, tasks := range [][]proto.BackgroundTask{meta.background.running, meta.background.ended} {
		for _, task := range tasks {
			if task.OutputFile != "" {
				logs[task.ID] = task.OutputFile
			}
		}
	}
	if len(logs) == 0 {
		return
	}
	attach := func(tasks []proto.BackgroundTask) []proto.BackgroundTask {
		tasks = slices.Clone(tasks)
		for i := range tasks {
			tasks[i].OutputFile = logs[tasks[i].ID]
		}
		return tasks
	}
	s.Background = attach(s.Background)
	s.SetBackgroundEnded(attach(s.BackgroundEnded()))
}
