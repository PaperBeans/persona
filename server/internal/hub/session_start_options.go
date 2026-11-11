package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

func (h *Hub) workSessionStartRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/machines/{mid}/launchers/{name}/start-options", h.withMachine(h.workSessionStartOptions))
}

func (h *Hub) workSessionStartOptions(w http.ResponseWriter, r *http.Request, m *Machine) {
	if _, err := h.extensionsOwn(r, h.machineOwner(m.cfg.ID)); err != nil {
		writeErrCode(w, http.StatusForbidden, codeOtherManager, err)
		return
	}
	if h.machineUntrusted(m.cfg.ID) {
		writeErrCode(w, http.StatusConflict, "machine_pending", errors.New("Confirm this computer before starting a session."))
		return
	}
	name := r.PathValue("name")
	params := map[string]any{"name": name}
	setWorkLauncherConfig(m, name, params)
	ctx, cancel := context.WithTimeout(r.Context(), 85*time.Second)
	defer cancel()
	var opts json.RawMessage
	if err := m.call(ctx, "session.start.options", params, &opts); err != nil {
		callErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, opts)
}

// Only hub configuration can vouch for a launcher absent from the node's list.
func setWorkLauncherConfig(m *Machine, name string, params map[string]any) {
	for _, l := range m.cfg.Launchers {
		if l.Name == name {
			params["configured"] = true
			params["configured_agent"] = l.AgentType
			return
		}
	}
}
