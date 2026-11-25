package node

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func putModeFile(t *testing.T, p string, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// callTransferUpload sends data the way an Agent's transfer does (dest=path or another dest in p), answering begin's reply and
// end's.
func callTransferUpload(t *testing.T, n *Node, p map[string]any, data []byte) (proto.UploadedFile, map[string]any, error) {
	t.Helper()
	call := func(method string, p map[string]any) (any, error) {
		raw, _ := json.Marshal(p)
		return n.uploadCall(method, raw)
	}
	p["size"] = len(data)
	res, err := call("file.upload.begin", p)
	if err != nil {
		return proto.UploadedFile{}, nil, err
	}
	begin := res.(map[string]any)
	for off := 0; off < len(data); off += proto.UploadChunkBytes {
		end := min(off+proto.UploadChunkBytes, len(data))
		if _, err := call("file.upload.chunk", map[string]any{"upload_id": begin["upload_id"], "offset": off, "data": data[off:end]}); err != nil {
			return proto.UploadedFile{}, begin, err
		}
	}
	out, err := call("file.upload.end", map[string]any{"upload_id": begin["upload_id"]})
	if err != nil {
		return proto.UploadedFile{}, begin, err
	}
	return out.(proto.UploadedFile), begin, nil
}

// noParts: nothing an upload or an unpack leaves while it runs is left in dir.
func noParts(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".persona-") {
			t.Fatalf("%s left in %s", e.Name(), dir)
		}
	}
}

// A file is sent as it is, with the SHA-256 of its bytes and the revision file.chunk then reads at. A credential is
// refused by its name wherever it is (on top of the read guard's own folders), also behind a link with another name.
func TestFilePackFile(t *testing.T) {
	n, home := writeNode(t)
	putModeFile(t, filepath.Join(home, "proj", "report.txt"), "the report", 0o644)
	p, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/proj/report.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != proto.FilePackFile || p.Name != "report.txt" || p.Size != 10 || p.SHA256 != sha([]byte("the report")) || p.Revision == "" || p.Temp {
		t.Fatalf("pack %+v", p)
	}
	c, err := n.readFileChunk(proto.FileChunkRequest{Path: p.Path, Revision: p.Revision, Length: 10})
	if err != nil || string(c.Data) != "the report" {
		t.Fatalf("chunk at the pack's revision: %v %q", err, c.Data)
	}

	for _, f := range []string{"proj/.env", "proj/.env.production", "proj/deploy/server.pem", "proj/hub.json", "proj/certs/tls.key",
		"proj/id_ed25519", "proj/store.p12", "backup/hub.json.bak", "proj/.aws/config"} {
		putModeFile(t, filepath.Join(home, f), "secret", 0o600)
		if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/" + f}); rpcCode(err) != proto.CodeTransferSecret {
			t.Errorf("%s: %v, want refused as a credential", f, err)
		}
	}
	putModeFile(t, filepath.Join(home, ".ssh", "config"), "Host x", 0o600)
	if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/.ssh/config"}); err == nil {
		t.Error("~/.ssh/config was packed")
	}
	putModeFile(t, filepath.Join(home, "proj", ".env.example"), "KEY=", 0o644)
	if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/proj/.env.example"}); err != nil {
		t.Errorf(".env.example (no values) refused: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(home, "proj", "id_ed25519"), filepath.Join(home, "proj", "notes.txt")); err != nil {
			t.Fatal(err)
		}
		if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/proj/notes.txt"}); err == nil {
			t.Error("a link to a key was packed")
		}
	}
	if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "~/proj/missing"}); rpcCode(err) != proto.CodeNotFound {
		t.Errorf("missing: %v", err)
	}
	if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: "relative/path"}); err == nil {
		t.Error("a relative path without cwd was packed")
	}
	for _, whole := range []string{"~", "~/", filepath.Dir(home), string(filepath.Separator)} {
		if _, err := n.filePack(context.Background(), proto.FilePackRequest{Path: whole}); err == nil || !strings.Contains(err.Error(), "whole home") {
			t.Errorf("%s: %v, want the whole home refused", whole, err)
		}
	}
}

