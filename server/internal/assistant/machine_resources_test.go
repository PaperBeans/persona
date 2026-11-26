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

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestMachineResourcesSummaryFieldsAndPrivacy(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "hub-box", Name: "Hub", Kind: "local", Online: true, Reachable: true},
		{ID: "worker-a", Name: "Worker A", Kind: "remote", Online: true, Reachable: true},
		{ID: "stale-box", Name: "Stale", Kind: "remote", Online: true, Reachable: true},
		{ID: "offline-box", Name: "Offline", Kind: "remote"},
		{ID: "pending-box", Name: "Pending", Kind: "remote", Online: true},
	}
	f.devicesFull = true
	f.devicesOther = []string{"hidden-peer"}
	f.sessions["hub-box"] = []proto.Session{
		{SessionID: "pub-work", Title: "Public job", Status: proto.StatusWorking, Cwd: "/work/repo"},
		{SessionID: "priv-space", Title: "Private health", Status: proto.StatusWorking, Space: SpaceHealth, Cwd: "/work/other"},
		{SessionID: "priv-dir", Title: "Private dir", Status: proto.StatusWorking, Cwd: identity.HealthDir("/home/owner")},
		{SessionID: "idle-job", Title: "Idle", Status: proto.StatusIdle, Cwd: "/work/repo"},
		{SessionID: "arch-job", Title: "Archived", Status: proto.StatusWorking, Archived: true, Cwd: "/work/repo"},
	}
	f.sessions["worker-a"] = []proto.Session{
		{SessionID: "w1", Status: proto.StatusWorking, Cwd: "/work/repo"},
	}
	f.sessions["stale-box"] = nil

	cpu, load := 22.5, 1.25
	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines: map[string]*proto.Machine{
			"hub-box": {
				Kind:   "local",
				OS:     "linux",
				Arch:   "amd64",
				System: &proto.System{Name: "Linux 6.8", OS: "linux", Arch: "amd64", Cores: 16},
			},
			"worker-a": {
				Kind:   "remote",
				OS:     "darwin",
				Arch:   "arm64",
				System: &proto.System{Name: "macOS 15", OS: "darwin", Arch: "arm64", Cores: 12},
			},
			"stale-box":   {OS: "linux", Arch: "amd64"},
			"offline-box": {OS: "linux", Arch: "amd64", Error: "ssh handshake failed: secret-internal-detail"},
			"pending-box": {},
		},
		samples: map[string]*proto.MetricSample{
			"hub-box": {
				T:         time.Now().Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  32 << 30,
				MemUsed:   12 << 30,
				DiskTotal: 500 << 30,
				DiskUsed:  200 << 30,
			},
			"worker-a": {
				T:         time.Now().Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  64 << 30,
				MemUsed:   16 << 30,
				DiskTotal: 1000 << 30,
				DiskUsed:  300 << 30,
			},
			"stale-box": {
				T:         time.Now().Add(-45 * time.Second).Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  16 << 30,
				MemUsed:   4 << 30,
				DiskTotal: 200 << 30,
				DiskUsed:  50 << 30,
			},
		},
		versions: map[string]*bool{},
	}
	for _, id := range []string{"hub-box", "worker-a", "stale-box"} {
		d.add(id, id)
		d.setSessions(id, f.sessions[id])
	}

	s := newTestService(t, f)
	s.devs = d
	s.health.loaded = true
	s.health.recs = map[string]*LauncherHealth{
		"worker-a/codex":  {Machine: "worker-a", Launcher: "codex", State: healthFailing, Error: "secret oauth error token", CheckedAt: time.Now()},
		"worker-a/claude": {Machine: "worker-a", Launcher: "claude", State: healthFailing, Error: "rate limited", CheckedAt: time.Now()},
		"worker-a/old":    {Machine: "worker-a", Launcher: "old", State: healthFailing, CheckedAt: time.Now().Add(-24 * time.Hour)},
	}

	get := func(role string) (string, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		if role != "" {
			req.Header.Set(RoleHeader, role)
		}
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s status=%d body=%s", role, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return rec.Body.String(), out
	}

	extBody, extOut := get(roleExtension)
	for _, leak := range []string{"secret-internal-detail", "secret oauth error token", "rate limited", "Private health", "priv-space", "priv-dir", "pub-work"} {
		if strings.Contains(extBody, leak) {
			t.Fatalf("machine-resources leaked %q: %s", leak, extBody)
		}
	}
	if extOut["complete"] != true || extOut["excluded_count"] != float64(1) {
		t.Fatalf("top-level flags: %s", extBody)
	}
	rows, _ := extOut["machines"].([]any)
	if len(rows) != 5 {
		t.Fatalf("machines count = %d, want 5", len(rows))
	}
	byID := map[string]map[string]any{}
	for _, item := range rows {
		m := item.(map[string]any)
		byID[m["id"].(string)] = m
	}

	hub := byID["hub-box"]
	if hub["is_hub"] != true || hub["os"] != "linux" || hub["arch"] != "amd64" || hub["online"] != true ||
		hub["cores"] != float64(16) || hub["memory_available_bytes"] != float64(20<<30) || hub["memory_total_bytes"] != float64(32<<30) ||
		hub["disk_free_bytes"] != float64(300<<30) || hub["disk_total_bytes"] != float64(500<<30) {
		t.Fatalf("hub summary: %v", hub)
	}
	// Extension token excludes every space session (both explicit Space and HealthDir) from working_agents.
	if hub["working_agents"] != float64(1) {
		t.Fatalf("extension hub working_agents = %v, want 1", hub["working_agents"])
	}

	worker := byID["worker-a"]
	if worker["is_hub"] != false || worker["os"] != "darwin" || worker["arch"] != "arm64" ||
		worker["disk_free_bytes"] != float64(700<<30) || worker["disk_total_bytes"] != float64(1000<<30) ||
		worker["working_agents"] != float64(1) {
		t.Fatalf("worker summary: %v", worker)
	}
	rawFail, _ := worker["failing_launchers"].([]any)
	gotFail := make([]string, 0, len(rawFail))
	for _, item := range rawFail {
		gotFail = append(gotFail, item.(string))
	}
	if !slices.Equal(gotFail, []string{"claude", "codex"}) {
		t.Fatalf("worker failing_launchers = %v, want [claude codex]", gotFail)
	}

	// Stale, offline, and pending machines return explicit null for unavailable numeric metrics.
	for _, id := range []string{"stale-box", "offline-box", "pending-box"} {
		row := byID[id]
		for _, field := range []string{"cpu_busy_percent", "load_1m", "memory_available_bytes", "memory_total_bytes", "disk_free_bytes", "disk_total_bytes"} {
			val, exists := row[field]
			if !exists || val != nil {
				t.Fatalf("%s.%s = %v (exists=%v), want explicit null", id, field, val, exists)
			}
		}
	}
	if byID["offline-box"]["online"] != false || byID["offline-box"]["state"] != "offline" || byID["offline-box"]["working_agents"] != nil {
		t.Fatalf("offline summary: %v", byID["offline-box"])
	}
	if byID["pending-box"]["online"] != false || byID["pending-box"]["state"] != "pending" {
		t.Fatalf("pending summary: %v", byID["pending-box"])
	}

	// Full role keeps ordinary machines_list counting (counts non-hidden-by-dir working agents including SpaceHealth on a regular cwd).
	_, fullOut := get(roleFull)
	for _, item := range fullOut["machines"].([]any) {
		m := item.(map[string]any)
		if m["id"] == "hub-box" && m["working_agents"] != float64(2) {
			t.Fatalf("full role hub working_agents = %v, want 2", m["working_agents"])
		}
	}
}

