package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- Claude's desktop app as the launcher of Model configuration › Agents ----
//
// A computer whose Claude desktop app keeps Claude Code sessions lists the launcher claude-desktop: a New Agent start
// with it opens its session in the app (node/claudedesktop_new.go; docs/integrations/claude-integration.md, "New
// sessions in the desktop app"). Settings › Model providers does not list it (listedRCLauncher): it has no command,
// gateway or test of its own. Model configuration offers it to Agents (agent.new) alone, so a person's New Agent can
// open in the app by default:
//
//   - the main chat, scheduled tasks and the background uses run headless or through model.run, which an app cannot,
//     so they never offer it (modelProviderCompatible);
//   - the Inbox AI twin and Lark /agent, Agents' headless uses, run on Claude Code while they follow it (modelBaseline);
//   - a start Persona makes itself (a task, agent_start, a resume) does not open the app from the default, which brings
//     the app to the front and runs on the app's own settings: it gets the launcher a computer without the default gets
//     (fitLauncher). Only a start a person makes in New Agent (AgentStartParams) follows it there;
//   - a computer without the app gets that launcher too, and New Agent's form says so before the start
//     (desktopDefaultHere).
//
// The Codex App's codex-app stays an explicit pick (docs/integrations/launchers.md).

// claudeDesktopLauncher is the launcher of Claude's desktop app (node/claudedesktop_new.go).
const claudeDesktopLauncher = "claude-desktop"

// desktopAppEntry: l is the desktop app's launcher as a computer lists it, not a login shell's of that name.
func desktopAppEntry(l proto.Launcher) bool {
	return l.Name == claudeDesktopLauncher && compat.Source(l.Source) == proto.SourceClaudeDesktop
}

// claudeDesktopProvider is the desktop app's launcher as a provider of Model configuration: the machines whose last list
// offers it. ok false when none does, or when have already holds a provider of that ID (one kept in Persona, a login
// shell's launcher of that name).
func (s *Service) claudeDesktopProvider(ctx context.Context, zshrc []zshrcLaunchers, have []ModelProvider) (ModelProvider, bool) {
	id := launcherProvider(claudeDesktopLauncher)
	if slices.ContainsFunc(have, func(p ModelProvider) bool { return p.ID == id }) {
		return ModelProvider{}, false
	}
	p := ModelProvider{ID: id, Name: mc(s.requestCtxLang(ctx), "provider_claude_desktop"), Kind: "launcher",
		Source: proto.SourceClaudeDesktop, Agent: "claude", Hidden: slices.Contains(s.hiddenLaunchers(ctx), claudeDesktopLauncher)}
	for _, m := range zshrc {
		if slices.ContainsFunc(m.Launchers, desktopAppEntry) && !slices.Contains(p.Machines, m.MachineID) {
			p.Machines = append(p.Machines, m.MachineID)
		}
	}
	return p, len(p.Machines) > 0
}

// desktopAppProvider: provider (launcher.<name>) is the desktop app's launcher, not one kept in Persona under its name.
// Told by the name alone, so it costs no machine's list: a login shell's launcher of that name counts too, and only
// keeps the uses above off it.
func (s *Service) desktopAppProvider(ctx context.Context, provider string) bool {
	name, ok := strings.CutPrefix(provider, "launcher.")
	return ok && name == claudeDesktopLauncher &&
		!slices.ContainsFunc(s.storedLaunchers(ctx), func(l storedLauncher) bool { return l.Name == name })
}

// personStartKey marks a start a person makes in New Agent (AgentStartParams): the only one a default desktop app's
// launcher opens in the app.
type personStartKey struct{}

func withPersonStart(ctx context.Context) context.Context {
	return context.WithValue(ctx, personStartKey{}, true)
}

func personStart(ctx context.Context) bool {
	on, _ := ctx.Value(personStartKey{}).(bool)
	return on
}

// desktopDefaultHere is New Agent's default on machine m (the GET /machines/{mid}/launchers answer) when def, Agents'
// binding, runs the desktop app's launcher and m lacks it: the launcher the start gives way to there (fitLauncher's
// launcherInstead), with its own model and effort, as the start will run. def as it is otherwise. list is the
// pickers' list, which leaves out a hidden launcher; a hidden default still runs (#4659), so then m's list is read as
// the start reads it.
func (s *Service) desktopDefaultHere(ctx context.Context, m Device, def map[string]any, list []ConfiguredLauncher) map[string]any {
	name, _ := def["provider"].(string)
	if name == "" || !s.desktopAppProvider(ctx, launcherProvider(name)) {
		return def
	}
	here := make([]proto.Launcher, 0, len(list))
	for _, l := range list {
		here = append(here, l.Launcher)
	}
	if slices.Contains(s.hiddenLaunchers(ctx), name) {
		all, known := s.launchersHere(withUnhidden(ctx, name), m.ID)
		if !known {
			return def // nothing is replaced on a guess, as at the start
		}
		here = all
	}
	if slices.ContainsFunc(here, func(l proto.Launcher) bool { return l.Name == name }) {
		return def
	}
	return launcherPickerBinding(ModelBinding{Provider: launcherProvider(launcherInstead(m, here))})
}
