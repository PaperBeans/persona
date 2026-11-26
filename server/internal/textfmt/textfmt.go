// Package textfmt is the small text formatting the hub and the assistant (internal/assistant, a process of its own)
// both write with, for people and for models: a clip or a cut, one line, how long ago, a short id, how an agent's
// session is named and its state. One copy that both import; neither imports the other.
//
//persona:layer library
package textfmt

import (
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/proto"
)

// Clip is s cut to its first n runes plus an ellipsis; a string of at most n runes comes back as it is, without a copy.
func Clip(s string, n int) string {
	if head, cut := runePrefix(s, n); cut {
		return head + "…"
	}
	return s
}

// Cut is s cut to its first n runes, without an ellipsis; a string of at most n runes comes back as it is, without a
// copy.
func Cut(s string, n int) string {
	head, _ := runePrefix(s, n)
	return head
}

// runePrefix is s's first n runes, and whether that left anything off. Ranging over s steps through the same runes as
// []rune(s) (one U+FFFD per invalid byte), so the cut lands where string([]rune(s)[:n]) puts it: a valid prefix is cut
// at its byte offset, and one with invalid bytes still has them replaced. An uncut s is kept as it is, invalid bytes
// and all.
func runePrefix(s string, n int) (string, bool) {
	if len(s) <= n { // n bytes are at most n runes
		return s, false
	}
	runes := 0
	for i := range s {
		if runes == n { // rune n+1 starts at i
			if utf8.ValidString(s[:i]) {
				return s[:i], true
			}
			return string([]rune(s[:i])), true
		}
		runes++
	}
	return s, false
}

// OneLine is s with every run of white space (line breaks among them) one space, trimmed.
func OneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// FirstNonEmpty is the first of s with more than white space in it, as given (not trimmed); "" when there is none.
func FirstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// FirstLine is the first line of s with text, without a Markdown heading's #s.
func FirstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(l, "# "))
		if l != "" {
			return l
		}
	}
	return ""
}

// Ago is how long ago t was, in words ("just now", "5 minutes ago"… in Chinese); "" for the zero time.
func Ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

// Short is an id's first 8 characters.
func Short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// StateNames are the agent states (proto.State*) as the apps say them.
var StateNames = map[string]string{
	proto.StateNeedsYou: "需要你", proto.StateWorking: "工作中", proto.StateUnread: "完成未看", proto.StateIdle: "空闲",
	proto.StateOffline: "离线", proto.StateUnknown: "未知",
}

// AgentLabel is how an agent's session is named: its title (its directory's name while it has none), under its
// herdr space and slot.
func AgentLabel(s proto.Session) string {
	title := s.Title
	if title == "" || title == "Claude Code" {
		title = path.Base(s.Cwd)
	}
	if s.SpaceName != "" {
		slot := s.SlotName
		if slot == "" || slot == title {
			return s.SpaceName + " › " + title
		}
		return s.SpaceName + " › " + slot + "「" + title + "」"
	}
	return title
}