func TestMachineResourcesAmbiguousFutureAndReconnectAreNull(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "future", Name: "Future", Online: true, Reachable: true},
		{ID: "reconn", Name: "Reconn", Online: true, Reachable: true},
		{ID: "ambig", Name: "Ambig", Online: true, Reachable: true},
	}
	f.devicesFull = true
	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines: map[string]*proto.Machine{
			"future": {OS: "linux", Arch: "amd64"},
			"reconn": {OS: "linux", Arch: "amd64"},
			"ambig":  {OS: "linux", Arch: "amd64"},
		},
		samples: map[string]*proto.MetricSample{
			"future": {T: time.Now().Add(30 * time.Second).Unix(), MemTotal: 100, MemUsed: 40, DiskTotal: 100, DiskUsed: 40},
			"reconn": {T: time.Now().Add(-10 * time.Second).Unix(), MemTotal: 100, MemUsed: 40, DiskTotal: 100, DiskUsed: 40},
			"ambig":  {T: time.Now().Unix(), MemTotal: 100, MemUsed: 0, DiskTotal: 100, DiskUsed: 0},
		},
		versions: map[string]*bool{},
	}
	node := d.add("reconn", "Reconn")
	node.connected = time.Now().Add(-2 * time.Second)
	s := newTestService(t, f)
	s.devs = d

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
	req.Header.Set(RoleHeader, roleExtension)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Machines {
		for _, field := range []string{"memory_available_bytes", "memory_total_bytes", "disk_free_bytes", "disk_total_bytes"} {
			if m[field] != nil {
				t.Fatalf("%s.%s = %v, want null", m["id"], field, m[field])
			}
		}
		if m["id"] == "future" && m["sample_age_seconds"] != nil {
			t.Fatalf("future sample_age_seconds = %v, want null", m["sample_age_seconds"])
		}
	}
}

