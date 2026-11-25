package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/conversationlog"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// logicalSpaceMemStore is an in-memory Store implementing logicalConversationStore,
// conversationSpaceStore, and track lookup so logicalDevices offline-placeholder space
// isolation tests always run (including under go test -race without Postgres).
type logicalSpaceMemStore struct {
	*fleetMem
	mu         sync.Mutex
	logical    []db.LogicalConversation
	spaces     map[string]string
	tracks     []db.Track
	trackKinds []db.TrackKind
	spaceErr   error
}

var (
	_ logicalConversationStore = (*logicalSpaceMemStore)(nil)
	_ conversationSpaceStore   = (*logicalSpaceMemStore)(nil)
	_ trackStore               = (*logicalSpaceMemStore)(nil)
	_ trackKindStore           = (*logicalSpaceMemStore)(nil)
)

func (st *logicalSpaceMemStore) Tracks(context.Context) ([]db.Track, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.tracks), nil
}

func (st *logicalSpaceMemStore) TrackKinds(context.Context) ([]db.TrackKind, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.trackKinds), nil
}

func (st *logicalSpaceMemStore) NoteConversationSpaces(_ context.Context, rows []db.ConversationSpace) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.spaces == nil {
		st.spaces = map[string]string{}
	}
	for _, r := range rows {
		if existing, ok := st.spaces[r.SessionID]; !ok || existing == "" {
			st.spaces[r.SessionID] = r.Space
		}
	}
	return nil
}

func (st *logicalSpaceMemStore) ConversationSpaces(_ context.Context, sessions []string) (map[string]string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.spaceErr != nil {
		return nil, st.spaceErr
	}
	out := map[string]string{}
	for _, id := range sessions {
		if sp, ok := st.spaces[id]; ok {
			out[id] = sp
		}
	}
	return out, nil
}

func (st *logicalSpaceMemStore) LogicalConversations(context.Context) ([]db.LogicalConversation, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]db.LogicalConversation, len(st.logical))
	for i, c := range st.logical {
		c.Segments = slices.Clone(c.Segments)
		out[i] = c
	}
	return out, nil
}

func (st *logicalSpaceMemStore) LogicalConversation(_ context.Context, machine, session string) (*db.LogicalConversation, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.logical {
		c := st.logical[i]
		if c.MachineID == machine && c.SessionID == session {
			cp := c
			cp.Segments = slices.Clone(c.Segments)
			return &cp, nil
		}
		for _, seg := range c.Segments {
			if seg.MachineID == machine && seg.SessionID == session {
				cp := c
				cp.Segments = slices.Clone(c.Segments)
				return &cp, nil
			}
		}
	}
	return nil, nil
}

func (st *logicalSpaceMemStore) AdoptConversationAliases(context.Context, string, string, string, string, string, []string) error {
	return nil
}
func (st *logicalSpaceMemStore) WithConversationEpoch(context.Context, string, string, int64, func(*db.LogicalConversation) error) error {
	return nil
}
func (st *logicalSpaceMemStore) WithDrainedConversationEpoch(context.Context, string, string, int64, func(*db.LogicalConversation) error) error {
	return nil
}
func (st *logicalSpaceMemStore) BeginConversationSwitch(context.Context, string, string, db.ConversationSwitch) error {
	return nil
}
func (st *logicalSpaceMemStore) PrepareConversationSegment(context.Context, string, string, string, conversationlog.Segment, int) error {
	return nil
}
func (st *logicalSpaceMemStore) ActivateConversationSegment(context.Context, string, string, string, int64) error {
	return nil
}
func (st *logicalSpaceMemStore) ResolveConversationSwitch(context.Context, string, string, string, string, string) error {
	return nil
}
func (st *logicalSpaceMemStore) ConversationSwitch(context.Context, string, string, string) (*db.ConversationSwitch, error) {
	return nil, nil
}
func (st *logicalSpaceMemStore) ObserveConversationSource(context.Context, string, string, string) error {
	return nil
}
func (st *logicalSpaceMemStore) ObserveConversationRevision(context.Context, string, string, int64) error {
	return nil
}
func (st *logicalSpaceMemStore) ConversationSegmentClosed(context.Context, string) error { return nil }
func (st *logicalSpaceMemStore) EnqueueConversationInput(context.Context, string, string, string, int64, json.RawMessage) error {
	return nil
}
func (st *logicalSpaceMemStore) ConversationInputReceipt(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "", nil
}
func (st *logicalSpaceMemStore) ClaimConversationInput(context.Context, string, string, int64) (*db.ConversationInput, error) {
	return nil, nil
}
func (st *logicalSpaceMemStore) ResolveConversationInput(context.Context, string, string, string, string) error {
	return nil
}

