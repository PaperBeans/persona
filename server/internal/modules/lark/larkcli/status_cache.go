package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Every lark-cli run is a Node.js process. Status reads come from the connector's status, the Inbox poll, thread
// reads and park replies, so the version and the sign-in state are cached here and shared by every caller (#15540).
const (
	// versionTTL is a backstop: the version is read again as soon as the executable on disk changes.
	versionTTL = 30 * time.Minute
	// authStatusTTL bounds how long a sign-in made outside Persona (lark-cli in a terminal) takes to show.
	// Sign-ins, sign-outs and profile changes made through Persona drop the cache at once.
	authStatusTTL = 30 * time.Second
	// failureTTL keeps a burst of callers from each spawning a failing command, while "not installed" and
	// transient failures are still read again within seconds.
	failureTTL = 5 * time.Second
)

type statusCache struct {
	mu      sync.Mutex
	version versionEntry
	auth    authEntry
	authGen uint64
	flights singleflight.Group
}

type executableIdentity struct {
	path    string
	target  string
	size    int64
	modNano int64
}

type versionEntry struct {
	id      executableIdentity
	version string
	err     error
	at      time.Time
}

type authEntry struct {
	set     bool
	profile string
	gen     uint64
	out     []byte
	err     error
	at      time.Time
}

func (r *Runner) cacheNow() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}

// shared runs load once for every concurrent caller with the same key. The run is detached from the first
// caller's cancellation (execute still bounds it by the configured timeout); a caller whose context ends stops
// waiting with the same timeout error execute would have returned.
func (r *Runner) shared(ctx context.Context, key string, load func(context.Context) (any, error)) (any, error) {
	ch := r.cache.flights.DoChan(key, func() (any, error) { return load(context.WithoutCancel(ctx)) })
	select {
	case res := <-ch:
		return res.Val, res.Err
	case <-ctx.Done():
		return nil, cliError(KindTimeout, ctx.Err())
	}
}

func (r *Runner) executable(ctx context.Context) (executableIdentity, bool, error) {
	bin, err := r.binary(ctx)
	if err != nil {
		return executableIdentity{}, false, err
	}
	info, err := os.Stat(bin)
	if err != nil {
		return executableIdentity{}, false, nil
	}
	target, _ := filepath.EvalSymlinks(bin)
	return executableIdentity{path: bin, target: target, size: info.Size(), modNano: info.ModTime().UnixNano()}, true, nil
}

// Version returns the executable's semantic version as printed by --version. It is read again only when the
// resolved executable's path, size or modification time changes.
func (r *Runner) Version(ctx context.Context) (string, error) {
	if _, err := r.resolveProfile(ctx); err != nil {
		return "", err
	}
	now := r.cacheNow()
	r.cache.mu.Lock()
	e := r.cache.version
	r.cache.mu.Unlock()
	if e.err != nil && now.Sub(e.at) < failureTTL {
		return "", e.err
	}
	id, known, err := r.executable(ctx)
	if err != nil {
		r.storeVersion(versionEntry{err: err, at: r.cacheNow()})
		return "", err
	}
	if known && e.err == nil && e.version != "" && e.id == id && now.Sub(e.at) < versionTTL {
		return e.version, nil
	}
	v, err := r.shared(ctx, "version", func(ctx context.Context) (any, error) {
		version, err := r.readVersion(ctx)
		if err != nil {
			r.storeVersion(versionEntry{err: err, at: r.cacheNow()})
		} else if known {
			r.storeVersion(versionEntry{id: id, version: version, at: r.cacheNow()})
		}
		return version, err
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (r *Runner) storeVersion(e versionEntry) {
	r.cache.mu.Lock()
	r.cache.version = e
	r.cache.mu.Unlock()
}

func (r *Runner) readVersion(ctx context.Context) (string, error) {
	out, err := r.execute(ctx, []string{"--version"}, outputVersion)
	if err != nil {
		return "", err
	}
	version := parseVersion(string(out))
	if version == "" {
		return "", cliError(KindOther, errors.New("invalid version output"))
	}
	return version, nil
}

// AuthStatus reads the user identity without touching credentials or changing lark-cli configuration. Results
// are shared for authStatusTTL per execution profile, failures for failureTTL; anything that can change the
// sign-in state drops them (invalidateAuth).
func (r *Runner) AuthStatus(ctx context.Context) (AuthStatus, error) {
	profile, err := r.resolveProfile(ctx)
	if err != nil {
		return AuthStatus{}, err
	}
	now := r.cacheNow()
	r.cache.mu.Lock()
	e, gen := r.cache.auth, r.cache.authGen
	r.cache.mu.Unlock()
	if e.set && e.profile == profile && e.gen == gen {
		ttl := authStatusTTL
		if e.err != nil {
			ttl = failureTTL
		}
		if now.Sub(e.at) < ttl {
			return r.authResult(e.out, e.err)
		}
	}
	v, err := r.shared(ctx, "auth\x00"+profile+"\x00"+strconv.FormatUint(gen, 10), func(ctx context.Context) (any, error) {
		out, err := r.execute(ctx, []string{"auth", "status"}, outputAuth)
		if err == nil && !json.Valid(out) {
			out, err = nil, cliError(KindOther, errors.New("invalid auth status JSON"))
		}
		r.cache.mu.Lock()
		if r.cache.authGen == gen {
			r.cache.auth = authEntry{set: true, profile: profile, gen: gen, out: out, err: err, at: r.cacheNow()}
		}
		r.cache.mu.Unlock()
		return out, err
	})
	if err != nil {
		return AuthStatus{}, err
	}
	return r.authResult(v.([]byte), nil)
}

// FreshAuthStatus reads the live user identity bypassing the auth status cache, then updates the cache if the
// request does not race an invalidation or cancellation. Used where mid-operation identity drift must fail closed.
func (r *Runner) FreshAuthStatus(ctx context.Context) (AuthStatus, error) {
	profile, err := r.resolveProfile(ctx)
	if err != nil {
		return AuthStatus{}, err
	}
	r.cache.mu.Lock()
	gen := r.cache.authGen
	r.cache.mu.Unlock()
	out, err := r.execute(ctx, []string{"auth", "status"}, outputAuth)
	if err == nil && !json.Valid(out) {
		out, err = nil, cliError(KindOther, errors.New("invalid auth status JSON"))
	}
	if ctx.Err() == nil {
		r.cache.mu.Lock()
		if r.cache.authGen == gen {
			r.cache.auth = authEntry{set: true, profile: profile, gen: gen, out: out, err: err, at: r.cacheNow()}
		}
		r.cache.mu.Unlock()
	}
	if err != nil {
		return AuthStatus{}, err
	}
	return r.authResult(out, nil)
}

func (r *Runner) authResult(out []byte, err error) (AuthStatus, error) {
	if err != nil {
		return AuthStatus{}, err
	}
	var raw authStatusWire
	if err := json.Unmarshal(out, &raw); err != nil {
		return AuthStatus{}, cliError(KindOther, errors.New("invalid auth status JSON"))
	}
	return raw.result(r.now()), nil
}

// invalidateAuth drops the cached sign-in state; a read already running when it is called is not stored.
func (r *Runner) invalidateAuth() {
	r.cache.mu.Lock()
	r.cache.authGen++
	r.cache.auth = authEntry{}
	r.cache.mu.Unlock()
}

// signedOut reports the errors that mean the account itself lost its sign-in. Missing-permission errors are left
// out: lark-cli reports a document that is not shared or not found the same way, and they do not change auth status.
func signedOut(kind ErrorKind) bool {
	return kind == KindNotLoggedIn || kind == KindExpired
}
