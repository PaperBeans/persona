//go:build !windows

package node

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
)

// Deleting a room whose process still runs waits for it to exit before removing anything: Claude Code writes its last
// lines (the last prompt, its costs) into the transcript as it exits, and the file came back after the delete with the
// owner's last question in it (#4665). The room's session-env folder in its profile goes too.
func TestGuestDeleteWaitsForTheRoomsProcess(t *testing.T) {
	n, home := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	rec, room := "0f8fad5b-d9cb-469f-a165-70867728950e", "7d4a3c1e-1111-4222-8333-444455556666"
	rel := ".persona/guest-meeting/" + rec
	dir := n.assistantDirOf(rel)
	transcript := filepath.Join(home, ".claude", "projects", claudeProjectName(dir), room+".jsonl")
	sessionEnv := filepath.Join(home, ".claude", "session-env", room)
	for _, d := range []string{dir, filepath.Dir(transcript), sessionEnv} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n.state.put(room, sessionState{Launcher: "claude", Cwd: dir, Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})

	// The room's process: on SIGTERM it takes a moment, then appends its last line, as Claude Code does.
	cmd := exec.Command("/bin/sh", "-c", `trap 'sleep 0.3; printf "{\"type\":\"last-prompt\"}\n" >> "$F"; exit 0' TERM; echo ready; while :; do sleep 0.05; done`)
	cmd.Env = append(os.Environ(), "F="+transcript)
	cmd.SysProcAttr = osproc.GroupAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		_ = osproc.StopGroup(cmd.Process.Pid, true)
		t.Fatal(err)
	}
	p := &assistantProc{id: room, agent: "claude", cmd: cmd, stdin: stdin, exited: make(chan struct{}), born: time.Now()}
	go func() { _ = cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		if p.alive() {
			_ = osproc.StopGroup(cmd.Process.Pid, true)
		}
		<-p.exited
	})
	n.mu.Lock()
	n.procs[room] = p
	n.mu.Unlock()

	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
	default:
		t.Fatal("the delete returned while the room's process still ran")
	}
	for _, gone := range []string{transcript, sessionEnv, dir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there after the delete: %v", gone, err)
		}
	}
}
