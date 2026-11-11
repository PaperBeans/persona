package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// 设置 › 模型供应商 with a bash machine, an offline one and one too slow to answer (#2854): the bash machine's
// launchers come with their rc file, plain claude shows as the built-in launcher by machine, every machine says how
// its list came in, and the page never waits past the per-machine timeout.
func TestLauncherListSaysHowEachMachineSynced(t *testing.T) {
	s, f, _ := modelService(t)
	old := launcherSyncTimeout
	launcherSyncTimeout = 300 * time.Millisecond
	t.Cleanup(func() { launcherSyncTimeout = old })
	f.mu.Lock()
	f.devices = []Device{
		{ID: "box", Name: "Box", Online: true, Reachable: true, ClaudeVersion: "2.1.0"},
		{ID: "slow", Name: "Slow", Online: true, Reachable: true},
		{ID: "away", Name: "Away"},
	}
	f.zshrc = map[string][]proto.Launcher{"box": {{Name: "claude", Model: "默认"}, {Name: "codex", Model: "默认", Agent: "codex", Source: "zshrc"},
		{Name: "kcc", BaseURL: "https://gw.example.com", Source: "zshrc"}}}
	f.rcFiles = map[string]string{"box": "~/.bashrc"}
	f.slowLaunchers = map[string]bool{"slow": true}
	for _, id := range []string{"box", "slow", "away"} {
		f.sessions[id] = nil
	}
	f.mu.Unlock()

	started := time.Now()
	code, raw := launcherCall(t, s, "GET", "/api/v1/launchers", "")
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("the list waited %v for the slow machine", took)
	}
	var res struct {
		Launchers []LauncherView `json:"launchers"`
		Sync      []LauncherSync `json:"sync"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(raw), &res) != nil {
		t.Fatalf("%d %s", code, raw)
	}
	byName := map[string]LauncherView{}
	for _, v := range res.Launchers {
		byName[v.Name] = v
	}
	if v := byName["kcc"]; v.Source != "zshrc" || !slices.Equal(v.RCFiles, []string{"~/.bashrc"}) || !slices.Equal(v.Machines, []string{"box"}) || len(v.Stale) != 0 {
		t.Errorf("kcc: %+v", v)
	}
	if v := byName["claude"]; v.Source != "builtin" || !slices.Equal(v.Machines, []string{"box"}) || v.Versions["box"] != "2.1.0" {
		t.Errorf("claude: %+v", v)
	}
	states := map[string]LauncherSync{}
	for _, st := range res.Sync {
		states[st.Machine] = st
	}
	if st := states["box"]; st.State != "ok" || st.RCFile != "~/.bashrc" || st.Shell != "bash" || st.SyncedAt == nil || st.Name != "Box" {
		t.Errorf("box: %+v", st)
	}
	if st := states["slow"]; st.State != "timeout" {
		t.Errorf("slow: %+v", st)
	}
	if st := states["away"]; st.State != "offline" {
		t.Errorf("away: %+v", st)
	}

	// The built-in claude has its own page and cannot be hidden (that would take plain claude out of every picker).
	if code, raw := launcherCall(t, s, "GET", "/api/v1/launchers/claude", ""); code != http.StatusOK {
		t.Errorf("detail: %d %s", code, raw)
	}
	if code, raw := launcherCall(t, s, "DELETE", "/api/v1/launchers/claude", ""); code != http.StatusBadRequest {
		t.Errorf("hide claude: %d %s", code, raw)
	}
	// 模型配置's 「Claude Code 默认」 says where it is.
	for _, p := range s.modelProviders(t.Context()) {
		if p.ID == "launcher.claude" && !slices.Equal(p.Machines, []string{"box"}) {
			t.Errorf("launcher.claude machines %v", p.Machines)
		}
	}
}

// A machine that answered before and is offline now keeps its launchers, marked stale, with its rc file.
func TestOfflineMachinesLaunchersAreStale(t *testing.T) {
	s, f, _ := modelService(t)
	f.mu.Lock()
	f.devices = []Device{{ID: "away", Name: "Away", Online: true, Reachable: true}}
	f.zshrc = map[string][]proto.Launcher{"away": {{Name: "kcc", BaseURL: "https://gw.example.com", Source: "zshrc"}}}
	f.sessions["away"] = nil
	f.mu.Unlock()
	if code, raw := launcherCall(t, s, "GET", "/api/v1/launchers", ""); code != 200 {
		t.Fatal(code, raw)
	}
	f.mu.Lock()
	f.devices = []Device{{ID: "away", Name: "Away"}}
	f.mu.Unlock()
	var raw string
	waitFor(t, "the offline launcher refresh", func() bool {
		_, raw = launcherCall(t, s, "GET", "/api/v1/launchers", "")
		var res struct {
			Launchers []LauncherView `json:"launchers"`
			Sync      []LauncherSync `json:"sync"`
		}
		_ = json.Unmarshal([]byte(raw), &res)
		i := slices.IndexFunc(res.Launchers, func(v LauncherView) bool { return v.Name == "kcc" })
		return i >= 0 && slices.Equal(res.Launchers[i].Stale, []string{"away"}) && slices.Equal(res.Launchers[i].RCFiles, []string{"~/.zshrc"}) &&
			len(res.Sync) == 1 && res.Sync[0].State == "offline" && res.Sync[0].SyncedAt != nil
	})
}

// A restored snapshot still names an offline machine before its background refresh can answer (#3198).
func TestRestoredLauncherListKeepsOfflineStale(t *testing.T) {
	s, f, _ := modelService(t)
	f.mu.Lock()
	f.devices = []Device{{ID: "away", Name: "Away", Online: true, Reachable: true}}
	f.zshrc = map[string][]proto.Launcher{"away": {{Name: "kcc", BaseURL: "https://gw.example.com", Source: "zshrc"}}}
	f.sessions["away"] = nil
	f.mu.Unlock()
	_ = s.zshrcLaunchers(t.Context(), true)
	s.models.zshrcMu.Lock()
	s.models.zshrc, s.models.zshrcSync = nil, nil
	s.models.zshrcMu.Unlock()
	f.mu.Lock()
	f.devices = []Device{{ID: "away", Name: "Away"}}
	f.mu.Unlock()

	code, raw := launcherCall(t, s, "GET", "/api/v1/launchers", "")
	var res struct {
		Launchers []LauncherView `json:"launchers"`
		Sync      []LauncherSync `json:"sync"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(raw), &res) != nil {
		t.Fatalf("%d %s", code, raw)
	}
	i := slices.IndexFunc(res.Launchers, func(v LauncherView) bool { return v.Name == "kcc" })
	if i < 0 || !slices.Equal(res.Launchers[i].Stale, []string{"away"}) {
		t.Fatalf("the restored row lost its offline mark: %s", raw)
	}
	if len(res.Sync) != 1 || res.Sync[0].State != "offline" || res.Sync[0].SyncedAt == nil {
		t.Fatalf("the restored sync lost its offline state: %+v", res.Sync)
	}
}

