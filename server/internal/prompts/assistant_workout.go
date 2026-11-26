package prompts

type WorkoutReport struct {
	Data     string
	Language string
	Strength *WorkoutStrength
}

type WorkoutStrength struct {
	Sets      string
	PlanDraft string
	Plan      string
}

// RenderWorkoutReport describes one consented workout, never a medical diagnosis.
func RenderWorkoutReport(data WorkoutReport) string { return render("assistant/workout_report", data) }
