package assistantwire

import (
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- what the Lark workbench reads of the assistant (internal/modules/lark/workbench.go, #13404) ----

// WorkbenchItem is an Inbox item as the workbench's cards show it.
type WorkbenchItem struct {
	ID, Source, AccountName, Title, Sender string
	Preview, Summary, Recap                string
	Tier                                   string // TierAction "action", "fyi", "noise"
	Urgent                                 bool
	UpdatedAt                              time.Time
	Done                                   bool // filed (done or dismissed)
	// ReplyVersion binds a rendered draft card to the exact current AI reply mirror; zero is a transient legacy draft.
	ReplyVersion int
	// Mail, Lark and Connector say where it came from; a connector's thread has its label, whether the owner can reply
	// to it, and where a reply goes.
	Mail, Lark, Connector bool
	ConnectorLabel        string
	ConnectorCanReply     bool
	ConnectorReplyTo      string
	// ReplySuggestionVersion and ReplySuggestionText bind a rendered draft card to the exact current reply suggestion.
	// They contain display data only; authorization snapshots stay inside the assistant.
	ReplySuggestionVersion int
	ReplySuggestionText    string
	// Mutes are what it can be muted by.
	Mutes []WorkbenchMute
}

// WorkbenchMute is one of an item's mutes: nothing of its source tagged Tag = Value stays in the Inbox.
type WorkbenchMute struct{ Tag, Value, Name string }

// WorkbenchCounts are what the home card sums up of the agents and the computers.
type WorkbenchCounts struct {
	Working, Unread, Idle int // agents by state
	Pending               int // questions and approvals waiting
	Online, Machines      int
}

// WorkbenchAgent is an agent at work, as the home card lists it: its computer and its session.
type WorkbenchAgent struct {
	Machine, MachineName string
	Session              proto.Session
}

// WorkbenchBoardRow is one thing the standing board says waits for the owner: who or what (From), in one line, how
// long it waited in words, where it opens (Link, "" when no web link fits a card), and Blocking when it holds work up
// (its row brings its card down; ID names it).
type WorkbenchBoardRow struct {
	ID, From, Summary, Waited, Link string
	Blocking                        bool
}

// WorkbenchCompanion is a companion bot the home card links to; Inbox marks the Inbox's own.
type WorkbenchCompanion struct {
	Name, Link string
	Inbox      bool
}

// WorkbenchMeeting is the next meeting of the day.
type WorkbenchMeeting struct {
	Start time.Time
	Title string
}
