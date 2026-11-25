// Package defect raises programmer errors: a wiring mistake, an outcome nobody set, a zero value where the domain
// refuses one. An expected failure is never a defect; it is a value (domainkit/outcome and domainkit/fail). A defect
// stops the operation, and only the listed sites recover it, through Catch (docs/server/architecture.md "Domain
// model", "Failures").
//
//persona:layer kit
package defect

// Defect is a programmer error: the operation that found it and what was wrong. The zero Defect is no defect; only
// Raise makes one.
type Defect struct {
	op    string
	cause string
}

// Error is the defect as a panic prints it: "<op>: <cause>".
func (d Defect) Error() string { return d.op + ": " + d.cause }

// Raise stops the caller with a Defect: op names the operation ("outcome.Unpack"), cause what was wrong.
func Raise(op, cause string) {
	if op == "" || cause == "" {
		panic(Defect{op: "defect.Raise", cause: "a defect needs its operation and its cause"})
	}
	panic(Defect{op: op, cause: cause})
}

// Catch, deferred, hands a Defect raised below it to report and lets the caller go on; any other panic goes on
// unwinding. Defer it only at the listed sites (docs/server/architecture.md "Domain model", "Failures").
func Catch(report func(d Defect)) {
	r := recover()
	if r == nil {
		return
	}
	d, ok := r.(Defect)
	if !ok {
		panic(r)
	}
	report(d)
}
