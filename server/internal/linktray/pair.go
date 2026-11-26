package linktray

import (
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// The pairing window (Mac: PairView): active mode takes a link from the phone or the web, passive mode makes one for
// the hub's admin page, and both list what is paired now.

// LinkKind says what a pasted or opened link is to this window.
type LinkKind string

const (
	LinkOK         LinkKind = "ok"         // a pairing link for this computer: preview it, pair on the user's click
	LinkIncomplete LinkKind = "incomplete" // ours, but without its token
	LinkOtherApp   LinkKind = "other_app"  // ours, but for the apps (persona://pair?…, a phone's link)
	LinkNotOurs    LinkKind = "not_ours"   // not a pairing link at all
)

// LinkPreview is what the window says about a link before anything is sent: which hub, whether it is pinned, what
// this computer is called there.
type LinkPreview struct {
	Kind             LinkKind `json:"kind"`
	Raw              string   `json:"raw,omitempty"`
	Hub              string   `json:"hub,omitempty"` // host[:port][path] of the hub's URL
	Pinned           bool     `json:"pinned,omitempty"`
	Machine          string   `json:"machine,omitempty"`
	Lines            []Line   `json:"lines,omitempty"`
	Notice           string   `json:"notice,omitempty"` // for every kind but ok and not_ours
	DefaultSSHTarget string   `json:"default_ssh_target,omitempty"`
	DefaultSSHAlways bool     `json:"default_ssh_always,omitempty"`
}

// ParseLink previews raw in language l (Mac: PairLink.parse). It only reads the link: link.Pair checks it for real.
func ParseLink(l i18n.Lang, raw string) LinkPreview {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || s == "" {
		return LinkPreview{Kind: LinkNotOurs}
	}
	if !link.PairScheme(u.Scheme) {
		return LinkPreview{Kind: LinkNotOurs}
	}
	if strings.ToLower(u.Host) != "link" {
		return LinkPreview{Kind: LinkOtherApp, Notice: i18n.T(l, "link-tray.notice_other_app")}
	}
	q := u.Query()
	if q.Get("token") == "" {
		return LinkPreview{Kind: LinkIncomplete, Notice: i18n.T(l, "link-tray.notice_incomplete")}
	}
	p := LinkPreview{Kind: LinkOK, Raw: s, Machine: q.Get("machine"), Pinned: q.Get("fp") != ""}
	if hub := q.Get("url"); hub != "" {
		p.Hub = HostText(hub)
	}
	if p.Hub != "" {
		p.Lines = append(p.Lines, Line{Icon: "server", Text: i18n.T(l, "link-tray.preview_hub", i18n.Args{"host": p.Hub})})
	} else {
		p.Lines = append(p.Lines, Line{Icon: "server", Text: i18n.T(l, "link-tray.preview_no_hub")})
	}
	if p.Pinned {
		p.Lines = append(p.Lines, Line{Icon: "shield", Text: i18n.T(l, "link-tray.preview_pinned"), Tone: TonePositive})
	}
	if p.Machine != "" {
		p.Lines = append(p.Lines, Line{Icon: "laptop", Text: i18n.T(l, "link-tray.preview_machine", i18n.Args{"name": p.Machine})})
	}
	if defTarget, defAlways, ok := link.PairSSHDefaults(s); ok {
		p.DefaultSSHTarget = defTarget
		p.DefaultSSHAlways = defAlways
	}
	return p
}

// Address is one hub candidate in the window's list.
type Address struct {
	URL    string `json:"url"`
	Text   string `json:"text"` // kind · host
	InUse  bool   `json:"in_use,omitempty"`
	Manual bool   `json:"manual,omitempty"` // added by hand: the only ones the user can remove
}

// PairState is the window's data: what each mode has paired, the addresses, the mode in use.
type PairState struct {
	Mode             string    `json:"mode"`
	ActivePaired     bool      `json:"active_paired"`
	Machine          string    `json:"machine,omitempty"`
	SystemTrust      bool      `json:"system_trust,omitempty"`
	SSH              *link.SSH `json:"ssh,omitempty"`
	DefaultSSHTarget string    `json:"default_ssh_target,omitempty"`
	Addresses        []Address `json:"addresses"`
	PassivePaired    bool      `json:"passive_paired"`
	PassiveText      string    `json:"passive_text,omitempty"` // who passive mode is paired with
	Listen           string    `json:"listen"`
	// SwitchBack: the other mode is paired, so the window offers to go back to it without pairing again.
	SwitchToActive  string `json:"switch_to_active,omitempty"`
	SwitchToPassive string `json:"switch_to_passive,omitempty"`
}

// BuildPair is the window's data from link.json and the running persona-link's status.
func BuildPair(l i18n.Lang, s Snapshot) PairState {
	c := s.Config
	p := PairState{Mode: s.Mode(), Listen: link.DefaultListen, Addresses: []Address{}}
	if c != nil {
		p.Listen = c.Passive.ListenAddr()
		p.ActivePaired = c.Token != ""
		if p.ActivePaired {
			p.Machine = c.Machine
		}
		p.SystemTrust = c.SystemTrust()
		p.SSH = c.SSH
		p.DefaultSSHTarget = link.DefaultSSHTarget(c.Endpoints)
		inUse, inUseSSH := "", false
		if st := s.live(); st != nil && st.Mode != link.ModePassive && st.State == "connected" {
			if st.Kind == link.KindSSH {
				inUseSSH = true
			} else {
				inUse = st.Address
			}
		}
		sshRow := func() Address {
			return Address{
				URL:   "ssh://" + c.SSH.Server,
				Text:  i18n.T(l, "link-tray.address_line", i18n.Args{"kind": KindText(l, link.KindSSH), "host": c.SSH.Server}),
				InUse: inUseSSH,
			}
		}
		if c.SSH != nil && c.SSH.Always {
			p.Addresses = append(p.Addresses, sshRow())
		}
		for _, e := range link.Ranked(c.Endpoints) {
			p.Addresses = append(p.Addresses, Address{URL: e.URL, Text: i18n.T(l, "link-tray.address_line",
				i18n.Args{"kind": KindText(l, link.KindOf(e.URL)), "host": HostText(e.URL)}),
				InUse: !inUseSSH && e.URL == inUse, Manual: e.Manual})
		}
		if c.SSH != nil && !c.SSH.Always {
			p.Addresses = append(p.Addresses, sshRow())
		}
		if c.Passive.Paired() {
			p.PassivePaired = true
			if c.Passive.HubName != "" {
				p.PassiveText = i18n.T(l, "link-tray.passive_paired_named", i18n.Args{"hub": c.Passive.HubName})
			} else {
				p.PassiveText = i18n.T(l, "link-tray.passive_paired")
			}
		}
	}
	if p.Mode == link.ModePassive && p.ActivePaired {
		p.SwitchToActive = i18n.T(l, "link-tray.switch_to_active")
	}
	if p.Mode != link.ModePassive && p.PassivePaired {
		p.SwitchToPassive = i18n.T(l, "link-tray.switch_to_passive")
	}
	return p
}
