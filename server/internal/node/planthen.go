package node

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A session started in plan mode goes on in one mode once its plan is approved, whichever surface approves it
// (#3784): the mode its start named (session.start's plan_then, sessionState.PlanThen). Approved on Persona's card,
// the hook's answer sets it (hooks.go). Approved in the terminal's own dialog, Claude Code applies the option picked
// there; the node watches the footer from the moment the card is raised and moves the session to the recorded mode when it differs.

// planThenOf is the mode to record for a start in mode with the asked-for plan_then: only for a plan-mode start, a
// mode Persona knows, and not plan itself.
func planThenOf(mode, then string) string {
	if mode != "plan" || then == "" || then == "plan" || !slices.Contains(permissionModes, then) {
		return ""
	}
	return then
}

// planThenFlags are the extra claude flags of a plan-mode start that goes on in bypassPermissions: without one of them
// that mode is not in the running session's shift+tab cycle, and neither the hook's setMode nor a later change can
// reach it (argsCanBypass).
func planThenFlags(mode, then string) string {
	if planThenOf(mode, then) == proto.ModeBypass {
		return " --allow-dangerously-skip-permissions"
	}
	return ""
}

// planThen is the mode session id goes on in after its plan, "" when its start named none.
func (n *Node) planThen(id string) string {
	if st, ok := n.state.get(id); ok {
		return st.PlanThen
	}
	return ""
}

// How long, and how often, the node looks at the terminal while a plan waits to be decided. Long: a plan approved in
// the terminal with "manually approve" is followed at once by a permission dialog, which hides the footer until it is
// answered.
var (
	planThenWatch = 10 * time.Minute
	planThenEvery = 3 * time.Second
)

// planThenSet bounds pressing the session over to its mode (tuiSetMode: a read and a key per step of the cycle).
const planThenSet = 20 * time.Second

// planThenAfterTerminal follows a plan from the moment its card is raised: once the footer shows a mode other than
// plan, the plan was approved in the terminal, and the session is moved to its recorded mode when the dialog picked
// another. A plan sent back (the footer stays on plan), a dialog in front (no footer) and a session with no recorded
// mode are left alone. The returned stop ends the watch: the card was answered, and its answer set the mode.
func (n *Node) planThenAfterTerminal(id string) (stop func()) {
	want := n.planThen(id)
	if want == "" {
		return func() {}
	}
	// Both read here, not in the goroutine: a test shortens them (fastPlanThen) and puts them back when it ends, which
	// can come before a watch it stopped at once has even started (#3942).
	ctx, cancel := context.WithTimeout(context.Background(), planThenWatch)
	every := planThenEvery
	go func() {
		defer cancel()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			pane, err := n.paneFor(ctx, id)
			if err != nil {
				return // its tab is gone
			}
			screen, err := n.readPane(ctx, pane, "visible", 60)
			if err != nil {
				return
			}
			switch cur := tuiMode(screen); cur {
			case "", "plan":
				continue // a dialog is up, or the plan is not approved (yet)
			case want:
				return
			default:
				// Its own time: the watch may be nearly over when the approval is seen.
				sctx, done := context.WithTimeout(context.Background(), planThenSet)
				err := n.tuiSetMode(sctx, pane, want)
				done()
				if err != nil {
					log.Printf("plan %s: approved in the terminal in %s, could not move to %s: %v", short(id), cur, want, err)
					return
				}
				n.noteModes(id, want, "")
				n.notifySessions()
				log.Printf("plan %s: approved in the terminal in %s, moved to %s as its start said", short(id), cur, want)
				return
			}
		}
	}()
	return cancel
}
