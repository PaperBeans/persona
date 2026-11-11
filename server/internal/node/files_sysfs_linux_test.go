package node

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Only use a readable, non-symlinked sysfs attribute that proves its stat size overstates its stable contents.
func sysfsFixture(path string) ([]byte, os.FileInfo, error) {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), current) {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil {
			return nil, nil, fmt.Errorf("lstat %s: %w", current, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("%s is a symlink", current)
		}
	}
	f, err := os.Open(path) // opening and reading, not permission bits, establish access
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 {
		return nil, nil, fmt.Errorf("not a regular file with a positive reported size")
	}
	isSysfs, err := fileReadIsSysfs(f)
	if err != nil || !isSysfs {
		return nil, nil, fmt.Errorf("descriptor is not verified as sysfs: %v", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !sameFileRead(before, after) {
		return nil, nil, fmt.Errorf("metadata changed during fixture read: %v", err)
	}
	if len(data) == 0 || len(data) > maxFileBytes || int64(len(data)) >= before.Size() || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, fmt.Errorf("not a short, nonempty UTF-8 attribute (reported %d, read %d)", before.Size(), len(data))
	}
	return data, before, nil
}

func TestReadFileRealLinuxSysfs(t *testing.T) {
	candidates := []string{
		"/sys/kernel/uevent_helper",
		"/sys/kernel/kexec_loaded",
		"/sys/kernel/kexec_crash_loaded",
		"/sys/kernel/mm/transparent_hugepage/enabled",
	}
	var reasons []string
	for _, path := range candidates {
		data, st, err := sysfsFixture(path)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		again, againStat, err := sysfsFixture(path)
		if err != nil || !sameFileRead(st, againStat) || !bytes.Equal(data, again) {
			reasons = append(reasons, fmt.Sprintf("%s: content or metadata was not stable", path))
			continue
		}
		n := &Node{home: t.TempDir()}
		first, firstErr := n.readFile(path, "", 0, false)
		atEnd, endErr := n.readFile(path, "", int64(len(data)), false)
		final, finalStat, finalErr := sysfsFixture(path)
		if finalErr != nil || !sameFileRead(st, finalStat) || !bytes.Equal(data, final) {
			reasons = append(reasons, fmt.Sprintf("%s: content or metadata changed during the test", path))
			continue
		}
		if firstErr != nil || endErr != nil {
			t.Fatalf("stable fixture %s: first error %v, EOF error %v", path, firstErr, endErr)
		}
		if first.Text != string(data) || first.Size != int64(len(data)) || first.Truncated || first.Next != 0 {
			t.Fatalf("stable fixture %s: first page size=%d truncated=%v next=%d", path, first.Size, first.Truncated, first.Next)
		}
		if atEnd.Text != "" || atEnd.Size != int64(len(data)) || atEnd.Truncated || atEnd.Next != 0 {
			t.Fatalf("stable fixture %s: EOF page size=%d truncated=%v next=%d", path, atEnd.Size, atEnd.Truncated, atEnd.Next)
		}
		t.Logf("verified native sysfs attribute %s: stat size %d, readable bytes %d", path, st.Size(), len(data))
		return
	}
	t.Skipf("no accessible, non-symlinked, stable short sysfs fixture: %s", strings.Join(reasons, "; "))
}
