package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
)

// memManagerStore is an in-memory managerStore for roster serialization tests that do not require Postgres.
type memManagerStore struct {
	mu       sync.Mutex
	byID     map[string]db.BusIdentity
	byHash   map[string]string
	settings map[string]json.RawMessage
}

func newMemManagerStore() *memManagerStore {
	return &memManagerStore{
		byID:     map[string]db.BusIdentity{},
		byHash:   map[string]string{},
		settings: map[string]json.RawMessage{},
	}
}

func (m *memManagerStore) SaveBusIdentity(_ context.Context, b db.BusIdentity, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, id := range m.byHash {
		if id == b.ID {
			delete(m.byHash, h)
		}
	}
	if prev, ok := m.byID[b.ID]; ok {
		b.Host, b.MachineUID, b.OS, b.Arch, b.Deploy, b.Version, b.LastSeen = prev.Host, prev.MachineUID, prev.OS, prev.Arch, prev.Deploy, prev.Version, prev.LastSeen
	}
	b.CreatedAt = time.Now().UTC()
	m.byID[b.ID] = b
	m.byHash[tokenHash] = b.ID
	return nil
}

func (m *memManagerStore) BusIdentityByHash(_ context.Context, hash string) (*db.BusIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byHash[hash]
	if !ok {
		return nil, nil
	}
	b := m.byID[id]
	return &b, nil
}

func (m *memManagerStore) BusIdentityByID(_ context.Context, id string) (*db.BusIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	return &b, nil
}

func (m *memManagerStore) BusIdentities(_ context.Context) ([]db.BusIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]db.BusIdentity, 0, len(m.byID))
	for _, b := range m.byID {
		out = append(out, b)
	}
	return out, nil
}

func (m *memManagerStore) DeleteBusIdentity(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return false, nil
	}
	delete(m.byID, id)
	for h, oid := range m.byHash {
		if oid == id {
			delete(m.byHash, h)
		}
	}
	return true, nil
}

func (m *memManagerStore) SeeBusIdentity(_ context.Context, id string, where *db.BusIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	b.LastSeen = &now
	if where != nil {
		b.Host, b.MachineUID, b.OS, b.Arch, b.Deploy, b.Version = where.Host, where.MachineUID, where.OS, where.Arch, where.Deploy, where.Version
	}
	m.byID[id] = b
	return nil
}

func (m *memManagerStore) RenameBusIdentity(_ context.Context, id, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return false, nil
	}
	b.Name = name
	m.byID[id] = b
	return true, nil
}

