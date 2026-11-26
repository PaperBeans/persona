// Package badlib declares library to hold a domain concept: its records and named primitives count as an
// application package's do.
//
//persona:layer library
package badlib

// Title is a named string. // app-named-primitive
type Title string

// Note is a record with a setter. // app-exported-record
type Note struct {
	Title  Title
	Pinned bool
}

// Pin sets the flag.
func (n *Note) Pin() { n.Pinned = true }
