package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func missingCodexDesktopBus(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

// Probe the CLI bundled with this App once per installed build. Other Codex homes can belong
// to different desktop apps; opening the default App would send their input to the wrong owner.
var queueBuild struct {
	sync.Mutex
	info os.FileInfo
	cli  string
}

var codexDesktopQueueCLI = func(home string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	defaultHome := filepath.Join(userHome, ".codex")
	if home != defaultHome && home != realPath(defaultHome) {
		return ""
	}
	cli := bundledCodexCLI()
	if cli == "" {
		return ""
	}
	info, err := os.Stat(filepath.Join(codexQueueApp(cli), "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	queueBuild.Lock()
	defer queueBuild.Unlock()
	if last := queueBuild.info; last != nil && os.SameFile(last, info) && last.ModTime() == info.ModTime() && last.Size() == info.Size() {
		return queueBuild.cli
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "queue", "--help").Output()
	if err != nil { // A bundle replacement can briefly make the executable unavailable.
		return ""
	}
	queueBuild.info, queueBuild.cli = info, ""
	if help := string(out); strings.Contains(help, "--thread") && strings.Contains(help, "--message") {
		queueBuild.cli = cli
	}
	return queueBuild.cli
}

func codexQueueApp(cli string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(cli), "../../../.."))
}

// Target the same bundle as the sender CLI even when a second app handles codex:// links.
var openCodexQueueThread = func(ctx context.Context, cli string, src codexSource) error {
	app := codexQueueApp(cli)
	// A loaded thread already has an owner to drain the queue. Only open a window
	// when necessary, so a phone send to another loaded chat leaves the Mac's focus alone.
	lock := filepath.Join(src.home, "thread-writer-locks", src.thread+".lock")
	if _, err := os.Stat(lock); err == nil {
		probe, stop := context.WithTimeout(ctx, 2*time.Second)
		out, _ := exec.CommandContext(probe, "/usr/sbin/lsof", "-nP", "-t", "--", lock).Output()
		stop()
		for _, field := range strings.Fields(string(out)) {
			pid, _ := strconv.Atoi(field)
			for pid > 1 {
				path, err := processExecPath(pid)
				if err != nil {
					break
				}
				if path == filepath.Join(app, "Contents/MacOS/ChatGPT") {
					return nil
				}
				parent, err := parentPID(pid)
				if err != nil || parent == pid {
					break
				}
				pid = parent
			}
		}
	}
	return exec.CommandContext(ctx, "/usr/bin/open", "-a", app, "codex://threads/"+src.thread).Run()
}

func (n *Node) enqueueCodexDesktop(ctx context.Context, cli string, src codexSource, input []map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, codexDesktopTimeout)
	defer cancel()
	if !rolloutUUID(src.thread) {
		return refusal("codex_read_only", "Invalid Codex desktop thread ID", nil)
	}
	s, err := startCodexStdio(ctx, cli, src.home, os.TempDir())
	if err != nil {
		return refusal("codex_read_only", err.Error(), nil)
	}
	defer s.close()

	id, _ := ctx.Value(sendTraceKey{}).(string)
	if id == "" {
		id = newUUID()
	}
	// list verifies queue support before opening a window. A retry during a restart may
	// refer to a submission that Codex already persisted but has not yet consumed.
	pending, err := codexSubmissionPending(ctx, s, src.thread, id)
	if err != nil {
		return refusal("codex_read_only", err.Error(), nil)
	}
	if err := openCodexQueueThread(ctx, cli, src); err != nil && !pending {
		return refusal("codex_read_only", fmt.Sprintf("Could not open the conversation in the Codex app: %v", err), nil)
	}
	if pending {
		markSendQueued(ctx)
		return nil
	}
	params := map[string]any{"threadId": src.thread, "clientUserMessageId": id, "input": input}
	var ack struct {
		QueuedSubmission struct {
			ID                  string `json:"id"`
			ClientUserMessageID string `json:"clientUserMessageId"`
		} `json:"queuedSubmission"`
	}
	if err := s.call(ctx, "thread/queue/add", params, &ack); err != nil {
		var rejected *codexAppServerError
		if errors.As(err, &rejected) {
			return refusal("codex_read_only", err.Error(), nil)
		}
		return uncertain(err)
	}
	if ack.QueuedSubmission.ID == "" || ack.QueuedSubmission.ClientUserMessageID != id {
		return uncertain(errors.New("Codex returned no matching message receipt"))
	}
	markSendQueued(ctx)
	n.notifyCodex()
	return nil
}

func codexSubmissionPending(ctx context.Context, s *codexStdio, thread, id string) (bool, error) {
	params := map[string]any{"threadId": thread}
	for {
		var page struct {
			Data []struct {
				ClientID string `json:"clientUserMessageId"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		if err := s.call(ctx, "thread/queue/list", params, &page); err != nil {
			return false, err
		}
		for _, message := range page.Data {
			if message.ClientID == id {
				return true, nil
			}
		}
		if page.Next == "" {
			return false, nil
		}
		params["cursor"] = page.Next
	}
}
