package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type gradingCacheStore struct {
	settingsStore
	callsMu     sync.Mutex
	calls       int
	err         error
	block       chan struct{}
	started     chan struct{}
	startedOnce sync.Once
}

func newGradingCacheStore() *gradingCacheStore {
	return &gradingCacheStore{settingsStore: settingsStore{vals: map[string][]byte{}}}
}

func (s *gradingCacheStore) Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error) {
	s.callsMu.Lock()
	s.calls++
	err := s.err
	s.callsMu.Unlock()
	if s.started != nil {
		s.startedOnce.Do(func() { close(s.started) })
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return s.settingsStore.Settings(ctx, keys...)
}

func (s *gradingCacheStore) stats() (int, error) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	return s.calls, s.err
}

func (s *gradingCacheStore) setErr(err error) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	s.err = err
}

func prepareGradingCacheStore(t *testing.T, store *gradingCacheStore) {
	t.Helper()
	ctx := context.Background()
	base := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push"}}`, false)
	if err := writePushGradingBase(ctx, store, base); err != nil {
		t.Fatal(err)
	}
	layer := pushGradingLayer{
		Source:    "extension:one",
		Manager:   "manager-a",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Config:    parseGradingTestConfig(t, `{"version":1,"actions":{"P2":"push"}}`, true),
	}
	if _, err := writePushGradingLayer(ctx, store, layer); err != nil {
		t.Fatal(err)
	}
}

func TestPushGradingEffectiveCachePerManager(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	cache := newPushGradingEffectiveCache(time.Minute)

	for _, tc := range []struct {
		name     string
		manager  string
		wantP2   string
		wantP2By string
	}{
		{name: "owning manager", manager: "manager-a", wantP2: "push", wantP2By: "extension:one"},
		{name: "another manager", manager: "manager-b", wantP2: "silence", wantP2By: "default"},
		{name: "default manager", manager: "", wantP2: "silence", wantP2By: "default"},
		{name: "assistant manager", manager: assistantwire.AssistantID, wantP2: "silence", wantP2By: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cache.effective(ctx, store, tc.manager)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Enabled || got.Actions.P1 != "push" || got.Actions.P2 != tc.wantP2 || got.By["actions.P2"] != tc.wantP2By {
				t.Fatalf("effective = %+v, want P2=%q by=%q", got, tc.wantP2, tc.wantP2By)
			}
		})
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("Settings calls = %d, want one cached load", calls)
	}
}

func TestPushGradingSnapshotManagerActivation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		baseRaw   string
		ownerWant bool
		otherWant bool
	}{
		{name: "automatic", baseRaw: `{"version":1}`, ownerWant: true, otherWant: false},
		{name: "owner disabled", baseRaw: `{"version":1,"enabled":false}`, ownerWant: false, otherWant: false},
		{name: "owner enabled", baseRaw: `{"version":1,"enabled":true}`, ownerWant: true, otherWant: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := parseGradingTestConfig(t, tc.baseRaw, false)
			snapshot := pushGradingSnapshot{
				Base: &base,
				Layer: &pushGradingLayer{
					Source:  "extension:one",
					Manager: "manager-a",
					Config:  parseGradingTestConfig(t, `{"version":1}`, true),
				},
			}
			if got := snapshot.effectiveForManager(" manager-a "); got.Enabled != tc.ownerWant {
				t.Fatalf("owning manager enabled = %v, want %v", got.Enabled, tc.ownerWant)
			}
			if got := snapshot.effectiveForManager("manager-b"); got.Enabled != tc.otherWant {
				t.Fatalf("other manager enabled = %v, want %v", got.Enabled, tc.otherWant)
			}
		})
	}
}

