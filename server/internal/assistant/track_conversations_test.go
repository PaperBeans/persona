package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// trackConversationsService is an assistant on the server with two space tracks, qa-plain (no categories) and
// qa-finance (finance by default), on a store that keeps information categories and extension trust. qa-plain has
// three open conversations and an archived one; qa-finance one; the main space one.
func trackConversationsService(t *testing.T) (*Service, *trustMemStore) {
	t.Helper()
	f := healthHub()
	f.devices[0].Online = true
	plain := func(sid string, hours int, last string) proto.Session {
		return proto.Session{MachineID: "server", SessionID: sid, Cwd: "/home/me/.persona/tracks/qa-plain", Assistant: true,
			Space: "qa-plain", Title: "Plain " + sid, LastText: last, UpdatedAt: convDay.Add(time.Duration(hours) * time.Hour)}
	}
	old := plain("plain-03-0000", 9, "archived")
	old.Archived = true
	f.sessions["server"] = []proto.Session{
		{MachineID: "server", SessionID: "main-0000-0000", Cwd: "/home/me/.persona/assistant", Assistant: true, Title: "Persona",
			UpdatedAt: convDay.Add(8 * time.Hour)},
		plain("plain-00-0000", 1, "oldest"),
		plain("plain-01-0000", 5, strings.Repeat("é", 250)),
		plain("plain-02-0000", 3, "middle"),
		old,
		{MachineID: "server", SessionID: "fin-00-0000", Cwd: "/home/me/.persona/tracks/qa-finance", Assistant: true,
			Space: "qa-finance", Title: "Budget", LastText: "bills", UpdatedAt: convDay.Add(2 * time.Hour)},
	}
	st := &trustMemStore{infoCategoryMemStore: &infoCategoryMemStore{trackRegistryStore: &trackRegistryStore{memStore: &memStore{}, rows: []db.Track{
		{Slug: "qa-plain", Kind: trackKindSpace, Title: "QA plain", Template: trackTemplateSpace, Revision: 1},
		{Slug: "qa-finance", Kind: trackKindSpace, Title: "QA finance", Template: trackTemplateSpace, DefaultCategories: []string{categoryFinance}, Revision: 1},
		{Slug: "qa-health-like", Kind: trackKindSpace, Title: "QA health-like", Template: trackTemplateHealth, Revision: 1},
	}}}}
	s := newServiceOn(t, f, st)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })
	return s, st
}

// serveExtension is a request an extension's token sent, as the hub relays it.
func serveExtension(s *Service, ext, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set(RoleHeader, roleExtension)
	r.Header.Set(ExtensionHeader, ext)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func decodeTrackConversations(t *testing.T, w *httptest.ResponseRecorder) (assistantwire.TrackConversations, string) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out assistantwire.TrackConversations
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range out.Conversations {
		ids = append(ids, string(c.SessionID))
	}
	return out, strings.Join(ids, ",")
}

// A space's open conversations (#15991): newest first, limited, counted before the limit, the last text clipped;
// only a space track with a directory of its own has them.
func TestTrackConversationsForTheUser(t *testing.T) {
	s, _ := trackConversationsService(t)
	out, ids := decodeTrackConversations(t, serve(s, http.MethodGet, "/api/v1/assistant/tracks/qa-plain/conversations", ""))
	if ids != "plain-01-0000,plain-02-0000,plain-00-0000" || out.Total != 3 || out.MachineID != "server" {
		t.Fatalf("qa-plain: %s total %d machine %q", ids, out.Total, out.MachineID)
	}
	first := out.Conversations[0]
	if n := len([]rune(first.LastText)); n != assistantwire.TrackConversationTextMax {
		t.Errorf("last_text has %d runes", n)
	}
	if first.Space != "qa-plain" || first.MachineID != "server" || first.Title != "Plain plain-01-0000" || !first.UpdatedAt.Equal(convDay.Add(5*time.Hour)) {
		t.Errorf("first row: %+v", first)
	}

	out, ids = decodeTrackConversations(t, serve(s, http.MethodGet, "/api/v1/assistant/tracks/qa-plain/conversations?limit=2", ""))
	if ids != "plain-01-0000,plain-02-0000" || out.Total != 3 {
		t.Fatalf("limit 2: %s total %d", ids, out.Total)
	}
	if _, ids = decodeTrackConversations(t, serve(s, http.MethodGet, "/api/v1/assistant/tracks/qa-finance/conversations", "")); ids != "fin-00-0000" {
		t.Fatalf("qa-finance for the user: %s", ids)
	}
	for _, limit := range []string{"0", "51", "-1", "x", "%2B5", "05", "%205"} {
		if w := serve(s, http.MethodGet, "/api/v1/assistant/tracks/qa-plain/conversations?limit="+limit, ""); w.Code != http.StatusBadRequest {
			t.Errorf("limit %q: %d", limit, w.Code)
		}
	}
	for _, slug := range []string{"qa-unknown", SpaceHealth, "qa-health-like"} {
		if w := serve(s, http.MethodGet, "/api/v1/assistant/tracks/"+slug+"/conversations", ""); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", slug, w.Code)
		}
	}
}

