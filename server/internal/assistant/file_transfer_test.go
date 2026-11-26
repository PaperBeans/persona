package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/proto"
)

// newFileTransferTest is a manager carrying "mac" (the caller's) and "box", both online, and "lab", a work computer.
func newFileTransferTest(t *testing.T) *fleetTest {
	t.Helper()
	ft := newFleetTest(t)
	ft.d.add("box", "Box")
	ft.d.add("lab", "Lab").trust = "work"
	ft.f.devices = []Device{{ID: "mac", Name: "Mac", Online: true}, {ID: "box", Name: "Box", Online: true}, {ID: "lab", Name: "Lab", Online: true}}
	ft.f.hello.Assistant.MachineID = "mac" // the assistant's computer
	return ft
}

func (ft *fleetTest) fileTransfer(machine, session, args string, assistant bool) proto.ToolResult {
	return ft.s.mcpCall(context.Background(), machine, session, fileTransferTool, json.RawMessage(args), assistant)
}

func (ft *fleetTest) transfersAsked() []devices.AgentTransfer {
	ft.d.mu.Lock()
	defer ft.d.mu.Unlock()
	return append([]devices.AgentTransfer(nil), ft.d.transfers...)
}

// Agents and assistant conversations both get file_transfer, unless manager.json turns it off; then a call copies
// nothing and says how to turn it on.
func TestFileTransferIsListedForAgentsAndTheAssistant(t *testing.T) {
	ft := newFileTransferTest(t)
	for _, assistant := range []bool{false, true} {
		if !toolListed(ft.s.mcpTools(context.Background(), assistant), fileTransferTool) {
			t.Fatalf("file_transfer not listed (assistant %v)", assistant)
		}
	}
	off := false
	ft.f.hello.Agents.FileTransfer.Enabled = &off
	for _, assistant := range []bool{false, true} {
		if toolListed(ft.s.mcpTools(context.Background(), assistant), fileTransferTool) {
			t.Fatalf("file_transfer listed while off (assistant %v)", assistant)
		}
	}
	r := ft.fileTransfer("mac", "s1", `{"direction":"send","machine":"box","path":"~/a.pdf"}`, false)
	if !r.IsError || !strings.Contains(resultText(r), "agents.file_transfer.enabled") || len(ft.transfersAsked()) != 0 {
		t.Fatalf("off: %q, %v", resultText(r), ft.transfersAsked())
	}
}

