package node

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Files sent from the phone to an agent (docs/protocol/protocol.md "Sending files"): the hub streams the upload in chunks
// (file.upload.begin / chunk / end; one JSON line per chunk, never the whole file), the node writes it to a hidden
// part file in the temp directory and, once every byte is there, renames it to the original name plus a timestamp.
// Apps that know dest=attachment (#8361) send chat files to be kept instead (beginAttachment), and the message carries
// each path in a <persona-file> tag.

// uploadIdle is how long an upload may go without a chunk before its part file is removed (the hub died mid-way).
const uploadIdle = 2 * time.Minute

type uploads struct {
	mu   sync.Mutex
	open map[string]*upload
}

// uploadFile is the part file owned by one upload operation at a time.
type uploadFile interface {
	Write([]byte) (int, error)
	Close() error
	Name() string
}

type upload struct {
	// mu protects lifecycle state; busy grants one caller exclusive file ownership.
	// Lock it before uploads.mu; the registry lock never covers disk I/O.
	mu      sync.Mutex
	busy    bool
	retired bool
	epoch   uint64
	closed  bool // only the file owner closes it

	name    string // as the phone sent it
	size    int64
	written int64
	f       uploadFile
	idle    *time.Timer
	random  bool // the final name starts with a random prefix instead of ending in a timestamp (terminal pastes)
	// into: a folder the app is browsing (dest=dir): the file keeps its own name there, shown is that folder as the
	// app knows it, conflict what a taken name does (proto.UploadConflict*).
	into     bool
	shown    string
	conflict string
	// want: the hex SHA-256 the bytes must hash to (an Agent's transfer, docs/protocol/file-transfer.md "Agents and the
	// assistant"), checked before the file gets its name; hash hashes them as they are written.
	want string
	hash hash.Hash
	// unpack: the bytes are a zip of a folder (file.pack), unpacked under the upload's name instead of kept as a file
	// (transfer_unpack.go).
	unpack bool
}

// begin opens an upload into the temp directory (the chat's "Send file").
func (u *uploads) begin(name string, size int64) (string, error) {
	return u.beginIn(os.TempDir(), name, size, proto.MaxUploadBytes, false)
}

// beginIn opens an upload whose part file and final file live in dir (one directory, so the rename never crosses
// file systems). random: the final name is <random>-<clean name>, which nobody can guess from the file name.
func (u *uploads) beginIn(dir, name string, size, limit int64, random bool) (string, error) {
	if size < 0 {
		return "", fmt.Errorf("size is required")
	}
	if size > limit {
		return "", &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("file is larger than %d MB", limit>>20)}
	}
	f, err := os.CreateTemp(dir, ".persona-upload-*") // 0600
	if err != nil {
		return "", err
	}
	id := randHex(8)
	up := &upload{name: name, size: size, f: f, random: random}
	up.mu.Lock()
	u.mu.Lock()
	if u.open == nil {
		u.open = map[string]*upload{}
	}
	u.open[id] = up
	u.mu.Unlock()
	u.armLocked(id, up)
	up.mu.Unlock()
	return id, nil
}

var errUploadRetired = errors.New("no such upload (it timed out or was cancelled)")

func (u *uploads) lookup(id string) *upload {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.open[id]
}

