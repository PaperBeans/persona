// Package textlib trims text: technical code with no domain concept, so nothing in it is counted.
//
//persona:layer library
package textlib

import "strings"

// Trimmed is s without the spaces around it.
func Trimmed(s string) string { return strings.TrimSpace(s) }
