//go:build !persona_without_health && !persona_core_only

package all

import (
	"github.com/nwverse/persona/server/internal/healthhost"
	"github.com/nwverse/persona/server/internal/modules/health"
)

// The Health module: its layer, which the assistant reaches through internal/healthhost.
func init() {
	modules = append(modules, health.Module{})
	healthhost.Provide(health.Module{}.Info().ID, func(h healthhost.Host) healthhost.Layer { return health.New(h) })
}
