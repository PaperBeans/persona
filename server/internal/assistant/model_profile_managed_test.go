package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func managedProfileService(t *testing.T) (*Service, *fakeHub, *downStore) {
	t.Helper()
	s, f, st := modelService(t)
	profileToolsDevices(s)
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", Model: "live-main", SmallModel: "live-small", Effort: "low"}})
	return s, f, st
}

func managedBusinessSnapshot(t *testing.T, s *Service, st *downStore) map[string]string {
	t.Helper()
	values, _ := profileStoreSnapshot(st.memStore)
	keys := []string{modelProfilesKey, modelProfileStateKey, launchersManagedKey, settingsKey}
	for _, id := range modelCategoryIDs {
		keys = append(keys, modelRecordKey(id))
	}
	out := map[string]string{}
	for _, key := range keys {
		out[key] = string(values[key])
	}
	return out
}

func runManagedProfileTool(t *testing.T, s *Service, name string, args any, allow bool, check func(proto.Interaction), beforeAnswer func()) proto.ToolResult {
	t.Helper()
	done := make(chan proto.ToolResult, 1)
	go func() {
		done <- s.Tool(context.Background(), ToolCall{Name: name, Arguments: json.RawMessage(managedJSON(t, args)), MachineID: "server", SessionID: "main"})
	}()
	card, ok := awaitCard(s)
	if !ok {
		t.Fatal("no approval card")
	}
	if check != nil {
		check(card)
	}
	if beforeAnswer != nil {
		beforeAnswer()
	}
	raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
	if _, err := s.Action(context.Background(), Action{Op: "answer", InteractionID: card.ID, Answer: raw}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("approved model-profile tool did not finish")
		return proto.ToolResult{}
	}
}

func managedJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func managedToolText(t *testing.T, result proto.ToolResult) string {
	t.Helper()
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("tool result: %+v", result)
	}
	return result.Content[0].Text
}

func managedProfile(t *testing.T, s *Service, id string) ModelProfileView {
	t.Helper()
	view := profilesOf(t, s)
	i := slices.IndexFunc(view.Profiles, func(p ModelProfileView) bool { return p.ID == id })
	if i < 0 {
		t.Fatalf("profile %s missing from %+v", id, view.Profiles)
	}
	return view.Profiles[i]
}