// send copies from the caller's computer to the one named (by id or name), fetch the other way; who called and how is
// handed over for the audit, and the answer says where it landed and its checked SHA-256.
func TestFileTransferSendsAndFetches(t *testing.T) {
	ft := newFileTransferTest(t)
	r := ft.fileTransfer("mac", "agent-1", `{"direction":"send","machine":"Box","path":"~/out/report.pdf","dest":"~/in/"}`, false)
	out := resultText(r)
	if r.IsError || !strings.Contains(out, "Copied report.pdf") || !strings.Contains(out, "Box:/home/me/Downloads/Persona/report.pdf") ||
		!strings.Contains(out, "SHA-256 abab") {
		t.Fatalf("send: %q", out)
	}
	r = ft.fileTransfer("mac", "conv-1", `{"direction":"fetch","machine":"box","path":"/data/set.csv","overwrite":true}`, true)
	if r.IsError {
		t.Fatalf("fetch: %q", resultText(r))
	}
	got := ft.transfersAsked()
	want := []devices.AgentTransfer{
		{From: "mac", To: "box", Path: "~/out/report.pdf", Dest: "~/in/", CallerMachine: "mac", CallerSession: "agent-1", Direction: "send"},
		{From: "box", To: "mac", Path: "/data/set.csv", Overwrite: true, CallerMachine: "mac", CallerSession: "conv-1", Assistant: true, Direction: "fetch"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("transfers %+v", got)
	}

	// A folder: its files, what was left out, the zip's checked hash.
	ft.d.transfer = func(t devices.AgentTransfer) (devices.AgentTransferResult, error) {
		return devices.AgentTransferResult{Kind: proto.FilePackDir, Name: "proj", Source: t.Path, ResultPath: "/home/u/proj", Files: 12,
			Bytes: 40 << 10, Size: 9 << 10, Skipped: 2, SkippedNames: []string{".env", "keys/server.pem"}, SHA256: "cd"}, nil
	}
	out = resultText(ft.fileTransfer("mac", "agent-1", `{"direction":"send","machine":"box","path":"~/proj"}`, false))
	for _, want := range []string{"the folder proj (12 files, 40.0 KB)", "Left out 2 entries", ".env, keys/server.pem", "9.0 KB zip with SHA-256 cd"} {
		if !strings.Contains(out, want) {
			t.Errorf("folder answer %q lacks %q", out, want)
		}
	}
}

// What the tool cannot do is refused before anything moves: a missing or relative path, this computer itself, an
// unknown computer (the answer lists the others), a work computer. The devices' refusal is passed on.
func TestFileTransferRefusals(t *testing.T) {
	ft := newFileTransferTest(t)
	for _, c := range []struct{ args, says string }{
		{`{"direction":"copy","machine":"box","path":"~/a"}`, `direction must be "send", "fetch" or "list"`},
		{`{"direction":"send","path":"~/a"}`, "machine is required"},
		{`{"direction":"send","machine":"box"}`, "path is required"},
		{`{"direction":"send","machine":"box","path":"a.txt"}`, "must be absolute or start with ~/"},
		{`{"direction":"fetch","machine":"box","path":"~/a","dest":"in"}`, "dest \"in\" must be absolute"},
		{`{"direction":"send","machine":"mac","path":"~/a"}`, "that is the computer you run on"},
		{`{"direction":"send","machine":"nope","path":"~/a"}`, "there is no computer \"nope\" (computers: box (online))"},
		{`{"direction":"fetch","machine":"lab","path":"~/a"}`, "Lab is a work computer"},
	} {
		r := ft.fileTransfer("mac", "agent-1", c.args, false)
		if !r.IsError || !strings.Contains(resultText(r), c.says) || !strings.Contains(resultText(r), "nothing was copied") {
			t.Errorf("%s: %q, want %q", c.args, resultText(r), c.says)
		}
	}
	if got := ft.transfersAsked(); len(got) != 0 {
		t.Fatalf("refused calls moved files: %+v", got)
	}
	ft.d.transfer = func(devices.AgentTransfer) (devices.AgentTransferResult, error) {
		return devices.AgentTransferResult{}, &devices.AgentTransferError{Code: "offline", Message: "Box is offline (not connected to Persona now); nothing was sent"}
	}
	r := ft.fileTransfer("mac", "agent-1", `{"direction":"send","machine":"box","path":"~/a"}`, false)
	if !r.IsError || !strings.Contains(resultText(r), "Box is offline") {
		t.Fatalf("offline: %q", resultText(r))
	}
}

// A work computer's Agents do not get to move files, nor do Inbox-model turns.
func TestFileTransferIsNotForWorkComputersOrInboxTurns(t *testing.T) {
	ft := newFileTransferTest(t)
	r := ft.fileTransfer("lab", "agent-9", `{"direction":"send","machine":"box","path":"~/a"}`, false)
	if !r.IsError {
		t.Fatalf("a work computer's call: %q", resultText(r))
	}
	r = ft.s.mcpCallWithOrigin(context.Background(), "mac", "inbox-1", fileTransferTool,
		json.RawMessage(`{"direction":"send","machine":"box","path":"~/a"}`), true, OriginInbox)
	if !r.IsError || !strings.Contains(resultText(r), "Inbox conversations cannot call file_transfer") {
		t.Fatalf("an Inbox turn: %q", resultText(r))
	}
	if got := ft.transfersAsked(); len(got) != 0 {
		t.Fatalf("moved: %+v", got)
	}
}

// list names the other computers that can take part, with their state and home: not this one, not a work computer.
func TestFileTransferListsTheComputers(t *testing.T) {
	ft := newFileTransferTest(t)
	ft.d.add("old", "Old").away = true
	out := resultText(ft.fileTransfer("mac", "agent-1", `{"direction":"list"}`, false))
	for _, want := range []string{"You are on Mac.", "- box, Box, online, /home/u", "- old, Old, offline, /home/u"} {
		if !strings.Contains(out, want) {
			t.Errorf("list %q lacks %q", out, want)
		}
	}
	if strings.Contains(out, "lab") || strings.Contains(out, "- mac") {
		t.Errorf("list names this or a work computer: %q", out)
	}
}
