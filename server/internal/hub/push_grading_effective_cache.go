package hub

import (
	"context"
	"errors"
	"sync"
	"time"
)

type pushGradingLoad struct {
	done     chan struct{}
	cancel   context.CancelFunc
	gen      uint64
	waiters  int
	snapshot *pushGradingSnapshot
	err      error
}

// pushGradingEffectiveCache keeps one consistent base/layer snapshot and
// resolves it independently for each manager. A failed reload retains the last
// good snapshot until another TTL interval passes.
type pushGradingEffectiveCache struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	gen      uint64
	snapshot *pushGradingSnapshot
	expires  time.Time
	loading  *pushGradingLoad
}

func newPushGradingEffectiveCache(ttl time.Duration) *pushGradingEffectiveCache {
	if ttl <= 0 {
		ttl = gradingConfigTTL
	}
	return &pushGradingEffectiveCache{now: time.Now, ttl: ttl}
}

// effectiveForManager resolves a manager-specific effective configuration. The
// cached extension layer is applied only when its owner matches the manager.
func (s pushGradingSnapshot) effectiveForManager(manager string) pushGradingEffective {
	if s.Layer != nil && normalizeGradingManager(s.Layer.Manager) == normalizeGradingManager(manager) {
		return resolvePushGrading(s.Base, &s.Layer.Config, s.Layer.Source)
	}
	return resolvePushGrading(s.Base, nil, "")
}

func (c *pushGradingEffectiveCache) loadSnapshot(ctx context.Context, store Store) (pushGradingSnapshot, error) {
	for {
		c.mu.Lock()
		if c.snapshot != nil && c.now().Before(c.expires) {
			snapshot := *c.snapshot
			c.mu.Unlock()
			return snapshot, nil
		}
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return pushGradingSnapshot{}, err
		}
		load := c.loading
		if load == nil {
			loadCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			load = &pushGradingLoad{
				done:   make(chan struct{}),
				cancel: cancel,
				gen:    c.gen,
			}
			c.loading = load
			go c.runLoad(load, loadCtx, store)
		}
		load.waiters++
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			c.leaveLoad(load)
			return pushGradingSnapshot{}, ctx.Err()
		case <-load.done:
		}
		if load.snapshot != nil {
			return *load.snapshot, nil
		}
		if load.err != nil {
			return pushGradingSnapshot{}, load.err
		}
		// The flight was superseded by invalidate() or cancelled after all waiters left; retry.
	}
}

func (c *pushGradingEffectiveCache) effective(ctx context.Context, store Store, manager string) (pushGradingEffective, error) {
	snapshot, err := c.loadSnapshot(ctx, store)
	if err != nil {
		return pushGradingEffective{}, err
	}
	return snapshot.effectiveForManager(manager), nil
}

func (c *pushGradingEffectiveCache) runLoad(load *pushGradingLoad, ctx context.Context, store Store) {
	defer load.cancel()
	var (
		snapshot pushGradingSnapshot
		err      error
		finished bool
	)
	defer func() {
		if !finished {
			c.mu.Lock()
			if c.loading == load {
				c.loading = nil
			}
			if err != nil {
				load.err = err
			} else {
				load.err = errors.New("push grading load aborted")
			}
			close(load.done)
			c.mu.Unlock()
		}
	}()

	snapshot, err = readPushGradingSnapshot(ctx, store)
	c.finishLoad(load, ctx, snapshot, err)
	finished = true
}

func (c *pushGradingEffectiveCache) finishLoad(load *pushGradingLoad, ctx context.Context, snapshot pushGradingSnapshot, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	active := c.loading == load
	if active {
		c.loading = nil
	}
	if active && c.gen == load.gen {
		nowFn := c.now
		if nowFn == nil {
			nowFn = time.Now
		}
		if err == nil {
			snap := snapshot
			c.snapshot = &snap
			c.expires = nowFn().Add(c.ttl)
			load.snapshot = &snap
		} else if ctx.Err() == nil {
			if c.snapshot != nil {
				c.expires = nowFn().Add(c.ttl)
				snap := *c.snapshot
				load.snapshot = &snap
			} else {
				c.expires = time.Time{}
				load.err = err
			}
		}
	}
	close(load.done)
}

func (c *pushGradingEffectiveCache) leaveLoad(load *pushGradingLoad) {
	var cancel context.CancelFunc
	c.mu.Lock()
	if c.loading == load {
		load.waiters--
		if load.waiters == 0 {
			c.loading = nil
			cancel = load.cancel
		}
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *pushGradingEffectiveCache) invalidate() {
	var cancel context.CancelFunc
	c.mu.Lock()
	c.gen++
	c.snapshot = nil
	c.expires = time.Time{}
	if c.loading != nil {
		cancel = c.loading.cancel
		c.loading = nil
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
