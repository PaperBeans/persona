// Package clocktest is clocks for tests: one fixed at an instant, and a fake one a test moves forward.
//
//persona:layer testkit
package clocktest

import (
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/clock"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

// Fixed is a clock that always reads t.
func Fixed(t time.Time) clock.Clock { return fixed{now: instant.ReadClock(t)} }

type fixed struct{ now instant.Now }

func (f fixed) Now() instant.Now { return f.now }

// Fake is a clock that reads the time a test set and moves only when the test advances it. It is safe for
// concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake is a fake clock reading t.
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

// Now reads the fake clock.
func (f *Fake) Now() instant.Now {
	f.mu.Lock()
	defer f.mu.Unlock()
	return instant.ReadClock(f.now)
}

// Advance moves the fake clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
