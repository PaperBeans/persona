package externalbinding

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Mode is how a source may forward. The zero Mode is no mode.
type Mode struct{ mode string }

func ModeOff() Mode      { return Mode{mode: "off"} }
func ModeRealtime() Mode { return Mode{mode: "realtime"} }
func ModeDigest() Mode   { return Mode{mode: "digest"} }
func ModeOnDemand() Mode { return Mode{mode: "on_demand"} }

// ParseMode recognizes a forwarding mode.
func ParseMode(raw string) (out outcome.Of[Mode, Fail]) {
	switch raw {
	case "off":
		return out.Ok(ModeOff())
	case "realtime":
		return out.Ok(ModeRealtime())
	case "digest":
		return out.Ok(ModeDigest())
	case "on_demand":
		return out.Ok(ModeOnDemand())
	default:
		return out.Err(wrong("mode.invalid", "mode"))
	}
}

// Wire is the mode as the wire carries it. Adapters only.
func (m Mode) Wire() string { return m.mode }

// ActionRole is the responsibility allowed by a binding. The zero ActionRole is no role.
type ActionRole struct{ actionRole string }

func ActionRoleOwner() ActionRole { return ActionRole{actionRole: "owner"} }
func ActionRoleFYI() ActionRole   { return ActionRole{actionRole: "fyi"} }

// ParseActionRole recognizes a source's role.
func ParseActionRole(raw string) (out outcome.Of[ActionRole, Fail]) {
	switch raw {
	case "owner":
		return out.Ok(ActionRoleOwner())
	case "fyi":
		return out.Ok(ActionRoleFYI())
	default:
		return out.Err(wrong("action_role.invalid", "actionRole"))
	}
}

// Wire is the action role as the wire carries it. Adapters only.
func (r ActionRole) Wire() string { return r.actionRole }

// NewItemPolicy controls what happens to unmatched work. The zero NewItemPolicy is no instruction.
type NewItemPolicy struct{ newItemPolicy string }

func NewItemPolicyAskOwner() NewItemPolicy { return NewItemPolicy{newItemPolicy: "ask_owner"} }
func NewItemPolicyAutoTodo() NewItemPolicy { return NewItemPolicy{newItemPolicy: "auto_todo"} }

// ParseNewItemPolicy recognizes an instruction for new items.
func ParseNewItemPolicy(raw string) (out outcome.Of[NewItemPolicy, Fail]) {
	switch raw {
	case "ask_owner":
		return out.Ok(NewItemPolicyAskOwner())
	case "auto_todo":
		return out.Ok(NewItemPolicyAutoTodo())
	default:
		return out.Err(wrong("new_item_policy.invalid", "newItemPolicy"))
	}
}

// Wire is the new-item policy as the wire carries it. Adapters only.
func (p NewItemPolicy) Wire() string { return p.newItemPolicy }

// DigestInterval is the time between digests in minutes. The zero DigestInterval is no interval.
type DigestInterval struct{ digestInterval int }

// ParseDigestInterval accepts between one minute and one year.
func ParseDigestInterval(minutes int) (out outcome.Of[DigestInterval, Fail]) {
	if minutes < 1 || minutes > 365*24*60 {
		return out.Err(wrong("digest_interval.invalid", "digestInterval"))
	}
	return out.Ok(DigestInterval{digestInterval: minutes})
}

// Wire is the interval in minutes. Adapters only.
func (i DigestInterval) Wire() int { return i.digestInterval }

// Forwarding sets a mode and digest interval independently. The zero Forwarding is an override that inherits both.
type Forwarding struct {
	mode           opt.Option[Mode]
	digestInterval opt.Option[DigestInterval]
}

// NewForwarding sets the mode, interval or both. An interval may accompany digest mode or an inherited mode.
func NewForwarding(mode opt.Option[Mode], interval opt.Option[DigestInterval]) (out outcome.Of[Forwarding, Fail]) {
	if opt.Fold(mode, func(m Mode) bool { return m == (Mode{}) }, func() bool { return false }) {
		return out.Err(wrong("mode.invalid", "mode"))
	}
	if opt.Fold(interval, func(i DigestInterval) bool { return i == (DigestInterval{}) }, func() bool { return false }) {
		return out.Err(wrong("digest_interval.invalid", "digestInterval"))
	}
	if !interval.IsNone() && !opt.Fold(mode, func(m Mode) bool { return m == ModeDigest() }, func() bool { return true }) {
		return out.Err(wrong("digest_interval.requires_digest", "digestInterval"))
	}
	return out.Ok(Forwarding{mode: mode, digestInterval: interval})
}

