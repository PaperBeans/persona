package larkcli

import "strings"

// isSourceAppLinkPath accepts exactly the public chat and thread AppLink routes.
func isSourceAppLinkPath(value string) bool {
	parent, suffix, found := strings.Cut(value, "/open")
	return found && suffix == "" && (parent == "/client/chat" || parent == "/client/thread")
}