func (m *memManagerStore) AddManagerInstallCode(context.Context, string, db.ManagerInstallCode) error {
	return nil
}
func (m *memManagerStore) RedeemManagerInstallCode(context.Context, string, string) (*db.ManagerInstallCode, string, error) {
	return nil, "", nil
}
func (m *memManagerStore) ExpireManagerInstallCode(context.Context, string) (bool, error) {
	return false, nil
}
func (m *memManagerStore) ManagerInstallCodeByHash(context.Context, string) (*db.ManagerInstallCode, error) {
	return nil, nil
}
func (m *memManagerStore) PendingManagerInstalls(context.Context) ([]db.ManagerInstallCode, error) {
	return nil, nil
}
func (m *memManagerStore) Setting(_ context.Context, key string, out any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.settings[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}
func (m *memManagerStore) PutSetting(_ context.Context, key string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v == nil {
		delete(m.settings, key)
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.settings[key] = b
	return nil
}

// manager.hello and GET /api/v1/managers additively carry machine_uid and pending_restart (omitempty), keeping older
// managers compatible, pushing {"type":"managers"} on live manager.status changes, preserving since on same-connection
// re-handshakes, and retaining machine_uid (while clearing pending_restart) after disconnect.
func TestManagersRosterMachineUIDAndPendingRestart(t *testing.T) {
	h := newTestHub(t)
	h.bus.idents = newMemManagerStore()
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		for _, c := range h.stopBus() {
			_ = c.ws.CloseNow()
		}
	})

	app := &client{device: "test-device", out: make(chan []byte, 64)}
	h.mu.Lock()
	h.clients[app] = struct{}{}
	h.mu.Unlock()
	h.onClientMessage(context.Background(), app, []byte(`{"type":"client_hello","features":["managers"]}`))
	<-app.out // client_hello_ack

	expectPush := func(want bool, what string) {
		t.Helper()
		if want {
			select {
			case raw := <-app.out:
				if string(raw) != `{"type":"managers"}` {
					t.Fatalf("%s: got %s", what, raw)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s: timed out waiting for managers push", what)
			}
			return
		}
		select {
		case raw := <-app.out:
			t.Fatalf("%s: unexpected push %s", what, raw)
		case <-time.After(100 * time.Millisecond):
		}
	}

	rawManagers := func() map[string]map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/managers", nil)
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/managers: %d %s", resp.StatusCode, body)
		}
		var out struct {
			Managers []map[string]any `json:"managers"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		byID := map[string]map[string]any{}
		for _, m := range out.Managers {
			byID[m["id"].(string)] = m
		}
		return byID
	}

	// 1. Older manager without machine_uid or pending_restart: both keys omitted in JSON.
	oldTok := busToken(t, h, "m-old111", "Old")
	oldConn := dialRawBus(t, srv, oldTok)
	if err := oldConn.call(assistantwire.MethodHello, map[string]any{
		"id": "m-old111", "host": "legacy-box", "os": "linux", "arch": "amd64", "deploy": "service", "version": "1.0.0",
		"capabilities": []string{assistantwire.CapabilityConfigOwn},
	}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "old manager connected")
	oldEntry := rawManagers()["m-old111"]
	if _, has := oldEntry["machine_uid"]; has {
		t.Fatalf("older manager should omit machine_uid: %v", oldEntry)
	}
	if _, has := oldEntry["pending_restart"]; has {
		t.Fatalf("older manager should omit pending_restart: %v", oldEntry)
	}

	// 2. Newer manager with machine_uid (pending_restart false/omitted).
	const uid = "12345678-1234-4123-8123-123456789abc"
	newTok := busToken(t, h, "m-new222", "Work")
	newConn := dialRawBus(t, srv, newTok)
	if err := newConn.call(assistantwire.MethodHello, map[string]any{
		"id": "m-new222", "host": "work-host", "machine_uid": uid, "os": "linux", "arch": "amd64",
		"deploy": "service", "version": "1.1.0", "capabilities": []string{assistantwire.CapabilityConfigOwn},
	}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "new manager connected")
	newEntry := rawManagers()["m-new222"]
	if newEntry["machine_uid"] != uid {
		t.Fatalf("machine_uid = %v, want %q", newEntry["machine_uid"], uid)
	}
	if _, has := newEntry["pending_restart"]; has {
		t.Fatalf("idle manager should omit pending_restart: %v", newEntry)
	}
	firstSince, _ := newEntry["since"].(string)
	if firstSince == "" {
		t.Fatalf("missing since: %v", newEntry)
	}

	// 3. Live manager.status {pending_restart: true} updates roster and pushes immediately.
	if err := newConn.peer.Notify(assistantwire.EventManagerStatus, assistantwire.ManagerStatus{PendingRestart: true}); err != nil {
		t.Fatal(err)
	}
	if err := newConn.call("ping", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "pending_restart true")
	if got := rawManagers()["m-new222"]; got["pending_restart"] != true {
		t.Fatalf("pending_restart after status true: %v", got)
	}

	// Repeated identical status does not push again.
	if err := newConn.peer.Notify(assistantwire.EventManagerStatus, assistantwire.ManagerStatus{PendingRestart: true}); err != nil {
		t.Fatal(err)
	}
	if err := newConn.call("ping", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(false, "duplicate pending_restart true")

	// 4. Live manager.status {pending_restart: false} clears and omits pending_restart.
	if err := newConn.peer.Notify(assistantwire.EventManagerStatus, assistantwire.ManagerStatus{PendingRestart: false}); err != nil {
		t.Fatal(err)
	}
	if err := newConn.call("ping", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "pending_restart false")
	if got := rawManagers()["m-new222"]; got["pending_restart"] != nil {
		t.Fatalf("pending_restart should be omitted after clearing: %v", got)
	}

	// 5. Second manager.hello on the same connection updates pending_restart and preserves since.
	if err := newConn.call(assistantwire.MethodHello, map[string]any{
		"id": "m-new222", "host": "work-host", "machine_uid": uid, "os": "linux", "arch": "amd64",
		"deploy": "service", "version": "1.1.0", "pending_restart": true,
		"capabilities": []string{assistantwire.CapabilityConfigOwn},
	}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "re-handshake pending_restart true")
	reEntry := rawManagers()["m-new222"]
	if reEntry["pending_restart"] != true || reEntry["since"] != firstSince {
		t.Fatalf("after re-handshake: %v (want pending_restart=true, since=%q)", reEntry, firstSince)
	}

	// 6. Disconnect clears pending_restart while keeping machine_uid for offline host correlation.
	_ = newConn.ws.Close(websocket.StatusNormalClosure, "restarting")
	<-newConn.ended
	expectPush(true, "disconnected")
	offEntry := rawManagers()["m-new222"]
	if offEntry["online"] != false || offEntry["machine_uid"] != uid {
		t.Fatalf("offline entry: %v", offEntry)
	}
	if _, has := offEntry["pending_restart"]; has {
		t.Fatalf("offline manager should omit pending_restart: %v", offEntry)
	}

	// 7. Reconnecting with pending_restart: true in initial manager.hello exposes it immediately.
	reConn := dialRawBus(t, srv, newTok)
	if err := reConn.call(assistantwire.MethodHello, map[string]any{
		"id": "m-new222", "host": "work-host", "machine_uid": uid, "os": "linux", "arch": "amd64",
		"deploy": "service", "version": "1.1.0", "pending_restart": true,
		"capabilities": []string{assistantwire.CapabilityConfigOwn},
	}, nil); err != nil {
		t.Fatal(err)
	}
	expectPush(true, "reconnected with pending_restart")
	if got := rawManagers()["m-new222"]; got["online"] != true || got["pending_restart"] != true || got["machine_uid"] != uid {
		t.Fatalf("reconnected entry: %v", got)
	}

	_ = bus.RoleManager
}
