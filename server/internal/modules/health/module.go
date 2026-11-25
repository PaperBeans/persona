// Package health is the Health module (docs/architecture/core-and-modules.md "Health", #14406): the manager's Health
// intake (docs/assistant/health.md): water and protein entries with their undo markers, the water reminders the owner
// switches on, the Health intake page, the legacy water extension's import and the health_intake tool.
//
// The core reaches it through internal/healthhost, filled by internal/modules/all; it reaches the core through its
// Host (host.go). The Health space (its conversations' directory and policies) stays in the core.
package health

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/modapi"
)

//go:embed module.json
var manifest []byte

// Module is the Health module.
type Module struct{}

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func (Module) Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("health: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}

// Register adds nothing: the kernel announces the module's feature flag (module.health, its manifest's), and its
// routes, tool and reminder loop reach the core through internal/healthhost, made per manager.
func (Module) Register(modapi.Host) error { return nil }
