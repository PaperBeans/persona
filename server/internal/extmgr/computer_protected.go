package extmgr

// Apps the assistant never operates (#4336, docs/extensions/computer-use.md "c) Per-App approval"): System Settings
// and the system's permission and authorization prompts. macOS's privacy permissions are meant to be answered by a
// person, and one Allow on a card that says "operate System Settings" would otherwise let an Agent turn any of them on.
// A call that names one of these apps is refused before any card. So is a script whose apps cannot all be read (it
// could open one by a computed name), and, on Cua Driver, any call while one of them has a window on the screen:
// element tokens, a pid or a step naming no app could still reach it. Steps naming no app run one at a time, each after
// the same look at the screen, so a prompt an earlier step brought up stops the rest. Reading the screen
// (computer_state) stays as it was: asked every time. Scripts are only looked at before they run; what one does
// with a window it finds while running is Cua Driver's (and the follow-up issue's) to stop.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/protectedapps"
	"github.com/nwverse/persona/server/internal/proto"
)

// protectedApp: a is one of the apps never operated (protectedapps.Is), by bundle id or by name.
func protectedApp(a approvedApp) bool { return protectedapps.Is(a.BundleID, a.Name) }

// refusedText is what the model is told; it passes the gist on to the user.
func refusedText(machine, why string) string { return protectedapps.Refusal(machine, why) }

// knownApps is Cua Driver's list of m's apps, nil when it cannot be read.
func (h *Manager) knownApps(ctx context.Context, m *Machine) []protectedapps.App {
	if res, err := h.computerCall(ctx, m, "persona-apps", "", "list_apps", map[string]any{}); err == nil {
		return protectedapps.Apps(res.Output)
	}
	return nil
}

// protectedOnScreen is the protected apps with a window on m's screen now, by name (protectedapps.OnScreen). ok is
// false when the screen could not be read: the caller refuses then, rather than act blind. known is refreshed when a
// window's pid is not in it (an app started since).
func (h *Manager) protectedOnScreen(ctx context.Context, m *Machine, known *[]protectedapps.App) (found []string, ok bool) {
	res, err := h.computerCall(ctx, m, "persona-apps", "", "list_windows", map[string]any{})
	if err != nil {
		return nil, false
	}
	windows, ok := protectedapps.Windows(res.Output)
	if !ok {
		return nil, false
	}
	found, *known = protectedapps.OnScreen(windows, *known, func() []protectedapps.App { return h.knownApps(ctx, m) })
	return found, true
}

// unnamedSteps: some step names no app of its own and the call names none (protectedapps.UnnamedSteps).
func unnamedSteps(app string, steps json.RawMessage) bool {
	return protectedapps.UnnamedSteps(app, steps)
}

// runStepsOneByOne runs steps one run_steps call at a time, looking at the screen before each after the first (which
// the caller looked at): a protected app's window stops the rest. The answer lists each step's own answer; the last
// step carries the call's observe, and its screenshot is the answer's.
func (h *Manager) runStepsOneByOne(ctx context.Context, m *Machine, session, conversation string, steps, observe json.RawMessage,
	known *[]protectedapps.App) (*proto.ComputerResult, string, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(steps, &list); err != nil {
		return nil, "", err
	}
	outs := make([]json.RawMessage, 0, len(list))
	var last *proto.ComputerResult
	for i, step := range list {
		if i > 0 {
			found, ok := h.protectedOnScreen(ctx, m, known)
			if !ok {
				return nil, "", fmt.Errorf("stopped before step %d of %d (the ones before it ran): the Mac's windows could not "+
					"be read to check for System Settings or a permission prompt", i+1, len(list))
			}
			if len(found) > 0 {
				return nil, fmt.Sprintf("%s came up on the screen before step %d (%d of %d done)", strings.Join(found, ", "), i+1, i, len(list)), nil
			}
		}
		in := map[string]any{"steps": []json.RawMessage{step}}
		if i == len(list)-1 && len(observe) > 0 {
			in["observe"] = observe
		}
		res, err := h.computerCall(ctx, m, session, conversation, "run_steps", in)
		if err != nil {
			return nil, "", err
		}
		last = res
		out := res.Output
		if len(out) == 0 {
			out, _ = json.Marshal(res.Text)
		}
		outs = append(outs, out)
		var r struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(res.Output, &r) == nil && r.OK != nil && !*r.OK {
			break // stopped at its first failure, as run_steps does
		}
	}
	all, _ := json.Marshal(map[string]any{"one_by_one": true, "steps": outs})
	return &proto.ComputerResult{Output: all, Image: last.Image}, "", nil
}
