package linktray

import (
	"errors"

	"github.com/nwverse/persona/server/internal/link"
)

// Host is what the tray asks of the program it runs in (cmd/persona-link): reading the state and doing what the
// panel's buttons say. The tray never touches link.json, the scheduled task or the hub itself, so the command line
// and the tray do each thing one way.
type Host interface {
	// Snapshot reads link.json, link.status.json and whether persona-link is alive. Cheap: called every second
	// while the panel is open. Task is left empty: TaskState fills it.
	Snapshot() Snapshot
	// TaskState asks the system about the scheduled task (it runs schtasks, about 50 ms): on opening the panel,
	// after an action and every half minute.
	TaskState() Task
	// Version is persona-link's version, shown at the bottom of the panel.
	Version() string
	// DataDir is the directory link.json and the logs live in: the tray watches it for changes.
	DataDir() string

	Reconnect() error
	RestartService() error
	InstallService() error
	// Pair redeems an active-mode pairing link (over ssh when non-nil) and says what this computer is called on the hub.
	Pair(raw, machine string, ssh *link.SSH) (string, error)
	// SetSSH saves or removes (nil) the SSH tunnel in link.json and asks a running persona-link to redial.
	SetSSH(ssh *link.SSH) (*link.SSH, error)
	// TestSSH checks that ssh reaches a Persona hub (against rawLink when given, else link.json).
	TestSSH(ssh link.SSH, rawLink string) (*link.SSH, error)
	// PairListen makes a passive-mode pairing link for the hub's admin page.
	PairListen(listen string) (Invitation, error)
	SetMode(mode string) error
	AddAddress(raw string) error
	RemoveAddress(url string) error
	// Rename asks the hub, through the running persona-link, to call this computer name ("": its id again), and says
	// what it calls it now; a *link.RenameError says why not.
	Rename(name string) (string, error)
}

// What a Host's errors can be beyond their text: the tray says these in the user's language (ErrorText).
var (
	// ErrNoService: there is no scheduled task to restart.
	ErrNoService = errors.New("the background service is not installed")
	// ErrInstallInTerminal: this program is the installed copy and can't install itself; a terminal can.
	ErrInstallInTerminal = errors.New("run persona-link install in a terminal")
)

// PairError is a pairing that failed in a way the person can fix: the hub refused the link, or no hub answered at
// Hosts. Err is the failure as it was.
type PairError struct {
	Rejected bool
	Hosts    []string
	Err      error
}

func (e *PairError) Error() string { return e.Err.Error() }
func (e *PairError) Unwrap() error { return e.Err }

// Invitation is a passive-mode pairing link and what the window says with it.
type Invitation struct {
	Link     string   `json:"link"`
	Listen   string   `json:"listen"`
	Addrs    []string `json:"addrs"`
	AddrText string   `json:"addr_text"`
	Firewall string   `json:"firewall"` // the PowerShell line that opens the port to the hub
}
