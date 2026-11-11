package devicewire

// MethodStartWorkSession uses the same creation and ownership handling as
// session.start, but requires the node to validate the selected start options.
const MethodStartWorkSession = "session.start.work"

func IsSessionStart(method string) bool {
	return method == "session.start" || method == MethodStartWorkSession
}
