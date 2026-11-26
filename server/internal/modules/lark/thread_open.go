package lark

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Opening a to-do from a topic (#5279, docs/assistant/todos.md "Opening a to-do from a topic"): in a group chat the hub
// registered, a topic the owner starts opens a to-do bound to it and hands the first words to the main conversation,
// the way a new topic starts a session in a bot that auto-starts on one. In a topic that exists but is bound to nothing,
// "/ai" followed by a line does the same.

// openNotesMax bounds the first words kept as the to-do's notes, in characters.
const openNotesMax = 4000

// isTopicRoot says whether ev is the message that starts its topic: it is in a topic (it has a thread_id) and either
// replies to nothing (no root_id) or is its own root (root_id is its own message id). Checked with the message API on
// a real topic group, not with the event push (the two carry the same fields): the first message has an empty
// root_id and parent_id and a thread_id, a reply has both set. The second form is kept as a second accepted shape.
// A wrong guess either opens nothing (the owner sends "/ai") or opens a to-do for a reply.
func isTopicRoot(ev botcard.MessageEvent) bool {
	m := ev.Message
	return m.ThreadID != "" && (m.RootID == "" || m.RootID == m.MessageID)
}

// aiCommand is the line after "/ai" (or the full-width "／ai", whatever its case) when text starts with it as a word of
// its own ("/ai fix it", "/ai", but not "/aim"); ok is false for any other text.
func aiCommand(text string) (rest string, ok bool) { return botcard.AICommand(text) }

// openingWords are the owner's words in ev as an opening reads them: only a text or a rich-text message has any (a
// voice message, a shared group or card, a location, a card, a file and a message of a type not known come as a
// placeholder like "[语音消息]", which are no words); the bot's @ at the start is taken off when separated by whitespace
// (in a group where the bot only gets what @s it, every message starts with one; content has it as the name, the
// message's own text as @_user_N), and an @ further in is the person's name when the message's mentions give it, else
// left as it stands. A rich-text message of only pictures or videos has no words either. "" when there are none.
func openingWords(ev botcard.MessageEvent, content botcard.Content) string {
	return extractOpeningWords(ev, content, false)
}

// openingCommandWords extracts the owner's words for command parsing (/ai, /read, /noreply): for rich-text posts it
// strips leading structured at elements through their mention IDs so an adjacent command keeps its boundary (#12668),
// without removing mentions that touch punctuation in ordinary topic-opening notes and quotes (#12932).
func openingCommandWords(ev botcard.MessageEvent, content botcard.Content) string {
	return extractOpeningWords(ev, content, true)
}

func extractOpeningWords(ev botcard.MessageEvent, content botcard.Content, structuredPostMentions bool) string {
	m := ev.Message
	var text string
	switch m.MessageType {
	case "text":
		text = botcard.ParseContent(m.MessageType, m.Content, nil).Text // with the @_user_N keys still in it
		text = trimOpeningMentions(text, m.Mentions, unicode.IsSpace)
	case "post":
		if structuredPostMentions {
			text = content.StripLeadingMentions(m.Mentions, nil)
		} else {
			if stripped := content.StripLeadingMentions(m.Mentions, nil); !hasOpeningWords(stripped) {
				return ""
			}
			text = trimOpeningMentions(content.Text, m.Mentions, unicode.IsSpace)
		}
	default:
		return ""
	}
	for _, mn := range m.Mentions {
		if mn.Key != "" && mn.Name != "" {
			// A whole key: "@_user_1" is not the start of "@_user_10".
			text = regexp.MustCompile(regexp.QuoteMeta(mn.Key)+`\b`).ReplaceAllLiteralString(text, "@"+mn.Name)
		}
	}
	if !hasOpeningWords(text) {
		return ""
	}
	return text
}

func hasOpeningWords(text string) bool {
	// The placeholders botcard.ParseContent writes for a picture and a video in rich text.
	return strings.TrimSpace(strings.NewReplacer("[图片]", "", "[视频]", "").Replace(text)) != "" // i18n-ignore: matched against the text parsePost writes
}

func trimOpeningMentions(text string, mentions []botcard.Mention, separator func(rune) bool) string {
	text = strings.TrimLeftFunc(strings.TrimSpace(text), separator)
	for cut := true; cut; {
		cut = false
		for _, mn := range mentions {
			for _, tok := range []string{mn.Key, "@" + mn.Name} {
				if tok == "" || tok == "@" {
					continue
				}
				// A whole word: "@_user_1" is not the start of "@_user_10".
				if rest, ok := strings.CutPrefix(text, tok); ok {
					r, _ := utf8.DecodeRuneInString(rest)
					if rest == "" || separator(r) {
						text, cut = strings.TrimLeftFunc(rest, separator), true
					}
				}
			}
		}
	}
	return text
}

var (
	// Media placeholders emitted by Lark, and common attachment/link icons in copied text.
	topicAttachment = lazyre.New(`(?i)^(?:\[(?:图片|视频|文件|附件|image|video|file|attachment)(?:[:：][^\]\r\n]*)?\]|[📎🔗]\x{FE0F}?)`) // i18n-ignore: incoming message text
	topicURL        = lazyre.New(`(?i)^<?(?:https?://|www\.)[^\s\p{Z}<>，。！？；、]+>?`)
	topicFilename   = lazyre.New(`^[^\s\p{Z}/\\]+\.[A-Za-z0-9]{1,8}[\s\p{Z}]+`)
)

// openingTitleWords chooses the temporary display name. The full parsed message still supplies
// the notes, the handoff and the model's later title.
func openingTitleWords(text string, content botcard.Content, mentions []botcard.Mention) (words string, linkLabel bool) {
	if stripped := content.StripLeadingMentions(mentions, nil); stripped != content.Text && (text == content.Text || text == stripped) {
		text = trimOpeningMentions(stripped, mentions, topicSeparator)
	} else if strings.HasPrefix(text, "@") {
		text = trimOpeningMentions(text, mentions, topicSeparator)
	}
	for text != "" {
		if label, rest, ok := content.CutPostLink(text); ok {
			label = strings.TrimSpace(label)
			if label != "" && !topicURL.MatchString(label) && topicAttachment.FindString(label) != label && !genericTopicRequest(label) {
				return label, true
			}
			text = trimOpeningMentions(rest, mentions, topicSeparator)
			continue
		}
		prefix := topicAttachment.FindString(text)
		if prefix == "" {
			prefix = topicURL.FindString(text)
		}
		if prefix == "" {
			// A filename can itself be the subject ("slides.pdf needs edits"). Skip it only beside a shared resource.
			if name := topicFilename.FindString(text); name != "" {
				rest := text[len(name):]
				if _, _, link := content.CutPostLink(rest); link || topicURL.MatchString(rest) || topicAttachment.MatchString(rest) {
					prefix = name
				}
			}
		}
		if prefix == "" {
			break
		}
		text = trimOpeningMentions(text[len(prefix):], mentions, topicSeparator)
	}
	if genericTopicRequest(text) {
		return "", false
	}
	return text, false
}

func topicSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",;:，；：、", r) // i18n-ignore: punctuation in the owner's incoming words, matched as written
}

