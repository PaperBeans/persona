package extmgr

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The assistant's pages (Dashboards, #15017) are drawn where the extensions' pages are, under a reserved extension id
// no extension can have (ids starting with "-" are the hub's own, as -subscriptions is): the apps already list and draw
// an extension's nav_pages, so a kept page needs no new route and no new client screen. The manager's assistant answers
// through Options.Pages. GET /api/v1/extensions/-/ui lists its places after the extensions' own; POST
// /api/v1/extensions/-pages/-/ui/{ui} goes to it, never to an extension's process, for a device with full access only.
// The native UI stream never serves an id starting with "-" (pageOpen): those pages are drawn over HTTP alone.

// PagesExtension is the reserved extension id the assistant's pages are listed and drawn under.
const PagesExtension = "-pages"

// Pages draws the assistant's pages.
type Pages interface {
	// Places are the pages' entries in the sidebar (and Overview cards when overview is true) for placement
	// ("" every one), titled in lang: none while the owner keeps them off.
	Places(ctx context.Context, lang i18n.Lang, placement string, overview bool) []UIPlace
	// UI draws page ui, or answers an action on it, for the owner's device (its id), in lang. A *PagesError says
	// which HTTP status a refusal answers.
	UI(ctx context.Context, lang i18n.Lang, ui string, in wire.UIRequest, device string) (extension.ActionResult, error)
}

// PagesError is a refusal of Pages.UI: the HTTP status and code it answers.
type PagesError struct {
	Status int
	Code   string
	Err    error
}

func (e *PagesError) Error() string { return e.Err.Error() }
func (e *PagesError) Unwrap() error { return e.Err }

// reservedExtension says whether id is one of the hub's own (an extension's id never starts with "-").
func reservedExtension(id string) bool { return strings.HasPrefix(id, "-") }

// pagesPlaces are the assistant's pages' places for the request: only for one of the owner's own devices, never an
// extension's token or a device that can only answer. Overview cards are included only when overview is true
// (`?pages_overview=1`) and placement is empty or `overview_card`.
func (h *Manager) pagesPlaces(r *http.Request, placement string, overview bool) []UIPlace {
	p := principalOf(r.Context())
	if h.opts.Pages == nil || p.Role != roleFull || p.Extension != "" {
		return nil
	}
	wantNav := placement == "" || extension.FullPage(placement)
	wantOverview := overview && (placement == "" || placement == "overview_card")
	if !wantNav && !wantOverview {
		return nil
	}
	return h.opts.Pages.Places(r.Context(), h.requestLang(r), placement, overview)
}

// pagesUI answers POST /api/v1/extensions/-pages/-/ui/{ui}; the caller checked the device's role.
func (h *Manager) pagesUI(w http.ResponseWriter, r *http.Request) {
	if h.opts.Pages == nil {
		writeExtensionResponseError(w, r, http.StatusNotFound, "not_found", errNoExtension)
		return
	}
	var in wire.UIRequest
	if !readExtensionJSON(w, r, &in) {
		return
	}
	// the request's UI language wins; an older app says its system's in the body
	lang := i18n.FromRequest(r)
	if !i18n.Valid(lang) {
		if in.Context != nil && in.Context.Locale != "" {
			lang = i18n.Resolve(in.Context.Locale)
		} else {
			lang = h.requestLang(r)
		}
	}
	res, err := h.opts.Pages.UI(r.Context(), lang, r.PathValue("ui"), in, principalOf(r.Context()).ID)
	var pe *PagesError
	switch {
	case errors.As(err, &pe):
		writeExtensionResponseError(w, r, pe.Status, pe.Code, pe.Err)
	case err != nil:
		writeExtensionResponseError(w, r, http.StatusServiceUnavailable, "unavailable", err)
	default:
		writeJSON(w, res)
	}
}
