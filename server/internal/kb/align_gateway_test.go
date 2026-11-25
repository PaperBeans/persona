package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fingerprint is the Lark module's larksync.MessageFingerprint too: this vector is the written rule's, so both
// sides are held to the same bytes.
func TestMessageFingerprintVector(t *testing.T) {
	at := time.Unix(1700000001, 0)
	const want = "ce93960aa5566b787ca3ad5000ad646f8b1376bd5721b7db45d6b1aca780553e"
	if got := messageFingerprint("  Example   Team ", "Example Sender", at, "Hello\n  World "); got != want {
		t.Fatalf("fingerprint %s, want %s", got, want)
	}
	if messageFingerprint("Example Team", "Example Sender", at.Add(time.Second), "hello world") == want {
		t.Fatal("1700000002 is another 2-second bucket")
	}
	if messageFingerprint("Example Team", "Example Sender", at.Add(-time.Second), "hello world") != want {
		t.Fatal("1700000000 and 1700000001 share a bucket")
	}
	// Empty sender produces the deterministic senderless fallback key (matching larksync.MessageFingerprint).
	noSender := messageFingerprint("  Example   Team ", "", at, "Hello\n  World ")
	if noSender == want {
		t.Fatal("empty sender must not collide with a named sender")
	}
	if got := messageFingerprint("Example Team", "  ", at.Add(-time.Second), "hello world"); got != noSender {
		t.Fatalf("senderless fingerprint %s, want %s", got, noSender)
	}
	if got := messageFingerprint("Example Team", "", at.Add(time.Second), "hello world"); got == noSender {
		t.Fatal("senderless fingerprint must still separate 2-second buckets")
	}
}

// alignAt is a time whose 2-second bucket starts at it, an hour ago (inside the text days, clear of the cutoff).
func alignAt() time.Time {
	return time.Now().Add(-time.Hour).Truncate(time.Minute).Add(10 * time.Second)
}

func alignEngine(t *testing.T) (*Engine, *int) {
	t.Helper()
	e := testEngine(t, nil)
	align := NewGatewayAligner(e.Store)
	calls := 0
	e.Align = func(src Source, sc SourceChat, m SourceMessage) bool {
		calls++
		return align(src, sc, m)
	}
	return e, &calls
}

func readCrosswalk(t *testing.T, s *Store) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.Dir, KindChat, crosswalkFile))
	if err != nil {
		t.Fatalf("crosswalk: %v", err)
	}
	var w map[string]string
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func receiptIDs(t *testing.T, s *Store, chat string) map[string]time.Time {
	t.Helper()
	var r messageReceipts
	if _, err := readJSON(s.path(KindChat, chat, ingestReceipts), &r); err != nil {
		t.Fatal(err)
	}
	return r.IDs
}

