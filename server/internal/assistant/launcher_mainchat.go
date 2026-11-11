package assistant

import (
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
)

// ---- which launchers can run the assistant's conversations ----
//
// The main chat, a scheduled task's conversation and the side conversations run headless on their machine: claude -p
// or codex app-server started through the launcher's shell command (node/assistant_start.go), or a runtime that
// declared caps.assistant. The picker (?purpose=assistant) lists assistantLaunchers (modelProviderCompatible), and the
// check before each message (sendModel) holds a launcher to the same list through mainChatFit. On top of that, the
// pickers leave out a launcher whose latest health check failed (launcher_health_check.go, withHealth): health keeps a
// message from being refused as launcher_missing on a wrong read, but a failing launcher is not refused here, and a
// model configuration that names one is left alone.

// mainChatFit is whether launcher name can run an assistant conversation on machine: in the main chat's list (fit),
// one the machine has that can't run it (refused, with why in the user's language), or not there at all (gone).
// Plain claude fits without asking; a list that can't be read fits too, as launchersHere has it: nothing is refused
// or replaced on a guess.
type mainChatFit int

const (
	mainChatFits mainChatFit = iota
	mainChatRefused
	mainChatGone
)

func (s *Service) mainChatFit(ctx context.Context, machine, name string) (mainChatFit, error) {
	if name == "" || name == "claude" {
		return mainChatFits, nil
	}
	ctx = withSettingsMemo(ctx)
	if m, managed := s.managedNamed(ctx, name); managed { // the same on every machine in its scope: no list to ask
		allowed, err := s.assistantProviders(ctx, machine, nil)
		switch {
		case !s.usableOn(ctx, m, machine):
			return mainChatGone, nil
		case err != nil || allowed[name]:
			return mainChatFits, nil
		}
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	picker, err := s.pickerLaunchers(cctx, machine)
	if err != nil || !slices.ContainsFunc(picker, func(l ConfiguredLauncher) bool { return l.Source != "" && compat.Source(l.Source) != "persona" }) {
		return mainChatFits, nil // the machine's own list did not come: a shell launcher may still be there
	}
	fits, err := s.assistantLaunchersOf(cctx, machine, picker)
	named := func(l ConfiguredLauncher) bool { return l.Name == name }
	switch {
	case err != nil || slices.ContainsFunc(fits, named):
		return mainChatFits, nil
	case slices.ContainsFunc(picker, named):
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	return mainChatGone, nil
}

// finishMainChatModel validates the binding after model resolution, including a default or a legacy fallback that
// was not the caller's explicit pick. A bad default gives way to a launcher from the current compatible picker;
// plain claude is preferred only when that list verified it. With no verified compatible launcher, nothing is sent.
func (s *Service) finishMainChatModel(ctx context.Context, machine, purpose string, params map[string]any, picked bool) error {
	name, _ := params["launcher"].(string)
	fit, refusal := s.mainChatFit(ctx, machine, name)
	if fit == mainChatFits {
		return nil
	}
	launchers, err := s.assistantLaunchers(ctx, machine)
	if err != nil {
		return err
	}
	fallback := ""
	for _, l := range launchers {
		if l.Name == "claude" {
			fallback = l.Name
			break
		}
		if fallback == "" {
			fallback = l.Name
		}
	}
	if fallback == "" {
		if refusal != nil {
			return refusal
		}
		return s.mainChatRefusal(ctx, machine, name)
	}
	s.logf("[%s] resolved assistant launcher %s can't run it, using %s", machine, name, fallback)
	params["launcher"] = fallback
	delete(params, "model")
	if picked {
		return s.applyPickedModel(ctx, params)
	}
	return s.applyModel(ctx, purpose, params)
}

// mainChatRefusal says that launcher can't run the assistant on machine, for the user's language.
func (s *Service) mainChatRefusal(ctx context.Context, machine, launcher string) error {
	d, _ := s.device(ctx, machine)
	return i18n.Err("assistant-start.launcher_not_main_chat", i18n.Args{"launcher": launcher, "machine": firstNonEmpty(d.Name, machine)})
}
