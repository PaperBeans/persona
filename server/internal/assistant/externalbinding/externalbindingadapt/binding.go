package externalbindingadapt

import (
	"fmt"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// TargetKind normalizes a target kind selector.
func TargetKind(raw assistantwire.ExternalTargetKind) (assistantwire.ExternalTargetKind, error) {
	kind, err := unpack(externalbinding.ParseTargetKind(string(raw)))
	return assistantwire.ExternalTargetKind(kind.Wire()), err
}

// TargetID normalizes a target identifier selector.
func TargetID(raw assistantwire.ExternalTargetID) (assistantwire.ExternalTargetID, error) {
	id, err := unpack(externalbinding.ParseTargetID(string(raw)))
	return assistantwire.ExternalTargetID(id.Wire()), err
}

// Binding normalizes an explicit binding and its optional overrides.
func Binding(in assistantwire.ExternalBindingInput) (assistantwire.ExternalBindingInput, error) {
	var zero assistantwire.ExternalBindingInput
	ref, err := refFromWire(in.ExternalRef)
	if err != nil {
		return zero, err
	}
	kind, err := unpack(externalbinding.ParseTargetKind(string(in.TargetKind)))
	if err != nil {
		return zero, err
	}
	id, err := unpack(externalbinding.ParseTargetID(string(in.TargetID)))
	if err != nil {
		return zero, err
	}
	target, err := unpack(externalbinding.NewTarget(kind, id))
	if err != nil {
		return zero, err
	}
	policy, err := policyFromWire(in.Policy)
	if err != nil {
		return zero, err
	}
	b, err := unpack(externalbinding.NewBinding(ref, target, policy))
	if err != nil {
		return zero, err
	}
	var r bindingReader
	b.Accept(&r)
	return r.binding, nil
}

type bindingReader struct {
	binding assistantwire.ExternalBindingInput
}

func (r *bindingReader) VisitBinding(ref externalbinding.ExternalRef, kind externalbinding.TargetKind, id externalbinding.TargetID, p externalbinding.Policy) {
	r.binding = assistantwire.ExternalBindingInput{
		ExternalRef: refWire(ref), TargetKind: assistantwire.ExternalTargetKind(kind.Wire()),
		TargetID: assistantwire.ExternalTargetID(id.Wire()), Policy: policyWire(p),
	}
}

func unpack[T any](result outcome.Of[T, externalbinding.Fail]) (T, error) {
	value, invalid := outcome.Unpack(result)
	if invalid == nil {
		return value, nil
	}
	var r failureReader
	invalid.Accept(&r)
	return value, fmt.Errorf("invalid external source %s", r.part)
}

type failureReader struct{ part string }

func (r *failureReader) VisitInvalid(_ fail.Code, part fail.Part) { r.part = part.Wire() }
func (r *failureReader) VisitRefused(fail.Code)                   {}
func (r *failureReader) VisitMissing(fail.Code)                   {}
func (r *failureReader) VisitConflict(fail.Code)                  {}
