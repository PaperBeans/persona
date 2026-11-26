package linktray

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := now.Add(d); return &t }

func paired() *link.Config {
	return &link.Config{Token: "t", Machine: "desk", FP: "fp", HubName: "home",
		Endpoints: []link.Endpoint{{URL: "https://192.0.2.2:8443", Kind: "lan"}, {URL: "https://hub.example.com", Kind: "name", Manual: true}}}
}

func running(st link.Status) Snapshot {
	return Snapshot{Now: now, Running: true, Status: &link.SavedStatus{Status: st}, Config: paired(), Task: Task{Installed: true}}
}

func TestLookOf(t *testing.T) {
	cases := []struct {
		name string
		s    Snapshot
		want Look
	}{
		{"connected", running(link.Status{State: "connected", Node: "online"}), LookConnected},
		{"pending wins over node", running(link.Status{State: "connected", Pending: true, Node: "offline"}), LookAwaitingConfirm},
		{"node offline", running(link.Status{State: "connected", Node: "offline"}), LookNodeMissing},
		{"listening", running(link.Status{State: "listening", Mode: link.ModePassive}), LookListening},
		{"passive node offline", running(link.Status{State: "listening", Mode: link.ModePassive, Node: "offline"}), LookNodeMissing},
		{"connecting", running(link.Status{State: "connecting"}), LookConnecting},
		{"waiting", running(link.Status{State: "waiting"}), LookDown},
		{"refused", running(link.Status{State: "refused"}), LookDown},
		{"unpaired", running(link.Status{State: "unpaired"}), LookUnpaired},
		{"alive, nothing said yet", Snapshot{Now: now, Running: true, Config: paired()}, LookConnecting},
		{"not running, paired", Snapshot{Now: now, Status: &link.SavedStatus{Status: link.Status{State: "connected"}}, Config: paired()}, LookDown},
		{"stopped is not running", Snapshot{Now: now, Running: true, Status: &link.SavedStatus{Status: link.Status{State: "stopped"}}, Config: paired()}, LookDown},
		{"not running, never paired", Snapshot{Now: now}, LookUnpaired},
		{"not running, passive unpaired", Snapshot{Now: now, Config: &link.Config{Token: "t", Machine: "m", FP: "f", Mode: link.ModePassive}}, LookUnpaired},
	}
	for _, c := range cases {
		if got := LookOf(c.s); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	for _, l := range Looks {
		if l.Tone() == "" && l != LookUnpaired {
			t.Errorf("%s has no tone", l)
		}
	}
}

func texts(ls []Line) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Text)
	}
	return out
}

func TestConnectedLinesFollowTheMac(t *testing.T) {
	s := running(link.Status{State: "connected", Mode: link.ModeActive, Machine: "desk", HubName: "home", MeshHubs: 2,
		Address: "https://192.0.2.2:8443", Kind: "lan", Since: at(-90 * time.Minute), Node: "online"})
	v := Build(i18n.En, s)
	want := []string{
		"Connected to the hub",
		"This computer connects (active)",
		"LAN · 192.0.2.2:8443",
		"Hub “home” · 2 hubs in its group",
		"Connected for 1 h 30 min",
		"Local node online",
		"This computer is “desk” on the hub",
	}
	if got := texts(v.Lines); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !v.Lines[0].Primary || v.Lines[0].Tone != TonePositive {
		t.Fatalf("first line %+v", v.Lines[0])
	}
	if v.Tooltip != "Persona Link · Connected · home" {
		t.Fatalf("tooltip %q", v.Tooltip)
	}
}

func TestNodeOfflineSaysWhatToDoSecond(t *testing.T) {
	v := Build(i18n.En, running(link.Status{State: "connected", Address: "https://hub.example.com", Kind: "name", Node: "offline", NodeError: "dial node: refused"}))
	got := texts(v.Lines)
	if got[0] != "Persona's background service isn't running on this computer" || !strings.Contains(got[1], "Restart background service") ||
		got[2] != "This computer connects (active)" || got[len(got)-1] != "dial node: refused" {
		t.Fatalf("lines %q", got)
	}
	if !hasAction(v, ActRestart) {
		t.Fatal("no restart action while the node is missing")
	}
}

func TestWaitingSplitsFailures(t *testing.T) {
	v := Build(i18n.ZhHans, running(link.Status{State: "waiting", Retry: at(12 * time.Second), Error: "内网：超时；域名：拒绝连接；"}))
	got := texts(v.Lines)
	if got[0] != "离线 · 12 秒后重试" || got[1] != "我去连 hub（主动）" || got[2] != "内网：超时" || got[3] != "域名：拒绝连接" {
		t.Fatalf("lines %q", got)
	}
}

