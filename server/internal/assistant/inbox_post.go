package assistant

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The assistant's own Inbox source (#6430, docs/assistant/inbox.md "Assistant rows"): a row the assistant puts into the
// Inbox for one of its conversations, so a result is not missed behind a quiet unread dot. Only the user's own request
// or a task they set makes one (the tool's description says so; a task's `inbox` is the user's setting). The source is
// internal, like a push subscription's: the manager writes the rows itself and no connector or hub reads anything.
//
// A row is the generic connector block the apps already draw for a source they do not know (thread, label, the
// conversation's page as url), so an older app shows a normal row and opens the conversation. One conversation has
// one row: a post within inboxPostWindow of the row's last one updates it (and opens it again when handled); a later
// one starts a new row.
const (
	inboxPostSource = "assistant"
	inboxPostKind   = "assistant_post"
	inboxPostWindow = 30 * time.Minute
	// A post's fields are cut like the Inbox's own: a title and a line for the list, a summary for the detail.
	inboxPostTitleMax, inboxPostSummaryMax, inboxPostReasonMax = 80, 600, 120
)

// A task's inbox setting: what of its runs goes into the Inbox.
const (
	inboxOff    = ""       // nothing (the default: a task from before this keeps behaving as it did)
	inboxWorthy = "worthy" // a run puts a row there itself (inbox_post) when it finds something worth a look
	inboxEvery  = "every"  // every run ends with a row, made by the manager
)

// inboxMode is the task's setting as stored.
func (t Task) inboxEvery() bool  { return t.Inbox == inboxEvery }
func (t Task) inboxWorthy() bool { return t.Inbox == inboxWorthy }

// checkInbox says whether m is an inbox setting the app may save.
func checkInbox(m string) error {
	switch m {
	case inboxOff, inboxWorthy, inboxEvery:
		return nil
	}
	return fmt.Errorf("放进收件箱 %q 不认识（\"\" 不放 / \"worthy\" 有值得看的才放 / \"every\" 每次都放）", m) // i18n-ignore: an error for the app's log
}

// inboxArg is a scheduling tool's inbox argument: "off" (or empty), "worthy" or "every".
func inboxArg(a string) (string, error) {
	switch a {
	case "", "off":
		return inboxOff, nil
	case inboxWorthy, inboxEvery:
		return a, nil
	}
	return "", fmt.Errorf("inbox %q: use \"off\" (nothing goes into the Inbox), \"worthy\" (a run puts a row there with inbox_post only when it finds something worth a look) or \"every\" (every run ends with a row)", a)
}

// inboxIn reads inbox off a request body: has says it was there. null or anything but a string is refused (400).
func inboxIn(raw map[string]json.RawMessage) (mode string, has bool, err error) {
	v, ok := raw["inbox"]
	if !ok {
		return "", false, nil
	}
	if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return "", true, errors.New(`放进收件箱不能是 null（"" 不放 / "worthy" / "every"）`) // i18n-ignore: an error for the app's log
	}
	if err := json.Unmarshal(v, &mode); err != nil {
		return "", true, fmt.Errorf("放进收件箱要是字符串: %v", err) // i18n-ignore: an error for the app's log
	}
	if mode == "off" {
		mode = inboxOff
	}
	return mode, true, checkInbox(mode)
}

// inboxModeText is how list_followups and the tools' answers say a task's inbox setting.
func inboxModeText(t Task) string {
	switch t.Inbox {
	case inboxWorthy:
		return "puts a row in the user's Inbox only when a run finds something worth a look (inbox_post)"
	case inboxEvery:
		return "every run ends with a row in the user's Inbox"
	}
	return "nothing goes into the Inbox"
}

// inboxPostOn: the assistant may put rows into the Inbox (the Inbox Sources page's switch; on unless turned off).
func (s *Service) inboxPostOn(ctx context.Context) bool {
	v := s.settings(ctx).InboxPost
	return v == nil || *v
}

// inboxPostArgs are inbox_post's arguments.
type inboxPostArgs struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Tier    string `json:"tier"`
	Reason  string `json:"reason"`
}

// toolInboxPost puts the conversation the call comes from into the user's Inbox (prompts/mcp/inbox/inbox_post.md).
func (s *Service) toolInboxPost(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	var a inboxPostArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Summary) == "" {
		return "", errors.New("title and summary are required")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return "", errors.New("reason is required: say which request of the user's (or which task setting) makes this post")
	}
	switch a.Tier {
	case "", TierFYI, TierAction:
	default:
		return "", fmt.Errorf("tier %q: use \"action\" (needs the user) or \"fyi\" (worth knowing)", a.Tier)
	}
	if s.justMeNow(machine, self) || s.inHealth(ctx, self) {
		// Like notify_user: a meeting's side turn and a health conversation put nothing where the lists can show it.
		return "", errors.New("this conversation can't be put into the Inbox")
	}
	if !s.inboxPostOn(ctx) {
		return "The user turned off \"Put assistant results in the Inbox\" (Inbox Sources), so nothing was added. If it matters, say so in this conversation.", nil
	}
	posted, err := s.postToInbox(ctx, machine, self, a, time.Now())
	if err != nil {
		return "", err
	}
	if posted.updated {
		return "The Inbox row for this conversation was updated.", nil
	}
	return "Added to the user's Inbox: it opens this conversation.", nil
}

