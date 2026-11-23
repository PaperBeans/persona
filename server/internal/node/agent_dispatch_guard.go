package node

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The destination owns path resolution: the hub cannot resolve a remote symlink.
func (n *Node) guardAgentDirectory(ctx context.Context, cwd string) error {
	cwd = proto.ExpandHome(strings.TrimSpace(cwd), n.home)
	blocked := identity.IsAssistantDir(cwd)
	resolved, err := filepath.EvalSymlinks(cwd)
	if err == nil {
		cwd = resolved
	} else {
		cwd = filepath.Clean(cwd)
	}
	blocked = blocked || identity.IsAssistantDir(cwd)
	candidate, statErr := os.Stat(cwd)
	for _, state := range []string{n.dir, identity.DataDir(n.home)} {
		dirs, _ := os.ReadDir(state)
		for _, dir := range dirs {
			if identity.IsManagerDirName(strings.ToLower(dir.Name()), "tracks") {
				// A track's directory (tracks/<slug>, #13472), however its path is spelled.
				tracks := filepath.Join(state, dir.Name())
				if info, err := os.Stat(tracks); statErr == nil && err == nil && identity.ValidTrackSlug(strings.ToLower(filepath.Base(cwd))) {
					if parent, err := os.Stat(filepath.Dir(cwd)); err == nil && os.SameFile(parent, info) {
						blocked = true
					}
				}
				continue
			}
			if !identity.IsManagerDirName(dir.Name(), "assistant") && !identity.IsManagerDirName(dir.Name(), identity.SpaceHealth) {
				continue
			}
			expected := filepath.Join(state, dir.Name())
			if cwd == expected {
				blocked = true
			}
			info, err := os.Stat(expected)
			if statErr == nil && err == nil && os.SameFile(candidate, info) {
				blocked = true
			}
		}
	}
	if blocked {
		return refusal("agent_assistant_workspace", i18n.T(i18n.From(ctx), "rpc-errors.agent_assistant_workspace"), nil)
	}
	return nil
}

// agentGOOS is the platform whose Agent rules a new session.start adds: this machine's. The package's tests set a
// platform without any (TestMain), so a start's task instructions are what the test sent on every platform.
var agentGOOS = runtime.GOOS

func agentPlatformInstructions(goos, rules string) string {
	if goos != "darwin" {
		return rules
	}
	return strings.TrimSpace(rules + "\n\n" + prompts.RenderAgentMacRules())
}
