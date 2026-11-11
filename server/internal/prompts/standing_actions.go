package prompts

type StandingActionsInput struct{ Task, Card, Directory string }

func RenderStandingActions() string { return render("agents/standing_actions", nil) }
func RenderStandingActionsInput(in StandingActionsInput) string {
	return render("agents/standing_actions_input", in)
}