// expect has upload id check that its bytes hash to want (hex SHA-256) before it is given its name.
func (u *uploads) expect(id, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if b, err := hex.DecodeString(want); err != nil || len(b) != sha256.Size {
		return fmt.Errorf("sha256 must be 64 hex digits")
	}
	up := u.lookup(id)
	if up == nil {
		return errUploadRetired
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.written > 0 {
		return fmt.Errorf("the upload has begun")
	}
	up.want, up.hash = want, sha256.New()
	return nil
}

// start keeps the entry discoverable while granting exclusive file ownership.
// Concurrent chunks/end fail as before, without retiring the active operation.
func (u *uploads) start(id string) (*upload, error) {
	up := u.lookup(id)
	if up == nil {
		return nil, errUploadRetired
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.retired {
		return nil, errUploadRetired
	}
	if up.busy {
		return nil, fmt.Errorf("upload is busy")
	}
	up.busy = true
	up.stopIdleLocked()
	return up, nil
}

func (up *upload) stopIdleLocked() {
	up.epoch++ // an already-running callback belongs to the previous idle period
	if up.idle != nil {
		up.idle.Stop()
		up.idle = nil
	}
}

func (u *uploads) armLocked(id string, up *upload) {
	up.stopIdleLocked()
	epoch := up.epoch
	up.idle = time.AfterFunc(uploadIdle, func() { u.expire(id, up, epoch) })
}

// retireLocked never restores an entry. The busy file owner performs cleanup
// after its I/O settles; otherwise cancellation owns cleanup itself.
func (u *uploads) retireLocked(id string, up *upload) {
	up.retired = true
	up.stopIdleLocked()
	u.mu.Lock()
	if u.open[id] == up {
		delete(u.open, id)
	}
	u.mu.Unlock()
}

func (u *uploads) expire(id string, up *upload, epoch uint64) {
	up.mu.Lock()
	if up.retired || up.epoch != epoch {
		up.mu.Unlock()
		return
	}
	u.retireLocked(id, up)
	cleanup := !up.busy
	up.mu.Unlock()
	if cleanup {
		up.discard()
	}
}

func (u *uploads) chunk(id string, offset int64, data []byte) (got int64, err error) {
	up, err := u.start(id)
	if err != nil {
		return 0, err
	}
	defer func() {
		up.mu.Lock()
		up.busy = false
		if up.retired && err == nil {
			got, err = 0, errUploadRetired
		}
		if err != nil {
			u.retireLocked(id, up)
		} else {
			u.armLocked(id, up)
		}
		cleanup := up.retired
		up.mu.Unlock()
		if cleanup {
			up.discard()
		}
	}()
	if offset != up.written {
		return 0, fmt.Errorf("chunk at %d, expected %d", offset, up.written)
	}
	if int64(len(data)) > up.size-up.written {
		return 0, fmt.Errorf("more data than the %d bytes announced", up.size)
	}
	n, err := up.f.Write(data)
	if err != nil {
		return 0, err
	}
	if n != len(data) {
		return 0, io.ErrShortWrite
	}
	if up.hash != nil {
		up.hash.Write(data)
	}
	up.written += int64(n)
	return up.written, nil
}

// beginInto opens an upload into a folder of the home (dest=dir) under the file's own name, shortened as a numbered
// copy's is when the file system cannot take it. With no conflict policy a taken name is refused here already, before
// any byte is sent.
func (n *Node) beginInto(dir, cwd, name, conflict string, size, limit int64) (string, error) {
	return n.beginIntoAs(dir, cwd, name, conflict, size, limit, false)
}

// beginIntoAs is beginInto for a file, or with unpack for a folder's zip to unpack under name: a taken name is then
// refused unless conflict says otherwise (keep: "name 2"; replace: merged into the folder of that name, never in
// place of a file or a link).
func (n *Node) beginIntoAs(dir, cwd, name, conflict string, size, limit int64, unpack bool) (string, error) {
	switch conflict {
	case "", proto.UploadConflictReplace, proto.UploadConflictKeep:
	default:
		return "", fmt.Errorf("unknown conflict %q", conflict)
	}
	shown, real, err := n.writableDir(dir, cwd)
	if err != nil {
		return "", err
	}
	clean := cleanName(name)
	if _, err := os.Lstat(filepath.Join(real, clean)); errors.Is(err, syscall.ENAMETOOLONG) {
		stem, ext := splitName(clean)
		clean = stem + ext
		if _, err := os.Lstat(filepath.Join(real, clean)); errors.Is(err, syscall.ENAMETOOLONG) {
			return "", err // still too long for this file system (a lower limit, or the whole path)
		}
	}
	if err := n.writable(filepath.Join(real, clean)); err != nil {
		return "", err
	}
	if st, err := os.Lstat(filepath.Join(real, clean)); err == nil {
		switch {
		case conflict == "":
			return "", errExists(clean)
		case conflict == proto.UploadConflictReplace && !unpack && st.IsDir():
			return "", errExists(clean)
		case conflict == proto.UploadConflictReplace && unpack && !st.IsDir(): // a file, or a link (Lstat: never followed)
			return "", errNotAFolder(clean)
		}
	}
	id, err := n.ups.beginIn(real, clean, size, limit, false)
	if err != nil {
		return "", err
	}
	if up := n.ups.lookup(id); up != nil {
		up.mu.Lock()
		up.into, up.shown, up.conflict, up.unpack = true, shown, conflict, unpack
		up.mu.Unlock()
	}
	return id, nil
}

// errNotAFolder: a folder is to be merged into name, which is a file or a link.
func errNotAFolder(name string) error {
	data, _ := json.Marshal(map[string]any{"name": name})
	return &rpc.Error{Code: proto.CodeExists, Message: fmt.Sprintf("%s already exists and is not a folder", name), Data: data}
}

// expandTarget is a target path as an upload names it ("~", "~/…" or absolute) with ~ the home, clean.
func (n *Node) expandTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target != "~" && !strings.HasPrefix(target, "~/") && !filepath.IsAbs(target) {
		return "", fmt.Errorf("the path must be absolute or start with ~/: %q", target)
	}
	if n.home == "" {
		return "", errReadOnly(target)
	}
	if target == "~" || strings.HasPrefix(target, "~/") {
		return filepath.Join(n.home, strings.TrimPrefix(target, "~")), nil
	}
	return filepath.Clean(target), nil
}

// makeWritableDir makes the folder want (absolute) and those missing on the way, only where a client may write:
// checked before anything is made, with the links of the part that exists resolved (realPath answers the rest as
// named), so a folder out of bounds is never created, not even empty.
func (n *Node) makeWritableDir(want string) error {
	real, err := n.realPath(want)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return explainDenied(want, n.home, err)
	}
	if err := n.writable(real); err != nil {
		return err
	}
	return os.MkdirAll(real, 0o755)
}

