// Package fail is the vocabulary of expected failures: a core says why it refused, and only by visiting the failure
// can a caller tell which kind it was. Each core seals its own family on these bases (`type Fail interface{
// fail.Fail; <core>Fail() }`); codes cannot be compared, so nothing decides on a code's text (docs/server/
// architecture.md "Domain model", "Failures").
//
//persona:layer kit
package fail

import (
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// Fail is an expected failure, read only through Accept. Only the bases in this package implement it; a core's
// family embeds one of them. The zero Fail is nil, which no operation returns as its failure, and a base's zero value
// is no failure either: its Accept raises a defect, and so does outcome's Err, which visits the failure it is given.
// A core's family type built empty (notFound{} over a fail.Absence) is therefore a defect where it is made.
type Fail interface {
	Accept(v FailVisitor)
	fail()
}

// FailVisitor is told which kind of failure it was handed: invalid input (with the part that was wrong), a refusal
// in the domain's rules, something missing, or a conflict with the state it met.
type FailVisitor interface {
	VisitInvalid(code Code, part Part)
	VisitRefused(code Code)
	VisitMissing(code Code)
	VisitConflict(code Code)
}

// Code is a failure's stable code, such as "inbox.item.not_done". It cannot be compared: decide by visiting, never
// by code. The zero Code is no code; NewCode never makes one, and the bases refuse it.
type Code struct {
	_    [0]func()
	code string
}

// NewCode makes a code. Only cores and the kit make codes; an empty code or one with a character outside a-z, 0-9,
// "_" and "." is a defect.
func NewCode(code string) Code {
	if strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789_.") != "" || code == "" {
		defect.Raise("fail.NewCode", "a code is made of a-z, 0-9, _ and .: "+quote(code))
	}
	return Code{code: code}
}

// Wire is the code as the wire carries it. Adapters and testkit only.
func (c Code) Wire() string { return c.code }

// Part names the part of an input that was wrong, in the core's own words ("machine", never the wire field
// "machine_id"); the adapter that fed the input maps it to its field. The zero Part is no part; NewPart never makes
// one.
type Part struct {
	_    [0]func()
	part string
}

// NewPart makes a part. Only cores and the kit make parts; an empty part, or one with a character outside letters,
// digits and ".", is a defect: an underscore is a wire field's spelling.
func NewPart(part string) Part {
	if strings.Trim(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.") != "" || part == "" {
		defect.Raise("fail.NewPart", "a part is made of letters, digits and .: "+quote(part))
	}
	return Part{part: part}
}

// Wire is the part as an adapter maps it. Adapters and testkit only.
func (p Part) Wire() string { return p.part }

// Invalid is input that broke a rule: its code and the part that was wrong. The zero Invalid is no failure;
// NewInvalid never makes one.
type Invalid struct {
	code Code
	part Part
}

// NewInvalid is the failure for a part of the input; a zero code or part is a defect.
func NewInvalid(c Code, p Part) Invalid {
	known(c, "fail.NewInvalid")
	if p.part == "" {
		defect.Raise("fail.NewInvalid", "the zero Part")
	}
	return Invalid{code: c, part: p}
}

// Accept tells v the code and the part; the zero Invalid is a defect.
func (i Invalid) Accept(v FailVisitor) {
	known(i.code, "fail.Invalid.Accept")
	if i.part.part == "" {
		defect.Raise("fail.Invalid.Accept", "the zero Part")
	}
	v.VisitInvalid(i.code, i.part)
}

func (Invalid) fail() {}

// Refusal is a request the domain's rules turn down. The zero Refusal is no failure; NewRefusal never makes one.
type Refusal struct{ refusal Code }

// NewRefusal is the failure for a refused request; a zero code is a defect.
func NewRefusal(c Code) Refusal { known(c, "fail.NewRefusal"); return Refusal{refusal: c} }

// Accept tells v the code; the zero Refusal is a defect.
func (r Refusal) Accept(v FailVisitor) {
	known(r.refusal, "fail.Refusal.Accept")
	v.VisitRefused(r.refusal)
}

func (Refusal) fail() {}

// Absence is something the request names that does not exist. The zero Absence is no failure; NewAbsence never
// makes one.
type Absence struct{ absence Code }

// NewAbsence is the failure for something missing; a zero code is a defect.
func NewAbsence(c Code) Absence { known(c, "fail.NewAbsence"); return Absence{absence: c} }

// Accept tells v the code; the zero Absence is a defect.
func (a Absence) Accept(v FailVisitor) {
	known(a.absence, "fail.Absence.Accept")
	v.VisitMissing(a.absence)
}

func (Absence) fail() {}

// Conflict is a request that met a state it cannot apply to. The zero Conflict is no failure; NewConflict never
// makes one.
type Conflict struct{ conflict Code }

// NewConflict is the failure for a conflicting request; a zero code is a defect.
func NewConflict(c Code) Conflict { known(c, "fail.NewConflict"); return Conflict{conflict: c} }

// Accept tells v the code; the zero Conflict is a defect.
func (c Conflict) Accept(v FailVisitor) {
	known(c.conflict, "fail.Conflict.Accept")
	v.VisitConflict(c.conflict)
}

func (Conflict) fail() {}

// Invalids is every invalid part of one input, in the order they were found, so a builder reports them all at
// once. The zero Invalids is no failure; NewInvalids never makes one.
type Invalids struct{ invalids []Invalid }

// NewInvalids starts the list with its first failure; a zero Invalid is a defect.
func NewInvalids(first Invalid) Invalids {
	known(first.code, "fail.NewInvalids")
	return Invalids{invalids: []Invalid{first}}
}

// With is the list with one more failure after the others; a zero Invalid is a defect. The receiver is unchanged.
func (s Invalids) With(i Invalid) Invalids {
	known(i.code, "fail.Invalids.With")
	return Invalids{invalids: append(slices.Clip(s.invalids), i)}
}

// Accept tells v each failure in order.
func (s Invalids) Accept(v FailVisitor) {
	if len(s.invalids) == 0 {
		defect.Raise("fail.Invalids.Accept", "the zero Invalids")
	}
	for _, i := range s.invalids {
		v.VisitInvalid(i.code, i.part)
	}
}

func (Invalids) fail() {}

// known raises a defect for the zero Code.
func known(c Code, op string) {
	if c.code == "" {
		defect.Raise(op, "the zero Code")
	}
}

// quote is s in double quotes, for a defect's cause.
func quote(s string) string { return `"` + s + `"` }