// Match a whole request, so "Have a look at the seed order" keeps its subject.
func genericTopicRequest(text string) bool {
	if !strings.ContainsFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
		return true
	}
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!?。！？")) { // i18n-ignore: punctuation in the owner's incoming words, matched as written
	case "have a look", "take a look", "please have a look", "please take a look",
		"看看", "看一下", "看下", "帮我看看", "请看一下", "見てください", "確認してください": // i18n-ignore: the owner's incoming words
		return true
	}
	return false
}

// openTopicIfAsked opens a to-do for the message when it asks for one: it starts a topic that is bound to nothing, or
// it is "/ai …" in such a topic. It says whether it took the message (everything the owner is to be told is said).
func (s core) openTopicIfAsked(ctx context.Context, ev botcard.MessageEvent, st FeishuStatus, content botcard.Content, topic *feishuTopic) bool {
	rest, asked := aiCommand(openingCommandWords(ev, content))
	if !asked && !isTopicRoot(ev) {
		return false
	}
	text := rest
	if !asked {
		text = openingWords(ev, content)
	}
	s.openTopicTodo(ctx, ev, st, content, text, topic)
	return true
}

// openTopicTodo makes the to-do for the topic ev is in, with text (the first words, the command taken off) as its
// notes, and hands the words to the main conversation (openTodoIn); what keeps it from doing so is said in the topic.
func (s core) openTopicTodo(ctx context.Context, ev botcard.MessageEvent, st FeishuStatus, content botcard.Content, text string, topic *feishuTopic) {
	if _, fail := s.openTodoIn(ctx, ev, st, content, text, "", topic, nil); fail != "" {
		s.botNoteTo(ctx, topic.target(), ev.Message.MessageID, fail)
	}
}

