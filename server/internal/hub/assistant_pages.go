package hub

import "github.com/nwverse/persona/server/internal/assistantwire"

// featureAssistantPages: the default manager keeps the assistant's pages (Dashboards, #15017,
// docs/assistant/dashboards.md): the reserved extension -pages among the extensions' places and pages, the dashboards
// switch of GET/PUT /api/v1/assistant and the assistant_pages event. Its routes are the extensions' and the
// assistant's, which go to the default manager, so its hello alone decides, as for assistant_search; an extension's
// report cannot add the feature (features).
const featureAssistantPages = assistantwire.CapabilityAssistantPages

// assistantPagesFeatures is the feature while the default manager is connected and named the capability in its hello
// (the switch on or off); nothing otherwise, so the apps show no Dashboards switch to an older manager.
func (h *Hub) assistantPagesFeatures() []string {
	if !h.defaultManagerNames(assistantwire.CapabilityAssistantPages) {
		return nil
	}
	return []string{featureAssistantPages}
}
