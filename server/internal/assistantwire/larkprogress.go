package assistantwire

import "time"

// RunProgress is what a Feishu run's progress card shows of the run besides its steps: the channels' live state of the
// run as the core keeps it (internal/assistant), copied for the lark module to draw (#13404). The zero value is a run
// the core has no live state for.
type RunProgress struct {
	// Waiting says why the run's message is not found in its conversation yet: "queued" behind a turn that works,
	// "preparing" while it is not in yet; "" once it is.
	Waiting string
	// Open are the tool calls still running, oldest first; Calls, Read and Edited count the calls, files read and files
	// edited so far (a count stops at RunFilesMost).
	Open                []RunOpenCall
	Calls, Read, Edited int
	// Subs are the subagents' tasks, at most the newest the core keeps; SubsMore counts the older ones dropped.
	Subs     []RunSubtask
	SubsMore int
	// Now is the core's clock, which the running calls' times are read against.
	Now time.Time
	// Target is the run's conversation, where the card's Stop button goes; the zero target (no live run) has no button.
	Target ChatTarget
}

// RunOpenCall is a tool call that has not come back: its step line and when it started.
type RunOpenCall struct {
	Step  string
	Since time.Time
}

// RunSubtask is a subagent's task: what it was asked (Desc), its kind and whether it is done.
type RunSubtask struct {
	Desc, Kind string
	Done       bool
}

// RunFilesMost is where the progress card's file counters stop counting ("1000+").
const RunFilesMost = 1000

// AnswerCardMaxBytes bounds one complete answer card's JSON: Feishu refuses cards past about 30 KB; 2 KB are kept as margin.
const AnswerCardMaxBytes = 28000
