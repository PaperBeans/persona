// Package core is the type-aware domain rules' planted core (TestDomainTypedPlanted).
//
//persona:layer core
package core

import (
	"maps"
	"slices"
)

// Book is a planted core value. The zero Book is empty.
type Book struct{ pages map[string]int }

// Count ranges over its map, next to the twin that ranges over its sorted keys.
func Count(b Book) int {
	n := 0
	for range b.pages { // planted: typed-map-range
		n++
	}
	for _, k := range slices.Sorted(maps.Keys(b.pages)) {
		n += len(k)
	}
	return n
}
