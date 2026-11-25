package extension

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Snapshots of an extension's data directory taken before the hub upgrades it by itself (docs/extensions/extensions.md
// "Auto-upgrade", "Backup before an upgrade"): <dir>/backups/<storage key>/<UTC time>-<version>.tar.gz, the newest
// BackupsKept of them. An uninstall that does not keep the data removes them with it.

// BackupsKept is how many snapshots of one extension's data are kept.
const BackupsKept = 3

const backupSuffix = ".tar.gz"

// StorageKeyOf is the stable key of extension id's directories and rows (its id when it is not installed).
func (r *Registry) StorageKeyOf(id string) string { return r.storageKey(id) }

// BackupDir is where the snapshots of extension id's data are kept.
func (r *Registry) BackupDir(id string) string {
	return filepath.Join(r.dir, "backups", r.storageKey(id))
}

// Backups are the snapshots of id's data, newest first.
func (r *Registry) Backups(id string) []string {
	dir := r.BackupDir(id)
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), backupSuffix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out
}

// BackupData snapshots id's data directory (it is the data of version), keeps the newest BackupsKept and says where
// the archive is and how many regular files it holds. Without a data directory there is nothing to keep: the path is
// "" and there is no error. Nothing is left behind when it fails.
func (r *Registry) BackupData(id, version string) (path string, files int, err error) {
	src := r.DataDir(id)
	if info, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return "", 0, nil
	} else if err != nil {
		return "", 0, err
	} else if !info.IsDir() {
		return "", 0, fmt.Errorf("%s is not a directory", src)
	}
	dir := r.BackupDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	name := r.now().UTC().Format("20060102T150405.000000000Z") + "-" + safeVersion(version) + backupSuffix
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone once renamed
	files, err = writeTarGz(tmp, src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	path = filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", 0, err
	}
	r.pruneBackups(id)
	return path, files, nil
}

// pruneBackups deletes all but the newest BackupsKept snapshots of id's data.
func (r *Registry) pruneBackups(id string) {
	all := r.Backups(id)
	for _, old := range all[min(len(all), BackupsKept):] {
		_ = os.Remove(old)
	}
}

// RestoreData puts id's data directory back as the archive at backup (one of BackupData's) has it. The directory is
// replaced whole: what the newer version wrote goes. The program of id must not be running. An empty backup (there
// was no data directory) removes the directory the newer version made.
func (r *Registry) RestoreData(id, backup string) error {
	dst := r.DataDir(id)
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if backup == "" {
		return os.RemoveAll(dst)
	}
	if filepath.Dir(backup) != r.BackupDir(id) {
		return fmt.Errorf("backup %s is not one of %s", backup, id)
	}
	staged, err := os.MkdirTemp(parent, ".restore-"+filepath.Base(dst)+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staged) }()
	if err := readTarGz(backup, staged); err != nil {
		return err
	}
	aside := staged + ".old"
	if err := os.Rename(dst, aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Rename(aside, dst)
		return err
	}
	return os.RemoveAll(aside)
}

// safeVersion is version as part of a file name.
func safeVersion(v string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, v)
}

// writeTarGz writes the tree under root to w: directories, regular files and symbolic links (as links, not followed).
func writeTarGz(w io.Writer, root string) (files int, err error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		case info.IsDir(), info.Mode().IsRegular():
		default:
			return nil // sockets, pipes: nothing to keep
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.CopyN(tw, f, info.Size()); err != nil {
			return err
		}
		files++
		return nil
	})
	if cerr := tw.Close(); err == nil {
		err = cerr
	}
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	return files, err
}

// readTarGz unpacks the archive at path into the empty directory dst. Names that would leave dst are refused, and links
// are made last, so that nothing is written through one.
func readTarGz(path, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	type link struct{ at, to string }
	var links []link
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(hdr.Name, "/"))
		if name == "" || !filepath.IsLocal(name) {
			return fmt.Errorf("backup entry %q leaves the data directory", hdr.Name)
		}
		at := filepath.Join(dst, name)
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(at, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			links = append(links, link{at, hdr.Linkname})
		default:
			return fmt.Errorf("backup entry %q: unexpected type %c", hdr.Name, hdr.Typeflag)
		}
	}
	for _, l := range links {
		for _, other := range links { // a link under another link would be made through it
			if strings.HasPrefix(l.at, other.at+string(filepath.Separator)) {
				return fmt.Errorf("backup entry %q is under a link", l.at)
			}
		}
		if err := os.MkdirAll(filepath.Dir(l.at), 0o700); err != nil {
			return err
		}
		if err := os.Symlink(l.to, l.at); err != nil {
			return err
		}
	}
	return nil
}
