package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nwverse/persona/server/internal/host"
)

// Persona's hooks (questions, permission requests and turn ends go to the phone; docs/integrations/claude-integration.md) reach
// the claude it starts as a plugin of their own, loaded for that session only (#1411). A start can give claude any
// number of --plugin-dir, each adding to the rest, but Claude Code keeps only the last --settings: a launcher (an
// alias, or a function in ~/.zshrc, whose words the node cannot read) may pass its own, with the gateway, its token
// and the model in it, and Persona's hooks in a --settings after it dropped all of that ("Not logged in"). A claude
// that does not load plugins (claudeLoadsPlugins, agentplugin.go) still gets them in --settings (launcher_settings.go).

// hooksPluginName is the plugin's name in a session; a session loads one of the two.
const hooksPluginName = "persona-hooks"

// hooksPlugin is the plugin with the hooks of kind's starts: "claude" (the agents Persona starts, the hooks of
// claude-settings.json) or "assistant" (its assistant, assistant-settings.json's).
func (n *Node) hooksPlugin(kind string) string { return filepath.Join(n.dir, kind+"-hooks") }

// hooksPluginFiles is the plugin with hooks (a settings file's "hooks") as files, by path under its directory; its
// version is theirs (a hash), so it changes when they do.
func hooksPluginFiles(hooks any) (map[string][]byte, error) {
	return pluginFiles(hooksPluginName, "Persona's hooks for the sessions it starts: questions, permission requests and turn ends go to the phone", hooks)
}

// The live replies of the Agents Persona starts (livereply.go, #1309) come from Claude Code's MessageDisplay hook, in a
// plugin of its own that a start loads only while the machine's setting is on (Settings › Claude Code, node.configure
// claude_live_replies; on unless the user turned it off). Registering the hook costs the terminal something: Claude
// then shows a reply a batch of lines at a time, each once the hook is done, instead of token by token. Claude reads
// plugins when it starts, so the setting reaches a session at its next start (herdr's restore included: the resume
// command is given again when it changes, setLiveReplies); one already running keeps what it started with.
const (
	liveRepliesPluginName = "persona-live-replies"
	liveRepliesDir        = "claude-live-replies" // under the node's directory, beside claude-hooks
)

// liveRepliesPluginFiles is the live replies plugin for a claude whose hook program is bin, of the node whose directory
// is dir.
func liveRepliesPluginFiles(bin, dir string) (map[string][]byte, error) {
	return pluginFiles(liveRepliesPluginName, "Persona's live replies: an Agent's reply goes to the phone as Claude shows it",
		map[string]any{"MessageDisplay": displayHooks(bin, dir)})
}

// liveRepliesPlugin is the --plugin-dir of the live replies for an Agent's start whose hooks plugin is hooks ("" when
// it has none: a claude that loads no plugins gets its hooks in --settings, and the event is newer than that): "" while
// the setting is off or the plugin is not written.
func (n *Node) liveRepliesPlugin(hooks string) string {
	if hooks == "" || n.live.off.Load() {
		return ""
	}
	dir := filepath.Join(n.dir, liveRepliesDir)
	if _, err := os.Stat(filepath.Join(dir, "hooks", "hooks.json")); err != nil {
		return ""
	}
	return dir
}

// setLiveReplies is node.configure claude_live_replies. The Claude sessions Persona started in herdr get their resume
// command again with the live replies plugin added or dropped, so a restore follows the setting as a start does.
func (n *Node) setLiveReplies(on bool) {
	if n.live.off.Swap(!on) == !on {
		return
	}
	if n.hosts == nil {
		return
	}
	h, ok := n.hosts.Get(host.Herdr).(*herdrHost)
	if !ok || h.c == nil {
		return
	}
	type todo struct {
		pane string
		spec claudeLaunch
	}
	var work []todo
	n.mu.Lock()
	for pane, spec := range n.launches {
		if spec.Agent != "" && spec.Agent != "claude" {
			continue
		}
		if d := n.liveRepliesPlugin(spec.Hooks); d != spec.Display {
			spec.Display = d
			n.launches[pane] = spec
			work = append(work, todo{pane, spec})
		}
	}
	n.mu.Unlock()
	if len(work) == 0 {
		return
	}
	go func() {
		for _, w := range work {
			n.reportResume(context.Background(), h, w.pane, w.spec)
		}
	}()
}

// pluginFiles is a plugin named name with hooks as files, by path under its directory; its version is the hooks' hash,
// so it changes when they do.
func pluginFiles(name, description string, hooks any) (map[string][]byte, error) {
	b, err := json.Marshal(hooks)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	manifest := map[string]any{"name": name, "version": "1.0.0-" + hex.EncodeToString(sum[:6]), "description": description}
	mb, err := marshalJSON(manifest)
	if err != nil {
		return nil, err
	}
	hb, err := marshalJSON(map[string]any{"hooks": hooks})
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		filepath.Join(".claude-plugin", "plugin.json"): mb,
		filepath.Join("hooks", "hooks.json"):           hb,
	}, nil
}

// claudeStartSettings is how a claude start of launcher gets Persona's hooks for kind ("claude" / "assistant") and a
// managed launcher's gateway: settings is the --settings to pass and hooks the --plugin-dir, "" for none.
//
// With a claude that loads plugins, the hooks are their plugin and settings is only a managed launcher's env (its
// gateway must beat the user's global settings env; Persona's own launcher has no settings of its own to lose):
// whatever --settings a launcher passes is then the one claude keeps. Otherwise, or while the plugin is not written,
// as before: the hooks' settings file, merged with an alias's own --settings or a managed launcher's env.
func (n *Node) claudeStartSettings(ctx context.Context, launcher, kind, cwd string, configDir ...string) (settings, hooks string) {
	ours := filepath.Join(n.dir, kind+"-settings.json")
	plugin := n.hooksPlugin(kind)
	if _, err := os.Stat(filepath.Join(plugin, "hooks", "hooks.json")); err != nil || !n.claudeLoadsPlugins(ctx) {
		return n.claudeSettings(ctx, launcher, ours, cwd, configDir...), ""
	}
	if m, ok := n.managedLauncher(launcher); ok {
		if path, err := n.mergeManagedSettings(launcher, m, "", configDir...); err != nil {
			n.logf("launcher %s: its gateway not written for the start: %v", launcher, err)
		} else {
			settings = path
		}
	}
	return settings, plugin
}
