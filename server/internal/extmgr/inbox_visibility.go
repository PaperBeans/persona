package extmgr

import (
	"encoding/json"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/extension"
)

// inboxVisibilityForConnectors gives a plugin only the boundary for a connector it contributes. Chat ids stay in
// manager.json and the initialize call; they are not copied into the extension registry's persisted settings.
func inboxVisibilityForConnectors(cfg *assistantwire.InboxVisibilityConfig, connectors []extension.ConnectorItem) json.RawMessage {
	for _, c := range connectors {
		if cfg.AppliesTo(c.ID) {
			raw, _ := json.Marshal(cfg)
			return raw
		}
	}
	return nil
}

// InboxVisibilityProvider reports positive provider-side enforcement for a source id, rather than inferring it from a
// tool name. Lark and Lark CLI have their own preflight wrappers. Other built-ins and older providers do not become
// capable merely because the manager can filter an already-returned body.
func (h *Manager) InboxVisibilityProvider(source string) bool {
	if source == "lark" || source == "larkcli" {
		return true
	}
	if h == nil || h.connectors == nil {
		return false
	}
	c := h.connectors.Get(source)
	return c != nil && c.Manifest().Has(connector.CapInboxVisibility)
}

// inboxVisibilityBlocksSource pauses a selected source with no provider-side preflight. Result filtering cannot undo a
// body read or a KB copy, so this decision is made before collection, archive enumeration and history calls.
func (h *Manager) inboxVisibilityBlocksSource(source string) bool {
	if h == nil || h.cfg == nil {
		return false
	}
	cfg := h.cfg.Inbox.Visibility
	return cfg != nil && (cfg.DirectChats || len(cfg.Chats) > 0) && cfg.AppliesTo(source) && !h.InboxVisibilityProvider(source)
}

func (h *Manager) inboxVisibilitySubscriptionAllowed(id, backing string) bool {
	if h == nil || h.cfg == nil {
		return true
	}
	cfg := h.cfg.Inbox.Visibility
	if cfg == nil || !cfg.DirectChats && len(cfg.Chats) == 0 || !cfg.AppliesTo(id) && !cfg.AppliesTo(backing) {
		return true
	}
	return h.InboxVisibilityProvider(backing)
}

// enforceIMessageVisibility also stops its background Watch loop, which otherwise constructs unread DM context before
// Collect is called. The user's stored Hand-to-Persona choice is not rewritten; the runtime source simply stays off.
func (h *Manager) enforceIMessageVisibility() {
	if h == nil || h.connectors == nil || !h.inboxVisibilityBlocksSource("imessage") {
		return
	}
	if c := h.imessage(); c != nil {
		c.SetAI(false)
	}
}

// validateInboxVisibilityCapabilities closes the rolling-upgrade gap before an older provider can start its source
// loops. JSON unknown-field tolerance would otherwise ignore the new boundary and collect bodies before the manager's
// tool fallback or stored-item quarantine could run. Empty controls preserve legacy provider initialization.
func validateInboxVisibilityCapabilities(cfg *assistantwire.InboxVisibilityConfig, connectors []extension.ConnectorItem) error {
	if cfg == nil || !cfg.DirectChats && len(cfg.Chats) == 0 {
		return nil
	}
	for _, c := range connectors {
		if !cfg.AppliesTo(c.ID) {
			continue
		}
		capable := false
		for _, capability := range c.Capabilities {
			capable = capable || capability == string(connector.CapInboxVisibility)
		}
		if !capable {
			return fmt.Errorf("connector %s does not declare %s; refusing source startup with private-chat visibility", c.ID, connector.CapInboxVisibility)
		}
	}
	return nil
}

// inboxVisibilityInitializeConfig also carries the boundary inside the extension's existing config object. The
// An extension's plugin kit can read it there without changing shared SDK build inputs; the top-level protocol field remains
// available to generated-SDK extensions. Neither representation is persisted in the extension registry.
func inboxVisibilityInitializeConfig(config, visibility json.RawMessage) (json.RawMessage, error) {
	if len(visibility) == 0 {
		return config, nil
	}
	values := map[string]json.RawMessage{}
	if len(config) != 0 {
		if err := json.Unmarshal(config, &values); err != nil {
			return nil, fmt.Errorf("extension config for inbox visibility: %w", err)
		}
		if values == nil {
			values = map[string]json.RawMessage{}
		}
	}
	values["inbox_visibility"] = visibility
	return json.Marshal(values)
}
