package extension

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func logoPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBundledIconPNGChecks(t *testing.T) {
	good := logoPNG(t, 512, 512)
	if err := CheckIcon(good); err != nil {
		t.Fatal(err)
	}
	oversizedDimensions := bytes.Clone(good)
	binary.BigEndian.PutUint32(oversizedDimensions[16:20], 8192)
	binary.BigEndian.PutUint32(oversizedDimensions[20:24], 8192)
	binary.BigEndian.PutUint32(oversizedDimensions[29:33], crc32.ChecksumIEEE(oversizedDimensions[12:29]))
	for name, data := range map[string][]byte{
		"missing":          nil,
		"svg":              []byte(`<svg viewBox="0 0 512 512"/>`),
		"small":            logoPNG(t, 128, 128),
		"not-square":       logoPNG(t, 512, 256),
		"truncated":        good[:len(good)-20],
		"large-file":       append(bytes.Clone(good), make([]byte, IconMost)...),
		"large-dimensions": oversizedDimensions,
	} {
		t.Run(name, func(t *testing.T) {
			if CheckIcon(data) == nil {
				t.Fatal("invalid artwork accepted")
			}
		})
	}
}

func TestBundledIconLocalFileBounds(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.Mkdir(assets, 0o700); err != nil {
		t.Fatal(err)
	}
	good := logoPNG(t, 512, 512)
	file := filepath.Join(root, IconPath)
	if err := os.WriteFile(file, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadIcon(root); err != nil || !bytes.Equal(data, good) || !HasIcon(root) {
		t.Fatal("local icon unavailable", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIcon(root); err == nil || HasIcon(root) {
		t.Fatal("file symlink accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(assets); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), assets); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIcon(root); err == nil || HasIcon(root) {
		t.Fatal("directory symlink accepted")
	}
	if _, err := ReadIcon(""); err == nil {
		t.Fatal("empty root accepted")
	}
}

func iconArchive(t *testing.T, headers []*tar.Header, data []byte) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && h.Size == int64(len(data)) {
			if _, err := tw.Write(data); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	file := filepath.Join(t.TempDir(), "package.tgz")
	if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestBundledIconArchiveChecks(t *testing.T) {
	data := logoPNG(t, 512, 512)
	h := func(name string) *tar.Header {
		return &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(data))}
	}
	for _, path := range []string{IconPath, "example/" + IconPath} {
		file := iconArchive(t, []*tar.Header{h(path)}, data)
		if got, err := PackageIcon(file, "archive.tgz", "example"); err != nil || !bytes.Equal(got, data) {
			t.Fatal("valid archive", path, err)
		}
	}
	for name, headers := range map[string][]*tar.Header{
		"traversal":       {h("../" + IconPath)},
		"absolute":        {h("/" + IconPath)},
		"duplicate":       {h(IconPath), h(IconPath)},
		"other-extension": {h("other/" + IconPath)},
		"symlink":         {{Name: IconPath, Typeflag: tar.TypeSymlink, Linkname: "other.png"}},
		"directory-link":  {{Name: "assets", Typeflag: tar.TypeSymlink, Linkname: "other"}, h(IconPath)},
		"hard-link":       {{Name: IconPath, Typeflag: tar.TypeLink, Linkname: "other.png"}},
		"missing":         {h("other.png")},
		"large-header":    {{Name: IconPath, Mode: 0o644, Typeflag: tar.TypeReg, Size: IconMost + 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PackageIcon(iconArchive(t, headers, data), "archive.tar.gz", "example"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("example/" + IconPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	_ = zw.Close()
	file := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := PackageIcon(file, "package.zip", "example"); err != nil || !bytes.Equal(got, data) {
		t.Fatal("zip", err)
	}
	for _, field := range []int{8, 10} {
		bad := bytes.Clone(b.Bytes())
		eocd := bytes.LastIndex(bad, []byte{'P', 'K', 5, 6})
		binary.LittleEndian.PutUint16(bad[eocd+field:eocd+field+2], 65535)
		if err := os.WriteFile(file, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := PackageIcon(file, "package.zip", "example"); err == nil {
			t.Fatal("unbounded ZIP directory accepted")
		}
	}
}

func TestBundledIconSourceAndCatalogProjection(t *testing.T) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(exampleManifest), &fields); err != nil {
		t.Fatal(err)
	}
	fields["icon"] = IconPath
	data, _ := json.Marshal(fields)
	m, err := Parse(data)
	if err != nil || m.Icon != IconPath {
		t.Fatal("source icon", err)
	}
	packed, err := PackagedManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	var projected map[string]any
	_ = json.Unmarshal(packed, &projected)
	if _, ok := projected["icon"]; ok {
		t.Fatal("source-only icon leaked into runtime manifest")
	}
	for _, path := range []string{IconPath, "../secret.png", "https://example.com/logo.png", "assets/other.svg"} {
		payload := popularityPayload(t, "extensions", map[string]any{"icon": path}, "")
		ix, _, err := ParseIndex(payload)
		if err != nil || len(ix.Extensions) != 1 {
			t.Fatal(err)
		}
		if got := ix.Extensions[0].Icon; (path == IconPath && got != IconPath) || (path != IconPath && got != "") {
			t.Fatal("catalog icon path", path, got)
		}
	}
}
