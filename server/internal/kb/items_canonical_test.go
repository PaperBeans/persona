package kb

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

func syncRef(binding, entry string) kbwire.SourceRef {
	return kbwire.SourceRef{Channel: larksyncwire.ConnectorID, Binding: binding, Entry: entry}
}

func syncDoc(id string, rev int64, title, body string, refs ...kbwire.SourceRef) kbwire.Item {
	return kbwire.Item{Kind: KindDoc, ID: id, Title: title, URL: "https://example.feishu.cn/docx/" + id, At: time.Unix(1700000000+rev, 0).UTC(),
		CanonicalKey: larksyncwire.KeyDoc + id, Revision: rev, Content: body, BodyHash: "hash-" + body, Sources: refs}
}

func collect(t *testing.T, e *Engine, source string, items ...kbwire.Item) {
	t.Helper()
	if err := e.CollectItems(context.Background(), &connectorItemSource{items: items}, source); err != nil {
		t.Fatal(err)
	}
}

func refKeys(refs []kbwire.SourceRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Channel+"|"+r.Binding+"|"+r.Entry)
	}
	return out
}

// A document the gateway stored and the sync then reads is one item: the gateway keeps it (ID, owner, tags), both
// are its sources, and the revisions decide the body: higher replaces, lower only adds its source, equal adds its
// source unless the body changed.
func TestCollectItemsMergesCanonicalAcrossSources(t *testing.T) {
	e := testEngine(t, nil)
	tagged := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	stored := &Item{Kind: KindDoc, ID: "doxcnA", Source: "lark", Title: "Gateway title", URL: "https://example.feishu.cn/docx/doxcnA",
		Signals: []string{kbwire.SignalMentions}, Tagging: Tagging{Importance: "P1", Relevance: 70, TaggedAt: tagged}}
	if err := e.Store.PutItem(stored); err != nil {
		t.Fatal(err)
	}

	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnA", 5, "Synced title", "body five", syncRef("b1", larksyncwire.EntryDrive)))
	it, _ := e.Store.Item(KindDoc, "doxcnA")
	if it.Source != "lark" || it.Title != "Synced title" || it.Revision != 5 || it.Content != "body five" || it.CanonicalKey != "feishu_doc:doxcnA" {
		t.Fatalf("newer revision: %+v", it)
	}
	if want := []string{"lark||gateway", "larksync|b1|drive"}; !slices.Equal(refKeys(it.Sources), want) {
		t.Fatalf("sources %v, want %v", refKeys(it.Sources), want)
	}
	if it.Importance != "P1" || !it.TaggedAt.IsZero() || !slices.Equal(it.Signals, []string{kbwire.SignalMentions}) {
		t.Fatalf("tags kept, tagged again for the new title: %+v", it.Tagging)
	}

	// A lower revision arriving late: only its source.
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnA", 3, "Old title", "body three", syncRef("b2", larksyncwire.EntryWiki)))
	it, _ = e.Store.Item(KindDoc, "doxcnA")
	if it.Title != "Synced title" || it.Revision != 5 || it.Content != "body five" || len(it.Sources) != 3 {
		t.Fatalf("stale revision overwrote: %+v", it)
	}

	// The same revision and body from another binding: only its source; the item is not tagged again.
	if err := e.Store.PutItem(func() *Item { x := *it; x.TaggedAt = tagged; return &x }()); err != nil {
		t.Fatal(err)
	}
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnA", 5, "Synced title", "body five", syncRef("b3", larksyncwire.EntrySearch)))
	it, _ = e.Store.Item(KindDoc, "doxcnA")
	if len(it.Sources) != 4 || !it.TaggedAt.Equal(tagged) {
		t.Fatalf("same revision: %+v", it)
	}
	// Nothing new at all: nothing written.
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnA", 5, "Synced title", "body five", syncRef("b3", larksyncwire.EntrySearch)))
	again, _ := e.Store.Item(KindDoc, "doxcnA")
	if !slices.Equal(refKeys(again.Sources), refKeys(it.Sources)) {
		t.Fatalf("repeat changed sources: %v", refKeys(again.Sources))
	}

	// A higher revision overwrites again.
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnA", 9, "Final title", "body nine", syncRef("b1", larksyncwire.EntryDrive)))
	it, _ = e.Store.Item(KindDoc, "doxcnA")
	if it.Title != "Final title" || it.Revision != 9 || it.Source != "lark" || len(it.Sources) != 4 {
		t.Fatalf("revision 9: %+v", it)
	}
}

