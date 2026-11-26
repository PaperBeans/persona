package larksync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func TestTokenFromURL(t *testing.T) {
	cases := []struct {
		url, kind, token string
	}{
		{"https://tenant.feishu.cn/docx/doxcnAbC123", "doc", "doxcnAbC123"},
		{"https://tenant.feishu.cn/docs/doccnX1", "doc", "doccnX1"},
		{"https://tenant.feishu.cn/doc/doccnX2", "doc", "doccnX2"},
		{"https://tenant.feishu.cn/sheets/shtcnS1?sheet=abc", "doc", "shtcnS1"},
		{"https://tenant.feishu.cn/sheet/shtcnS2", "doc", "shtcnS2"},
		{"https://tenant.feishu.cn/base/bascnB1?table=tbl1", "doc", "bascnB1"},
		{"https://tenant.feishu.cn/bitable/bascnB2", "doc", "bascnB2"},
		{"https://tenant.feishu.cn/file/boxcnF1", "doc", "boxcnF1"},
		{"https://tenant.feishu.cn/mindnotes/bmncnM1", "doc", "bmncnM1"},
		{"https://tenant.feishu.cn/slides/sldcnS1", "doc", "sldcnS1"},
		{"https://tenant.feishu.cn/wiki/wikcnW1#heading", "wiki", "wikcnW1"},
		{"https://tenant.feishu.cn/minutes/obcnM1", "minutes", "obcnM1"},
		{"https://tenant.feishu.cn/minutes/obcnM1.", "minutes", "obcnM1"},
		{"https://tenant.larksuite.com/docx/doxusA1", "doc", "doxusA1"},
		{"https://tenant.larkoffice.com/wiki/wikW2", "wiki", "wikW2"},
		{"https://larksuite.com/minutes/obusM2", "minutes", "obusM2"},
		{"tenant.feishu.cn/docx/doxNoScheme", "doc", "doxNoScheme"},
		{"https://TENANT.FEISHU.CN/DOCX/doxUpper", "doc", "doxUpper"},
		{"https://example.com/docx/doxA", "", ""},
		{"https://feishu.cn.example.com/docx/doxA", "", ""},
		{"https://tenant.feishu.cn/drive/folder/fldA", "", ""},
		{"https://tenant.feishu.cn/docx/", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		kind, token := TokenFromURL(c.url)
		if kind != c.kind || token != c.token {
			t.Errorf("TokenFromURL(%q) = %q, %q; want %q, %q", c.url, kind, token, c.kind, c.token)
		}
	}
}

func TestDocLinkFromURL(t *testing.T) {
	cases := []struct {
		url, objType, token string
		ok                  bool
	}{
		{"https://my.feishu.cn/docx/doxcnAbC123", "docx", "doxcnAbC123", true},
		{"https://my.feishu.cn/file/boxcnF1", "file", "boxcnF1", true},
		{"https://my.feishu.cn/drive/file/boxcnF2?from=copy", "file", "boxcnF2", true},
		{"https://my.feishu.cn/sheets/shtcnS1?sheet=abc", "sheet", "shtcnS1", true},
		{"https://my.feishu.cn/base/bascnB1", "bitable", "bascnB1", true},
		{"https://my.feishu.cn/wiki/wikcnW1", "wiki", "wikcnW1", true},
		{"https://my.feishu.cn/minutes/obcnM1", "minutes", "obcnM1", true},
		{"https://my.feishu.cn/wiki/space/123456", "", "", false},
		{"https://my.feishu.cn/drive/folder/fldA", "", "", false},
		{"https://example.com/docx/doxA", "", "", false},
	}
	for _, c := range cases {
		typ, tok, ok := docLinkFromURL(c.url)
		if typ != c.objType || tok != c.token || ok != c.ok {
			t.Errorf("docLinkFromURL(%q) = %q, %q, %v; want %q, %q, %v", c.url, typ, tok, ok, c.objType, c.token, c.ok)
		}
	}
}

// The fingerprint as the spec writes it, computed apart from the code under test (the knowledge base computes the
// same, internal/kb/align_gateway.go).
func specFingerprint(chat, sender string, at time.Time, body string) string {
	b := sha256.Sum256([]byte(body))
	s := sha256.Sum256([]byte(chat + "\x00" + sender + "\x00" + strconv.FormatInt(at.Unix()/2, 10) + "\x00" + hex.EncodeToString(b[:])))
	return hex.EncodeToString(s[:])
}

func TestMessageFingerprint(t *testing.T) {
	at := time.Unix(1_800_000_000, 0) // even: at and at+1s share a bucket
	base := MessageFingerprint("Team", "Alex", at, "hello world")
	if want := specFingerprint("team", "alex", at, "hello world"); base != want {
		t.Fatalf("fingerprint = %s, want the spec's %s", base, want)
	}
	cases := []struct {
		name               string
		chat, sender, body string
		at                 time.Time
		same               bool
	}{
		{"normalized spacing and case", "  TEAM ", "alex", "Hello\n  World ", at, true},
		{"same two-second bucket", "Team", "Alex", "hello world", at.Add(time.Second + 900*time.Millisecond), true},
		{"next bucket", "Team", "Alex", "hello world", at.Add(2 * time.Second), false},
		{"another body", "Team", "Alex", "hello there", at, false},
		{"another sender", "Team", "Sam", "hello world", at, false},
		{"empty sender differs from named sender", "Team", "", "hello world", at, false},
		{"another chat", "Other", "Alex", "hello world", at, false},
	}
	for _, c := range cases {
		got := MessageFingerprint(c.chat, c.sender, c.at, c.body)
		if (got == base) != c.same {
			t.Errorf("%s: same = %v, want %v", c.name, got == base, c.same)
		}
	}

	// Empty sender degrades to a deterministic senderless fingerprint that matches internal/kb/align_gateway.go.
	noSenderAt := time.Unix(1700000001, 0)
	noSender := MessageFingerprint("  Example   Team ", "", noSenderAt, "Hello\n  World ")
	if want := specFingerprint("example team", "", noSenderAt, "hello world"); noSender != want {
		t.Fatalf("senderless fingerprint = %s, want %s", noSender, want)
	}
	if got := MessageFingerprint("Example Team", "   ", noSenderAt.Add(-time.Second), "hello world"); got != noSender {
		t.Errorf("whitespace sender / same bucket = %s, want %s", got, noSender)
	}
	if got := MessageFingerprint("Example Team", "", noSenderAt.Add(time.Second), "hello world"); got == noSender {
		t.Error("senderless fingerprint must still separate 2-second buckets")
	}
	if got := MessageFingerprint("Example Team", "", noSenderAt, "hello there"); got == noSender {
		t.Error("senderless fingerprint must still separate message bodies")
	}
	if got := MessageFingerprint("Other Team", "", noSenderAt, "hello world"); got == noSender {
		t.Error("senderless fingerprint must still separate chats")
	}
}

func TestAttachmentRefRoundTrip(t *testing.T) {
	ref := attachmentRef("bot~a|b", "om_1", "file_k", "file")
	bnd, msg, key, kind, ok := parseRef(ref)
	if !ok || bnd != "bot~a|b" || msg != "om_1" || key != "file_k" || kind != "file" {
		t.Fatalf("parseRef(%q) = %q %q %q %q %v", ref, bnd, msg, key, kind, ok)
	}
	for _, bad := range []string{"", "a|b|c", "k|om|key|video", "|om|key|image"} {
		if _, _, _, _, ok := parseRef(bad); ok {
			t.Errorf("parseRef(%q) accepted", bad)
		}
	}
	if got := openIDOf("feishu:tenant_x:ou_1"); got != "ou_1" {
		t.Errorf("openIDOf = %q", got)
	}
}

func TestBackoffAndRetryWait(t *testing.T) {
	s := NewSyncer(nil, func() time.Time { return testNow })
	s.jitter = func(time.Duration) time.Duration { return 0 }
	for n, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		if got := s.backoff(n, 0); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
	if got := s.backoff(0, 90*time.Second); got != time.Minute {
		t.Errorf("a named wait above a minute = %v", got)
	}
	s.jitter = randJitter
	for range 50 {
		if got := s.backoff(1, 0); got < 2*time.Second || got >= 2500*time.Millisecond {
			t.Fatalf("jittered backoff(1) = %v, want [2s, 2.5s)", got)
		}
	}
}

// The limiter waits out a family's debt through the sleeper and never makes one family wait for another.
func TestLimiterPerFamily(t *testing.T) {
	clk := &fakeClock{t: testNow}
	l := newLimiter(clk.now)
	l.rates = map[string]float64{famSearch: 1, famIM: 5}
	if d := l.reserve(famSearch); d != 0 {
		t.Fatalf("first search waits %v", d)
	}
	if d := l.reserve(famSearch); d != time.Second {
		t.Fatalf("second search in the same instant waits %v, want 1s", d)
	}
	for i := range 5 {
		if d := l.reserve(famIM); d != 0 {
			t.Fatalf("im request %d waits %v while search is limited", i, d)
		}
	}
	clk.advance(2 * time.Second)
	if d := l.reserve(famSearch); d != 0 {
		t.Fatalf("search after the refill waits %v", d)
	}
	var slept []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if err := l.wait(t.Context(), famSearch, sleep); err != nil || len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("wait: %v, slept %v, want 1s", err, slept)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.wait(ctx, famIM, sleep); err == nil {
		t.Fatal("wait on a cancelled context went on")
	}
}