func TestPassiveLines(t *testing.T) {
	s := running(link.Status{State: "listening", Mode: link.ModePassive, Listen: ":18663", HubName: "home", LastSeen: at(-3 * time.Hour), From: "198.51.100.2"})
	got := texts(Build(i18n.En, s).Lines)
	want := []string{"Listening for the hub", "Hub connects (passive) · listening on :18663", "Hub “home”", "Hub last connected 3 h ago · from 198.51.100.2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines %q", got)
	}
	code := running(link.Status{State: "unpaired", Mode: link.ModePassive, Listen: ":18663", CodeExpires: at(9*time.Minute + time.Second)})
	if got := texts(Build(i18n.En, code).Lines); !strings.HasPrefix(got[2], "The pairing link is good for 9 min") {
		t.Fatalf("code line %q", got)
	}
	v := Build(i18n.En, s)
	if hasAction(v, ActReconnect) {
		t.Fatal("passive mode cannot redial")
	}
}

func TestNotRunning(t *testing.T) {
	missing := Build(i18n.En, Snapshot{Now: now})
	if missing.Lines[0].Text != "The background service isn't installed" || !hasAction(missing, ActInstall) || hasAction(missing, ActRestart) {
		t.Fatalf("no task: %+v", missing)
	}
	down := Build(i18n.En, Snapshot{Now: now, Config: paired(), Task: Task{Installed: true, State: "Ready"}})
	if down.Look != LookDown || down.Lines[0].Text != "Persona Link isn't running" || down.Lines[2].Text != "Background service: Ready" ||
		!hasAction(down, ActRestart) || action(down, ActReconnect).Disabled != true {
		t.Fatalf("down: %+v", down)
	}
	unpaired := Build(i18n.En, Snapshot{Now: now, Task: Task{Installed: true}})
	if unpaired.Look != LookUnpaired || action(unpaired, ActPair).Label != "Paste pairing link…" {
		t.Fatalf("unpaired: %+v", unpaired)
	}
}

func TestAutostartIsASwitch(t *testing.T) {
	s := running(link.Status{State: "connected"})
	s.Autostart = true
	if a := action(Build(i18n.En, s), ActAutostart); a.Checked == nil || !*a.Checked {
		t.Fatalf("autostart %+v", a)
	}
}

func hasAction(v View, id string) bool { return action(v, id).ID != "" }

func action(v View, id string) Action {
	for _, a := range v.Actions {
		if a.ID == id {
			return a
		}
	}
	return Action{}
}

func TestEveryLookHasItsOwnPillInEveryLanguage(t *testing.T) {
	for _, l := range i18n.Langs {
		seen := map[string]bool{}
		for _, look := range Looks {
			p := pill(l, look)
			if p == "" || strings.HasPrefix(p, "link-tray.") || seen[p] {
				t.Errorf("%s %s: pill %q", l, look, p)
			}
			seen[p] = true
		}
	}
}

func TestParseLink(t *testing.T) {
	ok := ParseLink(i18n.En, " persona://link?url=https%3A%2F%2Fhub.example.com%3A8443&token=abc&machine=desk&fp=xyz&tls=8443 ")
	if ok.Kind != LinkOK || ok.Hub != "hub.example.com:8443" || !ok.Pinned || ok.Machine != "desk" || len(ok.Lines) != 3 {
		t.Fatalf("ok %+v", ok)
	}
	if p := ParseLink(i18n.En, "persona://link?token=abc"); p.Kind != LinkOK || p.Lines[0].Text != "The link doesn't say where the hub is" {
		t.Fatalf("no url %+v", p)
	}
	// Older hubs print the baseline scheme: the same link.
	if p := ParseLink(i18n.En, link.LegacyPairLink("persona://link?token=abc&machine=desk")); p.Kind != LinkOK || p.Machine != "desk" {
		t.Fatalf("baseline scheme %+v", p)
	}
	for raw, kind := range map[string]LinkKind{
		"persona://pair?token=abc":   LinkOtherApp,
		"persona://link?machine=m":   LinkIncomplete,
		"https://hub.example.com":    LinkNotOurs,
		"":                           LinkNotOurs,
		"persona://connector?addr=x": LinkOtherApp,
	} {
		p := ParseLink(i18n.En, raw)
		if p.Kind != kind {
			t.Errorf("%q: %s, want %s", raw, p.Kind, kind)
		}
		if (kind == LinkOtherApp || kind == LinkIncomplete) && p.Notice == "" {
			t.Errorf("%q: no notice", raw)
		}
	}
}

