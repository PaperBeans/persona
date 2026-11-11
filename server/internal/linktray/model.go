// Package linktray is the Windows tray for Persona Link (docs/operations/windows.md "Tray"): what persona-link and the scheduled
// task say, turned into the tray icon, the panel's lines and its actions. This file is the part every platform
// compiles and tests: the window, the icon in the notification area and the WebView2 panel are in *_windows.go.
// It is the Windows counterpart of the Mac menu bar app's LinkModel.look and StatusBlock
// (apple/PersonaLink/Sources/PersonaLink), and keeps their order of lines.
package linktray

import (
	"cmp"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// Look is what the tray icon shows: the link's four states, a link that is up while the node is not or while the hub
// waits for the user to confirm this computer, and passive mode listening for the hub (Mac: LinkLook).
type Look string

const (
	LookConnected       Look = "connected"
	LookAwaitingConfirm Look = "awaiting_confirm"
	LookNodeMissing     Look = "node_missing"
	LookConnecting      Look = "connecting"
	LookListening       Look = "listening"
	LookDown            Look = "down"
	LookUnpaired        Look = "unpaired"
)

// Looks are all of them, in the order the snapshots draw them.
var Looks = []Look{LookConnected, LookAwaitingConfirm, LookNodeMissing, LookConnecting, LookListening, LookDown, LookUnpaired}

// Tones color a line, the status pill and the icon's badge; the panel maps them to its tokens.
const (
	TonePositive = "positive"
	ToneCaution  = "caution"
	ToneInfo     = "info"
	ToneDanger   = "danger"
	ToneMuted    = "" // secondary text, no color
)

// Tone is the look's color: green up, orange when something needs the user, blue on the way, red down.
func (l Look) Tone() string {
	switch l {
	case LookConnected:
		return TonePositive
	case LookAwaitingConfirm, LookNodeMissing:
		return ToneCaution
	case LookConnecting, LookListening:
		return ToneInfo
	case LookDown:
		return ToneDanger
	}
	return ToneMuted
}

// Task is the scheduled task "Persona Link" that keeps persona-link and the node running.
type Task struct {
	Installed bool   `json:"installed"`
	State     string `json:"state,omitempty"` // as schtasks words it, in the system's language; shown, never parsed
}

// Snapshot is everything the tray knows at one moment, read from disk and the system, never by connecting to
// anything: link.json, link.status.json, whether the persona-link that wrote it is alive, the scheduled task.
type Snapshot struct {
	Now time.Time
	// Running: the persona-link that wrote Status is alive (its pid, checked without its run lock).
	Running bool
	Status  *link.SavedStatus
	Config  *link.Config // nil before anything was paired
	Task    Task
	// Autostart: the tray starts at sign-in.
	Autostart bool
}

// Mode is the mode the link is in: what the running persona-link says, else what link.json says.
func (s Snapshot) Mode() string {
	if s.Running && s.Status != nil && s.Status.Mode != "" {
		return s.Status.Mode
	}
	return s.Config.CurrentMode()
}

// Paired says whether the current mode has a pairing to use.
func (s Snapshot) Paired() bool {
	c := s.Config
	if c == nil {
		return false
	}
	if s.Mode() == link.ModePassive {
		return c.Passive.Paired()
	}
	return c.Token != "" && c.Machine != "" && (c.FP != "" || c.Trust == link.TrustSystem)
}

// live is the status of a running persona-link; nil when none is running or it has said nothing yet.
func (s Snapshot) live() *link.Status {
	if !s.Running || s.Status == nil || s.Status.State == "stopped" {
		return nil
	}
	return &s.Status.Status
}

// LookOf is the icon's state (Mac: LinkModel.look). With no persona-link running it is down, unless there is nothing
// paired to run with: then what to do is pair.
func LookOf(s Snapshot) Look {
	st := s.live()
	if st == nil {
		if s.Running && s.Status == nil {
			return LookConnecting
		}
		if !s.Paired() {
			return LookUnpaired
		}
		return LookDown
	}
	switch st.State {
	case "connected", "listening":
		switch {
		case st.Pending:
			return LookAwaitingConfirm
		case st.Node == "offline":
			return LookNodeMissing
		case st.State == "connected":
			return LookConnected
		}
		return LookListening
	case "connecting", "":
		return LookConnecting
	case "unpaired":
		return LookUnpaired
	}
	return LookDown
}

// Line is one line of the panel's status: an icon from the panel's set, the text, and whether it is the line that
// matters most (bigger, primary color).
type Line struct {
	Icon    string `json:"icon"`
	Text    string `json:"text"`
	Primary bool   `json:"primary,omitempty"`
	Tone    string `json:"tone,omitempty"`
}

// Action ids, the same in the panel, the right-click menu and the bridge.
const (
	ActReconnect = "reconnect"
	ActPair      = "pair"
	ActRestart   = "restart_service"
	ActInstall   = "install_service"
	ActLogs      = "open_logs"
	ActAutostart = "autostart"
	ActQuit      = "quit"
	ActPanel     = "open_panel"
	// ActRename opens the panel's name field; the page saves it with the "rename" command (Host.Rename).
	ActRename = "rename"
)

// Action is a row of the panel.
type Action struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	Disabled bool   `json:"disabled,omitempty"`
	Checked  *bool  `json:"checked,omitempty"` // a switch: on or off
	Hint     string `json:"hint,omitempty"`
}