// agentStartupPaths are what the shell, the desktop or git run by themselves, relative to the home (folded): an
// Agent's transfer (dest=path) never writes one, nor a folder holding one, so a file sent to another computer does
// not become a program that runs there. The write guard already keeps ~/.ssh, ~/Library (LaunchAgents) and Persona's
// own folders out.
var agentStartupPaths = []string{
	".bashrc", ".bash_profile", ".bash_login", ".bash_logout", ".profile", ".zshrc", ".zshenv", ".zprofile", ".zlogin",
	".zlogout", ".cshrc", ".tcshrc", ".login", ".logout", ".xprofile", ".xinitrc", ".xsession", ".gitconfig",
	".config/autostart", ".config/systemd", ".config/fish", ".config/environment.d", ".config/git", ".local/share/systemd",
	".config/powershell", "documents/powershell", "documents/windowspowershell",
}

// agentWritable refuses an Agent's transfer target (absolute, clean) that is, is in or holds one of
// agentStartupPaths.
func (n *Node) agentWritable(target string) error {
	real, err := n.realPath(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return explainDenied(target, n.home, err)
	}
	for _, p := range []string{fold(target), fold(real)} {
		rel, ok := homeRel(p, n.home)
		if !ok {
			continue
		}
		for _, s := range agentStartupPaths {
			if rel == s || strings.HasPrefix(rel, s+"/") || strings.HasPrefix(s, rel+"/") {
				return &rpc.Error{Code: proto.CodeReadOnly, Message: fmt.Sprintf("%s is a file the shell or the desktop runs by itself; files sent between computers do not go there", target)}
			}
		}
	}
	return nil
}

// beginAt opens an upload to the path target (dest=path, an Agent's transfer): "~/" or absolute; an existing folder
// (or one named with a final "/") takes it under name, anything else is its new path. makeDirs makes the folders
// missing on the way; unpack and conflict are beginIntoAs's.
func (n *Node) beginAt(target, name, conflict string, size int64, makeDirs, unpack bool) (string, error) {
	want, err := n.expandTarget(target)
	if err != nil {
		return "", err
	}
	folder := strings.HasSuffix(strings.TrimSpace(target), "/") || strings.TrimSpace(target) == "~"
	if real, err := n.realPath(want); err == nil {
		if st, err := os.Stat(real); err == nil && st.IsDir() {
			folder = true
		}
	}
	dir, base := want, name
	if !folder {
		dir, base = filepath.Dir(want), filepath.Base(want)
	}
	if err := n.agentWritable(filepath.Join(dir, cleanName(base))); err != nil {
		return "", err
	}
	if makeDirs {
		if err := n.makeWritableDir(dir); err != nil {
			return "", err
		}
	}
	return n.beginIntoAs(dir, "", base, conflict, size, proto.MaxAgentTransferBytes, unpack)
}

