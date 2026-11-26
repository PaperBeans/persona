package extwire

// ScopeAgentsArchiveIdle grants an extension permission to request guarded idle-session archive-and-close on its
// manager's computers (strictly idle > N minutes; never working, main, direction-lead, open-todo, or card-blocked
// sessions) and to read its own idle-archive audit entries. Granted by name only.
const ScopeAgentsArchiveIdle = "agents.archive_idle"

func init() {
	addRouteScopes(map[string]string{
		"POST /api/v1/sessions/{mid}/{sid}/archive-idle": ScopeAgentsArchiveIdle,
		"GET /api/v1/sessions/archive-idle/audit":        ScopeAgentsArchiveIdle,
	})
}
