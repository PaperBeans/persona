package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// Advance the persisted attempt timestamp rather than sleeping through a retry interval.
func ageHealthTellAttempt(st *healthTellMemStore, id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.rows {
		if st.rows[i].ID == id {
			st.rows[i].UpdatedAt = time.Now().Add(-healthTellRetryInterval - time.Second)
		}
	}
}

func TestHealthTellContinuationKeepsItsTarget(t *testing.T) {
	for _, scenario := range []string{"pointer-changed", "lookup-misses", "restart-after-send"} {
		t.Run(scenario, func(t *testing.T) {
			s, f, st := healthTellService(t)
			tellNodeListsStarts(f)
			a := tellHealth(t, s, "pinned-first", "first")
			s.healthTellRound(t.Context(), st)
			original := st.row(t, a.ID).SessionID
			b := tellHealth(t, s, "pinned-second", "second")
			typed, calls := 0, 0
			seen := false
			f.answerNode(assistantwire.DeviceSessionSendOnce, func(_ string, raw json.RawMessage) (any, error) {
				var p struct {
					Session string `json:"session_id"`
					Key     string `json:"client_msg_id"`
				}
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				kept := st.row(t, b.ID)
				if kept.SessionID != original || kept.MachineID != "server" || p.Session != original || p.Key != "health-tell:"+b.ID {
					t.Fatalf("continuation was not pinned before dispatch: row=%+v params=%+v", kept, p)
				}
				calls++
				// The hub's once ledger can have delivery recorded even when the manager loses the reply.
				if !seen {
					seen = true
					typed++
					if scenario != "restart-after-send" {
						return nil, errors.New("send in doubt")
					}
				}
				return map[string]any{}, nil
			})
			if scenario == "restart-after-send" {
				st.resultFailures = 1
			}
			s.healthTellRound(t.Context(), st)
			pending := st.row(t, b.ID)
			if pending.Status != "received" || pending.SessionID != original || pending.MachineID != "server" || typed != 1 {
				t.Fatalf("lost pending target: %+v typed=%d", pending, typed)
			}
			if scenario == "restart-after-send" && pending.Attempts != 0 {
				t.Fatalf("crash window counted a result: %+v", pending)
			}
			if scenario != "lookup-misses" {
				day := pending.CreatedAt.In(s.location(t.Context())).Format("2006-01-02")
				f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: "replacement-chat", Assistant: true, Space: SpaceHealth, Cwd: "/home/me/" + s.healthRel()})
				s.keepTrainingChat(t.Context(), day, SessionRef{MachineID: "server", SessionID: "replacement-chat"}, true)
			}
			if scenario == "lookup-misses" {
				miss := true
				f.answerNode(DeviceSessionGet, func(_ string, raw json.RawMessage) (any, error) {
					if miss {
						miss = false
						return nil, errors.New("synthetic session lookup failure")
					}
					var p struct {
						ID string `json:"session_id"`
					}
					if err := json.Unmarshal(raw, &p); err != nil {
						return nil, err
					}
					for _, row := range f.sessions["server"] {
						if row.SessionID == p.ID {
							return map[string]any{"session": row, "ok": true}, nil
						}
					}
					return map[string]any{"ok": false}, nil
				})
			}
			if scenario == "restart-after-send" {
				s = newServiceOn(t, f, st)
				setSettings(t, s, func(v *Settings) { v.MachineID = "server" })
			}
			for range 3 {
				ageHealthTellAttempt(st, b.ID)
				s.healthTellRound(t.Context(), st)
				if len(f.sends) != 1 || st.row(t, b.ID).NewSessionID != "" {
					t.Fatalf("uncertain continuation started another chat: %+v sends=%v", st.row(t, b.ID), f.sends)
				}
				if st.row(t, b.ID).Status == "delivered" {
					break
				}
			}
			got := st.row(t, b.ID)
			if got.Status != "delivered" || got.SessionID != original || got.MachineID != "server" || got.Text != "" || typed != 1 || calls < 2 {
				t.Fatalf("recovery: %+v typed=%d calls=%d", got, typed, calls)
			}
		})
	}
}

