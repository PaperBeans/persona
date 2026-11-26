// Package badkernel declares a shared kernel that holds more than values.
//
//persona:layer core shared
package badkernel

// Tag is a label. The zero Tag is no tag.
type Tag struct{ tag uint8 }

// TagBuilder builds a tag. The zero TagBuilder is an empty builder.
type TagBuilder struct{ tagBuilder Tag } // layer-shared: a builder

// ID is the tag itself.
func (t Tag) ID() Tag { return t } // layer-shared: an entity

// Merge is a domain service.
func Merge(a, b Tag) Tag { return a } // layer-shared: a domain service
