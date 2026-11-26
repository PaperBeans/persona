package prompts

// TodoContextHandoff is the deterministic, auditable context block injected before a replacement
// Agent's task prompt when continuing a to-do on another provider or launcher (#12980).
type TodoContextHandoff struct {
	HandoffID      string
	OperationID    string
	TodoID         string
	TodoTitle      string
	Strategy       string
	Generator      string
	SupersedesID   string
	SourceMachine  string
	SourceSession  string
	SourceLauncher string
	SourceAgent    string
	TargetMachine  string
	TargetLauncher string
	TargetAgent    string
	BriefRevision  string
	KnownFailure   string
	Goal           string
	Request        string
	Criteria       []string
	OutOfBounds    []string
	Decisions      []TodoContextDecision
	SourceRanges   []TodoContextSourceRange
	Files          []string
	Commands       []string
	Failed         []string
	RecentTurns    []TodoContextTurn
	DiffScope      string
	DiffBase       string
	DiffHead       string
	DiffBranch     string
	DiffFiles      []string
	DiffTruncated  bool
	MissingSources []TodoContextGap
}

type TodoContextDecision struct {
	By       string
	Question string
	Choice   string
	Reason   string
}

type TodoContextSourceRange struct {
	MachineID string
	SessionID string
	Agent     string
	FromSeq   int
	ToSeq     int
	ItemCount int
	Truncated bool
}

type TodoContextTurn struct {
	Speaker string
	Text    string
}

type TodoContextGap struct {
	Source string
	Reason string
}

// RenderTodoContextHandoff renders the auditable to-do context handoff block.
func RenderTodoContextHandoff(h TodoContextHandoff) string {
	return render("agents/todo_context_handoff", h)
}