// FilterTerm is a keyword, mention or sender. The zero FilterTerm is empty and cannot match.
type FilterTerm struct{ filterTerm string }

// ParseFilterTerm validates one filter term.
func ParseFilterTerm(raw string) (out outcome.Of[FilterTerm, Fail]) {
	text := strings.TrimSpace(raw)
	if !validText(text, 256) {
		return out.Err(wrong("filter.term.invalid", "preFilter"))
	}
	return out.Ok(FilterTerm{filterTerm: text})
}

// Wire is the term as the wire carries it. Adapters only.
func (t FilterTerm) Wire() string { return t.filterTerm }

// FilterTerms is one filter's alternatives. The zero FilterTerms is empty.
type FilterTerms struct{ filterTerms []FilterTerm }

// NewFilterTerms bounds a filter and keeps each term once in canonical order.
func NewFilterTerms(terms []FilterTerm) (out outcome.Of[FilterTerms, Fail]) {
	if len(terms) > 32 {
		return out.Err(wrong("filter.terms.limit", "preFilter"))
	}
	for _, term := range terms {
		if term == (FilterTerm{}) {
			return out.Err(wrong("filter.term.invalid", "preFilter"))
		}
	}
	terms = slices.Clone(terms)
	slices.SortFunc(terms, func(a, b FilterTerm) int { return cmp.Compare(a.filterTerm, b.filterTerm) })
	return out.Ok(FilterTerms{filterTerms: slices.Compact(terms)})
}

// FilterTermsVisitor is told the filter's alternatives.
type FilterTermsVisitor interface {
	VisitFilterTerm(FilterTerm)
}

// Accept tells v each term in order.
func (t FilterTerms) Accept(v FilterTermsVisitor) {
	for _, term := range t.filterTerms {
		v.VisitFilterTerm(term)
	}
}

// PreFilter holds the alternatives for each type of condition. The zero PreFilter is an empty filter.
type PreFilter struct {
	keywords FilterTerms
	mentions FilterTerms
	senders  FilterTerms
}

// NewPreFilter defines the conditions that may admit an item before routing.
func NewPreFilter(keywords, mentions, senders FilterTerms) PreFilter {
	return PreFilter{keywords: keywords, mentions: mentions, senders: senders}
}

// PreFilterVisitor is told the filter's three lists.
type PreFilterVisitor interface {
	VisitPreFilter(keywords, mentions, senders FilterTerms)
}

// Accept tells v the filter's three lists.
func (f PreFilter) Accept(v PreFilterVisitor) {
	v.VisitPreFilter(f.keywords, f.mentions, f.senders)
}

// Policy supplies the overrides at one scope. The zero Policy is an override that inherits every field.
type Policy struct {
	forwarding    Forwarding
	actionRole    opt.Option[ActionRole]
	newItemPolicy opt.Option[NewItemPolicy]
	preFilter     opt.Option[PreFilter]
}

// NewPolicy creates a sparse policy from forwarding, responsibility, new-item behavior and filtering.
func NewPolicy(forwarding Forwarding, role opt.Option[ActionRole], handling opt.Option[NewItemPolicy], filter opt.Option[PreFilter]) (out outcome.Of[Policy, Fail]) {
	if opt.Fold(role, func(r ActionRole) bool { return r == (ActionRole{}) }, func() bool { return false }) {
		return out.Err(wrong("action_role.invalid", "actionRole"))
	}
	if opt.Fold(handling, func(p NewItemPolicy) bool { return p == (NewItemPolicy{}) }, func() bool { return false }) {
		return out.Err(wrong("new_item_policy.invalid", "newItemPolicy"))
	}
	return out.Ok(Policy{forwarding: forwarding, actionRole: role, newItemPolicy: handling, preFilter: filter})
}

// PolicyVisitor is told the fields supplied by one sparse policy.
type PolicyVisitor interface {
	VisitForwarding(opt.Option[Mode], opt.Option[DigestInterval])
	VisitHandling(opt.Option[ActionRole], opt.Option[NewItemPolicy], opt.Option[PreFilter])
}

// Accept tells v the fields supplied by this policy.
func (p Policy) Accept(v PolicyVisitor) {
	v.VisitForwarding(p.forwarding.mode, p.forwarding.digestInterval)
	v.VisitHandling(p.actionRole, p.newItemPolicy, p.preFilter)
}

// Scope is one level of a reference path or the origin of a resolved field. The zero Scope is no level.
type Scope struct{ scope string }

func ScopeChannel() Scope   { return Scope{scope: "channel"} }
func ScopeContainer() Scope { return Scope{scope: "container"} }
func ScopeItem() Scope      { return Scope{scope: "item"} }