func TestPushGradingEffectiveCacheTTLAndInvalidation(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cache := newPushGradingEffectiveCache(time.Minute)
	cache.now = func() time.Time { return now }

	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("fresh cache calls = %d, want 1", calls)
	}
	updatedBase := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"push"}}`, false)
	if err := writePushGradingBase(ctx, store, updatedBase); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	got, err := cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions.P2 != "push" || got.By["actions.P2"] != "base" {
		t.Fatalf("expired reload kept old configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 2 {
		t.Fatalf("expired cache calls = %d, want 2", calls)
	}
	updatedBase = parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	if err := writePushGradingBase(ctx, store, updatedBase); err != nil {
		t.Fatal(err)
	}
	cache.invalidate()
	got, err = cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions.P2 != "hold" || got.By["actions.P2"] != "base" {
		t.Fatalf("invalidated reload kept old configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("invalidated cache calls = %d, want 3", calls)
	}
}

func TestPushGradingEffectiveCacheReloadFailure(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	loadErr := errors.New("settings unavailable")
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cache := newPushGradingEffectiveCache(time.Minute)
	cache.now = func() time.Time { return now }

	store.setErr(loadErr)
	if _, err := cache.effective(ctx, store, "manager-a"); !errors.Is(err, loadErr) {
		t.Fatalf("first load error = %v, want %v", err, loadErr)
	}
	store.setErr(nil)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	store.setErr(loadErr)
	got, err := cache.effective(ctx, store, "manager-a")
	if err != nil {
		t.Fatalf("stale fallback error = %v", err)
	}
	if got.Actions.P2 != "push" || got.By["actions.P2"] != "extension:one" {
		t.Fatalf("stale fallback changed configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("reload calls = %d, want 3", calls)
	}
	now = now.Add(30 * time.Second)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("stale snapshot was retried before TTL: calls=%d", calls)
	}
	recoveredBase := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	if err := writePushGradingBase(ctx, store, recoveredBase); err != nil {
		t.Fatal(err)
	}
	store.setErr(nil)
	now = now.Add(30 * time.Second)
	got, err = cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatalf("recovered reload error = %v", err)
	}
	if got.Actions.P2 != "hold" || got.By["actions.P2"] != "base" {
		t.Fatalf("recovered reload kept stale configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 4 {
		t.Fatalf("recovery calls = %d, want 4", calls)
	}
}

func TestPushGradingEffectiveCacheConcurrentLoad(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	store.block = make(chan struct{})
	store.started = make(chan struct{})
	prepareGradingCacheStore(t, store)
	cache := newPushGradingEffectiveCache(time.Minute)

	const workers = 24
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			manager := "manager-a"
			if i%2 == 1 {
				manager = "manager-b"
			}
			got, err := cache.effective(ctx, store, manager)
			if err != nil {
				errs <- err
				return
			}
			want := "silence"
			if manager == "manager-a" {
				want = "push"
			}
			if got.Actions.P2 != want {
				errs <- errors.New("manager configuration crossed")
			}
		}(i)
	}
	close(start)
	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		close(store.block)
		t.Fatal("cache load did not start")
	}
	close(store.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("concurrent cache calls = %d, want single-flight load", calls)
	}
}

type staleFirstGradingStore struct {
	settingsStore
	mu           sync.Mutex
	calls        int
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (s *staleFirstGradingStore) Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		// Read v1 before invalidate() writes v2, then stall before returning v1.
		vals, err := s.settingsStore.Settings(ctx, keys...)
		close(s.firstStarted)
		<-s.releaseFirst
		return vals, err
	}
	return s.settingsStore.Settings(ctx, keys...)
}

func TestPushGradingEffectiveCacheInvalidateDuringInFlightLoad(t *testing.T) {
	ctx := context.Background()
	store := &staleFirstGradingStore{
		settingsStore: settingsStore{vals: map[string][]byte{}},
		firstStarted:  make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
	v1 := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"silence"}}`, false)
	if err := writePushGradingBase(ctx, store, v1); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cache := newPushGradingEffectiveCache(time.Minute)
	cache.now = func() time.Time { return now }

	type res struct {
		got pushGradingEffective
		err error
	}
	firstRes := make(chan res, 1)
	go func() {
		got, err := cache.effective(ctx, store, "manager-b")
		firstRes <- res{got: got, err: err}
	}()
	<-store.firstStarted

	// Update settings to v2 and invalidate while the first read already fetched v1.
	v2 := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	if err := writePushGradingBase(ctx, store, v2); err != nil {
		close(store.releaseFirst)
		t.Fatal(err)
	}
	cache.invalidate()
	close(store.releaseFirst)

	r1 := <-firstRes
	if r1.err != nil {
		t.Fatalf("first caller error = %v", r1.err)
	}
	if r1.got.Actions.P2 != "hold" {
		t.Fatalf("first caller after in-flight invalidate got P2=%q, want hold", r1.got.Actions.P2)
	}

	// Subsequent calls within the same TTL must observe v2, never the stale v1 snapshot.
	r2, err := cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatalf("subsequent call error = %v", err)
	}
	if r2.Actions.P2 != "hold" {
		t.Fatalf("subsequent call after in-flight invalidate got stale P2=%q, want hold", r2.Actions.P2)
	}
}

