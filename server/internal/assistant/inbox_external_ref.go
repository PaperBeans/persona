package assistant

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/repositorywire"
)

func externalNode(kind, id string) assistantwire.ExternalRefNode {
	if id == "" {
		return assistantwire.ExternalRefNode{}
	}
	return assistantwire.ExternalRefNode{Kind: assistantwire.ExternalRefKind(kind), ID: assistantwire.ExternalRefID(id)}
}

func (b *inboxBook) setConnectorExternalPath(it *InboxItem, thing connector.InboxThing) {
	b.srcMu.RLock()
	via := b.vias[it.Source].connector
	b.srcMu.RUnlock()
	setInboxExternalPath(it, via, thing)
}

func setInboxExternalPath(it *InboxItem, via string, thing connector.InboxThing) {
	var ref assistantwire.ExternalRef
	provider := it.Source
	// A recipe that retains a provider's own inbox envelope can report the
	// same object as its direct connector. Recipe-generated rows have their own source.
	thread, subscription := strings.CutPrefix(thing.Thread, "sub:"+it.Source+":")
	if !subscription && via != "" {
		provider = via
	}
	switch {
	case it.Mail != nil:
		ref = assistantwire.ExternalRef{Provider: "mail", Channel: assistantwire.ExternalChannelID(it.Mail.AccountID),
			Container: externalNode("mailbox", it.Mail.Mailbox), Item: externalNode("thread", it.Mail.Thread)}
	case it.Lark != nil || provider == "lark" || provider == "feishu" || provider == "larkcli":
		ref = larkExternalRef(it.Lark, thing)
	case provider == "github":
		ref = githubExternalRef(thing)
	case it.Source != inboxPostSource && thread != "":
		if subscription {
			provider = "subscription"
		}
		ref = assistantwire.ExternalRef{Provider: assistantwire.ExternalProvider(provider),
			Container: externalNode("source", it.Source), Item: externalNode("item", thread)}
	}
	it.ExternalPath, _ = externalbindingadapt.Path(ref)
}

func larkExternalRef(lark *InboxLark, thing connector.InboxThing) assistantwire.ExternalRef {
	ref := assistantwire.ExternalRef{Provider: "lark"}
	var chat, topic string
	if lark != nil {
		chat = lark.FeedID
		if lark.AccountID > 1 {
			ref.Channel = assistantwire.ExternalChannelID(strconv.Itoa(lark.AccountID))
		}
	}
	if rest, ok := strings.CutPrefix(thing.Thread, "larkcli:chat:"); ok {
		chat, topic, _ = strings.Cut(rest, ":thread:")
	}
	if u, err := url.Parse(thing.OpenURL); err == nil && u.Scheme == "https" && u.User == nil &&
		(u.Host == "applink.feishu.cn" || u.Host == "applink.larksuite.com") && u.Path == "/client/thread/open" {
		q := u.Query()
		if linkedChat := q.Get("open_chat_id"); linkedChat != "" && (chat == "" || chat == linkedChat) {
			chat, topic = linkedChat, q.Get("open_thread_id")
		}
	}
	if tagged := inboxExternalTag(thing.Tags, connector.TagChat); tagged != "" && (chat == "" || !strings.HasPrefix(chat, "oc_")) {
		chat = tagged
	}
	if chat == "" {
		if raw, ok := strings.CutPrefix(thing.Thread, "chat:"); ok {
			if account, feed, found := strings.Cut(raw, ":"); found {
				if n, err := strconv.Atoi(account); err == nil && n > 1 {
					ref.Channel, raw = assistantwire.ExternalChannelID(account), feed
				}
			}
			chat = raw
		}
	}
	ref.Container, ref.Item = externalNode("chat", chat), externalNode("thread", topic)
	return ref
}

func githubExternalRef(thing connector.InboxThing) assistantwire.ExternalRef {
	// The notification's own object, or the PR associated with its CI
	// branch, comes from source metadata. A URL in the message is just text.
	raw := inboxExternalTag(thing.Tags, connector.TagExternalObject)
	if raw == "" {
		raw = thing.URL
	}
	if raw == "" {
		raw = thing.OpenURL
	}
	ref := assistantwire.ExternalRef{Provider: "github"}
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Host != "" && u.RawPath == "" {
		ref.Channel = assistantwire.ExternalChannelID(u.Scheme + "://" + u.Host)
		path := strings.Trim(u.Path, "/")
		if strings.EqualFold(u.Hostname(), "api.github.com") {
			path = strings.TrimPrefix(path, "repos/")
		}
		parts := strings.Split(path, "/")
		if len(parts) >= 2 {
			ref.Container = externalNode("repository", parts[0]+"/"+parts[1])
		}
		if len(parts) >= 4 {
			switch parts[2] {
			case "pull", "pulls":
				ref.Item = externalNode("pull_request", parts[3])
			case "issues":
				ref.Item = externalNode("issue", parts[3])
			}
		}
	}
	if ref.Container == (assistantwire.ExternalRefNode{}) {
		repo := inboxExternalTag(thing.Tags, connector.TagRepo)
		if repo == "" {
			repo, _, _ = strings.Cut(thing.Thread, "#")
			if _, err := repositorywire.Parse("https://github.com/"+repo, "github"); err != nil {
				repo = ""
			}
		}
		ref.Container = externalNode("repository", repo)
	}
	return ref
}

func inboxExternalTag(tags []connector.Tag, key string) string {
	for _, tag := range tags {
		if tag.Key == key {
			return tag.Value
		}
	}
	return ""
}

// Use stable source event ids when present, with the source cursor as fallback.
// GitHub's notification cursor can hold multiple comments at the same timestamp
// and an account epoch used for reply authorization. Neither is a comment id.
func inboxExternalEventIDs(it *InboxItem) []string {
	if len(it.ExternalPath) == 0 {
		return nil
	}
	last := it.Last
	switch it.ExternalPath[0].Provider {
	case "mail":
		if id := strings.Trim(it.Mail.MessageID, "<> \r\n\t"); id != "" {
			return []string{"message:" + id}
		}
		if last != "" {
			return []string{"uid:" + strconv.FormatUint(uint64(it.Mail.Generation), 10) + ":" + last}
		}
	case "lark":
		if it.Lark != nil && strings.HasPrefix(it.Lark.ReplyTo, "om_") {
			return []string{it.Lark.ReplyTo}
		}
	case "github":
		if cursor, epoch, ok := strings.Cut(last, "#"); ok {
			if _, err := strconv.ParseUint(epoch, 10, 64); err == nil {
				last = cursor
			}
		}
		if raw, ok := strings.CutPrefix(last, "gh-message:"); ok {
			var head struct{ IDs []string }
			if json.Unmarshal([]byte(raw), &head) == nil && len(head.IDs) != 0 {
				slices.Sort(head.IDs)
				return slices.Compact(head.IDs)
			}
		}
		if it.Connector != nil {
			if u, err := url.Parse(it.Connector.URL); err == nil {
				if id := githubExternalCommentID(u.Fragment); id != "" {
					return []string{id}
				}
			}
		}
	}
	if last == "" {
		return nil
	}
	return []string{last}
}

func githubExternalCommentID(fragment string) string {
	for _, part := range []struct{ prefix, kind string }{
		{"issuecomment-", "comment:"}, {"pullrequestreview-", "review:"}, {"discussion_r", "review-comment:"},
	} {
		if raw, ok := strings.CutPrefix(fragment, part.prefix); ok {
			if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id > 0 {
				return part.kind + strconv.FormatUint(id, 10)
			}
		}
	}
	return ""
}