// A slow node never holds the list: the last snapshot answers while one refresh runs behind it (#3198).
func TestLauncherListAnswersLastSeenWhileRefreshRuns(t *testing.T) {
	s, f, _ := modelService(t)
	f.mu.Lock()
	f.devices = []Device{{ID: "box", Online: true, Reachable: true}, {ID: "slow", Online: true, Reachable: true}}
	f.zshrc = map[string][]proto.Launcher{"box": {{Name: "before"}}, "slow": {}}
	for _, id := range []string{"box", "slow"} {
		f.sessions[id] = nil
	}
	f.mu.Unlock()
	_ = s.zshrcLaunchers(t.Context(), true) // prime the last-seen snapshot without the route under test
	s.models.zshrcMu.Lock()
	s.models.zshrc, s.models.zshrcSync = nil, nil // a restart keeps the persisted snapshot, not the memory
	s.models.zshrcMu.Unlock()

	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	f.mu.Lock()
	f.zshrc = map[string][]proto.Launcher{"box": {{Name: "after"}}, "slow": {}}
	f.launcherGate = gate
	f.mu.Unlock()

	type answer struct {
		code int
		raw  string
	}
	asyncCall := func() <-chan answer {
		ch := make(chan answer, 1)
		go func() {
			r := httptest.NewRequest("GET", "/api/v1/launchers", strings.NewReader(""))
			ctx, cancel := context.WithCancel(r.Context())
			recorder := httptest.NewRecorder()
			s.ServeHTTP(recorder, r.WithContext(ctx))
			cancel() // the background refresh must outlive the client request
			ch <- answer{recorder.Code, recorder.Body.String()}
		}()
		return ch
	}
	await := func(ch <-chan answer) answer {
		t.Helper()
		select {
		case a := <-ch:
			return a
		case <-time.After(2 * time.Second):
			t.Fatal("the list waited for the gated node instead of answering last seen")
			return answer{}
		}
	}

	countCalls := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		calls := 0
		for _, call := range f.calls {
			if call.Method == DeviceLaunchersList {
				calls++
			}
		}
		return calls
	}
	first := await(asyncCall())
	waitFor(t, "the background refresh to reach both nodes", func() bool { return countCalls() >= 4 })
	second := await(asyncCall())
	for _, a := range []answer{first, second} {
		if a.code != http.StatusOK || !strings.Contains(a.raw, "before") || strings.Contains(a.raw, "after") {
			t.Fatalf("cached answer: %d %s", a.code, a.raw)
		}
	}
	if calls := countCalls(); calls != 4 { // two to prime, then one per machine for the single background refresh
		t.Fatalf("launchers.list calls = %d, want 4 (concurrent reads started another refresh)", calls)
	}

	release()
	waitFor(t, "the refreshed launcher snapshot", func() bool {
		snapshot, _ := s.zshrcLaunchersLastSeen(t.Context())
		for _, machine := range snapshot {
			for _, launcher := range machine.Launchers {
				if launcher.Name == "after" {
					return true
				}
			}
		}
		return false
	})
	code, raw := launcherCall(t, s, "GET", "/api/v1/launchers", "")
	if code != http.StatusOK || !strings.Contains(raw, "after") {
		t.Fatalf("the refreshed snapshot did not reach the route: %d %s", code, raw)
	}
}