func TestPushGradingEffectiveCacheLeaderCancellationDoesNotPoisonWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newGradingCacheStore()
		store.block = make(chan struct{})
		store.started = make(chan struct{})
		prepareGradingCacheStore(t, store)
		cache := newPushGradingEffectiveCache(time.Minute)

		leaderCtx, cancelLeader := context.WithCancel(context.Background())
		defer cancelLeader()
		leaderErr := make(chan error, 1)
		go func() {
			_, err := cache.effective(leaderCtx, store, "manager-a")
			leaderErr <- err
		}()
		<-store.started

		type res struct {
			got pushGradingEffective
			err error
		}
		waiterRes := make(chan res, 1)
		go func() {
			got, err := cache.effective(context.Background(), store, "manager-b")
			waiterRes <- res{got: got, err: err}
		}()
		// Wait until the waiter is durably blocked joining the single-flight load.
		synctest.Wait()

		cancelLeader()
		if err := <-leaderErr; !errors.Is(err, context.Canceled) {
			close(store.block)
			t.Fatalf("cancelled leader err = %v, want context.Canceled", err)
		}
		// Ensure runLoad remains durably blocked on store.block rather than cancelling when leader left.
		synctest.Wait()
		close(store.block)

		got := <-waiterRes
		if got.err != nil {
			t.Fatalf("healthy waiter poisoned by leader cancellation: err=%v", got.err)
		}
		if !got.got.Enabled || got.got.Actions.P1 != "push" || got.got.Actions.P2 != "silence" {
			t.Fatalf("healthy waiter effective = %+v", got.got)
		}
		if calls, _ := store.stats(); calls != 1 {
			t.Fatalf("Settings calls = %d, want 1 shared load", calls)
		}
	})
}

