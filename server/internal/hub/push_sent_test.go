package hub

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// sentPusher is a pusher on store (nil: memory only) with its clock at now, started as newPusher starts one: it reads
// the delivered pushes back (loadSent) on that clock.
func sentPusher(t *testing.T, store *lnStore, now time.Time) *Pusher {
	t.Helper()
	p := newPusher(t.TempDir(), nil, t.Logf)
	p.now = func() time.Time { return now }
	if store != nil {
		p.store = store
		p.loadSent()
	}
	return p
}

// recordAndSave records one delivered push and waits for the writer.
func recordAndSave(t *testing.T, p *Pusher, manager, level, key string, at time.Time) {
	t.Helper()
	p.recordSent(pushPolicy{manager: manager, relatedKey: key}, level, at)
	if !p.waitInterceptsSaved(5 * time.Second) {
		t.Fatal("the delivered push was not saved")
	}
}

// statsViews are what the readers of the record answer: the daily counts for every manager and for one, over the
// default day, two days and the whole 14 days.
func statsViews(p *Pusher) map[string][]pushDayStats {
	now := p.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	out := map[string][]pushDayStats{}
	for _, since := range []time.Time{today, today.AddDate(0, 0, -1), now.Add(-lnInterceptAge)} {
		for _, manager := range []string{"", "manager-a", "manager-b"} {
			out[since.Format(time.RFC3339)+"/"+manager] = p.pushDailyStats(since, manager)
		}
	}
	return out
}

// One delivered push appends one row and writes nothing else: the bytes it hands the store are the same whether the
// record is empty or holds the full 2,000 entries, and the old whole-list setting is never written (#8257).
func TestPushSentWritesOnlyTheNewRow(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	perPush := func(seeded int) (adds, bytes int) {
		store := newLnStore()
		var seed []db.PushSent
		for i := range seeded {
			seed = append(seed, db.PushSent{At: now.Add(-time.Duration(seeded-i) * time.Minute), Level: "p1",
				RelatedKey: fmt.Sprintf("session:0e000000-0000-4000-8000-%012d", i), Manager: "manager-a"})
		}
		store.sentRows = seed
		p := sentPusher(t, store, now)
		if got := len(p.sent); got != seeded {
			t.Fatalf("loaded %d delivered pushes, want %d", got, seeded)
		}
		store.sentAdds, store.sentAddBytes = 0, 0
		const pushes = 100
		for i := range pushes {
			recordAndSave(t, p, "manager-a", "p1", fmt.Sprintf("session:%d", i), now)
		}
		if _, written := store.vals[pushSentSettingKey]; written || store.puts != 0 {
			t.Fatalf("the pushes wrote settings (%d puts, %s present %v), want only appended rows", store.puts, pushSentSettingKey, written)
		}
		if store.sentAdds != pushes {
			t.Fatalf("%d appends for %d pushes, want one each", store.sentAdds, pushes)
		}
		if got, want := len(store.sentTable()), seeded+pushes; got != want {
			t.Fatalf("table rows = %d, want %d", got, want)
		}
		t.Logf("record of %d: %d pushes, %d appends, %d bytes, %d bytes/push", seeded, pushes, store.sentAdds,
			store.sentAddBytes, store.sentAddBytes/pushes)
		return store.sentAdds, store.sentAddBytes / pushes
	}
	_, empty := perPush(0)
	_, full := perPush(pushSentMax)
	if empty != full || full > 256 {
		t.Fatalf("bytes per push: %d with an empty record, %d with a full one; want the same, one small row", empty, full)
	}
}

// The readers answer the same from the table as from memory: the daily counts after a restart equal those of the
// process that recorded the pushes, and those of a pusher that kept them in memory only (the old record's answers).
func TestPushSentReadersUnchanged(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	store := newLnStore()
	p := sentPusher(t, store, now)
	memory := sentPusher(t, nil, now)
	pushes := []struct {
		manager, level, key string
		at                  time.Time
	}{
		{"manager-a", assistantwire.LevelP0, "a-1", now.Add(-time.Hour)},
		{"manager-a", assistantwire.LevelP1, "a-2", now.Add(-26 * time.Hour)},
		{"manager-b", assistantwire.LevelP2, "", now.Add(-3 * 24 * time.Hour)},
		{"", assistantwire.LevelP1, "pair", now.Add(-time.Minute)},
		{"manager-b", assistantwire.LevelP1, "b-old", now.Add(-13 * 24 * time.Hour)},
	}
	for _, e := range pushes {
		recordAndSave(t, p, e.manager, e.level, e.key, e.at)
		memory.recordSent(pushPolicy{manager: e.manager, relatedKey: e.key}, e.level, e.at)
	}
	want := statsViews(memory)
	if got := statsViews(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("live counts differ from the memory-only pusher:\n got %+v\nwant %+v", got, want)
	}
	restarted := sentPusher(t, store, now)
	if got := statsViews(restarted); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts after a restart differ:\n got %+v\nwant %+v", got, want)
	}
	h := &Hub{push: restarted}
	code, body := statsRequest(t, h, extPrincipal("manager-b"), "/api/v1/push/stats/daily?days=14")
	var out struct {
		Days []pushDayStats `json:"days"`
	}
	twoWeeks := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -13)
	err := json.Unmarshal(body, &out)
	got, _ := json.Marshal(out.Days)
	wantJSON, _ := json.Marshal(memory.pushDailyStats(twoWeeks, "manager-b"))
	if code != 200 || err != nil || string(got) != string(wantJSON) || len(out.Days) != 2 {
		t.Fatalf("an extension's stats after the restart: %d %s, want %s", code, body, wantJSON)
	}
}

