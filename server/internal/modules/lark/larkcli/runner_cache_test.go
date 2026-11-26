package larkcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingCLI wraps the fake lark-cli in a script of its own so a test can count the spawns and change the
// executable on disk.
func countingCLI(t *testing.T, scenario string) (bin, log string) {
	t.Helper()
	fake, err := filepath.Abs(filepath.Join("testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "lark-cli")
	log = filepath.Join(dir, "spawns.log")
	writeWrapper(t, bin, fake, "")
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	t.Setenv("LARKCLI_FAKE_LOG", log)
	return bin, log
}

func writeWrapper(t *testing.T, bin, fake, extra string) {
	t.Helper()
	script := "#!/bin/sh\n" + extra + "exec '" + fake + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func spawnCounts(t *testing.T, log string) (version, auth int) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case strings.Contains(line, "--version"):
			version++
		case strings.Contains(line, "auth status"):
			auth++
		}
	}
	return version, auth
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func cachedRunner(t *testing.T, cfg Config) (*Runner, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	r := New(cfg)
	r.clock = clock.Now
	return r, clock
}

func TestStatusReadsShareOneSpawn(t *testing.T) {
	bin, log := countingCLI(t, "success")
	r := New(Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	ctx := context.Background()
	read := func() {
		if err := r.CheckVersion(ctx); err != nil {
			t.Error(err)
		}
		if auth, err := r.AuthStatus(ctx); err != nil || !auth.LoggedIn {
			t.Errorf("auth = %+v, err = %v", auth, err)
		}
	}
	for range 10 {
		read()
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(read)
	}
	wg.Wait()
	if version, auth := spawnCounts(t, log); version != 1 || auth != 1 {
		t.Fatalf("26 status reads spawned --version %d and auth status %d times, want 1 and 1", version, auth)
	}
}

func TestConcurrentAuthStatusSharesOneSpawn(t *testing.T) {
	bin, log := countingCLI(t, "success")
	r, _ := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			if _, err := r.AuthStatus(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, auth := spawnCounts(t, log); auth != 1 {
		t.Fatalf("auth status spawned %d times, want 1", auth)
	}
}

func TestAuthStatusCacheExpiresAndInvalidates(t *testing.T) {
	bin, log := countingCLI(t, "success")
	profile := "example"
	var profileMu sync.Mutex
	r, clock := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second, ResolveProfile: func(context.Context) (string, error) {
		profileMu.Lock()
		defer profileMu.Unlock()
		return profile, nil
	}})
	ctx := context.Background()
	want := 0
	check := func(step string) {
		t.Helper()
		if _, err := r.AuthStatus(ctx); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if _, auth := spawnCounts(t, log); auth != want {
			t.Fatalf("%s: auth status spawned %d times, want %d", step, auth, want)
		}
	}
	want = 1
	check("first read")
	clock.Advance(authStatusTTL - time.Second)
	check("within the TTL")
	clock.Advance(2 * time.Second)
	want = 2
	check("after the TTL")

	if err := r.AuthLogout(ctx); err != nil {
		t.Fatal(err)
	}
	want = 3
	check("after logout")

	t.Setenv("LARKCLI_FAKE_SCENARIO", "missing_scope")
	if _, err := r.SearchMessages(ctx, "example", SearchOptions{}); KindOf(err) != KindMissingPermission {
		t.Fatalf("search err = %v", err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	check("after a missing-permission error")

	fake, err := filepath.Abs(filepath.Join("testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	writeWrapper(t, bin, fake, "case \" $* \" in *\" im \"*) echo 'not logged in' >&2; exit 1 ;; esac\n")
	if _, err := r.SearchMessages(ctx, "example", SearchOptions{}); KindOf(err) != KindNotLoggedIn {
		t.Fatalf("search err = %v", err)
	}
	writeWrapper(t, bin, fake, "")
	want = 4
	check("after a real call found the account signed out")

	profileMu.Lock()
	profile = "other"
	profileMu.Unlock()
	want = 5
	check("after the profile changed")

	if err := r.InitProfile(ctx, "other", "cli_example", "example-secret", "lark"); err != nil {
		t.Fatal(err)
	}
	want = 6
	check("after profile initialization")
}

func TestAuthStatusCacheDroppedWhenLoginFinishes(t *testing.T) {
	bin, log := countingCLI(t, "not_logged_in")
	r, _ := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	ctx := context.Background()
	if auth, err := r.AuthStatus(ctx); err != nil || auth.LoggedIn {
		t.Fatalf("before login: auth = %+v, err = %v", auth, err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	p, err := r.StartLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not finish")
	}
	if _, err := p.Poll(); err != nil {
		t.Fatal(err)
	}
	if auth, err := r.AuthStatus(ctx); err != nil || !auth.LoggedIn {
		t.Fatalf("after login: auth = %+v, err = %v", auth, err)
	}
	if _, auth := spawnCounts(t, log); auth != 2 {
		t.Fatalf("auth status spawned %d times, want 2", auth)
	}
}

func TestAuthStatusFailureIsCachedBriefly(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lark-cli")
	log := filepath.Join(dir, "spawns.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexit 3\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, clock := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	ctx := context.Background()
	for range 3 {
		if _, err := r.AuthStatus(ctx); err == nil {
			t.Fatal("a failing auth status succeeded")
		}
	}
	if _, auth := spawnCounts(t, log); auth != 1 {
		t.Fatalf("auth status spawned %d times within the failure TTL, want 1", auth)
	}
	clock.Advance(failureTTL + time.Second)
	if _, err := r.AuthStatus(ctx); err == nil {
		t.Fatal("a failing auth status succeeded")
	}
	if _, auth := spawnCounts(t, log); auth != 2 {
		t.Fatalf("auth status spawned %d times after the failure TTL, want 2", auth)
	}
}

func TestVersionCheckFollowsTheExecutable(t *testing.T) {
	bin, log := countingCLI(t, "success")
	r, clock := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	ctx := context.Background()
	for range 3 {
		if err := r.CheckVersion(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if version, _ := spawnCounts(t, log); version != 1 {
		t.Fatalf("--version spawned %d times, want 1", version)
	}
	clock.Advance(versionTTL / 2)
	if err := r.CheckVersion(ctx); err != nil {
		t.Fatal(err)
	}
	if version, _ := spawnCounts(t, log); version != 1 {
		t.Fatalf("--version spawned %d times for an unchanged executable, want 1", version)
	}

	fake, err := filepath.Abs(filepath.Join("testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	writeWrapper(t, bin, fake, "LARKCLI_FAKE_SCENARIO=old; export LARKCLI_FAKE_SCENARIO\n")
	if err := r.CheckVersion(ctx); KindOf(err) != KindUnsupportedVersion {
		t.Fatalf("replaced executable: err = %v, want unsupported version", err)
	}
	if err := r.CheckVersion(ctx); KindOf(err) != KindUnsupportedVersion {
		t.Fatalf("cached unsupported version: err = %v", err)
	}
	if version, _ := spawnCounts(t, log); version != 2 {
		t.Fatalf("--version spawned %d times after the executable changed, want 2", version)
	}
}

func TestFreshAuthStatusBypassesCache(t *testing.T) {
	bin, log := countingCLI(t, "success")
	r, _ := cachedRunner(t, Config{Bin: bin, Profile: "example", Timeout: 5 * time.Second})
	ctx := context.Background()
	if auth, err := r.AuthStatus(ctx); err != nil || !auth.LoggedIn {
		t.Fatalf("warm cache: auth = %+v, err = %v", auth, err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "not_logged_in")
	if auth, err := r.FreshAuthStatus(ctx); err != nil || auth.LoggedIn {
		t.Fatalf("live read after sign-out: auth = %+v, err = %v", auth, err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	if auth, err := r.FreshAuthStatus(ctx); err != nil || !auth.LoggedIn {
		t.Fatalf("live read after sign-in: auth = %+v, err = %v", auth, err)
	}
	if auth, err := r.AuthStatus(ctx); err != nil || !auth.LoggedIn {
		t.Fatalf("cached after live read: auth = %+v, err = %v", auth, err)
	}
	if _, auth := spawnCounts(t, log); auth != 3 {
		t.Fatalf("auth status spawned %d times, want 3 (1 cached + 2 live)", auth)
	}
}