func TestPushGradingEffectiveCacheDetachedLoadDoesNotClobberReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		store := &staleFirstGradingStore{
			settingsStore: settingsStore{vals: map[string][]byte{}},
			firstStarted:  make(chan struct{}),
			releaseFirst:  make(chan struct{}),
		}
		v1 := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"silence"}}`, false)
		if err := writePushGradingBase(ctx, store, v1); err != nil {
			t.Fatal(err)
		}

		now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
		cache := newPushGradingEffectiveCache(time.Minute)
		cache.now = func() time.Time { return now }

		firstCtx, cancelFirst := context.WithCancel(ctx)
		firstErr := make(chan error, 1)
		go func() {
			_, err := cache.effective(firstCtx, store, "manager-b")
			firstErr <- err
		}()
		<-store.firstStarted

		// Cancel the only waiter so load1 is detached without invalidate() bumping gen.
		cancelFirst()
		if err := <-firstErr; !errors.Is(err, context.Canceled) {
			close(store.releaseFirst)
			t.Fatalf("first cancelled caller err = %v, want context.Canceled", err)
		}

		// Store now holds newer v2; a replacement load reads and caches v2.
		v2 := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
		if err := writePushGradingBase(ctx, store, v2); err != nil {
			close(store.releaseFirst)
			t.Fatal(err)
		}
		got, err := cache.effective(ctx, store, "manager-b")
		if err != nil {
			close(store.releaseFirst)
			t.Fatalf("replacement load err = %v", err)
		}
		if got.Actions.P2 != "hold" {
			close(store.releaseFirst)
			t.Fatalf("replacement load got P2=%q, want hold", got.Actions.P2)
		}

		// Release the detached load1 (returning v1, nil) and ensure it does not clobber v2.
		close(store.releaseFirst)
		synctest.Wait()

		after, err := cache.effective(ctx, store, "manager-b")
		if err != nil {
			t.Fatalf("post-detached load err = %v", err)
		}
		if after.Actions.P2 != "hold" {
			t.Fatalf("detached load clobbered newer snapshot: got P2=%q, want hold", after.Actions.P2)
		}
	})
}

func TestPushGradingEffectiveCacheCancelAllWaitersDoesNotExtendExpiredTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		store := newGradingCacheStore()
		prepareGradingCacheStore(t, store)
		now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
		cache := newPushGradingEffectiveCache(time.Minute)
		cache.now = func() time.Time { return now }

		// Warm the cache with v1 (P2="silence" for manager-b).
		if got, err := cache.effective(ctx, store, "manager-b"); err != nil || got.Actions.P2 != "silence" {
			t.Fatalf("warm load = %+v, %v", got, err)
		}

		// Expire the cache and write v2 (P2="hold").
		now = now.Add(time.Minute)
		v2 := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
		if err := writePushGradingBase(ctx, store, v2); err != nil {
			t.Fatal(err)
		}

		store.block = make(chan struct{})
		store.started = make(chan struct{})
		store.startedOnce = sync.Once{}

		cancelCtx, cancel := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			_, err := cache.effective(cancelCtx, store, "manager-b")
			errCh <- err
		}()
		<-store.started
		cancel()
		if err := <-errCh; !errors.Is(err, context.Canceled) {
			close(store.block)
			t.Fatalf("cancelled reload err = %v, want context.Canceled", err)
		}
		// Wait while store.block is still open so runLoad must exit via <-ctx.Done().
		synctest.Wait()
		close(store.block)

		// A healthy caller at the same timestamp must reload v2 instead of being suppressed for a full TTL.
		got, err := cache.effective(ctx, store, "manager-b")
		if err != nil {
			t.Fatalf("healthy reload after cancelled reload = %v", err)
		}
		if got.Actions.P2 != "hold" {
			t.Fatalf("cancelled reload extended expired TTL: got P2=%q, want hold", got.Actions.P2)
		}
		if calls, _ := store.stats(); calls != 3 {
			t.Fatalf("Settings calls = %d, want 3 (warm, cancelled, healthy reload)", calls)
		}
	})
}

func TestPushGradingEffectiveCacheInvalidateNeverReturnsZeroConfig(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	vHold := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	vPush := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"push"}}`, false)
	if err := writePushGradingBase(ctx, store, vHold); err != nil {
		t.Fatal(err)
	}
	cache := newPushGradingEffectiveCache(time.Minute)

	const readers = 12
	const invalidations = 300
	done := make(chan struct{})
	errCh := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := cache.effective(ctx, store, "manager-b")
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				if !got.Enabled || got.Actions.P1 != "push" || (got.Actions.P2 != "hold" && got.Actions.P2 != "push") {
					select {
					case errCh <- errors.New("returned zero or corrupt effective config with nil error"):
					default:
					}
					return
				}
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}

	for i := range invalidations {
		cfg := vHold
		if i%2 == 1 {
			cfg = vPush
		}
		if err := writePushGradingBase(ctx, store, cfg); err != nil {
			close(done)
			wg.Wait()
			t.Fatal(err)
		}
		cache.invalidate()
	}
	close(done)
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestPushGradingEffectiveCacheWaiterAfterInvalidationAndLeaderCancel(t *testing.T) {
	t.Run("P1-1 waiter arriving after invalidation never gets zero snapshot", func(t *testing.T) {
		ctx := context.Background()
		store := newGradingCacheStore()
		prepareGradingCacheStore(t, store)
		store.block = make(chan struct{})
		store.started = make(chan struct{})
		cache := newPushGradingEffectiveCache(time.Minute)

		resA := make(chan pushGradingEffective, 1)
		errA := make(chan error, 1)
		go func() {
			got, err := cache.effective(ctx, store, "manager-a")
			if err != nil {
				errA <- err
				return
			}
			resA <- got
		}()
		select {
		case <-store.started:
		case <-time.After(5 * time.Second):
			close(store.block)
			t.Fatal("load A did not start")
		}

		// Update base and invalidate while A is blocked in Settings.
		updated := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"silence"}}`, false)
		if err := writePushGradingBase(ctx, store, updated); err != nil {
			close(store.block)
			t.Fatal(err)
		}
		cache.invalidate()

		// B arrives AFTER invalidation while A's stale load is still blocked.
		resB := make(chan pushGradingEffective, 1)
		errB := make(chan error, 1)
		go func() {
			got, err := cache.effective(ctx, store, "manager-b")
			if err != nil {
				errB <- err
				return
			}
			resB <- got
		}()

		close(store.block)
		for name, ch := range map[string]struct {
			res chan pushGradingEffective
			err chan error
		}{"A": {resA, errA}, "B": {resB, errB}} {
			select {
			case err := <-ch.err:
				t.Fatalf("%s error: %v", name, err)
			case got := <-ch.res:
				if !got.Enabled || got.Actions.P1 != "silence" {
					t.Fatalf("%s got stale or zero config: %+v", name, got)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s timed out", name)
			}
		}
	})

	t.Run("P1-2 cancelled leader does not poison concurrent waiter", func(t *testing.T) {
		store := newGradingCacheStore()
		prepareGradingCacheStore(t, store)
		store.block = make(chan struct{})
		store.started = make(chan struct{})
		cache := newPushGradingEffectiveCache(time.Minute)

		leaderCtx, cancelLeader := context.WithCancel(context.Background())
		leaderErr := make(chan error, 1)
		go func() {
			_, err := cache.effective(leaderCtx, store, "manager-a")
			leaderErr <- err
		}()
		select {
		case <-store.started:
		case <-time.After(5 * time.Second):
			close(store.block)
			t.Fatal("leader load did not start")
		}

		waiterRes := make(chan pushGradingEffective, 1)
		waiterErr := make(chan error, 1)
		go func() {
			got, err := cache.effective(context.Background(), store, "manager-a")
			if err != nil {
				waiterErr <- err
				return
			}
			waiterRes <- got
		}()

		// Cancel leader before releasing the store read.
		cancelLeader()
		select {
		case err := <-leaderErr:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("leader err = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			close(store.block)
			t.Fatal("leader did not return after cancel")
		}
		close(store.block)

		select {
		case err := <-waiterErr:
			t.Fatalf("waiter was poisoned by cancelled leader: %v", err)
		case got := <-waiterRes:
			if !got.Enabled || got.Actions.P2 != "push" {
				t.Fatalf("waiter got wrong config: %+v", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter timed out")
		}
	})
}

type panickingGradingStore struct {
	settingsStore
	panicVal any
}

func (s *panickingGradingStore) Settings(context.Context, ...string) (map[string]json.RawMessage, error) {
	panic(s.panicVal)
}

func TestPushGradingEffectiveCachePanicUnwindsWithoutSwallowingOrDeadlocking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		healthyStore := newGradingCacheStore()
		prepareGradingCacheStore(t, healthyStore)
		cache := newPushGradingEffectiveCache(time.Minute)

		loadCtx, cancel := context.WithCancel(ctx)
		load := &pushGradingLoad{
			done:   make(chan struct{}),
			cancel: cancel,
			gen:    cache.gen,
		}
		cache.mu.Lock()
		cache.loading = load
		cache.mu.Unlock()

		const waiters = 2
		waiterErrs := make(chan error, waiters)
		for range waiters {
			go func() {
				_, err := cache.effective(ctx, healthyStore, "manager-a")
				waiterErrs <- err
			}()
		}
		synctest.Wait()

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			cache.runLoad(load, loadCtx, &panickingGradingStore{panicVal: "simulated store panic"})
		}()
		if recovered != "simulated store panic" {
			t.Fatalf("runLoad recovered = %v, want panic to propagate", recovered)
		}
		if !errors.Is(loadCtx.Err(), context.Canceled) {
			t.Fatalf("loadCtx.Err() = %v, want context.Canceled", loadCtx.Err())
		}
		for range waiters {
			if err := <-waiterErrs; err == nil {
				t.Fatal("waiter error = nil, want non-nil aborted load error")
			}
		}

		got, err := cache.effective(ctx, healthyStore, "manager-a")
		if err != nil {
			t.Fatalf("subsequent effective after panic unwind = %v", err)
		}
		if !got.Enabled || got.Actions.P1 != "push" || got.Actions.P2 != "push" {
			t.Fatalf("subsequent effective = %+v, want healthy cached config", got)
		}
		if calls, _ := healthyStore.stats(); calls != 1 {
			t.Fatalf("healthyStore calls = %d, want 1", calls)
		}
	})
}
