// Package knowledgeui is the knowledge pages' module (docs/architecture/core-and-modules.md "Knowledge UI", #14407):
// the pipelines of the knowledge pages that are not the core's memory. The import of the connectors' older chat
// history (kbhistory, the kb_history_* tools and its driver, docs/assistant/knowledge.md) and the marks of the lines
// a knowledge base day shows that the owner sent from Persona (kbsent). The memory itself, its files and search and
// the kb_* tools that read and write it, stays in the core.
//
// The core reaches it through internal/knowledgeuihost, filled by internal/modules/all; it reaches the core through
// its Host (layer.go) and the knowledge base (internal/kb, which it imports into: a declared core-service dependency,
// archguard's testdata/module_services.txt).
package knowledgeui

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/modapi"
)

//go:embed module.json
var manifest []byte

// Module is the knowledge pages' module.
type Module struct{}

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func (Module) Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("knowledge-ui: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}

// Register adds nothing: the kernel announces the module's feature flag (module.knowledge-ui, its manifest's), and
// its tools, its loop's rounds and the selection page's chat lists reach the core through internal/knowledgeuihost,
// made per manager with the knowledge base's own schedule and route guard.
func (Module) Register(modapi.Host) error { return nil }
