package hub

import (
	"maps"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

type liveToolsPush struct {
	Type      string `json:"type"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	proto.LiveToolState
}

// Keep and publish under the same hub lock as a new subscriber's replay, so a
// concurrent completion cannot be followed by an older list of pending tools.
func (m *Machine) setLiveTools(id string, state proto.LiveToolState) {
	h := m.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	m.mu.Lock()
	if _, deleted := m.deletedConversations[id]; deleted || m.watchers[id] == 0 || !m.online {
		m.mu.Unlock()
		return
	}
	if len(state.Tools) == 0 && state.Summary == "" && !state.Active && !state.Thinking && state.RetryAttempt == 0 {
		delete(m.liveTools, id)
	} else {
		if m.liveTools == nil {
			m.liveTools = make(map[string]proto.LiveToolState)
		}
		m.liveTools[id] = state
	}
	m.mu.Unlock()
	h.toSubscribersLocked(m.cfg.ID, id, proto.FeatureSessionLiveTools, liveToolsPush{"live_tools", m.cfg.ID, id, state})
}

func (m *Machine) replayLiveTools(c *client, id string) {
	if !c.wants(proto.FeatureSessionLiveTools) {
		return
	}
	h := m.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	c.mu.Lock()
	sub := c.subs[[2]string{m.cfg.ID, id}]
	c.mu.Unlock()
	if !sub {
		return
	}
	m.mu.Lock()
	state, ok := m.liveTools[id]
	m.mu.Unlock()
	if ok {
		c.sendJSON(liveToolsPush{"live_tools", m.cfg.ID, id, state})
	}
}

func (h *Hub) replaySubscribedLiveTools(c *client) {
	c.mu.Lock()
	keys := slices.Collect(maps.Keys(c.subs))
	c.mu.Unlock()
	for _, key := range keys {
		if m := h.localMachine(key[0]); m != nil {
			m.replayLiveTools(c, key[1])
		}
	}
}

func (m *Machine) clearLiveTools() {
	h := m.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	m.mu.Lock()
	cached := m.liveTools
	m.liveTools = nil
	m.mu.Unlock()
	for id := range cached {
		h.toSubscribersLocked(m.cfg.ID, id, proto.FeatureSessionLiveTools, liveToolsPush{"live_tools", m.cfg.ID, id, proto.LiveToolState{Tools: []proto.LiveTool{}}})
	}
}