// The sync keys a wiki page by its document's obj_token and gives the node token as an alias; the gateway stored the
// page under the node token (its link's). And a stub the gateway wrote under another ID is found by its link's token.
func TestCollectItemsAlignsByAliasAndLinkToken(t *testing.T) {
	e := testEngine(t, nil)
	for _, it := range []Item{
		{Kind: KindDoc, ID: "wikcnNode", Source: "lark", URL: "https://example.feishu.cn/wiki/wikcnNode", Title: "Wiki page"},
		{Kind: KindDoc, ID: "gateway-stub", Source: "lark", URL: "https://example.larksuite.com/sheets/shtcnTok?sheet=1", Title: "Sheet"},
		{Kind: KindMeeting, ID: "gateway-minute", Source: "lark", URL: "https://example.feishu.cn/minutes/obcnMinute", Title: "Meeting"},
	} {
		if err := e.Store.PutItem(&it); err != nil {
			t.Fatal(err)
		}
	}
	wiki := syncDoc("doxcnObj", 2, "Wiki page v2", "wiki body", syncRef("b1", larksyncwire.EntryWiki))
	wiki.Aliases = []string{"wikcnNode"}
	sheet := syncDoc("shtcnTok", 4, "Sheet v4", "sheet body", syncRef("b1", larksyncwire.EntryDrive))
	minute := kbwire.Item{Kind: KindMeeting, ID: "obcnMinute", Title: "Meeting minutes", CanonicalKey: larksyncwire.KeyMinute + "obcnMinute",
		Revision: 1, Content: "transcript", Sources: []kbwire.SourceRef{syncRef("b1", larksyncwire.EntryMessage)}}
	collect(t, e, larksyncwire.ConnectorID, wiki, sheet, minute)

	if _, ok := e.Store.Item(KindDoc, "doxcnObj"); ok {
		t.Fatal("the wiki page was stored twice")
	}
	if it, _ := e.Store.Item(KindDoc, "wikcnNode"); it.Title != "Wiki page v2" || it.Source != "lark" || !slices.Contains(it.Aliases, "doxcnObj") || len(it.Sources) != 2 {
		t.Fatalf("wiki alias: %+v", it)
	}
	if _, ok := e.Store.Item(KindDoc, "shtcnTok"); ok {
		t.Fatal("the sheet was stored twice")
	}
	if it, _ := e.Store.Item(KindDoc, "gateway-stub"); it.Title != "Sheet v4" || it.Content != "sheet body" || it.Source != "lark" {
		t.Fatalf("link token: %+v", it)
	}
	if it, _ := e.Store.Item(KindMeeting, "gateway-minute"); it.Title != "Meeting minutes" || it.Content != "transcript" {
		t.Fatalf("minutes link token: %+v", it)
	}
	// Found again next time by the alias it now carries.
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnObj", 3, "Wiki page v3", "wiki body 3", syncRef("b2", larksyncwire.EntryDrive)))
	if it, _ := e.Store.Item(KindDoc, "wikcnNode"); it.Title != "Wiki page v3" || len(it.Sources) != 3 {
		t.Fatalf("by the alias: %+v", it)
	}
	if _, ok := e.Store.Item(KindDoc, "doxcnObj"); ok {
		t.Fatal("stored twice on the second read")
	}
}

