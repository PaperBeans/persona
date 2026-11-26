package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"
)

// pushGradingOption preserves the difference between an omitted field, an explicit
// null (restore the built-in default), and a concrete value. That distinction is
// needed when an extension layer is merged over the owner's base configuration.
type pushGradingOption[T any] struct {
	set   bool
	null  bool
	value T
}

type pushGradingConfig struct {
	Version           int
	Enabled           pushGradingOption[bool]
	Actions           pushGradingOption[pushGradingActionsConfig]
	DedupWindow       pushGradingOption[time.Duration]
	NotifyUser        pushGradingOption[pushGradingNotifyUserConfig]
	MergeHeld         pushGradingOption[bool]
	DedupExemptLevels pushGradingOption[[]string]
	Cards             pushGradingOption[pushGradingCardsConfig]
}

type pushGradingActionsConfig struct {
	P0 pushGradingOption[string]
	P1 pushGradingOption[string]
	P2 pushGradingOption[string]
}

type pushGradingNotifyUserConfig struct {
	Main      pushGradingOption[string]
	Others    pushGradingOption[string]
	OthersMax pushGradingOption[string]
}

type pushGradingCardsConfig struct {
	SupervisorHold  pushGradingOption[bool]
	EscalateReasons pushGradingOption[[]string]
	HoldConclusions pushGradingOption[bool]
	HoldAcceptance  pushGradingOption[bool]
	DecisionLevel   pushGradingOption[string]
	ReportLevel     pushGradingOption[string]
}

type pushGradingActions struct {
	P0 string
	P1 string
	P2 string
}

type pushGradingNotifyUser struct {
	Main      string
	Others    string
	OthersMax string
}

type pushGradingCards struct {
	SupervisorHold  bool
	EscalateReasons []string
	HoldConclusions bool
	HoldAcceptance  bool
	DecisionLevel   string
	ReportLevel     string
}

type pushGradingEffective struct {
	Enabled           bool
	Actions           pushGradingActions
	DedupWindow       time.Duration
	NotifyUser        pushGradingNotifyUser
	MergeHeld         bool
	DedupExemptLevels []string
	Cards             pushGradingCards
	By                map[string]string
}

func defaultPushGradingEffective() pushGradingEffective {
	return pushGradingEffective{
		Actions:           pushGradingActions{P0: "push", P1: "hold", P2: "silence"},
		DedupWindow:       12 * time.Hour,
		NotifyUser:        pushGradingNotifyUser{Main: "P0", Others: "P1"},
		MergeHeld:         true,
		DedupExemptLevels: []string{"P0"},
		Cards: pushGradingCards{SupervisorHold: true,
			EscalateReasons: []string{"irreversible", "visible_to_others", "only_owner_knows"},
			HoldConclusions: true, HoldAcceptance: true, DecisionLevel: "P0", ReportLevel: "P1"},
		By: map[string]string{
			"enabled": "default", "actions.P0": "default", "actions.P1": "default", "actions.P2": "default",
			"dedup_window": "default", "notify_user.main": "default", "notify_user.others": "default",
			"notify_user.others_max": "default", "merge_held": "default", "dedup_exempt_levels": "default",
			"dedup_exempt_levels.P0": "floor:F2",
			"cards.supervisor_hold":  "default", "cards.escalate_reasons": "default", "cards.hold_conclusions": "default",
			"cards.hold_acceptance": "default", "cards.decision_level": "default", "cards.report_level": "default",
		},
	}
}

func parsePushGradingConfig(raw []byte) (pushGradingConfig, error) {
	var cfg pushGradingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return pushGradingConfig{}, err
	}
	return cfg, nil
}

func (c *pushGradingConfig) UnmarshalJSON(raw []byte) error {
	*c = pushGradingConfig{}
	obj, err := decodePushGradingObject(raw, "version", "enabled", "actions", "dedup_window", "notify_user", "merge_held", "dedup_exempt_levels", "cards")
	if err != nil {
		return err
	}
	version, ok := obj["version"]
	if !ok || pushGradingNull(version) {
		return fmt.Errorf("push grading: version 1 is required")
	}
	if err := json.Unmarshal(version, &c.Version); err != nil || c.Version != 1 {
		return fmt.Errorf("push grading: version must be 1")
	}
	if c.Enabled, err = gradingOption(obj, "enabled", gradingValue[bool]); err != nil {
		return err
	}
	if c.Actions, err = gradingOption(obj, "actions", gradingValue[pushGradingActionsConfig]); err != nil {
		return err
	}
	if c.DedupWindow, err = gradingOption(obj, "dedup_window", gradingDuration); err != nil {
		return err
	}
	if c.NotifyUser, err = gradingOption(obj, "notify_user", gradingValue[pushGradingNotifyUserConfig]); err != nil {
		return err
	}
	if c.MergeHeld, err = gradingOption(obj, "merge_held", gradingValue[bool]); err != nil {
		return err
	}
	if c.DedupExemptLevels, err = gradingOption(obj, "dedup_exempt_levels", gradingValue[[]string]); err != nil {
		return err
	}
	if c.Cards, err = gradingOption(obj, "cards", gradingValue[pushGradingCardsConfig]); err != nil {
		return err
	}
	return nil
}

