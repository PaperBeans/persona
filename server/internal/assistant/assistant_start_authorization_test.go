package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The node discovers MCP tools while assistant.send is still starting its process.
// Run that callback outside fakeHub's mutex, since MCP calls read the hub too.
type startingPrivacyHub struct {
	Hub
	onStart func(DeviceCall) error
}

func (h *startingPrivacyHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceAssistantSend {
		if err := h.onStart(p); err != nil {
			return nil, err
		}
	}
	return h.Hub.DeviceCall(ctx, p)
}

func assertStartingAssistantTools(t *testing.T, s *Service, machine, session string, allowed bool) {
	t.Helper()
	names := privacyTools(t, s, machine, session, true)
	for _, name := range []string{"kb_search", "lark_chats", "computer_run"} {
		if slices.Contains(names, name) != allowed {
			t.Fatalf("%s/%s: %s allowed=%v, tools=%v", machine, session, name, allowed, names)
		}
	}
	if got := privacyCall(t, s, machine, session, "lark_chats", true); got.IsError == allowed {
		t.Fatalf("%s/%s: allowed=%v, call=%+v", machine, session, allowed, got)
	}
}

func assertAssistantDiscoveryOnly(t *testing.T, s *Service, machine string) {
	t.Helper()
	if !slices.Contains(privacyTools(t, s, machine, "", true), "kb_search") {
		t.Fatal("runtime host cannot rediscover assistant tools")
	}
	if got := privacyCall(t, s, machine, "", "lark_chats", true); !got.IsError {
		t.Fatalf("sessionless discovery granted execution: %+v", got)
	}
}

func TestAssistantStartAuthorizesRuntimeDiscovery(t *testing.T) {
	for _, origin := range []string{OriginUser, OriginInbox} {
		t.Run(origin, func(t *testing.T) {
			s, devices, f, extensions := privacyService(t)
			store := &memStore{}
			s.store = func() Store { return store }
			f.setSessions("other")
			assertStartingAssistantTools(t, s, "other", "", false)
			started := 0
			setHub(s, &startingPrivacyHub{Hub: f, onStart: func(p DeviceCall) error {
				started++
				if p.Device != "other" {
					t.Fatalf("started on %s", p.Device)
				}
				// Claude can discover tools without a session ID; Codex initially uses a
				// temporary process ID. Neither has a persisted origin at this point.
				assertStartingAssistantTools(t, s, "other", "", true)
				assertStartingAssistantTools(t, s, "other", "proc-starting", true)
				assertStartingAssistantTools(t, s, "work", "proc-starting", false)
				return nil
			}})
			var session proto.Session
			var err error
			if origin == OriginInbox {
				session.MachineID, session.SessionID, err = s.startInboxAgent(context.Background(),
					InboxAgentConfig{Enabled: true, Machine: "other"}, &InboxItem{ID: "event", Title: "Message"}, "Handle this message")
			} else {
				session, err = s.Start(context.Background(), StartParams{MachineID: "other", Text: "Private request", Untitled: true})
			}
			if err != nil || started != 1 || session.MachineID != "other" || session.SessionID == "" {
				t.Fatalf("start=%+v, callbacks=%d, error=%v", session, started, err)
			}
			// The temporary machine grant is gone. Only the manager-recorded session
			// can call tools. Runtime hosts may rediscover tools without a session ID.
			assertStartingAssistantTools(t, s, "other", session.SessionID, true)
			assertAssistantDiscoveryOnly(t, s, "other")
			assertStartingAssistantTools(t, s, "other", "proc-starting", false)
			assertStartingAssistantTools(t, s, "work", session.SessionID, false)
			s.origins.mu.Lock()
			var o convOrigin
			if recorded := s.origins.m[session.SessionID]; recorded != nil {
				o = *recorded
			}
			s.origins.mu.Unlock()
			if o.By != originByStart || o.Origin != origin || o.MachineID != "other" {
				t.Fatalf("recorded origin=%+v", o)
			}
			// A manager restart reloads the recorded grant, without a new start window.
			restarted := New(Options{Hub: f, Devices: devices, Store: func() Store { return store }, Logf: t.Logf})
			restarted.SetExtensions(extensions)
			assertStartingAssistantTools(t, restarted, "other", session.SessionID, true)
			assertAssistantDiscoveryOnly(t, restarted, "other")
			assertStartingAssistantTools(t, restarted, "other", "proc-starting", false)
		})
	}
}

func TestAssistantFailedStartRevokesRuntimeDiscovery(t *testing.T) {
	s, _, f, _ := privacyService(t)
	f.setSessions("other")
	refused := errors.New("runtime refused to start")
	started := 0
	setHub(s, &startingPrivacyHub{Hub: f, onStart: func(p DeviceCall) error {
		started++
		assertStartingAssistantTools(t, s, "other", "", true)
		return refused
	}})
	if _, err := s.Start(context.Background(), StartParams{MachineID: "other", Text: "Private request", Untitled: true}); !errors.Is(err, refused) {
		t.Fatalf("start error=%v", err)
	}
	if started != 1 {
		t.Fatalf("start callbacks=%d", started)
	}
	assertStartingAssistantTools(t, s, "other", "", false)
	assertStartingAssistantTools(t, s, "other", "proc-starting", false)
	s.origins.mu.Lock()
	defer s.origins.mu.Unlock()
	if len(s.origins.m) != 0 {
		t.Fatalf("failed start persisted origins=%v", s.origins.m)
	}
}
