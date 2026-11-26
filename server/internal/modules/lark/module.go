// Package lark is the Feishu / Lark module (docs/architecture/core-and-modules.md, #13404): the bot's client
// (feishubot), which the extension host drives through internal/botport, and the assistant's layer (Layer), which the
// assistant reaches through internal/larkhost. internal/modules/all provides both; the module serves the companion
// bots' four routes through the kernel (Register).
package lark

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/nwverse/persona/server/internal/modapi"
)

//go:embed module.json
var manifest []byte

// Module is the Feishu / Lark module. Its layer lookup, set by the composition root (internal/modules/all) with
// NewModule, finds the assistant's layer of the manager that is serving a request in its context; without it the routes
// answer 404.
type Module struct {
	layer func(ctx context.Context) *Layer
}

// NewModule is the module with layer, which finds the layer of the manager a request is for (nil: none).
func NewModule(layer func(ctx context.Context) *Layer) Module { return Module{layer: layer} }

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func (Module) Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("lark: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}

// Register serves the four routes the companion bots use (companions.go); the rest of the module is reached through
// internal/botport and internal/larkhost.
func (m Module) Register(h modapi.Host) error {
	for _, rt := range []struct {
		pattern string
		serve   func(core, http.ResponseWriter, *http.Request)
	}{
		{routeCompanions, core.putCompanions},
		{routeNotices, core.getNotices},
		{routeNoticesAction, core.postNoticesAction},
		{routeWorkbenchPost, core.postWorkbench},
	} {
		serve := rt.serve
		if err := h.AddHubRoute(rt.pattern, func(w http.ResponseWriter, r *http.Request) {
			if m.layer == nil {
				http.NotFound(w, r)
				return
			}
			l := m.layer(r.Context())
			if l == nil {
				http.NotFound(w, r)
				return
			}
			serve(l.chat(), w, r)
		}); err != nil {
			return err
		}
	}
	return nil
}
