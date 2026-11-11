package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// "default" records Claude's unset-directory behavior (~/.claude/projects but
// ~/.claude.json). An absolute ~/.claude is not equivalent: its global config
// is ~/.claude/.claude.json. "legacy" is an input only, resolved once and saved.
func (n *Node) assistantProfileSelection(ctx context.Context, id, launcher, runtime, asked string, resume bool) (string, error) {
	normalize := func(dir string) (string, error) {
		if dir == "legacy" {
			dir = n.legacyAssistantProfile(ctx, launcher, runtime)
		}
		if dir == "default" {
			return dir, nil
		}
		dir = proto.ExpandHome(dir, n.home)
		if !filepath.IsAbs(dir) {
			return "", errors.New("config_dir must be absolute, start with ~/, or be default or legacy")
		}
		return filepath.Clean(dir), nil
	}
	if n.sessionScope != nil {
		if asked != "" && asked != n.sessionScope.claude {
			return "", errors.New("config_dir must stay in the node's session scope")
		}
		return n.sessionScope.claude, nil
	}
	selected := ""
	if resume {
		st, _ := n.state.get(id)
		selected = st.ConfigDir
		if selected == "" {
			n.mu.Lock()
			p := n.procs[id]
			n.mu.Unlock()
			if p != nil {
				p.mu.Lock()
				selected = p.model.ConfigDir
				p.mu.Unlock()
			}
		}
		if selected == "" {
			if path := n.transcriptPath(id); path != "" {
				project := filepath.Dir(path)
				projects := filepath.Dir(project)
				if filepath.Base(projects) == "projects" {
					selected = filepath.Dir(projects)
					if sameScopePath(selected, filepath.Join(n.home, ".claude")) {
						// Preserve the old unset-profile global config, unless
						// the old parser explicitly selected this same directory.
						legacy := n.legacyAssistantProfile(ctx, launcher, runtime)
						if legacy == "default" || !sameScopePath(legacy, selected) {
							selected = "default"
						}
					}
				}
			}
		}
		if selected == "" {
			selected = n.legacyAssistantProfile(ctx, launcher, runtime)
		}
	} else if asked != "" {
		selected = asked
	} else {
		selected = n.assistantLauncherProfile(ctx, launcher, runtime)
		if selected == "" {
			root := n.dir
			if root == "" {
				root = identity.DataDir(n.home)
			}
			selected = filepath.Join(root, "profiles", launcher)
		}
	}
	selected, err := normalize(selected)
	if err != nil {
		return "", err
	}
	if resume && asked != "" {
		explicit, err := normalize(asked)
		if err != nil {
			return "", err
		}
		if !sameAssistantProfile(selected, explicit) {
			return "", errors.New("config_dir cannot move an existing conversation; start a new conversation instead")
		}
	}
	// A wrapper can override a prefix assignment. Refuse a conflicting wrapper
	// rather than claim to resume one profile while actually opening another.
	if _, managed := n.managedLauncher(launcher); !managed {
		if own := n.assistantLauncherProfile(ctx, launcher, runtime); own != "" {
			own, err = normalize(own)
			if err != nil {
				return "", err
			}
			if !sameAssistantProfile(selected, own) {
				return "", errors.New("config_dir conflicts with the launcher's own profile; restore that profile or use a compatible launcher")
			}
		}
	}
	return selected, nil
}

func sameAssistantProfile(a, b string) bool {
	if a == "default" || b == "default" {
		return a == b
	}
	return sameScopePath(a, b)
}

func (n *Node) assistantLauncherProfile(ctx context.Context, launcher, runtime string) string {
	var env map[string]string
	if l, managed := n.managedLauncher(launcher); managed {
		env = providerEnv(l, "", "", true)
	} else if runtime == proto.AssistantRuntimeAgentSDK && launcher == "claude" {
		return "" // the SDK runs the executable, not a shell function or alias
	} else if p, err := n.launcherProbe(ctx, launcher); err == nil {
		env = p.Env
	}
	if dir, set := env["CLAUDE_CONFIG_DIR"]; set {
		if launcher == "claude" {
			if d := n.claudeStartDir.Load(); d != nil && proto.ExpandHome(*d, n.home) == dir {
				return "" // the probe's machine assignment is not a launcher-owned profile
			}
		}
		if dir == "" {
			return "default"
		}
		return dir
	}
	return ""
}

// Capture the pre-change rules, only for a pre-existing unrecorded session (or
// an explicit new-session "legacy" request). Never guess the new private path.
func (n *Node) legacyAssistantProfile(ctx context.Context, launcher, runtime string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	_, managed := n.managedLauncher(launcher)
	if runtime != proto.AssistantRuntimeAgentSDK || launcher != "claude" && !managed {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := loginShell(ctx, `printf '\n__PERSONA_LEGACY_PROFILE__%s\n' "$CLAUDE_CONFIG_DIR"`)
		if out, err := cmd.Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if value, ok := strings.CutPrefix(line, "__PERSONA_LEGACY_PROFILE__"); ok {
					dir = value
				}
			}
		}
	}
	if own := n.assistantLauncherProfile(ctx, launcher, runtime); own != "" {
		return own
	}
	if launcher == "claude" {
		if d := n.claudeStartDir.Load(); d != nil && *d != "" {
			dir = *d
		}
	}
	if dir == "" {
		return "default"
	}
	return proto.ExpandHome(dir, n.home)
}

func (n *Node) assistantConfigDir(launcher, selected string) string {
	if n.sessionScope != nil {
		return n.sessionScope.claude
	}
	if selected == "" {
		root := n.dir
		if root == "" {
			root = identity.DataDir(n.home)
		}
		selected = filepath.Join(root, "profiles", launcher)
	}
	if selected == "default" {
		return ""
	}
	return proto.ExpandHome(selected, n.home)
}

func (n *Node) assistantConfigPath(selected string) string {
	if selected == "default" {
		return filepath.Join(n.home, ".claude")
	}
	return proto.ExpandHome(selected, n.home)
}

func (n *Node) claudeProcessEnv(base []string, dir string) []string {
	if n.sessionScope != nil {
		dir = n.sessionScope.claude
	}
	return identity.Environment(base, "CLAUDE_CONFIG_DIR="+dir)
}

// Terminal Agents keep launcherCommand unchanged; only this assistant start
// overrides a shell's default after startup, using its saved selection.
func (n *Node) launcherCommandIn(name, dir string, q func(string) string) string {
	cmd := name
	if _, managed := n.managedLauncher(name); managed {
		cmd = q(n.bin) + " launch " + name
	}
	return "CLAUDE_CONFIG_DIR=" + q(dir) + " " + cmd
}