// openTodoIn makes the to-do for the topic, with text (the first words, the command taken off) as its title and notes,
// and hands the words to the main conversation. Who takes it is worked out before anything is made, and a topic that
// turns out to be taken (the core's db.ErrThreadTaken) leaves no to-do behind. The first words are not a decision: the to-do is made
// of them. ev is the message the run answers under in the topic: the owner's own, or the one the bot opened it with
// (openFromElsewhere). quote, when not empty, is the text of a message the words reply to: it follows them in the notes
// and in the hand-over, as context. src, when given, is where the request came from, recorded on the to-do. A nil topic
// is a to-do bound to no topic (a request taken in the owner's private chat, openInOwnerChat): ev is then a message of that
// chat, and what is said goes there. todo is the to-do's id; fail says why no to-do was made, in the bot's language, ""
// when one was; what goes wrong after the to-do exists is said in the topic (or the private chat).
func (s core) openTodoIn(ctx context.Context, ev botcard.MessageEvent, st FeishuStatus, content botcard.Content, text, quote string,
	topic *feishuTopic, src *assistantwire.ChatTodoSource) (todo, fail string) {
	msg := ev.Message
	l := s.botLang(ctx)
	to := topic.target()
	where := "private chat" // for the log
	if topic != nil {
		where = "topic " + topic.Thread
	}
	say := func(key string, args ...i18n.Args) {
		s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, "assistant-feishu."+key, args...))
	}
	failed := func(key string, err error) string {
		return i18n.T(l, "assistant-feishu."+key, i18n.Args{"error": clip(oneLine(err.Error()), 200)})
	}
	if text == "" {
		// A to-do is named after what was said: a picture, a merged forward, a voice message or the like alone has no
		// words to name it by.
		return "", i18n.T(l, "assistant-feishu.thread_open_needs_text")
	}
	target, origin, why := s.h.OpenTarget(ctx, st)
	if why != "" {
		return "", why
	}
	// What the owner said, and apart from it what someone else said in the message it replied to: the notes carry both, the
	// second under a label that says whose it is and with nothing in it that closes a context block.
	words := text
	if quote != "" {
		words += "\n\n" + i18n.T(l, "assistant-feishu.elsewhere_quote_label") + "\n" + prompts.ContextBody(quote)
	}
	titleWords, linkLabel := text, false
	if topic != nil && src == nil {
		titleWords, linkLabel = openingTitleWords(text, content, msg.Mentions)
		if titleWords == "" {
			titleWords = i18n.T(l, "assistant-tool_titles.todo_create")
		}
	}
	title, retitle := s.h.TodoTitle(ctx, titleWords, l, linkLabel)
	if src != nil && src.Anchor == "" {
		src.Handoff = msg.MessageID
	}
	// The to-do is made, bound to the topic, given its source, supervised by the main conversation and shown in its
	// feed by the core; anything that fails there takes the to-do back.
	todo, err := s.h.OpenTodo(ctx, assistantwire.ChatOpenTodo{Title: title, Notes: clip(words, openNotesMax), Origin: origin,
		Topic: topic != nil, ChatID: topicChat(topic), ThreadID: topicThread(topic), Source: src, Retitle: retitle, Text: text, Where: where})
	if err != nil {
		return "", failed("thread_open_failed", err)
	}
	s.logf("bots: %s opened todo %s", where, todo)

	line := i18n.T(l, "assistant-feishu.thread_open_done", i18n.Args{"title": title})
	if link := s.h.TodoLink(strings.TrimRight(st.PublicURL, "/"), todo); isWebLink(link) {
		line += "\n" + link
	}
	s.botNoteTo(ctx, to, msg.MessageID, line)

	content.Text = prompts.RenderTodoThreadOpen(prompts.TodoThreadOpen{Language: s.h.AnswerLanguage(s.hello(ctx).Locale), Title: contextText(clip(oneLine(title), 80)), TodoID: todo, Text: text, Quote: quote, NoTopic: topic == nil})
	if err := s.botSendIn(ctx, &ev, content, target, topic); err != nil {
		s.logf("bots: %s: todo %s to the main conversation: %v", where, todo, err)
		say("thread_open_not_sent", i18n.Args{"error": clip(oneLine(err.Error()), 200)})
	}
	return todo, ""
}

// topicChat and topicThread are a topic's chat and thread, "" for none.
func topicChat(t *feishuTopic) string {
	if t == nil {
		return ""
	}
	return t.Chat
}

func topicThread(t *feishuTopic) string {
	if t == nil {
		return ""
	}
	return t.Thread
}

// isWebLink: link is an http(s) address with a host (what a message can carry as a link; "" is none).
func isWebLink(link string) bool {
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
