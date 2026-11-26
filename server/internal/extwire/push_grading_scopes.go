package extwire

func init() {
	// Route-to-scope bindings for ScopePushGrading (defined in contract.go):
	// an explicitly authorized extension manages only its own push grading layer
	// and previews combined grading decisions without mutating notification state.
	addRouteScopes(map[string]string{
		"GET /api/v1/push/grading/layer":    ScopePushGrading,
		"PUT /api/v1/push/grading/layer":    ScopePushGrading,
		"DELETE /api/v1/push/grading/layer": ScopePushGrading,
		"POST /api/v1/push/grading/preview": ScopePushGrading,
	})
}
