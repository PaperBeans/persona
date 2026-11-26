package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

type routineCheck struct {
	proto.RoutineCheckParams
	OnError string `json:"on_error,omitempty"`
}

// routineCheckApprovalKey scopes one shared owner decision to the exact
// normalized command, not just to the conversation that requested it.
type routineCheckApprovalKey struct {
	machine string
	session string
	check   string
}

type routineCheckApproval struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	allowed bool
	err     error
}

type routineCheckApprovalBook struct {
	mu    sync.Mutex
	calls map[routineCheckApprovalKey]*routineCheckApproval
}

func canonicalRoutineCheck(check *routineCheck) ([]byte, error) {
	// Marshaling the complete struct includes every execution-affecting field.
	// A nil check stays "null" and can never compare equal to a non-nil one.
	return json.Marshal(check)
}

func sameRoutineCheck(a, b *routineCheck) bool {
	aKey, aErr := canonicalRoutineCheck(a)
	bKey, bErr := canonicalRoutineCheck(b)
	return aErr == nil && bErr == nil && bytes.Equal(aKey, bKey)
}

// wait shares an approval only while its owner request is still in flight. The
// shared context retains the first caller's values but not its cancellation;
// the last cancelled waiter withdraws the request for everyone.
func (b *routineCheckApprovalBook) wait(ctx context.Context, key routineCheckApprovalKey, ask func(context.Context) (bool, error)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	if b.calls == nil {
		b.calls = map[routineCheckApprovalKey]*routineCheckApproval{}
	}
	call := b.calls[key]
	if call == nil {
		sharedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settingCardWait)
		call = &routineCheckApproval{done: make(chan struct{}), cancel: cancel}
		b.calls[key] = call
		go b.run(key, call, sharedCtx, ask)
	}
	call.waiters++
	b.mu.Unlock()

	select {
	case <-call.done:
		return call.allowed, call.err
	case <-ctx.Done():
		b.leave(key, call)
		return false, ctx.Err()
	}
}

func (b *routineCheckApprovalBook) run(key routineCheckApprovalKey, call *routineCheckApproval, ctx context.Context, ask func(context.Context) (bool, error)) {
	defer call.cancel()
	allowed, err := ask(ctx)
	b.mu.Lock()
	if b.calls[key] == call {
		delete(b.calls, key)
	}
	call.allowed, call.err = allowed, err
	close(call.done)
	b.mu.Unlock()
}

func (b *routineCheckApprovalBook) leave(key routineCheckApprovalKey, call *routineCheckApproval) {
	var cancel context.CancelFunc
	b.mu.Lock()
	if b.calls[key] == call {
		call.waiters--
		if call.waiters == 0 {
			delete(b.calls, key)
			cancel = call.cancel
		}
	}
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func parseRoutineCheck(raw json.RawMessage) (*routineCheck, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	check := &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Timeout: proto.RoutineCheckDefaultTimeout}}
	if err := json.Unmarshal(raw, check); err != nil {
		return nil, err
	}
	if strings.TrimSpace(check.Command) == "" || len(check.Command) > proto.RoutineCheckMaxCommand ||
		strings.ContainsRune(check.Command, 0) {
		return nil, errors.New("run_if.command must be non-empty and at most 4096 bytes")
	}
	if check.Timeout < 1 || check.Timeout > proto.RoutineCheckMaxTimeout {
		return nil, fmt.Errorf("run_if.timeout_s must be between 1 and %d", proto.RoutineCheckMaxTimeout)
	}
	switch check.OnError {
	case "", "run", "skip":
		return check, nil
	default:
		return nil, errors.New("run_if.on_error must be run or skip")
	}
}

func (s *Service) approveRoutineCheck(ctx context.Context, machine, self string, check *routineCheck) (bool, error) {
	input, err := canonicalRoutineCheck(check)
	if err != nil {
		return false, err
	}
	key := routineCheckApprovalKey{machine: machine, session: self, check: string(input)}
	return s.routineChecks.wait(ctx, key, func(ctx context.Context) (bool, error) {
		now := time.Now().UTC()
		reply, err := s.ask(ctx, proto.Interaction{
			ID: "ix_" + randomHex(6), MachineID: machine, SessionID: self, Kind: proto.IxPermission,
			ToolName: "schedule_routine", ToolInput: input, SessionTitle: "Persona",
			Summary:   i18n.T(s.lang(ctx), "assistant-schedule.run_if_approval"),
			CreatedAt: now, ExpiresAt: now.Add(settingCardWait),
		})
		return reply.UserAllowed(), err
	})
}

// checkRoutine returns the log entry and whether the scheduled run should be skipped.
func (s *Service) checkRoutine(ctx context.Context, t Task) (string, bool) {
	machine := t.ExecutionMachineID
	if machine == "" {
		machine = t.RunMachineID
	}
	if machine == "" {
		machine = s.settings(ctx).MachineID
		if t.SessionID != "" {
			machine = t.MachineID
		}
	}
	l := s.lang(ctx)
	cctx, cancel := context.WithTimeout(ctx, time.Duration(t.RunIf.Timeout+3)*time.Second)
	defer cancel()
	result := proto.RoutineCheckResult{ExitCode: -1}
	call := s.call
	if s.devs != nil {
		call = s.nodeCall
	}
	err := call(cctx, machine, proto.MethodRoutineCheck, t.RunIf.RoutineCheckParams, &result)
	if err == nil {
		switch result.ExitCode {
		case 0:
			return "", false
		case 1:
			if result.Reason != "" {
				return result.Reason, true
			}
			return i18n.T(l, "assistant-schedule.run_if_skipped"), true
		default:
			err = fmt.Errorf("exit %d", result.ExitCode)
			if result.Reason != "" {
				err = fmt.Errorf("%w: %s", err, result.Reason)
			}
		}
	}
	return i18n.T(l, "assistant-schedule.run_if_failed", i18n.Args{"error": err.Error()}), t.RunIf.OnError == "skip"
}
