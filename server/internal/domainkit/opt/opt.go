// Package opt is absence inside cores: an Option holds a value or none, without nil pointers or zero markers. It has
// no Get and no Or: a caller tells it what to do in each case (Match, Fold) rather than asking for its value
// (docs/server/architecture.md "Domain model", "Values").
//
//persona:layer kit
package opt

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// Option is a value of type T, or none. The zero Option is none.
type Option[T any] struct {
	value T
	some  bool
}

// Some is the option holding v.
func Some[T any](v T) Option[T] { return Option[T]{value: v, some: true} }

// None is the option holding nothing.
func None[T any]() Option[T] { return Option[T]{} }

// IsNone says whether o holds nothing.
func (o Option[T]) IsNone() bool { return !o.some }

// Match calls some with o's value, or none when it holds nothing; a nil function is a defect.
func (o Option[T]) Match(some func(T), none func()) {
	if some == nil || none == nil {
		defect.Raise("opt.Match", "a nil function")
	}
	if o.some {
		some(o.value)
		return
	}
	none()
}

// Fold is some of o's value, or none() when it holds nothing; a nil function is a defect.
func Fold[T, R any](o Option[T], some func(T) R, none func() R) R {
	if some == nil || none == nil {
		defect.Raise("opt.Fold", "a nil function")
	}
	if o.some {
		return some(o.value)
	}
	return none()
}