// zipEntries are the entries of the zip at p.
func zipEntries(t *testing.T, p string) []string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	slices.Sort(out)
	return out
}

// A folder is zipped into a temporary file under its own name, without credentials (by their name, anywhere in it),
// links or what the read guard refuses: those are left out and counted. The zip's SHA-256 is the bytes on disk, and
// file.pack.done removes it (and nothing else).
func TestFilePackFolder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	n, home := writeNode(t)
	proj := filepath.Join(home, "proj")
	putModeFile(t, filepath.Join(proj, "a.txt"), "a", 0o644)
	putModeFile(t, filepath.Join(proj, "bin", "run.sh"), "#!/bin/sh\n", 0o755)
	putModeFile(t, filepath.Join(proj, ".env"), "TOKEN=x", 0o600)
	putModeFile(t, filepath.Join(proj, "keys", "server.key"), "k", 0o600)
	putModeFile(t, filepath.Join(proj, ".ssh", "id_rsa"), "k", 0o600)
	putModeFile(t, filepath.Join(proj, ".env.example"), "TOKEN=", 0o644)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(proj, "outside")); err != nil {
			t.Fatal(err)
		}
	}
	p, err := n.filePack(context.Background(), proto.FilePackRequest{Path: proj})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != proto.FilePackDir || p.Name != "proj" || !p.Temp || p.Files != 3 {
		t.Fatalf("pack %+v", p)
	}
	wantSkipped := 3 // .env, keys/server.key, .ssh
	if runtime.GOOS != "windows" {
		wantSkipped++ // the link
	}
	if p.Skipped != wantSkipped || !slices.Contains(p.SkippedNames, ".env") || !slices.Contains(p.SkippedNames, ".ssh") {
		t.Fatalf("skipped %d %v, want %d", p.Skipped, p.SkippedNames, wantSkipped)
	}
	names := zipEntries(t, p.Path)
	for _, want := range []string{"proj/", "proj/a.txt", "proj/bin/run.sh", "proj/.env.example", "proj/keys/"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s not in the zip %v", want, names)
		}
	}
	for _, name := range names {
		if strings.Contains(name, ".env") && name != "proj/.env.example" || strings.Contains(name, "server.key") || strings.Contains(name, ".ssh") || strings.Contains(name, "outside") {
			t.Errorf("%s is in the zip", name)
		}
	}
	data, _ := os.ReadFile(p.Path)
	if sha(data) != p.SHA256 || int64(len(data)) != p.Size {
		t.Fatalf("the pack's sha256/size do not match its zip")
	}
	if err := removePack(proj); err == nil {
		t.Fatal("removePack removed a path it did not make")
	}
	if err := removePack(p.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(p.Path)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the zip's folder stays: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, "a.txt")); err != nil {
		t.Fatalf("the source was touched: %v", err)
	}
}

// An upload begun with sha256 is named only when its bytes hash to it; one that does not keeps nothing. dest=path
// echoes the destination and the check, so the manager can tell a node that would not make it.
func TestPathUploadChecksSHA256(t *testing.T) {
	n, home := writeNode(t)
	data := bytes.Repeat([]byte("x"), proto.UploadChunkBytes+10)
	_, _, err := callTransferUpload(t, n, map[string]any{"name": "a.bin", "dest": proto.UploadDestPath, "path": "~/proj/", "sha256": sha([]byte("other"))}, data)
	if rpcCode(err) != proto.CodeChecksumMismatch {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "proj", "a.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a file that did not match was kept: %v", err)
	}
	noParts(t, filepath.Join(home, "proj"))

	out, begin, err := callTransferUpload(t, n, map[string]any{"name": "a.bin", "dest": proto.UploadDestPath, "path": "~/proj/", "sha256": sha(data)}, data)
	if err != nil {
		t.Fatal(err)
	}
	if begin["dest"] != proto.UploadDestPath || begin["sha256"] != true {
		t.Fatalf("begin %v", begin)
	}
	if out.Path != filepath.Join(home, "proj", "a.bin") || out.SHA256 != sha(data) || out.Size != int64(len(data)) {
		t.Fatalf("end %+v", out)
	}
	if _, _, err := callTransferUpload(t, n, map[string]any{"name": "b", "dest": proto.UploadDestPath, "path": "~/proj/", "sha256": "abc"}, []byte("b")); err == nil {
		t.Fatal("a sha256 that is not one was taken")
	}
}

