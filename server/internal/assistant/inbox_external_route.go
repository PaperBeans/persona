package assistant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	w "github.com/nwverse/persona/server/internal/assistantwire"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
)

const externalDigestDefault = 60 * time.Minute

type externalTarget = w.ExternalInboxTarget
type externalDelivery = w.ExternalInboxDelivery
type externalPlan = w.ExternalInboxPlan

// This is the complete candidate record sent to the triage model.
type externalCandidateBrief struct {
	externalTarget
	Title  string `json:"title"`
	Scope  string `json:"scope"`
	Status string `json:"status"`
}

type externalCandidate struct {
	externalCandidateBrief
	policy   w.ExternalPolicy
	owner    bool
	delivery bool
	at       time.Time
}

type externalSelection struct {
	scope      w.ExternalRef
	candidates []externalCandidate
	readyAt    time.Time
	unmatched  bool
}

type externalTriageInput struct {
	Candidates []externalCandidateBrief `json:"candidates"`
	Summaries  []string                 `json:"summaries"`
}

type externalVerdict struct {
	Disposition string           `json:"disposition"`
	Owner       *externalTarget  `json:"action_owner,omitempty"`
	FYI         []externalTarget `json:"fyi,omitempty"`
}

type externalInboxTriager func(context.Context, externalTriageInput) ([]externalVerdict, error)

// selectExternalCandidates chooses the first scope with a live, eligible
// candidate. Within a scope the explicit binding supplies its policy and
// a projected reference link remains read-only.
func selectExternalCandidates(work w.ExternalInboxWork, config w.ExternalRoutingConfiguration,
	brief func(externalTarget) (externalCandidateBrief, bool)) (externalSelection, error) {
	var result externalSelection
	if len(work.Path) == 0 {
		return result, errors.New("external Inbox event has no reference path")
	}
	explicit := map[w.ExternalBindingID]*w.ExternalBinding{}
	for i := range config.Bindings {
		b := &config.Bindings[i]
		explicit[b.ID] = b
	}
	links := map[externalTarget]w.ExternalRepositoryRole{}
	for _, m := range config.Lookup.Matches {
		if m.Source == w.ExternalBindingRepositoryLink {
			target := externalTarget{ID: m.TargetID, Kind: m.TargetKind}
			if links[target] != "reference" {
				links[target] = m.RepositoryRole
			}
		}
	}
	seen := map[externalTarget]bool{}
	for _, scope := range work.Path {
		for _, m := range config.Lookup.Matches {
			if m.ExternalRef != scope {
				continue
			}
			target := externalTarget{ID: m.TargetID, Kind: m.TargetKind}
			if seen[target] {
				continue
			}
			details, open := brief(target)
			if !open {
				continue
			}
			effective, err := externalbindingadapt.Resolve(work.Path[0], config.Policies, explicit[m.BindingID])
			if err != nil {
				return result, err
			}
			policy := effective.Policy
			at, enabled := externalPolicyTime(work.ReceivedAt, policy)
			if !enabled && effective.Provenance.Mode.Source == w.ExternalPolicyDefault {
				continue // a default-off projection is not an opt-in
			}
			seen[target] = true
			if !enabled || !externalFilterMatches(policy.PreFilter, work.ExternalInboxEvent) {
				continue
			}
			owner := *policy.ActionRole == "owner" && links[target] != "reference"
			result.candidates = append(result.candidates, externalCandidate{
				externalCandidateBrief: details, policy: policy, owner: owner, at: at,
				delivery: owner && links[target] == "delivery",
			})
			if result.readyAt.IsZero() || at.Before(result.readyAt) {
				result.readyAt = at
			}
		}
		if len(result.candidates) > 0 {
			result.scope = scope
			return result, nil
		}
	}
	if len(seen) != 0 {
		return result, nil // a configured route refused the event
	}
	effective, err := externalbindingadapt.Resolve(work.Path[0], config.Policies, nil)
	if err != nil {
		return result, err
	}
	at, enabled := externalPolicyTime(work.ReceivedAt, effective.Policy)
	if enabled && externalFilterMatches(effective.Policy.PreFilter, work.ExternalInboxEvent) {
		result.readyAt, result.unmatched = at, true
	}
	return result, nil
}

func externalPolicyTime(at time.Time, policy w.ExternalPolicy) (time.Time, bool) {
	switch *policy.Mode {
	case "realtime":
		return at, true
	case "digest":
		interval := externalDigestDefault
		if policy.DigestIntervalMinutes != nil {
			interval = time.Duration(*policy.DigestIntervalMinutes) * time.Minute
		}
		return at.Truncate(interval).Add(interval), true
	default:
		return time.Time{}, false
	}
}

