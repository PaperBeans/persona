package node

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// The launcher probe assistantLauncherProfile reads is kept like the agent-kind and model probes on the same path
// (launcherProbeCached, modelProbeCache) instead of being taken before every message (#8947). The probe is a login
// shell that sources the user's startup files, about a second with an ordinary ~/.zshrc, and the profile check runs
// before each message to an assistant conversation, twice on some resume paths: every message waited for it.
//
// The check itself still runs on every message, on a probe at most profileProbeFresh old. The probe is taken again
// at once when what it depends on changes (shellStamp): a launcher edited in ~/.zshrc to set another config directory
// is refused on the very next message, as before. A failed probe is kept only profileProbeRetry, so a shell that was
// slow once is not taken for a launcher that sets nothing for long. A probe cut short by its caller is not kept.
var (
	profileProbeFresh = 10 * time.Minute
	profileProbeRetry = time.Minute
	// profileProbeSettle: a file written this recently may be written again within one timestamp tick at the same
	// size, which its stamp would not tell apart (docs/lessons/agents.md, #5659): until then every message probes.
	profileProbeSettle = 2 * time.Second
)

var profileProbes = struct {
	sync.Mutex
	got map[string]profileProbe
}{got: map[string]profileProbe{}}

type profileProbe struct {
	at    time.Time
	stamp string
	env   map[string]string
	ok    bool
}

// launcherProfileEnv is the environment the probe saw launcher set for the agent (Probe.Env), false when the probe
// failed. The map is shared: read it, never change it.
func (n *Node) launcherProfileEnv(ctx context.Context, launcher string) (map[string]string, bool) {
	key := n.home + "\x00" + launcher
	stamp, settled := shellStamp(n.home, launcher)
	profileProbes.Lock()
	c, hit := profileProbes.got[key]
	profileProbes.Unlock()
	if hit && settled && c.stamp == stamp && time.Since(c.at) < profileProbeFresh && (c.ok || time.Since(c.at) < profileProbeRetry) {
		return c.env, c.ok
	}
	p, err := n.launcherProbe(ctx, launcher)
	if ctx.Err() != nil {
		return p.Env, err == nil
	}
	profileProbes.Lock()
	if len(profileProbes.got) > 256 {
		profileProbes.got = map[string]profileProbe{}
	}
	profileProbes.got[key] = profileProbe{at: time.Now(), stamp: stamp, env: p.Env, ok: err == nil}
	profileProbes.Unlock()
	return p.Env, err == nil
}

// shellStamp is what a launcher probe depends on besides the launcher's name, so a change to any of it is seen at once:
// the node's environment (the login shell inherits it), the startup files an interactive zsh or bash reads, and the
// launcher's own file (a wrapper script, which the probe reads instead of running). Files are compared by size and
// modification time; settled is false while one of them was written within profileProbeSettle. A file one of them
// sources is not followed: a change there is seen after profileProbeFresh.
func shellStamp(home, launcher string) (stamp string, settled bool) {
	h := sha256.New()
	env := os.Environ()
	slices.Sort(env)
	for _, kv := range env {
		h.Write([]byte(kv))
		h.Write([]byte{0})
	}
	zdot := cmp.Or(os.Getenv("ZDOTDIR"), home)
	files := []string{
		filepath.Join(zdot, ".zshenv"), filepath.Join(zdot, ".zshrc"),
		filepath.Join(home, ".bashrc"), filepath.Join(home, ".bash_profile"), filepath.Join(home, ".profile"),
		"/etc/zshenv", "/etc/zshrc", "/etc/zsh/zshenv", "/etc/zsh/zshrc", "/etc/bash.bashrc",
	}
	if p, err := exec.LookPath(launcher); err == nil {
		files = append(files, p)
	}
	launcherCache.Lock()
	if p := launcherCache.scripts[launcher]; p != "" {
		files = append(files, p)
	}
	launcherCache.Unlock()
	settled = true
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(h, "%s\x00%d\x00%d\n", f, st.Size(), st.ModTime().UnixNano())
			if time.Since(st.ModTime()) < profileProbeSettle {
				settled = false
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), settled
}