// beginInbox opens an upload into the machine's receive folder (dest=inbox, docs/protocol/file-transfer.md): dir is the
// folder the hub's config names for this machine ("": proto.DefaultInboxDir), "~/" or absolute, and only a folder
// the phone may write to (writeguard.go). It is made when missing. The file keeps its name; a taken one gets " 2",
// " 3"… (conflict keep), so nothing there is ever replaced.
func (n *Node) beginInbox(dir, name string, size int64, unpack bool) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = proto.DefaultInboxDir
	}
	dir = strings.TrimSpace(dir)
	if dir != "~" && !strings.HasPrefix(dir, "~/") && !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the receive folder must be absolute or start with ~/: %q", dir)
	}
	if n.home == "" {
		return "", errReadOnly(dir)
	}
	want, err := n.expandTarget(dir)
	if err != nil {
		return "", err
	}
	// writableDir checks again once it exists.
	if err := n.makeWritableDir(want); err != nil {
		return "", err
	}
	return n.beginIntoAs(want, "", name, proto.UploadConflictKeep, size, proto.MaxInboxUploadBytes, unpack)
}

// attachmentDir is where a chat attachment is kept (dest=attachment): beside the pictures the chat sent that day
// (saveImages), in the node's own folder, which agents may read (agentsandbox.go) and nothing sweeps.
func attachmentDir(nodeDir string, now time.Time) string {
	return filepath.Join(nodeDir, "uploads", now.Format("2006-01-02"))
}

// beginAttachment opens an upload of a file sent with a chat message, kept for good (dest=attachment): its own name
// in attachmentDir, a taken one numbered " 2", " 3"… so nothing there is ever replaced.
func (n *Node) beginAttachment(name string, size int64, now time.Time) (string, error) {
	dir := attachmentDir(n.dir, now)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	clean := cleanName(name)
	if _, err := os.Lstat(filepath.Join(dir, clean)); errors.Is(err, syscall.ENAMETOOLONG) {
		stem, ext := splitName(clean)
		clean = stem + ext
	}
	id, err := n.ups.beginIn(dir, clean, size, proto.MaxUploadBytes, false)
	if err != nil {
		return "", err
	}
	if up := n.ups.lookup(id); up != nil {
		up.mu.Lock()
		up.into, up.shown, up.conflict = true, dir, proto.UploadConflictKeep
		up.mu.Unlock()
	}
	return id, nil
}

// end checks the size and gives the file its name; it returns the absolute path.
func (u *uploads) end(id string, now time.Time) (string, int64, error) {
	f, err := u.endFile(id, now)
	return f.Path, f.Size, err
}

// errChecksum: what was written does not hash to what the upload was begun with.
func errChecksum(got, want string) error {
	return &rpc.Error{Code: proto.CodeChecksumMismatch, Message: fmt.Sprintf("the bytes written hash to %s, not to the %s sent; nothing was kept", got, want)}
}

// endFile is end with all the answer says: for an upload begun with sha256, the bytes' hash, checked first (a
// mismatch keeps nothing); for one begun with unpack, the folder it unpacked into and its files.
func (u *uploads) endFile(id string, now time.Time) (proto.UploadedFile, error) {
	up, err := u.start(id)
	if err != nil {
		return proto.UploadedFile{}, err
	}
	defer func() {
		up.mu.Lock()
		u.retireLocked(id, up)
		up.busy = false
		up.mu.Unlock()
		up.discard()
	}()
	if err := up.close(); err != nil {
		return proto.UploadedFile{}, err
	}
	part := up.f.Name()
	if st, err := os.Stat(part); err != nil || st.Size() != up.size || up.written != up.size {
		return proto.UploadedFile{}, fmt.Errorf("got %d of %d bytes", up.written, up.size)
	}
	sum := ""
	if up.hash != nil {
		if sum = hex.EncodeToString(up.hash.Sum(nil)); sum != up.want {
			return proto.UploadedFile{}, errChecksum(sum, up.want)
		}
	}
	var out proto.UploadedFile
	switch {
	case up.unpack:
		out, err = u.finishUnpack(id, up)
	case up.into:
		out.Path, out.Size, err = u.finishInto(id, up)
	default:
		out.Path, out.Size, err = u.finishTemp(id, up, now)
	}
	if err != nil {
		return proto.UploadedFile{}, err
	}
	out.Name, out.SHA256 = filepath.Base(out.Path), sum
	return out, nil
}

