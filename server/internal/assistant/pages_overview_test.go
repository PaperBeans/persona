package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Overview cards (`g_<16 hex>`): listed only with `?pages_overview=1` (for empty or `overview_card` placement),
// only for kept watch pages with on_overview, opening `pg_<16 hex>`, drawn as title + watch section alone with
// table displays capped at 5 rows.
func TestWatchOverviewPlacesGatingAndDrawing(t *testing.T) {
	s, _, database := pagesTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	tableWatch := `{"question":"New 1-bed listings under 2,000 USD","display":"table","columns":["Listing","Rent"],"alert":{"when":"rows","value":3}}`
	ownView := `{"fallback":"Notes","sections":[{"key":"notes","items":[{"type":"text","key":"n1","text":"Extra note not on Overview"}]}]}`
	onOv := keptWatchPage(t, database, "Elm listings", "list", tableWatch, ownView, true, false, now.Add(-time.Hour))

	numWatch := `{"question":"Rent on Elm","display":"number","unit":"USD"}`
	offOv := keptWatchPage(t, database, "Elm rent", "money", numWatch, "", false, false, now.Add(-time.Hour))
	removedOv := keptWatchPage(t, database, "Removed watch", "money", numWatch, "", true, false, now.Add(-time.Hour))
	if _, _, err := database.RemoveAssistantPage(ctx, removedOv.ID, db.AssistantPageRemovedByOwner, now); err != nil {
		t.Fatal(err)
	}
	plain := keptPage(t, database, "Morning run", "main", now.Add(-time.Hour), false)
	if _, _, err := database.SetAssistantPageOverview(ctx, plain.ID, true, now); err != nil {
		t.Fatal(err)
	}

	// Add an 8-row table reading to onOv.
	eightRows := [][]string{
		{"1 Elm St", "1,900"}, {"2 Elm St", "1,910"}, {"3 Elm St", "1,920"}, {"4 Elm St", "1,930"},
		{"5 Elm St", "1,940"}, {"6 Elm St", "1,950"}, {"7 Elm St", "1,960"}, {"8 Elm St", "1,970"},
	}
	rawReading, _ := json.Marshal(map[string]any{"rows": eightRows})
	if _, _, err := database.AddAssistantPageReading(ctx, db.NewAssistantPageReading{
		PageID: onOv.ID, Reading: rawReading, ByKind: db.AssistantPageByRoutine, At: now,
	}); err != nil {
		t.Fatal(err)
	}

	hook := pagesHook{svc: func() *Service { return s }}

	// 1. Gating on overview flag and placement.
	withoutOv := hook.Places(ctx, i18n.En, "", false)
	for _, pl := range withoutOv {
		if pl.Placement == "overview_card" {
			t.Fatalf("overview=false must list no overview_card: %+v", withoutOv)
		}
	}
	if got := hook.Places(ctx, i18n.En, "overview_card", false); len(got) != 0 {
		t.Fatalf("placement=overview_card with overview=false must be empty: %+v", got)
	}
	onlyOv := hook.Places(ctx, i18n.En, "overview_card", true)
	wantGID := overviewCardID(onOv.ID)
	if len(onlyOv) != 1 {
		t.Fatalf("want exactly 1 overview_card place, got %+v", onlyOv)
	}
	got := onlyOv[0]
	if got.Extension != extmgr.PagesExtension || got.ExtensionName != "Persona" || got.ID != wantGID ||
		got.Title != "Elm listings" || got.Placement != "overview_card" || got.Size != extension.SizeCompact ||
		got.Opens != onOv.ID || got.Badge != nil {
		t.Fatalf("overview_card place = %+v", got)
	}
	allWithOv := hook.Places(ctx, i18n.En, "", true)
	var ovCount int
	for _, pl := range allWithOv {
		if pl.Placement == "overview_card" {
			ovCount++
			if pl.ID != wantGID {
				t.Fatalf("unexpected overview_card %+v", pl)
			}
		}
	}
	if ovCount != 1 {
		t.Fatalf("expected 1 overview_card in combined places, got %+v", allWithOv)
	}
	navOnly := hook.Places(ctx, i18n.En, "nav_page", true)
	for _, pl := range navOnly {
		if pl.Placement == "overview_card" {
			t.Fatalf("placement=nav_page must not return overview_card: %+v", navOnly)
		}
	}

	// 2. Drawing g_<hex>: title + watch section alone (no own sections, no footer), table capped at 5 rows.
	res := mustPage(t, s, wantGID, wire.UIRequest{Context: &wire.UIContext{UIVersion: 10, Platform: "web"}})
	v := res.View
	if v.Title != "Elm listings" || len(v.Sections) != 1 || v.Sections[0].Key != "persona.watch" {
		t.Fatalf("overview card view = %+v", v)
	}
	if pageItem(v, "persona.reading").Value != "8 rows" {
		t.Fatalf("metric should still show total 8 rows: %+v", pageItem(v, "persona.reading"))
	}
	tbl := pageItem(v, "persona.rows")
	if tbl.Type != wire.ComponentTable || len(tbl.Rows) != overviewTableRows || tbl.Rows[4][0] != "5 Elm St" {
		t.Fatalf("overview table must be capped to %d rows: %+v", overviewTableRows, tbl.Rows)
	}
	if pageItem(v, "persona.footer").Type != "" || pageItem(v, "n1").Type != "" {
		t.Fatalf("overview card must contain only the watch section: %+v", v.Sections)
	}

	// Full page still shows all 8 rows plus own section and footer.
	full := mustPage(t, s, onOv.ID, wire.UIRequest{}).View
	if gotRows := len(pageItem(full, "persona.rows").Rows); gotRows != 8 {
		t.Fatalf("full page table rows = %d, want 8", gotRows)
	}

	// 3. Refusals: actions on Overview card 400; off-overview, removed, non-watch, unknown 404.
	if _, err := pagesUI(t, s, wantGID, wire.UIRequest{Action: "persona.refresh"}); !pagesRefused(err, http.StatusBadRequest) {
		t.Fatalf("action on overview card should 400: %v", err)
	}
	for _, gid := range []string{overviewCardID(offOv.ID), overviewCardID(removedOv.ID), overviewCardID(plain.ID), "g_0000000000000000"} {
		if _, err := pagesUI(t, s, gid, wire.UIRequest{}); !pagesRefused(err, http.StatusNotFound) {
			t.Fatalf("%s should 404, got %v", gid, err)
		}
	}

	// 4. Dashboards switch off hides Overview cards too.
	setSettings(t, s, func(v *Settings) { v.Dashboards = nil })
	if got := hook.Places(ctx, i18n.En, "overview_card", true); len(got) != 0 {
		t.Fatalf("dashboards off must list no overview cards: %+v", got)
	}
	if _, err := pagesUI(t, s, wantGID, wire.UIRequest{}); !pagesRefused(err, http.StatusNotFound) {
		t.Fatalf("dashboards off must 404 overview card: %v", err)
	}
}
