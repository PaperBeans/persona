package node

// A conversation the user quoted with @, made reachable for the Agent that reads the quote (#13831). Every client
// writes the session picked in the composer's @ menu as a leading <persona-context kind="context"> block whose url is
// that session's address, persona://session/<machine>/<session> (web lib/mentions.ts sessionQuote, iOS
// PromptItems.quote, Android sessionQuote). The block itself says only the session's title, folder and last words.
// Before an Agent gets the message, the block's body gains the address and how to read more of that conversation or
// write to it through A2A (prompts agents/session_mention, in the owner's language): the Persona tools, or, for a
// harness without the Persona MCP tools, this node's program run from a terminal, named by its path as the task
// handoff names it (personad is not always on an Agent's PATH). The chip the apps draw from the block is unchanged;
// the note shows where the block is opened.

import (
	"cmp"
	"context"
	"encoding/json"
	"html"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// sessionMentionURL starts a quoted session's address.
const sessionMentionURL = "persona://session/"

// leadingContextBlock is one context block at the start of what is left of a message: its attributes and body. The
// clients put picked contexts in front, so a block further on (quoted inside the user's words) is never a mention.
var leadingContextBlock = lazyre.New(`(?s)^\s*<` + compat.ContextPattern() + `\b([^>]*)>(.*?)</` + compat.ContextPattern() + `>`)

// contextBlockAttr is one name="value" on a context tag, after a space (so data-kind="…" is not kind="…").
var contextBlockAttr = lazyre.New(`(?:^|\s)([a-z_]+)="([^"]*)"`)

// sessionNotes renders the notes of one message: the owner's language (the manager's prompt_language) and the node's
// program as a JSON string ("" when it has no absolute path).
type sessionNotes struct {
	language   string
	executable string
}

func newSessionNotes(language, program string) sessionNotes {
	notes := sessionNotes{language: language}
	if proto.NodeAbs(program) {
		encoded, _ := json.Marshal(program)
		notes.executable = string(encoded)
	}
	return notes
}

func (s sessionNotes) note(language, machine, session string) string {
	return prompts.RenderSessionMention(prompts.SessionMention{Language: language, Machine: machine, Session: session, Executable: s.executable})
}

// withSessionMentions is text for Agent session id with each leading session quote noted; text as it is for the
// assistant's conversations and guests, which read quotes with their own tools, and for a message with none. It runs
// for every session.send before any path takes the message (node.go handleMethod: a pane herdr types into, a
// runtime's session, the node's own send) and before the send is recorded for its echo, so the transcript's line is
// the text recorded.
func (n *Node) withSessionMentions(ctx context.Context, id, text string) string {
	if !strings.Contains(text, sessionMentionURL) || n.isAssistantSession(id) || n.guestOf(id) != nil {
		return text
	}
	return newSessionNotes(agentPromptLanguage(ctx), cmp.Or(n.bin, n.exe)).apply(text)
}

// apply is text with the body of each leading session quote ending in its note. A quote that has its note already
// (sent again: an edited or compacted resend) keeps it alone, in whichever language it was written.
func (s sessionNotes) apply(text string) string {
	var b strings.Builder
	rest, changed := text, false
	for {
		m := leadingContextBlock.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		block := rest[:m[1]]
		if machine, session, ok := mentionedSession(rest[m[2]:m[3]]); ok {
			body := rest[m[4]:m[5]]
			if !s.noted(body, machine, session) {
				block = rest[:m[4]] + strings.TrimRight(body, "\r\n") + "\n\n" + s.note(s.language, machine, session) + "\n" + rest[m[5]:m[1]]
				changed = true
			}
		}
		b.WriteString(block)
		rest = rest[m[1]:]
	}
	if !changed {
		return text
	}
	b.WriteString(rest)
	return b.String()
}

// noted: body holds this session's note in any of the languages it is written in.
func (s sessionNotes) noted(body, machine, session string) bool {
	for _, language := range []string{"", "English", "日本語"} {
		if strings.Contains(body, s.note(language, machine, session)) {
			return true
		}
	}
	return false
}

// mentionedSession is the session a context tag's attributes quote: kind "context", as the @ menu writes it, and a
// url that is exactly persona://session/<machine>/<session>. A forward (kind "forward", a url with ?at=) and any other
// block are not mentions, nor a tag that gives an attribute twice.
func mentionedSession(tag string) (machine, session string, ok bool) {
	attrs := map[string]string{}
	for _, a := range contextBlockAttr.FindAllStringSubmatch(tag, -1) {
		if _, twice := attrs[a[1]]; twice {
			return "", "", false
		}
		attrs[a[1]] = html.UnescapeString(a[2])
	}
	address, found := strings.CutPrefix(attrs["url"], sessionMentionURL)
	if attrs["kind"] != "context" || !found {
		return "", "", false
	}
	machine, session, found = strings.Cut(address, "/")
	if !found || !plainMentionID(machine) || !plainMentionID(session) {
		return "", "", false
	}
	return machine, session, true
}

// plainMentionID: an id the note can quote as it is, inside quotes and JSON: at most 200 bytes, without space,
// control characters, quotes, backslashes, angle brackets, &, or a url's / ? #.
func plainMentionID(id string) bool {
	return id != "" && len(id) <= 200 && !strings.ContainsFunc(id, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("\"'`\\<>&/?#", r)
	})
}