// View is the panel, the icon and its tooltip at one moment, in one language.
type View struct {
	Look    Look     `json:"look"`
	Tone    string   `json:"tone"`
	Pill    string   `json:"pill"`
	Tooltip string   `json:"tooltip"`
	Lines   []Line   `json:"lines"`
	Actions []Action `json:"actions"`
	Mode    string   `json:"mode"`
	// Name is what the hub calls this computer now (its id with older hubs), the rename field's first text.
	Name string `json:"name,omitempty"`
}

// Build turns a snapshot into what the tray shows, in language l.
func Build(l i18n.Lang, s Snapshot) View {
	look := LookOf(s)
	v := View{Look: look, Tone: look.Tone(), Pill: pill(l, look), Mode: s.Mode(), Lines: lines(l, s)}
	v.Tooltip = "Persona Link · " + v.Pill
	if name := hubName(s); name != "" {
		v.Tooltip += " · " + name
	}
	v.Actions = actions(l, s, look)
	v.Name = machineName(s)
	return v
}

// machineName is what the hub calls this computer in the current mode, as far as anything here knows it: the
// running persona-link's word, else link.json's; the id when the hub has said nothing (an older hub).
func machineName(s Snapshot) string {
	if st := s.live(); st != nil && st.MachineName != "" {
		return st.MachineName
	}
	c := s.Config
	switch {
	case c == nil:
		return ""
	case s.Mode() == link.ModePassive && c.Passive != nil:
		return cmp.Or(c.Passive.MachineName, c.Passive.Machine)
	case s.Mode() == link.ModePassive:
		return ""
	}
	return cmp.Or(c.MachineName, c.Machine)
}

// renamable: the panel offers to rename this computer: active mode, connected to a hub that says what it calls it
// (one that renames computers), and confirmed (a pending computer cannot rename itself).
func renamable(s Snapshot) bool {
	st := s.live()
	return st != nil && st.Mode != link.ModePassive && st.State == "connected" && st.MachineName != "" && !st.Pending
}

func pill(l i18n.Lang, look Look) string {
	switch look {
	case LookConnected:
		return i18n.T(l, "link-tray.look_connected")
	case LookAwaitingConfirm:
		return i18n.T(l, "link-tray.look_awaiting")
	case LookNodeMissing:
		return i18n.T(l, "link-tray.look_node_missing")
	case LookConnecting:
		return i18n.T(l, "link-tray.look_connecting")
	case LookListening:
		return i18n.T(l, "link-tray.look_listening")
	case LookDown:
		return i18n.T(l, "link-tray.look_down")
	}
	return i18n.T(l, "link-tray.look_unpaired")
}

// hubName is the hub this computer is paired with in the current mode, as far as anything here knows it.
func hubName(s Snapshot) string {
	if st := s.live(); st != nil && st.HubName != "" {
		return st.HubName
	}
	if c := s.Config; c != nil {
		if s.Mode() == link.ModePassive {
			if c.Passive != nil {
				return c.Passive.HubName
			}
			return ""
		}
		return c.HubName
	}
	return ""
}

