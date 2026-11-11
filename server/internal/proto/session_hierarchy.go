package proto

import "strings"

// IsCodexSubagent identifies delegated tasks from source metadata, even when
// their parent is not in the current session list. Titles never imply ancestry.
func (s Session) IsCodexSubagent() bool {
	if s.Agent != "codex" && !strings.HasPrefix(s.SourceKind, "codex_") {
		return false
	}
	return s.IsSubagent || s.ParentSessionID != "" || s.ParentThreadID != "" ||
		(s.RootSessionID != "" && s.RootSessionID != s.SessionID)
}

// IsSubtask says whether a session is listed and counted under the session that started it rather than on its own:
// a Codex subagent, or a runtime's own session that names its parent, whichever agent it runs (SourceRuntime: an
// agent another Paseo agent created, #4494). Unlike a Codex subagent, which its parent drives, such a runtime session
// can need the user. While it does it is no subtask: every place that counts sessions has it, and none folds it
// away. (The node lists it without its parent then; a card can still reach the hub before that row does.)
func (s Session) IsSubtask() bool {
	if s.SourceKind == SourceRuntime {
		return (s.IsSubagent || s.ParentSessionID != "") && s.State != StateNeedsYou
	}
	return s.IsCodexSubagent()
}
