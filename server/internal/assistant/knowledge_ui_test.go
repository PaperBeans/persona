package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/knowledgeuihost"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// needsKnowledgeUIModule skips a test of the knowledge pages' module's behaviour when the build has none
// (persona_without_knowledge_ui, persona_core_only).
func needsKnowledgeUIModule(t testing.TB) {
	t.Helper()
	if !knowledgeuihost.InBuild(moduleswitch.Set{}) {
		t.Skip("the knowledge-ui module is not in this build (persona_without_knowledge_ui, persona_core_only)")
	}
}

// The history import's files as the core's tests read them from the knowledge base's directory (the module's
// kbhistory/files.go, plan.go and status.go write them): its plans, its STOP and its status.

type historyPlanFile struct {
	Source      string            `json:"source"`
	GeneratedAt time.Time         `json:"generated_at"`
	ApprovedAt  *time.Time        `json:"approved_at,omitempty"`
	Chats       []historyPlanChat `json:"chats"`
	// Approved is what each chat would be read as when the owner approved, ApprovedUnlisted a chat outside the list.
	Approved         map[string]kb.ReadPlan `json:"approved,omitempty"`
	ApprovedUnlisted *kb.ReadPlan           `json:"approved_unlisted,omitempty"`
}

type historyPlanChat struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Muted, Collapsed, Done, Bot, Estimate and FiledAs as the source listed them.
	Muted     bool   `json:"muted,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Bot       bool   `json:"bot,omitempty"`
	Estimate  int    `json:"estimate"`
	FiledAs   string `json:"filed_as,omitempty"`
}

type historyStopFile struct {
	By   string            `json:"by,omitempty"`
	Code string            `json:"code,omitempty"`
	Args map[string]string `json:"args,omitempty"`
	Text string            `json:"text,omitempty"`
}

type historyStatusLine struct {
	SlowUntil   time.Time `json:"slow_until,omitzero"`
	SlowPending bool      `json:"slow_pending,omitempty"`
}

func historyDirOf(data string) string { return filepath.Join(data, "history") }

func historyPlanPathOf(data, source string) string {
	return filepath.Join(historyDirOf(data), "plan-"+kb.SafeID(source)+".json")
}

func readHistoryJSON(t *testing.T, path string, v any) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return true
}

func writeHistoryJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// loadHistoryPlanErr is the source's plan in data; false when there is none (a file that does not read fails the
// test, so the error is always nil).
func loadHistoryPlanErr(t *testing.T, data, source string) (*historyPlanFile, bool, error) {
	t.Helper()
	var p historyPlanFile
	if !readHistoryJSON(t, historyPlanPathOf(data, source), &p) {
		return nil, false, nil
	}
	return &p, true, nil
}

// saveHistoryPlan and writeHistoryStop write a plan and a STOP as the import does.
func saveHistoryPlan(t *testing.T, data string, p historyPlanFile) {
	t.Helper()
	writeHistoryJSON(t, historyPlanPathOf(data, p.Source), p)
}

func writeHistoryStop(t *testing.T, data string, st historyStopFile) {
	t.Helper()
	writeHistoryJSON(t, filepath.Join(historyDirOf(data), "STOP"), st)
}

// readHistoryStop is what STOP says, and whether it is there.
func readHistoryStop(t *testing.T, data string) (historyStopFile, bool) {
	t.Helper()
	var st historyStopFile
	return st, readHistoryJSON(t, filepath.Join(historyDirOf(data), "STOP"), &st)
}

// readHistoryStatus are the sources' lines of status.json.
func readHistoryStatus(t *testing.T, data string) map[string]historyStatusLine {
	t.Helper()
	out := map[string]historyStatusLine{}
	readHistoryJSON(t, filepath.Join(historyDirOf(data), "status.json"), &out)
	return out
}

// historyIndexedNow: the knowledge base the module reaches through its host is open on data and has the search
// index (what the import of data waits for).
func historyIndexedNow(s *Service, data string) bool {
	e := knowledgeUIHost{s}.Current()
	return e != nil && e.Store.Dir == data && e.Store.SearchIndexReady()
}

// With the knowledge-ui module absent (left out of the build or switched off) the kb_history_* tools are not offered
// and answer as unknown, the selection page's chat list says the source has no history and asks nothing, and a
// knowledge base day comes as it is, with no sent marks. With it, all of these are there.
func TestKnowledgeUIFollowsItsModule(t *testing.T) {
	for _, c := range []struct {
		name string
		off  []string
		on   bool
	}{{"build", nil, knowledgeuihost.InBuild(moduleswitch.Set{})}, {"switched off", []string{knowledgeuihost.ModuleID}, false}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeHub()
			f.hello.KB = &KBSettings{History: &assistantwire.KBHistory{Enabled: true}}
			f.answerApp(AppConnectors, assistantwire.ConnectorsArchives, func(json.RawMessage) (any, error) {
				return assistantwire.Archives{Archives: []assistantwire.ArchiveSource{{ID: "chat", History: true}}}, nil
			})
			f.answerApp(AppConnectors, assistantwire.ConnectorsHistoryChats, func(json.RawMessage) (any, error) {
				return assistantwire.HistoryChatsResult{Chats: []connectorwire.HistoryChat{{ID: "d1", Name: "Direct One", Kind: "dm"}}}, nil
			})
			st := newKBStore()
			s := New(Options{Hub: f, Store: func() Store { return st }, Logf: t.Logf, KB: &KBOptions{Dir: t.TempDir()}, ModulesOff: c.off})
			t.Cleanup(s.kbEnd)
			if (s.ui != nil) != c.on {
				t.Fatalf("knowledge-ui layer %v, want %v", s.ui != nil, c.on)
			}
			var names []string
			for _, tool := range s.Tools(context.Background()) {
				names = append(names, tool.Name)
			}
			for _, name := range historyToolNames {
				if slices.Contains(names, name) != c.on {
					t.Errorf("tool %s offered: %v (module on: %v)", name, !c.on, c.on)
				}
			}
			if !c.on {
				if _, err := s.runTool(context.Background(), "", "", "kb_history_status", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "unknown tool") {
					t.Errorf("kb_history_status without the module: %v", err)
				}
			}
			kbEng(t, s)
			w := selectionCall(t, s, "GET", "/api/v1/kb/selection/candidates?source=chat", "", roleFull)
			if (w.Code == http.StatusOK) != c.on || !c.on && w.Code != http.StatusNotFound {
				t.Errorf("candidates: HTTP %d %s (module on: %v)", w.Code, w.Body, c.on)
			}
			if n := len(f.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats)); (n > 0) != c.on {
				t.Errorf("asked the source %d times (module on: %v)", n, c.on)
			}
		})
	}
}

func TestKnowledgeUIToolsFollowTheModuleSwitchBeforeTheLayerStarts(t *testing.T) {
	for _, c := range []struct {
		name string
		off  []string
		on   bool
	}{{"build", nil, knowledgeuihost.InBuild(moduleswitch.Set{})}, {"switched off", []string{knowledgeuihost.ModuleID}, false}} {
		t.Run(c.name, func(t *testing.T) {
			s := &Service{moduleSwitch: moduleswitch.Off(c.off...)}
			var names []string
			for _, tool := range s.Tools(context.Background()) {
				names = append(names, tool.Name)
			}
			for _, name := range historyToolNames {
				if slices.Contains(names, name) != c.on {
					t.Errorf("tool %s offered before the layer starts: %v (module on: %v)", name, !c.on, c.on)
				}
			}
		})
	}
}
