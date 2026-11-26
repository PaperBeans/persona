// Package db is the planted database library: rows in and rows out, no domain concept.
//
//persona:layer library
package db

import "time"

// Usage is one stored usage row.
type Usage struct {
	Day    time.Time
	Tokens int64
}
