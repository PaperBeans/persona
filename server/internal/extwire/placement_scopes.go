package extwire

// ScopeTasksSchedule grants an extension permission to inspect pending automatic-placement task metadata (excluding
// prompts and hidden spaces) and submit CAS assign/defer decisions. Granted by name only.
const ScopeTasksSchedule = "tasks.schedule"

func init() {
	addRouteScopes(map[string]string{
		"GET /api/v1/assistant/machine-resources":           ScopeMachinesRead,
		"GET /api/v1/assistant/placement/pending":           ScopeTasksSchedule,
		"POST /api/v1/assistant/placement/{todo_id}/assign": ScopeTasksSchedule,
		"POST /api/v1/assistant/placement/{todo_id}/defer":  ScopeTasksSchedule,
	})
}