// lines is the status block (Mac: StatusBlock.lines). Without a running persona-link it says why and what to do;
// otherwise what the link is doing, which mode second.
func lines(l i18n.Lang, s Snapshot) []Line {
	st := s.live()
	if st == nil {
		return notRunning(l, s)
	}
	if st.Mode == link.ModePassive {
		return passiveLines(l, s, st)
	}
	out := activeLines(l, s, st)
	if st.State != "unpaired" && len(out) > 0 {
		// Third when the second says what to do about the first (confirm on the phone, restart the service).
		at := 1
		if st.State == "connected" && (st.Pending || st.Node == "offline") {
			at = 2
		}
		at = min(at, len(out))
		out = append(out[:at], append([]Line{{Icon: "arrow_out", Text: i18n.T(l, "link-tray.mode_active")}}, out[at:]...)...)
	}
	return out
}

func notRunning(l i18n.Lang, s Snapshot) []Line {
	switch {
	case s.Running: // alive, nothing reported yet
		return []Line{{Icon: "hourglass", Text: i18n.T(l, "link-tray.starting"), Primary: true}}
	case !s.Task.Installed:
		out := []Line{{Icon: "warning", Text: i18n.T(l, "link-tray.service_missing"), Primary: true, Tone: ToneCaution},
			{Icon: "info", Text: i18n.T(l, "link-tray.service_missing_hint")}}
		if !s.Paired() {
			out = append(out, Line{Icon: "link", Text: i18n.T(l, "link-tray.unpaired_hint")})
		}
		return out
	case !s.Paired():
		return unpairedLines(l, s.Mode())
	}
	out := []Line{{Icon: "octagon", Text: i18n.T(l, "link-tray.link_not_running"), Primary: true, Tone: ToneDanger},
		{Icon: "info", Text: i18n.T(l, "link-tray.link_not_running_hint")}}
	if s.Task.State != "" {
		out = append(out, Line{Icon: "gear", Text: i18n.T(l, "link-tray.task_state", i18n.Args{"state": s.Task.State})})
	}
	return out
}

func unpairedLines(l i18n.Lang, mode string) []Line {
	if mode == link.ModePassive {
		return []Line{{Icon: "link", Text: i18n.T(l, "link-tray.passive_unpaired"), Primary: true},
			{Icon: "info", Text: i18n.T(l, "link-tray.passive_code_hint")}}
	}
	return []Line{{Icon: "link", Text: i18n.T(l, "link-tray.unpaired_title"), Primary: true},
		{Icon: "info", Text: i18n.T(l, "link-tray.unpaired_hint")}}
}

func machineLine(l i18n.Lang, name string) []Line {
	if name == "" {
		return nil
	}
	return []Line{{Icon: "laptop", Text: i18n.T(l, "link-tray.machine", i18n.Args{"name": name})}}
}

func hubLine(l i18n.Lang, name string, mesh int) []Line {
	if name == "" {
		return nil
	}
	return []Line{{Icon: "server", Text: HubText(l, name, mesh)}}
}

// failures is one line per address persona-link tried: it joins their errors with a full-width semicolon.
func failures(text string) []Line {
	var out []Line
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == '；' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, Line{Icon: "x", Text: part})
		}
	}
	return out
}

