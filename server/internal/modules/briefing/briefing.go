// Package briefing is the daily briefing module (docs/architecture/core-and-modules.md, #13408, #13845): the facts a
// scheduled routine with facts on opens with, its evening all-day recording summary, its chat feed item, its
// model-failure fallback, and its wait-row setting. It reaches the core only through modapi.
//
// What stays in internal/assistant for now: the Lark card renderer (briefing_card.go, pending a neutral Card model) and
// the HTTP routes (briefRoutes in briefing_feed.go, pending hub_routes slot support).
package briefing

import (
	_ "embed"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/modapi"
)

//go:embed module.json
var manifest []byte

// Module is the briefing module.
type Module struct{}

// Info is the embedded manifest (module.json, checked against schema/module.schema.json).
func (Module) Info() modapi.Info {
	var info modapi.Info
	if err := json.Unmarshal(manifest, &info); err != nil {
		panic("briefing: module.json: " + err.Error()) // embedded and tested: never at run time
	}
	return info
}

// Register adds the wait setting and the routine facts' wait rules and runtime runner.
func (Module) Register(h modapi.Host) error {
	if err := h.AddSetting(modapi.Setting{Key: SettingKey, Value: value, Check: check, Describe: describe}); err != nil {
		return err
	}
	return h.AddScheduleFacts(modapi.ScheduleFacts{
		Setting: SettingKey,
		Waits: func(stored json.RawMessage) modapi.WaitRules {
			return waitsOf(stored)
		},
		Runner: newRunner(h),
	})
}