func TestManagedProfileBindingsSaveReadbackAndExplicitApply(t *testing.T) {
	s, _, st := managedProfileService(t)
	ctx := context.Background()
	live := modelRecord{Revision: 3, Binding: ModelBinding{Provider: "launcher.gw", Model: "live-old"}}
	if err := st.PutSetting(ctx, modelRecordKey("agent.new"), live); err != nil {
		t.Fatal(err)
	}
	tiers := map[string]ModelTier{"dev": {Launcher: "gw", Model: "tier-old", Aliases: []string{"standard"}}}
	storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance",
		Purposes:  map[string]ModelBinding{"agent.new": {Provider: "launcher.gw", Model: "profile-old"}},
		Launchers: map[string]LauncherModels{"gw": {SmallModel: profileValue("profile-small"), Effort: profileValue("low")}}, Tiers: tiers})
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "performance"}); err != nil {
		t.Fatal(err)
	}
	tiersBefore := managedJSON(t, tiers)
	saveOnly := i18n.T(i18n.Default, "assistant-settings_tool.model_profile_save_only")
	result := runManagedProfileTool(t, s, "persona_settings", map[string]any{"action": "set", "key": modelProfileBindingsKey,
		"value": map[string]any{"profile": "performance", "purposes": map[string]any{
			"agent.new": map[string]any{"provider": "launcher.gw", "model": "profile-new", "effort": "high"}},
			"launchers": map[string]any{"gw": map[string]any{"small_model": nil, "effort": "medium"}}}}, true,
		func(card proto.Interaction) {
			if card.ToolName != "persona_settings" {
				t.Errorf("card tool = %s", card.ToolName)
			}
			for _, text := range []string{"purposes.agent.new", "profile-old", "profile-new", "launchers.gw.small_model", "profile-small", saveOnly} {
				if !strings.Contains(card.Summary, text) {
					t.Errorf("card lacks %q:\n%s", text, card.Summary)
				}
			}
		}, nil)
	var saved struct {
		Revision int64            `json:"revision"`
		Profile  ModelProfileView `json:"profile"`
		Note     string           `json:"note"`
	}
	if err := json.Unmarshal([]byte(managedToolText(t, result)), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 || saved.Profile.Purposes["agent.new"].Model != "profile-new" || saved.Note != saveOnly {
		t.Fatalf("save result: %+v", saved)
	}
	readBack := managedProfile(t, s, "performance")
	if readBack.Purposes["agent.new"] != (ModelBinding{Provider: "launcher.gw", Model: "profile-new", Effort: "high"}) ||
		readBack.Launchers["gw"].SmallModel == nil || *readBack.Launchers["gw"].SmallModel != "" || *readBack.Launchers["gw"].Effort != "medium" {
		t.Fatalf("read-back: %+v", readBack)
	}
	if managedJSON(t, readBack.Tiers) != tiersBefore {
		t.Fatalf("bindings edit changed tiers: %s", managedJSON(t, readBack.Tiers))
	}
	if current, err := s.modelRecord(ctx, "agent.new"); err != nil || !reflect.DeepEqual(current, live) {
		t.Fatalf("save changed current purpose: %+v %v", current, err)
	}
	if current := s.storedLaunchers(ctx)[0]; current.SmallModel != "live-small" || current.Effort != "low" {
		t.Fatalf("save changed current launcher: %+v", current)
	}
	state, err := s.readModelProfileState(ctx)
	if err != nil || len(state.Events) != 1 {
		t.Fatalf("events: %+v %v", state.Events, err)
	}
	wantChanges := []string{"purposes.agent.new", "launchers.gw.small_model", "launchers.gw.effort"}
	if event := state.Events[0]; event.Action != "save" || event.To != "performance" || event.By.Kind != "assistant" || !slices.Equal(event.Changed, wantChanges) {
		t.Fatalf("save audit: %+v, want %v", event, wantChanges)
	}
	// Saving was deliberately not an apply. An explicit switch changes live settings and retains the standard rollback.
	requireProfileMCP(t, s, "model_profile_switch", map[string]any{"id": "performance"}, nil)
	if current, err := s.modelRecord(ctx, "agent.new"); err != nil || current.Binding.Model != "profile-new" || current.Binding.Effort != "high" {
		t.Fatalf("explicit apply: %+v %v", current, err)
	}
	if current := s.storedLaunchers(ctx)[0]; current.SmallModel != "" || current.Effort != "medium" {
		t.Fatalf("applied launcher: %+v", current)
	}
	if state, _ := s.readModelProfileState(ctx); len(state.rollbackStack()) == 0 {
		t.Fatal("explicit apply lost the existing switch rollback")
	}
}

func TestManagedProfileRevisionConflictAndDenialWriteNothing(t *testing.T) {
	for _, when := range []string{"revision conflict", "owner denies"} {
		t.Run(when, func(t *testing.T) {
			s, _, st := managedProfileService(t)
			storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance"}, ModelProfile{ID: "economy", Name: "Economy"})
			args := map[string]any{"action": "set", "key": modelProfileBindingsKey, "value": map[string]any{
				"profile": "performance", "purposes": map[string]any{"assistant": map[string]any{"provider": "launcher.gw", "model": "approved-model"}}}}
			allow := when != "owner denies"
			result := runManagedProfileTool(t, s, "persona_settings", args, allow, nil, func() {
				if when == "revision conflict" {
					// Change another profile: the catalog revision, not just the target's before-image, must protect the card.
					if _, err := s.saveModelProfile(context.Background(), nil, "economy", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
						p.Description = "edited while the card waited"
						return nil
					}); err != nil {
						t.Error(err)
					}
				}
			})
			text := managedToolText(t, result)
			if allow && !strings.Contains(text, "changed after the card") || !allow && !strings.Contains(text, "declined") {
				t.Fatalf("result: %s", text)
			}
			p := managedProfile(t, s, "performance")
			if _, ok := p.Purposes["assistant"]; ok {
				t.Fatalf("unapproved/stale binding was written: %+v", p.Purposes)
			}
			economy := managedProfile(t, s, "economy")
			if (economy.Description == "edited while the card waited") != allow {
				t.Fatalf("unrelated mutation was overwritten or missing: %+v", economy)
			}
			if allow && profilesOf(t, s).Revision != 2 || !allow && profilesOf(t, s).Revision != 1 {
				t.Fatalf("unexpected catalog revision %d", profilesOf(t, s).Revision)
			}
		})
	}
}

