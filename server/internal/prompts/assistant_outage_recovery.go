package prompts

// OutageRecovery is the localized per-task recovery summary emitted once a provider-wide outage ends
// after holding and coalescing outage-caused interrupted and unjudged supervision signals (#13158).
type OutageRecovery struct {
	Language     string
	Provider     string
	Suppressed   int
	Interrupted  int
	Unjudged     int
	LatestState  string
	LatestDetail string
}

// RenderOutageRecovery renders the coalesced per-task outage recovery summary.
func RenderOutageRecovery(r OutageRecovery) string { return render("assistant/outage_recovery", r) }
