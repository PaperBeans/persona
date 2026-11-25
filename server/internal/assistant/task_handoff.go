package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// taskHandoff is rendered after placement and launcher fitting. An optional node.info read must never turn a
// usable assignment into a failed or indefinitely waiting start merely because its handoff hint is unavailable.
func (s *Service) taskHandoff(ctx context.Context, machine string, fit startAgent) string {
	return prompts.RenderTaskHandoff(s.taskEntry(ctx, machine, fit))
}

// taskEntry is the verified way the fitted Agent reaches its own Persona tools: the MCP tools, the terminal entry
// (the node's executable), or none. The handoff note names it, and a task leader's note follows it (#12136).
func (s *Service) taskEntry(ctx context.Context, machine string, fit startAgent) prompts.TaskHandoff {
	source := compat.Source(fit.source)
	// Empty is a legacy shell launcher (proto.Launcher). A runtime or app's same-family agent does not establish
	// terminal caller identity. Paseo injects the existing Persona MCP server into its Claude and Codex providers.
	terminal := source == "" || source == "zshrc" || source == "path" || source == "persona" || source == "builtin" && fit.agent == "claude"
	if source == "persona" && fit.agent == "agy" {
		terminal = false // managed agy starts on its runtime (node/runtimes_start.go), not a pane
	}
	mcp := terminal && fit.agent == "claude" || source == proto.EnginePaseo && (fit.agent == "claude" || fit.agent == "codex")
	return s.taskEntryFor(ctx, machine, fit.agent, mcp, terminal)
}

// adoptedTaskEntry uses the running session's provenance. A Codex app-server thread
// can identify its caller through CODEX_THREAD_ID even when it has no terminal pane.
func (s *Service) adoptedTaskEntry(ctx context.Context, machine string, c proto.Session) prompts.TaskHandoff {
	paseo := c.Host == proto.EnginePaseo
	terminal := !paseo && (c.SourceKind == "" || c.SourceKind == proto.SourceClaudeTerminal ||
		c.Agent == "codex" && c.SourceThreadID != "" && (c.SourceKind == "codex_app_server" || c.SourceKind == "codex_history"))
	mcp := terminal && c.Agent == "claude" || paseo && (c.Agent == "claude" || c.Agent == "codex")
	return s.taskEntryFor(ctx, machine, c.Agent, mcp, terminal)
}

func (s *Service) taskEntryFor(ctx context.Context, machine, agent string, mcp, terminal bool) prompts.TaskHandoff {
	h := prompts.TaskHandoff{Language: answerLanguage(s.hello(ctx).Locale), MCP: mcp}
	if terminal && (agent == "codex" || proto.IsWorkAgentType(agent) || proto.IsNoModeAgentType(agent)) {
		h.Executable = s.nodeExecutable(ctx, machine)
	}
	return h
}

// nodeExecutable is machine's node program as the node names it (node.info's Executable, an absolute path) for an
// Agent to run Persona's tools from a terminal, encoded as a JSON string: paths, quotes and line breaks included, it
// stays data rather than shell code. "" when the node names none.
//
// It is the path the node named when its connection began, as the devices keep it (devices.Summary.Executable).
// Asking again is not cheap: a node works node.info out whole, Claude Code's version from an interactive login shell
// included, so on a computer whose shell takes seconds to start the answer came after the second this hint may wait,
// and every handoff there lost its terminal entry (#14446). The node is asked, within that second, only when the
// devices name no such path (a machine this manager does not hold, a node that did not say): an optional hint never
// holds up a start.
func (s *Service) nodeExecutable(ctx context.Context, machine string) string {
	exe := ""
	if s.devs != nil {
		if m, ok := s.devs.Summary(machine); ok {
			exe = m.Executable
		}
	}
	if !proto.NodeAbs(exe) {
		cctx, cancel := context.WithTimeout(ctx, min(agentStartListTimeout, time.Second))
		defer cancel()
		var info proto.NodeInfo
		if s.call(cctx, machine, "node.info", map[string]any{}, &info) != nil {
			return ""
		}
		exe = info.Executable
	}
	if !proto.NodeAbs(exe) {
		return ""
	}
	encoded, _ := json.Marshal(exe)
	return string(encoded)
}