// inboxPosted is what a post did.
type inboxPosted struct {
	id      string
	updated bool
}

// postToInbox puts machine's conversation session into the Inbox. Under the hood it is a generic connector row of the
// source "assistant", thread = the conversation. A post within inboxPostWindow of the conversation's row updates that
// row, reopened when it was handled; any other makes a new one. The user's own policy for the source still applies.
func (s *Service) postToInbox(ctx context.Context, machine, session string, a inboxPostArgs, now time.Time) (inboxPosted, error) {
	if err := s.inboxReady(ctx); err != nil {
		return inboxPosted{}, err
	}
	title := clip(oneLine(strings.TrimSpace(a.Title)), inboxPostTitleMax)
	summary := clip(oneLine(strings.TrimSpace(a.Summary)), inboxPostSummaryMax)
	tier := a.Tier
	if tier == "" {
		tier = TierFYI
	}
	l := s.inbox.userLang()
	label := i18n.T(l, "assistant-inbox_post.label")
	link := ""
	if web := s.webBase(ctx); web != "" { // an older app opens the row's url in its link card; without an address it has none
		link = sessionLink(web, machine, session, true)
	}
	thread := machine + "/" + session
	sum := sha1.Sum([]byte(thread + "\n" + now.UTC().Format(time.RFC3339Nano)))
	thing := connector.InboxThing{Thread: thread, Kind: inboxPostKind, Label: label, Title: title,
		Preview: clip(summary, 120), Context: title + "\n" + summary, At: now, Last: hex.EncodeToString(sum[:8]), URL: link,
		Tier: connector.Tier(tier), Why: clip(oneLine(strings.TrimSpace(a.Reason)), inboxPostReasonMax)}

	b := &s.inbox
	b.mu.Lock()
	var open *InboxItem
	for _, it := range b.items {
		if it.Source == inboxPostSource && it.Connector != nil && it.Connector.Thread == thread &&
			(open == nil || newerItem(it, open)) {
			open = it
		}
	}
	var w *inboxWrite
	var it *InboxItem
	updated := open != nil && now.Sub(open.UpdatedAt) <= inboxPostWindow
	if updated {
		it = open
		w = beginWrite(it)
		prev := seenOf(it)
		fillConnectorItem(it, thing)
		it.DoneAt, it.DoneBy, it.Read = nil, "", false
		b.noteReceived(it, prev, nil)
	} else {
		it = &InboxItem{ID: inboxPostSource + ":" + hex.EncodeToString(sum[:8]), Source: inboxPostSource, Account: inboxPostSource,
			AccountName: label, StartedAt: now, Count: 1}
		fillConnectorItem(it, thing)
		b.noteReceived(it, inboxSeen{}, nil)
		b.items[it.ID] = it
		w = newWrite(it)
	}
	it.Summary, it.TriagedBy, it.Urgent = summary, "rules", false
	b.noteTriaged(it)
	b.applyPolicyTier(it) // the user's own rule for the source wins over what the post asked
	b.settle(w)
	b.mu.Unlock()
	if failed := s.saveWrites(ctx, []*inboxWrite{w}, "an assistant post"); failed[it.ID] {
		return inboxPosted{}, errInboxNotPersisted
	}
	return inboxPosted{id: it.ID, updated: updated}, nil
}

// inboxPostEvery ends a run of a task set to "every": its conversation (machine, session) just finished a turn, and the
// row says what the run answered. A run that failed or answered nothing puts nothing there (the failure is told as ever).
func (s *Service) inboxPostEvery(ctx context.Context, machine, session string) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	var task *Task
	for i := range sc.tasks {
		if t := sc.tasks[i]; t.inboxEvery() && (t.RunSessionID == session || t.SessionID == session) {
			task = &t
			break
		}
	}
	sc.mu.Unlock()
	if task == nil || !s.inboxPostOn(ctx) {
		return
	}
	sess, ok := s.session(ctx, machine, session)
	if !ok || sess.Status == proto.StatusWorking || strings.TrimSpace(sess.TurnError) != "" || strings.TrimSpace(sess.LastText) == "" {
		return
	}
	reason := i18n.T(s.inbox.userLang(), "assistant-inbox_post.every_reason")
	if _, err := s.postToInbox(ctx, machine, session, inboxPostArgs{Title: task.labelIn(s.inbox.userLang()),
		Summary: sess.LastText, Tier: TierFYI, Reason: reason}, time.Now()); err != nil {
		s.logf("task %s: Inbox row: %v", task.ID, err)
	}
}
