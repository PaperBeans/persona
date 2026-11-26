// Package sharedhere declares a shared kernel inside the assistant's tree.
//
//persona:layer core shared
package sharedhere // layer-shared: a shared kernel lives directly under internal

// Tone is a notice's tone. The zero Tone is none.
type Tone struct{ tone uint8 }
