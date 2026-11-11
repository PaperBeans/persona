package prompts

// StandingAuthorization is one scoped grant carried into an agent's task instructions.
type StandingAuthorization struct {
	ID, MachineID, Directory string
	Actions, Conditions      []string
	Quote, Source, GrantedAt string
	Reversible               bool
}

// StandingAuthorizations carries current grants alongside the task as sent.
type StandingAuthorizations struct {
	Text   string
	Grants []StandingAuthorization
}

func RenderStandingAuthorizations(in StandingAuthorizations) string {
	return render("assistant/standing_authorizations", in)
}
