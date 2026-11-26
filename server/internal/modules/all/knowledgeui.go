//go:build !persona_without_knowledge_ui && !persona_core_only

package all

import (
	"github.com/nwverse/persona/server/internal/knowledgeuihost"
	knowledgeui "github.com/nwverse/persona/server/internal/modules/knowledge-ui"
)

// The knowledge pages' module: its layer, which the assistant reaches through internal/knowledgeuihost.
func init() {
	modules = append(modules, knowledgeui.Module{})
	knowledgeuihost.Provide(knowledgeui.Module{}.Info().ID, func(h knowledgeuihost.Host) knowledgeuihost.Layer { return knowledgeui.New(h) })
}
