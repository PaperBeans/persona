// Package githubport is how the GitHub connector module reaches the extension manager (docs/architecture/core-and-modules.md
// "Provider seams"): the composition root (internal/modules/all) provides the module's factory, and extmgr makes the
// built-in GitHub connector with it when the manager runs. A build without the module (persona_without_github,
// persona_core_only) provides none, and neither does a manager whose `modules` setting switched it off
// (moduleswitch): the manager runs without the built-in GitHub connector (an extension's GitHub connector still
// works). It is its own package so that the composition root need not import extmgr.
package githubport

import (
	"github.com/nwverse/persona/server/internal/githubwire"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

var port = moduleswitch.NewPort[githubwire.Factory]("githubport")

// Provide sets the GitHub module's factory under its module id; the composition root calls it from init.
func Provide(module string, f githubwire.Factory) { port.Provide(module, f) }

// Factory is the GitHub module's factory, nil when this build has no GitHub module or s switched it off.
func Factory(s moduleswitch.Set) githubwire.Factory {
	f, _ := port.Get(s)
	return f
}
