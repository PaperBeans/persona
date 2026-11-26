//go:build persona_core_only || persona_without_briefing

package assistant

import "testing"

// The approval and undo tests require the module. A build without it must reject the same
// proposed settings locally, without changing raw null/default data or creating history.
func TestUnavailableBriefingPreservesReversibleSettings(t *testing.T) {
	const priorCustom = `{"stalled_minutes":21,"silent_minutes":121,"orphan_minutes":11,"card_minutes":4,"max_rows":14}`
	setting := reversibleSettingCases[2]
	for _, test := range []struct {
		name   string
		auto   bool
		before string
		after  string
	}{
		{"manual unset to nondefault", false, "null", setting.value},
		{"manual explicit default to unset", false, reversibleBriefingDefaultValue, "null"},
		{"unset to nondefault", true, "null", setting.value},
		{"nondefault to unset", true, setting.value, "null"},
		{"explicit default to unset", true, reversibleBriefingDefaultValue, "null"},
		{"unset to explicit default", true, "null", reversibleBriefingDefaultValue},
		{"explicit default to nondefault", true, reversibleBriefingDefaultValue, setting.value},
		{"nondefault to explicit default", true, setting.value, reversibleBriefingDefaultValue},
		{"older custom value to nondefault", true, priorCustom, setting.value},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, st := newReversibleSettingService(t, test.auto)
			if s.modules.Loaded("briefing") {
				t.Fatal("this build unexpectedly loaded the briefing module")
			}
			putReversibleRaw(t, st, setting, test.before)
			requireUnavailableBriefingAction(t, s, "set", test.after)
			requireReversibleRaw(t, st, setting, test.before)
			if len(s.pendingQuestions()) != 0 || len(reversibleRecords(st)) != 0 {
				t.Fatal("an unavailable briefing setting left a card or an audit record")
			}
		})
	}
}