func activeLines(l i18n.Lang, s Snapshot, st *link.Status) []Line {
	machine := machineLine(l, cmp.Or(st.MachineName, st.Machine))
	mesh := 0
	if st.State == "connected" {
		mesh = st.MeshHubs
	}
	hub := hubLine(l, st.HubName, mesh)
	address := Line{Icon: "network", Text: i18n.T(l, "link-tray.address_line", i18n.Args{"kind": KindText(l, st.Kind), "host": HostText(st.Address)})}
	switch st.State {
	case "connected":
		if st.Pending {
			// Paired from a link the phone or the web made: the hub keeps this computer out until it is confirmed there.
			out := []Line{{Icon: "hourglass", Text: i18n.T(l, "link-tray.awaiting_title"), Primary: true, Tone: ToneCaution},
				{Icon: "phone", Text: i18n.T(l, "link-tray.awaiting_hint")}, address}
			return append(append(out, hub...), machine...)
		}
		out := []Line{{Icon: "check_filled", Text: i18n.T(l, "link-tray.connected_title"), Primary: true, Tone: TonePositive}, address}
		if st.Trust == link.TrustSystem {
			out = append(out, Line{Icon: "shield", Text: i18n.T(l, "link-tray.system_trust")})
		}
		out = append(out, hub...)
		if st.Since != nil {
			out = append(out, Line{Icon: "clock", Text: i18n.T(l, "link-tray.connected_for", i18n.Args{"duration": Duration(l, s.Now.Sub(*st.Since))})})
		}
		switch st.Node {
		case "online":
			out = append(out, Line{Icon: "check", Text: i18n.T(l, "link-tray.node_online"), Tone: TonePositive})
		case "offline":
			// The first line says it: what is wrong is on this computer, not the link.
			out[0] = Line{Icon: "warning", Text: i18n.T(l, "link-tray.node_offline_title"), Primary: true, Tone: ToneCaution}
			out = append(out[:1], append([]Line{{Icon: "info", Text: i18n.T(l, "link-tray.node_offline_hint")}}, out[1:]...)...)
			if st.NodeError != "" {
				out = append(out, Line{Icon: "info", Text: st.NodeError})
			}
		default:
			out = append(out, Line{Icon: "ellipsis", Text: i18n.T(l, "link-tray.node_waiting")})
		}
		return append(out, machine...)
	case "connecting":
		out := []Line{{Icon: "sync", Text: i18n.T(l, "link-tray.connecting_title"), Primary: true, Tone: ToneInfo}}
		return append(append(out, hub...), machine...)
	case "unpaired":
		return unpairedLines(l, link.ModeActive)
	case "refused":
		text := st.Error
		if text == "" {
			text = i18n.T(l, "link-tray.refused_default")
		}
		out := []Line{{Icon: "lock_off", Text: text, Primary: true, Tone: ToneDanger}}
		return append(append(out, hub...), machine...)
	}
	// waiting
	title := i18n.T(l, "link-tray.offline_title")
	if st.Retry != nil {
		title = i18n.T(l, "link-tray.with_retry", i18n.Args{"state": title, "retry": Until(l, st.Retry.Sub(s.Now))})
	}
	errText := st.Error
	var extra []Line
	if st.SSHError != nil {
		defText := st.SSHError.Text(i18n.Default)
		locText := st.SSHError.Text(l)
		switch {
		case errText == "":
			errText = locText
		case defText != "" && strings.Contains(errText, defText):
			errText = strings.Replace(errText, defText, locText, 1)
		case !strings.Contains(errText, locText):
			extra = []Line{{Icon: "x", Text: locText}}
		}
	}
	out := append(append([]Line{{Icon: "offline", Text: title, Primary: true, Tone: ToneDanger}}, failures(errText)...), extra...)
	return append(append(out, hub...), machine...)
}

