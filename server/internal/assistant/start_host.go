package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
)

// Where an Agent the assistant starts opens (#13447). A new Agent with the computer's default host runs agy in a herdr
// pane (#10237): someone is at that terminal. A task_plan task or an agent_start has nobody at the pane, and a pane
// Persona cannot read (herdr's antigravity-cli integration missing) leaves its supervisor nothing to judge. So an agy
// start that names no host goes to the computer's Antigravity companion when that runs its launcher: a conversation
// Persona reads as it goes, with approval cards, Persona's tools and token usage. The node refuses a start naming the
// companion while none is connected (agy_companion_offline, node/starthost.go); one the hub sent there by itself then
// starts again in the computer's default host, so a companion that just went away never fails the task. A start that
// names a host (agy, or an engine host such as herdr) keeps it.

// checkStartHostName refuses a host no start can name: agy, the Antigravity companion, or an engine host. "" is the
// computer's default host.
func checkStartHostName(host string) error {
	if host == "" || host == proto.HostAgy || proto.IsEngineHost(host) {
		return nil
	}
	return fmt.Errorf("host %q is not where an Agent can start: name agy (the Antigravity companion) or an engine host (%s), "+
		"or leave it out", host, strings.Join(proto.EngineHostIDs, ", "))
}

// checkHostLauncher refuses a start that names the companion for a launcher that does not start the Antigravity CLI:
// the companion runs nothing else. An agent the hub could not tell ("") is the node's to judge.
func checkHostLauncher(params map[string]any, fit startAgent) error {
	if host, _ := params["host"].(string); host != proto.HostAgy || fit.agent == "" || fit.agent == "agy" {
		return nil
	}
	launcher, _ := params["launcher"].(string)
	return fmt.Errorf("host agy (the Antigravity companion) runs only the Antigravity CLI, and launcher %s runs %s: "+
		"leave host out or pick an agy launcher", launcher, kindName(fit.agent))
}

// companionStartHost names the companion in params (host agy) for an agy start that names no host, when the companion
// runs its launcher on m (companionRuns). True when it did: a start the node then refuses as away (companionAway) goes
// again without the host.
func (s *Service) companionStartHost(ctx context.Context, m Device, fit startAgent, params map[string]any) bool {
	if host, _ := params["host"].(string); host != "" || fit.agent != "agy" || !s.companionRuns(ctx, m, fit) {
		return false
	}
	params["host"] = proto.HostAgy
	return true
}

// companionRuns: m's Antigravity companion runs the agy launcher fit is: its own agy launcher, which the computer's list
// names while the companion is connected (the list's entry for it is the companion's, source agy), or a launcher kept in
// Persona that starts agy, which the node hands to the companion while one is connected (node startRuntime).
func (s *Service) companionRuns(ctx context.Context, m Device, fit startAgent) bool {
	switch compat.Source(fit.source) {
	case proto.HostAgy:
		return true
	case "persona":
		list, known := s.launchersHere(withUnhidden(ctx, proto.HostAgy), m.ID)
		return known && slices.ContainsFunc(list, func(l proto.Launcher) bool {
			return compat.Source(l.Source) == proto.HostAgy && l.Agent == "agy"
		})
	}
	return false
}

// companionAway: err is the node's refusal of a start the hub sent to the companion by itself (chosen), because no
// companion is connected there now; the start then goes to the computer's default host without the host.
func companionAway(err error, chosen bool) bool {
	return chosen && refusedWith(err, proto.CodeAgyCompanionOffline)
}
