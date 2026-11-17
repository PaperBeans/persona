package node

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// A start that ended with shell_waiting or agent_start_stalled keeps its tab (keepStartTab, #2570). Whoever remedies it
// (the assistant's main conversation, through the hub) may type into that tab while it is kept and no agent runs
// there (start.keys), and start again in it (session.start's in_pane) instead of opening another.

// startKeysText and startKeysCount bound what one start.keys types: a short answer, not a command line.
const (
	startKeysText  = 32 // runes of text
	startKeysCount = 8  // named keys
)

// keptStartTab is the kept tab of a failed start on pane while it is still kept and no agent runs there.
func (n *Node) keptStartTab(pane string) (keptStart, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.keptStartTabLocked(pane)
}

// keptStartTabLocked is keptStartTab with n.mu already held. Reusing a tab needs its expiry, binding and agent checks
// plus its deletion in one critical section, so two session.start requests cannot claim the same kept tab.
func (n *Node) keptStartTabLocked(pane string) (keptStart, bool) {
	k, ok := n.keptStarts[pane]
	if !ok || !time.Now().Before(k.until) {
		return keptStart{}, false
	}
	if _, bound := n.panes[pane]; bound {
		return keptStart{}, false
	}
	for _, a := range n.agents {
		if a.PaneID == pane {
			return keptStart{}, false
		}
	}
	return k, true
}

// errStartTabGone refuses typing into, or starting in, a pane that is not a failed start's kept tab (any more): it
// expired, an agent runs there, or it never was one.
var errStartTabGone = refusal("start_tab_gone", "that tab is not a failed start's kept tab any more (it expired, or an agent runs there)", nil)

// startKeys types text (as is, no enter) and then presses keys (Persona's key names: "enter", "esc", "ctrl+c", "y"…)
// into the kept tab of a failed start on pane. Only such a tab takes them, and only a short answer.
func (n *Node) startKeys(ctx context.Context, pane, text string, keys []string) error {
	if pane == "" || text == "" && len(keys) == 0 {
		return errors.New("start.keys needs a pane and text or keys")
	}
	if utf8.RuneCountInString(text) > startKeysText || len(keys) > startKeysCount {
		return refusal("too_large", "start.keys types at most 32 characters and 8 keys", nil)
	}
	// start.keys must not type into a pane that another request claims as a fresh start while it waits.
	unlock := n.typing.lock(workAgentPaneLockPrefix + pane)
	defer unlock()
	if _, ok := n.keptStartTab(pane); !ok {
		return errStartTabGone
	}
	h, ref, err := n.paneHost(pane)
	if err != nil {
		return err
	}
	if text != "" {
		if err := h.SendText(ctx, ref, text); err != nil {
			return err
		}
	}
	if len(keys) > 0 {
		return h.SendKeys(ctx, ref, keys...)
	}
	return nil
}

// reusedTabKey carries the kept tab a start runs in instead of a new one (withKeptTab), for newTab.
type reusedTabKey struct{}

// reusingKeptTab is ctx for a start in pane, the kept tab of an earlier failed start, when it still is one: the start
// takes it over (it is no longer kept; this start keeps or closes it as its own, startTabFailed). Else ctx as it is,
// and the start opens a new tab. An Orca terminal takes one launch (orcaHost.Run): a kept tab whose start already typed
// its line there (agent_start_stalled) is closed instead, and the start opens a new tab rather than fail (#9534); one
// where nothing was typed (shell_waiting, answered with start.keys) is still started in.
func (n *Node) reusingKeptTab(ctx context.Context, pane string) context.Context {
	if pane == "" {
		return ctx
	}
	n.mu.Lock()
	k, ok := n.keptStartTabLocked(pane)
	if !ok {
		n.mu.Unlock()
		return ctx
	}
	delete(n.keptStarts, pane)
	n.mu.Unlock()
	if o, isOrca := k.launch.(*orcaHost); isOrca && !o.launchable(k.created.Pane) {
		n.closeNewTab(ctx, k.launch, k.created, k.ws)
		return ctx
	}
	return context.WithValue(ctx, reusedTabKey{}, k.created)
}
