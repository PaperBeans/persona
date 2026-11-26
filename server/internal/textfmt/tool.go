package textfmt

import (
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
)

// ToolName is the tool a card asks about, for a sentence: the manager's name for people on its own tools' cards
// (proto.Interaction.ToolTitle), else ToolDisplayName.
func ToolName(ix proto.Interaction) string {
	if ix.ToolTitle != "" {
		return ix.ToolTitle
	}
	return ToolDisplayName(ix.ToolName)
}

// ToolDisplayName is a tool's name for a sentence: an MCP tool as "Persona · mail send" (Persona's own server keeps
// its old id persona, which allow lists and hooks match), others as written.
func ToolDisplayName(name string) string {
	name = compat.CanonicalMCPToolName(name)
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return name
	}
	server, tool := rest, ""
	if i := strings.LastIndex(rest, "__"); i > 0 {
		server, tool = rest[:i], strings.ReplaceAll(rest[i+2:], "_", " ")
	}
	if server = strings.TrimPrefix(server, "plugin_"); compat.IsMCPServer(server) {
		server = "Persona"
	}
	if server == "" {
		return name
	}
	if tool == "" {
		return server
	}
	return server + " · " + tool
}
