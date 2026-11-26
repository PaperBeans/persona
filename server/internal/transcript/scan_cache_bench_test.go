package transcript

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigClaudeTranscriptBytes builds a Claude transcript payload of about mb
// megabytes with questions, long answers, and periodic background-task markers.
func bigClaudeTranscriptBytes(mb int) []byte {
	var buf bytes.Buffer
	buf.Grow((mb << 20) + (64 << 10))
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 36)
	parent := ""
	for i := 0; buf.Len() < mb<<20; i++ {
		var line string
		id := fmt.Sprintf("u%d", i)
		switch {
		case i%40 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"question %d"}}`, id, parent, i)
		case i%997 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>t%d</task-id>\n<status>completed</status>\n</task-notification>"}}`, id, parent, i)
		case i%991 == 0:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call%d","name":"Agent","input":{"description":"look","prompt":"go"}}]}}`, id, parent, i)
		default:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"answer %d %s"}]}}`, id, parent, i, filler)
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		parent = id
	}
	return buf.Bytes()
}

// bigClaudeTranscript writes a Claude transcript of about mb megabytes: questions, long answers, and now and then
// a background task with its notification, so the scan finds seed markers as a real one does.
func bigClaudeTranscript(tb testing.TB, mb int) string {
	tb.Helper()
	p := filepath.Join(tb.TempDir(), "big.jsonl")
	fh, err := os.Create(p)
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriterSize(fh, 1<<20)
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 36)
	var written int64
	parent := ""
	for i := 0; written < int64(mb)<<20; i++ {
		var line string
		id := fmt.Sprintf("u%d", i)
		switch {
		case i%40 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"question %d"}}`, id, parent, i)
		case i%997 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>t%d</task-id>\n<status>completed</status>\n</task-notification>"}}`, id, parent, i)
		case i%991 == 0:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call%d","name":"Agent","input":{"description":"look","prompt":"go"}}]}}`, id, parent, i)
		default:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"answer %d %s"}]}}`, id, parent, i, filler)
		}
		n, _ := w.WriteString(line + "\n")
		written += int64(n)
		parent = id
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		tb.Fatal(err)
	}
	return p
}

func appendLine(tb testing.TB, p string, i int) {
	tb.Helper()
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer fh.Close()
	if _, err := fmt.Fprintf(fh, `{"type":"assistant","uuid":"x%d","timestamp":"2026-11-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"more %d"}]}}`+"\n", i, i); err != nil {
		tb.Fatal(err)
	}
}

// coldPage is what a page read costs the node when it holds no parsed File for the session: a File opened on the
// path, read, and its latest exchanges paged.
func coldPage(tb testing.TB, p string) {
	f := NewFile(p)
	if _, _, err := f.RefreshSpan(); err != nil {
		tb.Fatal(err)
	}
	if items, _, _ := f.PageTurns(0, 2, 200); len(items) == 0 {
		tb.Fatal("no items")
	}
}

// BenchmarkColdPageBigTranscript: page reads of a 96 MB transcript, each from a File opened cold, with the file at
// rest and with a line appended before each read.
func BenchmarkColdPageBigTranscript(b *testing.B) {
	p := bigClaudeTranscript(b, 96)
	b.Run("unchanged", func(b *testing.B) {
		coldPage(b, p)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			coldPage(b, p)
		}
	})
	b.Run("appended", func(b *testing.B) {
		coldPage(b, p)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			appendLine(b, p, i)
			b.StartTimer()
			coldPage(b, p)
		}
	})
}

// BenchmarkScanLinesCached measures cached scanLinesCached reads for a single
// large transcript and for round-robin rotation across 16 large transcripts.
func BenchmarkScanLinesCached(b *testing.B) {
	clear := func() {
		scanCache.mu.Lock()
		scanCache.entries = nil
		scanCache.mu.Unlock()
	}
	clear()
	b.Cleanup(clear)

	payload := bigClaudeTranscriptBytes(9)
	const fileCount = 16
	dir := b.TempDir()
	paths := make([]string, fileCount)
	handles := make([]*os.File, fileCount)
	infos := make([]os.FileInfo, fileCount)
	for i := range fileCount {
		p := filepath.Join(dir, fmt.Sprintf("bench-%02d.jsonl", i))
		if err := os.WriteFile(p, payload, 0o600); err != nil {
			b.Fatal(err)
		}
		fh, err := openTranscript(p)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = fh.Close() })
		st, err := statOpenedTranscript(fh)
		if err != nil {
			b.Fatal(err)
		}
		paths[i], handles[i], infos[i] = p, fh, st
	}

	// Warm all 16 entries once.
	for i := range fileCount {
		if _, _, _, _, _, _, _, err := scanLinesCached(paths[i], handles[i], infos[i]); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("single_file", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, _, _, _, _, _, err := scanLinesCached(paths[0], handles[0], infos[0]); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("rotate_16_files", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			idx := i % fileCount
			if _, _, _, _, _, _, _, err := scanLinesCached(paths[idx], handles[idx], infos[idx]); err != nil {
				b.Fatal(err)
			}
		}
	})
}
