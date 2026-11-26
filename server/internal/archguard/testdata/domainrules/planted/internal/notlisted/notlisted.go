// Package notlisted declares contract, but contracts.txt does not list it.
//
//persona:layer contract
package notlisted // layer-contract

// Ping is a message.
type Ping struct{ At int64 }
