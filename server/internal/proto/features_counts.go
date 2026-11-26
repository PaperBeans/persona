package proto

// FeatureCountsView: the hub relays each manager's counts view (docs/protocol/counts.md "The hub's relay"). Listed
// always, whatever the managers are: hub features reach a client only in the hello, so a flag that followed the
// managers would switch a client's rule on every redeploy (D10 of docs/plans/plan-derived-counts.md). Whether a
// manager tells its view is its roster entry's counts_view. A client that names it (?features= or client_hello's
// features) gets the views as `counts` messages.
const FeatureCountsView = "counts_view"

func init() { Features = append(Features, FeatureCountsView) }
