package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Thread preserves the source's message boundaries. Inbox context is a legacy
// text projection and cannot supply identity, direction or pagination for this page.
func (c *Connector) Thread(ctx context.Context, thread, before string, limit int) (connector.ThreadPage, error) {
	chatID, topic, err := conversationIDs(thread)
	if err != nil {
		return connector.ThreadPage{}, conversationError(err)
	}
	if limit <= 0 || limit > connector.ThreadMost {
		limit = connector.ThreadMost
	}
	auth, err := c.runner.AuthStatus(ctx)
	if err != nil {
		return connector.ThreadPage{}, conversationError(err)
	}
	if !auth.LoggedIn || auth.Expired {
		return connector.ThreadPage{}, conversationError(&adapter.Error{Kind: adapter.KindNotLoggedIn})
	}
	source, err := c.runner.ListMessages(ctx, chatID, topic, before, limit)
	if err != nil {
		return connector.ThreadPage{}, conversationError(err)
	}
	page := connector.ThreadPage{Chat: connector.ThreadChat{ID: thread, Kind: ""}, Messages: []connector.ThreadMessage{}}
	seen := make(map[string]bool, len(source.Messages))
	for _, m := range source.Messages {
		if m.MessageID == "" || seen[m.MessageID] {
			continue
		}
		seen[m.MessageID] = true
		// Never display a response belonging to a different chat or topic.
		if m.ChatID != "" && m.ChatID != chatID {
			continue
		}
		if topic != "" && m.ThreadID != "" && m.ThreadID != topic {
			continue
		}
		if m.ChatType == "p2p" {
			page.Chat.Kind = "dm"
		} else if m.ChatType == "group" {
			page.Chat.Kind = "group"
		}
		if m.ChatName != "" {
			page.Chat.Name = m.ChatName
		}
		mine := auth.OpenID != "" && (m.Sender.IDType == "" || m.Sender.IDType == "open_id") && m.Sender.ID == auth.OpenID
		text := conversationText(m.Content)
		format := ""
		if m.MsgType == "post" || m.MsgType == "interactive" {
			format = "markdown"
		}
		out := connector.ThreadMessage{ID: m.MessageID, At: conversationTime(m.CreateTime), Sender: connector.ThreadPerson{ID: m.Sender.ID, Name: m.Sender.Name}, Mine: mine, Text: text, Format: format, Edited: m.Updated}
		// Existing Inbox items may predate stored AppLinks. Their loaded original
		// message can still supply its source link without recollecting history.
		link, linkErr := url.Parse(m.MessageAppLink)
		if linkErr == nil && link.Scheme == "https" && link.User == nil &&
			(link.Host == "applink.feishu.cn" || link.Host == "applink.larksuite.com") &&
			(link.Path == "/client/chat/open" || link.Path == "/client/thread/open") {
			out.URL = m.MessageAppLink
		}
		if m.Deleted {
			out.Text = ""
			out.Format = ""
			out.Event = true
		}
		if m.ParentID != "" {
			out.ReplyTo = &connector.ThreadQuote{ID: m.ParentID}
		}
		page.Messages = append(page.Messages, out)
	}
	if source.HasMore {
		page.Before = source.PageToken
	}
	return page, nil
}

// Media is deliberately unavailable until the CLI attachment contract is wired.
// No media references are advertised by these text/card pages.
func (c *Connector) Media(context.Context, string) (connector.Media, error) {
	return connector.Media{}, errors.New("lark-cli conversation attachments are not available")
}

func conversationIDs(thread string) (chat, topic string, err error) {
	rest, ok := strings.CutPrefix(thread, "larkcli:chat:")
	if !ok {
		return "", "", fmt.Errorf("invalid lark-cli conversation")
	}
	chat, topic, split := strings.Cut(rest, ":thread:")
	if chat == "" || strings.ContainsAny(chat, ":\r\n\t ") || split && (topic == "" || strings.ContainsAny(topic, ":\r\n\t ")) {
		return "", "", fmt.Errorf("invalid lark-cli conversation")
	}
	return chat, topic, nil
}

func conversationTime(value string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	// CLI 1.0.99's common.FormatTime emits local wall time without an offset.
	// Keep the CLI process's zone (cliLocation); interpreting it as UTC shifts the chat.
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, value, cliLocation()); err == nil {
			return t
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	if n >= 1e12 {
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// The shortcut may nest a JSON string around {text: ...}. Retain unknown
// structures literally rather than flattening arbitrary object fields out of order.
func conversationText(raw json.RawMessage) string {
	var object struct {
		Text *string `json:"text"`
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if json.Unmarshal([]byte(text), &object) == nil && object.Text != nil {
			return *object.Text
		}
		return text
	}
	if json.Unmarshal(raw, &object) == nil && object.Text != nil {
		return *object.Text
	}
	return string(raw)
}

// Preserve a failed read's category even when the next auth-status call succeeds.
func conversationError(err error) error {
	code := ""
	switch adapter.KindOf(err) {
	case adapter.KindRateLimited:
		code = "rate_limited"
	case adapter.KindMissingPermission:
		code = "no_permission"
	case adapter.KindExpired, adapter.KindNotLoggedIn:
		code = "not_signed_in"
	}
	if code != "" {
		return &rpc.Error{Code: code, Message: err.Error()}
	}
	return err
}
