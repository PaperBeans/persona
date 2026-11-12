package assistant

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

var inboxTaskURL = lazyre.New(`https?://[^\s<>"\x60]+`)

// inboxTaskRefs keeps the host, repository, PR/issue kind and number. Comment fragments and queries do not
// change the task. A bare number is insufficient evidence for routing another person's message.
func inboxTaskRefs(text string) []string {
	var refs []string
	for _, raw := range inboxTaskURL.FindAllString(text, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)]}，。；：！）》」』")) // i18n-ignore: URL punctuation, not displayed text
		if err != nil || u.Host == "" || u.User != nil {
			continue
		}
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" && parts[2] != "issues" {
			continue
		}
		n, err := strconv.ParseUint(parts[3], 10, 64)
		if err != nil || n == 0 {
			continue
		}
		repo := strings.Join(parts[:3], "/")
		if strings.EqualFold(u.Hostname(), "github.com") {
			repo = strings.ToLower(repo)
		}
		refs = append(refs, strings.ToLower(u.Host)+"/"+repo+"/"+strconv.FormatUint(n, 10))
	}
	return refs
}

// linkInboxTaskURLs runs before the AI twin's switch: exact task links need neither triage nor a model. Keep
// existing links, leave ambiguous matches to the judge, and persist new links before notifying their supervisors.
func (s *Service) linkInboxTaskURLs(ctx context.Context) {
	if s.ensureTodosLoaded(ctx) != nil {
		return
	}
	byURL := map[string]string{}
	s.todos.mu.RLock()
	for _, t := range s.todos.todos {
		if t.DoneAt != nil || t.Cancelled() || isAskTodo(t.ID) {
			continue
		}
		for _, ref := range inboxTaskRefs(t.PRURL + " " + t.Title + " " + t.Notes) {
			if id, exists := byURL[ref]; exists && id != t.ID {
				byURL[ref] = "" // more than one open task names this link
			} else if !exists {
				byURL[ref] = t.ID
			}
		}
	}
	s.todos.mu.RUnlock()
	b := &s.inbox
	linked := map[*InboxItem]bool{}
	var writes []*inboxWrite
	var items []*InboxItem
	b.mu.Lock()
	for _, it := range b.items {
		if !it.open() {
			continue
		}
		if it.Agent == nil || it.Agent.TodoID == "" {
			text := it.Title + " " + it.Preview + " " + it.Context
			if it.Connector != nil {
				text += " " + it.Connector.URL
			}
			id, ambiguous := "", false
			for _, ref := range inboxTaskRefs(text) {
				if match, exists := byURL[ref]; exists {
					if match == "" || id != "" && id != match {
						ambiguous = true
						break
					}
					id = match
				}
			}
			if id != "" && !ambiguous {
				w := beginWrite(it)
				if it.Agent == nil {
					it.Agent = &InboxAgent{State: AgentSkipped}
				}
				it.Agent.TodoID = id
				b.settle(w)
				writes = append(writes, w)
				linked[it] = true
			}
		}
		if it.Agent != nil && it.Agent.TodoID != "" {
			items = append(items, it)
		}
	}
	b.mu.Unlock()
	failed := s.saveWrites(ctx, writes, "its task URL link")
	kept := items[:0]
	for _, it := range items {
		if !failed[it.ID] {
			kept = append(kept, it)
		}
	}
	s.superviseInbox(ctx, kept, linked)
}
