package hub

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"
)

func parseGradingTestConfig(t *testing.T, raw string, extension bool) pushGradingConfig {
	t.Helper()
	cfg, err := parsePushGradingConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if err := cfg.validate(extension); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	return cfg
}

func TestPushGradingConfigDefaultsAndEnabledPrecedence(t *testing.T) {
	got := resolvePushGrading(nil, nil, "")
	if got.Enabled || got.Actions.P0 != "push" || got.Actions.P1 != "hold" || got.Actions.P2 != "silence" ||
		got.DedupWindow != 12*time.Hour || got.NotifyUser.Main != "P0" || got.NotifyUser.Others != "P1" ||
		got.NotifyUser.OthersMax != "" || !got.MergeHeld || !got.Cards.SupervisorHold ||
		!got.Cards.HoldConclusions || !got.Cards.HoldAcceptance || got.Cards.DecisionLevel != "P0" || got.Cards.ReportLevel != "P1" ||
		!slices.Equal(got.DedupExemptLevels, []string{"P0"}) ||
		!slices.Equal(got.Cards.EscalateReasons, []string{"irreversible", "visible_to_others", "only_owner_knows"}) {
		t.Fatalf("default config changed: %+v", got)
	}
	for _, field := range []string{"enabled", "actions.P0", "actions.P1", "actions.P2", "dedup_window", "notify_user.main", "notify_user.others", "notify_user.others_max", "merge_held", "dedup_exempt_levels", "cards.supervisor_hold", "cards.escalate_reasons", "cards.hold_conclusions", "cards.hold_acceptance", "cards.decision_level", "cards.report_level"} {
		if got.By[field] != "default" {
			t.Errorf("%s source = %q, want default", field, got.By[field])
		}
	}

	layer := parseGradingTestConfig(t, `{"version":1,"actions":{"P1":"hold"}}`, true)
	baseFalse := parseGradingTestConfig(t, `{"version":1,"enabled":false}`, false)
	if got := resolvePushGrading(&baseFalse, &layer, "extension:one"); got.Enabled || got.By["enabled"] != "base" {
		t.Fatalf("owner false did not win: %+v", got)
	}
	baseTrue := parseGradingTestConfig(t, `{"version":1,"enabled":true}`, false)
	if got := resolvePushGrading(&baseTrue, nil, ""); !got.Enabled || got.By["enabled"] != "base" {
		t.Fatalf("owner true did not win: %+v", got)
	}
	baseAuto := parseGradingTestConfig(t, `{"version":1}`, false)
	if got := resolvePushGrading(&baseAuto, &layer, "extension:one"); !got.Enabled || got.By["enabled"] != "extension:one" {
		t.Fatalf("layer did not enable auto mode: %+v", got)
	}
	baseNull := parseGradingTestConfig(t, `{"version":1,"enabled":null}`, false)
	if got := resolvePushGrading(&baseNull, nil, ""); got.Enabled || got.By["enabled"] != "default" {
		t.Fatalf("null enabled was not the automatic default: %+v", got)
	}
}

func TestPushGradingConfigShallowMergeNullAndSources(t *testing.T) {
	base := parseGradingTestConfig(t, `{
		"version":1,
		"actions":{"P0":"hold","P1":"push","P2":"hold"},
		"dedup_window":"6h",
		"notify_user":{"main":"P1","others":"P2","others_max":"P1"},
		"merge_held":false,
		"dedup_exempt_levels":["P0","P1"],
		"cards":{"supervisor_hold":false,"escalate_reasons":["irreversible"],"hold_conclusions":false,"hold_acceptance":false,"decision_level":"P1","report_level":"P2"}
	}`, false)
	layer := parseGradingTestConfig(t, `{
		"version":1,
		"actions":{"P1":null,"P2":"push"},
		"dedup_window":null,
		"notify_user":{"others":null},
		"merge_held":null,
		"dedup_exempt_levels":["P0","P2"],
		"cards":{"escalate_reasons":null,"hold_acceptance":null,"decision_level":null}
	}`, true)
	got := resolvePushGrading(&base, &layer, "extension:one")
	if got.Actions.P0 != "hold" || got.By["actions.P0"] != "base" {
		t.Errorf("nested base value was replaced: %+v", got.Actions)
	}
	if got.Actions.P1 != "hold" || got.By["actions.P1"] != "extension:one" {
		t.Errorf("nested null did not restore the default: %+v", got.Actions)
	}
	if got.Actions.P2 != "push" || got.By["actions.P2"] != "extension:one" {
		t.Errorf("extension action missing: %+v", got.Actions)
	}
	if got.DedupWindow != 12*time.Hour || got.By["dedup_window"] != "extension:one" {
		t.Errorf("duration null did not restore 12h: %+v", got.DedupWindow)
	}
	if got.NotifyUser.Main != "P1" || got.NotifyUser.Others != "P1" || got.NotifyUser.OthersMax != "P1" {
		t.Errorf("notify_user merge is wrong: %+v", got.NotifyUser)
	}
	if got.By["notify_user.main"] != "base" || got.By["notify_user.others"] != "extension:one" || got.By["notify_user.others_max"] != "base" {
		t.Errorf("notify_user sources are wrong: %+v", got.By)
	}
	if !got.MergeHeld || got.By["merge_held"] != "extension:one" {
		t.Errorf("merge_held null did not restore the default: %+v", got.MergeHeld)
	}
	if !slices.Equal(got.DedupExemptLevels, []string{"P0", "P2"}) || got.By["dedup_exempt_levels"] != "extension:one" {
		t.Errorf("array was merged instead of replaced: %v", got.DedupExemptLevels)
	}
	if got.Cards.SupervisorHold || got.By["cards.supervisor_hold"] != "base" {
		t.Errorf("nested card base value was lost: %+v", got.Cards)
	}
	if !slices.Equal(got.Cards.EscalateReasons, []string{"irreversible", "visible_to_others", "only_owner_knows"}) || got.By["cards.escalate_reasons"] != "extension:one" {
		t.Errorf("card array null did not restore the default: %v", got.Cards.EscalateReasons)
	}
	if got.Cards.HoldConclusions || got.By["cards.hold_conclusions"] != "base" {
		t.Errorf("conclusion rule did not keep its base value: %+v", got.Cards)
	}
	if !got.Cards.HoldAcceptance || got.By["cards.hold_acceptance"] != "extension:one" {
		t.Errorf("acceptance null did not restore the default: %+v", got.Cards)
	}
	if got.Cards.DecisionLevel != "P0" || got.By["cards.decision_level"] != "extension:one" || got.Cards.ReportLevel != "P2" || got.By["cards.report_level"] != "base" {
		t.Errorf("report levels or sources are wrong: %+v", got.Cards)
	}
}