func (c *pushGradingActionsConfig) UnmarshalJSON(raw []byte) error {
	*c = pushGradingActionsConfig{}
	obj, err := decodePushGradingObject(raw, "P0", "P1", "P2")
	if err != nil {
		return err
	}
	if c.P0, err = gradingOption(obj, "P0", gradingValue[string]); err != nil {
		return err
	}
	if c.P1, err = gradingOption(obj, "P1", gradingValue[string]); err != nil {
		return err
	}
	if c.P2, err = gradingOption(obj, "P2", gradingValue[string]); err != nil {
		return err
	}
	return nil
}

func (c *pushGradingNotifyUserConfig) UnmarshalJSON(raw []byte) error {
	*c = pushGradingNotifyUserConfig{}
	obj, err := decodePushGradingObject(raw, "main", "others", "others_max")
	if err != nil {
		return err
	}
	if c.Main, err = gradingOption(obj, "main", gradingValue[string]); err != nil {
		return err
	}
	if c.Others, err = gradingOption(obj, "others", gradingValue[string]); err != nil {
		return err
	}
	if c.OthersMax, err = gradingOption(obj, "others_max", gradingValue[string]); err != nil {
		return err
	}
	return nil
}

func (c *pushGradingCardsConfig) UnmarshalJSON(raw []byte) error {
	*c = pushGradingCardsConfig{}
	obj, err := decodePushGradingObject(raw, "supervisor_hold", "escalate_reasons", "hold_conclusions", "hold_acceptance", "decision_level", "report_level")
	if err != nil {
		return err
	}
	if c.SupervisorHold, err = gradingOption(obj, "supervisor_hold", gradingValue[bool]); err != nil {
		return err
	}
	if c.EscalateReasons, err = gradingOption(obj, "escalate_reasons", gradingValue[[]string]); err != nil {
		return err
	}
	if c.HoldConclusions, err = gradingOption(obj, "hold_conclusions", gradingValue[bool]); err != nil {
		return err
	}
	if c.HoldAcceptance, err = gradingOption(obj, "hold_acceptance", gradingValue[bool]); err != nil {
		return err
	}
	if c.DecisionLevel, err = gradingOption(obj, "decision_level", gradingValue[string]); err != nil {
		return err
	}
	if c.ReportLevel, err = gradingOption(obj, "report_level", gradingValue[string]); err != nil {
		return err
	}
	return nil
}

func (c pushGradingConfig) MarshalJSON() ([]byte, error) {
	obj := map[string]any{"version": c.Version}
	putGradingOption(obj, "enabled", c.Enabled)
	putGradingOption(obj, "actions", c.Actions)
	if c.DedupWindow.set {
		if c.DedupWindow.null {
			obj["dedup_window"] = nil
		} else {
			obj["dedup_window"] = c.DedupWindow.value.String()
		}
	}
	putGradingOption(obj, "notify_user", c.NotifyUser)
	putGradingOption(obj, "merge_held", c.MergeHeld)
	putGradingOption(obj, "dedup_exempt_levels", c.DedupExemptLevels)
	putGradingOption(obj, "cards", c.Cards)
	return json.Marshal(obj)
}

func (c pushGradingActionsConfig) MarshalJSON() ([]byte, error) {
	obj := map[string]any{}
	putGradingOption(obj, "P0", c.P0)
	putGradingOption(obj, "P1", c.P1)
	putGradingOption(obj, "P2", c.P2)
	return json.Marshal(obj)
}

func (c pushGradingNotifyUserConfig) MarshalJSON() ([]byte, error) {
	obj := map[string]any{}
	putGradingOption(obj, "main", c.Main)
	putGradingOption(obj, "others", c.Others)
	putGradingOption(obj, "others_max", c.OthersMax)
	return json.Marshal(obj)
}

func (c pushGradingCardsConfig) MarshalJSON() ([]byte, error) {
	obj := map[string]any{}
	putGradingOption(obj, "supervisor_hold", c.SupervisorHold)
	putGradingOption(obj, "escalate_reasons", c.EscalateReasons)
	putGradingOption(obj, "hold_conclusions", c.HoldConclusions)
	putGradingOption(obj, "hold_acceptance", c.HoldAcceptance)
	putGradingOption(obj, "decision_level", c.DecisionLevel)
	putGradingOption(obj, "report_level", c.ReportLevel)
	return json.Marshal(obj)
}

