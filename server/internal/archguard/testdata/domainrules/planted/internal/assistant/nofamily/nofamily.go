// Package nofamily returns failures with no sealed family of its own.
//
//persona:layer core
package nofamily

import "github.com/nwverse/persona/server/internal/domainkit/fail"

// refused is a refusal no family seals.
type refused struct{ fail.Refusal } // core-fail-family (the package has no Fail family)
