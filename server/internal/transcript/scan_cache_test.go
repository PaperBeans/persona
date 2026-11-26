package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func resetScanCache(t *testing.T) {
	t.Helper()
	clear := func() {
		scanCache.mu.Lock()
		scanCache.entries = nil
		scanCache.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

type scanResult struct {
	Starts                      []int64
	Notif                       []notifSeed
	Bash, Ultracode, Background []int
	First                       []byte
	LastNL                      int64
}

func scanBoth(t *testing.T, p string) (cached, cold scanResult, read int64) {
	t.Helper()
	fh, err := openTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	st, err := statOpenedTranscript(fh)
	if err != nil {
		t.Fatal(err)
	}
	before := scanRead.Load()
	var r scanResult
	r.Starts, r.Notif, r.Bash, r.Ultracode, r.Background, r.First, r.LastNL, err = scanLinesCached(p, fh, st)
	if err != nil {
		t.Fatal(err)
	}
	read = scanRead.Load() - before
	var c scanResult
	c.Starts, c.Notif, c.Bash, c.Ultracode, c.Background, c.First, c.LastNL, err = scanLines(fh, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	return r, c, read
}

func sameScan(t *testing.T, step, p string) int64 {
	t.Helper()
	cached, cold, read := scanBoth(t, p)
	if !reflect.DeepEqual(cached, cold) {
		t.Fatalf("%s: the cached scan differs from a cold one (%d/%d lines, %d/%d notifications, last newline %d/%d)", step,
			len(cached.Starts), len(cold.Starts), len(cached.Notif), len(cold.Notif), cached.LastNL, cold.LastNL)
	}
	if len(cold.Notif) == 0 {
		t.Fatalf("%s: the transcript has no seeds to compare", step)
	}
	return read
}

func appendRaw(t *testing.T, p, s string) {
	t.Helper()
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// A page read of a big transcript from a File opened cold scanned the whole file every time; a scan of the same file
// now reads its last block and what was appended, and finds what a cold scan finds.
func TestScanCacheRescansOnlyTheEnd(t *testing.T) {
	resetScanCache(t)
	p := bigClaudeTranscript(t, 24)
	st, _ := os.Stat(p)
	if read := sameScan(t, "first", p); read < st.Size() {
		t.Fatalf("the first scan read %d of %d bytes", read, st.Size())
	}
	limit := int64(scanBlock + scanKeep + scanBlock)
	if read := sameScan(t, "unchanged", p); read > limit {
		t.Fatalf("an unchanged file was scanned again: %d bytes", read)
	}
	appendRaw(t, p, `{"type":"user","uuid":"n1","message":{"role":"user","content":"<task-notification>`+"\n"+`<task-id>late</task-id>"`)
	sameScan(t, "partial line", p)
	appendRaw(t, p, "}}\n")
	sameScan(t, "line completed", p)
	for i := range 3 {
		appendLine(t, p, i)
		if read := sameScan(t, fmt.Sprintf("append %d", i), p); read > limit {
			t.Fatalf("append %d: scanned %d bytes", i, read)
		}
	}
	big := make([]byte, 0, 6<<20)
	for len(big) < 6<<20 {
		big = fmt.Appendf(big, `{"type":"assistant","uuid":"g%d","message":{"role":"assistant","content":[{"type":"text","text":"%0500d"}]}}`+"\n", len(big), 0)
	}
	appendRaw(t, p, string(big))
	sameScan(t, "grown past a block", p)
}

// What changed the file other than an append is scanned from the start.
func TestScanCacheInvalidates(t *testing.T) {
	resetScanCache(t)
	p := bigClaudeTranscript(t, 12)
	sameScan(t, "first", p)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	full := int64(len(data)) - scanBlock

	if err := os.Truncate(p, int64(len(data))-4096); err != nil {
		t.Fatal(err)
	}
	if read := sameScan(t, "shrunk", p); read < full {
		t.Fatalf("a shrunk file was not scanned again (%d bytes)", read)
	}

	if err := os.WriteFile(p+".new", append(append([]byte(nil), data...), data[:1<<20]...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".new", p); err != nil {
		t.Fatal(err)
	}
	if read := sameScan(t, "replaced", p); read < full {
		t.Fatalf("a replaced file was not scanned again (%d bytes)", read)
	}

	edited := append([]byte(nil), data...)
	copy(edited[scanBlock-100:], "<task-notification>")
	fh, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.Write(append(edited, data[:1<<20]...)); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	if read := sameScan(t, "rewritten in place", p); read < full {
		t.Fatalf("a file rewritten in place was not scanned again (%d bytes)", read)
	}

	st, _ := os.Stat(p)
	now, _ := os.ReadFile(p)
	copy(now[len(now)-scanBlock-200:], "<task-notification>")
	if err := os.WriteFile(p, now, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, time.Now(), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if read := sameScan(t, "edited at the same size", p); read < full {
		t.Fatalf("a file edited at the same size was not scanned again (%d bytes)", read)
	}
}

// Pages of a File opened cold are the same with the scan cached as without, and its scan reads only the end.
func TestColdPageWithScanCache(t *testing.T) {
	resetScanCache(t)
	p := bigClaudeTranscript(t, 40)
	page := func() ([]any, Span) {
		f := NewFile(p)
		if _, _, err := f.RefreshSpan(); err != nil {
			t.Fatal(err)
		}
		items, more, span := f.PageTurns(0, 2, 200)
		older, _, _ := f.Page(items[0].Seq, 50)
		meta, err := json.Marshal(f.Meta())
		if err != nil {
			t.Fatal(err)
		}
		return []any{items, more, older, string(meta)}, span
	}
	page()
	appendLine(t, p, 1)
	before := scanRead.Load()
	got, gotSpan := page()
	if read := scanRead.Load() - before; read > 3*scanBlock {
		t.Fatalf("a cold page scanned %d bytes", read)
	}
	scanCache.mu.Lock()
	scanCache.entries = nil
	scanCache.mu.Unlock()
	want, wantSpan := page()
	if !reflect.DeepEqual(got, want) || gotSpan != wantSpan {
		t.Fatalf("a page read with the scan cached differs from a cold one: span %+v, want %+v", gotSpan, wantSpan)
	}
}

// Cold opens of one file running together while it grows give each what a cold scan of what it saw gives.
func TestScanCacheConcurrent(t *testing.T) {
	resetScanCache(t)
	p := bigClaudeTranscript(t, 12)
	var wg sync.WaitGroup
	for g := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 4 {
				f := NewFile(p)
				if _, _, err := f.RefreshSpan(); err != nil {
					t.Error(err)
					return
				}
				if items, _, _ := f.PageTurns(0, 2, 200); len(items) == 0 {
					t.Error("no items")
				}
			}
		}()
		if g%2 == 0 {
			appendLine(t, p, g)
		}
	}
	wg.Wait()
	sameScan(t, "after", p)
}

func TestScanCacheBounded(t *testing.T) {
	resetScanCache(t)
	now := time.Now()
	for i := range scanCacheFiles + 3 {
		scanEntryFor(fmt.Sprintf("/f%d", i), now.Add(time.Duration(i)*time.Second))
	}
	if n := len(scanCache.entries); n != scanCacheFiles {
		t.Fatalf("%d entries kept, want %d", n, scanCacheFiles)
	}
	if scanCache.entries["/f0"] != nil {
		t.Fatal("the least recently used entry stayed")
	}
	scanEntryFor("/late", now.Add(scanCacheIdle+time.Hour))
	if n := len(scanCache.entries); n != 1 {
		t.Fatalf("%d entries after the others were idle, want 1", n)
	}
}

// Rotating across 16 large (>2*scanBlock) transcripts must not evict cached scan
// checkpoints or trigger a second full cold rescan.
func TestScanCacheHandles16LargeFilesWithoutColdRescan(t *testing.T) {
	resetScanCache(t)
	const fileCount = 16
	payload := bigClaudeTranscriptBytes(9)
	dir := t.TempDir()
	paths := make([]string, fileCount)
	for i := range fileCount {
		p := filepath.Join(dir, fmt.Sprintf("transcript-%02d.jsonl", i))
		if err := os.WriteFile(p, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}

	scanOnce := func(p string) (int64, int64) {
		t.Helper()
		fh, err := openTranscript(p)
		if err != nil {
			t.Fatal(err)
		}
		defer fh.Close()
		st, err := statOpenedTranscript(fh)
		if err != nil {
			t.Fatal(err)
		}
		before := scanRead.Load()
		if _, _, _, _, _, _, _, err := scanLinesCached(p, fh, st); err != nil {
			t.Fatal(err)
		}
		return scanRead.Load() - before, st.Size()
	}

	// Pass 1: populate scanCache for all 16 files with one cold scan each.
	for i, p := range paths {
		read, size := scanOnce(p)
		if read < size {
			t.Fatalf("cold pass file %d read %d of %d bytes", i, read, size)
		}
	}
	scanCache.mu.Lock()
	cachedCount := len(scanCache.entries)
	scanCache.mu.Unlock()
	if cachedCount != fileCount {
		t.Fatalf("cached %d files after cold pass, want %d", cachedCount, fileCount)
	}

	// Pass 2: rotate across all 16 unchanged files; each must reuse its cached
	// checkpoint and read only the tail block.
	tailLimit := int64(scanBlock + scanKeep)
	for i, p := range paths {
		read, size := scanOnce(p)
		if read > tailLimit {
			t.Fatalf("second pass file %d triggered cold rescan: read %d bytes (size %d, limit %d)", i, read, size, tailLimit)
		}
	}

	// Pass 3: append a line to each file and rotate again; still no cold rescan,
	// and the cached scan matches a cold scan byte-for-byte.
	for i, p := range paths {
		appendLine(t, p, i)
		read, size := scanOnce(p)
		if read > tailLimit {
			t.Fatalf("appended pass file %d triggered cold rescan: read %d bytes (size %d, limit %d)", i, read, size, tailLimit)
		}
	}
	if read := sameScan(t, "post-rotation parity", paths[0]); read > tailLimit {
		t.Fatalf("parity scan triggered cold rescan: read %d bytes (limit %d)", read, tailLimit)
	}
}