// Upgrade: the entries of the old whole-list setting move into the table once, at the first start; the expired ones
// go, the counts equal what the old build read from the setting, and a second start moves nothing again.
func TestPushSentAdoptsTheOldSetting(t *testing.T) {
	now := time.Now().UTC()
	var legacy []pushSent
	for i := range 10 { // past the 14 days: the old build dropped them at load too
		legacy = append(legacy, pushSent{At: now.Add(-lnInterceptAge - time.Duration(10-i)*time.Hour), Level: "p1", Manager: "manager-a"})
	}
	for i := range pushSentMax - 10 {
		manager := "manager-a"
		if i%3 == 0 {
			manager = "manager-b"
		}
		legacy = append(legacy, pushSent{At: now.Add(-time.Duration(pushSentMax-10-i) * 9 * time.Minute), Level: "p1",
			RelatedKey: fmt.Sprintf("k-%d", i), Manager: manager})
	}
	store := newLnStore()
	if err := store.PutSetting(t.Context(), pushSentSettingKey, legacy); err != nil {
		t.Fatal(err)
	}
	// What the old build answered: loadLnState kept the setting's entries inside the 14 days.
	old := sentPusher(t, nil, now)
	for _, e := range legacy {
		if e.At.After(now.Add(-lnInterceptAge)) {
			old.sent = append(old.sent, e)
		}
	}
	want := statsViews(old)

	p := sentPusher(t, store, now)
	if _, left := store.vals[pushSentSettingKey]; left {
		t.Fatal("the old setting is still there after the move")
	}
	if rows := store.sentTable(); len(rows) != pushSentMax-10 || rows[0].RelatedKey != "k-0" || rows[len(rows)-1].RelatedKey != fmt.Sprintf("k-%d", pushSentMax-11) {
		t.Fatalf("table after the move: %d rows, want the %d unexpired entries in order", len(rows), pushSentMax-10)
	}
	if got := statsViews(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts after the upgrade differ from the old build's:\n got %+v\nwant %+v", got, want)
	}
	again := sentPusher(t, store, now)
	if rows := store.sentTable(); len(rows) != pushSentMax-10 {
		t.Fatalf("a second start changed the table: %d rows", len(rows))
	}
	if got := statsViews(again); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts after a second start differ:\n got %+v\nwant %+v", got, want)
	}
}

// The table is swept to the age and count limits: at start (past 2,000 rows, or older than 14 days) and by the writer
// at most once an hour, so it never grows past what the counts read.
func TestPushSentPrunesByAgeAndCount(t *testing.T) {
	now := time.Now().UTC()
	store := newLnStore()
	for i := range pushSentMax + 50 {
		store.sentRows = append(store.sentRows, db.PushSent{At: now.Add(-time.Duration(pushSentMax+50-i) * time.Second), Level: "p1",
			RelatedKey: fmt.Sprintf("k-%d", i), Manager: "manager-a"})
	}
	store.sentRows[60].At = now.Add(-lnInterceptAge - time.Hour) // one expired row among the newest 2,000
	p := sentPusher(t, store, now)
	rows := store.sentTable()
	if len(rows) != pushSentMax-1 || rows[0].RelatedKey != "k-50" {
		t.Fatalf("after the start sweep: %d rows from %q, want %d from k-50", len(rows), rows[0].RelatedKey, pushSentMax-1)
	}
	if len(p.sent) != pushSentMax-1 {
		t.Fatalf("memory holds %d, want the table's %d", len(p.sent), pushSentMax-1)
	}

	// Within the hour no sweep runs; the table may pass the count limit by what the hour adds.
	recordAndSave(t, p, "manager-a", "p1", "within-the-hour", now)
	recordAndSave(t, p, "manager-a", "p1", "within-the-hour-2", now)
	if got := len(store.sentTable()); got != pushSentMax+1 {
		t.Fatalf("rows within the hour = %d, want %d (no sweep yet)", got, pushSentMax+1)
	}
	if len(p.sent) != pushSentMax {
		t.Fatalf("memory holds %d, want the %d limit", len(p.sent), pushSentMax)
	}

	// Fifteen days on, the next push sweeps: everything before it is past the 14 days.
	later := now.Add(15 * 24 * time.Hour)
	p.now = func() time.Time { return later }
	recordAndSave(t, p, "manager-a", "p1", "fifteen-days-on", later)
	if rows := store.sentTable(); len(rows) != 1 || rows[0].RelatedKey != "fifteen-days-on" {
		t.Fatalf("after the hourly sweep: %d rows, want only the new push", len(rows))
	}
}
