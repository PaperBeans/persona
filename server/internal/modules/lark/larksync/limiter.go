package larksync

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// API families: each has its own token bucket, as the platform limits each API on its own.
const (
	famIM            = "im"
	famSearch        = "search"
	famDrive         = "drive"
	famDriveDownload = "drive_download"
	famDocx          = "docx"
	famSheets        = "sheets"
	famBitable       = "bitable"
	famWiki          = "wiki"
	famMinutes       = "minutes"
)

// familyRates are the requests per second a binding makes to each family: about half of what the platform documents
// for a user token (most read APIs allow 10 to 20 a second per app and user; drive file downloads allow 5; message
// search far less), since every binding of one app shares the app's quota and the owner's other tools (lark-cli, the
// gateway) use it too.
var familyRates = map[string]float64{
	famIM:            5,
	famSearch:        1,
	famDrive:         5,
	famDriveDownload: 2,
	famDocx:          5,
	famSheets:        5,
	famBitable:       5,
	famWiki:          5,
	famMinutes:       2,
}

// Backoff on a rate limit or a passing failure.
const (
	maxLimitedRetries   = 4 // a 429 is waited out at most this many times before the resource gives up
	maxTransientRetries = 2 // a 5xx or a broken connection
	backoffBase         = time.Second
	backoffMax          = 60 * time.Second
)

// limiter is a binding's token buckets, one per API family, on an injectable clock. A request reserves its token at
// once and then waits out the debt, so a clock that does not move (tests) can never spin the wait.
type limiter struct {
	mu      sync.Mutex
	now     func() time.Time
	rates   map[string]float64 // requests per second by family (familyRates; tests raise them)
	buckets map[string]*bucket
}

// bucket holds up to one second of requests (rate tokens), refilled continuously.
type bucket struct {
	rate   float64
	tokens float64
	last   time.Time
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, rates: familyRates, buckets: map[string]*bucket{}}
}

// reserve takes one token from family's bucket and returns how long to wait before the request may go.
func (l *limiter) reserve(family string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[family]
	if b == nil {
		rate := l.rates[family]
		if rate <= 0 {
			rate = 1
		}
		b = &bucket{rate: rate, tokens: rate, last: now}
		l.buckets[family] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = min(b.rate, b.tokens+el*b.rate)
		b.last = now
	}
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / b.rate * float64(time.Second))
}

// wait reserves a token of family and sleeps out the wait, ending early with ctx's error.
func (l *limiter) wait(ctx context.Context, family string, sleep func(context.Context, time.Duration) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d := l.reserve(family); d > 0 {
		return sleep(ctx, d)
	}
	return nil
}

// backoff is the wait before retry number n (0 first): the wait the platform named (Retry-After or
// x-ogw-ratelimit-reset) when it named one, else 1 s, 2 s, 4 s… plus jitter; never above a minute.
func (s *Syncer) backoff(n int, named time.Duration) time.Duration {
	if named > 0 {
		return min(named, backoffMax)
	}
	d := backoffBase << min(n, 6)
	d = min(d, backoffMax)
	return min(d+s.jitter(d/4), backoffMax)
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// randJitter is a random duration in [0, d).
func randJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)))
}
