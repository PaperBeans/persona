package assistant

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Web Overview cards for Dashboards phase 2 watch pages (#13020, docs/assistant/dashboards.md "How a watch is drawn"):
// a kept watch page with on_overview has an overview_card place id `g_` + the page id's 16 hex digits, opening the
// page (`pg_<hex>`). Places are listed only when the client passes `?pages_overview=1` on GET /api/v1/extensions/-/ui.
// POST /api/v1/extensions/-pages/-/ui/g_<hex> draws the page's title and its `persona.watch` section alone (capping a
// table display at its first 5 rows).

const overviewTableRows = 5

var overviewIDPattern = lazyre.New(`^g_[0-9a-f]{16}$`)

// overviewCardID maps a page id `pg_<16 hex>` to its Overview card id `g_<16 hex>`.
func overviewCardID(pageID string) string {
	if hex, ok := strings.CutPrefix(pageID, "pg_"); ok {
		return "g_" + hex
	}
	return ""
}

// pageIDFromOverview maps an Overview card id `g_<16 hex>` to its page id `pg_<16 hex>`.
func pageIDFromOverview(gid string) string {
	if hex, ok := strings.CutPrefix(gid, "g_"); ok {
		return "pg_" + hex
	}
	return ""
}

// pageOverviewPlace is the overview_card place listed for kept watch page p when on_overview is on.
func pageOverviewPlace(p db.AssistantPageRow) extmgr.UIPlace {
	return extmgr.UIPlace{
		Extension:     extmgr.PagesExtension,
		ExtensionName: "Persona",
		ID:            overviewCardID(p.ID),
		Title:         p.Title,
		Placement:     "overview_card",
		Size:          extension.SizeCompact,
		Opens:         p.ID,
	}
}

// overviewCard answers POST /api/v1/extensions/-pages/-/ui/g_<hex>: the page's title and watch section alone (tables
// capped to overviewTableRows rows), read-only.
func (r *pageRequest) overviewCard(ctx context.Context, gid string) (extension.ActionResult, error) {
	if r.in.Action != "" {
		return extension.ActionResult{}, pagesRefusal(http.StatusBadRequest, "bad_request", "bad_action")
	}
	pid := pageIDFromOverview(gid)
	p, err := r.st.AssistantPage(ctx, pid)
	if errors.Is(err, db.ErrAssistantPageNotFound) || err == nil && (p.Status != db.AssistantPageActive || len(p.Watch) == 0 || !p.OnOverview) {
		return extension.ActionResult{}, pagesRefusal(http.StatusNotFound, "not_found", "not_found")
	}
	if err != nil {
		return extension.ActionResult{}, err
	}
	watch, err := wire.ParsePageWatch(p.Watch)
	if err != nil {
		v := drawBrokenPage(p, r.l)
		if err := r.fitSlot(&v, "overview_card"); err != nil {
			return extension.ActionResult{}, err
		}
		return extension.ActionResult{View: &v}, nil
	}
	rows, err := r.st.AssistantPageReadings(ctx, p.ID, db.AssistantPageReadingsKept)
	if err != nil {
		return extension.ActionResult{}, err
	}
	v := wire.View{
		Title: p.Title,
		Sections: []wire.Section{
			drawWatchSection(watch, parseWatchReadings(watch, rows), r.l, r.loc, r.now, overviewTableRows),
		},
	}
	if err := r.fitSlot(&v, "overview_card"); err != nil {
		r.s.logf("assistant pages: overview card %s does not fit UI version %d: %v", gid, r.version, err)
		v = drawBrokenPage(p, r.l)
		if err := r.fitSlot(&v, "overview_card"); err != nil {
			return extension.ActionResult{}, err
		}
	}
	return extension.ActionResult{View: &v}, nil
}