// finishTemp names an upload into its folder (the temp directory, the node's uploads): name-<time>.ext, or a random
// prefix.
func (u *uploads) finishTemp(id string, up *upload, now time.Time) (string, int64, error) {
	part := up.f.Name()
	reserve := reserveName
	if up.random {
		reserve = reserveRandomName
	}
	path, err := reserve(filepath.Dir(part), up.name, now)
	if err != nil {
		return "", 0, err
	}
	if err := u.publish(id, up, path); err != nil { // replaces the empty reservation
		_ = os.Remove(path)
		return "", 0, err
	}
	return path, up.size, nil
}

// finishInto gives a dest=dir upload its own name in the folder, by the conflict policy; it returns the path as the
// app knows the folder. The name is taken with O_EXCL (or replaced by rename) so a file that appeared meanwhile is
// never overwritten unasked.
func (u *uploads) finishInto(id string, up *upload) (string, int64, error) {
	part := up.f.Name()
	dir, name := filepath.Dir(part), up.name
	mode := os.FileMode(0o644)
	var path string
	switch up.conflict {
	case proto.UploadConflictReplace:
		path = filepath.Join(dir, name)
		if st, err := os.Lstat(path); err == nil {
			if st.IsDir() {
				return "", 0, errExists(name)
			}
			if st.Mode().IsRegular() {
				mode = st.Mode().Perm() // the replaced file's permissions carry over
			}
		}
	default:
		var err error
		if path, err = reserveNumbered(dir, name, up.conflict == proto.UploadConflictKeep); err != nil {
			return "", 0, err
		}
	}
	if err := os.Chmod(part, mode); err != nil {
		if up.conflict != proto.UploadConflictReplace {
			_ = os.Remove(path)
		}
		return "", 0, err
	}
	if err := u.publish(id, up, path); err != nil {
		if up.conflict != proto.UploadConflictReplace {
			_ = os.Remove(path) // the empty reservation
		}
		return "", 0, err
	}
	return filepath.Join(up.shown, filepath.Base(path)), up.size, nil
}

// reserveNumbered creates an empty file for name in dir, or with keep the first free "stem 2.ext", "stem 3.ext"…
// as Finder names a copy; without keep a taken name is CodeExists.
func reserveNumbered(dir, name string, keep bool) (string, error) {
	stem, ext := splitName(name)
	for i := 1; i < 1000; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s %d%s", stem, i, ext)
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		if !keep {
			return "", errExists(name)
		}
	}
	return "", fmt.Errorf("no free name for %s", name)
}

// publish serializes the final rename with cancellation on this upload only.
// A successful rename is the commit point: a later abort cannot undo it.
func (u *uploads) publish(id string, up *upload, path string) error {
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.retired {
		return errUploadRetired
	}
	if err := os.Rename(up.f.Name(), path); err != nil {
		return err
	}
	u.retireLocked(id, up)
	return nil
}

func (u *uploads) abort(id string) {
	up := u.lookup(id)
	if up == nil {
		return
	}
	up.mu.Lock()
	if up.retired {
		up.mu.Unlock()
		return
	}
	u.retireLocked(id, up)
	cleanup := !up.busy
	up.mu.Unlock()
	if cleanup {
		up.discard()
	}
}

func (up *upload) close() error {
	if up.closed {
		return nil
	}
	up.closed = true
	return up.f.Close()
}

func (up *upload) discard() {
	_ = up.close()
	_ = os.Remove(up.f.Name())
}

