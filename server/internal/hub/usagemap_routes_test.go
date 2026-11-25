package hub

import (
	"net/http"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
)

// A computer's disk usage map and its trash (#14576) are for full credentials: one lists what is on the disk, the
// others move and delete files. The watch's answer-only token never reaches the manager with them, no extension scope
// opens them, and the hub relays them to the devices manager.
func TestUsageMapRoutesNeedFullCredentials(t *testing.T) {
	routes := []string{
		"GET /api/v1/machines/{mid}/storage/usage",
		"POST /api/v1/machines/{mid}/storage/trash",
		"POST /api/v1/machines/{mid}/storage/trash/undo",
		"POST /api/v1/machines/{mid}/storage/trash/purge",
	}
	watch := principal{Role: roleAnswer}
	for _, rq := range [][2]string{
		{http.MethodGet, "/api/v1/machines/mac/storage/usage"},
		{http.MethodPost, "/api/v1/machines/mac/storage/trash"},
		{http.MethodPost, "/api/v1/machines/mac/storage/trash/undo"},
		{http.MethodPost, "/api/v1/machines/mac/storage/trash/purge"},
	} {
		if allowed(watch, rq[0], rq[1]) {
			t.Errorf("an answer-only device may %s %s", rq[0], rq[1])
		}
		if !allowed(principal{Role: roleFull}, rq[0], rq[1]) {
			t.Errorf("a full device may not %s %s", rq[0], rq[1])
		}
	}
	for _, p := range routes {
		if s, ok := extwire.RouteScopes[p]; ok {
			t.Errorf("%s is open to extensions with %s", p, s)
		}
		if !slices.Contains(devicewire.Routes, p) {
			t.Errorf("%s is not relayed to the devices manager", p)
		}
	}
	// The offload extension point's routes are the extensions manager's, under its relayed prefix, and no scope opens them.
	for _, p := range []string{"GET /api/v1/extensions/-/offload", "POST /api/v1/extensions/{id}/-/offload/{cid}/run"} {
		if s, ok := extwire.RouteScopes[p]; ok {
			t.Errorf("%s is open to extensions with %s", p, s)
		}
	}
}