func TestPushGradingConfigWholeObjectNullRestoresDefaults(t *testing.T) {
	base := parseGradingTestConfig(t, `{"version":1,"actions":{"P0":"hold"},"notify_user":{"main":"P2"},"cards":{"supervisor_hold":false,"hold_conclusions":false,"report_level":"P2"}}`, false)
	layer := parseGradingTestConfig(t, `{"version":1,"actions":null,"notify_user":null,"cards":null}`, true)
	got := resolvePushGrading(&base, &layer, "extension:one")
	if got.Actions.P0 != "push" || got.NotifyUser.Main != "P0" || !got.Cards.SupervisorHold || !got.Cards.HoldConclusions || got.Cards.ReportLevel != "P1" {
		t.Fatalf("whole-object null did not restore defaults: %+v", got)
	}
	for _, field := range []string{"actions.P0", "notify_user.main", "cards.supervisor_hold"} {
		if got.By[field] != "extension:one" {
			t.Errorf("%s source = %q", field, got.By[field])
		}
	}
}

func TestPushGradingConfigRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		extension bool
	}{
		{"missing version", `{}`, false},
		{"unsupported version", `{"version":2}`, false},
		{"unknown field", `{"version":1,"unknown":true}`, false},
		{"duplicate field", `{"version":1,"version":1}`, false},
		{"unknown nested field", `{"version":1,"actions":{"P3":"push"}}`, false},
		{"extension enabled", `{"version":1,"enabled":true}`, true},
		{"extension null enabled", `{"version":1,"enabled":null}`, true},
		{"extension holds P0", `{"version":1,"actions":{"P0":"hold"}}`, true},
		{"extension silences P0", `{"version":1,"actions":{"P0":"silence"}}`, true},
		{"owner may hold P0 is valid only in base", `{"version":1,"actions":{"P0":"unknown"}}`, false},
		{"negative duration", `{"version":1,"dedup_window":"-1s"}`, false},
		{"too long duration", `{"version":1,"dedup_window":"48h1s"}`, false},
		{"invalid duration", `{"version":1,"dedup_window":"12"}`, false},
		{"invalid level", `{"version":1,"notify_user":{"main":"P3"}}`, false},
		{"invalid cap", `{"version":1,"notify_user":{"others_max":"urgent"}}`, false},
		{"duplicate exemption", `{"version":1,"dedup_exempt_levels":["P0","P0"]}`, false},
		{"invalid escalation reason", `{"version":1,"cards":{"escalate_reasons":["other"]}}`, false},
		{"invalid report level", `{"version":1,"cards":{"report_level":"urgent"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parsePushGradingConfig([]byte(tc.raw))
			if err == nil {
				err = cfg.validate(tc.extension)
			}
			if err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestPushGradingConfigAlwaysExemptsP0FromDedup(t *testing.T) {
	layer := parseGradingTestConfig(t, `{"version":1,"dedup_exempt_levels":["P1"]}`, true)
	got := resolvePushGrading(nil, &layer, "extension:one")
	if !slices.Equal(got.DedupExemptLevels, []string{"P0", "P1"}) {
		t.Fatalf("P0 floor was not unioned into the configured levels: %v", got.DedupExemptLevels)
	}
	if got.By["dedup_exempt_levels.P0"] != "floor:F2" {
		t.Fatalf("P0 exemption source = %q", got.By["dedup_exempt_levels.P0"])
	}
}

func TestPushGradingConfigRoundTripPreservesNull(t *testing.T) {
	original := parseGradingTestConfig(t, `{"version":1,"actions":{"P1":null},"dedup_window":null}`, true)
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped := parseGradingTestConfig(t, string(raw), true)
	if !reflect.DeepEqual(original, roundTripped) {
		t.Fatalf("null presence changed after round trip:\n%s", raw)
	}
}
