package externalbinding

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Provider names a source protocol. The zero Provider is no provider.
type Provider struct{ provider string }

// ParseProvider gives built-in aliases and custom providers one spelling.
func ParseProvider(raw string) (out outcome.Of[Provider, Fail]) {
	name := strings.ToLower(strings.TrimSpace(raw))
	switch name {
	case "feishu":
		name = "lark"
	case "email":
		name = "mail"
	}
	if !validName(name) {
		return out.Err(wrong("provider.invalid", "provider"))
	}
	return out.Ok(Provider{provider: name})
}

// IsGitHub reports whether a provider uses GitHub repository identities.
func (p Provider) IsGitHub() bool {
	if p == (Provider{}) {
		defect.Raise("externalbinding.Provider.IsGitHub", "the zero Provider")
	}
	return p.provider == "github"
}

// Wire is the canonical provider name. Adapters only.
func (p Provider) Wire() string { return p.provider }

// Channel is a provider's account or connection. The zero Channel is the provider's default channel.
type Channel struct{ channel string }

// ParseChannel validates an optional channel identifier.
func ParseChannel(raw string) (out outcome.Of[Channel, Fail]) {
	id := strings.TrimSpace(raw)
	if id != "" && !validText(id, 512) {
		return out.Err(wrong("channel.invalid", "channel"))
	}
	return out.Ok(Channel{channel: id})
}

// Wire is the canonical channel name. Adapters only.
func (c Channel) Wire() string { return c.channel }

// Kind names the kind of object in a path segment. The zero Kind is no kind.
type Kind struct{ kind string }

// Wire is the canonical kind name. Adapters only.
func (k Kind) Wire() string { return k.kind }

// ExternalID is an opaque identifier within its provider and kind. The zero ExternalID is no identifier.
type ExternalID struct{ external string }

// Wire is the identifier as the wire carries it. Adapters only.
func (x ExternalID) Wire() string { return x.external }

// Segment is one container or item in a reference. The zero Segment is no segment.
type Segment struct {
	kind     Kind
	external ExternalID
}

// SegmentVisitor is told the kind and identifier of a path segment.
type SegmentVisitor interface {
	VisitSegment(Kind, ExternalID)
}

// Accept tells v the kind and identifier.
func (s Segment) Accept(v SegmentVisitor) {
	s.known()
	v.VisitSegment(s.kind, s.external)
}

func (s Segment) known() {
	if s == (Segment{}) {
		defect.Raise("externalbinding.Segment", "the zero Segment")
	}
}

// ParseSegment validates a kind and its opaque identifier.
func ParseSegment(kind, id string) (out outcome.Of[Segment, Fail]) {
	kind, id = strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(id)
	switch {
	case !validName(kind):
		return out.Err(wrong("segment.kind.invalid", "kind"))
	case !validText(id, 512):
		return out.Err(wrong("segment.identifier.invalid", "identifier"))
	}
	return out.Ok(Segment{kind: Kind{kind: kind}, external: ExternalID{external: id}})
}

// ExternalRef is the canonical identity of a channel, container or item. The zero ExternalRef is no reference.
type ExternalRef struct {
	provider  Provider
	channel   Channel
	container opt.Option[Segment]
	item      opt.Option[Segment]
}

// NewExternalRef validates the hierarchy for the provider.
func NewExternalRef(provider Provider, channel Channel, container, item opt.Option[Segment]) (out outcome.Of[ExternalRef, Fail]) {
	if provider == (Provider{}) {
		return out.Err(wrong("provider.invalid", "provider"))
	}
	containerKind, itemKind := segmentKind(container), segmentKind(item)
	switch provider.provider {
	case "mail":
		if channel == (Channel{}) {
			return out.Err(wrong("mail.account.required", "channel"))
		}
		if containerKind != "" && containerKind != "mailbox" && containerKind != "label" {
			return out.Err(wrong("container.kind.invalid", "container"))
		}
	case "lark":
		if containerKind != "" && containerKind != "chat" {
			return out.Err(wrong("container.kind.invalid", "container"))
		}
	case "github":
		if containerKind != "" && containerKind != "repository" {
			return out.Err(wrong("container.kind.invalid", "container"))
		}
	}
	if itemKind != "" {
		switch {
		case containerKind == "" && provider.provider != "mail":
			return out.Err(wrong("container.required", "container"))
		case (provider.provider == "mail" || provider.provider == "lark") && itemKind != "thread":
			return out.Err(wrong("item.kind.invalid", "item"))
		case provider.IsGitHub() && itemKind != "issue" && itemKind != "pull_request":
			return out.Err(wrong("item.kind.invalid", "item"))
		}
	}
	return out.Ok(ExternalRef{provider: provider, channel: channel, container: container, item: item})
}

func segmentKind(segment opt.Option[Segment]) string {
	return opt.Fold(segment, func(s Segment) string {
		s.known()
		return s.kind.kind
	}, func() string { return "" })
}

// ExternalRefVisitor is told the pieces of one canonical source reference.
type ExternalRefVisitor interface {
	VisitExternalRef(Provider, Channel, opt.Option[Segment], opt.Option[Segment])
}

// Accept tells v this reference's channel and optional path segments.
func (r ExternalRef) Accept(v ExternalRefVisitor) {
	r.known()
	v.VisitExternalRef(r.provider, r.channel, r.container, r.item)
}

func (r ExternalRef) known() {
	if r == (ExternalRef{}) {
		defect.Raise("externalbinding.ExternalRef", "the zero ExternalRef")
	}
}

// RefPath describes a reference and its parents. The zero RefPath is no path.
type RefPath struct{ refPath ExternalRef }

// Path returns the reference's lookup path.
func Path(ref ExternalRef) RefPath {
	ref.known()
	return RefPath{refPath: ref}
}

// RefPathVisitor is told each reference and its specificity.
type RefPathVisitor interface {
	VisitRef(ExternalRef, Scope)
}

// Accept tells v the references from the most specific node to the channel.
func (p RefPath) Accept(v RefPathVisitor) {
	ref := p.refPath
	ref.known()
	if !ref.item.IsNone() {
		v.VisitRef(ref, ScopeItem())
		ref.item = opt.None[Segment]()
	}
	if !ref.container.IsNone() {
		v.VisitRef(ref, ScopeContainer())
		ref.container = opt.None[Segment]()
	}
	v.VisitRef(ref, ScopeChannel())
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func validText(s string, maxBytes int) bool {
	return s != "" && len(s) <= maxBytes && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
