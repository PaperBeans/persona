package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/proto"
)

// file_transfer (#14683, docs/protocol/file-transfer.md "Agents and the assistant"): an Agent or an assistant
// conversation copies a file or a folder between its own computer and another of the user's, in one call, instead of
// ssh / scp (the computers often cannot reach each other) or a chat upload. One end is always the caller's computer.
// The devices move the bytes node to node (devices/agent_transfer.go); this side decides who may and to where: only
// the user's confirmed personal computers, both online, never a work computer (whose own calls never reach here:
// mcpCallWithOrigin). Guests, meeting turns, Inbox-model turns and scheduled routines do not get the tool (their
// allowlists do not name it). manager.json agents.file_transfer.enabled false turns it off.

const fileTransferTool = "file_transfer"

// fileTransferWait bounds one call: the largest transfer over a slow link, with the folder packed and unpacked.
const fileTransferWait = 90 * time.Minute

// fileTransferTools is the tool while it is on and the manager has the computers' connections, none otherwise.
func (s *Service) fileTransferTools(ctx context.Context) []proto.Tool {
	if s.devs == nil || !s.hello(ctx).Agents.FileTransfer.On() {
		return nil
	}
	return toolsNamed([]string{fileTransferTool})
}

// fileTransferPath: p names a path on a computer as the tool takes it, absolute or in the home (~/).
func fileTransferPath(p string) bool {
	return p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "/") || filepath.VolumeName(p) != "" || isWindowsAbs(p)
}

