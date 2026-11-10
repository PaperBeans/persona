// Package memtrim gives the memory of a large request back to the operating system once it is done with.
//
// A relayed upload of 22 MB takes the hub's slot and the manager past a hundred MB, and when nothing else runs
// nothing allocates after it: no collection runs, and the pages stay with the process for minutes (the scavenger
// returns them slowly). After a body of LargeBody bytes or more, AfterLarge collects and returns the free pages soon
// after, on a goroutine of its own, at most once per Every per process.
package memtrim

import (
	"runtime/debug"
	"sync/atomic"
	"time"
)

const (
	// LargeBody is the size from which a body is worth returning the memory after.
	LargeBody = 8 << 20
	// Every bounds how often a process does it: a collection that returns every free page costs a few milliseconds
	// of CPU, and a burst of uploads needs it once.
	Every = 30 * time.Second
)

// last is when the latest trim was claimed, in Unix nanoseconds (0: never).
var last atomic.Int64

// free is what a trim runs, settle after the claim: the caller's request ends first (its answer written, its
// buffers dropped), so the collection finds them garbage. Variables for the tests.
var (
	free   = debug.FreeOSMemory
	settle = time.Second
)

// AfterLarge is called as a request with a body of size bytes ends: from LargeBody up, it starts a trim unless one
// was started within Every.
func AfterLarge(size int) {
	if size >= LargeBody && claim(time.Now()) {
		time.AfterFunc(settle, free)
	}
}

// claim takes the turn at now: false when one was taken within Every before it.
func claim(now time.Time) bool {
	t := now.UnixNano()
	for {
		prev := last.Load()
		if prev != 0 && t-prev < int64(Every) {
			return false
		}
		if last.CompareAndSwap(prev, t) {
			return true
		}
	}
}
