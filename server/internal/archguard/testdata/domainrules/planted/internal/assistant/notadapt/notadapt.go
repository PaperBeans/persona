// Package notadapt declares adapter to hold raw shapes for application code: what it exports is refused, and its
// named primitives are counted as an application package's are.
//
//persona:layer adapter
package notadapt

// Kind is raw text under the package's own name.
type Kind string

// Kinds is raw text in a slice under the package's own name.
type Kinds []string // adapter-exported-result

// Lookup is a callback under the package's own name.
type Lookup func(key string) string // adapter-exported-result

// Word is raw text under an exported alias.
type Word = string // adapter-exported-result

// KindReady is raw text application code reads.
const KindReady Kind = "ready" // adapter-exported-result

// level is a number under the package's own name.
type level int
