package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// authNode is a node in the fake home fakeClaudeHome made, its claude at version (cached, as the self-check reads it).
func authNode(t *testing.T, version string) *Node {
	t.Helper()
	home := os.Getenv("HOME")
	n := &Node{home: home, dir: filepath.Join(home, ".persona"), logger: log.New(io.Discard, "", 0)}
	n.claudeV.set(version)
	return n
}

// What `claude auth status --json` prints (2.1.308) for a claude.ai login: the account's email and organization are
// in it and must not leave the machine.
const authClaudeAI = `{
  "loggedIn": true,
  "authMethod": "claude.ai",
  "apiProvider": "firstParty",
  "analyticsDisabled": false,
  "configDirectory": "/home/me/.claude",
  "email": "me@example.com",
  "orgId": "org-1",
  "orgName": "Example",
  "subscriptionType": "max"
}`

// Plain claude is asked in the login shell with the --settings a start gives it before the subcommand, and with the
// machine's claude_start_dir as its config directory; behind a banner and escape codes its status is read, and only
// the login's method leaves (no email, no organization).
func TestClaudeAuthStatusSignedIn(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "args")
	fakeClaudeHome(t, `printf '%s\n' "CONFIG=$CLAUDE_CONFIG_DIR" "$@" > `+shellQuote(seen)+`
printf 'Welcome to this machine\n\033]1337;ShellIntegrationVersion=14\007'
cat <<'EOF'
`+authClaudeAI+`
EOF
`)
	n := authNode(t, "2.1.308")
	dir := "/tmp/profile-dir"
	n.claudeStartDir.Store(&dir)
	st, err := n.claudeAuthStatus(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := proto.ClaudeAuthStatus{State: proto.ClaudeLoggedIn, AuthMethod: "claude.ai", APIProvider: "firstParty", Version: "2.1.308"}
	if st != want {
		t.Fatalf("status = %+v", st)
	}
	b, _ := os.ReadFile(seen)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(args) != 6 || args[0] != "CONFIG=/tmp/profile-dir" || args[1] != "--settings" || args[3] != "auth" || args[4] != "status" || args[5] != "--json" {
		t.Fatalf("claude ran with %q", args)
	}
}

// Signed out: Claude Code exits 1 with the same JSON, and that is not_logged_in, not an error.
func TestClaudeAuthStatusSignedOut(t *testing.T) {
	fakeClaudeHome(t, `echo '{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}'
exit 1
`)
	st, err := authNode(t, "2.1.308").claudeAuthStatus(context.Background(), "claude")
	if err != nil || st.State != proto.ClaudeNotLoggedIn || st.AuthMethod != "none" {
		t.Fatalf("status = %+v (%v)", st, err)
	}
}

// A key in the environment counts as signed in to Claude Code: reported as api_key with the variable's name, which
// the hub takes as unverified until a message goes.
func TestClaudeAuthStatusAPIKey(t *testing.T) {
	fakeClaudeHome(t, `echo '{"loggedIn": true, "authMethod": "api_key", "apiProvider": "firstParty", "apiKeySource": "ANTHROPIC_API_KEY"}'
`)
	st, _ := authNode(t, "2.1.308").claudeAuthStatus(context.Background(), "claude")
	if st.State != proto.ClaudeLoggedIn || st.AuthMethod != "api_key" || st.KeySource != "ANTHROPIC_API_KEY" {
		t.Fatalf("status = %+v", st)
	}
	// Something that looks like a key itself is never passed on as a source.
	if got, ok := parseClaudeAuthStatus(`{"loggedIn": true, "authMethod": "api_key", "apiKeySource": "sk-ant-abcdefghijklmnop"}`); !ok || got.KeySource != "" {
		t.Fatalf("status = %+v", got)
	}
}