func defaultScope() Scope { return Scope{scope: "default"} }
func bindingScope() Scope { return Scope{scope: "binding"} }

// Wire is the scope as the wire carries it. Adapters only.
func (s Scope) Wire() string { return s.scope }

// BindingPolicy applies a binding's override at the binding's own level in the path. The zero BindingPolicy is
// no binding override.
type BindingPolicy struct {
	scope  Scope
	policy Policy
}

// NewBindingPolicy marks the level at which a selected binding may override defaults.
func NewBindingPolicy(scope Scope, policy Policy) BindingPolicy {
	switch scope {
	case ScopeChannel(), ScopeContainer(), ScopeItem():
		return BindingPolicy{scope: scope, policy: policy}
	default:
		defect.Raise("externalbinding.NewBindingPolicy", "the binding needs a path level")
		return BindingPolicy{}
	}
}

type provenance struct {
	mode           Scope
	digestInterval opt.Option[Scope]
	actionRole     Scope
	newItemPolicy  Scope
	preFilter      Scope
}

// Resolution is an effective policy and the source of each field. The zero Resolution is no resolution.
type Resolution struct {
	policy     Policy
	provenance provenance
}

// ResolutionVisitor is told the resolved policy and the sources of its fields.
type ResolutionVisitor interface {
	VisitResolvedPolicy(Policy)
	VisitForwardingSources(mode Scope, interval opt.Option[Scope])
	VisitHandlingSources(role, newItem, filter Scope)
}

// Accept tells v the resolved policy and its provenance.
func (r Resolution) Accept(v ResolutionVisitor) {
	if r.provenance.mode == (Scope{}) {
		defect.Raise("externalbinding.Resolution.Accept", "the zero Resolution")
	}
	v.VisitResolvedPolicy(r.policy)
	s := r.provenance
	v.VisitForwardingSources(s.mode, s.digestInterval)
	v.VisitHandlingSources(s.actionRole, s.newItemPolicy, s.preFilter)
}

// Resolve applies built-in defaults, then channel, container and item overrides. A selected binding wins over
// the node at its level. More specific nodes can still override it.
func Resolve(channel, container, item Policy, binding opt.Option[BindingPolicy]) Resolution {
	def := defaultScope()
	result := Resolution{
		policy: Policy{
			forwarding:    Forwarding{mode: opt.Some(ModeOff())},
			actionRole:    opt.Some(ActionRoleFYI()),
			newItemPolicy: opt.Some(NewItemPolicyAskOwner()),
			preFilter:     opt.Some(PreFilter{}),
		},
		provenance: provenance{mode: def, actionRole: def, newItemPolicy: def, preFilter: def},
	}
	b := opt.Fold(binding, func(b BindingPolicy) BindingPolicy {
		if b.scope == (Scope{}) {
			defect.Raise("externalbinding.Resolve", "the zero BindingPolicy")
		}
		return b
	}, func() BindingPolicy { return BindingPolicy{} })
	result = result.layer(channel, ScopeChannel(), b)
	result = result.layer(container, ScopeContainer(), b)
	result = result.layer(item, ScopeItem(), b)
	if !opt.Fold(result.policy.forwarding.mode, func(m Mode) bool { return m == ModeDigest() }, func() bool { return false }) {
		result.policy.forwarding.digestInterval = opt.None[DigestInterval]()
		result.provenance.digestInterval = opt.None[Scope]()
	}
	return result
}

func (r Resolution) layer(policy Policy, scope Scope, binding BindingPolicy) Resolution {
	r = r.overlay(policy, scope)
	if binding.scope == scope {
		r = r.overlay(binding.policy, bindingScope())
	}
	return r
}

func (r Resolution) overlay(p Policy, scope Scope) Resolution {
	if !p.forwarding.mode.IsNone() {
		r.policy.forwarding.mode = p.forwarding.mode
		r.provenance.mode = scope
	}
	if !p.forwarding.digestInterval.IsNone() {
		r.policy.forwarding.digestInterval = p.forwarding.digestInterval
		r.provenance.digestInterval = opt.Some(scope)
	}
	if !p.actionRole.IsNone() {
		r.policy.actionRole = p.actionRole
		r.provenance.actionRole = scope
	}
	if !p.newItemPolicy.IsNone() {
		r.policy.newItemPolicy = p.newItemPolicy
		r.provenance.newItemPolicy = scope
	}
	if !p.preFilter.IsNone() {
		r.policy.preFilter = p.preFilter
		r.provenance.preFilter = scope
	}
	return r
}
