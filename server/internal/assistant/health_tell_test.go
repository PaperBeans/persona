package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

type healthTellMemStore struct {
	memStore
	mu             sync.Mutex
	rows           []db.HealthTell
	fail           bool
	resultFailures int
	targetFailure  bool
}

func (st *healthTellMemStore) InsertHealthTell(_ context.Context, r db.HealthTell) (db.HealthTell, bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.fail {
		return r, false, errors.New("synthetic storage failure")
	}
	for _, old := range st.rows {
		if old.ClientID == r.ClientID {
			return old, false, nil
		}
	}
	r.CreatedAt, r.UpdatedAt, r.Status, r.Source = time.Now(), time.Now(), assistantwire.HealthTellReceived, "shortcut"
	st.rows = append(st.rows, r)
	return r, true, nil
}
func (st *healthTellMemStore) HealthTellsPending(_ context.Context, limit int) ([]db.HealthTell, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []db.HealthTell
	for _, r := range st.rows {
		if r.Status == assistantwire.HealthTellReceived {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b db.HealthTell) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out[:min(len(out), limit)], nil
}
func (st *healthTellMemStore) ReserveHealthTellTarget(_ context.Context, id, machine, session string) (db.HealthTell, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.targetFailure {
		return db.HealthTell{}, errors.New("synthetic target write failure")
	}
	for i := range st.rows {
		r := &st.rows[i]
		if r.ID == id {
			if r.MachineID == "" && r.SessionID == "" && r.NewSessionID == "" && r.Status == assistantwire.HealthTellReceived {
				r.MachineID, r.SessionID = machine, session
			}
			return *r, nil
		}
	}
	return db.HealthTell{}, errors.New("missing row")
}
func (st *healthTellMemStore) ReserveHealthTellSession(_ context.Context, id, sid string) (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.rows {
		r := &st.rows[i]
		if r.ID == id {
			if r.NewSessionID == "" && r.Status == assistantwire.HealthTellReceived {
				r.NewSessionID = sid
				r.MachineID, r.SessionID, r.UpdatedAt = "", "", time.Now()
			}
			return r.NewSessionID, nil
		}
	}
	return "", errors.New("missing row")
}
func (st *healthTellMemStore) SetHealthTellResult(_ context.Context, id, status, machine, session, errText string, tried bool) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.resultFailures > 0 {
		st.resultFailures--
		return false, errors.New("synthetic result write failure")
	}
	for i := range st.rows {
		r := &st.rows[i]
		if r.ID == id && r.Status == assistantwire.HealthTellReceived {
			r.Status, r.Error, r.UpdatedAt = status, errText, time.Now()
			if tried {
				r.Attempts++
			}
			if status == assistantwire.HealthTellDelivered {
				r.Text = ""
				r.MachineID, r.SessionID = machine, session
			}
			return true, nil
		}
	}
	return false, nil
}
func (st *healthTellMemStore) row(t *testing.T, id string) db.HealthTell {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("missing %s", id)
	return db.HealthTell{}
}
func healthTellService(t *testing.T) (*Service, *fakeHub, *healthTellMemStore) {
	t.Helper()
	f := healthHub()
	f.devices[0].Online = true
	f.answerNode(DeviceAssistantPrepare, func(_ string, raw json.RawMessage) (any, error) {
		var p struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return map[string]any{"cwd": "/home/me/" + p.Dir}, nil
	})
	st := &healthTellMemStore{}
	s := newServiceOn(t, f, st)
	setSettings(t, s, func(v *Settings) { v.MachineID = "server" })
	return s, f, st
}
func tellHealth(t *testing.T, s *Service, id, text string) assistantwire.HealthTellReceipt {
	t.Helper()
	b, _ := json.Marshal(assistantwire.HealthTell{ClientID: id, Text: text})
	w := intakeHTTP(s, "POST", "/api/v1/assistant/health/tell", string(b), "answer")
	if w.Code != 202 && w.Code != 200 {
		t.Fatalf("receipt: %d %s", w.Code, w.Body)
	}
	var r assistantwire.HealthTellReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestHealthTellReceiptBeforeModelWork(t *testing.T) {
	s, f, st := healthTellService(t)
	gate := make(chan struct{})
	defer close(gate)
	f.beforeAssistantSend = func() { <-gate }
	f.beforeSend = func() { <-gate }
	start := time.Now()
	r := tellHealth(t, s, "receipt-client", "  private\u0000 note\n\tkept  ")
	if time.Since(start) >= time.Second || r.Status != "received" {
		t.Fatalf("slow or incorrect receipt: %+v", r)
	}
	if got := st.row(t, r.ID); got.Text != "private note\n\tkept" || got.Source != "shortcut" {
		t.Fatalf("stored: %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method == DeviceAssistantSend || c.Method == assistantwire.DeviceSessionSendOnce {
			t.Fatalf("node work before receipt: %+v", c)
		}
	}
}
func TestHealthTellRoles(t *testing.T) {
	for _, tc := range []struct {
		role, body string
		code       int
	}{
		{"full", `{"client_id":"full-client","text":"note"}`, 202},
		{"answer", `{"client_id":"watch-client","text":"note"}`, 202},
		{"extension", `{"client_id":"extension-client","text":"note"}`, 403},
		{"scoped", `{"client_id":"scoped-client","text":"note"}`, 403},
		{"answer", `{"client_id":"empty-client","text":" \u0000 "}`, 400},
		{"answer", `{"client_id":"bad","text":"note"}`, 400},
		{"answer", `{"client_id":"large-client","text":"` + strings.Repeat("a", 8193) + `"}`, 400},
		{"answer", `{"client_id":"body-client","text":"` + strings.Repeat("a", 17000) + `"}`, 400},
		{"answer", `{`, 400},
	} {
		t.Run(tc.role+"/"+http.StatusText(tc.code), func(t *testing.T) {
			s, _, _ := healthTellService(t)
			w := intakeHTTP(s, "POST", "/api/v1/assistant/health/tell", tc.body, tc.role)
			if w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	s, _, st := healthTellService(t)
	st.fail = true
	if w := intakeHTTP(s, "POST", "/api/v1/assistant/health/tell", `{"client_id":"fail-client","text":"note"}`, "answer"); w.Code != 500 {
		t.Fatalf("write failure: %d", w.Code)
	}
	s2 := newServiceOn(t, healthHub(), &memStore{})
	if w := intakeHTTP(s2, "POST", "/api/v1/assistant/health/tell", `{"client_id":"no-store","text":"note"}`, "answer"); w.Code != 503 {
		t.Fatalf("no store: %d", w.Code)
	}
}
func TestHealthTellRetryReceipt(t *testing.T) {
	s, _, st := healthTellService(t)
	a := tellHealth(t, s, "retry-client", "first")
	b := tellHealth(t, s, "retry-client", "different")
	if a.ID != b.ID || a.Duplicate || !b.Duplicate || len(st.rows) != 1 || st.row(t, a.ID).Text != "first" {
		t.Fatalf("receipts: %+v %+v", a, b)
	}
}
