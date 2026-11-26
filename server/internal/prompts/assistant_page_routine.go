package prompts

// PageRoutineRun is the header prepended to a dashboard-bound routine's run prompt (assistant/page_routine_run).
type PageRoutineRun struct {
	Language string
	PageID   string
	Title    string
	Question string
	Display  string
	Unit     string
	Columns  []string
	HasView  bool
}

func RenderPageRoutineRun(p PageRoutineRun) string { return render("assistant/page_routine_run", p) }
