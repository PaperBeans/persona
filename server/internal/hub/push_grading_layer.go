package hub

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Extension push grading layers and read-only preview (docs/server/server.md "Push grading", issue #14380).
//
// Each extension explicitly granted the push.grading scope owns at most one persisted grading layer, stored under
// hub_settings key hub.push_grading.layers (separate from the owner's hub.ln_mode switch, which extensions may never
// modify). Active layers belonging to a push's manager combine with the owner's settings (hub.ln_mode and active
// attention mode):
//   - When no extension layer exists, push grading behaves identically to the owner's settings.
//   - System alerts (pairing, computer joining) are never modified or held by extension layers.
//   - Active owner attention modes (fragment, immersive, quiet) are never weakened by extension layers: if the owner's
//     attention mode holds or hushes a push, that holds even when an extension layer would allow or promote it.
//   - An extension layer may activate grading for its manager while hub.ln_mode is off, adjust the repeat-suppression
//     window, or match pushes with ordered rules to re-grade or hold/silence/allow them. It can never turn off
//     hub.ln_mode when the owner turned it on.
//   - When several extensions have active layers on the same manager and more than one matches a push, the stricter
//     effect wins (silent > hold > P2 > P1 > allow / P0), keeping composition deterministic and order-independent.
//   - Removing the layer (by the owning extension or by full owner credentials) or disabling / uninstalling / dropping
//     the scope of the extension (reconciled from extwire.Report) removes the layer and restores the owner's settings.
//   - POST /api/v1/push/grading/preview runs the exact same pure evaluator (evaluatePushGradingLocked) without sending,
//     recording, or mutating any notification, dedup, intercept, delivered-push, or layer state.

const (
	gradingLayersSettingKey = "hub.push_grading.layers"

	pushGradingLayerBodyLimit  = 16 << 10
	pushGradingLayerMaxRules   = 32
	pushGradingLayersMax       = 64
	pushGradingMinDedupSeconds = 60
	pushGradingMaxDedupSeconds = 24 * 60 * 60
	pushGradingMaxFieldRunes   = 128
	pushGradingMaxRuleIDRunes  = 64

	codeLayerOwnershipDenied = "layer_ownership_denied"

	pushGradingActionAllow  = "allow"
	pushGradingActionHush   = "hush"
	pushGradingActionHold   = "hold"
	pushGradingActionSilent = "silent"

	pushGradingSourceDefault   = "default"
	pushGradingSourceOwner     = "owner"
	pushGradingSourceAttention = "attention"
	pushGradingSourceExtension = "extension"
)

var (
	gradingRuleIDPattern = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	gradingReasonPattern = lazyre.New(`^[a-z][a-z0-9_]{0,63}$`)
)

// gradingLayerKey uniquely identifies the single push grading layer owned by one extension on one manager.
type gradingLayerKey struct {
	Manager   string
	Extension string
}

func normalizeGradingManager(manager string) string {
	return cmp.Or(strings.TrimSpace(manager), assistantwire.AssistantID)
}

// PushGradingLayer is one authorized extension's persisted push grading layer.
type PushGradingLayer struct {
	Extension          string            `json:"extension"`
	Manager            string            `json:"manager"`
	Enabled            bool              `json:"enabled"`
	GradingEnabled     *bool             `json:"grading_enabled,omitempty"`
	DedupWindowSeconds *int              `json:"dedup_window_seconds,omitempty"`
	Rules              []PushGradingRule `json:"rules,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// PushGradingRule matches a push by When and applies an optional level re-grade and/or direct action.
type PushGradingRule struct {
	ID     string           `json:"id,omitempty"`
	When   PushGradingMatch `json:"when"`
	Level  string           `json:"level,omitempty"`
	Action string           `json:"action,omitempty"`
	Reason string           `json:"reason,omitempty"`
}

// PushGradingMatch is the conjunction of non-empty predicates a rule matches against one push.
type PushGradingMatch struct {
	Kinds            []string `json:"kinds,omitempty"`
	Levels           []string `json:"levels,omitempty"`
	RelatedKeyPrefix string   `json:"related_key_prefix,omitempty"`
	MachineID        string   `json:"machine_id,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	Track            string   `json:"track,omitempty"`
	Depth            string   `json:"depth,omitempty"`
	Extension        string   `json:"extension,omitempty"`
}

// PushGradingMatchInfo identifies which extension layer (and optional rule) contributed to a grading decision.
type PushGradingMatchInfo struct {
	Extension string `json:"extension"`
	RuleIndex *int   `json:"rule_index,omitempty"`
	RuleID    string `json:"rule_id,omitempty"`
}

// PushGradingDecision is the combined grading verdict shared by live delivery and POST /api/v1/push/grading/preview.
type PushGradingDecision struct {
	Allow              bool                  `json:"allow"`
	Held               bool                  `json:"held"`
	Action             string                `json:"action"`
	Reason             string                `json:"reason,omitempty"`
	Hush               bool                  `json:"hush"`
	InputLevel         string                `json:"input_level"`
	EffectiveLevel     string                `json:"effective_level"`
	Source             string                `json:"source"`
	OwnerEnabled       bool                  `json:"owner_enabled"`
	EffectiveGrading   bool                  `json:"effective_grading"`
	AttentionMode      string                `json:"attention_mode"`
	DedupWindowSeconds int                   `json:"dedup_window_seconds"`
	DedupHit           bool                  `json:"dedup_hit"`
	MatchedLayer       *PushGradingMatchInfo `json:"matched_layer,omitempty"`
}

type pushGradingLayerInput struct {
	Extension          string            `json:"extension,omitempty"`
	Manager            string            `json:"manager,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	GradingEnabled     *bool             `json:"grading_enabled,omitempty"`
	DedupWindowSeconds *int              `json:"dedup_window_seconds,omitempty"`
	Rules              []PushGradingRule `json:"rules,omitempty"`
}

type pushGradingPreviewRequest struct {
	Notification pushGradingPreviewNotification `json:"notification"`
	Layer        *pushGradingLayerInput         `json:"layer,omitempty"`
}

type pushGradingPreviewNotification struct {
	Kind       string `json:"kind,omitempty"`
	Level      string `json:"level,omitempty"`
	RelatedKey string `json:"related_key,omitempty"`
	MachineID  string `json:"machine_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TodoID     string `json:"todo_id,omitempty"`
	Track      string `json:"track,omitempty"`
	Depth      string `json:"depth,omitempty"`
	Manager    string `json:"manager,omitempty"`
	Extension  string `json:"extension,omitempty"`
	Done       bool   `json:"done,omitempty"`
	System     bool   `json:"system,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
}

func clonePushGradingLayer(l PushGradingLayer) PushGradingLayer {
	out := l
	if l.GradingEnabled != nil {
		v := *l.GradingEnabled
		out.GradingEnabled = &v
	}
	if l.DedupWindowSeconds != nil {
		v := *l.DedupWindowSeconds
		out.DedupWindowSeconds = &v
	}
	if len(l.Rules) > 0 {
		out.Rules = make([]PushGradingRule, len(l.Rules))
		for i, r := range l.Rules {
			r.When.Kinds = slices.Clone(r.When.Kinds)
			r.When.Levels = slices.Clone(r.When.Levels)
			out.Rules[i] = r
		}
	} else {
		out.Rules = nil
	}
	return out
}

func validPushGradingKind(kind string) bool {
	switch kind {
	case assistantwire.NotifyInput, assistantwire.NotifyPush, assistantwire.NotifyDone,
		assistantwire.NotifyReply, assistantwire.NotifyDecide, kindAlert:
		return true
	default:
		return false
	}
}

func validPushGradingLevel(level string) bool {
	switch level {
	case assistantwire.LevelP0, assistantwire.LevelP1, assistantwire.LevelP2:
		return true
	default:
		return false
	}
}

func validPushGradingDepth(depth string) bool {
	return depth == "" || depth == assistantwire.DepthQuick || depth == assistantwire.DepthDeep
}

func validPushGradingField(s string, maxRunes int) bool {
	if s == "" {
		return true
	}
	if strings.TrimSpace(s) != s || utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (in pushGradingLayerInput) buildLayer(manager, ext string, now time.Time) (PushGradingLayer, error) {
	if in.Enabled == nil && in.GradingEnabled == nil && in.DedupWindowSeconds == nil && len(in.Rules) == 0 {
		return PushGradingLayer{}, errors.New("layer must set enabled, grading_enabled, dedup_window_seconds, or at least one rule")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	layer := PushGradingLayer{
		Extension:          ext,
		Manager:            normalizeGradingManager(manager),
		Enabled:            enabled,
		GradingEnabled:     in.GradingEnabled,
		DedupWindowSeconds: in.DedupWindowSeconds,
		Rules:              in.Rules,
		UpdatedAt:          now.UTC(),
	}
	layer = clonePushGradingLayer(layer)
	if err := layer.validate(); err != nil {
		return PushGradingLayer{}, err
	}
	return layer, nil
}

func (l PushGradingLayer) validate() error {
	if !validPushGradingField(l.Extension, pushGradingMaxFieldRunes) || l.Extension == "" {
		return errors.New("layer extension is required")
	}
	if !validPushGradingField(l.Manager, pushGradingMaxFieldRunes) || l.Manager == "" {
		return errors.New("layer manager is required")
	}
	if l.DedupWindowSeconds != nil {
		if *l.DedupWindowSeconds < pushGradingMinDedupSeconds || *l.DedupWindowSeconds > pushGradingMaxDedupSeconds {
			return fmt.Errorf("dedup_window_seconds must be between %d and %d", pushGradingMinDedupSeconds, pushGradingMaxDedupSeconds)
		}
	}
	if len(l.Rules) > pushGradingLayerMaxRules {
		return fmt.Errorf("rules has %d entries; at most %d", len(l.Rules), pushGradingLayerMaxRules)
	}
	seenIDs := map[string]bool{}
	for i, r := range l.Rules {
		if r.ID != "" {
			if utf8.RuneCountInString(r.ID) > pushGradingMaxRuleIDRunes || !gradingRuleIDPattern.MatchString(r.ID) {
				return fmt.Errorf("rules[%d].id is invalid", i)
			}
			if seenIDs[r.ID] {
				return fmt.Errorf("rules[%d].id %q is repeated", i, r.ID)
			}
			seenIDs[r.ID] = true
		}
		if r.Level == "" && r.Action == "" {
			return fmt.Errorf("rules[%d] must set level or action", i)
		}
		if r.Level != "" && !validPushGradingLevel(r.Level) {
			return fmt.Errorf("rules[%d].level must be P0, P1 or P2", i)
		}
		switch r.Action {
		case "", pushGradingActionAllow, pushGradingActionHold, pushGradingActionSilent:
		default:
			return fmt.Errorf("rules[%d].action must be allow, hold or silent", i)
		}
		if r.Reason != "" {
			if r.Action == pushGradingActionAllow {
				return fmt.Errorf("rules[%d].reason is not valid with action allow", i)
			}
			if !gradingReasonPattern.MatchString(r.Reason) {
				return fmt.Errorf("rules[%d].reason must be lowercase ASCII snake_case", i)
			}
		}
		if err := r.When.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (m PushGradingMatch) validate(index int) error {
	seenKinds := map[string]bool{}
	for _, k := range m.Kinds {
		if !validPushGradingKind(k) || seenKinds[k] {
			return fmt.Errorf("rules[%d].when.kinds has an invalid or duplicate kind %q", index, k)
		}
		seenKinds[k] = true
	}
	seenLevels := map[string]bool{}
	for _, lv := range m.Levels {
		if !validPushGradingLevel(lv) || seenLevels[lv] {
			return fmt.Errorf("rules[%d].when.levels has an invalid or duplicate level %q", index, lv)
		}
		seenLevels[lv] = true
	}
	if !validPushGradingDepth(m.Depth) {
		return fmt.Errorf("rules[%d].when.depth must be quick or deep", index)
	}
	for name, val := range map[string]string{
		"related_key_prefix": m.RelatedKeyPrefix,
		"machine_id":         m.MachineID,
		"session_id":         m.SessionID,
		"track":              m.Track,
		"extension":          m.Extension,
	} {
		if !validPushGradingField(val, pushGradingMaxFieldRunes) {
			return fmt.Errorf("rules[%d].when.%s is invalid", index, name)
		}
	}
	return nil
}

func (m PushGradingMatch) matches(policy pushPolicy, inputLevel string) bool {
	if len(m.Kinds) > 0 && !slices.Contains(m.Kinds, policy.kind) {
		return false
	}
	if len(m.Levels) > 0 && !slices.Contains(m.Levels, inputLevel) {
		return false
	}
	if m.RelatedKeyPrefix != "" && !strings.HasPrefix(strings.TrimSpace(policy.relatedKey), m.RelatedKeyPrefix) {
		return false
	}
	if m.MachineID != "" && strings.TrimSpace(policy.machineID) != m.MachineID {
		return false
	}
	if m.SessionID != "" && strings.TrimSpace(policy.sessionID) != m.SessionID {
		return false
	}
	if m.Track != "" && strings.TrimSpace(policy.track) != m.Track {
		return false
	}
	if m.Depth != "" {
		d := policy.depth
		if d == "" {
			d = assistantwire.DepthQuick
		}
		if d != m.Depth {
			return false
		}
	}
	if m.Extension != "" && strings.TrimSpace(policy.extension) != m.Extension {
		return false
	}
	return true
}

func (n pushGradingPreviewNotification) validate() error {
	if n.Kind != "" && !validPushGradingKind(n.Kind) {
		return errors.New("notification.kind must be input, assistant, done, reply, decide or alert")
	}
	if n.Level != "" && !validPushGradingLevel(n.Level) {
		return errors.New("notification.level must be P0, P1 or P2")
	}
	if !validPushGradingDepth(n.Depth) {
		return errors.New("notification.depth must be quick or deep")
	}
	for name, val := range map[string]string{
		"related_key": n.RelatedKey,
		"machine_id":  n.MachineID,
		"session_id":  n.SessionID,
		"todo_id":     n.TodoID,
		"track":       n.Track,
		"manager":     n.Manager,
		"extension":   n.Extension,
	} {
		if !validPushGradingField(val, pushGradingMaxFieldRunes) {
			return fmt.Errorf("notification.%s is invalid", name)
		}
	}
	if utf8.RuneCountInString(n.Title) > notifyTitleMost {
		return errors.New("notification.title is too long")
	}
	if utf8.RuneCountInString(n.Body) > notifyBodyMost {
		return errors.New("notification.body is too long")
	}
	return nil
}

// ---- evaluation (pure, shared between live applyPushGrading and read-only preview) ----

type matchedRuleEffect struct {
	extension string
	ruleIndex int
	ruleID    string
	level     string
	action    string
	reason    string
	rank      int
}

func (e *matchedRuleEffect) info() *PushGradingMatchInfo {
	if e == nil {
		return nil
	}
	idx := e.ruleIndex
	return &PushGradingMatchInfo{Extension: e.extension, RuleIndex: &idx, RuleID: e.ruleID}
}

func ruleStrictnessRank(r PushGradingRule) int {
	switch r.Action {
	case pushGradingActionSilent:
		return 5
	case pushGradingActionHold:
		return 4
	case pushGradingActionAllow:
		return 1
	}
	switch r.Level {
	case assistantwire.LevelP2:
		return 3
	case assistantwire.LevelP1:
		return 2
	case assistantwire.LevelP0:
		return 1
	default:
		return 0
	}
}

func normalizePushLevel(level string) string {
	switch level {
	case assistantwire.LevelP0, assistantwire.LevelP2:
		return level
	default:
		return assistantwire.LevelP1
	}
}

func allowOrHushAction(hush bool) string {
	if hush {
		return pushGradingActionHush
	}
	return pushGradingActionAllow
}

func holdActionForReason(reason string) string {
	if reason == interceptReasonP2 || reason == assistantwire.AttentionReasonSilent {
		return pushGradingActionSilent
	}
	return pushGradingActionHold
}

// evaluatePushGradingLocked computes the combined push grading decision without mutating any state.
// Callers may invoke it from applyPushGrading (which then records sent/intercept/dedup under gradingMu)
// or from previewPushGrading (which returns the verdict directly with zero side effects).
func evaluatePushGradingLocked(
	now time.Time,
	ownerEnabled bool,
	attn assistantwire.Attention,
	layers []PushGradingLayer,
	lastAlert map[string]time.Time,
	policy pushPolicy,
	mainConvPromote bool,
) PushGradingDecision {
	attnMode := attn.Mode
	if attnMode == "" {
		attnMode = assistantwire.AttentionNormal
	}
	inputLevel := normalizePushLevel(policy.level)
	if policy.system {
		inputLevel = assistantwire.LevelP0
	} else if policy.done && policy.level == "" {
		inputLevel = assistantwire.LevelP2
	}
	if mainConvPromote && !policy.system && (attn.Active() || ownerEnabled || len(layers) > 0) {
		inputLevel = assistantwire.LevelP0
	}

	baseDecision := func() PushGradingDecision {
		return PushGradingDecision{
			InputLevel:         inputLevel,
			EffectiveLevel:     inputLevel,
			OwnerEnabled:       ownerEnabled,
			AttentionMode:      attnMode,
			DedupWindowSeconds: int(gradingDedupWindow.Seconds()),
		}
	}

	// Hub system alerts (pairing, computer joining) are always P0 and never altered or held by extension layers.
	if policy.system {
		dec := baseDecision()
		if attn.Active() {
			v := assistantwire.DecideAttention(attn, attentionPush(policy, assistantwire.LevelP0))
			dec.EffectiveGrading = true
			dec.Allow = !v.Hold
			dec.Held = v.Hold
			dec.Hush = v.Hush
			dec.Reason = v.Reason
			dec.Source = pushGradingSourceAttention
			if v.Hold {
				dec.Action = holdActionForReason(v.Reason)
			} else {
				dec.Action = allowOrHushAction(v.Hush)
			}
			return dec
		}
		dec.Allow = true
		dec.Action = pushGradingActionAllow
		dec.EffectiveGrading = ownerEnabled
		if ownerEnabled {
			dec.Source = pushGradingSourceOwner
		} else {
			dec.Source = pushGradingSourceDefault
		}
		return dec
	}

	layerActivatesGrading := false
	activatingLayer := ""
	dedupWindow := gradingDedupWindow
	dedupLayer := ""
	hasCustomDedup := false
	var bestRule *matchedRuleEffect

	for _, layer := range layers {
		if !layer.Enabled {
			continue
		}
		if layer.GradingEnabled != nil && *layer.GradingEnabled {
			layerActivatesGrading = true
			if activatingLayer == "" {
				activatingLayer = layer.Extension
			}
		}
		if layer.DedupWindowSeconds != nil && *layer.DedupWindowSeconds > 0 {
			w := time.Duration(*layer.DedupWindowSeconds) * time.Second
			if !hasCustomDedup || w > dedupWindow {
				dedupWindow = w
				dedupLayer = layer.Extension
				hasCustomDedup = true
			}
		}
		for i, r := range layer.Rules {
			if !r.When.matches(policy, inputLevel) {
				continue
			}
			cand := matchedRuleEffect{
				extension: layer.Extension,
				ruleIndex: i,
				ruleID:    r.ID,
				level:     r.Level,
				action:    r.Action,
				reason:    r.Reason,
				rank:      ruleStrictnessRank(r),
			}
			if bestRule == nil || cand.rank > bestRule.rank {
				c := cand
				bestRule = &c
			}
			break // first matching rule within one layer wins
		}
	}

	effectiveLevel := inputLevel
	if bestRule != nil && bestRule.level != "" {
		effectiveLevel = bestRule.level
	}
	effectiveGrading := attn.Active() || ownerEnabled || layerActivatesGrading

	dec := baseDecision()
	dec.EffectiveLevel = effectiveLevel
	dec.EffectiveGrading = effectiveGrading
	dec.DedupWindowSeconds = int(dedupWindow.Seconds())

	// Owner's active attention mode always takes precedence over extension allows/promotions.
	if attn.Active() {
		baseAttn := assistantwire.DecideAttention(attn, attentionPush(policy, inputLevel))
		effAttn := assistantwire.DecideAttention(attn, attentionPush(policy, effectiveLevel))
		if baseAttn.Hold {
			dec.Allow = false
			dec.Held = true
			dec.Reason = baseAttn.Reason
			dec.Action = holdActionForReason(baseAttn.Reason)
			dec.Source = pushGradingSourceAttention
			return dec
		}
		if effAttn.Hold {
			dec.Allow = false
			dec.Held = true
			dec.Reason = effAttn.Reason
			dec.Action = holdActionForReason(effAttn.Reason)
			dec.Source = pushGradingSourceExtension
			dec.MatchedLayer = bestRule.info()
			return dec
		}
		if bestRule != nil && (bestRule.action == pushGradingActionHold || bestRule.action == pushGradingActionSilent) {
			dec.Allow = false
			dec.Held = true
			dec.Action = bestRule.action
			if bestRule.action == pushGradingActionSilent {
				dec.Reason = cmp.Or(bestRule.reason, interceptReasonP2)
			} else {
				dec.Reason = cmp.Or(bestRule.reason, interceptReasonP1)
			}
			dec.Source = pushGradingSourceExtension
			dec.MatchedLayer = bestRule.info()
			return dec
		}
		dec.Allow = true
		dec.Hush = baseAttn.Hush || effAttn.Hush
		dec.Action = allowOrHushAction(dec.Hush)
		dec.Source = pushGradingSourceAttention
		if bestRule != nil {
			dec.MatchedLayer = bestRule.info()
		}
		return dec
	}

	// Normal attention mode: apply explicit rule action if any rule matched with an explicit action.
	if bestRule != nil && bestRule.action != "" {
		dec.MatchedLayer = bestRule.info()
		dec.Source = pushGradingSourceExtension
		switch bestRule.action {
		case pushGradingActionSilent:
			dec.Allow = false
			dec.Held = true
			dec.Action = pushGradingActionSilent
			dec.Reason = cmp.Or(bestRule.reason, interceptReasonP2)
			return dec
		case pushGradingActionHold:
			dec.Allow = false
			dec.Held = true
			dec.Action = pushGradingActionHold
			dec.Reason = cmp.Or(bestRule.reason, interceptReasonP1)
			return dec
		case pushGradingActionAllow:
			dec.Allow = true
			dec.Action = pushGradingActionAllow
			return dec
		}
	}

	if !effectiveGrading {
		dec.Allow = true
		dec.Action = pushGradingActionAllow
		dec.Source = pushGradingSourceDefault
		if bestRule != nil {
			dec.Source = pushGradingSourceExtension
			dec.MatchedLayer = bestRule.info()
		}
		return dec
	}

	// Grading is active (owner switch on or extension layer enabled grading).
	source := pushGradingSourceOwner
	var matched *PushGradingMatchInfo
	if bestRule != nil {
		source = pushGradingSourceExtension
		matched = bestRule.info()
	} else if !ownerEnabled && layerActivatesGrading {
		source = pushGradingSourceExtension
		matched = &PushGradingMatchInfo{Extension: activatingLayer}
	}
	dec.Source = source
	dec.MatchedLayer = matched

	// P0 always interrupts, before the dedup window check.
	if effectiveLevel == assistantwire.LevelP0 {
		dec.Allow = true
		dec.Action = pushGradingActionAllow
		return dec
	}

	key := strings.TrimSpace(policy.relatedKey)
	if key != "" && lastAlert != nil {
		if last, seen := lastAlert[key]; seen && now.Sub(last) < dedupWindow {
			dec.Allow = false
			dec.Held = true
			dec.Action = pushGradingActionHold
			dec.Reason = interceptReasonDedup
			dec.DedupHit = true
			if hasCustomDedup && (!ownerEnabled || now.Sub(last) >= gradingDedupWindow) && dec.MatchedLayer == nil {
				dec.Source = pushGradingSourceExtension
				dec.MatchedLayer = &PushGradingMatchInfo{Extension: dedupLayer}
			}
			return dec
		}
	}

	switch effectiveLevel {
	case assistantwire.LevelP2:
		dec.Allow = false
		dec.Held = true
		dec.Action = pushGradingActionSilent
		if bestRule != nil && bestRule.reason != "" {
			dec.Reason = bestRule.reason
		} else {
			dec.Reason = interceptReasonP2
		}
		return dec
	default:
		dec.EffectiveLevel = assistantwire.LevelP1
		dec.Allow = false
		dec.Held = true
		dec.Action = pushGradingActionHold
		if bestRule != nil && bestRule.reason != "" {
			dec.Reason = bestRule.reason
		} else {
			dec.Reason = interceptReasonP1
		}
		return dec
	}
}

// ---- Pusher layer storage, persistence, reconciliation, and read-only preview ----

func (p *Pusher) hasAnyActiveGradingLayer() bool {
	if p == nil {
		return false
	}
	p.gradingMu.Lock()
	defer p.gradingMu.Unlock()
	for _, l := range p.gradingLayers {
		if l.Enabled {
			return true
		}
	}
	return false
}

func (p *Pusher) hasActiveGradingLayerFor(manager string) bool {
	if p == nil {
		return false
	}
	mgr := normalizeGradingManager(manager)
	p.gradingMu.Lock()
	defer p.gradingMu.Unlock()
	for k, l := range p.gradingLayers {
		if k.Manager == mgr && l.Enabled {
			return true
		}
	}
	return false
}

// activeGradingLayersLocked returns active layers for manager sorted by Extension ascending. gradingMu is held.
func (p *Pusher) activeGradingLayersLocked(manager string) []PushGradingLayer {
	if len(p.gradingLayers) == 0 {
		return nil
	}
	mgr := normalizeGradingManager(manager)
	var out []PushGradingLayer
	for k, l := range p.gradingLayers {
		if k.Manager == mgr && l.Enabled {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b PushGradingLayer) int {
		return cmp.Compare(a.Extension, b.Extension)
	})
	return out
}

// maxDedupWindowLocked returns the longest dedup window configured by any active layer. gradingMu is held.
func (p *Pusher) maxDedupWindowLocked() time.Duration {
	var best time.Duration
	for _, l := range p.gradingLayers {
		if l.Enabled && l.DedupWindowSeconds != nil {
			if w := time.Duration(*l.DedupWindowSeconds) * time.Second; w > best {
				best = w
			}
		}
	}
	return best
}

func sortedGradingLayers(m map[gradingLayerKey]PushGradingLayer) []PushGradingLayer {
	out := make([]PushGradingLayer, 0, len(m))
	for _, l := range m {
		out = append(out, clonePushGradingLayer(l))
	}
	slices.SortFunc(out, func(a, b PushGradingLayer) int {
		if c := cmp.Compare(a.Manager, b.Manager); c != 0 {
			return c
		}
		return cmp.Compare(a.Extension, b.Extension)
	})
	return out
}

func (p *Pusher) gradingLayer(key gradingLayerKey) (PushGradingLayer, bool) {
	if p == nil {
		return PushGradingLayer{}, false
	}
	key.Manager = normalizeGradingManager(key.Manager)
	p.gradingMu.Lock()
	defer p.gradingMu.Unlock()
	l, ok := p.gradingLayers[key]
	if !ok {
		return PushGradingLayer{}, false
	}
	return clonePushGradingLayer(l), true
}

func (p *Pusher) listGradingLayers(manager string) []PushGradingLayer {
	if p == nil {
		return []PushGradingLayer{}
	}
	p.gradingMu.Lock()
	all := sortedGradingLayers(p.gradingLayers)
	p.gradingMu.Unlock()
	if manager == "" {
		return all
	}
	out := make([]PushGradingLayer, 0, len(all))
	for _, l := range all {
		if l.Manager == manager {
			out = append(out, l)
		}
	}
	return out
}

func (p *Pusher) findGradingLayerByExtension(manager, ext string) (PushGradingLayer, bool) {
	if p == nil || ext == "" {
		return PushGradingLayer{}, false
	}
	if manager != "" {
		return p.gradingLayer(gradingLayerKey{Manager: manager, Extension: ext})
	}
	if l, ok := p.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: ext}); ok {
		return l, true
	}
	for _, l := range p.listGradingLayers("") {
		if l.Extension == ext {
			return l, true
		}
	}
	return PushGradingLayer{}, false
}

// loadGradingLayers reads persisted extension push grading layers once at startup.
func (p *Pusher) loadGradingLayers() {
	if p == nil || p.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
	defer cancel()
	var stored []PushGradingLayer
	ok, err := p.store.Setting(ctx, gradingLayersSettingKey, &stored)
	if err != nil {
		p.logf("push grading: read %s: %v", gradingLayersSettingKey, err)
		return
	}
	if !ok {
		return
	}
	loaded := make(map[gradingLayerKey]PushGradingLayer, len(stored))
	for _, l := range stored {
		l.Manager = normalizeGradingManager(l.Manager)
		if err := l.validate(); err != nil {
			p.logf("push grading: ignore invalid stored layer for %s/%s: %v", l.Manager, l.Extension, err)
			continue
		}
		loaded[gradingLayerKey{Manager: l.Manager, Extension: l.Extension}] = clonePushGradingLayer(l)
	}
	p.gradingMu.Lock()
	p.gradingLayers = loaded
	p.gradingLayersVersion++
	p.gradingMu.Unlock()
}

// putGradingLayer durably stores layer for its owning (manager, extension) key before updating memory.
func (p *Pusher) putGradingLayer(ctx context.Context, layer PushGradingLayer) (PushGradingLayer, error) {
	if p == nil {
		return PushGradingLayer{}, errors.New("push settings are not available")
	}
	layer = clonePushGradingLayer(layer)
	layer.Manager = normalizeGradingManager(layer.Manager)
	if err := layer.validate(); err != nil {
		return PushGradingLayer{}, err
	}
	key := gradingLayerKey{Manager: layer.Manager, Extension: layer.Extension}

	p.gradingLayerSaveMu.Lock()
	defer p.gradingLayerSaveMu.Unlock()

	p.gradingMu.Lock()
	if _, exists := p.gradingLayers[key]; !exists && len(p.gradingLayers) >= pushGradingLayersMax {
		p.gradingMu.Unlock()
		return PushGradingLayer{}, fmt.Errorf("at most %d push grading layers may be stored", pushGradingLayersMax)
	}
	next := make(map[gradingLayerKey]PushGradingLayer, len(p.gradingLayers)+1)
	for k, v := range p.gradingLayers {
		next[k] = v
	}
	next[key] = layer
	baseVersion := p.gradingLayersVersion
	p.gradingMu.Unlock()

	if p.store != nil {
		if !quiesceHeld(ctx) {
			if !p.quiesce.begin() {
				return PushGradingLayer{}, i18n.Err("hub-selfupdate.err_quiesced")
			}
			defer p.quiesce.end()
		}
		wctx, cancel := context.WithTimeout(ctx, p.storeTimeout)
		err := p.store.PutSetting(wctx, gradingLayersSettingKey, sortedGradingLayers(next))
		cancel()
		if err != nil {
			return PushGradingLayer{}, err
		}
	}

	p.gradingMu.Lock()
	if p.gradingLayers == nil {
		p.gradingLayers = map[gradingLayerKey]PushGradingLayer{}
	}
	p.gradingLayers[key] = layer
	p.gradingLayersVersion++
	if p.gradingLayersVersion == baseVersion+1 {
		p.gradingLayersDirty = false
	} else if p.store != nil {
		p.gradingLayersDirty = true
		if !p.gradingLayersWriting {
			p.startGradingLayersFlushLocked()
		}
	}
	p.gradingMu.Unlock()
	return clonePushGradingLayer(layer), nil
}

// deleteGradingLayers removes every layer matching pred and persists the update before updating memory.
func (p *Pusher) deleteGradingLayers(ctx context.Context, pred func(gradingLayerKey) bool) (int, error) {
	if p == nil {
		return 0, errors.New("push settings are not available")
	}
	p.gradingLayerSaveMu.Lock()
	defer p.gradingLayerSaveMu.Unlock()

	p.gradingMu.Lock()
	var removedKeys []gradingLayerKey
	next := make(map[gradingLayerKey]PushGradingLayer, len(p.gradingLayers))
	for k, v := range p.gradingLayers {
		if pred(k) {
			removedKeys = append(removedKeys, k)
			continue
		}
		next[k] = v
	}
	baseVersion := p.gradingLayersVersion
	p.gradingMu.Unlock()

	if len(removedKeys) == 0 {
		return 0, nil
	}

	if p.store != nil {
		if !quiesceHeld(ctx) {
			if !p.quiesce.begin() {
				return 0, i18n.Err("hub-selfupdate.err_quiesced")
			}
			defer p.quiesce.end()
		}
		wctx, cancel := context.WithTimeout(ctx, p.storeTimeout)
		err := p.store.PutSetting(wctx, gradingLayersSettingKey, sortedGradingLayers(next))
		cancel()
		if err != nil {
			return 0, err
		}
	}

	p.gradingMu.Lock()
	for _, k := range removedKeys {
		delete(p.gradingLayers, k)
	}
	p.gradingLayersVersion++
	if p.gradingLayersVersion == baseVersion+1 {
		p.gradingLayersDirty = false
	} else if p.store != nil {
		p.gradingLayersDirty = true
		if !p.gradingLayersWriting {
			p.startGradingLayersFlushLocked()
		}
	}
	p.gradingMu.Unlock()
	return len(removedKeys), nil
}

// reconcileGradingLayers prunes any stored layer of manager whose extension is no longer in activeExtensions
// (disabled, uninstalled, incompatible, or no longer granted push.grading). Memory updates synchronously so the next
// push immediately sees the restored owner settings; persistence flushes asynchronously off the bus read loop.
func (p *Pusher) reconcileGradingLayers(manager string, activeExtensions []string) int {
	if p == nil {
		return 0
	}
	mgr := normalizeGradingManager(manager)
	active := make(map[string]bool, len(activeExtensions))
	for _, id := range activeExtensions {
		active[id] = true
	}
	p.gradingMu.Lock()
	removed := 0
	for k := range p.gradingLayers {
		if k.Manager == mgr && !active[k.Extension] {
			delete(p.gradingLayers, k)
			removed++
		}
	}
	if removed > 0 {
		p.gradingLayersVersion++
		if p.store != nil {
			p.gradingLayersDirty = true
			if !p.gradingLayersWriting {
				p.startGradingLayersFlushLocked()
			}
		}
	}
	p.gradingMu.Unlock()
	return removed
}

// startGradingLayersFlushLocked launches the single background flusher. gradingMu is held.
func (p *Pusher) startGradingLayersFlushLocked() {
	p.gradingLayersWriting = true
	if p.gradingLayersIdle == nil {
		p.gradingLayersIdle = make(chan struct{})
	}
	go p.flushGradingLayers()
}

func (p *Pusher) flushGradingLayers() {
	for {
		p.gradingLayerSaveMu.Lock()
		p.gradingMu.Lock()
		if !p.gradingLayersDirty || p.store == nil {
			p.gradingLayersWriting = false
			if p.gradingLayersIdle != nil {
				close(p.gradingLayersIdle)
				p.gradingLayersIdle = nil
			}
			p.gradingMu.Unlock()
			p.gradingLayerSaveMu.Unlock()
			return
		}
		ver := p.gradingLayersVersion
		list := sortedGradingLayers(p.gradingLayers)
		p.gradingMu.Unlock()

		if !p.quiesce.beginOrDefer(func() {
			p.gradingMu.Lock()
			p.gradingLayersDirty = true
			p.gradingLayersWriting = false
			if p.gradingLayersIdle != nil {
				close(p.gradingLayersIdle)
				p.gradingLayersIdle = nil
			}
			p.gradingMu.Unlock()
		}) {
			p.gradingLayerSaveMu.Unlock()
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), p.storeTimeout)
		err := p.store.PutSetting(ctx, gradingLayersSettingKey, list)
		cancel()
		p.quiesce.end()
		p.gradingLayerSaveMu.Unlock()

		p.gradingMu.Lock()
		if err != nil {
			p.logf("push grading: save layers: %v", err)
			p.gradingLayersDirty = true
			p.gradingLayersWriting = false
			if p.gradingLayersIdle != nil {
				close(p.gradingLayersIdle)
				p.gradingLayersIdle = nil
			}
			p.gradingMu.Unlock()
			return
		}
		if p.gradingLayersVersion == ver {
			p.gradingLayersDirty = false
			p.gradingLayersWriting = false
			if p.gradingLayersIdle != nil {
				close(p.gradingLayersIdle)
				p.gradingLayersIdle = nil
			}
			p.gradingMu.Unlock()
			return
		}
		p.gradingMu.Unlock()
	}
}

// waitGradingLayersSaved waits for any background layer flush to finish (for tests, without sleeping).
func (p *Pusher) waitGradingLayersSaved(ctx context.Context) bool {
	if p == nil {
		return true
	}
	for {
		p.gradingMu.Lock()
		idle := !p.gradingLayersWriting && !p.gradingLayersDirty
		ch := p.gradingLayersIdle
		p.gradingMu.Unlock()
		if idle {
			return true
		}
		if ch == nil {
			return false
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
}

// previewPushGrading evaluates a notification against the owner's settings and active extension layers without writing
// or mutating any push, intercept, delivered-push, dedup, or layer state. When draft is non-nil, it is substituted for
// that extension's stored layer for this evaluation only and never stored.
func (p *Pusher) previewPushGrading(policy pushPolicy, draft *PushGradingLayer, mainConvPromote bool) PushGradingDecision {
	now := time.Now()
	if p != nil && p.now != nil {
		now = p.now()
	}
	attn := assistantwire.Attention{Mode: assistantwire.AttentionNormal}
	ownerEnabled := false
	if p != nil {
		attn = p.attentionNow()
		ownerEnabled = p.pushGradingOn()
	}
	var layers []PushGradingLayer
	var lastAlert map[string]time.Time
	if p != nil {
		p.gradingMu.Lock()
		mgr := normalizeGradingManager(policy.manager)
		var draftKey *gradingLayerKey
		if draft != nil {
			k := gradingLayerKey{Manager: normalizeGradingManager(draft.Manager), Extension: draft.Extension}
			draftKey = &k
		}
		for k, l := range p.gradingLayers {
			if k.Manager != mgr {
				continue
			}
			if draftKey != nil && k == *draftKey {
				continue
			}
			if l.Enabled {
				layers = append(layers, clonePushGradingLayer(l))
			}
		}
		if draft != nil && normalizeGradingManager(draft.Manager) == mgr && draft.Enabled {
			layers = append(layers, clonePushGradingLayer(*draft))
		}
		slices.SortFunc(layers, func(a, b PushGradingLayer) int {
			return cmp.Compare(a.Extension, b.Extension)
		})
		lastAlert = p.gradingLastAlert
		dec := evaluatePushGradingLocked(now, ownerEnabled, attn, layers, lastAlert, policy, mainConvPromote)
		p.gradingMu.Unlock()
		return dec
	}
	if draft != nil && draft.Enabled {
		layers = []PushGradingLayer{clonePushGradingLayer(*draft)}
	}
	return evaluatePushGradingLocked(now, ownerEnabled, attn, layers, nil, policy, mainConvPromote)
}

// ---- strict JSON decoding & HTTP routes ----

// decodeStrictJSON reads at most limit bytes, rejects duplicate keys (including case collisions), unknown fields, and
// trailing JSON values, then decodes the single JSON object into dst.
func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return err
	}
	if err := checkUniqueObjectKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

// checkUniqueObjectKeys verifies that raw is a single JSON object with no duplicate or case-colliding keys at any depth.
func checkUniqueObjectKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	type frame struct {
		object, wantKey bool
		keys            map[string]bool
	}
	var stack []*frame
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].wantKey = true
		}
	}
	values := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(stack) == 0 {
			if values++; values > 1 {
				return errors.New("request body must contain a single JSON object")
			}
			if d, ok := tok.(json.Delim); !ok || d != '{' {
				return errors.New("request body must be a JSON object")
			}
		}
		if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].wantKey {
			if d, ok := tok.(json.Delim); ok && d == '}' {
				stack = stack[:n-1]
				valueDone()
				continue
			}
			k, _ := tok.(string)
			lower := strings.ToLower(k)
			if stack[n-1].keys[lower] {
				return fmt.Errorf("duplicate key %q", clip(k, 64))
			}
			stack[n-1].keys[lower] = true
			stack[n-1].wantKey = false
			continue
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, &frame{object: true, wantKey: true, keys: map[string]bool{}})
		case json.Delim('['):
			stack = append(stack, &frame{})
		case json.Delim(']'):
			stack = stack[:len(stack)-1]
			valueDone()
		default:
			valueDone()
		}
	}
	if values == 0 || len(stack) > 0 {
		return errors.New("request body must be a JSON object")
	}
	return nil
}

// checkExtensionLayerOwnership verifies that an extension caller has an extension identity and is not targeting
// another extension or manager through headers, query parameters, or payload fields.
func checkExtensionLayerOwnership(w http.ResponseWriter, r *http.Request, p principal, bodyExt, bodyMgr string) (gradingLayerKey, bool) {
	if p.Extension == "" {
		writeErrCode(w, http.StatusForbidden, codeLayerOwnershipDenied, errors.New("extension identity is required"))
		return gradingLayerKey{}, false
	}
	ownMgr := normalizeGradingManager(p.Manager)
	q := r.URL.Query()
	hdrMgr := strings.TrimSpace(r.Header.Get(ManagerHeader))
	qMgr := strings.TrimSpace(q.Get(ManagerQuery))
	bMgr := strings.TrimSpace(bodyMgr)
	if (hdrMgr != "" && hdrMgr != ownMgr) || (qMgr != "" && qMgr != ownMgr) || (bMgr != "" && bMgr != ownMgr) {
		writeErrCode(w, http.StatusForbidden, codeOtherManager, errOtherManager)
		return gradingLayerKey{}, false
	}
	qExt := strings.TrimSpace(q.Get("extension"))
	if qExt != "" && qExt != p.Extension {
		writeErrCode(w, http.StatusForbidden, codeLayerOwnershipDenied, errors.New("an extension may access only its own push grading layer"))
		return gradingLayerKey{}, false
	}
	if bExt := strings.TrimSpace(bodyExt); bExt != "" && bExt != p.Extension {
		writeErrCode(w, http.StatusForbidden, codeLayerOwnershipDenied, errors.New("an extension may modify only its own push grading layer"))
		return gradingLayerKey{}, false
	}
	return gradingLayerKey{Manager: ownMgr, Extension: p.Extension}, true
}

func isFullCredential(p principal) bool {
	return p.Role == "" || p.Role == roleFull
}

// pushGradingLayerRoutes registers GET/PUT/DELETE /api/v1/push/grading/layer and POST /api/v1/push/grading/preview.
// Extension tokens reach them only with the push.grading scope (extwire/push_grading_scopes.go).
func (h *Hub) pushGradingLayerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/push/grading/layer", func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r.Context())
		if p.Role == roleExtension {
			key, ok := checkExtensionLayerOwnership(w, r, p, "", "")
			if !ok {
				return
			}
			var layer *PushGradingLayer
			if l, found := h.push.gradingLayer(key); found {
				layer = &l
			}
			writeJSON(w, map[string]any{
				"layer":          layer,
				"owner_enabled":  h.push.pushGradingOn(),
				"attention_mode": h.push.attentionNow().Mode,
			})
			return
		}
		if !isFullCredential(p) {
			writeErr(w, http.StatusForbidden, errors.New("this credential cannot read push grading layers"))
			return
		}
		q := r.URL.Query()
		qExt := strings.TrimSpace(q.Get("extension"))
		qMgr := namedManager(r)
		if qExt != "" {
			var layer *PushGradingLayer
			if l, found := h.push.findGradingLayerByExtension(qMgr, qExt); found {
				layer = &l
			}
			writeJSON(w, map[string]any{
				"layer":          layer,
				"owner_enabled":  h.push.pushGradingOn(),
				"attention_mode": h.push.attentionNow().Mode,
			})
			return
		}
		writeJSON(w, map[string]any{
			"layers":         h.push.listGradingLayers(qMgr),
			"owner_enabled":  h.push.pushGradingOn(),
			"attention_mode": h.push.attentionNow().Mode,
		})
	})

	api.HandleFunc("PUT /api/v1/push/grading/layer", func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r.Context())
		if p.Role != roleExtension {
			writeErr(w, http.StatusForbidden, errors.New("push grading layers belong to extensions; use PUT /api/v1/push/grading for owner settings"))
			return
		}
		var in pushGradingLayerInput
		if err := decodeStrictJSON(w, r, &in, pushGradingLayerBodyLimit); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		key, ok := checkExtensionLayerOwnership(w, r, p, in.Extension, in.Manager)
		if !ok {
			return
		}
		layer, err := in.buildLayer(key.Manager, key.Extension, h.push.now())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		saved, err := h.push.putGradingLayer(r.Context(), layer)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(w, map[string]any{
			"layer":          saved,
			"owner_enabled":  h.push.pushGradingOn(),
			"attention_mode": h.push.attentionNow().Mode,
		})
	})

	api.HandleFunc("DELETE /api/v1/push/grading/layer", func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r.Context())
		if p.Role == roleExtension {
			key, ok := checkExtensionLayerOwnership(w, r, p, "", "")
			if !ok {
				return
			}
			removed, err := h.push.deleteGradingLayers(r.Context(), func(k gradingLayerKey) bool { return k == key })
			if err != nil {
				writeErr(w, http.StatusServiceUnavailable, err)
				return
			}
			writeJSON(w, map[string]any{
				"ok":             true,
				"removed":        removed > 0,
				"owner_enabled":  h.push.pushGradingOn(),
				"attention_mode": h.push.attentionNow().Mode,
			})
			return
		}
		if !isFullCredential(p) {
			writeErr(w, http.StatusForbidden, errors.New("this credential cannot remove push grading layers"))
			return
		}
		q := r.URL.Query()
		qExt := strings.TrimSpace(q.Get("extension"))
		qMgr := namedManager(r)
		removed, err := h.push.deleteGradingLayers(r.Context(), func(k gradingLayerKey) bool {
			if qMgr != "" && k.Manager != qMgr {
				return false
			}
			if qExt != "" && k.Extension != qExt {
				return false
			}
			return true
		})
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		writeJSON(w, map[string]any{
			"ok":             true,
			"removed":        removed > 0,
			"removed_count":  removed,
			"owner_enabled":  h.push.pushGradingOn(),
			"attention_mode": h.push.attentionNow().Mode,
		})
	})

	api.HandleFunc("POST /api/v1/push/grading/preview", func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r.Context())
		if p.Role != roleExtension && !isFullCredential(p) {
			writeErr(w, http.StatusForbidden, errors.New("this credential cannot preview push grading"))
			return
		}
		var req pushGradingPreviewRequest
		if err := decodeStrictJSON(w, r, &req, pushGradingLayerBodyLimit); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if err := req.Notification.validate(); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		var draft *PushGradingLayer
		mgr := normalizeGradingManager(cmp.Or(req.Notification.Manager, namedManager(r)))
		if p.Role == roleExtension {
			draftExt, draftMgr := "", ""
			if req.Layer != nil {
				draftExt, draftMgr = req.Layer.Extension, req.Layer.Manager
			}
			key, ok := checkExtensionLayerOwnership(w, r, p, draftExt, draftMgr)
			if !ok {
				return
			}
			if req.Notification.Manager != "" && req.Notification.Manager != key.Manager {
				writeErrCode(w, http.StatusForbidden, codeOtherManager, errOtherManager)
				return
			}
			mgr = key.Manager
			if req.Layer != nil {
				l, err := req.Layer.buildLayer(key.Manager, key.Extension, h.push.now())
				if err != nil {
					writeErr(w, http.StatusBadRequest, err)
					return
				}
				draft = &l
			}
		} else if req.Layer != nil {
			lExt := cmp.Or(strings.TrimSpace(req.Layer.Extension), "draft")
			lMgr := cmp.Or(strings.TrimSpace(req.Layer.Manager), mgr)
			l, err := req.Layer.buildLayer(lMgr, lExt, h.push.now())
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			draft = &l
		}

		kind := req.Notification.Kind
		if kind == "" {
			kind = assistantwire.NotifyPush
		}
		derived := levelFor(kind, req.Notification.Level)
		if kind == kindAlert && req.Notification.Level == "" {
			derived = assistantwire.LevelP1
		}
		done := req.Notification.Done || kind == assistantwire.NotifyDone
		mainConvPromote := kind == assistantwire.NotifyPush && req.Notification.Level == "" &&
			req.Notification.SessionID != "" && h.isMainOn(req.Notification.MachineID, req.Notification.SessionID)
		policy := pushPolicy{
			kind:       kind,
			level:      derived,
			relatedKey: req.Notification.RelatedKey,
			todoID:     req.Notification.TodoID,
			machineID:  req.Notification.MachineID,
			sessionID:  req.Notification.SessionID,
			title:      req.Notification.Title,
			body:       req.Notification.Body,
			manager:    mgr,
			extension:  req.Notification.Extension,
			depth:      req.Notification.Depth,
			track:      req.Notification.Track,
			done:       done,
			system:     req.Notification.System,
		}
		decision := h.push.previewPushGrading(policy, draft, mainConvPromote)
		writeJSON(w, decision)
	})
}
