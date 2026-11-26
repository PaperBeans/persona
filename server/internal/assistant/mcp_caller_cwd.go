package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/proto"
)

// Only nodeRequest installs this evidence, after the devices connection has
// identified the machine. Hub and extension payloads cannot create this context.
// Keep it bound to the caller so nested tool calls cannot reuse it for a target.
type nodeCallerCwd struct{ machine, session, cwd string }
type nodeCallerCwdKey struct{}

func withNodeCallerCwd(ctx context.Context, machine, session, cwd string) context.Context {
	return context.WithValue(ctx, nodeCallerCwdKey{}, nodeCallerCwd{machine, session, cwd})
}

func (s *Service) callerCwd(ctx context.Context, machine, session string) string {
	p, ok := ctx.Value(nodeCallerCwdKey{}).(nodeCallerCwd)
	if !ok || machine == "" || session == "" || p.session != session || !s.sameMachine(machine, p.machine) {
		return ""
	}
	return p.cwd
}

// Discovery uses one session snapshot for both its list and cursor. A session
// arriving between those reads must not mark the earlier list as current.
type callerSpaceSnapshotKey struct{}
type callerSpaceSnapshot struct {
	machine, session string
	row              proto.Session
	known            bool
}

func (s *Service) callerSpaceSession(ctx context.Context, machine, session string) (proto.Session, bool) {
	if p, ok := ctx.Value(callerSpaceSnapshotKey{}).(callerSpaceSnapshot); ok && p.session == session && s.sameMachine(machine, p.machine) {
		return p.row, p.known
	}
	return s.session(ctx, machine, session)
}