// A message the sync reads that the gateway archived (same chat name, sender, body, bucket) is not written again and
// pairs the chats; another body, or the same body from another sender, is kept.
func TestGatewayAlignerSkipsWhatTheGatewayArchived(t *testing.T) {
	e, calls := alignEngine(t)
	ctx := context.Background()
	at := alignAt()
	gw := SourceChat{ID: "f1", Name: "Example Team", Kind: "group"}
	if n, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{
		{ID: "g1", SenderID: "u1", SenderName: "Example Sender", Text: "Synthetic   status update", At: at},
		{ID: "g2", SenderID: "u1", SenderName: "Example Sender", Text: "Second synthetic line", At: at.Add(20 * time.Second)},
	}); err != nil || n != 2 {
		t.Fatalf("gateway ingest: %d, %v", n, err)
	}
	if *calls != 2 {
		t.Fatalf("the aligner is asked about every new message: %d calls", *calls)
	}
	sync := SourceChat{ID: "oc_1", Name: " example  team", Kind: "group"}
	n, err := e.Ingest(ctx, named("larksync"), sync, []SourceMessage{
		{ID: "om_1", SenderID: "ou_1", SenderName: "Example Sender", Text: "synthetic status update", At: at.Add(time.Second)},
		{ID: "om_2", SenderID: "ou_1", SenderName: "Example Sender", Text: "A different synthetic body", At: at.Add(time.Second)},
		{ID: "om_3", SenderID: "ou_2", SenderName: "Another Sender", Text: "Synthetic status update", At: at.Add(time.Second)},
		{ID: "om_4", SenderID: "ou_1", SenderName: "Example Sender", Text: "Synthetic status update", At: at.Add(5 * time.Second)},
	})
	if err != nil || n != 3 {
		t.Fatalf("sync ingest: added %d, want 3 (om_2, om_3 and om_4, two buckets later), %v", n, err)
	}
	text := e.Store.Since("larksync-oc_1", time.Time{}, 1<<20)
	if strings.Contains(text, "Example Sender: synthetic status update") || !strings.Contains(text, "A different synthetic body") ||
		!strings.Contains(text, "Another Sender: Synthetic status update") {
		t.Fatalf("sync archive:\n%s", text)
	}
	if w := readCrosswalk(t, e.Store); w["larksync-oc_1"] != "lark-f1" || len(w) != 1 {
		t.Fatalf("crosswalk %v", w)
	}
	if _, ok := receiptIDs(t, e.Store, "larksync-oc_1")["om_1"]; !ok {
		t.Fatal("the duplicate is received: its ID is in the receipts")
	}

	// Received: the same message again is not asked about. A batch of only a duplicate keeps its receipt too.
	before := *calls
	if n, err := e.Ingest(ctx, named("larksync"), sync, []SourceMessage{
		{ID: "om_1", SenderID: "ou_1", SenderName: "Example Sender", Text: "synthetic status update", At: at.Add(time.Second)},
		{ID: "om_5", SenderID: "ou_1", SenderName: "Example Sender", Text: "Second synthetic line", At: at.Add(21 * time.Second)},
	}); err != nil || n != 0 {
		t.Fatalf("again: %d, %v", n, err)
	}
	if *calls != before+1 {
		t.Fatalf("om_1 was asked about again: %d calls, want %d", *calls, before+1)
	}
	if _, ok := receiptIDs(t, e.Store, "larksync-oc_1")["om_5"]; !ok {
		t.Fatal("a batch of only duplicates records them")
	}
	if c, _ := e.Store.Chat("larksync-oc_1"); c.Messages != 3 {
		t.Fatalf("messages %d, want 3", c.Messages)
	}
}