// New canonical content is stored once with every source that handed it over in the same collection, and a source's
// own update never goes back to a lower revision.
func TestCollectItemsCanonicalNewAndSameOwner(t *testing.T) {
	e := testEngine(t, nil)
	collect(t, e, larksyncwire.ConnectorID,
		syncDoc("doxcnNew", 2, "New doc", "body", syncRef("b1", larksyncwire.EntryDrive)),
		syncDoc("doxcnNew", 2, "New doc", "body", syncRef("b2", larksyncwire.EntryDrive)))
	it, ok := e.Store.Item(KindDoc, "doxcnNew")
	if !ok || it.Source != larksyncwire.ConnectorID || it.Seen.IsZero() {
		t.Fatalf("new: %+v", it)
	}
	if want := []string{"larksync|b1|drive", "larksync|b2|drive"}; !slices.Equal(refKeys(it.Sources), want) {
		t.Fatalf("sources %v", refKeys(it.Sources))
	}
	collect(t, e, larksyncwire.ConnectorID, syncDoc("doxcnNew", 1, "Older", "old", syncRef("b1", larksyncwire.EntryDrive)))
	if it, _ := e.Store.Item(KindDoc, "doxcnNew"); it.Title != "New doc" || it.Revision != 2 {
		t.Fatalf("same owner went back: %+v", it)
	}

	// Without a canonical key, a lower revision from the owner does not overwrite either.
	if err := e.Store.PutItem(&Item{Kind: KindDoc, ID: "plain", Source: "example", Title: "Rev 4", Revision: 4}); err != nil {
		t.Fatal(err)
	}
	collect(t, e, "example", kbwire.Item{Kind: KindDoc, ID: "plain", Title: "Rev 3", Revision: 3})
	if it, _ := e.Store.Item(KindDoc, "plain"); it.Title != "Rev 4" {
		t.Fatalf("lower revision overwrote: %+v", it)
	}
	collect(t, e, "example", kbwire.Item{Kind: KindDoc, ID: "plain", Title: "Rev 5", Revision: 5})
	if it, _ := e.Store.Item(KindDoc, "plain"); it.Title != "Rev 5" || it.Revision != 5 {
		t.Fatalf("higher revision: %+v", it)
	}
}

func TestLinkToken(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.feishu.cn/docx/doxcnA":            "doxcnA",
		"https://example.larksuite.com/wiki/wikcnB?from=x": "wikcnB",
		"https://example.larkoffice.com/base/bascnC#table": "bascnC",
		"https://example.feishu.cn/minutes/obcnD/":         "obcnD",
		"https://example.com/docx/doxcnA":                  "",
		"https://feishu.cn.example.com/docx/doxcnA":        "",
		"https://example.feishu.cn/drive/folder/fldcnE":    "",
		"not a url": "",
		"":          "",
	} {
		if got := linkToken(in); got != want {
			t.Errorf("linkToken(%q) = %q, want %q", in, got, want)
		}
	}
}

