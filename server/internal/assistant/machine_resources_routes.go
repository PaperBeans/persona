package assistant

import (
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

const roleScoped = "scoped"

// machineResourcesRoutes serves GET /api/v1/assistant/machine-resources under machines.read.
// It shares the snapshot builder with machines_list while omitting connection errors, session details,
// launcher error text, and (for extension and scoped tokens) every space session from working_agents.
func (s *Service) machineResourcesRoutes() {
	s.handle("GET /api/v1/assistant/machine-resources", func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(RoleHeader)
		excludeSpaces := role == roleExtension || role == roleScoped
		snaps, complete, excluded, err := s.collectMachineSnapshots(r.Context(), excludeSpaces)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		machines := make([]assistantwire.MachineResourceSummary, 0, len(snaps))
		for _, snap := range snaps {
			machines = append(machines, snap.summary)
		}
		apierr.WriteJSON(w, map[string]any{
			"machines":       machines,
			"complete":       complete,
			"excluded_count": excluded,
		})
	})
}
