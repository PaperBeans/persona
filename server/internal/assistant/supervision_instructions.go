package assistant

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistant/supervisionrole"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// supervisionStartup marks a manager-authorized start before its conversation id exists.
// It is never read from RPC parameters or inferred from a model purpose supplied by a caller.
type supervisionStartup struct{}

// runtimeSessionSupportsSupervisionPolicy reports an explicit capability, never an inference from a launcher name.
func runtimeSessionSupportsSupervisionPolicy(c proto.Session) bool {
	return c.SourceKind == proto.SourceRuntime && c.Capabilities != nil && c.Capabilities.SupervisionPolicy
}

// runtimeSessionLacksSupervisionPolicy reports a session hosted by the runtime protocol whose runtime has not
// advertised supervision_policy support. Absent or unknown capability stays conservatively unsupported (#10338, #11173).
func runtimeSessionLacksSupervisionPolicy(c proto.Session) bool {
	return c.SourceKind == proto.SourceRuntime && !runtimeSessionSupportsSupervisionPolicy(c)
}

// runtimeWithSupervisionPolicy reports a known runtime session with an explicit separate policy channel.
func (s *Service) runtimeWithSupervisionPolicy(ctx context.Context, machine, id string) bool {
	c, ok := s.session(ctx, machine, id)
	return ok && runtimeSessionSupportsSupervisionPolicy(c)
}

// runtimeWithoutSupervisionPolicy reports a known session hosted by a runtime that lacks a separate policy channel.
// Launcher names and agent kinds alone do not identify that execution path.
func (s *Service) runtimeWithoutSupervisionPolicy(ctx context.Context, machine, id string) bool {
	if id == "" {
		return false
	}
	c, ok := s.session(ctx, machine, id)
	return ok && runtimeSessionLacksSupervisionPolicy(c)
}

func (s *Service) supervisionInstructions(ctx context.Context, machine, id string) (string, error) {
	cfg := s.settings(ctx)
	if id == "" || s.isWorkMachine(machine) {
		return "", nil
	}
	main := id == cfg.Main
	if main {
		if machine != cfg.mainRouteMachine() {
			return "", nil
		}
	} else if machine != cfg.MachineID {
		return "", nil
	}
	c, known := s.session(ctx, machine, id)
	if known && runtimeSessionLacksSupervisionPolicy(c) {
		return "", nil
	}
	background := (id == cfg.BackgroundSupervisor && cfg.BackgroundSupervisorMachineID == machine) || s.isBackgroundCallerOn(ctx, machine, id)
	leader := s.isLeader(ctx, id)
	if !main && !background && !leader {
		// Side chats genuinely supervise their own open tasks (#4669). Sharing the
		// directory, a scheduled purpose, or the ability to plan is not a role.
		if known && (!c.Assistant || c.Space != "") {
			return "", nil
		}
		if err := s.ensureTodosLoaded(ctx); err != nil {
			return "", err // unknown ownership must not silently demote a side supervisor
		}
		owns := false
		s.todos.mu.RLock()
		for _, todo := range s.todos.todos {
			if todo.DoneAt == nil && todo.Supervision != nil && todo.Supervision.Supervisor == id {
				owns = true
				break
			}
		}
		s.todos.mu.RUnlock()
		if !owns {
			return "", nil
		}
	}
	// Only a candidate supervisor needs this fail-closed check. An unreadable guest
	// registry must not stop ordinary chats that would receive no role policy anyway.
	if _, guest, err := s.guestOf(ctx, machine, id); err != nil {
		return "", err
	} else if guest {
		return "", nil
	}
	role := supervisionrole.RoleSide()
	switch {
	case main:
		role = supervisionrole.RoleMain()
	case background:
		role = supervisionrole.RoleBackground()
	case leader:
		role = supervisionrole.RoleLeader()
	}
	idle := leadIdle(c, known)
	if idle {
		if err := s.recordSupervisionRole(ctx, machine, id, role); err != nil {
			// Role persistence must not add a new owner-send failure mode. Without a durable baseline, retain the legacy
			// complete policy for this send and retry the compact selection on a later idle send.
			policy := prompts.RenderSupervisionInstructions()
			return s.leaderPolicy(ctx, id, policy, false, role)
		}
	}
	policyRole := role
	if known && !idle {
		if held, ok := s.workingSupervisionRole(ctx, machine, id); ok {
			policyRole = held
		} else {
			// A pre-role-filtering process has no baseline. Keep its complete legacy policy until an idle send instead of
			// presenting a new fingerprint that the node must refuse as busy.
			policy := prompts.RenderSupervisionInstructions()
			return s.leaderPolicy(ctx, id, policy, false, role)
		}
	}
	policy := supervisionPolicy(policyRole)
	// A leader's own direction notes and leader/side role changes follow its role policy (direction_notes.go): both wait
	// for an idle send and roll back together if that send is refused. Former leaders keep remaining tasks as side supervisors.
	return s.leaderPolicy(ctx, id, policy, idle, role)
}

func (s *Service) putSupervisionInstructions(ctx context.Context, machine, id string, p map[string]any) error {
	// Always overwrite caller input, including empty roles, so demotion clears the node's
	// process fingerprint instead of retaining an earlier conversation's policy.
	text, err := s.supervisionInstructions(ctx, machine, id)
	if err != nil {
		return err
	}
	if id == "" && p["guest"] == nil {
		if startup := ctx.Value(supervisionStartup{}); startup != nil {
			role, ok := startup.(supervisionrole.Role)
			if !ok {
				return fmt.Errorf("invalid supervision startup role %T", startup)
			}
			text = supervisionPolicy(role)
		}
	}
	p["supervision_instructions"] = text
	return nil
}