func putGradingOption[T any](obj map[string]any, name string, option pushGradingOption[T]) {
	if !option.set {
		return
	}
	if option.null {
		obj[name] = nil
		return
	}
	obj[name] = option.value
}

func gradingOption[T any](obj map[string]json.RawMessage, name string, parse func(json.RawMessage) (T, error)) (pushGradingOption[T], error) {
	raw, ok := obj[name]
	if !ok {
		return pushGradingOption[T]{}, nil
	}
	if pushGradingNull(raw) {
		return pushGradingOption[T]{set: true, null: true}, nil
	}
	value, err := parse(raw)
	if err != nil {
		return pushGradingOption[T]{}, fmt.Errorf("push grading: %s: %w", name, err)
	}
	return pushGradingOption[T]{set: true, value: value}, nil
}

func gradingValue[T any](raw json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(raw, &value)
	return value, err
}

func gradingDuration(raw json.RawMessage) (time.Duration, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return time.ParseDuration(text)
}

func pushGradingNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decodePushGradingObject(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	open, err := dec.Token()
	if err != nil || open != json.Delim('{') {
		return nil, fmt.Errorf("push grading: expected an object")
	}
	obj := map[string]json.RawMessage{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("push grading: expected a field name")
		}
		if _, exists := obj[name]; exists {
			return nil, fmt.Errorf("push grading: duplicate field %s", name)
		}
		if !slices.Contains(allowed, name) {
			return nil, fmt.Errorf("push grading: unknown field %s", name)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		obj[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("push grading: trailing JSON value")
	}
	return obj, nil
}

func (c pushGradingConfig) validate(extension bool) error {
	if extension && c.Enabled.set {
		return fmt.Errorf("push grading: enabled is owner-only")
	}
	if c.Actions.set && !c.Actions.null {
		for name, option := range map[string]pushGradingOption[string]{"P0": c.Actions.value.P0, "P1": c.Actions.value.P1, "P2": c.Actions.value.P2} {
			if !option.set || option.null {
				continue
			}
			if !slices.Contains([]string{"push", "hold", "silence"}, option.value) {
				return fmt.Errorf("push grading: actions.%s must be push, hold or silence", name)
			}
			if extension && name == "P0" && option.value != "push" {
				return fmt.Errorf("push grading: an extension cannot hold or silence P0")
			}
		}
	}
	if c.DedupWindow.set && !c.DedupWindow.null && (c.DedupWindow.value < 0 || c.DedupWindow.value > 48*time.Hour) {
		return fmt.Errorf("push grading: dedup_window must be between 0s and 48h")
	}
	if c.NotifyUser.set && !c.NotifyUser.null {
		for name, option := range map[string]pushGradingOption[string]{"main": c.NotifyUser.value.Main, "others": c.NotifyUser.value.Others, "others_max": c.NotifyUser.value.OthersMax} {
			if !option.set || option.null {
				continue
			}
			if name == "others_max" && option.value == "" {
				continue
			}
			if !slices.Contains([]string{"P0", "P1", "P2"}, option.value) {
				return fmt.Errorf("push grading: notify_user.%s must be P0, P1 or P2", name)
			}
		}
	}
	if c.DedupExemptLevels.set && !c.DedupExemptLevels.null {
		if err := validateGradingStrings(c.DedupExemptLevels.value, "P0", "P1", "P2"); err != nil {
			return fmt.Errorf("push grading: dedup_exempt_levels: %w", err)
		}
	}
	if c.Cards.set && !c.Cards.null {
		if c.Cards.value.EscalateReasons.set && !c.Cards.value.EscalateReasons.null {
			if err := validateGradingStrings(c.Cards.value.EscalateReasons.value, "irreversible", "visible_to_others", "only_owner_knows"); err != nil {
				return fmt.Errorf("push grading: cards.escalate_reasons: %w", err)
			}
		}
		for name, option := range map[string]pushGradingOption[string]{"decision_level": c.Cards.value.DecisionLevel, "report_level": c.Cards.value.ReportLevel} {
			if option.set && !option.null && !slices.Contains([]string{"P0", "P1", "P2"}, option.value) {
				return fmt.Errorf("push grading: cards.%s must be P0, P1 or P2", name)
			}
		}
	}
	return nil
}

func validateGradingStrings(values []string, allowed ...string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return fmt.Errorf("unsupported value %q", value)
		}
		if seen[value] {
			return fmt.Errorf("duplicate value %q", value)
		}
		seen[value] = true
	}
	return nil
}

