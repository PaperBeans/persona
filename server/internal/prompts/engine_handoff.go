package prompts

// EngineHandoff is the note an Agent session's conversation carries to the other engine when it switches between
// Claude Code and Codex (agents/engine_handoff, #5054): what came before, put in front of the user's message. The node
// builds it from the conversation's transcripts (node/engine_handoff_build.go); every value is clipped already.
type EngineHandoff struct {
	From, To string // the engines as the model reads them ("Claude Code", "Codex")
	// Delta: To ran this conversation before and is resumed, so it remembers what came before it left; the note is
	// only what happened while it was away.
	Delta  bool
	Ledger EngineHandoffLedger
	// Index is the earlier turns, a line for each one's words and one for its last reply, oldest first; Omitted says
	// older lines were left out.
	Index   []EngineHandoffIndex
	Omitted bool
	Recent  []EngineHandoffTurn // the latest turns, oldest first
	// Transcripts are the files that keep the conversation word for word on this computer, oldest first.
	Transcripts []EngineHandoffTranscript
}

// EngineHandoffLedger is what the work did, read off its tool calls: the files it changed, the latest commands it
// ran, and the attempts that failed.
type EngineHandoffLedger struct {
	Files    []string
	Commands []EngineHandoffCommand
	Failed   []EngineHandoffCommand
}

// EngineHandoffCommand is a command with what it came to (Status: "" when the result does not say, "exit" with
// Exit, or one of ok, failed, tests_failed, denied, blocked, interrupted, timed_out, background), or, for a failed
// attempt, a failing test's path its output named (Hint).
type EngineHandoffCommand struct {
	Command string
	Status  string
	Exit    int
	Hint    string
}

// Who an input of the conversation came from, when not the user (Who "").
const (
	EngineHandoffPersona = "persona" // a message Persona wrote into the conversation (a follow-up, a wake-up)
	EngineHandoffAgent   = "agent"   // a message from another Agent session (From)
)

// EngineHandoffIndex is one line of the earlier turns' index: what an input said (Who, From), or the reply.
type EngineHandoffIndex struct {
	Who, From string
	Reply     bool
	Text      string
}

// EngineHandoffTurn is one turn word for word: the input (Who, From, Text, and how many pictures came with it) and
// what followed it.
type EngineHandoffTurn struct {
	Who, From string
	Text      string
	Images    int
	Lines     []EngineHandoffLine
}

// Kinds of EngineHandoffLine.
const (
	EngineHandoffSaid    = "assistant" // the agent's words
	EngineHandoffTool    = "tool"      // a tool call: Tool, ID, its input (Text)
	EngineHandoffResult  = "result"    // a call's result: ID, Text, Failed
	EngineHandoffSkipped = "omitted"   // Omitted lines left out of the middle of a long turn
)

// EngineHandoffLine is one thing in a turn after its input.
type EngineHandoffLine struct {
	Kind     string
	Tool, ID string
	Text     string
	Failed   bool
	Omitted  int
}

// EngineHandoffTranscript is a session's transcript file: the engine (as the model reads it), the session, the path.
type EngineHandoffTranscript struct {
	Engine, Session, Path string
}

// RenderEngineHandoff is agents/engine_handoff.
func RenderEngineHandoff(h EngineHandoff) string { return render("agents/engine_handoff", h) }

// EngineHandoffMessage is the user's message as a switch to the other engine types it (agents/engine_handoff_message):
// behind a <persona-context kind="engine-switch"> block holding the handoff note (Text; nothing in it ends the block,
// ContextBody) and naming the engines (From, To: "claude" / "codex"), the model the new engine runs (Model, "" when
// not known) and whether the conversation's earlier session of To was resumed. The apps draw the switch divider from
// the block and show its text behind "View handoff context"; the model reads the text.
type EngineHandoffMessage struct {
	From, To, Model string
	Resumed         bool
	Text, Message   string
}

// RenderEngineHandoffMessage is agents/engine_handoff_message.
func RenderEngineHandoffMessage(m EngineHandoffMessage) string {
	return render("agents/engine_handoff_message", m)
}