func TestManagedProfileClearPurposeRestoresInheritance(t *testing.T) {
	s, _, st := managedProfileService(t)
	ctx := context.Background()
	if err := st.PutSetting(ctx, modelRecordKey("agent.new"), modelRecord{Revision: 2, Binding: ModelBinding{Provider: "launcher.gw", Model: "explicit-old"}}); err != nil {
		t.Fatal(err)
	}
	storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance", Purposes: map[string]ModelBinding{
		"agent.new": {Provider: "launcher.gw", Model: "profile-old"}}})
	result := runManagedProfileTool(t, s, "persona_settings", map[string]any{"action": "set", "key": modelProfileBindingsKey,
		"value": map[string]any{"profile": "performance", "purposes": map[string]any{"agent.new": nil}}}, true, nil, nil)
	managedToolText(t, result)
	p := managedProfile(t, s, "performance")
	binding, ok := p.Purposes["agent.new"]
	if !ok || binding != (ModelBinding{}) {
		t.Fatalf("null did not make an explicit inherit binding: %+v", p.Purposes)
	}
	requireProfileMCP(t, s, "model_profile_switch", map[string]any{"id": "performance"}, nil)
	if current, err := s.modelRecord(ctx, "agent.new"); err != nil || current.Binding != (ModelBinding{}) {
		t.Fatalf("apply did not restore inheritance: %+v %v", current, err)
	}
}

func TestManagedProfileRejectsInvalidInputBeforeCard(t *testing.T) {
	s, _, st := managedProfileService(t)
	storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance"})
	before := managedBusinessSnapshot(t, s, st)
	for _, value := range []map[string]any{
		{"profile": "performance", "purposes": map[string]any{"unknown": map[string]any{}}},
		{"profile": "performance", "purposes": map[string]any{"kb": map[string]any{"permission_mode": "plan"}}},
		{"profile": "performance", "purposes": map[string]any{"assistant": map[string]any{"provider": "bad provider"}}},
		{"profile": "performance", "purposes": map[string]any{"assistant": map[string]any{"model": "bad model"}}},
		{"profile": "performance", "purposes": map[string]any{"assistant": map[string]any{"api_key": "not-allowed"}}},
		{"profile": "performance", "launchers": map[string]any{"gw": map[string]any{"base_url": "https://example.invalid"}}},
	} {
		result := s.Tool(context.Background(), ToolCall{Name: "persona_settings", MachineID: "server", SessionID: "main",
			Arguments: json.RawMessage(managedJSON(t, map[string]any{"action": "set", "key": modelProfileBindingsKey, "value": value}))})
		if !result.IsError {
			t.Errorf("invalid value succeeded: %+v", value)
		}
	}
	if len(s.pendingQuestions()) != 0 {
		t.Fatal("invalid input created an approval card")
	}
	if after := managedBusinessSnapshot(t, s, st); !reflect.DeepEqual(after, before) {
		t.Fatal("invalid input changed model settings")
	}
}

