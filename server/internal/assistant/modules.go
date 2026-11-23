package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/kernel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modapi"
)

// The built-in feature modules (docs/architecture/core-and-modules.md): the assistant reaches them only through its
// kernel (internal/assistant/kernel), which loads those compiled in (internal/modules/all) and switched on.

// modulesKey is the settings row that switches built-in modules off: {"disabled": ["<id>", …]}. A module switched off
// is not loaded at the manager's next start, as if it were not compiled in; its settings stay stored, hidden.
const modulesKey = "modules"

// ModuleSwitch is the value of the modules settings row.
type ModuleSwitch struct {
	Disabled []string `json:"disabled,omitempty"`
}

// ReadModuleSwitch reads the modules settings row: the ids of the built-in modules switched off. The manager reads it
// once, when it starts (main.go), and hands it to New, so the switch takes effect at the next start; New itself reads
// no database. A row that cannot be read switches nothing off.
func ReadModuleSwitch(ctx context.Context, st Store, logf func(format string, args ...any)) []string {
	if st == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var sw ModuleSwitch
	if _, err := st.Setting(ctx, modulesKey, &sw); err != nil {
		if logf != nil {
			logf("modules: reading the %s setting: %v; loading every built-in module", modulesKey, err)
		}
		return nil
	}
	return sw.Disabled
}

// loadModules loads mods (nil: the built-in ones) into the kernel, leaving out those switched off.
func (s *Service) loadModules(mods []modapi.Module, off []string) {
	if mods == nil {
		mods = kernel.Builtin()
	}
	s.modules = kernel.New(mods, func(id string) bool { return slices.Contains(off, id) }, s.logf)
}

// moduleValue is a module setting's value as the assistant's settings keep it: the JSON as stored, nil when never set.
// A stored null reads as never set, so it is left out when the settings are written again, as the typed pointer it
// replaces was.
type moduleValue json.RawMessage

func (v moduleValue) MarshalJSON() ([]byte, error) {
	if len(v) == 0 {
		return []byte("null"), nil
	}
	return json.RawMessage(v).MarshalJSON()
}

func (v *moduleValue) UnmarshalJSON(data []byte) error {
	if string(bytes.TrimSpace(data)) == "null" {
		*v = nil
		return nil
	}
	*v = append(moduleValue(nil), data...) // a copy: never the backing array of a value another copy holds
	return nil
}

// moduleStored is what is stored for a module setting key (nil: never set). Each module key the assistant keeps in
// its own settings has its field here, so a switched-off module's value stays as it was.
func moduleStored(v Settings, key string) json.RawMessage {
	switch key {
	case "assistant.briefing_waits":
		return json.RawMessage(v.BriefingWaits)
	}
	return nil
}

func moduleStore(v *Settings, key string, raw json.RawMessage) error {
	if string(bytes.TrimSpace(raw)) == "null" {
		raw = nil
	}
	switch key {
	case "assistant.briefing_waits":
		v.BriefingWaits = moduleValue(raw)
		return nil
	}
	return errors.New(key + " has no place in the assistant's settings")
}

// moduleSettingGet is persona_settings' get of a module setting: the value in force.
func (s *Service) moduleSettingGet(key string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		m, ok := s.modules.Setting(key)
		if !ok {
			return "", errors.New(key + " is not available: its module is off")
		}
		return string(m.Value(moduleStored(s.settings(ctx), key))), nil
	}
}

// moduleSettingSet is an ordinary persona_settings change of a module setting: the tool only prepares a before/after
// card (cardKey, in the settings tool's namespace, with before and after), and the existing approval path calls apply.
// null restores the module's defaults.
func (s *Service) moduleSettingSet(key, cardKey, applied string) func(ctx context.Context, raw json.RawMessage) (*ownChange, error) {
	return func(ctx context.Context, raw json.RawMessage) (*ownChange, error) {
		m, ok := s.modules.Setting(key)
		if !ok {
			return nil, errors.New(key + " is not available: its module is off")
		}
		next, err := m.Check(raw)
		if err != nil {
			return nil, err
		}
		before, after := m.Value(moduleStored(s.settings(ctx), key)), m.Value(next)
		if bytes.Equal(before, after) {
			return &ownChange{summary: key + " is unchanged"}, nil
		}
		if s.store() == nil {
			return nil, errors.New("no database on this hub")
		}
		return &ownChange{cardText: func(l i18n.Lang) string {
			return i18n.T(l, "assistant-settings_tool."+cardKey, i18n.Args{"before": m.Describe(before), "after": m.Describe(after)})
		}, apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { return moduleStore(v, key, next) }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return applied, nil
		}}, nil
	}
}

// briefingWaits are the wait rules of the routine facts' module for the settings v; false when no module gives the
// routine facts (the briefing is off or not compiled in).
func (s *Service) briefingWaits(v Settings) (modapi.WaitRules, bool) {
	f, ok := s.modules.ScheduleFacts()
	if !ok {
		return nil, false
	}
	return f.Waits(moduleStored(v, f.Setting)), true
}

// briefingOn says whether a module gives the routine facts: without one a routine with facts on opens with none, and
// a daily report with the north star's lines alone.
func (s *Service) briefingOn() bool {
	_, ok := s.modules.ScheduleFacts()
	return ok
}