// An extension (#15991) lists a space's conversations only through this route: a space hidden from it answers 404 and
// a row its policy hides is left out, of the total too; GET /api/v1/assistant?space= still shows it none.
func TestTrackConversationsForAnExtension(t *testing.T) {
	ctx := context.Background()
	s, st := trackConversationsService(t)
	out, ids := decodeTrackConversations(t, serveExtension(s, "qa-ext", "/api/v1/assistant/tracks/qa-plain/conversations"))
	if ids != "plain-01-0000,plain-02-0000,plain-00-0000" || out.Total != 3 {
		t.Fatalf("a visible space: %s total %d", ids, out.Total)
	}
	if w := serveExtension(s, "qa-ext", "/api/v1/assistant/tracks/qa-finance/conversations"); w.Code != http.StatusNotFound {
		t.Fatalf("a space hidden from extensions: %d", w.Code)
	}

	if err := s.tagConversation(ctx, "plain-02-0000", []string{categoryFinance}, infoTaggedByModel, ""); err != nil {
		t.Fatal(err)
	}
	out, ids = decodeTrackConversations(t, serveExtension(s, "qa-ext", "/api/v1/assistant/tracks/qa-plain/conversations"))
	if ids != "plain-01-0000,plain-00-0000" || out.Total != 2 {
		t.Fatalf("a row tagged finance: %s total %d", ids, out.Total)
	}
	if _, ids = decodeTrackConversations(t, serve(s, http.MethodGet, "/api/v1/assistant/tracks/qa-plain/conversations", "")); ids != "plain-01-0000,plain-02-0000,plain-00-0000" {
		t.Fatalf("the user, a row tagged finance: %s", ids)
	}

	// The user's own extension is shown the finance space and the tagged row.
	_ = st.SetExtensionTrust(ctx, "qa-owner", assistantwire.ExtensionTrustOwner, nil, "user")
	if _, ids = decodeTrackConversations(t, serveExtension(s, "qa-owner", "/api/v1/assistant/tracks/qa-finance/conversations")); ids != "fin-00-0000" {
		t.Fatalf("the user's own extension, qa-finance: %s", ids)
	}
	if _, ids = decodeTrackConversations(t, serveExtension(s, "qa-owner", "/api/v1/assistant/tracks/qa-plain/conversations")); ids != "plain-01-0000,plain-02-0000,plain-00-0000" {
		t.Fatalf("the user's own extension, qa-plain: %s", ids)
	}

	// The main list keeps its rule: none of a space's conversations to an extension.
	w := serveExtension(s, "qa-owner", "/api/v1/assistant?space=qa-plain")
	if w.Code != http.StatusOK {
		t.Fatalf("?space=: %d %s", w.Code, w.Body.String())
	}
	var view struct {
		Conversations []proto.Session `json:"conversations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Conversations) != 0 {
		t.Fatalf("?space= showed an extension %d conversations", len(view.Conversations))
	}
}
