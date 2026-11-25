package assistantwire

// The counts view on the bus (docs/protocol/counts.md, step 7a of docs/plans/plan-derived-counts.md): the manager
// tells the hub its CountsView (counts_gen.go), and the hub keeps it as it is, never decoding counts (step 7b).
const (
	// CapabilityCountsView: the manager tells its counts view (EventCountsView) after each handshake and whenever
	// it changes. Clients draw a manager's view only while its roster entry says it names this (D10 of the plan): a
	// manager without it, or one rolled back, sends every client to its own count.
	CapabilityCountsView = "counts_view"

	// EventCountsView (the manager tells the hub, one way): a CountsView, built in the manager's live round from the
	// agents and cards the Live Activity and Persona Link's summary are built from. A view whose counts did not change
	// is not told again, except after a handshake, when the last one is told as it was. A hub from before it drops the
	// event.
	EventCountsView = "counts.view"

	// CountsViewMaxBytes is the most a counts view takes as JSON. The manager lists fewer rows to stay within it
	// (CountsViewCount.More), and the hub keeps no larger view (step 7b).
	CountsViewMaxBytes = 16 << 10
)
