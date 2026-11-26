package node

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What an Agent's file transfer reads on the computer it sends from (file.pack, docs/protocol/file-transfer.md
// "Agents and the assistant"): a file as it is, or a folder zipped into a temporary file, each with the SHA-256 of
// the bytes the manager will stream (file.chunk) to the other computer, which checks it before the file gets its
// name (upload.go). Both go through the read guard (pathguard.go) and, on top of it, refuse credentials by name
// wherever they are (identity.TransferSecretName): a named one is refused, one inside a folder is left out and
// counted. Links inside a folder are not carried (they would lead elsewhere on the other computer, or nowhere).

// packMaxAge: a folder's zip that file.pack.done never removed (the manager went away mid-way) is removed by the next
// file.pack once it is this old.
const packMaxAge = 6 * time.Hour

// packSkippedNamed is how many of the entries a folder's zip left out are named in the answer.
const packSkippedNamed = 10

// packBase is where file.pack keeps the folders' zips, one temporary folder each (p-*).
func packBase() string { return filepath.Join(os.TempDir(), "persona-transfers") }

// errTransferSecret refuses a credential by its name.
func errTransferSecret(shown string) error {
	return &rpc.Error{Code: proto.CodeTransferSecret, Message: fmt.Sprintf("%s looks like a credential (by its name) and is not sent between computers", shown)}
}

// The methods, from the node's method table (node_methods.go): neither is bound to a session.
func init() {
	nodeRPC(proto.MethodFilePack, (*Node).filePack)
	nodeRPC(proto.MethodFilePackDone, (*Node).filePackDone)
}

// filePackDoneParams is file.pack.done's request: the zip file.pack answered.
type filePackDoneParams struct {
	Path string `json:"path"`
}

// filePackDone removes a folder's zip once it was sent (or not).
func (n *Node) filePackDone(_ context.Context, p filePackDoneParams) (map[string]any, error) {
	if err := removePack(p.Path); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// filePack prepares the file or folder p names for sending.
func (n *Node) filePack(ctx context.Context, p proto.FilePackRequest) (*proto.FilePack, error) {
	shown := strings.TrimSpace(p.Path)
	asked := n.resolvePath(shown, p.Cwd)
	if asked == "" {
		return nil, errors.New("the path must be absolute or start with ~/")
	}
	if identity.TransferSecretPath(filepath.ToSlash(asked)) {
		return nil, errTransferSecret(shown)
	}
	real, err := n.realPath(absPath(asked))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errNotFound(shown)
		}
		return nil, explainDenied(asked, n.home, err)
	}
	if err := n.offLimitsResolved(real); err != nil {
		return nil, err
	}
	if identity.TransferSecretPath(filepath.ToSlash(real)) { // a link with an innocent name to a key
		return nil, errTransferSecret(shown)
	}
	st, err := os.Stat(real)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errNotFound(shown)
		}
		return nil, explainDenied(asked, n.home, err)
	}
	switch {
	case st.Mode().IsRegular():
		return n.packFile(ctx, asked, real)
	case st.IsDir() && (isHome(fold(real), n.home) || filepath.Dir(real) == real || n.home != "" && isHomeUnder(real, n.home)):
		return nil, fmt.Errorf("%s holds the whole home folder: name a folder in it", shown)
	case st.IsDir():
		return n.packDir(ctx, asked, real)
	}
	return nil, fmt.Errorf("%s is neither a file nor a folder", shown)
}

// isHomeUnder: dir is a folder above the home (the home is in it).
func isHomeUnder(dir, home string) bool {
	for _, h := range []string{home, realHome(home)} {
		if h != "" && strings.HasPrefix(fold(filepath.Clean(h)), fold(filepath.Clean(dir))+"/") {
			return true
		}
	}
	return false
}