// passiveLines: the hub dials this computer. Which mode and where it listens first, then what the hub did last.
func passiveLines(l i18n.Lang, s Snapshot, st *link.Status) []Line {
	listen := st.Listen
	if listen == "" {
		listen = link.DefaultListen
		if s.Config != nil {
			listen = s.Config.Passive.ListenAddr()
		}
	}
	mode := Line{Icon: "arrow_in", Text: i18n.T(l, "link-tray.mode_passive_listen", i18n.Args{"mode": i18n.T(l, "link-tray.mode_passive"), "listen": listen})}
	machine := machineLine(l, cmp.Or(st.MachineName, st.Machine))
	hub := hubLine(l, st.HubName, 0)
	var seen []Line
	if st.LastSeen != nil {
		text := i18n.T(l, "link-tray.passive_last_seen", i18n.Args{"ago": Ago(l, s.Now.Sub(*st.LastSeen))})
		if st.From != "" {
			text = i18n.T(l, "link-tray.passive_last_seen_from", i18n.Args{"ago": Ago(l, s.Now.Sub(*st.LastSeen)), "from": st.From})
		}
		seen = []Line{{Icon: "clock", Text: text}}
	}
	var node []Line
	if st.Node == "offline" {
		node = append(node, Line{Icon: "warning", Text: i18n.T(l, "link-tray.passive_node_offline"), Tone: ToneCaution})
		if st.NodeError != "" {
			node = append(node, Line{Icon: "info", Text: st.NodeError})
		}
	}
	switch st.State {
	case "connected":
		out := []Line{{Icon: "network", Text: i18n.T(l, "link-tray.passive_connected", i18n.Args{"from": st.Address}), Primary: true, Tone: TonePositive}, mode}
		out = append(out, hub...)
		if st.Since != nil {
			out = append(out, Line{Icon: "clock", Text: i18n.T(l, "link-tray.connected_for", i18n.Args{"duration": Duration(l, s.Now.Sub(*st.Since))})})
		}
		switch {
		case st.Node == "online":
			out = append(out, Line{Icon: "check", Text: i18n.T(l, "link-tray.node_online"), Tone: TonePositive})
		case len(node) > 0:
			out = append(out, node[0])
		default:
			out = append(out, Line{Icon: "ellipsis", Text: i18n.T(l, "link-tray.node_waiting")})
		}
		return append(out, machine...)
	case "listening":
		out := []Line{{Icon: "antenna", Text: i18n.T(l, "link-tray.passive_listening"), Primary: true, Tone: ToneInfo}, mode}
		out = append(append(append(out, hub...), seen...), node...)
		return append(out, machine...)
	case "unpaired":
		out := []Line{{Icon: "link", Text: i18n.T(l, "link-tray.passive_unpaired"), Primary: true}, mode}
		if st.CodeExpires != nil && st.CodeExpires.After(s.Now) {
			return append(out, Line{Icon: "hourglass", Text: i18n.T(l, "link-tray.passive_code_left", i18n.Args{"left": Left(l, st.CodeExpires.Sub(s.Now))})})
		}
		return append(out, Line{Icon: "info", Text: i18n.T(l, "link-tray.passive_code_hint")})
	}
	// waiting: cannot listen
	title := i18n.T(l, "link-tray.passive_cannot_listen")
	if st.Retry != nil {
		title = i18n.T(l, "link-tray.with_retry", i18n.Args{"state": title, "retry": Until(l, st.Retry.Sub(s.Now))})
	}
	out := append([]Line{{Icon: "offline", Text: title, Primary: true, Tone: ToneDanger}, mode}, failures(st.Error)...)
	return append(append(out, hub...), machine...)
}

// actions are the panel's rows (Mac: MenuView's rows), with what only Windows needs: the scheduled task and the logs.
func actions(l i18n.Lang, s Snapshot, look Look) []Action {
	var out []Action
	if s.Mode() != link.ModePassive {
		// Only a running persona-link with a pairing can redial.
		out = append(out, Action{ID: ActReconnect, Label: i18n.T(l, "link-tray.action_reconnect"), Icon: "refresh",
			Disabled: s.live() == nil || look == LookUnpaired})
	}
	pair := Action{ID: ActPair, Label: i18n.T(l, "link-tray.action_pair_change"), Icon: "link"}
	if look == LookUnpaired {
		pair.Label = i18n.T(l, "link-tray.action_pair_paste")
	}
	out = append(out, pair)
	if renamable(s) {
		out = append(out, Action{ID: ActRename, Label: i18n.T(l, "link-tray.action_rename"), Icon: "pencil"})
	}
	switch {
	case !s.Task.Installed:
		out = append(out, Action{ID: ActInstall, Label: i18n.T(l, "link-tray.action_install"), Icon: "download"})
	case look == LookDown || look == LookNodeMissing:
		out = append(out, Action{ID: ActRestart, Label: i18n.T(l, "link-tray.action_restart"), Icon: "restart"})
	}
	on := s.Autostart
	out = append(out,
		Action{ID: ActLogs, Label: i18n.T(l, "link-tray.action_logs"), Icon: "folder"},
		Action{ID: ActAutostart, Label: i18n.T(l, "link-tray.action_autostart"), Icon: "sign_in", Checked: &on},
		Action{ID: ActQuit, Label: i18n.T(l, "link-tray.action_quit"), Icon: "power", Hint: i18n.T(l, "link-tray.quit_hint")})
	return out
}