// A minutes link in a message becomes a meeting stub keyed by its token, and the synced minutes with that token fill
// it in: one item, not a document stub beside a meeting.
func TestMinutesLinkBecomesAStub(t *testing.T) {
	e := testEngine(t, nil)
	if _, err := e.Ingest(context.Background(), named("feed"), SourceChat{ID: "c", Name: "Example"}, []SourceMessage{
		{ID: "m1", SenderName: "Example Sender", Text: "notes https://example.feishu.cn/minutes/obcnStub here", At: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if it, ok := e.Store.Item(KindMeeting, "obcnStub"); !ok || it.URL != "https://example.feishu.cn/minutes/obcnStub" {
		t.Fatalf("minutes stub: %+v, %v", it, ok)
	}
	if _, ok := e.Store.Item(KindDoc, "obcnStub"); ok {
		t.Fatal("a minutes link also made a document stub")
	}
	collect(t, e, "larksync", kbwire.Item{Kind: KindMeeting, ID: "obcnStub", Title: "Weekly sync", URL: "https://example.feishu.cn/minutes/obcnStub",
		At: time.Now().Add(-time.Hour).UTC(), CanonicalKey: "feishu_minute:obcnStub", Revision: 1, Content: "transcript",
		Sources: []kbwire.SourceRef{syncRef("primary~a1", "drive")}})
	meetings := 0
	for _, it := range e.Store.Items(KindMeeting) {
		if strings.Contains(it.URL, "obcnStub") {
			meetings++
			if it.Content != "transcript" {
				t.Errorf("the synced minutes did not fill the stub: %+v", it)
			}
		}
	}
	if meetings != 1 {
		t.Errorf("%d meetings for one minutes, want 1", meetings)
	}
}

// Within one collection, the names an item gains in a merge find it for the items after it: the wiki read gives the
// node token's item the document's token, and the drive read of the same document, which carries no alias, lands on it.
func TestCollectItemsCanonicalNamesFoundWithinOneCall(t *testing.T) {
	e := testEngine(t, nil)
	if err := e.Store.PutItem(&Item{Kind: KindDoc, ID: "wikcnNode", Source: "lark", URL: "https://example.feishu.cn/wiki/wikcnNode"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutItem(&Item{Kind: KindDoc, ID: "unrelated", Source: "lark", URL: "https://example.feishu.cn/docx/unrelated"}); err != nil {
		t.Fatal(err)
	}
	// An item merged by its link token first builds the index; the wiki read then adds doxcnObj to wikcnNode's names.
	first := syncDoc("other", 1, "Unrelated", "u", syncRef("b1", larksyncwire.EntryDrive))
	first.URL = "https://example.feishu.cn/docx/unrelated"
	wiki := syncDoc("doxcnObj", 2, "Wiki page", "body", syncRef("b1", larksyncwire.EntryWiki))
	wiki.Aliases = []string{"wikcnNode"}
	wiki.URL = "https://example.feishu.cn/wiki/wikcnNode"
	drive := syncDoc("doxcnObj", 2, "Wiki page", "body", syncRef("b2", larksyncwire.EntryDrive))
	drive.ID = "doxcnObj"
	collect(t, e, larksyncwire.ConnectorID, first, wiki, drive)
	if _, ok := e.Store.Item(KindDoc, "doxcnObj"); ok {
		t.Fatal("the drive read of the wiki page was stored beside it")
	}
	if it, _ := e.Store.Item(KindDoc, "wikcnNode"); len(it.Sources) != 3 || it.Title != "Wiki page" {
		t.Fatalf("wiki page: %+v", it)
	}
}

// An owner without canonical keys (the gateway) updating an item the sync merged into keeps the sync's sources, names
// and body; one that never had them is stored exactly as the owner sends it.
func TestCollectItemsLegacyUpdateKeepsCanonical(t *testing.T) {
	e := testEngine(t, nil)
	if err := e.Store.PutItem(&Item{Kind: KindMeeting, ID: "m1", Source: "lark", Title: "Old"}); err != nil {
		t.Fatal(err)
	}
	minute := kbwire.Item{Kind: KindMeeting, ID: "m1", Title: "Minutes", CanonicalKey: larksyncwire.KeyMinute + "m1", Content: "transcript",
		BodyHash: "h", Aliases: []string{"obcnM"}, Sources: []kbwire.SourceRef{syncRef("b1", larksyncwire.EntryMessage)}}
	collect(t, e, larksyncwire.ConnectorID, minute)
	collect(t, e, "lark", kbwire.Item{Kind: KindMeeting, ID: "m1", Title: "Renamed by the gateway"})
	it, _ := e.Store.Item(KindMeeting, "m1")
	if it.Title != "Renamed by the gateway" || it.Content != "transcript" || it.CanonicalKey == "" || len(it.Sources) != 2 ||
		!slices.Equal(it.Aliases, []string{"obcnM"}) {
		t.Fatalf("legacy update over a merged item: %+v", it)
	}
	if err := e.Store.PutItem(&Item{Kind: KindMeeting, ID: "m2", Source: "lark", Title: "Plain"}); err != nil {
		t.Fatal(err)
	}
	collect(t, e, "lark", kbwire.Item{Kind: KindMeeting, ID: "m2", Title: "Plain renamed"})
	if it, _ := e.Store.Item(KindMeeting, "m2"); it.Title != "Plain renamed" || it.CanonicalKey != "" || it.Sources != nil || it.Content != "" {
		t.Fatalf("plain update: %+v", it)
	}
}
