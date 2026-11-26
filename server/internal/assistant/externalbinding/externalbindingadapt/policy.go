package externalbindingadapt

import (
	"errors"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Policy validates and normalizes an override while retaining every omitted field.
func Policy(in assistantwire.ExternalPolicy) (assistantwire.ExternalPolicy, error) {
	policy, err := policyFromWire(in)
	if err != nil {
		return assistantwire.ExternalPolicy{}, err
	}
	return policyWire(policy), nil
}

func policyFromWire(in assistantwire.ExternalPolicy) (externalbinding.Policy, error) {
	var zero externalbinding.Policy
	mode, err := optionalText(in.Mode, externalbinding.ParseMode)
	if err != nil {
		return zero, err
	}
	interval, err := optional(in.DigestIntervalMinutes, externalbinding.ParseDigestInterval)
	if err != nil {
		return zero, err
	}
	forwarding, err := unpack(externalbinding.NewForwarding(mode, interval))
	if err != nil {
		return zero, err
	}
	role, err := optionalText(in.ActionRole, externalbinding.ParseActionRole)
	if err != nil {
		return zero, err
	}
	handling, err := optionalText(in.NewItemPolicy, externalbinding.ParseNewItemPolicy)
	if err != nil {
		return zero, err
	}
	filter := opt.None[externalbinding.PreFilter]()
	if in.PreFilter != nil {
		value, err := filterFromWire(*in.PreFilter)
		if err != nil {
			return zero, err
		}
		filter = opt.Some(value)
	}
	return unpack(externalbinding.NewPolicy(forwarding, role, handling, filter))
}

func optionalText[W ~string, C any](raw *W, parse func(string) outcome.Of[C, externalbinding.Fail]) (opt.Option[C], error) {
	return optional(raw, func(v W) outcome.Of[C, externalbinding.Fail] { return parse(string(v)) })
}

func optional[W, C any](raw *W, parse func(W) outcome.Of[C, externalbinding.Fail]) (opt.Option[C], error) {
	none := opt.None[C]()
	if raw == nil {
		return none, nil
	}
	v, err := unpack(parse(*raw))
	if err != nil {
		return none, err
	}
	return opt.Some(v), nil
}

func filterFromWire(in assistantwire.ExternalPreFilter) (externalbinding.PreFilter, error) {
	var zero externalbinding.PreFilter
	keywords, err := filterTerms(in.Keywords, true)
	if err != nil {
		return zero, err
	}
	mentions, err := filterTerms(in.Mentions, false)
	if err != nil {
		return zero, err
	}
	senders, err := filterTerms(in.Senders, false)
	if err != nil {
		return zero, err
	}
	return externalbinding.NewPreFilter(keywords, mentions, senders), nil
}

func filterTerms(raw []string, lower bool) (externalbinding.FilterTerms, error) {
	var zero externalbinding.FilterTerms
	terms := make([]externalbinding.FilterTerm, 0, len(raw))
	for _, text := range raw {
		if lower {
			text = strings.ToLower(text)
		}
		term, err := unpack(externalbinding.ParseFilterTerm(text))
		if err != nil {
			return zero, err
		}
		terms = append(terms, term)
	}
	return unpack(externalbinding.NewFilterTerms(terms))
}

type policyReader struct{ policy assistantwire.ExternalPolicy }

func (r *policyReader) VisitForwarding(mode opt.Option[externalbinding.Mode], interval opt.Option[externalbinding.DigestInterval]) {
	r.policy.Mode = wireOption(mode, func(m externalbinding.Mode) assistantwire.ExternalForwardMode {
		return assistantwire.ExternalForwardMode(m.Wire())
	})
	r.policy.DigestIntervalMinutes = wireOption(interval, externalbinding.DigestInterval.Wire)
}

func (r *policyReader) VisitHandling(role opt.Option[externalbinding.ActionRole], newItem opt.Option[externalbinding.NewItemPolicy], filter opt.Option[externalbinding.PreFilter]) {
	r.policy.ActionRole = wireOption(role, func(r externalbinding.ActionRole) assistantwire.ExternalActionRole {
		return assistantwire.ExternalActionRole(r.Wire())
	})
	r.policy.NewItemPolicy = wireOption(newItem, func(n externalbinding.NewItemPolicy) assistantwire.ExternalNewItemPolicy {
		return assistantwire.ExternalNewItemPolicy(n.Wire())
	})
	r.policy.PreFilter = wireOption(filter, filterWire)
}

func wireOption[C, W any](value opt.Option[C], encode func(C) W) *W {
	return opt.Fold(value, func(v C) *W { return new(encode(v)) }, func() *W { return nil })
}

func policyWire(policy externalbinding.Policy) assistantwire.ExternalPolicy {
	var r policyReader
	policy.Accept(&r)
	return r.policy
}

type termsReader struct{ terms []string }

func (r *termsReader) VisitFilterTerm(term externalbinding.FilterTerm) {
	r.terms = append(r.terms, term.Wire())
}

type filterReader struct {
	filter assistantwire.ExternalPreFilter
}

func (r *filterReader) VisitPreFilter(keywords, mentions, senders externalbinding.FilterTerms) {
	var k, m, s termsReader
	keywords.Accept(&k)
	mentions.Accept(&m)
	senders.Accept(&s)
	r.filter = assistantwire.ExternalPreFilter{Keywords: k.terms, Mentions: m.terms, Senders: s.terms}
}

func filterWire(f externalbinding.PreFilter) assistantwire.ExternalPreFilter {
	var r filterReader
	f.Accept(&r)
	return r.filter
}

// Resolve calculates the policy for a reference and, optionally, one binding on its path.
func Resolve(in assistantwire.ExternalRef, stored []assistantwire.ExternalScopedPolicy, binding *assistantwire.ExternalBinding) (assistantwire.ExternalEffectivePolicy, error) {
	var zero assistantwire.ExternalEffectivePolicy
	entries, err := path(in)
	if err != nil {
		return zero, err
	}
	sources := make(map[externalbinding.Scope]assistantwire.ExternalRef, len(entries))
	policies := make(map[externalbinding.Scope]externalbinding.Policy, len(entries))
	bound := opt.None[externalbinding.BindingPolicy]()
	for _, entry := range entries {
		sources[entry.scope] = entry.ref
		for _, row := range stored {
			if row.ExternalRef == entry.ref {
				policy, err := policyFromWire(row.Policy)
				if err != nil {
					return zero, err
				}
				policies[entry.scope] = policy
				break
			}
		}
		if binding != nil && binding.ExternalRef == entry.ref {
			policy, err := policyFromWire(binding.Policy)
			if err != nil {
				return zero, err
			}
			bound = opt.Some(externalbinding.NewBindingPolicy(entry.scope, policy))
		}
	}
	if binding != nil && bound.IsNone() {
		return zero, errors.New("binding does not match the external reference path")
	}
	r := resolutionReader{result: assistantwire.ExternalEffectivePolicy{ExternalRef: entries[0].ref}, sources: sources, binding: binding}
	resolution := externalbinding.Resolve(policies[externalbinding.ScopeChannel()], policies[externalbinding.ScopeContainer()], policies[externalbinding.ScopeItem()], bound)
	resolution.Accept(&r)
	return r.result, nil
}

type resolutionReader struct {
	result  assistantwire.ExternalEffectivePolicy
	sources map[externalbinding.Scope]assistantwire.ExternalRef
	binding *assistantwire.ExternalBinding
}

func (r *resolutionReader) VisitResolvedPolicy(policy externalbinding.Policy) {
	r.result.Policy = policyWire(policy)
}

func (r *resolutionReader) VisitForwardingSources(mode externalbinding.Scope, interval opt.Option[externalbinding.Scope]) {
	r.result.Provenance.Mode = r.origin(mode)
	r.result.Provenance.DigestIntervalMinutes = wireOption(interval, r.origin)
}

func (r *resolutionReader) VisitHandlingSources(role, newItem, filter externalbinding.Scope) {
	r.result.Provenance.ActionRole = r.origin(role)
	r.result.Provenance.NewItemPolicy = r.origin(newItem)
	r.result.Provenance.PreFilter = r.origin(filter)
}

func (r *resolutionReader) origin(scope externalbinding.Scope) assistantwire.ExternalPolicyOrigin {
	source := assistantwire.ExternalPolicySource(scope.Wire())
	switch source {
	case assistantwire.ExternalPolicyDefault:
		return assistantwire.ExternalPolicyOrigin{Source: source}
	case assistantwire.ExternalPolicyBinding:
		return assistantwire.ExternalPolicyOrigin{Source: source, ExternalRef: &r.binding.ExternalRef, BindingID: r.binding.ID}
	default:
		ref := r.sources[scope]
		return assistantwire.ExternalPolicyOrigin{Source: source, ExternalRef: &ref}
	}
}
