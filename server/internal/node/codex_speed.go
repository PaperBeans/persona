package node

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

// A missing serviceTier is unknown. Native null means Standard, and older
// Codex versions used "fast" for the tier now advertised as "priority".
func codexServiceTier(raw json.RawMessage) string {
	var tier string
	if len(raw) == 0 || json.Unmarshal(raw, &tier) != nil {
		return ""
	}
	switch tier {
	case "":
		return "default"
	case "fast":
		return "priority"
	default:
		return tier
	}
}

func (n *Node) codexSpeedRow(s *proto.Session) {
	c, thread, ok := n.codexEffortThread(s.SessionID)
	if !ok {
		return
	}
	c.mu.Lock()
	tiers := c.models[n.codexModelNow(s.SessionID, c, thread, s.Model)].tiers
	c.mu.Unlock()
	if thread.serviceTier != "" {
		s.ServiceTier = thread.serviceTier
	}
	s.ServiceTiers = codexSpeedChoices(tiers)
}

func codexSpeedChoices(tiers []proto.ServiceTier) []proto.ServiceTier {
	if len(tiers) == 0 || slices.ContainsFunc(tiers, func(t proto.ServiceTier) bool { return t.ID == "default" }) {
		return tiers
	}
	return append([]proto.ServiceTier{{ID: "default", Name: "Standard"}}, tiers...)
}

// Speed is a separate RPC so an older node cannot silently ignore it in modes.
// There is no optimistic or next-message fallback for a billable speed choice.
func (n *Node) setCodexSpeed(ctx context.Context, id, tier string) error {
	n.mu.Lock()
	proc := n.procs[id]
	n.mu.Unlock()
	if proc != nil && proc.agent == "codex" && proc.cx != nil {
		return proc.cx.setSpeed(ctx, tier)
	}
	c, thread, ok := n.codexEffortThread(id)
	if !ok {
		return n.setCodexTerminalSpeed(ctx, id, tier)
	}
	model := n.codexModelNow(id, c, thread, n.metaOf(id).model)
	c.mu.Lock()
	tiers := c.models[model].tiers
	c.mu.Unlock()
	if len(tiers) == 0 || tier != "default" && !slices.ContainsFunc(tiers, func(t proto.ServiceTier) bool { return t.ID == tier }) {
		return fmt.Errorf("this model does not advertise the requested Codex speed")
	}
	call, cancel := context.WithTimeout(ctx, codexEffortTimeout)
	defer cancel()
	if err := c.wire.call(call, "thread/settings/update", map[string]any{"threadId": thread.id, "serviceTier": tier}, nil); err != nil {
		return fmt.Errorf("couldn't change Codex speed: %w", err)
	}
	c.mu.Lock()
	if current, exists := c.threads[thread.id]; exists {
		// A native settings notification received during the RPC is newer.
		if current.serviceTierRevision == thread.serviceTierRevision {
			current.serviceTier = tier
			current.serviceTierRevision++
		}
		current.revision++
		c.threads[thread.id] = current
	}
	c.mu.Unlock()
	n.notifyCodex()
	return nil
}

// setCodexTerminalSpeed uses the same checked input and rollout acknowledgment as terminal model changes.
func (n *Node) setCodexTerminalSpeed(ctx context.Context, id, tier string) error {
	t, why, live := n.codexOnTerminalCtx(ctx, id, true)
	if !live {
		return fmt.Errorf("this session does not support Codex speed changes")
	}
	if why != nil {
		return why
	}
	n.forgetMeta(t.rollout)
	now := n.codexTerminalSettingsOf(id, t, n.metaOf(id))
	model, _ := codexModelEffortsIn(t.home, now.model)
	if !proto.ValidModelID(tier) || len(model.tiers) == 0 || tier != "default" && !slices.ContainsFunc(model.tiers, func(t proto.ServiceTier) bool { return t.ID == tier }) {
		return fmt.Errorf("this model does not advertise the requested Codex speed")
	}
	native := tier
	switch tier {
	case "default":
		native = "standard"
	case "priority":
		native = "fast"
	}
	_, err := n.applyCodexTerminalSettings(ctx, id, t, "/model --speed="+native, codexTerminalSettings{tier: tier})
	return err
}
