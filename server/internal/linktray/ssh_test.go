package linktray

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

func TestSSHKindAndErrorSentencesInAllLanguages(t *testing.T) {
	for _, l := range i18n.Langs {
		if got := KindText(l, link.KindSSH); got != "SSH" {
			t.Fatalf("%s KindText(ssh) = %q, want SSH", l, got)
		}
		seen := map[string]string{}
		for _, code := range []string{
			link.SSHNoBinary,
			link.SSHAuthRefused,
			link.SSHHostKey,
			link.SSHForwardRefused,
			link.SSHTargetUnreachable,
		} {
			se := &link.SSHError{Code: code, Server: "user@example.com:2222", Target: "127.0.0.1:18662"}
			msg := ErrorText(l, se)
			if msg == "" || strings.HasPrefix(msg, "link-tray.") || strings.Contains(msg, "\n") {
				t.Fatalf("%s %s -> %q", l, code, msg)
			}
			if code == link.SSHHostKey && !strings.Contains(msg, "ssh -p 2222 user@example.com") {
				t.Fatalf("%s host_key missing terminal command: %q", l, msg)
			}
			if code == link.SSHTargetUnreachable && !strings.Contains(msg, "127.0.0.1:18662") {
				t.Fatalf("%s target_unreachable missing target: %q", l, msg)
			}
			if prev, dup := seen[msg]; dup {
				t.Fatalf("%s %s matches %s: %q", l, code, prev, msg)
			}
			seen[msg] = code
		}
	}
}

func TestParseLinkSSHDefaults(t *testing.T) {
	loop := ParseLink(i18n.En, "persona://link?url=http%3A%2F%2F127.0.0.1%3A18660&tls=18662&fp=xyz&token=abc&machine=desk")
	if loop.Kind != LinkOK || loop.DefaultSSHTarget != "127.0.0.1:18662" || !loop.DefaultSSHAlways {
		t.Fatalf("loopback link = %+v", loop)
	}
	lan := ParseLink(i18n.En, "persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&tls=18662&fp=xyz&token=abc&machine=desk")
	if lan.Kind != LinkOK || lan.DefaultSSHTarget != "127.0.0.1:18662" || lan.DefaultSSHAlways {
		t.Fatalf("LAN link = %+v", lan)
	}
}

func TestBuildAndBuildPairWithSSHTunnel(t *testing.T) {
	cfg := paired()
	cfg.SSH = &link.SSH{Server: "user@example.com", Target: "127.0.0.1:8443", Always: true}
	st := link.Status{
		State:   "connected",
		Mode:    link.ModeActive,
		Machine: "desk",
		HubName: "home",
		Address: "user@example.com",
		Kind:    link.KindSSH,
		Since:   at(-5 * time.Minute),
		Node:    "online",
		SSH:     cfg.SSH,
	}
	snap := Snapshot{Now: now, Running: true, Status: &link.SavedStatus{Status: st}, Config: cfg, Task: Task{Installed: true}}

	for _, l := range i18n.Langs {
		v := Build(l, snap)
		got := strings.Join(texts(v.Lines), "\n")
		if !strings.Contains(got, "SSH · user@example.com") {
			t.Fatalf("%s menu lines missing SSH route:\n%s", l, got)
		}
	}

	// With Always=true, the SSH row is listed first and marked InUse.
	pv := BuildPair(i18n.En, snap)
	if pv.SSH == nil || pv.SSH.Server != "user@example.com" || pv.SSH.Target != "127.0.0.1:8443" || !pv.SSH.Always {
		t.Fatalf("pair view ssh = %+v", pv)
	}
	if len(pv.Addresses) != 3 || pv.Addresses[0].Text != "SSH · user@example.com" ||
		!pv.Addresses[0].InUse || pv.Addresses[0].Manual {
		t.Fatalf("Always=true addresses = %+v", pv.Addresses)
	}

	// With Always=false, direct candidates come first and the SSH fallback row comes last.
	cfg.SSH.Always = false
	pvFallback := BuildPair(i18n.En, snap)
	last := pvFallback.Addresses[len(pvFallback.Addresses)-1]
	if last.Text != "SSH · user@example.com" || !last.InUse {
		t.Fatalf("Always=false last row = %+v (all=%+v)", last, pvFallback.Addresses)
	}

	// Waiting with SSHError renders the localized plain sentence in the tray panel for every language.
	waitSnap := running(link.Status{
		State:    "waiting",
		Retry:    at(10 * time.Second),
		Error:    "fallback error",
		SSHError: &link.SSHStatusError{Code: link.SSHAuthRefused, Server: "user@example.com", Target: "127.0.0.1:8443"},
	})
	for _, l := range i18n.Langs {
		v := Build(l, waitSnap)
		want := waitSnap.Status.SSHError.Text(l)
		if !strings.Contains(strings.Join(texts(v.Lines), "\n"), want) {
			t.Fatalf("%s waiting lines missing %q: %q", l, want, texts(v.Lines))
		}
	}
}
