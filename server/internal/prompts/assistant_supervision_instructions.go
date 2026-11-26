package prompts

// SupervisionParts are the role-private paragraphs a conversation's supervision policy carries beside the common
// policy: a leader's, a background supervisor's, or neither (the main chat and a side supervisor). The assistant
// fills them from the conversation's role (internal/assistant/supervisionrole).
type SupervisionParts struct {
	Leader     bool
	Background bool
}

type supervisionInstructionData struct {
	Leader     bool
	Background bool
}

// RenderSupervisionInstructions returns the full policy union for its golden and size budget.
// Conversations use RenderSupervisionInstructionsFor instead.
func RenderSupervisionInstructions() string {
	return render("assistant/supervision_instructions", supervisionInstructionData{Leader: true, Background: true})
}

// RenderSupervisionInstructionsFor keeps common supervision policy and only the given role-private paragraphs.
func RenderSupervisionInstructionsFor(p SupervisionParts) string {
	return render("assistant/supervision_instructions", supervisionInstructionData{Leader: p.Leader, Background: p.Background})
}
