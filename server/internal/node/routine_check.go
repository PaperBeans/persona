package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
)

func routineCheck(ctx context.Context, raw json.RawMessage) (proto.RoutineCheckResult, error) {
	var p proto.RoutineCheckParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return proto.RoutineCheckResult{}, err
	}
	if strings.TrimSpace(p.Command) == "" || len(p.Command) > proto.RoutineCheckMaxCommand ||
		p.Timeout < 1 || p.Timeout > proto.RoutineCheckMaxTimeout {
		return proto.RoutineCheckResult{}, errors.New("invalid routine check command or timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
	defer cancel()

	cmd := loginShell(ctx, p.Command)
	cmd.Dir, _ = os.UserHomeDir()
	killGroupOnCancel(cmd)
	var output routineCheckLine
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return proto.RoutineCheckResult{}, err
	}
	// exec stops watching the deadline once the shell exits, even if a child still holds the output open.
	waited := make(chan struct{})
	var watch sync.WaitGroup
	stop := sync.OnceFunc(func() { _ = osproc.StopGroup(cmd.Process.Pid, true) })
	watch.Go(func() {
		select {
		case <-ctx.Done():
			stop()
		case <-waited:
		}
	})
	err := cmd.Wait() // killGroupOnCancel also sets a bounded WaitDelay.
	close(waited)
	watch.Wait()
	stop() // A child may have closed its output and kept running.
	if ctx.Err() != nil {
		return proto.RoutineCheckResult{}, ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) && !errors.As(err, &exit) {
		return proto.RoutineCheckResult{}, err
	}
	return proto.RoutineCheckResult{
		ExitCode: cmd.ProcessState.ExitCode(),
		Reason:   strings.TrimSpace(strings.ToValidUTF8(string(output.line), "")),
	}, nil
}

type routineCheckLine struct {
	line []byte
	done bool
}

func (w *routineCheckLine) Write(p []byte) (int, error) {
	n := len(p)
	if w.done {
		return n, nil
	}
	if i := bytes.IndexByte(p, '\n'); i >= 0 {
		p = p[:i]
		w.done = true
	}
	room := 512 - len(w.line)
	w.line = append(w.line, p[:min(room, len(p))]...)
	w.done = w.done || len(w.line) == 512
	return n, nil
}