func TestMachineResourcesUnlistedSessionsReturnNull(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "unlisted-box", Name: "Unlisted", Online: true, Reachable: true},
		{ID: "empty-listed-box", Name: "Empty Listed", Online: true, Reachable: true},
	}
	f.devicesFull = true

	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines: map[string]*proto.Machine{
			"unlisted-box":     {OS: "linux", Arch: "amd64"},
			"empty-listed-box": {OS: "linux", Arch: "amd64"},
		},
		samples:  map[string]*proto.MetricSample{},
		versions: map[string]*bool{},
	}
	unlisted := d.add("unlisted-box", "Unlisted")
	unlisted.unlisted = true
	d.add("empty-listed-box", "Empty Listed")
	d.setSessions("empty-listed-box", []proto.Session{})

	s := newTestService(t, f)
	s.devs = d

	for _, role := range []string{roleExtension, roleScoped, roleFull} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		req.Header.Set(RoleHeader, role)
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s status=%d body=%s", role, rec.Code, rec.Body.String())
		}
		var out struct {
			Machines []map[string]any `json:"machines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		byID := map[string]map[string]any{}
		for _, m := range out.Machines {
			byID[m["id"].(string)] = m
		}
		if val, exists := byID["unlisted-box"]["working_agents"]; !exists || val != nil {
			t.Fatalf("role=%s unlisted-box working_agents = %v (exists=%v), want explicit null", role, val, exists)
		}
		if got := byID["empty-listed-box"]["working_agents"]; got != float64(0) {
			t.Fatalf("role=%s empty-listed-box working_agents = %v, want 0", role, got)
		}
	}

	text, err := s.toolMachinesList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var factsOut struct {
		Machines []machineFacts `json:"machines"`
	}
	if err := json.Unmarshal([]byte(text), &factsOut); err != nil {
		t.Fatal(err)
	}
	byID := map[string]machineFacts{}
	for _, m := range factsOut.Machines {
		byID[m.ID] = m
	}
	if byID["unlisted-box"].WorkingAgents.Value != nil || byID["unlisted-box"].WorkingAgents.Reason != "sessions_not_listed" {
		t.Fatalf("machines_list unlisted-box working_agents = %+v, want null with sessions_not_listed", byID["unlisted-box"].WorkingAgents)
	}
	if byID["empty-listed-box"].WorkingAgents.Value != float64(0) || byID["empty-listed-box"].WorkingAgents.Reason != "" {
		t.Fatalf("machines_list empty-listed-box working_agents = %+v, want known 0", byID["empty-listed-box"].WorkingAgents)
	}
}

func TestMachineResourcesScopedTokenExcludesSpaceSessions(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "box", Name: "Box", Online: true, Reachable: true},
	}
	f.devicesFull = true
	f.sessions["box"] = []proto.Session{
		{SessionID: "pub-work", Title: "Public", Status: proto.StatusWorking, Cwd: "/work/repo"},
		{SessionID: "health-space", Title: "Health Space", Status: proto.StatusWorking, Space: SpaceHealth, Cwd: "/work/other"},
		{SessionID: "custom-space", Title: "Custom Space", Status: proto.StatusWorking, Space: "finance", Cwd: "/work/finance"},
		{SessionID: "health-dir", Title: "Health Dir", Status: proto.StatusWorking, Cwd: identity.HealthDir("/home/owner")},
		{SessionID: "idle-job", Title: "Idle", Status: proto.StatusIdle, Cwd: "/work/repo"},
	}

	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines:  map[string]*proto.Machine{"box": {OS: "linux", Arch: "amd64"}},
		samples:   map[string]*proto.MetricSample{},
		versions:  map[string]*bool{},
	}
	d.add("box", "Box")
	d.setSessions("box", f.sessions["box"])

	s := newTestService(t, f)
	s.devs = d

	fetch := func(role string) (string, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		req.Header.Set(RoleHeader, role)
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s status=%d body=%s", role, rec.Code, rec.Body.String())
		}
		var out struct {
			Machines []map[string]any `json:"machines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Machines) != 1 {
			t.Fatalf("role=%s machines len = %d, want 1", role, len(out.Machines))
		}
		return rec.Body.String(), out.Machines[0]
	}

	for _, role := range []string{roleExtension, roleScoped} {
		body, row := fetch(role)
		if row["working_agents"] != float64(1) {
			t.Fatalf("role=%s working_agents = %v, want 1 (body=%s)", role, row["working_agents"], body)
		}
		for _, leak := range []string{"Health Space", "Custom Space", "Health Dir", "health-space", "custom-space", "health-dir", "pub-work"} {
			if strings.Contains(body, leak) {
				t.Fatalf("role=%s leaked %q: %s", role, leak, body)
			}
		}
	}

	_, fullRow := fetch(roleFull)
	text, err := s.toolMachinesList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var factsOut struct {
		Machines []machineFacts `json:"machines"`
	}
	if err := json.Unmarshal([]byte(text), &factsOut); err != nil {
		t.Fatal(err)
	}
	if fullRow["working_agents"] != factsOut.Machines[0].WorkingAgents.Value {
		t.Fatalf("full role working_agents = %v, machines_list = %v", fullRow["working_agents"], factsOut.Machines[0].WorkingAgents.Value)
	}
	if fullRow["working_agents"].(float64) < 3 {
		t.Fatalf("full role working_agents = %v, want >= 3", fullRow["working_agents"])
	}
}

