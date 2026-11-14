package devices

import (
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func (m *Machine) sessionsRevision() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionRevision
}

// The reconnect read can finish after a newer sessions.changed notification.
// Only install its snapshot if neither the connection nor the list advanced.
func (m *Machine) setInitialSessions(peer *rpc.Peer, revision uint64, list []proto.Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peer != peer || m.sessionRevision != revision {
		return false
	}
	for i := range list {
		list[i].MachineID = m.cfg.ID
	}
	m.sessions = list
	m.sessionsListed = true
	m.sessionRevision++
	m.lastSeen = time.Now().UTC()
	return true
}