// Once paired, a chat compares within its pair only, whatever the names say; a chat never stored keeps no file.
func TestGatewayAlignerFollowsTheCrosswalk(t *testing.T) {
	e, _ := alignEngine(t)
	ctx := context.Background()
	at := alignAt()
	if _, err := e.Ingest(ctx, named("lark"), SourceChat{ID: "f2", Name: "Gateway name", Kind: "group"}, []SourceMessage{
		{ID: "g1", SenderName: "Example Sender", Text: "Paired synthetic text", At: at},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(ctx, named("lark"), SourceChat{ID: "f3", Name: "Sync name", Kind: "group"}, []SourceMessage{
		{ID: "g2", SenderName: "Example Sender", Text: "Same-name synthetic text", At: at},
	}); err != nil {
		t.Fatal(err)
	}
	walk, _ := json.Marshal(map[string]string{"larksync-oc_2": "lark-f2"})
	if err := os.WriteFile(filepath.Join(e.Store.Dir, KindChat, crosswalkFile), walk, 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := e.Ingest(ctx, named("larksync"), SourceChat{ID: "oc_2", Name: "Sync name", Kind: "group"}, []SourceMessage{
		{ID: "om_1", SenderName: "Example Sender", Text: "Paired synthetic text", At: at},
		{ID: "om_2", SenderName: "Example Sender", Text: "Same-name synthetic text", At: at},
	})
	if err != nil || n != 1 {
		t.Fatalf("added %d, want 1 (the pair's message skipped, the same-name chat not consulted): %v", n, err)
	}
	if !strings.Contains(e.Store.Since("larksync-oc_2", time.Time{}, 1<<20), "Same-name synthetic text") {
		t.Fatal("a chat outside the pair is not compared")
	}
	// Only duplicates for a chat not stored yet: nothing is written, not even an empty chat.
	if n, err := e.Ingest(ctx, named("larksync"), SourceChat{ID: "oc_3", Name: "Gateway name", Kind: "group"}, []SourceMessage{
		{ID: "om_3", SenderName: "Example Sender", Text: "Paired synthetic text", At: at},
	}); err != nil || n != 0 {
		t.Fatalf("only a duplicate: %d, %v", n, err)
	}
	if _, ok := e.Store.Chat("larksync-oc_3"); ok {
		t.Fatal("an empty chat was stored")
	}
}

// A gateway line its receipts do not bind (no IDs) is compared by its minute; other sources are never aligned.
func TestGatewayAlignerMinuteLinesAndOtherSources(t *testing.T) {
	e, _ := alignEngine(t)
	ctx := context.Background()
	at := alignAt()
	if _, err := e.Ingest(ctx, named("lark"), SourceChat{ID: "f4", Name: "Example Team", Kind: "group"}, []SourceMessage{
		{SenderName: "Example Sender", Text: "Unbound synthetic text", At: at},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.Ingest(ctx, ownedArchiveSource{named: "feed", owner: "example"}, SourceChat{ID: "x", Name: "Example Team"}, []SourceMessage{
		{ID: "x1", SenderName: "Example Sender", Text: "Unbound synthetic text", At: at},
	}); err != nil || n != 1 {
		t.Fatalf("another source is never aligned: %d, %v", n, err)
	}
	n, err := e.Ingest(ctx, ownedArchiveSource{named: "larksync", owner: "larksync"}, SourceChat{ID: "oc_4", Name: "Example Team"}, []SourceMessage{
		{ID: "om_1", SenderName: "Example Sender", Text: "Unbound synthetic text", At: at.Add(30 * time.Second)},
		{ID: "om_2", SenderName: "Example Sender", Text: "Unbound synthetic text", At: at.Add(2 * time.Minute)},
	})
	if err != nil || n != 1 {
		t.Fatalf("added %d, want 1 (the same minute skipped, two minutes later kept): %v", n, err)
	}
}

// When a synced message has no display name (for example a bot/app sender or a binding without contact scope),
// alignment degrades to the senderless fingerprint (chat name + time window + body) so it still deduplicates against
// the gateway's named copy without matching the owner's own lines or unrelated messages.
func TestGatewayAlignerFallbackWhenSenderNameEmpty(t *testing.T) {
	e, _ := alignEngine(t)
	ctx := context.Background()
	at := alignAt()
	gw := SourceChat{ID: "f_bot", Name: "Example Team", Kind: "group"}
	if n, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{
		{ID: "g_bot", SenderID: "b1", SenderName: "Example Bot", Text: "Build succeeded", At: at},
		{ID: "g_usr", SenderID: "u1", SenderName: "Example Sender", Text: "Looks good", At: at.Add(10 * time.Second)},
		{ID: "g_me", SenderID: "u_me", SenderName: "Owner", Mine: true, Text: "Owner note", At: at.Add(20 * time.Second)},
		{ID: "g_anon", SenderID: "u2", SenderName: "", Text: "Gateway unnamed note", At: at.Add(30 * time.Second)},
		// Minute-only gateway lines (no ID -> unbound receipts).
		{SenderName: "Example Bot", Text: "Minute bot alert", At: at.Add(2 * time.Minute)},
		{SenderName: "Example Sender", Text: "", Media: []string{"img.png"}, At: at.Add(4 * time.Minute)},
	}); err != nil || n != 6 {
		t.Fatalf("gateway ingest: %d, %v", n, err)
	}

	sync := SourceChat{ID: "oc_bot", Name: "Example Team", Kind: "group"}
	n, err := e.Ingest(ctx, named("larksync"), sync, []SourceMessage{
		// Exact-time bot message with empty SenderName -> deduped against "Example Bot" and pairs crosswalk.
		{ID: "om_bot_dup", SenderID: "cli_example_bot", SenderName: "", Text: "build succeeded", At: at.Add(time.Second)},
		// Exact-time user message with empty SenderName (e.g. contact lookup failed) -> deduped against "Example Sender".
		{ID: "om_usr_dup", SenderID: "ou_1", SenderName: "", Text: "Looks good", At: at.Add(11 * time.Second)},
		// Different body from unnamed bot -> kept.
		{ID: "om_bot_new", SenderID: "cli_example_bot", SenderName: "", Text: "Build failed", At: at.Add(time.Second)},
		// Non-mine unnamed message with same text/time as owner's own line ("我") -> NOT deduped.
		{ID: "om_not_mine", SenderID: "cli_example_bot", SenderName: "", Text: "Owner note", At: at.Add(20 * time.Second)},
		// Reverse direction: gateway line had empty sender ("?"), sync has display name -> deduped.
		{ID: "om_rev_dup", SenderID: "ou_2", SenderName: "Example Sender", Text: "Gateway unnamed note", At: at.Add(31 * time.Second)},
		// Minute-only non-empty body with empty SenderName -> deduped.
		{ID: "om_min_dup", SenderID: "cli_example_bot", SenderName: "", Text: "Minute bot alert", At: at.Add(2*time.Minute + 20*time.Second)},
		// Minute-only attachment-only (empty body) with empty SenderName vs named gateway line -> kept (no collision).
		{ID: "om_min_img", SenderID: "cli_example_bot", SenderName: "", Text: "[image]", Media: []string{"ref"}, At: at.Add(4*time.Minute + 20*time.Second)},
	})
	if err != nil || n != 3 {
		t.Fatalf("sync ingest: added %d, want 3 (om_bot_new, om_not_mine, om_min_img), %v", n, err)
	}
	text := e.Store.Since("larksync-oc_bot", time.Time{}, 1<<20)
	for _, gone := range []string{"build succeeded", "Looks good", "Gateway unnamed note", "Minute bot alert"} {
		if strings.Contains(text, gone) {
			t.Errorf("duplicate %q was written to sync archive:\n%s", gone, text)
		}
	}
	for _, kept := range []string{"Build failed", "Owner note"} {
		if !strings.Contains(text, kept) {
			t.Errorf("expected %q to be kept in sync archive:\n%s", kept, text)
		}
	}
	if w := readCrosswalk(t, e.Store); w["larksync-oc_bot"] != "lark-f_bot" {
		t.Fatalf("crosswalk %v, want larksync-oc_bot -> lark-f_bot", w)
	}
}

func TestSyncMessageAsALineWouldNameIt(t *testing.T) {
	for _, c := range []struct {
		m            SourceMessage
		sender, body string
	}{
		{SourceMessage{SenderName: "Example Sender", Text: " text "}, "Example Sender", "text"},
		{SourceMessage{Mine: true, SenderName: "Owner", Text: "text"}, "我", "text"}, // i18n-ignore: archive data
		{SourceMessage{Text: "text"}, "?", "text"},
		{SourceMessage{SenderName: "  ", Text: "text"}, "?", "text"},
		{SourceMessage{SenderName: "S", Text: "look [image]", Media: []string{"ref"}}, "S", "look"},
		{SourceMessage{SenderName: "S", Text: "[file report.pdf]", Media: []string{"ref"}}, "S", ""},
		{SourceMessage{SenderName: "S", Text: "literal [image]"}, "S", "literal [image]"},
	} {
		if sender, body, _ := syncMessage(c.m); sender != c.sender || body != c.body {
			t.Errorf("%+v: %q %q, want %q %q", c.m, sender, body, c.sender, c.body)
		}
	}
}

func TestAlignDaysNearMidnight(t *testing.T) {
	zone := time.UTC
	if got := alignDays(time.Date(2026, 3, 2, 12, 0, 0, 0, zone), zone); len(got) != 1 || got[0] != "2026-03-02" {
		t.Errorf("noon: %v", got)
	}
	if got := alignDays(time.Date(2026, 3, 2, 0, 3, 0, 0, zone), zone); len(got) != 2 || got[1] != "2026-03-01" {
		t.Errorf("after midnight: %v", got)
	}
	if got := alignDays(time.Date(2026, 3, 2, 23, 58, 0, 0, zone), zone); len(got) != 2 || got[1] != "2026-03-03" {
		t.Errorf("before midnight: %v", got)
	}
}

// Pre-#15486 overlapping larksync chats (written without alignment and with empty sender names "?") are deduplicated
// once by DedupeLarkSyncChats: overlapping lines are removed from the larksync copy, .crosswalk.json is recorded,
// non-overlapping lines and all receipt IDs are preserved, and reruns (with or without the version marker) are no-ops.
func TestDedupeLarkSyncChatsBackfillIsIdempotentAndPreservesNonOverlap(t *testing.T) {
	e := testEngine(t, nil)
	ctx := context.Background()
	at := alignAt()

	// Seed Lark gateway chat.
	gw := SourceChat{ID: "f_overlap", Name: "Example Overlap Group", Kind: "group"}
	if n, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{
		{ID: "g1", SenderID: "u1", SenderName: "Example Alice", Text: "Shared announcement", At: at},
		{ID: "g2", SenderID: "u_me", SenderName: "Owner", Mine: true, Text: "Owner reply", At: at.Add(20 * time.Second)},
	}); err != nil || n != 2 {
		t.Fatalf("gateway ingest: %d, %v", n, err)
	}

	// Simulate pre-#15486 larksync ingestion (e.Align nil, SenderName empty -> written as "?"), with 2 overlapping
	// messages + 1 unique message that the gateway does not have.
	sync := SourceChat{ID: "oc_overlap", Name: "Example Overlap Group", Kind: "group"}
	if n, err := e.Ingest(ctx, named("larksync"), sync, []SourceMessage{
		{ID: "om_dup1", SenderID: "ou_a", SenderName: "", Text: "Shared announcement", At: at.Add(time.Second)},
		{ID: "om_dup2", SenderID: "ou_me", SenderName: "", Mine: true, Text: "Owner reply", At: at.Add(21 * time.Second)},
		{ID: "om_unique", SenderID: "ou_b", SenderName: "", Text: "Only in larksync", At: at.Add(40 * time.Second)},
	}); err != nil || n != 3 {
		t.Fatalf("pre-#15486 sync ingest: %d, %v", n, err)
	}

	// First pass removes the 2 duplicates, writes .crosswalk.json, preserves om_unique and all 3 receipt IDs.
	dropped := e.DedupeLarkSyncChats()
	if dropped != 2 {
		t.Fatalf("DedupeLarkSyncChats dropped %d, want 2", dropped)
	}
	text := e.Store.Since("larksync-oc_overlap", time.Time{}, 1<<20)
	if strings.Contains(text, "Shared announcement") || strings.Contains(text, "Owner reply") {
		t.Fatalf("duplicates still in larksync archive:\n%s", text)
	}
	if !strings.Contains(text, "Only in larksync") {
		t.Fatalf("non-overlapping message lost:\n%s", text)
	}
	if w := readCrosswalk(t, e.Store); w["larksync-oc_overlap"] != "lark-f_overlap" {
		t.Fatalf("crosswalk = %v, want larksync-oc_overlap -> lark-f_overlap", w)
	}
	if c, ok := e.Store.Chat("larksync-oc_overlap"); !ok || c.Messages != 1 {
		t.Fatalf("chat count = %+v, want Messages=1", c)
	}
	rIDs := receiptIDs(t, e.Store, "larksync-oc_overlap")
	for _, id := range []string{"om_dup1", "om_dup2", "om_unique"} {
		if _, ok := rIDs[id]; !ok {
			t.Errorf("receipt %s lost after dedupe", id)
		}
	}

	// Second pass is skipped via version marker (0 dropped).
	if again := e.DedupeLarkSyncChats(); again != 0 {
		t.Fatalf("second pass with marker dropped %d, want 0", again)
	}

	// Even if the version marker is removed, rerunning is inherently idempotent (0 dropped, unique line stays).
	if err := os.Remove(filepath.Join(e.Store.Dir, KindChat, larkSyncRepairFile)); err != nil {
		t.Fatal(err)
	}
	if again := e.DedupeLarkSyncChats(); again != 0 {
		t.Fatalf("rerun without marker dropped %d, want 0", again)
	}
	if !strings.Contains(e.Store.Since("larksync-oc_overlap", time.Time{}, 1<<20), "Only in larksync") {
		t.Fatal("unique line disappeared on rerun")
	}
}

// Re-ingesting a repair batch with resolved SenderNames rewrites pre-#15486 "?:" lines in place, updates Person.Name
// and unnamed DM Chat.Name, removes any line that now aligns with a gateway chat, and is idempotent on replay.
func TestIngestRepairsPre15486SenderNamesAndIsIdempotent(t *testing.T) {
	e := testEngine(t, nil)
	ctx := context.Background()
	at := alignAt()

	// Seed a gateway chat with a minute-only attachment line that could not align without a sender name.
	gw := SourceChat{ID: "f_dm", Name: "Example Group", Kind: "group"}
	if _, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{
		{ID: "g_named", SenderID: "u1", SenderName: "Example Alice", Text: "Gateway copy", At: at},
	}); err != nil {
		t.Fatal(err)
	}

	// Pre-#15486 ingestion (no Align, empty SenderName) into a group chat and an unnamed DM chat.
	grp := SourceChat{ID: "oc_grp", Name: "Example Group", Kind: "group"}
	if n, err := e.Ingest(ctx, named("larksync"), grp, []SourceMessage{
		{ID: "om_overlap", SenderID: "ou_alice", SenderName: "", Text: "Gateway copy", At: at.Add(time.Second)},
		{ID: "om_keep", SenderID: "ou_bob", SenderName: "", Text: "Sync only line", At: at.Add(30 * time.Second)},
	}); err != nil || n != 2 {
		t.Fatalf("seed grp: %d, %v", n, err)
	}
	dm := SourceChat{ID: "oc_dm", Name: "", Kind: "dm"}
	if n, err := e.Ingest(ctx, named("larksync"), dm, []SourceMessage{
		{ID: "om_dm", SenderID: "ou_carol", SenderName: "", Text: "Direct hello", At: at.Add(40 * time.Second)},
	}); err != nil || n != 1 {
		t.Fatalf("seed dm: %d, %v", n, err)
	}

	// Now enable the gateway aligner and replay repair batches with resolved SenderNames.
	e.Align = NewGatewayAligner(e.Store)
	if n, err := e.Ingest(ctx, named("larksync"), grp, []SourceMessage{
		{ID: "om_overlap", SenderID: "ou_alice", SenderName: "Example Alice", Text: "Gateway copy", At: at.Add(time.Second)},
		{ID: "om_keep", SenderID: "ou_bob", SenderName: "Example Bob", Text: "Sync only line", At: at.Add(30 * time.Second)},
	}); err != nil || n != 0 {
		t.Fatalf("repair grp: %d, %v", n, err)
	}
	if n, err := e.Ingest(ctx, named("larksync"), SourceChat{ID: "oc_dm", Name: "Example Carol", Kind: "dm"}, []SourceMessage{
		{ID: "om_dm", SenderID: "ou_carol", SenderName: "Example Carol", Text: "Direct hello", At: at.Add(40 * time.Second)},
	}); err != nil || n != 0 {
		t.Fatalf("repair dm: %d, %v", n, err)
	}

	grpText := e.Store.Since("larksync-oc_grp", time.Time{}, 1<<20)
	if strings.Contains(grpText, "Gateway copy") {
		t.Fatalf("aligned duplicate not removed during repair:\n%s", grpText)
	}
	if !strings.Contains(grpText, "Example Bob: Sync only line") || strings.Contains(grpText, "?: Sync only line") {
		t.Fatalf("sender name not repaired in grp:\n%s", grpText)
	}
	if c, _ := e.Store.Chat("larksync-oc_grp"); c.Messages != 1 {
		t.Fatalf("grp Messages = %d, want 1", c.Messages)
	}
	if p, ok := e.Store.Person(PersonID("larksync", "ou_bob")); !ok || p.Name != "Example Bob" {
		t.Fatalf("person ou_bob = %+v, want Example Bob", p)
	}
	if w := readCrosswalk(t, e.Store); w["larksync-oc_grp"] != "lark-f_dm" {
		t.Fatalf("crosswalk = %v", w)
	}

	dmText := e.Store.Since("larksync-oc_dm", time.Time{}, 1<<20)
	if !strings.Contains(dmText, "Example Carol: Direct hello") {
		t.Fatalf("dm sender not repaired:\n%s", dmText)
	}
	if c, _ := e.Store.Chat("larksync-oc_dm"); c.Name != "Example Carol" {
		t.Fatalf("dm chat name = %q, want Example Carol", c.Name)
	}

	// Idempotent replay: running the exact same repair batch again leaves text and counts unchanged.
	beforeText := e.Store.Since("larksync-oc_grp", time.Time{}, 1<<20)
	if n, err := e.Ingest(ctx, named("larksync"), grp, []SourceMessage{
		{ID: "om_overlap", SenderID: "ou_alice", SenderName: "Example Alice", Text: "Gateway copy", At: at.Add(time.Second)},
		{ID: "om_keep", SenderID: "ou_bob", SenderName: "Example Bob", Text: "Sync only line", At: at.Add(30 * time.Second)},
	}); err != nil || n != 0 {
		t.Fatalf("replay repair grp: %d, %v", n, err)
	}
	if afterText := e.Store.Since("larksync-oc_grp", time.Time{}, 1<<20); afterText != beforeText {
		t.Fatalf("replay modified archive:\nbefore:\n%s\nafter:\n%s", beforeText, afterText)
	}
}

// Source renderers differ in link notation, entity escaping and inline media placeholders. The additional match
// preserves exact receipt times, known senders, attachment counts and every meaningful word/link destination.
func TestGatewayAlignerEquivalentBodyAndAttachmentFormats(t *testing.T) {
	cases := []struct {
		name, gateway, synced string
		media                 bool
	}{
		{"link", "Read [guide](https://example.com/guide)", "Read guide (https://example.com/guide)", false},
		{"entity", "Review A &amp; B", "Review A & B", false},
		{"inline_image", "Before after", "Before [image] after", true},
		{"inline_video", "Watch this now", "Watch [video] this now", true},
	}
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprint("archived=", archived), func(t *testing.T) {
			e := testEngine(t, nil)
			ctx := context.Background()
			at := alignAt()
			gw := SourceChat{ID: "f_formats", Name: "Example formats", Kind: "group"}
			sc := SourceChat{ID: "oc_formats", Name: gw.Name, Kind: "group"}
			// Keep an existing sync chat so duplicate delivery exercises its durable per-message receipts.
			// A chat containing only duplicates intentionally stores no empty directory (covered above).
			if _, err := e.Ingest(ctx, named("larksync"), sc, []SourceMessage{
				{ID: "om_sync_only", SenderName: "Example Sender", Text: "Only in sync", At: at.Add(-time.Minute)},
			}); err != nil {
				t.Fatal(err)
			}
			var synced []SourceMessage
			for i, c := range cases {
				g := SourceMessage{ID: "gateway_" + c.name, SenderName: "Example Sender", Text: c.gateway, At: at.Add(time.Duration(i) * time.Minute)}
				m := SourceMessage{ID: "sync_" + c.name, SenderName: g.SenderName, Text: c.synced, At: g.At.Add(time.Second)}
				if c.media {
					g.Media, m.Media = []string{"gateway-ref"}, []string{"sync-ref"}
				}
				if _, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{g}); err != nil {
					t.Fatal(err)
				}
				synced = append(synced, m)
			}
			if !archived {
				e.Align = NewGatewayAligner(e.Store)
			}
			n, err := e.Ingest(ctx, named("larksync"), sc, synced)
			if err != nil {
				t.Fatal(err)
			}
			if archived {
				if n != len(cases) {
					t.Fatalf("archive seed: %d", n)
				}
				// A deployed version-1 pass must not prevent the new representation repair from running.
				marker := filepath.Join(e.Store.Dir, KindChat, larkSyncRepairFile)
				if err := os.WriteFile(marker, []byte(`{"version":1}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if n := e.DedupeLarkSyncChats(); n != len(cases) {
					t.Fatalf("format repair dropped %d, want %d", n, len(cases))
				}
				if n := e.DedupeLarkSyncChats(); n != 0 {
					t.Fatalf("format repair is not idempotent: %d", n)
				}
			} else if n != 0 {
				t.Fatalf("new equivalent messages written twice: %d", n)
			}
			for _, m := range synced {
				if _, ok := receiptIDs(t, e.Store, "larksync-oc_formats")[m.ID]; !ok {
					t.Errorf("receipt lost for %s", m.ID)
				}
			}
			if readCrosswalk(t, e.Store)["larksync-oc_formats"] != "lark-f_formats" {
				t.Fatal("format match did not preserve chat provenance")
			}
		})
	}
}

func TestGatewayAlignerFormatFallbackRefusesUncertainMatches(t *testing.T) {
	for _, name := range []string{"different_sender", "unknown_sender", "different_time", "minute_only", "different_url", "url_case", "literal_media", "media_count", "file_name", "code", "attachment_only", "ambiguous_lines", "ambiguous_chats"} {
		t.Run(name, func(t *testing.T) {
			e, _ := alignEngine(t)
			ctx := context.Background()
			at := alignAt()
			g := SourceMessage{ID: "g1", SenderName: "Example Sender", Text: "Read [guide](https://example.com/Guide)", At: at}
			m := SourceMessage{ID: "om_one", SenderName: g.SenderName, Text: "Read guide (https://example.com/Guide)", At: at.Add(time.Second)}
			switch name {
			case "different_sender":
				m.SenderName = "Another Sender"
			case "unknown_sender":
				m.SenderName = ""
			case "different_time":
				m.At = at.Add(10 * time.Second)
			case "minute_only":
				g.ID = ""
			case "different_url":
				m.Text = "Read guide (https://example.com/other)"
			case "url_case":
				m.Text = "Read guide (https://example.com/guide)"
			case "literal_media":
				g.Text, m.Text = "Before after", "Before [image] after"
			case "media_count":
				g.Text, m.Text = "Before after", "Before [image] after"
				g.Media, m.Media = []string{"one"}, []string{"one", "two"}
			case "file_name":
				m.Text += " [file different-report.pdf]"
				g.Media, m.Media = []string{"one"}, []string{"other"}
			case "code":
				g.Text, m.Text = "`A &amp; B`", "`A & B`"
			case "attachment_only":
				g.Text, m.Text = "", "[video]"
				g.Media, m.Media = []string{"one"}, []string{"other"}
			case "ambiguous_lines":
				g.Text, m.Text = "Review A &amp; B", "Review A & B"
			}
			gw := SourceChat{ID: "f_safe", Name: "Example safe", Kind: "group"}
			if _, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{g}); err != nil {
				t.Fatal(err)
			}
			if name == "ambiguous_lines" {
				g.ID, g.Text = "g2", "Review A &#38; B"
				if _, err := e.Ingest(ctx, named("lark"), gw, []SourceMessage{g}); err != nil {
					t.Fatal(err)
				}
			}
			if name == "ambiguous_chats" {
				if _, err := e.Ingest(ctx, named("lark"), SourceChat{ID: "f_other", Name: gw.Name, Kind: "group"}, []SourceMessage{g}); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := e.Ingest(ctx, named("larksync"), SourceChat{ID: "oc_safe", Name: gw.Name, Kind: "group"}, []SourceMessage{m}); err != nil || n != 1 {
				t.Fatalf("uncertain match must be retained: added=%d err=%v", n, err)
			}
			if _, err := os.Stat(filepath.Join(e.Store.Dir, KindChat, crosswalkFile)); !os.IsNotExist(err) {
				t.Fatal("uncertain match created a crosswalk")
			}
		})
	}
}
