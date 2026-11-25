package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The Inbox as the Lark workbench reads it (the lark module's workbench.go): one item a card's button names.

// workbenchItem is the item a card's button names, as listed, while it is open or done within inboxKeepDone (the list
// carries only the newest of those, inbox_list_done.go, so it is looked up by id rather than in a list).
func (s *Service) workbenchItem(id string) (InboxItem, bool) {
	b := &s.inbox
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.items[id]
	if !ok || it.DoneAt != nil && it.DoneAt.Before(time.Now().Add(-inboxKeepDone)) {
		return InboxItem{}, false
	}
	return it.listed(), true
}

// draftWorkbenchReply is the Inbox action exposed through the registered Lark host. Generation, the test hook,
// and the reload stay together so the rendered text cannot borrow a replacement suggestion's version.
func (s *Service) draftWorkbenchReply(ctx context.Context, id, instruction string, expectedVersion int, strictVersion bool) (assistantwire.WorkbenchItem, string, error) {
	publishedVersion := 0
	text, err := s.draftReplyVersionMode(ctx, id, instruction, expectedVersion, strictVersion, &publishedVersion)
	if hook := s.inbox.workbenchDraftHook; hook != nil {
		hook()
	}
	live, liveOK := s.workbenchItem(id)
	if publishedVersion > 0 && err == nil {
		// Render only the exact text/version pair created by this generation. A replacement installed before this
		// reload must never lend its newer version to the older generated text.
		g := currentSuggestion(&live)
		if !liveOK || g == nil || g.Kind != SuggestReply || g.Version != publishedVersion || g.Text != text {
			err = errSuggestionStale
		}
	}
	cardItem := workbenchItemOf(live)
	if liveOK && publishedVersion == 0 && err == nil {
		// A transient result has no publication version and must not borrow a mirror installed while it generated.
		cardItem.ReplyVersion = 0
	}
	return cardItem, text, err
}