func TestTextHelpers(t *testing.T) {
	cases := map[string]string{
		HostText("https://[2001:db8::1]:8443/"):     "[2001:db8::1]:8443",
		HostText("https://example.com/persona"):     "example.com/persona",
		HostText("not a url"):                       "not a url",
		Duration(i18n.En, 30*time.Second):           "under a minute",
		Duration(i18n.En, 3*24*time.Hour+time.Hour): "3 d 1 h",
		Ago(i18n.Ja, 2*time.Minute):                 "2 分前",
		Until(i18n.En, 0):                           "retrying now",
		Until(i18n.En, 125*time.Second):             "retrying in 2 min",
		Left(i18n.ZhHans, 30*time.Second):           "不到 1 分钟",
		HubText(i18n.En, "home", 1):                 "Hub “home” · not linked to other hubs",
		KindText(i18n.En, "weird"):                  "Address",
		CodeAddrs(i18n.En, []string{"a:1", "b:2"}):  "The hub tries these addresses in order: a:1, b:2. If it can't reach any of them, change the address in the hub's admin page.",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

func TestBuildPair(t *testing.T) {
	c := paired()
	c.Passive = &link.Passive{TokenSHA256: "h", HubName: "nas"}
	s := running(link.Status{State: "connected", Address: "https://hub.example.com"})
	s.Config = c
	p := BuildPair(i18n.En, s)
	if !p.ActivePaired || p.Machine != "desk" || !p.PassivePaired || p.SwitchToPassive == "" || p.SwitchToActive != "" ||
		!strings.Contains(p.PassiveText, "“nas”") || p.Listen != link.DefaultListen {
		t.Fatalf("pair %+v", p)
	}
	if len(p.Addresses) != 2 {
		t.Fatalf("addresses %+v", p.Addresses)
	}
	var inUse, manual int
	for _, a := range p.Addresses {
		if a.InUse {
			inUse++
			if a.URL != "https://hub.example.com" {
				t.Fatalf("in use %+v", a)
			}
		}
		if a.Manual {
			manual++
		}
	}
	if inUse != 1 || manual != 1 {
		t.Fatalf("addresses %+v", p.Addresses)
	}
	empty := BuildPair(i18n.En, Snapshot{Now: now})
	if empty.ActivePaired || empty.PassivePaired || empty.Addresses == nil || empty.Mode != link.ModeActive {
		t.Fatalf("empty %+v", empty)
	}
}

func TestLabelsAreTranslated(t *testing.T) {
	for _, l := range i18n.Langs {
		for k, v := range Labels(l) {
			if v == "" || strings.HasPrefix(v, "link-tray.") {
				t.Errorf("%s %s = %q", l, k, v)
			}
		}
	}
	if !strings.Contains(FirewallCommand("18663"), "-LocalPort 18663 ") {
		t.Fatal(FirewallCommand("18663"))
	}
}

// The failures the tray knows are said in the user's language, with the raw error kept after them; others as they are.
func TestErrorText(t *testing.T) {
	raw := errors.New("dial tcp 192.0.2.1:18662: connection refused")
	unreachable := &PairError{Hosts: []string{"192.0.2.1:18662", "hub.example.com:18662"}, Err: raw}
	for _, tc := range []struct {
		l      i18n.Lang
		err    error
		prefix string
		detail bool
	}{
		{i18n.En, fmt.Errorf("restart: %w", ErrNoService), "The background service isn't installed", false},
		{i18n.En, ErrInstallInTerminal, "Can't install from here", false},
		{i18n.En, unreachable, "Can't reach the hub (192.0.2.1:18662, hub.example.com:18662).", true},
		{i18n.Ja, unreachable, "ハブ（192.0.2.1:18662、hub.example.com:18662）に接続できません", true},
		{i18n.En, &PairError{Rejected: true, Err: raw}, "The hub doesn't accept this pairing link", true},
		{i18n.En, raw, raw.Error(), false},
	} {
		got := ErrorText(tc.l, tc.err)
		if !strings.HasPrefix(got, tc.prefix) || tc.detail != strings.Contains(got, "\n") || (tc.detail && !strings.HasSuffix(got, raw.Error()+")") && !strings.HasSuffix(got, raw.Error()+"）")) {
			t.Errorf("%s %v:\n%s", tc.l, tc.err, got)
		}
	}
}

// The name the hub gives this computer is the one shown, and renaming is offered only where the hub takes it: active
// mode, connected, a hub that says the name, and a confirmed computer.
func TestTheHubsNameAndRename(t *testing.T) {
	st := link.Status{State: "connected", Mode: link.ModeActive, Machine: "desk", MachineName: "87K-itx", HubName: "home",
		Address: "https://192.0.2.2:8443", Kind: "lan", Node: "online"}
	v := Build(i18n.En, running(st))
	if got := texts(v.Lines); got[len(got)-1] != "This computer is “87K-itx” on the hub" || v.Name != "87K-itx" {
		t.Fatalf("lines %q name %q", got, v.Name)
	}
	if !hasAction(v, ActRename) {
		t.Fatal("no rename while connected to a hub that renames")
	}
	older := st
	older.MachineName = ""
	if v := Build(i18n.En, running(older)); hasAction(v, ActRename) || v.Name != "desk" {
		t.Fatalf("an older hub: rename %v, name %q", hasAction(v, ActRename), v.Name)
	}
	pending := st
	pending.Pending = true
	if hasAction(Build(i18n.En, running(pending)), ActRename) {
		t.Fatal("a computer waiting to be confirmed renames itself")
	}
	down := st
	down.State = "waiting"
	if v := Build(i18n.En, running(down)); hasAction(v, ActRename) {
		t.Fatal("rename offered while offline")
	}
	// Not running: the name link.json keeps.
	s := Snapshot{Now: now, Config: paired(), Task: Task{Installed: true}}
	s.Config.MachineName = "Studio"
	if v := Build(i18n.En, s); v.Name != "Studio" {
		t.Fatalf("name %q", v.Name)
	}
	if ErrorText(i18n.En, &link.RenameError{Code: link.RenamePending}) == "" {
		t.Fatal("no words for a refused rename")
	}
}
