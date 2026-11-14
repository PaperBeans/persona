package assistant

import "fmt"

// ---- which agents take a permission mode and an effort at their start (#7559) ----
//
// The node starts Claude Code with --permission-mode and --effort, and Codex with its own sandbox (plan) and reasoning
// effort; every other agent it starts in a terminal (traex, omp) takes neither, and the node refuses a start that
// names one (node/control.go). So the hub never fills one in for those: a writable task runs without a mode, and a
// read-only one relies on the read-only constraint every read-only task's first message carries
// (prompts.RenderReadonlyTaskConstraint), the same constraint a Claude-family launcher, which starts in
// bypassPermissions, relies on. One the caller names itself is refused here, saying why, before anything starts.

// startTakesModes: a start of agent takes a permission mode and an effort. "" (the hub can't tell what the launcher
// runs) keeps what the start sends, for the node to judge: nothing is dropped on a guess.
func startTakesModes(agent string) bool {
	return agent == "" || agent == "claude" || agent == "codex"
}

// startModesRefused is the refusal of a permission mode or effort the caller named for a launcher whose agent takes
// neither; nil when it named none.
func startModesRefused(launcher, agent, permission, effort string) error {
	switch {
	case permission != "":
		return fmt.Errorf("launcher %s runs %s, which takes no permission mode (%s): leave permission out; "+
			"set readonly true for a task that only reads, and its first message carries the read-only constraint", launcher, agent, permission)
	case effort != "":
		return fmt.Errorf("launcher %s runs %s, which takes no effort (%s): leave effort out", launcher, agent, effort)
	}
	return nil
}