func switchedLogical(machine, rootID, activeSegID string) db.LogicalConversation {
	return db.LogicalConversation{
		MachineID: machine,
		SessionID: rootID,
		Epoch:     2,
		Revision:  200,
		Segments: []conversationlog.Segment{
			placeholderSegment("seg-1", 1, machine, rootID, "claude", "retired"),
			placeholderSegment("seg-2", 2, machine, activeSegID, "codex", "active"),
		},
	}
}

// placeholderSegment is one segment of switchedLogical, built from the documented zero (archguard's out-literal rule).
func placeholderSegment(id string, ordinal int, machine, session, engine, state string) conversationlog.Segment {
	var s conversationlog.Segment
	s.ID, s.Ordinal, s.MachineID, s.SessionID, s.Engine, s.State = id, ordinal, machine, session, engine, state
	return s
}

func TestLogicalDevicesOfflinePlaceholderSpaceIsolationAndFailClosed(t *testing.T) {
	ctx := context.Background()
	const (
		healthTitle       = "Synthetic health offline title"
		healthTag         = "health-secret-tag"
		healthUndelivered = "Synthetic blood pressure draft"
		trackSlug         = "qa-finance"
		trackTitle        = "Synthetic finance offline title"
		trackTag          = "finance-secret-tag"
		trackUndelivered  = "Synthetic tax filing draft"
		segHealthTitle    = "Synthetic segment-noted health title"
		unknownTitle      = "Synthetic unknown-space title"
		unknownTag        = "unknown-secret-tag"
		unknownDraft      = "Synthetic unknown-space draft"
	)

	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}

	physical := newFleetDevs()
	physical.add("box", "Computer")
	physical.setSessions("box", []proto.Session{
		{SessionID: "main", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-1", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-2", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-3", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-4", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-5", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-6", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "coding-1", Cwd: "/code/example", Status: proto.StatusWorking},
	})

	kindCfg, err := json.Marshal(validKind())
	if err != nil {
		t.Fatal(err)
	}
	st := &logicalSpaceMemStore{
		fleetMem: newFleetMem(),
		trackKinds: []db.TrackKind{
			{Slug: "qa-space", Title: "QA space", Config: kindCfg, Revision: 1},
		},
		tracks: []db.Track{
			{Slug: trackSlug, Kind: "qa-space", Title: "QA finance", Template: trackTemplateSpace, DefaultCategories: []string{categoryFinance}, Revision: 1},
		},
		logical: []db.LogicalConversation{
			// 3 default-space pinned directions whose active switched physical segments vanished.
			switchedLogical("box", "dir-7", "seg-dir-7-active"),
			switchedLogical("box", "dir-8", "seg-dir-8-active"),
			switchedLogical("box", "dir-9", "seg-dir-9-active"),
			switchedLogical("box", "dir-archived", "seg-dir-archived-active"),
			// Health and custom-track switched conversations whose active segments vanished.
			switchedLogical("box", "health-offline", "seg-health-active"),
			switchedLogical("box", "track-offline", "seg-track-active"),
			// Health conversation whose space was noted only on its segment id, never on its root id.
			switchedLogical("box", "seg-health-offline", "seg-only-health-active"),
			// Unnoted logical conversation whose space cannot be determined (must fail closed).
			switchedLogical("box", "unknown-offline", "seg-unknown-active"),
		},
		spaces: map[string]string{
			"dir-7":                  "",
			"dir-8":                  "",
			"dir-9":                  "",
			"dir-archived":           "",
			"health-offline":         SpaceHealth,
			"track-offline":          trackSlug,
			"seg-only-health-active": SpaceHealth,
		},
	}
	if err := st.PutSettings(ctx, map[string]any{
		"assistant.main":    "main",
		"assistant.leaders": []string{"dir-1", "dir-2", "dir-3", "dir-4", "dir-5", "dir-6", "dir-7", "dir-8", "dir-9", "dir-archived"},
	}); err != nil {
		t.Fatal(err)
	}

	s := New(Options{Hub: f, Devices: physical, Store: func() Store { return st }, Logf: t.Logf})
	s.devs = &logicalDevices{Devices: physical, s: s}
	s.logicalChanged()
	s.fleet.rep.sessions = map[string]SessionView{
		"main":               {Title: "Persona"},
		"dir-7":              {Title: "Direction 7"},
		"dir-8":              {Title: "Direction 8"},
		"dir-9":              {Title: "Direction 9"},
		"dir-archived":       {Title: "Archived direction", Archived: true},
		"health-offline":     {Title: healthTitle, Tags: []string{healthTag}, Undelivered: []proto.UndeliveredMessage{{ID: "u-h", Text: healthUndelivered}}},
		"track-offline":      {Title: trackTitle, Tags: []string{trackTag}, Undelivered: []proto.UndeliveredMessage{{ID: "u-t", Text: trackUndelivered}}},
		"seg-health-offline": {Title: segHealthTitle},
		"unknown-offline":    {Title: unknownTitle, Tags: []string{unknownTag}, Undelivered: []proto.UndeliveredMessage{{ID: "u-u", Text: unknownDraft}}},
	}

	// Verify real logicalDevices.Session preserves/recovers Space on offline placeholders.
	for _, tc := range []struct {
		id        string
		wantSpace string
	}{
		{id: "dir-7", wantSpace: ""},
		{id: "health-offline", wantSpace: SpaceHealth},
		{id: "track-offline", wantSpace: trackSlug},
		{id: "seg-health-offline", wantSpace: SpaceHealth},
	} {
		row, ok := s.devs.Session("box", tc.id)
		if !ok || !row.LogicalPlaceholder || !row.Assistant {
			t.Fatalf("logicalDevices.Session(%q) = %+v, ok=%v; want offline LogicalPlaceholder", tc.id, row, ok)
		}
		if row.Space != tc.wantSpace {
			t.Fatalf("logicalDevices.Session(%q).Space = %q, want %q", tc.id, row.Space, tc.wantSpace)
		}
	}

	get := func(role, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if role != "" {
			req.Header.Set(RoleHeader, role)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	decodeIDs := func(t *testing.T, body []byte) []string {
		t.Helper()
		var out struct {
			Conversations []proto.Session `json:"conversations"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode conversations: %v (%s)", err, body)
		}
		ids := make([]string, 0, len(out.Conversations))
		for _, c := range out.Conversations {
			ids = append(ids, c.SessionID)
		}
		return ids
	}

	secretTokens := []string{
		"health-offline", healthTitle, healthTag, healthUndelivered,
		"track-offline", trackTitle, trackTag, trackUndelivered,
		"seg-health-offline", segHealthTitle,
		"unknown-offline", unknownTitle, unknownTag, unknownDraft,
	}

	// 1. Default space: device and extension both see main + all 9 unarchived pinned directions,
	//    and neither leaks Health, custom track, segment-noted Health, or unknown-space placeholders.
	wantDefault := []string{"main", "dir-1", "dir-2", "dir-3", "dir-4", "dir-5", "dir-6", "dir-7", "dir-8", "dir-9"}
	for _, role := range []string{"", roleExtension} {
		w := get(role, "/api/v1/assistant")
		if w.Code != http.StatusOK {
			t.Fatalf("role=%q GET /api/v1/assistant: %d %s", role, w.Code, w.Body)
		}
		if got := decodeIDs(t, w.Body.Bytes()); !slices.Equal(got, wantDefault) {
			t.Fatalf("role=%q default conversations = %v, want %v", role, got, wantDefault)
		}
		for _, secret := range secretTokens {
			if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
				t.Fatalf("role=%q GET /api/v1/assistant leaked %q: %s", role, secret, w.Body)
			}
		}
	}

	// 2. Health space: device sees both health-offline and seg-health-offline; extension sees zero rows and zero secrets.
	wHealthDev := get("", "/api/v1/assistant?space=health")
	if wHealthDev.Code != http.StatusOK {
		t.Fatalf("device ?space=health: %d %s", wHealthDev.Code, wHealthDev.Body)
	}
	if got := decodeIDs(t, wHealthDev.Body.Bytes()); !slices.Equal(got, []string{"health-offline", "seg-health-offline"}) {
		t.Fatalf("device ?space=health conversations = %v, want [health-offline seg-health-offline]", got)
	}
	wHealthExt := get(roleExtension, "/api/v1/assistant?space=health")
	if wHealthExt.Code != http.StatusOK {
		t.Fatalf("extension ?space=health: %d %s", wHealthExt.Code, wHealthExt.Body)
	}
	if got := decodeIDs(t, wHealthExt.Body.Bytes()); len(got) != 0 {
		t.Fatalf("extension ?space=health leaked conversations: %v", got)
	}
	for _, secret := range secretTokens {
		if bytes.Contains(wHealthExt.Body.Bytes(), []byte(secret)) {
			t.Fatalf("extension ?space=health leaked %q: %s", secret, wHealthExt.Body)
		}
	}

	// 3. Custom track space: device sees track-offline; extension sees zero rows and zero secrets.
	wTrackDev := get("", "/api/v1/assistant?space="+trackSlug)
	if wTrackDev.Code != http.StatusOK {
		t.Fatalf("device ?space=%s: %d %s", trackSlug, wTrackDev.Code, wTrackDev.Body)
	}
	if got := decodeIDs(t, wTrackDev.Body.Bytes()); !slices.Equal(got, []string{"track-offline"}) {
		t.Fatalf("device ?space=%s conversations = %v, want [track-offline]", trackSlug, got)
	}
	wTrackExt := get(roleExtension, "/api/v1/assistant?space="+trackSlug)
	if wTrackExt.Code != http.StatusOK {
		t.Fatalf("extension ?space=%s: %d %s", trackSlug, wTrackExt.Code, wTrackExt.Body)
	}
	if got := decodeIDs(t, wTrackExt.Body.Bytes()); len(got) != 0 {
		t.Fatalf("extension ?space=%s leaked conversations: %v", trackSlug, got)
	}
	for _, secret := range secretTokens {
		if bytes.Contains(wTrackExt.Body.Bytes(), []byte(secret)) {
			t.Fatalf("extension ?space=%s leaked %q: %s", trackSlug, secret, wTrackExt.Body)
		}
	}

	// 4. Extension feed access fails closed for Health, custom track, and unknown-space placeholders.
	for _, id := range []string{"health-offline", "track-offline", "seg-health-offline", "unknown-offline"} {
		wFeed := get(roleExtension, "/api/v1/assistant/conversations/"+id+"/feed")
		if wFeed.Code != http.StatusNotFound {
			t.Fatalf("extension feed for %s = %d %s, want 404", id, wFeed.Code, wFeed.Body)
		}
	}

	// 5. Unknown-space placeholder must never be poisoned into assistant_conversation_spaces as default space "".
	decorated := s.decorated("box")
	s.conversationSpacesListed("box", decorated)
	waitFor(t, "listed spaces written", s.listedSpacesWritten)
	st.mu.Lock()
	_, poisoned := st.spaces["unknown-offline"]
	st.mu.Unlock()
	if poisoned {
		t.Fatalf("conversationSpacesListed poisoned unknown-offline into assistant_conversation_spaces")
	}

	// 6. Store lookup failure fails closed for uncached logical placeholders (never treated as default space "").
	st.mu.Lock()
	st.logical = append(st.logical, switchedLogical("box", "err-offline", "seg-err-active"))
	st.spaceErr = errors.New("space store unavailable")
	st.mu.Unlock()
	s.logicalChanged()
	for _, role := range []string{"", roleExtension} {
		w := get(role, "/api/v1/assistant")
		if w.Code != http.StatusOK {
			t.Fatalf("role=%q GET /api/v1/assistant on space-store error: %d %s", role, w.Code, w.Body)
		}
		if got := decodeIDs(t, w.Body.Bytes()); slices.Contains(got, "err-offline") {
			t.Fatalf("role=%q exposed uncached logical placeholder during space-store error: %v", role, got)
		}
	}
}
