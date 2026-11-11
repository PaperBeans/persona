package node

import (
	"context"
	"log"
	"slices"
	"sync/atomic"
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

// terminalDecided marks an ask whose question the terminal's own dialog answered: its card is withdrawn as dismissed
// (decided on the computer), where a hook that merely went away is cancelled.
type terminalDecided struct{ atomic.Bool }

type terminalDecidedKey struct{}

// planAfterTerminal follows a plan from the moment its card is raised. The plan dialog hides the TUI's footer; once
// the footer is back, the terminal decided the plan (approved, or sent back), and decidedHere is called once so the
// card that still waits is withdrawn. When the footer then shows a mode other than plan, the plan was approved there,
// and a session whose start recorded a mode (planThen) is moved to it when the dialog picked another. A dialog still
// in front (no footer) is waited out: a plan approved with "manually approve" is followed at once by a permission
// dialog. The returned stop ends the watch: the card was answered, and its answer set the mode.
func (n *Node) planAfterTerminal(id string, decidedHere func()) (stop func()) {
	want := n.planThen(id)
	ctx, cancel := context.WithTimeout(context.Background(), planThenWatch)
	go func() {
		defer cancel()
		t := time.NewTicker(planThenEvery)
		defer t.Stop()
		told := false
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
			cur := tuiMode(screen)
			if cur == "" {
				continue // a dialog is up
			}
			if !told && decidedHere != nil {
				told = true
				decidedHere()
			}
			switch {
			case cur == "plan":
				return // sent back in the terminal: it plans again, and its next plan is a new card
			case want == "" || cur == want:
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
