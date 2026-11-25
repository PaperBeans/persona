package node

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A folder an Agent sends to this computer (docs/protocol/file-transfer.md "Agents and the assistant") comes as the
// zip file.pack made on the other computer (transfer_pack.go): every entry under one top folder, whose name is not
// used (the upload's name is). Once its bytes are in and hash to what was sent (upload.go endFile), it is unpacked
// into a hidden folder beside where it goes and only then given its name, so nothing half-unpacked ever shows under
// it. Nothing in it can leave that folder: an absolute name, a "..", a link or a special file is never made (a link
// or a special file is left out and counted, a name out of bounds refuses the whole zip), and it is held to the
// files and bytes a folder may have.

// errUnsafeEntry: a zip entry would land outside its folder.
func errUnsafeEntry(name string) error {
	return fmt.Errorf("the folder's zip has an entry outside it (%q); nothing was unpacked", name)
}

// zipEntryPath is a zip entry's path inside its top folder (slash-separated, clean; "" for the top folder itself), or
// false when it is not one an unpack may make: absolute, with a "..", a backslash or a NUL, on Windows a ":" (a drive,
// or a file's alternate stream), or not under a top folder.
func zipEntryPath(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") ||
		runtime.GOOS == "windows" && strings.Contains(name, ":") {
		return "", false
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == ".." || part == "." || part == "" {
			return "", false
		}
	}
	top, rest, _ := strings.Cut(strings.TrimSuffix(name, "/"), "/")
	if top == "" {
		return "", false
	}
	if rest == "" {
		if !strings.HasSuffix(name, "/") {
			return "", false // a file beside the top folder
		}
		return "", true
	}
	return path.Clean(rest), true
}

// unpackStaging unpacks the zip at zipPath into a new hidden folder in dir and answers it, with the files made and the
// entries left out.
func unpackStaging(zipPath, dir string) (staging string, files, skipped int, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("the folder's zip cannot be read: %w", err)
	}
	defer zr.Close()
	// Every name is checked before anything is made.
	for _, f := range zr.File {
		if _, ok := zipEntryPath(f.Name); !ok {
			return "", 0, 0, errUnsafeEntry(f.Name)
		}
	}
	staging, err = os.MkdirTemp(dir, ".persona-unpack-")
	if err != nil {
		return "", 0, 0, err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		_ = os.RemoveAll(staging)
		return "", 0, 0, err
	}
	var total int64
	for _, f := range zr.File {
		rel, _ := zipEntryPath(f.Name)
		if rel == "" {
			continue
		}
		dst := filepath.Join(staging, filepath.FromSlash(rel))
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = os.MkdirAll(dst, 0o755)
		case !mode.IsRegular(): // links, devices, pipes: never made
			skipped++
			continue
		default:
			if files++; files > maxZipFiles {
				err = &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("more than %d files to unpack", maxZipFiles)}
				break
			}
			var n int64
			n, err = unpackFile(f, dst, maxZipBytes-total)
			total += n
		}
		if err != nil {
			_ = os.RemoveAll(staging)
			return "", 0, 0, err
		}
	}
	return staging, files, skipped, nil
}

// unpackFile writes one zip entry to dst (a new file), at most room bytes; a longer one is CodeTooLarge whatever its
// header says. The permissions keep the owner's read and write and an executable's x, nothing else (no setuid).
func unpackFile(f *zip.File, dst string, room int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	perm := (f.Mode().Perm() | 0o600) & 0o755
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		_ = out.Close()
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(rc, room+1))
	_ = rc.Close()
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > room {
		err = &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("more than %d GB to unpack", maxZipBytes>>30)}
	}
	if err == nil && !f.Modified.IsZero() {
		_ = os.Chtimes(dst, f.Modified, f.Modified)
	}
	return n, err
}

// placeUnpacked gives the unpacked folder staging (in dir) the name it goes under, by conflict: "" only when the name
// is free (CodeExists otherwise); keep the first free of "name", "name 2"… (the whole name numbered, not split at a dot); replace merged into the folder of that
// name, its files of the same name replaced. Answers where it is.
func placeUnpacked(staging, dir, name, conflict string) (string, error) {
	target := filepath.Join(dir, name)
	switch conflict {
	case proto.UploadConflictKeep:
		for i := 1; i < 1000; i++ {
			n := name
			if i > 1 {
				n = fmt.Sprintf("%s %d", name, i) // a folder's whole name, as Finder numbers one: "v1.2 2"
			}
			p := filepath.Join(dir, n)
			err := renameExcl(staging, p)
			if err == nil {
				return p, nil
			}
			if !errors.Is(err, fs.ErrExist) {
				return "", err
			}
		}
		return "", fmt.Errorf("no free name for %s", name)
	case proto.UploadConflictReplace:
		st, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if !st.IsDir() { // a file, or a link: never followed
			return "", errNotAFolder(name)
		}
		return target, mergeUnpacked(staging, target)
	}
	if err := renameExcl(staging, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", errExists(name)
		}
		return "", err
	}
	return target, nil
}

// mergeUnpacked moves what staging holds into the folder target, replacing a file of the same name. Every folder on
// the way is looked at without following links: one that is a link or a file stops the merge there (what was moved
// stays), so nothing is ever written through a link.
func mergeUnpacked(staging, target string) error {
	return filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(staging, p)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(target, rel)
		st, lerr := os.Lstat(dst)
		if d.IsDir() {
			switch {
			case lerr == nil && st.IsDir():
				return nil
			case lerr == nil:
				return errNotAFolder(filepath.ToSlash(rel))
			case !errors.Is(lerr, fs.ErrNotExist):
				return lerr
			}
			return os.Mkdir(dst, 0o755)
		}
		if lerr == nil && st.IsDir() {
			return errExists(filepath.ToSlash(rel))
		}
		return os.Rename(p, dst)
	})
}

// finishUnpack unpacks a folder's zip (an upload begun with unpack) under the upload's name in its folder, by its
// conflict policy. Like publish it is the commit point: an abort that came first wins, one that comes later changes
// nothing.
func (u *uploads) finishUnpack(id string, up *upload) (proto.UploadedFile, error) {
	part := up.f.Name()
	dir := filepath.Dir(part)
	staging, files, skipped, err := unpackStaging(part, dir)
	if err != nil {
		return proto.UploadedFile{}, err
	}
	defer os.RemoveAll(staging) // gone once placed; left only by a failure (or what a merge did not take)
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.retired {
		return proto.UploadedFile{}, errUploadRetired
	}
	placed, err := placeUnpacked(staging, dir, up.name, up.conflict)
	if err != nil {
		return proto.UploadedFile{}, err
	}
	u.retireLocked(id, up)
	return proto.UploadedFile{Path: filepath.Join(up.shown, filepath.Base(placed)), Size: up.size, Files: files, Skipped: skipped}, nil
}