type hookedResourceDevs struct {
	*resourceDevs
	onMachineResources func(id string)
}

func (d *hookedResourceDevs) MachineResources(id string) (*proto.Machine, *proto.MetricSample, *bool) {
	if d.onMachineResources != nil {
		d.onMachineResources(id)
	}
	return d.resourceDevs.MachineResources(id)
}

func TestMachineResourcesSampleAgeAfterSlowRPC(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cpu, load := 18.5, 0.75

	t.Run("slow devices list RPC", func(t *testing.T) {
		var clockMu sync.Mutex
		now := t0
		advance := func(delta time.Duration) {
			clockMu.Lock()
			now = now.Add(delta)
			clockMu.Unlock()
		}
		readClock := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}

		f := newFakeHub()
		f.devices = []Device{
			{ID: "aged-out", Name: "Aged Out", Online: true, Reachable: true},
			{ID: "arrived-during-rpc", Name: "Arrived During RPC", Online: true, Reachable: true},
		}
		f.devicesFull = true
		f.onDevicesList = func() {
			advance(40 * time.Second)
		}

		d := &resourceDevs{
			fleetDevs: newFleetDevs(),
			machines: map[string]*proto.Machine{
				"aged-out":           {OS: "linux", Arch: "amd64"},
				"arrived-during-rpc": {OS: "linux", Arch: "amd64"},
			},
			samples: map[string]*proto.MetricSample{
				"aged-out": {
					T:         t0.Add(-5 * time.Second).Unix(),
					CPU:       &cpu,
					Load:      &load,
					MemTotal:  100,
					MemUsed:   40,
					DiskTotal: 200,
					DiskUsed:  50,
				},
				"arrived-during-rpc": {
					T:         t0.Add(35 * time.Second).Unix(),
					CPU:       &cpu,
					Load:      &load,
					MemTotal:  100,
					MemUsed:   40,
					DiskTotal: 200,
					DiskUsed:  50,
				},
			},
			versions: map[string]*bool{},
		}
		d.add("aged-out", "Aged Out")
		d.add("arrived-during-rpc", "Arrived During RPC")

		s := newTestService(t, f)
		s.devs = d
		s.machineNow = readClock

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		req.Header.Set(RoleHeader, roleExtension)
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Machines []map[string]any `json:"machines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		byID := map[string]map[string]any{}
		for _, m := range out.Machines {
			byID[m["id"].(string)] = m
		}

		aged := byID["aged-out"]
		if aged["sample_age_seconds"] != float64(45) {
			t.Fatalf("aged-out sample_age_seconds = %v, want 45", aged["sample_age_seconds"])
		}
		for _, field := range []string{"cpu_busy_percent", "load_1m", "memory_available_bytes", "memory_total_bytes", "disk_free_bytes", "disk_total_bytes"} {
			if aged[field] != nil {
				t.Fatalf("aged-out.%s = %v, want null after slow RPC", field, aged[field])
			}
		}

		fresh := byID["arrived-during-rpc"]
		if fresh["sample_age_seconds"] != float64(5) || fresh["cpu_busy_percent"] != cpu || fresh["load_1m"] != load ||
			fresh["memory_available_bytes"] != float64(60) || fresh["disk_free_bytes"] != float64(150) {
			t.Fatalf("arrived-during-rpc = %v, want fresh sample with age 5", fresh)
		}
	})

	t.Run("clock advances before later machine snapshot", func(t *testing.T) {
		var clockMu sync.Mutex
		now := t0
		advance := func(delta time.Duration) {
			clockMu.Lock()
			now = now.Add(delta)
			clockMu.Unlock()
		}
		readClock := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}

		f := newFakeHub()
		f.devices = []Device{
			{ID: "first", Name: "First", Online: true, Reachable: true},
			{ID: "second-fresh", Name: "Second Fresh", Online: true, Reachable: true},
			{ID: "third-stale", Name: "Third Stale", Online: true, Reachable: true},
		}
		f.devicesFull = true

		base := &resourceDevs{
			fleetDevs: newFleetDevs(),
			machines: map[string]*proto.Machine{
				"first":        {OS: "linux", Arch: "amd64"},
				"second-fresh": {OS: "linux", Arch: "amd64"},
				"third-stale":  {OS: "linux", Arch: "amd64"},
			},
			samples: map[string]*proto.MetricSample{
				"first": {
					T:         t0.Add(-5 * time.Second).Unix(),
					CPU:       &cpu,
					Load:      &load,
					MemTotal:  100,
					MemUsed:   40,
					DiskTotal: 200,
					DiskUsed:  50,
				},
				"second-fresh": {
					T:         t0.Add(35 * time.Second).Unix(),
					CPU:       &cpu,
					Load:      &load,
					MemTotal:  100,
					MemUsed:   40,
					DiskTotal: 200,
					DiskUsed:  50,
				},
				"third-stale": {
					T:         t0.Add(-5 * time.Second).Unix(),
					CPU:       &cpu,
					Load:      &load,
					MemTotal:  100,
					MemUsed:   40,
					DiskTotal: 200,
					DiskUsed:  50,
				},
			},
			versions: map[string]*bool{},
		}
		for _, id := range []string{"first", "second-fresh", "third-stale"} {
			base.add(id, id)
		}
		d := &hookedResourceDevs{
			resourceDevs: base,
			onMachineResources: func(id string) {
				if id == "second-fresh" {
					advance(40 * time.Second)
				}
			},
		}

		s := newTestService(t, f)
		s.devs = d
		s.machineNow = readClock

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		req.Header.Set(RoleHeader, roleExtension)
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Machines []map[string]any `json:"machines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		byID := map[string]map[string]any{}
		for _, m := range out.Machines {
			byID[m["id"].(string)] = m
		}

		if byID["first"]["sample_age_seconds"] != float64(5) || byID["first"]["cpu_busy_percent"] != cpu {
			t.Fatalf("first = %v, want fresh sample age 5", byID["first"])
		}
		if byID["second-fresh"]["sample_age_seconds"] != float64(5) || byID["second-fresh"]["cpu_busy_percent"] != cpu ||
			byID["second-fresh"]["memory_available_bytes"] != float64(60) || byID["second-fresh"]["disk_free_bytes"] != float64(150) {
			t.Fatalf("second-fresh = %v, want fresh sample age 5 (not sample_clock_in_future)", byID["second-fresh"])
		}
		if byID["third-stale"]["sample_age_seconds"] != float64(45) || byID["third-stale"]["cpu_busy_percent"] != nil ||
			byID["third-stale"]["memory_available_bytes"] != nil || byID["third-stale"]["disk_free_bytes"] != nil {
			t.Fatalf("third-stale = %v, want stale null metrics at age 45", byID["third-stale"])
		}
	})
}