// dest=path: an existing folder takes the file under its own name, any other path is its new name (with make_dirs,
// its missing folders are made); a taken name is refused before any byte unless conflict is replace; only the home is
// writable, Persona's and the key folders not.
func TestPathUploadNamesConflictsAndBounds(t *testing.T) {
	n, home := writeNode(t)
	put := func(target string, data string, extra map[string]any) (proto.UploadedFile, error) {
		p := map[string]any{"name": "src.txt", "dest": proto.UploadDestPath, "path": target, "make_dirs": true, "sha256": sha([]byte(data))}
		for k, v := range extra {
			p[k] = v
		}
		out, _, err := callTransferUpload(t, n, p, []byte(data))
		return out, err
	}
	if out, err := put("~/proj", "1", nil); err != nil || out.Path != filepath.Join(home, "proj", "src.txt") {
		t.Fatalf("into an existing folder: %+v %v", out, err)
	}
	if out, err := put("~/new/deep/renamed.txt", "2", nil); err != nil || out.Path != filepath.Join(home, "new", "deep", "renamed.txt") {
		t.Fatalf("a new path: %+v %v", out, err)
	}
	if _, err := put("~/proj/src.txt", "3", nil); rpcCode(err) != proto.CodeExists {
		t.Fatalf("a taken name: %v, want exists", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "proj", "src.txt")); string(b) != "1" {
		t.Fatalf("the taken file changed: %q", b)
	}
	if _, err := put("~/proj/src.txt", "4", map[string]any{"conflict": proto.UploadConflictReplace}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "proj", "src.txt")); string(b) != "4" {
		t.Fatalf("overwrite did not replace: %q", b)
	}
	if err := os.MkdirAll(filepath.Join(home, "proj", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := put("~/proj/sub/", "6", map[string]any{"name": "sub"}); err != nil {
		t.Fatalf("into ~/proj/sub: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "proj", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := put(filepath.Join(home, "proj"), "7", map[string]any{"name": "dir", "conflict": proto.UploadConflictReplace}); rpcCode(err) != proto.CodeExists {
		t.Fatalf("a file over a folder: %v", err)
	}
	outside := t.TempDir()
	for _, bad := range []string{outside + "/x.txt", "~/.persona/x.txt", "~/.ssh/authorized_keys", "~/Library/x", "relative.txt",
		"~/.bashrc", "~/.zshrc", "~/.config/autostart/x.desktop", "~/.config/systemd/user/x.service", "~/.config", "~/.gitconfig"} {
		if _, err := put(bad, "8", nil); err == nil {
			t.Errorf("%s was written", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".persona")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.persona was made: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.config was made on the way to a startup folder: %v", err)
	}
	if out, err := put("~/.config/myapp/settings.json", "9", nil); err != nil || out.Path != filepath.Join(home, ".config", "myapp", "settings.json") {
		t.Errorf("an ordinary app's settings: %+v %v", out, err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("written outside the home: %v", ents)
	}
}

// pack packs a folder on one node and reads its zip back as the manager does, through file.chunk at the pack's
// revision a chunk at a time, answering the bytes and the pack.
func pack(t *testing.T, n *Node, path string) ([]byte, *proto.FilePack) {
	t.Helper()
	raw, err := n.handleMethod(context.Background(), proto.MethodFilePack, json.RawMessage(`{"path":`+strconvQuote(path)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	p := raw.(*proto.FilePack)
	var data []byte
	for off := int64(0); off < p.Size; {
		c, err := n.readFileChunk(proto.FileChunkRequest{Path: p.Path, Revision: p.Revision, Offset: off,
			Length: int(min(int64(proto.FileDownloadChunkBytes), p.Size-off))})
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, c.Data...)
		off += int64(len(c.Data))
	}
	if sha(data) != p.SHA256 {
		t.Fatal("the zip read through file.chunk does not hash to the pack's SHA-256")
	}
	t.Cleanup(func() {
		if _, err := n.handleMethod(context.Background(), proto.MethodFilePackDone, json.RawMessage(`{"path":`+strconvQuote(p.Path)+`}`)); err != nil {
			t.Error(err)
		}
	})
	return data, p
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A folder's zip is unpacked under the name it goes to, files and an executable's mode kept, only once it hashes to
// what was sent. Not over a taken name unless replace, which merges into the folder and replaces files of the same
// name, keeping the others; never over a file; the receive folder numbers it.
func TestPathUploadUnpacksAFolder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	src, srcHome := writeNode(t)
	putModeFile(t, filepath.Join(srcHome, "proj", "a.txt"), "new a", 0o644)
	putModeFile(t, filepath.Join(srcHome, "proj", "bin", "run.sh"), "#!/bin/sh\n", 0o755)
	putModeFile(t, filepath.Join(srcHome, "proj", "empty", ".keep"), "", 0o644)
	data, p := pack(t, src, "~/proj")

	dst, home := writeNode(t)
	unpack := func(target string, extra map[string]any) (proto.UploadedFile, map[string]any, error) {
		params := map[string]any{"name": p.Name, "dest": proto.UploadDestPath, "path": target, "make_dirs": true, "sha256": p.SHA256, "unpack": true}
		for k, v := range extra {
			params[k] = v
		}
		return callTransferUpload(t, dst, params, data)
	}
	out, begin, err := unpack("~/work/", nil) // a final "/": a folder to make and put it in
	if err != nil {
		t.Fatal(err)
	}
	if begin["unpack"] != true || begin["sha256"] != true {
		t.Fatalf("begin %v", begin)
	}
	got := filepath.Join(home, "work", "proj")
	if out.Path != got || out.Files != 3 || out.SHA256 != p.SHA256 {
		t.Fatalf("end %+v", out)
	}
	if b, _ := os.ReadFile(filepath.Join(got, "a.txt")); string(b) != "new a" {
		t.Fatalf("a.txt %q", b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(got, "bin", "run.sh")); st == nil || st.Mode().Perm()&0o100 == 0 {
			t.Fatalf("run.sh lost its x: %v", st)
		}
	}
	noParts(t, filepath.Join(home, "work"))

	// Taken: refused before any byte, then merged with replace.
	if _, _, err := unpack("~/work", nil); rpcCode(err) != proto.CodeExists {
		t.Fatalf("a taken folder: %v", err)
	}
	putModeFile(t, filepath.Join(got, "a.txt"), "old a", 0o644)
	putModeFile(t, filepath.Join(got, "mine.txt"), "mine", 0o644)
	if _, _, err := unpack("~/work", map[string]any{"conflict": proto.UploadConflictReplace}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(got, "a.txt")); string(b) != "new a" {
		t.Fatalf("merge did not replace a.txt: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(got, "mine.txt")); string(b) != "mine" {
		t.Fatalf("merge removed mine.txt: %q", b)
	}
	noParts(t, filepath.Join(home, "work"))

	// Never in place of a file.
	putModeFile(t, filepath.Join(home, "files", "proj"), "a file", 0o644)
	if _, _, err := unpack("~/files", map[string]any{"conflict": proto.UploadConflictReplace}); rpcCode(err) != proto.CodeExists {
		t.Fatalf("over a file: %v", err)
	}
	// A new name.
	if out, _, err := unpack("~/work/copy", nil); err != nil || out.Path != filepath.Join(home, "work", "copy") {
		t.Fatalf("renamed: %+v %v", out, err)
	}
	// The receive folder numbers it.
	for _, want := range []string{"proj", "proj 2"} {
		out, _, err := callTransferUpload(t, dst, map[string]any{"name": p.Name, "dest": proto.UploadDestInbox, "dir": "~/Downloads/Persona",
			"sha256": p.SHA256, "unpack": true}, data)
		if err != nil || out.Path != filepath.Join(home, "Downloads", "Persona", want) {
			t.Fatalf("inbox: %+v %v, want %s", out, err, want)
		}
	}
	// A zip that does not hash to what was sent is not unpacked.
	if _, _, err := unpack("~/bad", map[string]any{"sha256": sha([]byte("x"))}); rpcCode(err) != proto.CodeChecksumMismatch {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bad", "proj")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a mismatched folder was unpacked: %v", err)
	}
	// unpack only where a folder can go.
	if _, _, err := callTransferUpload(t, dst, map[string]any{"name": "x", "dest": proto.UploadDestTerminal, "unpack": true}, data); err == nil {
		t.Fatal("unpack into the terminal's uploads")
	}
}

// buildZip makes a zip of entries (name → content; a name ending in / is a folder; link: content is its target).
func buildZip(t *testing.T, entries [][2]string, links map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		if strings.HasSuffix(e[0], "/") {
			h.SetMode(fs.ModeDir | 0o755)
		} else {
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e[1]))
	}
	for name, target := range links {
		h := &zip.FileHeader{Name: name}
		h.SetMode(fs.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(target))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Nothing in a zip leaves its folder: a name with "..", an absolute one or one beside the top folder refuses the
// whole zip; links are left out, never made; a merge never writes through a link in the folder it merges into.
func TestUnpackStaysInsideItsFolder(t *testing.T) {
	n, home := writeNode(t)
	up := func(target string, data []byte, extra map[string]any) (proto.UploadedFile, error) {
		p := map[string]any{"name": "proj", "dest": proto.UploadDestPath, "path": target, "make_dirs": true, "sha256": sha(data), "unpack": true}
		for k, v := range extra {
			p[k] = v
		}
		out, _, err := callTransferUpload(t, n, p, data)
		return out, err
	}
	for _, bad := range [][][2]string{
		{{"proj/", ""}, {"proj/../../evil.txt", "x"}},
		{{"proj/", ""}, {"/abs.txt", "x"}},
		{{"loose.txt", "x"}},
		{{"proj/a\\..\\b", "x"}},
	} {
		if _, err := up("~/in/", buildZip(t, bad, nil), nil); err == nil {
			t.Errorf("%v was unpacked", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "evil.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("an entry left its folder")
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	out, err := up("~/in/", buildZip(t, [][2]string{{"proj/", ""}, {"proj/a.txt", "a"}}, map[string]string{"proj/link": outside}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Skipped != 1 || out.Files != 1 {
		t.Fatalf("end %+v", out)
	}
	if _, err := os.Lstat(filepath.Join(home, "in", "proj", "link")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a link was made: %v", err)
	}
	// A merge into a folder whose "sub" is a link elsewhere stops there.
	if err := os.Symlink(outside, filepath.Join(home, "in", "proj", "sub")); err != nil {
		t.Fatal(err)
	}
	_, err = up("~/in", buildZip(t, [][2]string{{"proj/", ""}, {"proj/sub/", ""}, {"proj/sub/x.txt", "x"}}, nil),
		map[string]any{"conflict": proto.UploadConflictReplace})
	if rpcCode(err) != proto.CodeExists {
		t.Fatalf("merge through a link: %v", err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("written through the link: %v", ents)
	}
	noParts(t, filepath.Join(home, "in"))
}