func resolvePushGrading(base, layer *pushGradingConfig, layerSource string) pushGradingEffective {
	out := defaultPushGradingEffective()
	defaults := defaultPushGradingEffective()
	apply := func(cfg *pushGradingConfig, source string) {
		if cfg == nil {
			return
		}
		if cfg.Actions.set {
			if cfg.Actions.null {
				out.Actions = defaults.Actions
				for _, field := range []string{"actions.P0", "actions.P1", "actions.P2"} {
					out.By[field] = source
				}
			} else {
				applyGradingScalar(out.By, "actions.P0", cfg.Actions.value.P0, &out.Actions.P0, defaults.Actions.P0, source)
				applyGradingScalar(out.By, "actions.P1", cfg.Actions.value.P1, &out.Actions.P1, defaults.Actions.P1, source)
				applyGradingScalar(out.By, "actions.P2", cfg.Actions.value.P2, &out.Actions.P2, defaults.Actions.P2, source)
			}
		}
		applyGradingScalar(out.By, "dedup_window", cfg.DedupWindow, &out.DedupWindow, defaults.DedupWindow, source)
		if cfg.NotifyUser.set {
			if cfg.NotifyUser.null {
				out.NotifyUser = defaults.NotifyUser
				for _, field := range []string{"notify_user.main", "notify_user.others", "notify_user.others_max"} {
					out.By[field] = source
				}
			} else {
				applyGradingScalar(out.By, "notify_user.main", cfg.NotifyUser.value.Main, &out.NotifyUser.Main, defaults.NotifyUser.Main, source)
				applyGradingScalar(out.By, "notify_user.others", cfg.NotifyUser.value.Others, &out.NotifyUser.Others, defaults.NotifyUser.Others, source)
				applyGradingScalar(out.By, "notify_user.others_max", cfg.NotifyUser.value.OthersMax, &out.NotifyUser.OthersMax, defaults.NotifyUser.OthersMax, source)
			}
		}
		applyGradingScalar(out.By, "merge_held", cfg.MergeHeld, &out.MergeHeld, defaults.MergeHeld, source)
		applyGradingSlice(out.By, "dedup_exempt_levels", cfg.DedupExemptLevels, &out.DedupExemptLevels, defaults.DedupExemptLevels, source)
		if cfg.Cards.set {
			if cfg.Cards.null {
				out.Cards = defaults.Cards
				for _, field := range []string{"cards.supervisor_hold", "cards.escalate_reasons", "cards.hold_conclusions", "cards.hold_acceptance", "cards.decision_level", "cards.report_level"} {
					out.By[field] = source
				}
			} else {
				applyGradingScalar(out.By, "cards.supervisor_hold", cfg.Cards.value.SupervisorHold, &out.Cards.SupervisorHold, defaults.Cards.SupervisorHold, source)
				applyGradingSlice(out.By, "cards.escalate_reasons", cfg.Cards.value.EscalateReasons, &out.Cards.EscalateReasons, defaults.Cards.EscalateReasons, source)
				applyGradingScalar(out.By, "cards.hold_conclusions", cfg.Cards.value.HoldConclusions, &out.Cards.HoldConclusions, defaults.Cards.HoldConclusions, source)
				applyGradingScalar(out.By, "cards.hold_acceptance", cfg.Cards.value.HoldAcceptance, &out.Cards.HoldAcceptance, defaults.Cards.HoldAcceptance, source)
				applyGradingScalar(out.By, "cards.decision_level", cfg.Cards.value.DecisionLevel, &out.Cards.DecisionLevel, defaults.Cards.DecisionLevel, source)
				applyGradingScalar(out.By, "cards.report_level", cfg.Cards.value.ReportLevel, &out.Cards.ReportLevel, defaults.Cards.ReportLevel, source)
			}
		}
	}
	apply(base, "base")
	apply(layer, layerSource)
	switch {
	case base != nil && base.Enabled.set && !base.Enabled.null:
		out.Enabled = base.Enabled.value
		out.By["enabled"] = "base"
	case layer != nil:
		out.Enabled = true
		out.By["enabled"] = layerSource
	default:
		out.Enabled = false
		out.By["enabled"] = "default"
	}
	// F2 is a floor, not a preference: configuration may add exemptions but can
	// never remove P0. Keep it first so the resolved value is deterministic.
	if !slices.Contains(out.DedupExemptLevels, "P0") {
		out.DedupExemptLevels = append([]string{"P0"}, out.DedupExemptLevels...)
	}
	out.By["dedup_exempt_levels.P0"] = "floor:F2"
	return out
}

func applyGradingScalar[T any](by map[string]string, field string, option pushGradingOption[T], destination *T, defaultValue T, source string) {
	if !option.set {
		return
	}
	if option.null {
		*destination = defaultValue
	} else {
		*destination = option.value
	}
	by[field] = source
}

func applyGradingSlice(by map[string]string, field string, option pushGradingOption[[]string], destination *[]string, defaultValue []string, source string) {
	if !option.set {
		return
	}
	if option.null {
		*destination = slices.Clone(defaultValue)
	} else {
		*destination = slices.Clone(option.value)
	}
	by[field] = source
}
