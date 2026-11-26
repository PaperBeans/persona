package externalbinding

import (
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// TargetKind is the kind of Persona work connected to a source. The zero TargetKind is no kind of work.
type TargetKind struct{ targetKind string }

func TargetKindDirection() TargetKind { return TargetKind{targetKind: "direction"} }
func TargetKindTodo() TargetKind      { return TargetKind{targetKind: "todo"} }

// ParseTargetKind accepts directions and to-dos.
func ParseTargetKind(raw string) (out outcome.Of[TargetKind, Fail]) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "todo":
		return out.Ok(TargetKindTodo())
	case "direction":
		return out.Ok(TargetKindDirection())
	default:
		return out.Err(wrong("target.kind.invalid", "targetKind"))
	}
}

// Wire is the target kind as the wire carries it. Adapters only.
func (k TargetKind) Wire() string { return k.targetKind }

// TargetID names a direction or a to-do. The zero TargetID is no identifier.
type TargetID struct{ target string }

// ParseTargetID validates a target identifier.
func ParseTargetID(raw string) (out outcome.Of[TargetID, Fail]) {
	id := strings.TrimSpace(raw)
	if !validText(id, 512) {
		return out.Err(wrong("target.identifier.invalid", "target"))
	}
	return out.Ok(TargetID{target: id})
}

// Wire is the target identifier as the wire carries it. Adapters only.
func (x TargetID) Wire() string { return x.target }

// Target names one piece of Persona work. The zero Target is no target.
type Target struct {
	targetKind TargetKind
	target     TargetID
}

// NewTarget names a piece of work using its kind and identifier.
func NewTarget(kind TargetKind, id TargetID) (out outcome.Of[Target, Fail]) {
	if kind == (TargetKind{}) {
		return out.Err(wrong("target.kind.invalid", "targetKind"))
	}
	if id == (TargetID{}) {
		return out.Err(wrong("target.identifier.invalid", "target"))
	}
	return out.Ok(Target{targetKind: kind, target: id})
}

// Binding connects any external reference to any target, with an optional policy override. The zero Binding is
// no connection.
type Binding struct {
	externalRef ExternalRef
	target      Target
	policy      Policy
}

// NewBinding creates the connection from a validated reference, target and policy.
func NewBinding(ref ExternalRef, target Target, policy Policy) (out outcome.Of[Binding, Fail]) {
	if ref == (ExternalRef{}) {
		return out.Err(wrong("reference.required", "reference"))
	}
	if target == (Target{}) {
		return out.Err(wrong("target.required", "target"))
	}
	return out.Ok(Binding{externalRef: ref, target: target, policy: policy})
}

// BindingVisitor is told the connection's reference, target and override.
type BindingVisitor interface {
	VisitBinding(ExternalRef, TargetKind, TargetID, Policy)
}

// Accept tells v which reference and target are connected and which policy applies.
func (b Binding) Accept(v BindingVisitor) {
	if b.target == (Target{}) {
		defect.Raise("externalbinding.Binding.Accept", "the zero Binding")
	}
	v.VisitBinding(b.externalRef, b.target.targetKind, b.target.target, b.policy)
}