// packFile hashes a file to send as it is; a file that changes while it is read is refused (try again).
func (n *Node) packFile(ctx context.Context, asked, real string) (*proto.FilePack, error) {
	if inICloud(real) {
		return nil, errICloud
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, explainDenied(asked, n.home, err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() > proto.MaxAgentTransferBytes {
		return nil, tooLargeToSend(before.Size())
	}
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx: ctx, r: f}); err != nil {
		return nil, err
	}
	after, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !sameFileRead(before, after) || fileRevision(before) != fileRevision(after) {
		return nil, errFileChanged
	}
	return &proto.FilePack{Kind: proto.FilePackFile, Path: real, Name: filepath.Base(asked), Source: asked, Size: before.Size(),
		Revision: fileRevision(before), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func tooLargeToSend(size int64) error {
	return &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("%d MB is over the %d MB a transfer takes", size>>20, proto.MaxAgentTransferBytes>>20)}
}

// ctxReader stops a long read when ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// hashingWriter writes to w and hashes what it wrote.
type hashingWriter struct {
	w io.Writer
	h hash.Hash
}

func (hw hashingWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	hw.h.Write(p[:n])
	return n, err
}

// packDir zips the folder real into a temporary file, every entry under the folder's own name: its files and
// folders, never through a link, and without what the read guard refuses or what is a credential by its name (left
// out and counted, as are links, special files and what cannot be read). At most maxZipFiles files and maxZipBytes
// before packing.
func (n *Node) packDir(ctx context.Context, asked, real string) (pack *proto.FilePack, err error) {
	base := packBase()
	cleanOldPacks(base, time.Now())
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(base, "p-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	name := filepath.Base(asked)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "folder"
	}
	out, err := os.OpenFile(filepath.Join(dir, cleanName(name)+".zip"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if out != nil {
			_ = out.Close()
		}
	}()
	h := sha256.New()
	zw := zip.NewWriter(hashingWriter{w: out, h: h})
	pack = &proto.FilePack{Kind: proto.FilePackDir, Name: name, Source: asked, Temp: true}
	skip := func(rel string) {
		pack.Skipped++
		if len(pack.SkippedNames) < packSkippedNamed {
			pack.SkippedNames = append(pack.SkippedNames, filepath.ToSlash(rel))
		}
	}
	top := cleanName(name)
	walkErr := filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		rel, _ := filepath.Rel(real, path)
		if err != nil {
			if path == real {
				return err
			}
			skip(rel)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != real && (n.offLimitsResolved(path) != nil || identity.TransferSecretPath(filepath.ToSlash(rel))) {
			skip(rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			skip(rel)
			return nil
		}
		inZip := top
		if path != real {
			inZip = top + "/" + filepath.ToSlash(rel)
		}
		switch {
		case info.IsDir():
			hd, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hd.Name, hd.Method = inZip+"/", zip.Store
			_, err = zw.CreateHeader(hd)
			return err
		case !info.Mode().IsRegular(): // links, sockets, devices, pipes
			skip(rel)
			return nil
		}
		if pack.Files++; pack.Files > maxZipFiles {
			return &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("more than %d files to send", maxZipFiles)}
		}
		if pack.Bytes += info.Size(); pack.Bytes > maxZipBytes {
			return &rpc.Error{Code: proto.CodeTooLarge, Message: fmt.Sprintf("more than %d GB to send", maxZipBytes>>30)}
		}
		f, err := os.Open(path)
		if err != nil {
			pack.Files--
			pack.Bytes -= info.Size()
			skip(rel)
			return nil
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || !os.SameFile(st, info) { // replaced by something else since it was listed
			pack.Files--
			pack.Bytes -= info.Size()
			skip(rel)
			return nil
		}
		hd, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hd.Name, hd.Method = inZip, zip.Deflate
		w, err := zw.CreateHeader(hd)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, ctxReader{ctx: ctx, r: f})
		return err
	})
	if walkErr != nil {
		return nil, explainDenied(asked, n.home, walkErr)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := out.Close(); err != nil {
		out = nil
		return nil, err
	}
	zipPath := out.Name()
	out = nil
	st, err := os.Stat(zipPath)
	if err != nil {
		return nil, err
	}
	if st.Size() > proto.MaxAgentTransferBytes {
		return nil, tooLargeToSend(st.Size())
	}
	pack.Path, pack.Size, pack.Revision, pack.SHA256 = zipPath, st.Size(), fileRevision(st), hex.EncodeToString(h.Sum(nil))
	return pack, nil
}

// removePack removes a folder's zip file.pack made, with its temporary folder; any other path is refused.
func removePack(p string) error {
	base := packBase()
	rel, err := filepath.Rel(base, filepath.Clean(p))
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || !filepath.IsAbs(p) || len(parts) != 2 || !strings.HasPrefix(parts[0], "p-") {
		return fmt.Errorf("%s is not a zip file.pack made", p)
	}
	return os.RemoveAll(filepath.Join(base, parts[0]))
}

// cleanOldPacks removes the zips older than packMaxAge.
func cleanOldPacks(base string, now time.Time) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range ents {
		if info, err := e.Info(); err == nil && strings.HasPrefix(e.Name(), "p-") && now.Sub(info.ModTime()) > packMaxAge {
			_ = os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
}
