package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/knowledgeuihost"
	"github.com/nwverse/persona/server/internal/rpc"
)

type kbImportProviderReadFixture struct {
	knowledgeuihost.Layer
	calls   int
	binding assistantwire.KBImportProvider
	day     func(assistantwire.KBDayParams) assistantwire.KBDayResult
}

func (f *kbImportProviderReadFixture) ProviderCheckpoint(_ *kb.Engine, _ *assistantwire.KBHistory, binding assistantwire.KBImportProvider, _ string, _ time.Time) (wire.KBImportCheckpointResult, bool) {
	f.calls++
	return wire.KBImportCheckpointResult{State: "running", Approval: "approved", Read: 10, Written: 7}, binding == f.binding
}

func (f *kbImportProviderReadFixture) Day(_ context.Context, _ *kb.Engine, p assistantwire.KBDayParams) (assistantwire.KBDayResult, error) {
	return f.day(p), nil
}

func TestKBProviderReadRPC(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub()
	s := kbServiceOn(t, f, newKBStore(), t.TempDir(), nil)
	b := assistantwire.KBImportProvider{Extension: "knowledge-provider", StorageID: "old-install", InstalledAt: time.Date(2026, 11, 1, 12, 1, 2, 123000, time.UTC), Connector: "example", SourceID: "account-chat", FiledAs: "archive"}
	enabled := func() *KBSettings {
		return &KBSettings{ImportProvider: &b, History: &assistantwire.KBHistory{Enabled: true, Sources: []assistantwire.KBHistorySource{{Name: b.Connector.String()}}}}
	}
	set := func(cfg *KBSettings) { f.mu.Lock(); f.hello.KB = cfg; f.mu.Unlock() }
	set(enabled())
	kbEng(t, s)
	layer := &kbImportProviderReadFixture{Layer: s.ui, binding: b}
	s.ui = layer
	caller := assistantwire.KBProviderRequest{Extension: b.Extension, StorageID: b.StorageID, InstalledAt: b.InstalledAt.Format(time.RFC3339Nano)}
	call := func(method string, who assistantwire.KBProviderRequest, params string) (any, error) {
		t.Helper()
		if err := json.Unmarshal([]byte(params), &who.Params); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(who)
		if err != nil {
			t.Fatal(err)
		}
		result, handled, err := s.kbRPCRequest(ctx, method, raw)
		if !handled {
			t.Fatalf("unhandled %s", method)
		}
		return result, err
	}
	deny := func(method string, who assistantwire.KBProviderRequest, params string) {
		t.Helper()
		out, err := call(method, who, params)
		var re *rpc.Error
		if out != nil || !errors.As(err, &re) || re.Code != "permission_denied" || re.Message != "knowledge import selection unavailable" {
			t.Fatalf("binding refusal: %+v %v", out, err)
		}
	}
	checkpoint := assistantwire.MethodKBProviderCheckpoint
	for _, tc := range []struct {
		name string
		edit func(*assistantwire.KBProviderRequest)
	}{
		{"another extension", func(r *assistantwire.KBProviderRequest) { r.Extension = "unselected" }},
		{"different storage", func(r *assistantwire.KBProviderRequest) { r.StorageID = "other" }},
		{"new installation", func(r *assistantwire.KBProviderRequest) {
			r.InstalledAt = b.InstalledAt.Add(time.Microsecond).Format(time.RFC3339Nano)
		}},
		{"invalid installation time", func(r *assistantwire.KBProviderRequest) { r.InstalledAt = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) { other := caller; tc.edit(&other); deny(checkpoint, other, "{}") })
	}
	for _, cfg := range []*KBSettings{{Off: true, ImportProvider: &b, History: enabled().History}, {ImportProvider: &b}, {History: enabled().History}} {
		set(cfg)
		deny(checkpoint, caller, "{}")
	}
	if layer.calls != 0 {
		t.Fatal("lookup before binding check")
	}
	set(enabled())
	result, err := call(checkpoint, caller, "{}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(result)
	if err != nil || string(got) != `{"state":"running","approval":"approved","read":10,"written":7}` {
		t.Fatalf("checkpoint payload %s (%v)", got, err)
	}
	markers := assistantwire.MethodKBProviderSentMarkers
	const query = `{"approval":"approved","day":"2026-11-01"}`
	deny(markers, caller, `{"approval":"superseded","day":"2026-11-01"}`)
	archive := func(q assistantwire.KBDayParams) assistantwire.KBDayResult {
		if q.ID != kb.ChatID(b.FiledAs, string(b.SourceID)) || q.Day != "2026-11-01" {
			t.Fatalf("archive lookup %+v", q)
		}
		return assistantwire.KBDayResult{SentOrigin: "available", Messages: []kb.DayMessage{{N: 2, Text: "private other person"}, {N: 3, SentBy: "assistant", SentLedgerID: 99, Text: "private archived text", RecalledAt: &b.InstalledAt}}}
	}
	layer.day = archive
	result, err = call(markers, caller, query)
	if err != nil {
		t.Fatal(err)
	}
	got, err = json.Marshal(result)
	if err != nil || string(got) != fmt.Sprintf(`{"markers":[{"n":3,"sent_by":"assistant","recalled_at":%q}]}`, caller.InstalledAt) {
		t.Fatalf("marker payload %s (%v)", got, err)
	}
	layer.day = func(q assistantwire.KBDayParams) assistantwire.KBDayResult {
		other := b
		other.SourceID = "another-account-chat"
		cfg := enabled()
		cfg.ImportProvider = &other
		set(cfg)
		return archive(q)
	}
	deny(markers, caller, query)
}

func TestKBProviderMarkerPagination(t *testing.T) {
	var rows []kb.DayMessage
	for n := 1; n <= 203; n++ {
		line := kb.DayMessage{N: n, Text: "archived data", SentLedgerID: 41}
		if n%2 == 0 {
			line.SentBy = "assistant"
		}
		rows = append(rows, line)
	}
	first := kbMarkerPage(rows, 0)
	if len(first.Markers) != 100 || first.NextAfter != 200 || first.Markers[0].N != 2 || first.Markers[99].N != 200 {
		t.Fatalf("first page %+v", first)
	}
	last := kbMarkerPage(rows, first.NextAfter)
	got, err := json.Marshal(last)
	if err != nil || string(got) != `{"markers":[{"n":202,"sent_by":"assistant"}]}` {
		t.Fatalf("last page %s (%v)", got, err)
	}
	exact := kbMarkerPage(rows[:201], 0)
	if len(exact.Markers) != 100 || exact.NextAfter != 0 {
		t.Fatal("an exactly full final page claimed a successor")
	}
	empty, _ := json.Marshal(kbMarkerPage(rows, 202))
	if string(empty) != `{"markers":[]}` {
		t.Fatalf("empty page %s", empty)
	}
}
