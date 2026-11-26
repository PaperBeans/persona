package node

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/proto"
)

// A reader belongs to a single watched Codex rollout. The caller serializes Read.
// The snapshot holds only the invocation's public name and correlation id, never the
// script, arguments or output. It does not add items to the durable chat transcript.
type codexLiveToolReader struct {
	info              os.FileInfo
	offset            int64  // end of the last complete line
	anchor            []byte // bytes before offset, to detect a truncate-and-regrow
	turn              string
	open              bool
	active            []proto.LiveTool // start order
	nativeProcesses   []string         // recently observed native command items, including completions before the outer tool returns
	summary           string
	summaryOwner      string
	thinking          bool
	lastModelActivity time.Time
	reasoningID       string
	compacting        bool
	compactionID      string
}

func (r *codexLiveToolReader) Read(path string) (proto.LiveToolState, error) {
	f, err := os.Open(path)
	if err != nil {
		*r = codexLiveToolReader{}
		return proto.LiveToolState{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return proto.LiveToolState{}, err
	}
	same := r.info != nil && os.SameFile(st, r.info)
	if same && st.Size() == r.info.Size() && st.ModTime().Equal(r.info.ModTime()) {
		return r.snapshot(), nil
	}
	if !same || st.Size() <= r.info.Size() || !r.anchorMatches(f) {
		*r = codexLiveToolReader{}
		// Start at the latest actual turn start. In particular, completed/mirrored
		// calls in previous turns must never reappear on reconnect.
		end := eachLineBackAt(f, 0, st.Size(), usageMaxLine, func(line []byte, at int64) bool {
			if bytes.Contains(line, []byte(`"task_started"`)) {
				var m codexLiveRecord
				if json.Unmarshal(line, &m) == nil && m.Type == "event_msg" && m.Payload.Type == "task_started" {
					r.open = true
					r.offset = at
					return false
				}
			}
			return true
		})
		if !r.open {
			r.offset = end
		}
	}
	base := r.offset
	var accounted int64
	skipped := func() {
		// A record larger than the shared reader limit may have completed any
		// invocation. Drop uncertain indicators until new start evidence arrives.
		r.clearActivity()
	}
	read := eachLineAt(io.NewSectionReader(f, base, st.Size()-base), func(line []byte, at, size int64) bool {
		if at > accounted {
			skipped()
		}
		r.apply(line)
		accounted = at + size
		return true
	})
	if read > accounted {
		skipped()
	}
	r.offset = base + read
	r.info = st
	n := min(int64(96), r.offset)
	r.anchor = make([]byte, n)
	if n > 0 {
		if _, err := f.ReadAt(r.anchor, r.offset-n); err != nil {
			*r = codexLiveToolReader{}
			return proto.LiveToolState{}, err
		}
	}
	return r.snapshot(), nil
}

func (r *codexLiveToolReader) snapshot() proto.LiveToolState {
	return proto.LiveToolState{Tools: slices.Clone(r.active), Active: r.open, Thinking: r.thinking, Compacting: r.compacting, Summary: r.summary}
}

func (r *codexLiveToolReader) clearActivity() {
	r.active = nil
	r.nativeProcesses = nil
	r.summary, r.summaryOwner = "", ""
	r.thinking, r.reasoningID = false, ""
	r.compacting, r.compactionID = false, ""
}

func (r *codexLiveToolReader) anchorMatches(f *os.File) bool {
	n := len(r.anchor)
	var buf [96]byte
	if n == 0 {
		return true
	}
	_, err := f.ReadAt(buf[:n], r.offset-int64(n))
	return err == nil && bytes.Equal(buf[:n], r.anchor)
}

type codexLiveItem struct {
	ID, Type, Status, Server, Tool, Kind string
	ProcessID                            string          `json:"process_id"`
	Arguments                            json.RawMessage `json:"arguments"`
	Result                               struct {
		IsError bool `json:"isError"`
	} `json:"result"`
}

type codexLiveRecord struct {
	Timestamp string
	Type      string
	Payload   struct {
		Type, Namespace, Name, Status string
		ProcessID                     string `json:"process_id"`
		CallID                        string `json:"call_id"`
		TurnID                        string `json:"turn_id"`
		Item                          codexLiveItem
		Completions                   []struct{ Item codexLiveItem } `json:"display_completions"`
	}
}

func (r *codexLiveToolReader) apply(line []byte) {
	// All these probes are just a cheap reject. Only the outer envelope and its
	// immediate payload decide the event; embedded prompts are not events.
	if !bytes.Contains(line, []byte(`"call_id"`)) && !bytes.Contains(line, []byte(`"event_msg"`)) && !bytes.Contains(line, []byte(`"history_mutation"`)) && !bytes.Contains(line, []byte(`"compacted"`)) {
		return
	}
	var m codexLiveRecord
	if json.Unmarshal(line, &m) != nil {
		return
	}
	p := m.Payload
	// A new model response supersedes a retry diagnostic. Local tool completions
	// and token-count telemetry can still arrive while a stream is disconnected.
	if m.modelProgress() {
		if at, err := time.Parse(time.RFC3339Nano, m.Timestamp); err == nil && at.After(r.lastModelActivity) {
			r.lastModelActivity = at
		}
	}
	switch m.Type {
	case "compacted":
		// This is the recorded replacement history. It ends a pending compaction,
		// but a completed checkpoint alone never starts a live indicator.
		r.compacting, r.compactionID = false, ""

	case "event_msg":
		switch p.Type {
		case "task_started":
			if !r.open || r.turn != p.TurnID || p.TurnID == "" {
				r.clearActivity()
			}
			r.turn, r.open = p.TurnID, true
		case "task_complete", "turn_aborted", "turn_rolled_back":
			if p.TurnID == "" || r.turn == "" || p.TurnID == r.turn {
				r.clearActivity()
				r.turn, r.open = "", false
			}
		case "exec_command_begin", "exec_command_end":
			if p.TurnID != "" && r.turn != "" && p.TurnID != r.turn {
				return
			}
			item := codexLiveItem{ID: p.CallID, ProcessID: p.ProcessID, Type: "CommandExecution", Status: p.Status}
			if p.Type == "exec_command_begin" {
				r.noteCommand(item)
				r.start(item.ID, item.liveName())
			} else {
				r.complete(item)
			}
		case "agent_reasoning_delta", "agent_reasoning_raw_content_delta":
			if r.open {
				r.thinking = true
			}
		case "item_started":
			switch p.Item.Type {
			case "ContextCompaction", "contextCompaction", "context_compaction":
				if r.open && (p.TurnID == "" || r.turn == "" || p.TurnID == r.turn) {
					r.compacting, r.compactionID = true, p.Item.ID
					r.thinking, r.reasoningID = false, ""
				}
			case "Reasoning":
				if r.open {
					r.thinking, r.reasoningID = true, p.Item.ID
				}
			case "AgentMessage":
				r.thinking, r.reasoningID = false, ""
			default:
				r.noteCommand(p.Item)
				r.start(p.Item.ID, p.Item.liveName())
			}
		case "item_completed":
			r.complete(p.Item)
		}
	case "history_mutation":
		for _, completion := range p.Completions {
			r.complete(completion.Item)
		}
	case "response_item":
		switch p.Type {
		case "function_call", "custom_tool_call":
			// 'completed' here means Codex finished *writing the call*. Its output
			// or the terminal display record is what ends the invocation.
			if p.Name == "" {
				return
			}
			name := p.Name
			if p.Namespace != "" {
				name = p.Namespace + "." + name
			}
			r.start(p.CallID, name)
		case "function_call_output", "custom_tool_call_output":
			if bytes.Contains(line, []byte("session_id")) {
				for _, tool := range r.active {
					if tool.ID == p.CallID && (tool.Name == "exec" || tool.Name == "functions.exec" || tool.Name == "wait" || tool.Name == "functions.wait") {
						r.yieldedCommands(line)
						break
					}
				}
			}
			r.stop(p.CallID)
		}
	}
}

func (i codexLiveItem) liveName() string {
	switch i.Type {
	case "CommandExecution":
		return "Bash"
	case "McpToolCall":
		if i.Server != "" && i.Tool != "" {
			return "mcp__" + i.Server + "__" + i.Tool
		}
	case "FileChange":
		return "Edit"
	case "ImageView":
		return "ViewImage"
	case "Extension":
		return i.Kind
	case "CollabAgentToolCall":
		return i.Tool
	}
	return ""
}

// A code-mode call may finish while exec_command still owns a running terminal.
// Only consume complete structured receipts, never match fragments in command output.
func (r *codexLiveToolReader) yieldedCommands(line []byte) {
	var record struct {
		Payload struct{ Output []struct{ Type, Text string } }
	}
	if json.Unmarshal(line, &record) != nil {
		return
	}
	type receipt struct {
		Chunk   string   `json:"chunk_id"`
		Session int64    `json:"session_id"`
		Exit    *int     `json:"exit_code"`
		Elapsed *float64 `json:"wall_time_seconds"`
		Output  *string  `json:"output"`
	}
	for _, part := range record.Payload.Output {
		if part.Type != "input_text" || len(part.Text) > 64<<10 || !strings.Contains(part.Text, `"session_id"`) {
			continue
		}
		var result struct {
			receipt
			Status string
			Value  *receipt
		}
		if json.Unmarshal([]byte(part.Text), &result) != nil {
			continue
		}
		entry := &result.receipt
		if result.Status == "fulfilled" && result.Value != nil {
			entry = result.Value
		}
		if entry.Session <= 0 || entry.Chunk == "" || entry.Elapsed == nil || entry.Exit != nil || entry.Output == nil {
			continue
		}
		id := strconv.FormatInt(entry.Session, 10)
		if !slices.Contains(r.nativeProcesses, id) {
			r.start("terminal-session:"+id, "Bash")
		}
	}
}

func (r *codexLiveToolReader) noteCommand(i codexLiveItem) {
	if !r.open || i.Type != "CommandExecution" || i.ProcessID == "" {
		return
	}
	r.stop("terminal-session:" + i.ProcessID)
	if !slices.Contains(r.nativeProcesses, i.ProcessID) {
		if len(r.nativeProcesses) == 128 {
			r.nativeProcesses = r.nativeProcesses[1:]
		}
		r.nativeProcesses = append(r.nativeProcesses, i.ProcessID)
	}
}

func (r *codexLiveToolReader) complete(i codexLiveItem) {
	if !r.open {
		return
	}
	r.noteCommand(i)
	switch i.Type {
	case "ContextCompaction", "contextCompaction", "context_compaction":
		if r.compactionID == "" || i.ID == r.compactionID {
			r.compacting, r.compactionID = false, ""
		}
		return
	}
	if i.Type == "Reasoning" && (r.reasoningID == "" || i.ID == r.reasoningID) || i.Type == "AgentMessage" {
		r.thinking, r.reasoningID = false, ""
	}
	if i.Type == "McpToolCall" && i.Server == "codex_app" && i.Tool == "update_running_summary" &&
		i.Status == "completed" && !i.Result.IsError && len(r.active) == 1 &&
		(r.active[0].Name == "exec" || r.active[0].Name == "functions.exec") {
		// This inner display record has its own id. Bind its heading only when
		// there is a single enclosing executor, and only for that executor's life.
		var args struct {
			Labels []string `json:"labels"`
		}
		if json.Unmarshal(i.Arguments, &args) == nil {
			if title := codexLiveTitle(args.Labels); title != "" {
				r.summary, r.summaryOwner = title, r.active[0].ID
			}
		}
	}
	switch i.Status {
	case "backgrounded", "inProgress", "in_progress":
		r.start(i.ID, i.liveName())
	default:
		r.stop(i.ID)
	}
}

// Labels are complete headings, or [present verb, past verb, subject]. Distinguish
// the latter only for recognizable pairs; choose the first supplied heading otherwise.
func codexLiveTitle(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	title := strings.TrimSpace(labels[0])
	if title == "" {
		return ""
	}
	if len(labels) == 3 {
		past, subject := strings.TrimSpace(labels[1]), strings.TrimSpace(labels[2])
		en := strings.HasSuffix(strings.ToLower(title), "ing") && !strings.ContainsAny(title+past, " \t") &&
			strings.HasSuffix(strings.ToLower(past), "ed")
		zh := past == "已"+strings.TrimPrefix(title, "正在")
		if subject != "" {
			if en {
				title += " " + subject
			} else if zh {
				title += subject
			}
		}
	}
	if utf8.RuneCountInString(title) > 160 || strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return ""
	}
	return title
}

func (r *codexLiveToolReader) start(id, name string) {
	if !r.open || len(id) == 0 || len(id) > 512 || len(name) == 0 || len(name) > 256 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return
	}
	r.thinking, r.reasoningID = false, ""
	for _, t := range r.active {
		if t.ID == id {
			return
		}
	}
	// Bound unfinished calls in malformed or abandoned rollouts.
	if len(r.active) == 128 {
		r.stop(r.active[0].ID)
	}
	r.active = append(r.active, proto.LiveTool{ID: id, Name: name})
}

func (r *codexLiveToolReader) stop(id string) {
	if id == r.summaryOwner {
		r.summary, r.summaryOwner = "", ""
	}
	for idx, t := range r.active {
		if t.ID == id {
			r.active = slices.Delete(r.active, idx, idx+1)
			return
		}
	}
}
