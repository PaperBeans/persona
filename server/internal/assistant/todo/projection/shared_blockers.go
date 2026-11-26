package projection

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

const (
	SharedBlockerKindPullRequest    = "pull_request"
	SharedBlockerKindIssue          = "issue"
	SharedBlockerKindExternalTarget = "external_target"
)

// SharedBlocker is a deterministic summary of an external blocker shared by two or more open tasks under todos_v2.
type SharedBlocker struct {
	Key     string   `json:"key"`
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	URL     string   `json:"url,omitempty"`
	TodoIDs []string `json:"todo_ids"`
	Count   int      `json:"count"`
}

type blockerRef struct {
	key   string
	kind  string
	label string
	url   string
}

// SharedBlockers groups open tasks that share the same normalized external blocker root cause
// (PR URL, issue URL, or external wait target) when todos_v2 is enabled.
// Only groups with Count >= 2 are emitted, ordered deterministically by Count descending then Key ascending.
// Closed (Done / Cancelled) and Someday tasks are excluded.
func SharedBlockers(list []Facts, views []assistantwire.TodoSummary) []SharedBlocker {
	if len(list) == 0 || len(views) == 0 {
		return nil
	}
	type group struct {
		ref   blockerRef
		todos map[string]bool
	}
	groups := map[string]*group{}
	for i := range list {
		f := &list[i]
		if !f.TodosV2 || f.ID == "" || i >= len(views) || !isOpen(views[i]) {
			continue
		}
		refs := extractTaskBlockers(f, views[i])
		for _, ref := range refs {
			g := groups[ref.key]
			if g == nil {
				g = &group{ref: ref, todos: map[string]bool{}}
				groups[ref.key] = g
			} else if ref.label != "" && (g.ref.label == "" || ref.label < g.ref.label) {
				// Pick lexicographically smallest display label across matching tasks so output is order-independent.
				g.ref.label = ref.label
			}
			g.todos[f.ID] = true
		}
	}
	var out []SharedBlocker
	for _, g := range groups {
		if len(g.todos) < 2 {
			continue
		}
		ids := make([]string, 0, len(g.todos))
		for id := range g.todos {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		out = append(out, SharedBlocker{
			Key:     g.ref.key,
			Kind:    g.ref.kind,
			Label:   g.ref.label,
			URL:     g.ref.url,
			TodoIDs: ids,
			Count:   len(ids),
		})
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b SharedBlocker) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Key, b.Key))
	})
	return out
}

func extractTaskBlockers(f *Facts, v assistantwire.TodoSummary) []blockerRef {
	var refs []blockerRef
	seen := map[string]bool{}
	add := func(ref blockerRef, ok bool) {
		if !ok || ref.key == "" || seen[ref.key] {
			return
		}
		seen[ref.key] = true
		refs = append(refs, ref)
	}

	if sv := f.Supervision; sv != nil && sv.State == reviewParked && sv.Parked != nil && sv.Parked.Evidence != nil {
		ev := sv.Parked.Evidence
		if strings.TrimSpace(ev.URL) != "" {
			add(normalizeBlockerURL(ev.URL))
		} else if strings.TrimSpace(ev.Target) != "" {
			if ref, ok := normalizeBlockerURL(ev.Target); ok {
				add(ref, true)
			} else {
				add(normalizeBlockerTarget(ev.Target))
			}
		}
	}

	if v.Phase != assistantwire.TodoPhaseReady && v.Phase != assistantwire.TodoPhaseWorking {
		if f.PRURL != "" && (f.PRStatus == nil || f.PRStatus.State == "" || f.PRStatus.State == prOpen) {
			add(normalizeBlockerURL(f.PRURL))
		}
		if f.PRStatus != nil && f.PRStatus.URL != "" && f.PRStatus.State == prOpen {
			add(normalizeBlockerURL(f.PRStatus.URL))
		}
		for _, pr := range f.OtherPRs {
			if pr.URL != "" && pr.State == prOpen {
				add(normalizeBlockerURL(pr.URL))
			}
		}
	}
	return refs
}

func normalizeBlockerURL(raw string) (blockerRef, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return blockerRef{}, false
	}
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		raw = raw[:i]
	}
	sep := strings.Index(raw, "://")
	if sep <= 0 {
		return blockerRef{}, false
	}
	scheme := strings.ToLower(raw[:sep])
	if scheme != "http" && scheme != "https" {
		return blockerRef{}, false
	}
	rest := raw[sep+3:]
	host := rest
	path := ""
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		host = rest[:slash]
		path = rest[slash:]
	}
	host = strings.ToLower(host)
	if host == "" || strings.Contains(host, "@") {
		return blockerRef{}, false
	}
	trimmedPath := strings.TrimRight(path, "/")
	segs := splitNonEmpty(trimmedPath)

	// GitHub / Gitea style PR or Issue: /<owner>/<repo>/(pull|issues)/<num>
	if len(segs) >= 4 && isPositiveInt(segs[3]) {
		owner := strings.ToLower(segs[0])
		repo := strings.ToLower(segs[1])
		num := segs[3]
		switch strings.ToLower(segs[2]) {
		case "pull":
			canonical := scheme + "://" + host + "/" + owner + "/" + repo + "/pull/" + num
			return blockerRef{
				key:   SharedBlockerKindPullRequest + ":" + canonical,
				kind:  SharedBlockerKindPullRequest,
				label: owner + "/" + repo + "#" + num,
				url:   canonical,
			}, true
		case "issues":
			canonical := scheme + "://" + host + "/" + owner + "/" + repo + "/issues/" + num
			return blockerRef{
				key:   SharedBlockerKindIssue + ":" + canonical,
				kind:  SharedBlockerKindIssue,
				label: owner + "/" + repo + "#" + num,
				url:   canonical,
			}, true
		}
	}

	// GitLab style MR or Issue: /<group...>/<repo>/-/(merge_requests|issues)/<num>
	for i := 2; i+2 < len(segs); i++ {
		if segs[i] != "-" || !isPositiveInt(segs[i+2]) {
			continue
		}
		repoPath := strings.ToLower(strings.Join(segs[:i], "/"))
		num := segs[i+2]
		switch strings.ToLower(segs[i+1]) {
		case "merge_requests":
			canonical := scheme + "://" + host + "/" + repoPath + "/-/merge_requests/" + num
			return blockerRef{
				key:   SharedBlockerKindPullRequest + ":" + canonical,
				kind:  SharedBlockerKindPullRequest,
				label: repoPath + "!" + num,
				url:   canonical,
			}, true
		case "issues":
			canonical := scheme + "://" + host + "/" + repoPath + "/-/issues/" + num
			return blockerRef{
				key:   SharedBlockerKindIssue + ":" + canonical,
				kind:  SharedBlockerKindIssue,
				label: repoPath + "#" + num,
				url:   canonical,
			}, true
		}
	}

	canonical := scheme + "://" + host + trimmedPath
	label := host + trimmedPath
	return blockerRef{
		key:   "url:" + canonical,
		kind:  SharedBlockerKindExternalTarget,
		label: label,
		url:   canonical,
	}, true
}

func normalizeBlockerTarget(raw string) (blockerRef, bool) {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if collapsed == "" {
		return blockerRef{}, false
	}
	norm := strings.ToLower(collapsed)
	return blockerRef{
		key:   "target:" + norm,
		kind:  SharedBlockerKindExternalTarget,
		label: collapsed,
	}, true
}

func splitNonEmpty(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func isPositiveInt(s string) bool {
	if s == "" {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}
