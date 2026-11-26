// Package mailport is how the mail module reaches the extension manager (docs/architecture/core-and-modules.md "Provider seams"):
// the composition root (internal/modules/all) provides the module's factory, and extmgr opens mail with it when the
// manager runs. A build without the module (persona_without_mail, persona_core_only) provides none, and the manager
// runs without mail; so does a manager whose `modules` setting switched it off (moduleswitch). It is its own package so that the composition root need not import extmgr.
package mailport

import (
	"github.com/nwverse/persona/server/internal/mailwire"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

var port = moduleswitch.NewPort[mailwire.Factory]("mailport")

// Provide sets the mail module's factory under its module id; the composition root calls it from init.
func Provide(module string, f mailwire.Factory) { port.Provide(module, f) }

// Factory is the mail module's factory, nil when this build has no mail module or s switched it off.
func Factory(s moduleswitch.Set) mailwire.Factory {
	f, _ := port.Get(s)
	return f
}
