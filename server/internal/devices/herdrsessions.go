package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Blocked named herdr sessions (#14898, proto/herdrsessions.go): per computer, named herdr sessions
// (`herdr --session <name>`) kept out of Persona. Stored in settings beside blocked Claude config directories and
// Agent folder rules (`herdr_sessions_blocked/<machine id>`), sent to the node in `node.configure`
// (`herdr_sessions_blocked`), and queried live via `herdr.sessions.list`.

func blockedHerdrSessionsKey(machine string) string { return "herdr_sessions_blocked/" + machine }

const blockedHerdrSessionsRetry = 5 * time.Second

type herdrSessionsCache struct {
	mu       sync.Mutex
	loaded   bool
	blocked  []string
	err      error
	failedAt time.Time
}

func (m *Machine) blockedHerdrSessions(ctx context.Context) ([]string, error) {
	c := &m.herdrSessionsBlocked
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := m.loadBlockedHerdrSessions(ctx); err != nil {
		return nil, err
	}
	return slices.Clone(c.blocked), nil
}

func (m *Machine) loadBlockedHerdrSessions(ctx context.Context) error {
	c := &m.herdrSessionsBlocked
	if c.loaded {
		return nil
	}
	if c.err != nil && time.Since(c.failedAt) < blockedHerdrSessionsRetry {
		return c.err
	}
	var blocked []string
	if st := m.d.opts.Store; st != nil {
		if _, err := st.Setting(ctx, blockedHerdrSessionsKey(m.cfg.ID), &blocked); err != nil {
			c.err, c.failedAt = fmt.Errorf("read blocked herdr sessions: %w", err), time.Now()
			return c.err
		}
	}
	c.loaded, c.blocked, c.err = true, proto.CleanBlockedHerdrSessions(blocked), nil
	return nil
}

// blockedHerdrSessionsParam is node.configure's herdr_sessions_blocked: [] when none are blocked (clears the node's
// list), nil (null on the wire) when the store read failed (the node keeps its persisted list).
func (m *Machine) blockedHerdrSessionsParam(ctx context.Context) []string {
	blocked, err := m.blockedHerdrSessions(ctx)
	if err != nil {
		m.d.logf("[%s] blocked herdr sessions for the node: %v (the node keeps its blocked sessions)", m.cfg.ID, err)
		return nil
	}
	if blocked == nil {
		return []string{}
	}
	return blocked
}

func (m *Machine) editBlockedHerdrSessions(ctx context.Context, edit func([]string) ([]string, error)) ([]string, error) {
	st := m.d.opts.Store
	if st == nil {
		return nil, errors.New("no database")
	}
	c := &m.herdrSessionsBlocked
	c.mu.Lock()
	if err := m.loadBlockedHerdrSessions(ctx); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	next, err := edit(slices.Clone(c.blocked))
	if err == nil {
		next, err = proto.ValidateBlockedHerdrSessions(next)
		if err != nil {
			err = fmt.Errorf("%w: %v", errBadPattern, err)
		}
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if slices.Equal(next, c.blocked) {
		c.mu.Unlock()
		return next, nil
	}
	if err := st.PutSetting(ctx, blockedHerdrSessionsKey(m.cfg.ID), next); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.blocked = slices.Clone(next)
	c.mu.Unlock()
	m.d.logf("[%s] blocked herdr sessions: %v", m.cfg.ID, next)
	go m.reconfigureClaudeDirs(ctx)
	return next, nil
}

// herdrSessionsState asks an online node for its named herdr sessions (herdr.sessions.list) and merges them with the
// computer's blocked list. Offline or older nodes return Live: false with the stored blocked list.
func (m *Machine) herdrSessionsState(ctx context.Context) (proto.HerdrSessionsState, error) {
	blocked, err := m.blockedHerdrSessions(ctx)
	if err != nil {
		return proto.HerdrSessionsState{}, err
	}
	if blocked == nil {
		blocked = []string{}
	}
	state := proto.HerdrSessionsState{
		Sessions: []proto.HerdrNamedSession{},
		Blocked:  blocked,
	}
	m.mu.Lock()
	online := m.peer != nil
	m.mu.Unlock()
	if !online {
		return state, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var res struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"sessions"`
	}
	if err := m.call(callCtx, "herdr.sessions.list", map[string]any{}, &res); err != nil {
		return state, nil
	}
	state.Live = true
	for _, s := range res.Sessions {
		name, ok := proto.CleanHerdrSessionName(s.Name)
		if !ok {
			continue
		}
		state.Sessions = append(state.Sessions, proto.HerdrNamedSession{
			Name:    name,
			Running: s.Running,
			Blocked: slices.Contains(blocked, name),
		})
	}
	return state, nil
}

// HerdrSessions returns machine id's named herdr sessions state; an unknown machine returns empty state.
func (d *Devices) HerdrSessions(ctx context.Context, id string) (proto.HerdrSessionsState, error) {
	m := d.machineByID(id)
	if m == nil {
		return proto.HerdrSessionsState{Sessions: []proto.HerdrNamedSession{}, Blocked: []string{}}, nil
	}
	return m.herdrSessionsState(ctx)
}

// EditBlockedHerdrSessions updates machine id's blocked named herdr sessions list.
func (d *Devices) EditBlockedHerdrSessions(ctx context.Context, id string, edit func([]string) ([]string, error)) ([]string, error) {
	m := d.machineByID(id)
	if m == nil {
		return nil, errUnknownMachine
	}
	return m.editBlockedHerdrSessions(ctx, edit)
}

func (d *Devices) getHerdrSessions(w http.ResponseWriter, r *http.Request, m *Machine) {
	state, err := m.herdrSessionsState(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, state)
}

func (d *Devices) putHerdrSessionsBlocked(w http.ResponseWriter, r *http.Request, m *Machine) {
	var in struct {
		Blocked []string `json:"blocked"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("bad body: %w", err))
		return
	}
	_, err := m.editBlockedHerdrSessions(r.Context(), func([]string) ([]string, error) {
		return in.Blocked, nil
	})
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errBadPattern) {
			code = http.StatusBadRequest
		}
		writeErr(w, code, err)
		return
	}
	state, err := m.herdrSessionsState(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, state)
}
