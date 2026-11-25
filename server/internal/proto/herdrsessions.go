package proto

import (
	"fmt"
	"slices"
	"strings"
)

// Named herdr session blocking (#14898): per computer, named herdr sessions (`herdr --session <name>`) kept out of
// Persona. The default herdr server and `persona-*` data-directory sessions are never configurable here (the default
// server is the engine's own; `persona-*` sessions are already ignored by the default-server node).

// MaxBlockedHerdrSessions bounds one computer's blocked named herdr session list.
const MaxBlockedHerdrSessions = 100

// HerdrNamedSession is one named herdr session on a computer (`GET /api/v1/machines/{mid}/herdr/sessions`).
type HerdrNamedSession struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Blocked bool   `json:"blocked"`
}

// HerdrSessionsState is what GET /api/v1/machines/{mid}/herdr/sessions and
// PUT /api/v1/machines/{mid}/herdr/sessions/blocked answer.
type HerdrSessionsState struct {
	Sessions []HerdrNamedSession `json:"sessions"`
	Blocked  []string            `json:"blocked"`
	// Live: true when the node answered herdr.sessions.list; false when offline or on an older node.
	Live bool `json:"live"`
}

// ValidHerdrSessionName reports whether name is a valid configurable named herdr session:
// 1..128 bytes of [A-Za-z0-9._-], not ".", "..", "default", or "persona-*".
func ValidHerdrSessionName(name string) bool {
	if name == "" || name == "." || name == ".." || name == "default" || len(name) > 128 {
		return false
	}
	if strings.HasPrefix(name, "persona-") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// CleanHerdrSessionName trims whitespace and validates a configurable named herdr session name.
func CleanHerdrSessionName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if !ValidHerdrSessionName(name) {
		return "", false
	}
	return name, true
}

// CleanBlockedHerdrSessions trims, validates, and deduplicates stored blocked herdr session names.
func CleanBlockedHerdrSessions(names []string) []string {
	out := []string{}
	for _, raw := range names {
		name, ok := CleanHerdrSessionName(raw)
		if !ok || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// ValidateBlockedHerdrSessions validates a user-supplied list of blocked herdr session names, rejecting invalid names,
// duplicates, or lists exceeding MaxBlockedHerdrSessions.
func ValidateBlockedHerdrSessions(names []string) ([]string, error) {
	if len(names) > MaxBlockedHerdrSessions {
		return nil, fmt.Errorf("at most %d blocked herdr sessions per computer", MaxBlockedHerdrSessions)
	}
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name, ok := CleanHerdrSessionName(raw)
		if !ok {
			return nil, fmt.Errorf("bad herdr session name %q: letters, digits, '-', '_' and '.' only (not default or persona-*)", raw)
		}
		if slices.Contains(out, name) {
			return nil, fmt.Errorf("herdr session %q: listed twice", name)
		}
		out = append(out, name)
	}
	return out, nil
}