func TestModelProfileCapturePreservesTiersAndCurrentConfiguration(t *testing.T) {
	s, _, st := managedProfileService(t)
	ctx := context.Background()
	live := modelRecord{Revision: 4, Binding: ModelBinding{Provider: "launcher.gw", Model: "live-new", Effort: "high"}}
	if err := st.PutSetting(ctx, modelRecordKey("agent.new"), live); err != nil {
		t.Fatal(err)
	}
	customTiers := map[string]ModelTier{"dev": {Launcher: "gw", Model: "kept-tier", Description: "Keep this preset",
		Candidates: []TierCandidate{{Launcher: "gw", Model: "kept-tier"}, {Launcher: "claude", Model: "fallback-model"}}, Aliases: []string{"standard"}}}
	storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance", Purposes: map[string]ModelBinding{
		"agent.new": {Provider: "launcher.gw", Model: "stored-old"}}, Tiers: customTiers})
	tiersBefore := managedJSON(t, customTiers)
	captureNote := i18n.T(i18n.Default, "assistant-settings_tool.model_profile_capture_note")
	result := runManagedProfileTool(t, s, "model_profile_capture", map[string]any{"id": "performance"}, true, func(card proto.Interaction) {
		if card.ToolName != "model_profile_capture" || !strings.Contains(card.Summary, captureNote) {
			t.Errorf("capture card: %+v", card)
		}
	}, nil)
	managedToolText(t, result)
	p := managedProfile(t, s, "performance")
	if len(p.Purposes) != len(modelCategoryIDs) || p.Purposes["agent.new"] != live.Binding {
		t.Fatalf("capture purposes: %+v", p.Purposes)
	}
	if managedJSON(t, p.Tiers) != tiersBefore {
		t.Fatalf("capture changed tiers:\nbefore %s\nafter  %s", tiersBefore, managedJSON(t, p.Tiers))
	}
	if current, err := s.modelRecord(ctx, "agent.new"); err != nil || !reflect.DeepEqual(current, live) {
		t.Fatalf("capture changed current purpose: %+v %v", current, err)
	}
	if current := s.storedLaunchers(ctx)[0]; current.Model != "live-main" || current.SmallModel != "live-small" || current.Effort != "low" {
		t.Fatalf("capture changed current launcher: %+v", current)
	}
}

func TestModelProfileSaveEventRecordsActualWebDiffNotLiveDrift(t *testing.T) {
	s, _, st := managedProfileService(t)
	storeProfiles(t, st, ModelProfile{ID: "performance", Name: "Performance",
		Purposes:  map[string]ModelBinding{"assistant": {Provider: "launcher.gw", Model: "stored-old"}},
		Launchers: map[string]LauncherModels{"gw": {SmallModel: profileValue("old-small")}},
		Tiers:     map[string]ModelTier{"dev": {Launcher: "gw", Model: "old-tier"}}})
	body := `{"revision":1,"name":"Performance","purposes":{"assistant":{"provider":"launcher.gw","model":"stored-new"}},"launchers":{"gw":{"small_model":"new-small"}},"tiers":{"dev":{"launcher":"gw","model":"new-tier"}}}`
	response := request(s, http.MethodPut, "/api/v1/model-profiles/performance", body)
	if response.Code != http.StatusOK {
		t.Fatalf("web save: %d %s", response.Code, response.Body)
	}
	state, err := s.readModelProfileState(context.Background())
	if err != nil || len(state.Events) != 1 {
		t.Fatalf("events: %+v %v", state.Events, err)
	}
	want := []string{"purposes.assistant", "launchers.gw.small_model", "tiers.dev"}
	if event := state.Events[0]; event.Action != "save" || !slices.Equal(event.Changed, want) || event.By.Kind != "hub" {
		t.Fatalf("save event: %+v, want changed %v", event, want)
	}
	p := managedProfile(t, s, "performance")
	if p.Purposes["assistant"].Model != "stored-new" || !slices.Contains(p.Changes, "assistant") {
		t.Fatalf("saved value and independent live drift should coexist: %+v", p)
	}
	// Older readers that do not know changed still decode the event normally.
	var oldReader struct {
		Action string `json:"action"`
		To     string `json:"to"`
	}
	if raw := managedJSON(t, state.Events[0]); json.Unmarshal([]byte(raw), &oldReader) != nil || oldReader.Action != "save" || oldReader.To != "performance" {
		t.Fatalf("old reader compatibility: %s", raw)
	}
}
