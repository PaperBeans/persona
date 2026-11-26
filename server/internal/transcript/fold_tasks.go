package transcript

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

var foldCreatedTask = lazyre.New(`Task #([A-Za-z0-9_-]+) created successfully`)
var foldListedTask = lazyre.New(`^#([A-Za-z0-9_-]+) \[(pending|in_progress|completed)\] (.+)$`)

type taskFields map[string]json.RawMessage

func (f taskFields) str(key string) *string {
	var value any
	if json.Unmarshal(f[key], &value) != nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		return &v
	case float64, bool:
		s := string(f[key])
		return &s
	}
	return nil
}

func taskObject(raw []byte) taskFields {
	var fields taskFields
	_ = json.Unmarshal(raw, &fields)
	return fields
}

func foldTaskSnapshots(run []proto.Item, uses map[string]proto.Item) []proto.FoldTaskSnapshot {
	var snapshots []proto.FoldTaskSnapshot
	for _, result := range run {
		if result.Kind != proto.KindToolResult || result.IsError {
			continue
		}
		use, ok := uses[result.ToolUseID]
		if !ok {
			continue
		}
		if !planTools[use.ToolName] {
			continue
		}
		input, output := taskObject(use.ToolInput), strings.TrimSpace(StripToolResultTimestampHeaders(result.Text))
		obj := taskObject([]byte(output))
		if string(obj["success"]) == "false" || (obj["error"] != nil && string(obj["error"]) != "null") {
			continue
		}
		snapshot := proto.FoldTaskSnapshot{Entries: []proto.FoldTask{}, SourceItemID: use.ID, SourceToolUseID: use.ToolUseID, SourceSeq: use.Seq}
		switch use.ToolName {
		case "TodoWrite", claudeDesktopPlan:
			key := "todos"
			if use.ToolName == claudeDesktopPlan {
				key = "items"
			}
			raw, exists := obj[key]
			if !exists {
				raw = input[key]
			}
			var todos []taskFields
			if string(raw) == "null" || json.Unmarshal(raw, &todos) != nil {
				continue
			}
			valid := true
			for i, todo := range todos {
				subject := todo.str("content")
				if subject == nil || strings.TrimSpace(*subject) == "" {
					valid = false
					break
				}
				snapshot.Entries = append(snapshot.Entries, proto.FoldTask{ID: "todo:" + strconv.Itoa(i), Subject: subject, Description: todo.str("activeForm"), Status: normalizedTaskStatus(todo.str("status"))})
			}
			if !valid {
				continue
			}
			snapshot.Replace = "todo"
		case "TaskCreate", "TaskUpdate":
			fields := taskObject(obj["task"])
			if fields == nil {
				fields = obj
			}
			id := fields.str("id")
			if id == nil {
				id = fields.str("taskId")
			}
			if id == nil && use.ToolName == "TaskUpdate" {
				id = input.str("taskId")
			}
			if id == nil && use.ToolName == "TaskCreate" {
				if match := foldCreatedTask.FindStringSubmatch(output); match != nil {
					id = &match[1]
				}
			}
			if id == nil {
				continue
			}
			merged := taskFields{}
			for k, v := range input {
				merged[k] = v
			}
			for k, v := range fields {
				merged[k] = v
			}
			entry := proto.FoldTask{ID: "task:" + *id, Subject: merged.str("subject"), Description: merged.str("description"), Status: merged.str("status")}
			if use.ToolName == "TaskCreate" {
				snapshot.FallbackStatus = "pending"
			}
			snapshot.Entries = append(snapshot.Entries, entry)
		case "TaskList":
			raw, exists := obj["tasks"]
			if !exists {
				raw = json.RawMessage(output)
			}
			var entries []taskFields
			if string(raw) == "null" || json.Unmarshal(raw, &entries) != nil {
				if output != "No tasks found" && output != "No tasks found." {
					valid := true
					for _, line := range strings.Split(output, "\n") {
						if strings.TrimSpace(line) == "" {
							continue
						}
						match := foldListedTask.FindStringSubmatch(strings.TrimSpace(line))
						if match == nil {
							valid = false
							break
						}
						fields := taskFields{}
						for k, v := range map[string]string{"id": match[1], "status": match[2], "subject": match[3]} {
							fields[k], _ = json.Marshal(v)
						}
						entries = append(entries, fields)
					}
					if !valid || len(entries) == 0 {
						continue
					}
				}
			}
			valid := true
			for _, fields := range entries {
				id, subject := fields.str("id"), fields.str("subject")
				if id == nil || subject == nil {
					valid = false
					break
				}
				snapshot.Entries = append(snapshot.Entries, proto.FoldTask{ID: "task:" + *id, Subject: subject, Description: fields.str("description"), Status: fields.str("status")})
			}
			if !valid {
				continue
			}
			snapshot.Replace = "task"
		default:
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func normalizedTaskStatus(status *string) *string {
	if status != nil && (*status == "pending" || *status == "in_progress" || *status == "completed") {
		return status
	}
	unknown := "unknown"
	return &unknown
}