// reserveName creates an empty 0600 file for name-<time>.ext in dir and returns its path; on a clash within the
// same second it tries again with a short random suffix.
func reserveName(dir, name string, now time.Time) (string, error) {
	stem, ext := splitName(cleanName(name))
	base := stem + "-" + now.Format("20060102-150405")
	for i := 0; i < 8; i++ {
		n := base + ext
		if i > 0 {
			n = base + "-" + randHex(2) + ext
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s", name)
}

// reserveRandomName creates an empty 0600 file named <16 hex>-<clean name> in dir and returns its path. The name is
// cut to what a file system takes, and never has a separator in it (cleanName), so it cannot leave dir.
func reserveRandomName(dir, name string, _ time.Time) (string, error) {
	stem, ext := splitName(cleanName(name))
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, randHex(8)+"-"+stem+ext)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s", name)
}

// cleanName keeps the phone's file name but nothing that would leave the directory or break a terminal.
func cleanName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || strings.Trim(name, ".") == "" {
		return "file"
	}
	return name
}

var plainExt = lazyre.New(`^\.[\p{L}\p{N}]{1,12}$`)

// compound extensions stay whole: report.tar.gz -> report-<time>.tar.gz.
var compoundExts = []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"}

// splitName splits off the extension and shortens the stem so the full name stays well under 255 bytes.
func splitName(name string) (stem, ext string) {
	lower := strings.ToLower(name)
	for _, c := range compoundExts {
		if strings.HasSuffix(lower, c) && len(name) > len(c) {
			stem, ext = name[:len(name)-len(c)], name[len(name)-len(c):]
		}
	}
	if ext == "" {
		ext = filepath.Ext(name)
		stem = strings.TrimSuffix(name, ext)
		if stem == "" || !plainExt.MatchString(ext) { // ".env", "notes. final", "a.._b"
			stem, ext = name, ""
		}
	}
	const maxStem = 180
	for len(stem) > maxStem {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return stem, ext
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// healthReportsDir is where the health space's reports go (dest=health): reports/ in the health directory, kept for
// good. The health conversations run there; the others are denied the directory (healthdeny.go).
func healthReportsDir(home string) string {
	return filepath.Join(identity.HealthDir(home), "reports")
}

// healthReportsDirOf is healthReportsDir for the health space named space: "" the built-in manager's (as ever),
// another manager's health-<id> beside it (identity.HealthDirName); any other name is refused, "health" too (the
// built-in's is named by leaving it out).
func healthReportsDirOf(home, space string) (string, error) {
	if space == "" {
		return healthReportsDir(home), nil
	}
	if space == identity.SpaceHealth || !identity.IsManagerDirName(space, identity.SpaceHealth) {
		return "", fmt.Errorf("bad health space %q", space)
	}
	return filepath.Join(filepath.Dir(identity.HealthDir(home)), space, "reports"), nil
}

// uploadCall serves the file.upload.* methods.
func (n *Node) uploadCall(method string, params json.RawMessage) (any, error) {
	var p struct {
		UploadID string `json:"upload_id"`
		Name     string `json:"name"`
		Dest     string `json:"dest"` // "terminal": a paste into a terminal (terminalUploadDir); "file": a task's file for its conversation (fileUploadDir); "health": a report (healthReportsDir); "dir": Dir; "inbox": the receive folder; "attachment": a chat attachment kept (attachmentDir); "" the temp directory
		// HealthDir: dest=health, the manager's health space ("health-<id>"; "": the built-in's), echoed back so the hub
		// can tell a node that does not know it (and would put the report in the built-in's).
		HealthDir string `json:"health_dir"`
		Dir       string `json:"dir"`      // dest=dir: the folder, ~/ or relative to Cwd; dest=inbox: the receive folder ("": the default)
		Cwd       string `json:"cwd"`      // dest=dir
		Conflict  string `json:"conflict"` // dest=dir, dest=path: proto.UploadConflict*
		// Path: dest=path, where it goes (beginAt); MakeDirs makes the folders missing on the way.
		Path     string `json:"path"`
		MakeDirs bool   `json:"make_dirs"`
		// SHA256: the hex SHA-256 the bytes must hash to, checked at the end before the file gets its name; Unpack:
		// they are a folder's zip (file.pack) to unpack (dest=path, dir or inbox). Both are echoed, so the manager can
		// tell a node that does not know them.
		SHA256 string `json:"sha256"`
		Unpack bool   `json:"unpack"`
		Size   int64  `json:"size"`
		Offset int64  `json:"offset"`
		Data   []byte `json:"data"` // base64 in JSON
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("bad params: %w", err)
	}
	switch method {
	case methodFileUploadBegin:
		var id string
		var err error
		switch p.Dest {
		case "":
			id, err = n.ups.begin(p.Name, p.Size)
		case proto.UploadDestTerminal:
			dir := terminalUploadDir(n.dir, time.Now())
			if err = os.MkdirAll(dir, 0o700); err == nil {
				id, err = n.ups.beginIn(dir, p.Name, p.Size, proto.MaxTerminalUploadBytes, true)
			}
		case proto.UploadDestFile:
			dir := fileUploadDir(n.dir, time.Now())
			if err = os.MkdirAll(dir, 0o700); err == nil {
				id, err = n.ups.beginIn(dir, p.Name, p.Size, proto.MaxFileUploadBytes, true)
			}
		case proto.UploadDestHealth:
			var dir string
			if dir, err = healthReportsDirOf(n.home, p.HealthDir); err == nil {
				if err = os.MkdirAll(dir, 0o700); err == nil {
					id, err = n.ups.beginIn(dir, p.Name, p.Size, proto.MaxUploadBytes, false)
				}
			}
		case proto.UploadDestDir:
			id, err = n.beginIntoAs(p.Dir, p.Cwd, p.Name, p.Conflict, p.Size, proto.MaxDirUploadBytes, p.Unpack)
		case proto.UploadDestInbox:
			id, err = n.beginInbox(p.Dir, p.Name, p.Size, p.Unpack)
		case proto.UploadDestPath:
			id, err = n.beginAt(p.Path, p.Name, p.Conflict, p.Size, p.MakeDirs, p.Unpack)
		case proto.UploadDestAttachment:
			id, err = n.beginAttachment(p.Name, p.Size, time.Now())
		default:
			err = fmt.Errorf("unknown upload destination %q", p.Dest)
		}
		if err == nil && p.Unpack && p.Dest != proto.UploadDestDir && p.Dest != proto.UploadDestInbox && p.Dest != proto.UploadDestPath {
			n.ups.abort(id)
			err = fmt.Errorf("unpack is for dest=path, dir or inbox, not %q", p.Dest)
		}
		if err == nil && p.SHA256 != "" {
			if err = n.ups.expect(id, p.SHA256); err != nil {
				n.ups.abort(id)
			}
		}
		if err != nil {
			return nil, err
		}
		res := map[string]any{"upload_id": id, "chunk_bytes": proto.UploadChunkBytes}
		if p.Dest == proto.UploadDestDir || p.Dest == proto.UploadDestInbox || p.Dest == proto.UploadDestFile || p.Dest == proto.UploadDestAttachment ||
			p.Dest == proto.UploadDestPath {
			res["dest"] = p.Dest // the hub checks it: a node from before dest=dir would quietly use the temp directory
		}
		if p.SHA256 != "" {
			res["sha256"] = true // checked at the end: a node from before would keep bytes that do not match
		}
		if p.Unpack {
			res["unpack"] = true // a node from before would keep the zip as a file
		}
		if p.Dest == proto.UploadDestHealth && p.HealthDir != "" {
			res["health_dir"] = p.HealthDir // the same for another manager's health space
		}
		return res, nil
	case methodFileUploadChunk:
		got, err := n.ups.chunk(p.UploadID, p.Offset, p.Data)
		if err != nil {
			return nil, err
		}
		return map[string]any{"received": got}, nil
	case methodFileUploadEnd:
		f, err := n.ups.endFile(p.UploadID, time.Now())
		if err != nil {
			return nil, err
		}
		n.logger.Printf("file from the phone: %s (%d bytes)", f.Path, f.Size)
		return f, nil
	case methodFileUploadAbort:
		n.ups.abort(p.UploadID)
		return map[string]any{"ok": true}, nil
	}
	return nil, fmt.Errorf("unknown method %s", method)
}