func TestHealthTellContinuationWaitsForDurableTarget(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "target-first", "first")
	s.healthTellRound(t.Context(), st)
	b := tellHealth(t, s, "target-second", "second")
	st.targetFailure = true
	s.healthTellRound(t.Context(), st)
	if got := st.row(t, b.ID); got.Attempts != 0 || got.SessionID != "" || len(f.sentOnce) != 0 || len(f.sends) != 1 {
		t.Fatalf("sent without target write: %+v", got)
	}
	st.targetFailure = false
	s.healthTellRound(t.Context(), st)
	if got := st.row(t, b.ID); got.Status != "delivered" || got.SessionID != st.row(t, a.ID).SessionID {
		t.Fatalf("target recovery: %+v", got)
	}
}

func TestHealthTellDeletedTrainingChatIsReplaced(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			s, f, st := healthTellService(t)
			tellNodeListsStarts(f)
			a := tellHealth(t, s, "deleted-first", "first")
			s.healthTellRound(t.Context(), st)
			original := st.row(t, a.ID).SessionID
			b := tellHealth(t, s, "deleted-second", "second")
			if pinned {
				f.failSendOnce = map[string]error{"server/" + original: errors.New("send in doubt")}
				s.healthTellRound(t.Context(), st)
				ageHealthTellAttempt(st, b.ID)
			}
			if w := serve(s, "DELETE", "/api/v1/assistant/conversations/"+original, `{"machine_id":"server"}`); w.Code != 200 {
				t.Fatalf("delete: %d %s", w.Code, w.Body)
			}
			s.healthTellRound(t.Context(), st)
			got := st.row(t, b.ID)
			if got.Status != "delivered" || got.SessionID == original || got.NewSessionID == "" || len(f.sends) != 2 || len(f.pushes) != 0 {
				t.Fatalf("deleted target: %+v starts=%d pushes=%v", got, len(f.sends), f.pushes)
			}
			ref, ok := s.trainingChat(t.Context(), got.CreatedAt.In(s.location(t.Context())).Format("2006-01-02"))
			if !ok || ref.SessionID != got.SessionID {
				t.Fatalf("pointer not replaced: %+v %v", ref, ok)
			}
		})
	}
}

func TestHealthTellExpiredBurstPushesOncePerRound(t *testing.T) {
	s, f, st := healthTellService(t)
	for i := range 8 {
		r := tellHealth(t, s, fmt.Sprintf("burst-client-%d", i), "private sentence")
		st.mu.Lock()
		for j := range st.rows {
			if st.rows[j].ID == r.ID {
				st.rows[j].CreatedAt = time.Now().Add(-25 * time.Hour)
			}
		}
		st.mu.Unlock()
	}
	f.devices[0].Online = false
	s.healthTellRound(t.Context(), st)
	s.healthTellRound(t.Context(), st)
	for _, r := range st.rows {
		if r.Status != "failed" || r.Text != "private sentence" {
			t.Fatalf("expired row: %+v", r)
		}
	}
	if len(f.pushes) != 1 {
		t.Fatalf("expired burst pushed %d times", len(f.pushes))
	}
}

func TestHealthTellNewReceiptsDoNotAccelerateRetry(t *testing.T) {
	s, f, st := healthTellService(t)
	tellNodeListsStarts(f)
	a := tellHealth(t, s, "cooldown-first", "first")
	s.healthTellRound(t.Context(), st)
	b := tellHealth(t, s, "cooldown-blocked", "second")
	attempts := 0
	f.answerNode(assistantwire.DeviceSessionSendOnce, func(_ string, _ json.RawMessage) (any, error) { attempts++; return nil, errors.New("send in doubt") })
	s.healthTellRound(t.Context(), st)
	for i := range 5 {
		tellHealth(t, s, fmt.Sprintf("cooldown-new-%d", i), "later")
		s.healthTellRound(t.Context(), st)
	}
	if got := st.row(t, b.ID); got.Attempts != 1 || attempts != 1 || got.SessionID != st.row(t, a.ID).SessionID || len(f.sends) != 1 {
		t.Fatalf("accelerated retry: %+v attempts=%d starts=%d", got, attempts, len(f.sends))
	}
	for _, r := range st.rows[2:] {
		if r.Status != "received" || r.Attempts != 0 {
			t.Fatalf("later sentence overtook blocked one: %+v", r)
		}
	}
	ageHealthTellAttempt(st, b.ID)
	s.healthTellRound(t.Context(), st)
	if got := st.row(t, b.ID); got.Attempts != 2 || attempts != 2 {
		t.Fatalf("retry did not resume after interval: %+v attempts=%d", got, attempts)
	}
}