// A claude from before `claude auth` is not asked (it could take the words for a prompt): unknown, old_claude.
func TestClaudeAuthStatusOldClaude(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	fakeClaudeHome(t, `touch `+shellQuote(ran)+`
`)
	st, err := authNode(t, "2.1.40").claudeAuthStatus(context.Background(), "claude")
	if err != nil || st.State != proto.ClaudeAuthUnknown || st.Reason != proto.ClaudeAuthOld || st.Version != "2.1.40" {
		t.Fatalf("status = %+v (%v)", st, err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("an old claude was run")
	}
}

// What cannot be read is unknown, never a failure: claude not found, an answer that is no status, a run past the
// time limit.
func TestClaudeAuthStatusUnreadable(t *testing.T) {
	fakeClaudeHome(t, `echo "error: unknown option '--json'" >&2
exit 1
`)
	n := authNode(t, "")
	if st, err := n.claudeAuthStatus(context.Background(), "claude"); err != nil || st.State != proto.ClaudeAuthUnknown ||
		st.Reason != proto.ClaudeAuthUnreadable {
		t.Fatalf("status = %+v (%v)", st, err)
	}
	fakeClaudeHome(t, `sleep 5
`)
	old := claudeAuthTimeout
	claudeAuthTimeout = 300 * time.Millisecond
	t.Cleanup(func() { claudeAuthTimeout = old })
	if st, _ := authNode(t, "2.1.308").claudeAuthStatus(context.Background(), "claude"); st.State != proto.ClaudeAuthUnknown ||
		st.Reason != proto.ClaudeAuthTimeout {
		t.Fatalf("status = %+v", st)
	}
	// Without zsh's banners: a missing claude prints nothing to read.
	if _, ok := parseClaudeAuthStatus("zsh:1: command not found: claude\n"); ok {
		t.Fatal("read a status out of command not found")
	}
}

// A managed launcher is asked through `personad launch <name>` with its gateway merged into the --settings, as it
// starts; a word that is no launcher here is refused, not run.
func TestClaudeAuthStatusManagedLauncher(t *testing.T) {
	fakeClaudeHome(t, "exit 1\n")
	seen := filepath.Join(t.TempDir(), "seen")
	bin := filepath.Join(os.Getenv("HOME"), "bin", "personad")
	script := `#!/bin/sh
printf '%s\n' "$@" > ` + shellQuote(seen) + `
while [ $# -gt 0 ]; do [ "$1" = --settings ] && cat "$2" >> ` + shellQuote(seen) + `; shift; done
echo '{"loggedIn": true, "authMethod": "oauth_token", "apiProvider": "firstParty"}'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	n := authNode(t, "2.1.308")
	n.bin = bin
	n.setManaged([]proto.ManagedLauncher{{Name: "gw", BaseURL: "https://gw.example.com", APIKey: "tok-test", Auth: "bearer"}})
	st, err := n.claudeAuthStatus(context.Background(), "gw")
	if err != nil || st.State != proto.ClaudeLoggedIn || st.AuthMethod != "oauth_token" {
		t.Fatalf("status = %+v (%v)", st, err)
	}
	b, _ := os.ReadFile(seen)
	if s := string(b); !strings.HasPrefix(s, "launch\ngw\n--settings\n") || !strings.Contains(s, "auth\nstatus\n--json\n") ||
		!strings.Contains(s, "https://gw.example.com") {
		t.Fatalf("ran with %s", s)
	}
	if _, err := n.claudeAuthStatus(context.Background(), "reboot"); err == nil {
		t.Fatal("an unknown launcher was run")
	}
}

// authProfileFixture sets up a node with a fake claude that reports logged_in only for CLAUDE_CONFIG_DIR values listed
// in $HOME/signed-in-profiles ("unset" stands for an unset CLAUDE_CONFIG_DIR, i.e. ~/.claude) and writes the
// CLAUDE_CONFIG_DIR each auth status run saw to $HOME/auth-profile-seen.
type authProfileFixture struct {
	n       *Node
	home    string
	private string
}

func newAuthProfileFixture(t *testing.T) *authProfileFixture {
	t.Helper()
	fakeClaudeHome(t, `dir="${CLAUDE_CONFIG_DIR-unset}"
printf '%s\n' "$dir" > "$HOME/auth-profile-seen"
if [ -f "$HOME/signed-in-profiles" ]; then
  while IFS= read -r want || [ -n "$want" ]; do
    if [ "$dir" = "$want" ]; then
      echo '{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty"}'
      exit 0
    fi
  done < "$HOME/signed-in-profiles"
fi
echo '{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}'
exit 1
`)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	t.Setenv("PERSONA_ASSISTANT", "")
	_ = os.Unsetenv("PERSONA_ASSISTANT")
	n := authNode(t, "2.1.308")
	n.state = newState()
	n.procs = map[string]*assistantProc{}
	n.paths = map[string]string{}
	n.cdirs = newClaudeDirs()
	n.cdirs.scan = func() map[int]procEnv { return nil }
	private := filepath.Join(n.dir, "profiles", "claude")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	return &authProfileFixture{n: n, home: n.home, private: private}
}

func (f *authProfileFixture) setSignedIn(t *testing.T, dirs ...string) {
	t.Helper()
	content := strings.Join(dirs, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(filepath.Join(f.home, "signed-in-profiles"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *authProfileFixture) call(t *testing.T, params map[string]any) (proto.ClaudeAuthStatus, string) {
	t.Helper()
	seenPath := filepath.Join(f.home, "auth-profile-seen")
	_ = os.Remove(seenPath)
	raw, err := nodeCall(t, f.n, "claude.auth_status", params)
	if err != nil {
		t.Fatalf("claude.auth_status(%v): %v", params, err)
	}
	var st proto.ClaudeAuthStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	b, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen profile: %v", err)
	}
	return st, strings.TrimSpace(string(b))
}

// Plain claude.auth_status without assistant context preserves machine-default / claude_start_dir behavior for older
// managers and non-assistant callers.
func TestClaudeAuthStatusPlainPreservesMachineDefault(t *testing.T) {
	f := newAuthProfileFixture(t)
	f.setSignedIn(t, f.private)

	st, seen := f.call(t, map[string]any{"name": "claude"})
	if st.State != proto.ClaudeNotLoggedIn || seen != "unset" {
		t.Fatalf("plain default: status=%+v seen=%q, want not_logged_in in unset", st, seen)
	}

	startDir := filepath.Join(f.home, "start-profile")
	f.n.claudeStartDir.Store(&startDir)
	f.setSignedIn(t, startDir)
	st, seen = f.call(t, map[string]any{"name": "claude"})
	if st.State != proto.ClaudeLoggedIn || seen != startDir {
		t.Fatalf("plain claude_start_dir signed in: status=%+v seen=%q, want logged_in in %q", st, seen, startDir)
	}

	f.setSignedIn(t, f.private)
	st, seen = f.call(t, map[string]any{"name": "claude"})
	if st.State != proto.ClaudeNotLoggedIn || seen != startDir {
		t.Fatalf("plain claude_start_dir signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, startDir)
	}
}

// With purpose "assistant", claude.auth_status inspects Persona's private assistant profile by default rather than
// machine-default ~/.claude or claude_start_dir.
func TestClaudeAuthStatusAssistantPrivateProfile(t *testing.T) {
	t.Run("private-signed-in-default-signed-out", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		f.setSignedIn(t, f.private)

		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
		if st.State != proto.ClaudeLoggedIn || st.AuthMethod != "claude.ai" || seen != f.private {
			t.Fatalf("status=%+v seen=%q, want logged_in (claude.ai) in %q", st, seen, f.private)
		}
	})

	t.Run("private-signed-out-default-signed-in", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		f.setSignedIn(t, "unset")

		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("status=%+v seen=%q, want not_logged_in in %q", st, seen, f.private)
		}
	})
}

// With purpose "assistant" and config_dir "legacy", claude.auth_status inspects the machine's legacy profile
// (~/.claude, claude_start_dir, or shell CLAUDE_CONFIG_DIR), while omitting config_dir stays in Persona's private
// profile.
func TestClaudeAuthStatusAssistantLegacyProfile(t *testing.T) {
	t.Run("machine-default", func(t *testing.T) {
		f := newAuthProfileFixture(t)

		f.setSignedIn(t, "unset")
		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeLoggedIn || seen != "unset" {
			t.Fatalf("legacy signed in: status=%+v seen=%q, want logged_in in unset", st, seen)
		}
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("private signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, f.private)
		}

		f.setSignedIn(t, f.private)
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeNotLoggedIn || seen != "unset" {
			t.Fatalf("legacy signed out: status=%+v seen=%q, want not_logged_in in unset", st, seen)
		}
	})

	t.Run("claude-start-dir", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		startDir := filepath.Join(f.home, "start-profile")
		f.n.claudeStartDir.Store(&startDir)

		f.setSignedIn(t, startDir)
		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeLoggedIn || seen != startDir {
			t.Fatalf("legacy claude_start_dir signed in: status=%+v seen=%q, want logged_in in %q", st, seen, startDir)
		}
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("default private with claude_start_dir: status=%+v seen=%q, want not_logged_in in %q", st, seen, f.private)
		}

		f.setSignedIn(t, f.private)
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeNotLoggedIn || seen != startDir {
			t.Fatalf("legacy claude_start_dir signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, startDir)
		}
	})

	t.Run("shell-config-dir", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		shellDir := filepath.Join(f.home, "shell-profile")
		writeFile(t, filepath.Join(f.home, ".zshrc"), "export CLAUDE_CONFIG_DIR="+shellQuote(shellDir)+"\n")

		f.setSignedIn(t, shellDir)
		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeLoggedIn || seen != shellDir {
			t.Fatalf("legacy shell profile signed in: status=%+v seen=%q, want logged_in in %q", st, seen, shellDir)
		}
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("default private over shell profile: status=%+v seen=%q, want not_logged_in in %q", st, seen, f.private)
		}

		f.setSignedIn(t, f.private)
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "config_dir": "legacy"})
		if st.State != proto.ClaudeNotLoggedIn || seen != shellDir {
			t.Fatalf("legacy shell profile signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, shellDir)
		}
	})
}

// An existing main conversation checks its recorded profile, except when recorded in another launcher's private
// profile: checking claude then falls back to claude's own new-conversation profile rather than blaming the other
// launcher's profile.
func TestClaudeAuthStatusAssistantConversationProfile(t *testing.T) {
	t.Run("recorded-default", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		startDir := filepath.Join(f.home, "start-profile")
		f.n.claudeStartDir.Store(&startDir)
		f.n.state.put("c-default", sessionState{Launcher: "claude", ConfigDir: "default"})

		f.setSignedIn(t, "unset")
		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "conversation": "c-default"})
		if st.State != proto.ClaudeLoggedIn || seen != "unset" {
			t.Fatalf("recorded default signed in: status=%+v seen=%q, want logged_in in unset", st, seen)
		}

		f.setSignedIn(t, f.private, startDir)
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "conversation": "c-default"})
		if st.State != proto.ClaudeNotLoggedIn || seen != "unset" {
			t.Fatalf("recorded default signed out: status=%+v seen=%q, want not_logged_in in unset", st, seen)
		}
	})

	t.Run("recorded-private", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		f.n.state.put("c-private", sessionState{Launcher: "claude", ConfigDir: f.private})

		f.setSignedIn(t, f.private)
		st, seen := f.call(t, map[string]any{
			"name":         "claude",
			"purpose":      "assistant",
			"conversation": "c-private",
			"config_dir":   "legacy",
		})
		if st.State != proto.ClaudeLoggedIn || seen != f.private {
			t.Fatalf("recorded private signed in: status=%+v seen=%q, want logged_in in %q", st, seen, f.private)
		}

		f.setSignedIn(t, "unset")
		st, seen = f.call(t, map[string]any{
			"name":         "claude",
			"purpose":      "assistant",
			"conversation": "c-private",
			"config_dir":   "legacy",
		})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("recorded private signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, f.private)
		}
	})

	t.Run("recorded-other-launcher-private-profile", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		otherProfile := filepath.Join(f.n.dir, "profiles", "gateway")
		if err := os.MkdirAll(otherProfile, 0o700); err != nil {
			t.Fatal(err)
		}
		f.n.state.put("c-other", sessionState{Launcher: "gateway", ConfigDir: otherProfile})

		// Checking claude must inspect claude's own private profile, not gateway's signed-out private profile.
		f.setSignedIn(t, f.private)
		st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "conversation": "c-other"})
		if st.State != proto.ClaudeLoggedIn || seen != f.private {
			t.Fatalf("other launcher private profile (claude private signed in): status=%+v seen=%q, want logged_in in %q",
				st, seen, f.private)
		}

		// Conversely, gateway's signed-in profile must not mask claude's signed-out private profile.
		f.setSignedIn(t, otherProfile)
		st, seen = f.call(t, map[string]any{"name": "claude", "purpose": "assistant", "conversation": "c-other"})
		if st.State != proto.ClaudeNotLoggedIn || seen != f.private {
			t.Fatalf("other launcher private profile (claude private signed out): status=%+v seen=%q, want not_logged_in in %q",
				st, seen, f.private)
		}

		// When new conversations are configured for legacy, claude falls back to that legacy profile.
		startDir := filepath.Join(f.home, "start-profile")
		f.n.claudeStartDir.Store(&startDir)
		f.setSignedIn(t, startDir)
		st, seen = f.call(t, map[string]any{
			"name":         "claude",
			"purpose":      "assistant",
			"conversation": "c-other",
			"config_dir":   "legacy",
		})
		if st.State != proto.ClaudeLoggedIn || seen != startDir {
			t.Fatalf("other launcher private profile with legacy fallback: status=%+v seen=%q, want logged_in in %q",
				st, seen, startDir)
		}
	})
}

// A wrapper launcher that sets its own CLAUDE_CONFIG_DIR checks that profile rather than Persona's private profile.
func TestClaudeAuthStatusAssistantWrapperLauncher(t *testing.T) {
	resetLauncherCache := func(t *testing.T) {
		t.Helper()
		launcherCache.Lock()
		oldAt, oldList := launcherCache.at, launcherCache.list
		launcherCache.at, launcherCache.list = time.Time{}, nil
		launcherCache.Unlock()
		t.Cleanup(func() {
			launcherCache.Lock()
			launcherCache.at, launcherCache.list = oldAt, oldList
			launcherCache.Unlock()
		})
	}

	t.Run("explicit-wrapper-dir", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		resetLauncherCache(t)
		wrapperDir := filepath.Join(f.home, "wrapper-profile")
		writeFile(t, filepath.Join(f.home, ".zshrc"),
			"_claude_launchers() { echo work; }\n"+
				`function work { CLAUDE_CONFIG_DIR=`+shellQuote(wrapperDir)+` command claude "$@"; }`+"\n")

		f.setSignedIn(t, wrapperDir)
		st, seen := f.call(t, map[string]any{"name": "work", "purpose": "assistant"})
		if st.State != proto.ClaudeLoggedIn || seen != wrapperDir {
			t.Fatalf("wrapper signed in: status=%+v seen=%q, want logged_in in %q", st, seen, wrapperDir)
		}

		f.setSignedIn(t, filepath.Join(f.n.dir, "profiles", "work"), "unset")
		st, seen = f.call(t, map[string]any{"name": "work", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != wrapperDir {
			t.Fatalf("wrapper signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, wrapperDir)
		}
	})

	t.Run("default-fallback-wrapper-dir", func(t *testing.T) {
		f := newAuthProfileFixture(t)
		resetLauncherCache(t)
		wrapperDir := filepath.Join(f.home, "wrapper-fallback-profile")
		writeFile(t, filepath.Join(f.home, ".zshrc"),
			"_claude_launchers() { echo workfb; }\n"+
				`function workfb { if [ -z "${CLAUDE_CONFIG_DIR+x}" ]; then CLAUDE_CONFIG_DIR=`+shellQuote(wrapperDir)+`; fi; CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" command claude "$@"; }`+"\n")

		f.setSignedIn(t, wrapperDir)
		st, seen := f.call(t, map[string]any{"name": "workfb", "purpose": "assistant"})
		if st.State != proto.ClaudeLoggedIn || seen != wrapperDir {
			t.Fatalf("fallback wrapper signed in: status=%+v seen=%q, want logged_in in %q", st, seen, wrapperDir)
		}

		f.setSignedIn(t, filepath.Join(f.n.dir, "profiles", "workfb"), "unset")
		st, seen = f.call(t, map[string]any{"name": "workfb", "purpose": "assistant"})
		if st.State != proto.ClaudeNotLoggedIn || seen != wrapperDir {
			t.Fatalf("fallback wrapper signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, wrapperDir)
		}
	})
}

// A scoped node (sessionScope != nil) always checks inside sessionScope.claude and never escapes via shell defaults,
// claude_start_dir, legacy, or conversation state.
func TestClaudeAuthStatusAssistantScopedNode(t *testing.T) {
	f := newAuthProfileFixture(t)
	workDir := filepath.Join(f.home, "work-node")
	scopeDir := filepath.Join(workDir, "assistant-m-work")
	scopedClaude := filepath.Join(workDir, "claude")
	scopedCodex := filepath.Join(workDir, "codex")
	for _, p := range []string{scopeDir, scopedClaude, scopedCodex} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PERSONA_HOME", workDir)
	t.Setenv(nodeSessionDirEnv, scopeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", scopedClaude)
	t.Setenv("CODEX_HOME", scopedCodex)
	scope, err := readNodeSessionScope(f.home, workDir)
	if err != nil {
		t.Fatal(err)
	}
	f.n.dir = workDir
	f.n.sessionScope = scope

	outsideDir := filepath.Join(f.home, "outside-profile")
	f.n.claudeStartDir.Store(&outsideDir)
	writeFile(t, filepath.Join(f.home, ".zshrc"), "export CLAUDE_CONFIG_DIR="+shellQuote(outsideDir)+"\n")
	f.n.state.put("c-outside", sessionState{Launcher: "claude", ConfigDir: outsideDir})

	f.setSignedIn(t, scopedClaude)
	for _, params := range []map[string]any{
		{"name": "claude", "purpose": "assistant"},
		{"name": "claude", "purpose": "assistant", "config_dir": "legacy"},
		{"name": "claude", "purpose": "assistant", "conversation": "c-outside"},
	} {
		st, seen := f.call(t, params)
		if st.State != proto.ClaudeLoggedIn || seen != scopedClaude {
			t.Fatalf("scoped %v: status=%+v seen=%q, want logged_in in %q", params, st, seen, scopedClaude)
		}
	}

	// An explicit external config_dir must be refused without executing claude.
	seenPath := filepath.Join(f.home, "auth-profile-seen")
	_ = os.Remove(seenPath)
	if _, err := nodeCall(t, f.n, "claude.auth_status", map[string]any{
		"name":       "claude",
		"purpose":    "assistant",
		"config_dir": outsideDir,
	}); err == nil {
		t.Fatal("scoped node accepted an external config_dir")
	}
	if _, err := os.Stat(seenPath); !os.IsNotExist(err) {
		t.Fatalf("scoped node ran claude for an external config_dir (%v)", err)
	}

	f.setSignedIn(t, outsideDir, "unset", f.private)
	st, seen := f.call(t, map[string]any{"name": "claude", "purpose": "assistant"})
	if st.State != proto.ClaudeNotLoggedIn || seen != scopedClaude {
		t.Fatalf("scoped signed out: status=%+v seen=%q, want not_logged_in in %q", st, seen, scopedClaude)
	}
}

// Assistant-profile claude.auth_status sets PERSONA_ASSISTANT=1 so `personad launch` preserves the resolved
// CLAUDE_CONFIG_DIR (a recorded conversation profile or a scoped node's profile) instead of letting a managed
// launcher's own Env override it; plain non-assistant claude.auth_status leaves PERSONA_ASSISTANT unset.
func TestClaudeAuthStatusAssistantManagedLauncherPreservesProfile(t *testing.T) {
	f := newAuthProfileFixture(t)
	outsideDir := filepath.Join(f.home, "managed-env-profile")
	recordedDir := filepath.Join(f.n.dir, "profiles", "gwenv")
	if err := os.MkdirAll(recordedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "managed-launch-env")
	bin := filepath.Join(f.home, "bin", "personad")
	script := `#!/bin/sh
printf 'ASSISTANT=%s\nCONFIG=%s\n' "${PERSONA_ASSISTANT-unset}" "${CLAUDE_CONFIG_DIR-unset}" > ` + shellQuote(seen) + `
while [ $# -gt 0 ]; do
  if [ "$1" = --settings ]; then
    cat "$2" >> ` + shellQuote(seen) + `
    break
  fi
  shift
done
if [ "${PERSONA_ASSISTANT-}" = "1" ]; then
  dir="${CLAUDE_CONFIG_DIR-unset}"
else
  dir=` + shellQuote(outsideDir) + `
fi
printf '%s\n' "$dir" > "$HOME/auth-profile-seen"
if [ -f "$HOME/signed-in-profiles" ] && grep -Fxq -- "$dir" "$HOME/signed-in-profiles"; then
  echo '{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty"}'
  exit 0
fi
echo '{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}'
exit 1
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f.n.bin = bin
	f.n.setManaged([]proto.ManagedLauncher{{
		Name:    "gwenv",
		BaseURL: "https://gw.example.com",
		APIKey:  "tok-test",
		Auth:    "bearer",
		Env:     "CLAUDE_CONFIG_DIR=" + outsideDir,
	}})

	// Existing conversation recorded in recordedDir: assistant auth_status runs with PERSONA_ASSISTANT=1 and strips
	// CLAUDE_CONFIG_DIR from merged settings so recordedDir wins over the managed launcher's Env.
	f.n.state.put("c-managed", sessionState{Launcher: "gwenv", ConfigDir: recordedDir})
	f.setSignedIn(t, recordedDir)
	st, dir := f.call(t, map[string]any{"name": "gwenv", "purpose": "assistant", "conversation": "c-managed"})
	if st.State != proto.ClaudeLoggedIn || dir != recordedDir {
		t.Fatalf("assistant managed conversation: status=%+v seen=%q, want logged_in in %q", st, dir, recordedDir)
	}
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if out := string(b); !strings.Contains(out, "ASSISTANT=1\n") || !strings.Contains(out, "CONFIG="+recordedDir+"\n") ||
		strings.Contains(out, `"CLAUDE_CONFIG_DIR"`) {
		t.Fatalf("assistant managed launch env/settings = %s", out)
	}

	// Plain non-assistant claude.auth_status leaves PERSONA_ASSISTANT unset (terminal Agent semantics unchanged).
	f.setSignedIn(t, outsideDir)
	st, dir = f.call(t, map[string]any{"name": "gwenv"})
	if st.State != proto.ClaudeLoggedIn || dir != outsideDir {
		t.Fatalf("plain managed launcher: status=%+v seen=%q, want logged_in in %q", st, dir, outsideDir)
	}
	b, err = os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if out := string(b); !strings.Contains(out, "ASSISTANT=unset\n") {
		t.Fatalf("plain managed launcher unexpectedly set PERSONA_ASSISTANT: %s", out)
	}

	// Scoped node with a managed launcher setting an external CLAUDE_CONFIG_DIR stays inside sessionScope.claude.
	workDir := filepath.Join(f.home, "work-managed")
	scopeDir := filepath.Join(workDir, "assistant-m-work")
	scopedClaude := filepath.Join(workDir, "claude")
	scopedCodex := filepath.Join(workDir, "codex")
	for _, p := range []string{scopeDir, scopedClaude, scopedCodex} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PERSONA_HOME", workDir)
	t.Setenv(nodeSessionDirEnv, scopeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", scopedClaude)
	t.Setenv("CODEX_HOME", scopedCodex)
	scope, err := readNodeSessionScope(f.home, workDir)
	if err != nil {
		t.Fatal(err)
	}
	f.n.dir = workDir
	f.n.sessionScope = scope
	f.setSignedIn(t, scopedClaude)
	st, dir = f.call(t, map[string]any{"name": "gwenv", "purpose": "assistant"})
	if st.State != proto.ClaudeLoggedIn || dir != scopedClaude {
		t.Fatalf("scoped managed launcher: status=%+v seen=%q, want logged_in in %q", st, dir, scopedClaude)
	}
}