func externalFilterMatches(filter *w.ExternalPreFilter, ev w.ExternalInboxEvent) bool {
	if filter == nil {
		return true
	}
	text := strings.ToLower(ev.Title + "\n" + ev.Summary)
	if len(filter.Keywords) > 0 && !slices.ContainsFunc(filter.Keywords, func(keyword string) bool {
		return strings.Contains(text, strings.ToLower(keyword))
	}) {
		return false
	}
	if len(filter.Senders) > 0 && !slices.ContainsFunc(filter.Senders, func(term string) bool {
		return slices.ContainsFunc(ev.Senders, func(sender string) bool { return strings.EqualFold(sender, term) })
	}) {
		return false
	}
	if len(filter.Mentions) == 0 {
		return true
	}
	mentions := strings.FieldsFunc(text, func(r rune) bool {
		return r != '@' && r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return slices.ContainsFunc(filter.Mentions, func(term string) bool {
		term = strings.TrimPrefix(strings.ToLower(term), "@")
		return slices.Contains(mentions, "@"+term) || slices.Contains(ev.Mentions, term)
	})
}

func (c externalCandidate) recipient() externalDelivery {
	return externalDelivery{Target: c.externalTarget, Scope: c.externalTarget}
}

// deterministicExternalPlan handles exact matches and PR role disambiguation
// without invoking a model. The former applies to both work and FYI delivery.
func deterministicExternalPlan(work w.ExternalInboxWork, sel externalSelection) (externalPlan, bool) {
	var plan externalPlan
	if sel.unmatched {
		plan.Ask = true
		return plan, true
	}
	switch len(sel.candidates) {
	case 0:
		return plan, true
	case 1:
		c := sel.candidates[0]
		if c.owner {
			plan.Owner = new(c.recipient())
		} else {
			plan.FYI = append(plan.FYI, c.recipient())
		}
		return plan, true
	}
	if work.Path[0].Provider != "github" || work.Path[0].Item == (w.ExternalRefNode{}) {
		return plan, false
	}
	for _, preferDelivery := range []bool{true, false} {
		var found *externalDelivery
		for _, c := range sel.candidates {
			if !c.owner || c.Kind != w.ExternalTargetTodo || (preferDelivery && !c.delivery) {
				continue
			}
			if found != nil {
				return plan, false
			}
			found = new(c.recipient())
		}
		if found != nil {
			plan.Owner = found
			break
		}
	}
	if plan.Owner == nil && slices.ContainsFunc(sel.candidates, func(c externalCandidate) bool { return c.owner }) {
		return plan, false
	}
	for _, c := range sel.candidates {
		if !c.owner {
			plan.FYI = append(plan.FYI, c.recipient())
		}
	}
	return plan, true
}

func externalPlanFromVerdict(work w.ExternalInboxWork, sel externalSelection, v externalVerdict) (externalPlan, error) {
	var plan externalPlan
	switch v.Disposition {
	case "ignore":
		if v.Owner != nil || len(v.FYI) != 0 {
			return plan, errExternalVerdict
		}
		return plan, nil
	case "new":
		if v.Owner != nil || len(v.FYI) != 0 {
			return plan, errExternalVerdict
		}
		var direction *externalCandidate
		for _, candidate := range sel.candidates {
			if candidate.owner && candidate.Kind == w.ExternalTargetDirection && *candidate.policy.NewItemPolicy == "auto_todo" {
				if direction != nil {
					plan.Ask = true
					return plan, nil
				}
				direction = new(candidate)
			}
		}
		if direction != nil {
			plan.Owner = new(direction.recipient())
			plan.Owner.Target = externalTarget{ID: w.ExternalTargetID("todo_" + work.Key), Kind: w.ExternalTargetTodo}
			plan.Owner.AutoTodo = true
		} else {
			plan.Ask = true
		}
	case "assign":
		if v.Owner == nil && len(v.FYI) == 0 {
			return plan, errExternalVerdict
		}
		find := func(target externalTarget) (externalCandidate, bool) {
			for _, c := range sel.candidates {
				if c.externalTarget == target {
					return c, true
				}
			}
			return externalCandidate{}, false
		}
		if v.Owner != nil {
			c, ok := find(*v.Owner)
			if !ok || !c.owner {
				return plan, errExternalVerdict
			}
			plan.Owner = new(c.recipient())
		}
		seen := map[externalTarget]bool{}
		for _, target := range v.FYI {
			c, ok := find(target)
			if !ok || (v.Owner != nil && target == *v.Owner) {
				return externalPlan{}, errExternalVerdict
			}
			if !seen[target] {
				plan.FYI = append(plan.FYI, c.recipient())
				seen[target] = true
			}
		}
	default:
		return plan, errExternalVerdict
	}
	return plan, nil
}

var errExternalVerdict = errors.New("external Inbox triage returned a decision outside the eligible candidates")
