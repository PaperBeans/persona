package hub

import (
	"context"
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/i18n"
	recoveryops "github.com/nwverse/persona/server/internal/operations/recovery"
)

type recoveryService interface {
	Overview(context.Context) (recoveryops.Overview, error)
	Inspect(context.Context, string) (recoveryops.Check, error)
	Verify(context.Context, string) (recoveryops.Check, error)
}

// recoveryRoutes are hub installation administration, not manager business
// APIs. There is deliberately no create, restore, drill, download or key route:
// a running hub cannot quiesce the whole installation, and no reauth boundary
// authorizes exporting a bundle's private keys.
func (h *Hub) recoveryRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/hub/recovery", func(w http.ResponseWriter, r *http.Request) {
		if !h.recoveryAllowed(w, r, "overview") {
			return
		}
		overview, err := h.recoveryForRequest().Overview(r.Context())
		h.auditRecovery(r, "overview", recoveryops.Check{State: "passed"}, err)
		if err != nil {
			writeRecoveryError(w, err)
			return
		}
		writeJSON(w, overview)
	})
	api.HandleFunc("POST /api/v1/hub/recovery/bundles/{id}/inspect", func(w http.ResponseWriter, r *http.Request) {
		if !h.recoveryAllowed(w, r, "inspect") {
			return
		}
		check, err := h.recoveryForRequest().Inspect(r.Context(), r.PathValue("id"))
		h.auditRecovery(r, "inspect", check, err)
		if err != nil {
			writeRecoveryError(w, err)
			return
		}
		writeJSON(w, check)
	})
	api.HandleFunc("POST /api/v1/hub/recovery/bundles/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		if !h.recoveryAllowed(w, r, "verify") {
			return
		}
		check, err := h.recoveryForRequest().Verify(r.Context(), r.PathValue("id"))
		h.auditRecovery(r, "verify", check, err)
		if err != nil {
			writeRecoveryError(w, err)
			return
		}
		writeJSON(w, check)
	})
}

func (h *Hub) recoveryForRequest() recoveryService {
	if h.recovery != nil {
		return h.recovery
	}
	return recoveryops.NewService(h.cfg.dir)
}

func (h *Hub) recoveryAllowed(w http.ResponseWriter, r *http.Request, action string) bool {
	who := principalOf(r.Context())
	if who.Role == roleFull {
		return true
	}
	h.logf("recovery: action=%s device=%s result=forbidden", action, recoveryActor(who))
	writeErr(w, http.StatusForbidden, i18n.Err("hub-recovery.err_full_only"))
	return false
}

func (h *Hub) auditRecovery(r *http.Request, action string, check recoveryops.Check, err error) {
	result := check.State
	if result == "" {
		result = "failed"
	}
	if errors.Is(err, recoveryops.ErrBusy) {
		result = "busy"
	} else if err != nil {
		result = "failed"
	}
	h.logf("recovery: action=%s device=%s result=%s", action, recoveryActor(principalOf(r.Context())), result)
}

func recoveryActor(who principal) string {
	if who.ID != "" {
		return who.ID
	}
	return "hub-token"
}

func writeRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, recoveryops.ErrInvalidBundleID):
		writeErr(w, http.StatusBadRequest, i18n.Err("hub-recovery.err_bundle_id"))
	case errors.Is(err, recoveryops.ErrBusy):
		writeErr(w, http.StatusConflict, i18n.Err("hub-recovery.err_busy"))
	case errors.Is(err, recoveryops.ErrBundleNotFound):
		writeErr(w, http.StatusNotFound, i18n.Err("hub-recovery.err_bundle_not_found"))
	default:
		// Operational errors can contain paths, database URLs and key material.
		// The terminal commands retain the detailed redacted diagnostic instead.
		writeErr(w, http.StatusServiceUnavailable, i18n.Err("hub-recovery.err_unavailable"))
	}
}
