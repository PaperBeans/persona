package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Blocked named herdr sessions (#14898): per computer, named herdr sessions (`herdr --session <name>`) kept out of
// Persona (internal/devices herdrsessions.go). Same get / add / remove flow as agents.hidden_dirs and
// agents.blocked_claude_dirs: every change waits for user approval and offers rollback.

const blockedHerdrSessionsKey = "agents.blocked_herdr_sessions"

// HerdrSessions is where each computer's named herdr sessions and blocked list are kept (internal/devices.Devices).
type HerdrSessions interface {
	HerdrSessions(ctx context.Context, id string) (proto.HerdrSessionsState, error)
	EditBlockedHerdrSessions(ctx context.Context, id string, edit func([]string) ([]string, error)) ([]string, error)
}

func (s *Service) blockedHerdrSessionsSetting() ownSetting {
	return ownSetting{
		Setting: Setting{
			Key:     blockedHerdrSessionsKey,
			Title:   i18n.T(i18n.Default, "assistant-herdr_sessions.title"),
			Actions: []string{"get", "add", "remove"},
			About: "per machine, named herdr sessions (`herdr --session <name>`) kept out of Persona: " +
				"their panes, workspaces, hooks, permission cards, live processes and remembered transcripts do not appear in Persona, " +
				"while herdr itself keeps running. The default server and persona-* sessions are never configurable here. " +
				"get lists each machine's blocked sessions and detected named sessions",
			Value: `add: {"machine": machine id, "session": named herdr session, e.g. "agent"}; remove: "<machine id>:<session>"`,
		},
		get:    s.getBlockedHerdrSessions,
		change: s.changeBlockedHerdrSessions,
	}
}

func (s *Service) getBlockedHerdrSessions(ctx context.Context) (string, error) {
	if s.herdrSessions == nil {
		return "No machines.", nil
	}
	var b strings.Builder
	for _, m := range s.fleetMachines() {
		st, err := s.herdrSessions.HerdrSessions(ctx, m.ID)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s (%s): blocked %s\n", m.ID, m.Name, orNone(strings.Join(st.Blocked, ", ")))
		for _, hs := range st.Sessions {
			state := "listed"
			if hs.Blocked {
				state = "blocked"
			}
			run := "stopped"
			if hs.Running {
				run = "running"
			}
			fmt.Fprintf(&b, "  - %s (%s, %s)\n", hs.Name, run, state)
		}
	}
	if b.Len() == 0 {
		return "No machines.", nil
	}
	return b.String(), nil
}

func (s *Service) changeBlockedHerdrSessions(ctx context.Context, action string, v json.RawMessage) (*ownChange, error) {
	if s.herdrSessions == nil {
		return nil, errors.New("blocked herdr sessions are kept with the computers, and this manager carries none")
	}
	var machine, rawSession string
	switch action {
	case "add":
		var a struct {
			Machine string `json:"machine"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(v, &a); err != nil {
			return nil, errors.New(`value must be {"machine", "session"}`)
		}
		machine, rawSession = a.Machine, a.Session
	case "remove":
		var id string
		if err := json.Unmarshal(v, &id); err != nil {
			return nil, errors.New(`remove takes "<machine id>:<session>"`)
		}
		var ok bool
		if machine, rawSession, ok = strings.Cut(id, ":"); !ok {
			return nil, fmt.Errorf(`remove takes "<machine id>:<session>", got %q`, id)
		}
	default:
		return nil, fmt.Errorf("unsupported action %q", action)
	}
	m, ok := s.fleetMachine(strings.TrimSpace(machine))
	if !ok {
		return nil, fmt.Errorf("no machine %q", machine)
	}
	session, ok := proto.CleanHerdrSessionName(rawSession)
	if !ok {
		return nil, fmt.Errorf("bad herdr session name %q: letters, digits, '-', '_' and '.' only (not default or persona-*)", rawSession)
	}
	st, err := s.herdrSessions.HerdrSessions(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	listed := slices.Contains(st.Blocked, session)
	name := m.Name
	if name == "" {
		name = m.ID
	}
	args := i18n.Args{"machine": name, "session": session}
	switch action {
	case "add":
		if listed {
			return &ownChange{summary: session + " is already blocked on " + m.ID + "."}, nil
		}
		return &ownChange{
			summary:  "Block named herdr session " + session + " on " + m.ID,
			cardText: func(l i18n.Lang) string { return i18n.T(l, "assistant-herdr_sessions.block", args) },
			apply: func(ctx context.Context) (string, error) {
				_, err := s.herdrSessions.EditBlockedHerdrSessions(ctx, m.ID, func(now []string) ([]string, error) {
					if slices.Contains(now, session) {
						return now, nil
					}
					return append(now, session), nil
				})
				if err != nil {
					return "", err
				}
				return "Blocked named herdr session " + session + " on " + m.ID + ". Use remove with \"" + m.ID + ":" + session + "\" to undo.", nil
			},
		}, nil
	case "remove":
		if !listed {
			return nil, fmt.Errorf("%s is not blocked on %s (get lists the blocked sessions)", session, m.ID)
		}
		return &ownChange{
			summary:  "Unblock named herdr session " + session + " on " + m.ID,
			cardText: func(l i18n.Lang) string { return i18n.T(l, "assistant-herdr_sessions.unblock", args) },
			apply: func(ctx context.Context) (string, error) {
				_, err := s.herdrSessions.EditBlockedHerdrSessions(ctx, m.ID, func(now []string) ([]string, error) {
					return slices.DeleteFunc(now, func(x string) bool { return x == session }), nil
				})
				if err != nil {
					return "", err
				}
				return "Unblocked named herdr session " + session + " on " + m.ID + ".", nil
			},
		}, nil
	}
	return nil, nil
}
