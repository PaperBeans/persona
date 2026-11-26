package node

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// The information categories of an assistant process's conversation (#13246, docs/extensions/extensions.md
// "Assistant"): next to PERSONA_SPACE and PERSONA_TRACK_KIND, the process gets PERSONA_CATEGORIES, a comma-separated
// sorted list of category names (empty string for general, e.g. PERSONA_CATEGORIES=; "health" in the health space;
// "emotion,finance" when tagged with both). The manager sends the directory's default categories on assistant.prepare
// (categories) and an existing conversation's accumulated categories on assistant.send, session.send (via SendParams)
// and assistant.answer_again (categories, or categories_unknown when a turn waits to be tagged, the store cannot be
// read, or the conversation's track cannot be placed). When the node was not told (an older manager, a directory not
// prepared since the node started, categories_unknown, or an invalid token), PERSONA_CATEGORIES is left out of the
// environment so a command takes the categories as unknown (sensitive). Long-lived processes (headless Claude,
// assistant-tui, codex app-server) and warm spares record the (value, known) state they spawned with so stale idle
// processes restart, active processes refuse stricter/unknown transitions, and stale spares are rejected/replaced.

// categoriesEnv is the variable.
const categoriesEnv = "PERSONA_CATEGORIES"

// categoryPattern is one information category name (lowercase ASCII letters, digits and underscores, starting with a
// letter: health, finance, relationships, emotion, legal_id, and future manager-versioned categories).
var categoryPattern = lazyre.New(`^[a-z][a-z0-9_]{0,31}$`)

// sessionCategoriesState is the last manager-attested categories state for one assistant conversation.
type sessionCategoriesState struct {
	value string
	known bool
}

// categoriesValue normalizes cats into a sorted, deduplicated comma-separated value, or returns ok=false when any
// entry is not a valid category token (the caller then leaves PERSONA_CATEGORIES unset).
func categoriesValue(cats []string) (string, bool) {
	if len(cats) == 0 {
		return "", true
	}
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if !categoryPattern.MatchString(c) {
			return "", false
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return strings.Join(out, ","), true
}

// setSessionCategories records the manager-attested categories for conversation id (assistant.send). An older manager
// that sends neither categories nor categories_unknown leaves any existing state untouched.
func (n *Node) setSessionCategories(id string, cats *[]string, unknown bool) {
	if id == "" || (!unknown && cats == nil) {
		return
	}
	var st sessionCategoriesState
	if !unknown && cats != nil {
		st.value, st.known = categoriesValue(*cats)
	}
	n.mu.Lock()
	if n.assistantCategories == nil {
		n.assistantCategories = map[string]sessionCategoriesState{}
	}
	n.assistantCategories[id] = st
	n.mu.Unlock()
}

// processCategoriesLocked is processCategories with n.mu held.
func (n *Node) processCategoriesLocked(cwd, session string) sessionCategoriesState {
	key := assistantPrepareDir(cwd)
	if session != "" {
		if st, ok := n.assistantCategories[session]; ok {
			return st
		}
	}
	prep := n.assistantPrepared[key]
	return sessionCategoriesState{value: prep.categories, known: prep.categoriesKnown}
}

// processCategories is the PERSONA_CATEGORIES value for a process of session in assistant directory cwd, and whether
// it is known: per-session state from assistant.send takes precedence over the directory's default categories from
// assistant.prepare.
func (n *Node) processCategories(cwd, session string) (string, bool) {
	n.mu.Lock()
	st := n.processCategoriesLocked(cwd, session)
	n.mu.Unlock()
	return st.value, st.known
}

// categoriesFromEnv extracts the PERSONA_CATEGORIES state put into a spawned process's environment slice.
func categoriesFromEnv(env []string) sessionCategoriesState {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], categoriesEnv+"="); ok {
			return sessionCategoriesState{value: v, known: true}
		}
	}
	return sessionCategoriesState{}
}

// categoriesStricter reports whether want requires stricter category protection than have:
// either want is unknown while have was known, or want includes any category token absent from have.
// When have is already unknown (PERSONA_CATEGORIES absent), the process is already fail-closed.
func categoriesStricter(have, want sessionCategoriesState) bool {
	if !have.known {
		return false
	}
	if !want.known {
		return true
	}
	if want.value == "" || want.value == have.value {
		return false
	}
	var haveList []string
	if have.value != "" {
		haveList = strings.Split(have.value, ",")
	}
	for c := range strings.SplitSeq(want.value, ",") {
		if !slices.Contains(haveList, c) {
			return true
		}
	}
	return false
}

// categoriesStale reports whether live process p was started with a different PERSONA_CATEGORIES state than
// processCategories(cwd, p.id) now requires. Guest processes never receive gateEnv or PERSONA_CATEGORIES.
func (n *Node) categoriesStale(p *assistantProc, cwd string) bool {
	if p == nil || p.guest {
		return false
	}
	n.mu.Lock()
	want := n.processCategoriesLocked(cmp.Or(cwd, p.cwd), p.id)
	n.mu.Unlock()
	return p.categories != want
}

// categoriesStricterFor reports whether processCategories(cwd, p.id) requires stricter category protection than
// live process p was started with.
func (n *Node) categoriesStricterFor(p *assistantProc, cwd string) bool {
	if p == nil || p.guest {
		return false
	}
	n.mu.Lock()
	want := n.processCategoriesLocked(cmp.Or(cwd, p.cwd), p.id)
	n.mu.Unlock()
	return categoriesStricter(p.categories, want)
}

// categoriesStaleSpareLocked reports whether warm spare p (with no conversation id yet) was started with a different
// PERSONA_CATEGORIES state than its directory now requires. Caller holds n.mu.
func (n *Node) categoriesStaleSpareLocked(p *assistantProc) bool {
	if p == nil || p.guest {
		return false
	}
	return p.categories != n.processCategoriesLocked(p.cwd, "")
}
