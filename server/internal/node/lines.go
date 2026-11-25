package node

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"sync"
)

// Reading transcripts line by line, forward (what a file gained since the last read) and backward (its latest lines
// first, stopping once the caller has what it looks for). The session list does this every few seconds for many
// files, so the buffers are reused.

// lineReaders are eachLine's 1 MiB buffers.
var lineReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 1<<20) }}

// eachLine calls fn for every complete line and returns the bytes those lines take (a partial last line is left
// for the next scan).
func eachLine(r io.Reader, fn func([]byte)) int64 {
	return eachLineAt(r, func(line []byte, _, _ int64) bool { fn(line); return true })
}

// eachLineAt is eachLine telling fn where in r each line starts and how long it is (with its newline), and stopping
// after the line fn returns false for (the bytes returned include that line). Lines longer than usageMaxLine are
// skipped, but counted.
func eachLineAt(r io.Reader, fn func(line []byte, at, size int64) bool) int64 {
	return scanLines(r, fn)
}

// withLineAt calls fn with the line of size bytes at off in r (one eachLineAt gave).
func withLineAt(r io.ReaderAt, off, size int64, fn func([]byte)) {
	buf := make([]byte, size)
	if n, err := r.ReadAt(buf, off); int64(n) == size || err == nil {
		fn(bytes.TrimSpace(buf[:n]))
	}
}

func scanLines(r io.Reader, fn func(line []byte, at, size int64) bool) int64 {
	br := lineReaders.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() {
		br.Reset(nil)
		lineReaders.Put(br)
	}()
	var done, pending int64 // pending: the start of a line longer than the buffer
	var long []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			if !skipping && len(long)+len(chunk) <= usageMaxLine {
				long = append(long, chunk...)
			} else {
				skipping, long = true, long[:0]
			}
			pending += int64(len(chunk))
			continue
		}
		if err != nil { // EOF: whatever is left is an unfinished line
			return done
		}
		at := done
		done += pending + int64(len(chunk))
		if len(long)+len(chunk) > usageMaxLine {
			skipping = true
		}
		line := chunk
		if len(long) > 0 && !skipping {
			line = append(long, chunk...)
		}
		if !skipping && !fn(bytes.TrimSpace(line), at, done-at) {
			return done
		}
		long, skipping, pending = long[:0], false, 0
	}
}

// backChunk is how much eachLineBack reads at a time (more for a line that does not fit).
const backChunk = 64 << 10

// backBufs are eachLineBack's buffers; one grown past backBufKeep for a long line is not kept.
var backBufs = sync.Pool{New: func() any { b := make([]byte, 0, 2*backChunk); return &b }}

const backBufKeep = 1 << 20

// eachLineBack calls fn for the complete lines of [lo, hi) in r, the last one first, until fn returns false, and
// returns where the complete lines end: just past the last newline (lo when there is none). What follows it is a line
// still being written, as eachLine leaves it. lo must be where a line starts. Lines of maxLine bytes or more (with
// their newline) are skipped, as eachLine skips them. A read error ends it early.
func eachLineBack(r io.ReaderAt, lo, hi int64, maxLine int, fn func([]byte) bool) int64 {
	return eachLineBackAt(r, lo, hi, maxLine, func(line []byte, _ int64) bool { return fn(line) })
}

// eachLineBackAt also gives the byte offset where the complete line starts.
func eachLineBackAt(r io.ReaderAt, lo, hi int64, maxLine int, fn func([]byte, int64) bool) int64 {
	bp := backBufs.Get().(*[]byte)
	buf := (*bp)[:0]
	defer func() {
		if cap(buf) <= backBufKeep {
			*bp = buf[:0]
			backBufs.Put(bp)
		}
	}()
	end := int64(-1)  // found with the last newline
	pos := hi         // [lo, pos) is not read yet; buf holds [pos, pos+len(buf)): the end of a line starting earlier
	skipping := false // that line is too long: its bytes are dropped until its start is found
	for pos > lo {
		n := min(max(int64(backChunk), int64(len(buf))), pos-lo) // at least double what is held
		carry := len(buf)
		if need := int(n) + carry; cap(buf) < need {
			grown := make([]byte, need)
			copy(grown[n:], buf)
			buf = grown
		} else {
			buf = buf[:need]
			copy(buf[n:], buf[:carry])
		}
		if _, err := r.ReadAt(buf[:n], pos-n); err != nil && !errors.Is(err, io.EOF) {
			return max(end, lo)
		}
		pos -= n
		data := buf
		for {
			k := bytes.LastIndexByte(data, '\n')
			if k < 0 {
				break
			}
			line := data[k+1:]
			switch {
			case end < 0:
				end = pos + int64(k) + 1 // what follows is a line still being written
			case skipping || len(line)+1 > maxLine:
			case !fn(bytes.TrimSpace(line), pos+int64(k)+1):
				return end
			}
			skipping = false
			data = data[:k]
		}
		buf = data
		if len(buf)+1 > maxLine {
			skipping, buf = true, buf[:0]
		}
	}
	if end < 0 {
		return lo
	}
	if !skipping { // the range's first line
		fn(bytes.TrimSpace(buf), lo)
	}
	return end
}
