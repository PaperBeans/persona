package badapp

import "time"

// Item is a record with exported fields in application code: app-exported-record counts it.
type Item struct {
	DoneAt *time.Time
	DoneBy string
}

// Kind is a named primitive: app-named-primitive counts it.
type Kind string

// reopen writes the done state field by field: app-lifecycle-write counts both writes.
func reopen(it *Item) {
	it.DoneAt, it.DoneBy = nil, ""
}
