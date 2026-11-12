package update

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// The Android app a release carries (docs/operations/hub-update.md "The Android app", #5563): android.json beside
// latest.json names the signed APK published with the release (scripts/release/android_tag_release.py), and the hub
// keeps its apps/android directory, which its phones update from (internal/hub/androidapp.go), at that APK. As with
// the hub's own binaries nothing is signed by the publisher: the sha256 guards against a broken download; the phone
// itself refuses an APK signed with another key than the app it has.

const (
	androidManifestLimit = 64 << 10
	androidAPKLimit      = 256 << 20
	androidMaxVersion    = 2100000000 // Google Play's ceiling, which scripts/release/android_apk_atomic.py keeps too
	// androidLockName is the lock scripts/release/android_apk_atomic.py publishes under: a publisher copying an APK
	// over SSH and this sync never write the directory at the same time.
	androidLockName = ".publish.lock"
)

var (
	androidAPKName = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._-]*\.apk$`)
	androidPackage = lazyre.New(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	androidDigest  = lazyre.New(`^[0-9a-f]{64}$`)
	androidStored  = lazyre.New(`^([0-9a-f]{64})\.apk(\.gz)?$`)
)

// AndroidRelease is android.json: the hub's apps/android/latest.json (scripts/release/publish_android_apk.py writes
// it) plus the release's version and the APK's file name beside android.json. Only what the sync checks is read; the
// whole object becomes the hub's latest.json, whose other fields (min_sdk, notes…) the phones read.
type AndroidRelease struct {
	Version           string `json:"version"`
	Name              string `json:"name"`
	PackageName       string `json:"package_name"`
	VersionCode       int64  `json:"version_code"`
	VersionName       string `json:"version_name"`
	CertificateSHA256 string `json:"certificate_sha256"`
	SHA256            string `json:"sha256"`
	Size              int64  `json:"size"`
}

func (r *AndroidRelease) check() error {
	switch {
	case !androidAPKName.MatchString(r.Name):
		return errors.New("android.json: name is not an APK file name")
	case !androidPackage.MatchString(r.PackageName):
		return errors.New("android.json: package_name is not an Android package")
	case r.VersionCode < 1 || r.VersionCode > androidMaxVersion:
		return errors.New("android.json: version_code is out of range")
	case !androidDigest.MatchString(r.CertificateSHA256):
		return errors.New("android.json: certificate_sha256 is not a lowercase SHA-256")
	case !androidDigest.MatchString(r.SHA256):
		return errors.New("android.json: sha256 is not a lowercase SHA-256")
	case r.Size < 1 || r.Size > androidAPKLimit:
		return errors.New("android.json: size is out of range")
	}
	return nil
}

// AndroidSync is what SyncAndroid did.
type AndroidSync string

const (
	AndroidNone    AndroidSync = "none"    // the release carries no Android app
	AndroidCurrent AndroidSync = "current" // the directory already has that version or a newer one
	AndroidUpdated AndroidSync = "updated" // the directory now offers the release's APK
)

// SyncAndroid puts the Android app the release at base carries into dir (the hub's apps/android), in the layout
// android_apk_atomic.py publishes: <sha256>.apk, its gzip copy, then latest.json last. It never goes back to a lower
// version code, and never replaces an APK of another package or signed with another key: the phones could not install
// it over the app they have. A release without android.json changes nothing. The release read is returned with
// AndroidCurrent and AndroidUpdated, and with an error when it was read.
func SyncAndroid(ctx context.Context, client *http.Client, base, dir string) (AndroidSync, *AndroidRelease, error) {
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return AndroidNone, nil, errors.New("this personad has nowhere to look for releases")
	}
	data, err := get(ctx, client, base+"/android.json", androidManifestLimit)
	if errors.Is(err, errNotFound) {
		return AndroidNone, nil, nil
	}
	if err != nil {
		return AndroidNone, nil, err
	}
	var r AndroidRelease
	if err := json.Unmarshal(data, &r); err != nil {
		return AndroidNone, nil, fmt.Errorf("android.json: %w", err)
	}
	if err := r.check(); err != nil {
		return AndroidNone, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AndroidNone, &r, err
	}
	unlock, err := lockAndroidDir(filepath.Join(dir, androidLockName))
	if err != nil {
		return AndroidNone, &r, err
	}
	defer unlock()
	removeAndroidLeftovers(dir)

	current, err := readAndroidCurrent(dir)
	if err != nil {
		return AndroidNone, &r, err
	}
	if current != nil {
		if current.VersionCode >= r.VersionCode {
			return AndroidCurrent, &r, nil
		}
		if current.PackageName != "" && current.PackageName != r.PackageName {
			return AndroidNone, &r, fmt.Errorf("the release's Android app is %s, the one published here %s", r.PackageName, current.PackageName)
		}
		if current.CertificateSHA256 != "" && current.CertificateSHA256 != r.CertificateSHA256 {
			return AndroidNone, &r, errors.New("the release's Android app is signed with another key than the one published here")
		}
	}

	apk := filepath.Join(dir, r.SHA256+".apk")
	if err := fetchFile(ctx, client, base+"/"+r.Name, apk, File{Name: r.Name, Size: r.Size, SHA256: r.SHA256}); err != nil {
		if errors.Is(err, errNotFound) {
			err = fmt.Errorf("android.json lists %s, which the release does not have", r.Name)
		}
		return AndroidNone, &r, err
	}
	// fetchFile renamed the download into place without syncing it: latest.json must never name an APK a power loss
	// could leave empty.
	if err := syncFile(apk); err != nil {
		return AndroidNone, &r, err
	}
	if err := replaceFile(apk+".gz", 0o600, func(out *os.File) error { return gzipFile(apk, out) }); err != nil {
		return AndroidNone, &r, err
	}
	if err := replaceFile(filepath.Join(dir, "latest.json"), 0o600, func(out *os.File) error {
		_, err := out.Write(data)
		return err
	}); err != nil {
		return AndroidNone, &r, err
	}
	keep := map[string]bool{r.SHA256: true}
	if current != nil {
		keep[current.SHA256] = true
	}
	pruneAndroid(dir, keep)
	return AndroidUpdated, &r, nil
}

// androidCurrent is what SyncAndroid reads of the latest.json already published.
type androidCurrent struct {
	PackageName       string `json:"package_name"`
	VersionCode       int64  `json:"version_code"`
	CertificateSHA256 string `json:"certificate_sha256"`
	SHA256            string `json:"sha256"`
}

// readAndroidCurrent is the latest.json published in dir, nil with none. One that does not read is an error: what
// cannot be read is never overwritten (the hub's endpoint answers 500 for it, which someone has to look at).
func readAndroidCurrent(dir string) (*androidCurrent, error) {
	data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c androidCurrent
	err = json.Unmarshal(data, &c)
	c.SHA256 = strings.ToLower(c.SHA256)
	if err != nil || c.VersionCode < 1 || c.VersionCode > androidMaxVersion || !androidDigest.MatchString(c.SHA256) {
		return nil, errors.New("the Android app published here has a latest.json that does not read; leaving it as it is")
	}
	c.CertificateSHA256 = strings.ToLower(strings.ReplaceAll(c.CertificateSHA256, ":", ""))
	return &c, nil
}

// gzipFile writes path gzipped to out the way android_apk_atomic.gzip_copy does: no name and no time in the header.
func gzipFile(path string, out io.Writer) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	zw, err := gzip.NewWriterLevel(out, 6)
	if err != nil {
		return err
	}
	if _, err := io.Copy(zw, in); err != nil {
		return err
	}
	return zw.Close()
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// removeAndroidLeftovers removes what an interrupted sync left: fetchFile's <name>.part and replaceFile's <name>.new.
// A publisher's .upload-* staging directories are its own, and taken away by it.
func removeAndroidLeftovers(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Type().IsRegular() && (strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".new")) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// pruneAndroid removes the APKs (and gzip copies) in dir that are neither the one just published nor the one it
// replaced: a phone that read the previous latest.json may still be downloading that one.
func pruneAndroid(dir string, keep map[string]bool) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if m := androidStored.FindStringSubmatch(e.Name()); m != nil && e.Type().IsRegular() && !keep[m[1]] {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
