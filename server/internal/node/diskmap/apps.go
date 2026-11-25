package diskmap

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// appInfo is what an app bundle's Info.plist says of it.
type appInfo struct {
	bundleID, version, name string
	mtime                   time.Time
}

// appInfos keeps each bundle's Info.plist as read, until the file changes.
type appInfos struct {
	mu sync.Mutex
	by map[string]appInfo
}

// get is the bundle at app's identity (empty when it has no readable Info.plist).
func (a *appInfos) get(app string) appInfo {
	file := filepath.Join(app, "Contents", "Info.plist")
	st, err := os.Stat(file)
	if err != nil {
		return appInfo{}
	}
	a.mu.Lock()
	if a.by == nil {
		a.by = map[string]appInfo{}
	}
	if info, ok := a.by[app]; ok && info.mtime.Equal(st.ModTime()) {
		a.mu.Unlock()
		return info
	}
	a.mu.Unlock()
	info := appInfo{mtime: st.ModTime()}
	if vals, err := readPlistStrings(file); err == nil {
		info.bundleID = strings.TrimSpace(vals["CFBundleIdentifier"])
		info.version = strings.TrimSpace(vals["CFBundleShortVersionString"])
		info.name = strings.TrimSpace(vals["CFBundleDisplayName"])
		if info.name == "" {
			info.name = strings.TrimSpace(vals["CFBundleName"])
		}
	}
	a.mu.Lock()
	a.by[app] = info
	a.mu.Unlock()
	return info
}
