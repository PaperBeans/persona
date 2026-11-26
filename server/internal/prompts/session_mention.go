package prompts

// SessionMention is what an Agent is told about another conversation the user quoted with @ (#13831,
// node/session_mention.go): its address and how to read it or write to it through A2A (agents/session_mention).
type SessionMention struct {
	// Language is the owner's language name from the manager (prompt_language): empty or 简体中文 for Chinese,
	// 日本語, anything else English.
	Language string
	// Machine and Session are the quoted session's ids, plain (no space, quote or angle bracket: the node checks).
	Machine string
	Session string
	// Executable is the node's program for an Agent without the Persona MCP tools, encoded as a JSON string, never
	// shell code (as TaskHandoff's); empty when the node has no absolute path for it, and the note says personad.
	Executable string
}

func RenderSessionMention(m SessionMention) string { return render("agents/session_mention", m) }