// isWindowsAbs: C:\… or C:/…, a Windows computer's absolute path, whatever system the manager runs on.
func isWindowsAbs(p string) bool {
	return len(p) >= 3 && (p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z') && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// fileTransferToolCall runs file_transfer for the conversation or Agent session on machine.
func (s *Service) fileTransferToolCall(ctx context.Context, machine, session string, args json.RawMessage, assistant bool) proto.ToolResult {
	text, err := s.fileTransfer(ctx, machine, session, args, assistant)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(text, false)
}

func (s *Service) fileTransfer(ctx context.Context, machine, session string, args json.RawMessage, assistant bool) (string, error) {
	if !s.hello(ctx).Agents.FileTransfer.On() {
		return "", errors.New("moving files between computers is switched off (agents.file_transfer.enabled in manager.json); nothing was copied")
	}
	if s.devs == nil {
		return "", errors.New("this Persona cannot reach the computers' connections, so files cannot be moved between them; nothing was copied")
	}
	var a struct {
		Direction string `json:"direction"`
		Machine   string `json:"machine"`
		Path      string `json:"path"`
		Dest      string `json:"dest"`
		Overwrite bool   `json:"overwrite"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("arguments: %v", err)
		}
	}
	a.Direction, a.Machine, a.Path, a.Dest = strings.TrimSpace(a.Direction), strings.TrimSpace(a.Machine), strings.TrimSpace(a.Path), strings.TrimSpace(a.Dest)
	here := s.canonical(machine)
	switch a.Direction {
	case "list":
		return s.fileTransferList(ctx, here), nil
	case "send", "fetch":
	default:
		return "", errors.New(`direction must be "send", "fetch" or "list"; nothing was copied`)
	}
	switch {
	case a.Machine == "":
		return "", fmt.Errorf("machine is required (the other computer; %s); nothing was copied", s.fileTransferNames(ctx, here))
	case a.Path == "":
		return "", errors.New("path is required: the file or folder to copy; nothing was copied")
	case !fileTransferPath(a.Path):
		return "", fmt.Errorf("path %q must be absolute or start with ~/ (the home of the computer it is on); nothing was copied", a.Path)
	case a.Dest != "" && !fileTransferPath(a.Dest):
		return "", fmt.Errorf("dest %q must be absolute or start with ~/ (the home of the receiving computer); nothing was copied", a.Dest)
	}
	other, ok := s.machineByName(ctx, a.Machine)
	if !ok {
		return "", fmt.Errorf("there is no computer %q (%s); nothing was copied", a.Machine, s.fileTransferNames(ctx, here))
	}
	otherID := s.canonical(other.ID)
	name := cmp.Or(other.Name, otherID)
	switch {
	case otherID == here || s.sameMachine(otherID, here):
		return "", errors.New("that is the computer you run on: copy files there with your own tools (cp, mv); nothing was copied")
	case s.isWorkMachine(otherID):
		return "", fmt.Errorf("%s is a work computer: files are not moved to or from it; nothing was copied", name)
	case s.machineTrust(ctx, otherID) != nil:
		return "", fmt.Errorf("%s is not confirmed in Persona yet: files are not moved to or from it until the user confirms it; nothing was copied", name)
	}
	t := devices.AgentTransfer{From: here, To: otherID, Path: a.Path, Dest: a.Dest, Overwrite: a.Overwrite,
		CallerMachine: here, CallerSession: session, Assistant: assistant, Direction: a.Direction}
	if a.Direction == "fetch" {
		t.From, t.To = otherID, here
	}
	cctx, cancel := context.WithTimeout(ctx, fileTransferWait)
	defer cancel()
	res, err := s.devs.AgentTransfer(cctx, t)
	if err != nil {
		return "", fmt.Errorf("%w; nothing was copied", err)
	}
	return s.fileTransferDone(t, res), nil
}

// fileTransferDone words a done transfer for the model.
func (s *Service) fileTransferDone(t devices.AgentTransfer, res devices.AgentTransferResult) string {
	from, to := s.machineName(t.From), s.machineName(t.To)
	var b strings.Builder
	if res.Kind == proto.FilePackDir {
		fmt.Fprintf(&b, "Copied the folder %s (%d files, %s) from %s:%s to %s:%s.", res.Name, res.Files, byteSize(res.Bytes),
			from, res.Source, to, res.ResultPath)
		if res.Skipped > 0 {
			fmt.Fprintf(&b, " Left out %d entries (credentials by their name, links, special or unreadable files)", res.Skipped)
			if len(res.SkippedNames) > 0 {
				fmt.Fprintf(&b, ", among them: %s", strings.Join(res.SkippedNames, ", "))
			}
			b.WriteString(".")
		}
		fmt.Fprintf(&b, " It went as a %s zip with SHA-256 %s, checked on %s before it was unpacked.", byteSize(res.Size), res.SHA256, to)
	} else {
		fmt.Fprintf(&b, "Copied %s (%s) from %s:%s to %s:%s. SHA-256 %s, checked on %s before the file got its name.",
			res.Name, byteSize(res.Size), from, res.Source, to, res.ResultPath, res.SHA256, to)
	}
	return b.String()
}

// byteSize is n bytes for a person: 512 B, 3.4 KB, 12.0 MB, 1.5 GB.
func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

// fileTransferComputer is one computer as list shows it.
type fileTransferComputer struct {
	ID, Name, Home, State string
}

// fileTransferComputers are the computers file_transfer can reach from here: the user's confirmed personal ones other
// than here, online or not.
func (s *Service) fileTransferComputers(ctx context.Context, here string) []fileTransferComputer {
	var out []fileTransferComputer
	for _, d := range s.devs.List() {
		id := s.canonical(d.ID)
		if id == here || s.sameMachine(id, here) || d.Trust == "work" || s.isWorkMachine(id) || s.machineTrust(ctx, id) != nil {
			continue
		}
		state := "offline"
		if d.Online {
			state = "online"
		}
		out = append(out, fileTransferComputer{ID: id, Name: cmp.Or(d.Name, id), Home: d.Home, State: state})
	}
	slices.SortFunc(out, func(a, b fileTransferComputer) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// fileTransferList answers direction list.
func (s *Service) fileTransferList(ctx context.Context, here string) string {
	list := s.fileTransferComputers(ctx, here)
	if len(list) == 0 {
		return "There is no other computer of the user's to copy files to or from (work computers and computers not yet confirmed are left out)."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are on %s. The other computers (id, name, state, home):", s.machineName(here))
	for _, c := range list {
		fmt.Fprintf(&b, "\n- %s, %s, %s, %s", c.ID, c.Name, c.State, cmp.Or(c.Home, "home unknown"))
	}
	b.WriteString("\nOnly an online computer can send or receive.")
	return b.String()
}

// fileTransferNames lists the computers for an error: "computers: a (online), b (offline)".
func (s *Service) fileTransferNames(ctx context.Context, here string) string {
	list := s.fileTransferComputers(ctx, here)
	if len(list) == 0 {
		return "there is no other computer to use"
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, fmt.Sprintf("%s (%s)", c.ID, c.State))
	}
	return "computers: " + strings.Join(names, ", ")
}
