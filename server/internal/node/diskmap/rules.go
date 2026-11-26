package diskmap

import (
	"path"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// The protection rules (docs/server/machine-usage-map.md "Protection rules"): what the map may offer to move to the
// trash or hand to an offload provider. One function decides for both purposes, on a path the node resolved itself
// (the real folder it is in, the last component not followed), with the facts lstat gave. It is pure: the system's
// shape (its GOOS, the home folder, the cache catalog, the Agents' and Persona's folders) is injected, so the table
// tests cover macOS, Linux and Windows path shapes on any computer.

// ruleEnv is the system the rules judge paths on.
type ruleEnv struct {
	goos string
	home string
	// caches are the cache catalog's folders (machdiag), by id.
	caches map[string]string
	// agents and persona are folders the Agents and Persona keep their own data in, beyond the fixed names below.
	agents, persona []string
}

// facts is what lstat (or the walk) said of the path.
type facts struct {
	exists  bool
	dir     bool
	regular bool
	link    bool // a symbolic link or a reparse point
	mount   bool // the root of another filesystem
}

// Fixed names, compared case-insensitively on macOS and Windows.
var (
	standardFolders = []string{"desktop", "documents", "downloads", "movies", "music", "pictures", "public", "videos", "templates"}
	// credentialDirs are folders of keys and sign-ins, relative to home (and keychains, per system below).
	credentialDirs = []string{".ssh", ".gnupg", ".gpg", ".aws", ".kube", ".azure", ".docker", ".config/gcloud", ".password-store",
		".netrc", ".pgpass", ".git-credentials"}
	// agentDirs are the Agents' own folders, relative to home.
	agentDirs = []string{".claude", ".claude.json", ".codex", ".gemini", ".pi", ".omp", ".opencode", ".config/opencode",
		".local/share/opencode", ".cursor", ".agy", ".antigravity", ".config/claude", ".qwen", ".kimi"}
	// systemCaches are ~/Library/Caches children macOS itself relies on.
	systemCaches = []string{"cloudkit", "familycircle", "geoservices", "passkit"}
	// managedExtensions are bundles and disks an app or a virtual machine manages, and downloads still in progress.
	managedExtensions = []string{"photoslibrary", "musiclibrary", "tvlibrary", "imovielibrary", "photolibrary", "aplibrary",
		"migratedphotolibrary", "fcpbundle", "vmwarevm", "pvm", "utm", "vbox", "vdi", "vmdk", "vhd", "vhdx", "qcow2", "hdd", "hds",
		"sparsebundle", "sparseimage", "icloud", "download", "crdownload", "part", "partial"}
	// managedNames are files that are a running virtual machine's or container runtime's disk without such an extension.
	managedNames = []string{"docker.raw", "docker.qcow2", "data.img", "disk.img"}
)

// caseless says whether names on goos compare without case.
func caseless(goos string) bool { return goos == "darwin" || goos == "windows" }

// norm is p in the form the rules compare: forward slashes, cleaned, lowercased where the system ignores case.
func (e ruleEnv) norm(p string) string {
	if e.goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	p = path.Clean(p)
	if caseless(e.goos) {
		p = strings.ToLower(p)
	}
	return p
}

// within says whether p is root or inside it (both normalized).
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

// touches says whether p is in root or holds it: trashing either takes root with it.
func touches(p, root string) bool { return within(p, root) || within(root, p) }

// isAbs is filepath.IsAbs for goos, on a normalized path.
func isAbsFor(goos, p string) bool {
	if goos == "windows" {
		return len(p) >= 3 && p[1] == ':' && p[2] == '/' || strings.HasPrefix(p, "//")
	}
	return strings.HasPrefix(p, "/")
}

// ext is the lowercased extension of a name, without the dot.
func ext(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return strings.ToLower(name[i+1:])
	}
	return ""
}

// refusal is the first rule that refuses p for purpose (proto.StoragePurpose*), or "" when p may go. p is absolute,
// resolved by the node; f are lstat's facts.
func (e ruleEnv) refusal(p, purpose string, f facts) string {
	switch {
	case !f.exists:
		return proto.RefusalNotFound
	case f.link:
		return proto.RefusalSymlink
	case !f.dir && !f.regular:
		return proto.RefusalNotRegular
	}
	trash := purpose != proto.StoragePurposeOffload
	n, home := e.norm(p), e.norm(e.home)
	if !isAbsFor(e.goos, n) || e.home == "" {
		return proto.RefusalOutsideHome
	}
	rel := ""
	if within(n, home) && n != home {
		rel = strings.TrimPrefix(n, strings.TrimSuffix(home, "/")+"/")
	}
	parts := []string{}
	if rel != "" {
		parts = strings.Split(rel, "/")
	}
	inHome := func(r string) string { return strings.TrimSuffix(home, "/") + "/" + e.normRel(r) }

	// Specific rules first: what is precious refuses whatever folder it is in.
	if code := e.precious(n, inHome, within); code != "" {
		return code
	}
	if e.cloudSynced(n, parts, inHome) {
		return proto.RefusalCloudSynced
	}
	if e.goos == "darwin" && len(parts) >= 3 && parts[0] == "library" && parts[1] == "caches" {
		if strings.HasPrefix(parts[2], "com.apple.") || slices.Contains(systemCaches, parts[2]) {
			return proto.RefusalSystemCache
		}
	}

	// Apps: what is inside one belongs to it; one itself is the app's own to move (offload may take one from the
	// Applications folders: the provider says whether it can).
	bundle, inside := e.appBundle(n)
	switch {
	case inside:
		return proto.RefusalInsideApp
	case bundle && trash:
		return proto.RefusalAppBundle
	case bundle:
		if dir := path.Dir(n); dir == "/applications" || dir == inHome("Applications") {
			return "" // the provider says whether it can take an app
		}
	}
	if f.mount {
		return proto.RefusalMountPoint
	}
	for _, s := range parts {
		x := ext(s)
		if slices.Contains(managedExtensions, x) || slices.Contains(managedNames, strings.ToLower(s)) {
			return proto.RefusalManagedLibrary
		}
	}

	// Where it is.
	switch {
	case !within(n, home):
		return proto.RefusalOutsideHome
	case n == home:
		return proto.RefusalHomeRoot
	case len(parts) == 1 && slices.Contains(standardFolders, strings.ToLower(parts[0])):
		return proto.RefusalStandardFolder
	}
	if e.cacheAt(n) == "" || !trash {
		if e.appManaged(n, parts, trash) {
			return proto.RefusalAppManaged
		}
		if strings.HasPrefix(parts[0], ".") && !(trash && len(parts) == 2 && parts[0] == ".cache") {
			return proto.RefusalDotFolder
		}
	}
	// Last, a folder that would be allowed but holds something precious takes it with it.
	return e.precious(n, inHome, func(p, root string) bool { return within(root, p) })
}

// precious is the code of the credentials', the Agents' or Persona's folder that rel(n, folder) holds, or "".
func (e ruleEnv) precious(n string, inHome func(string) string, rel func(p, root string) bool) string {
	for _, d := range credentialDirs {
		if rel(n, inHome(d)) {
			return proto.RefusalCredentials
		}
	}
	for _, d := range e.keychains() {
		if rel(n, inHome(d)) {
			return proto.RefusalCredentials
		}
	}
	for _, d := range agentDirs {
		if rel(n, inHome(d)) {
			return proto.RefusalAgentData
		}
	}
	for _, d := range e.agents {
		if d != "" && rel(n, e.norm(d)) {
			return proto.RefusalAgentData
		}
	}
	if rel(n, inHome(".persona")) {
		return proto.RefusalPersonaData
	}
	for _, d := range e.persona {
		if d != "" && rel(n, e.norm(d)) {
			return proto.RefusalPersonaData
		}
	}
	return ""
}

// appBundle says whether p (normalized) names a macOS app bundle by its name, or something inside one (inside).
// Folders in ~/Library are named after bundle ids ("com.example.app") and are not bundles.
func (e ruleEnv) appBundle(n string) (bundle, inside bool) {
	if e.goos != "darwin" || e.home != "" && within(n, e.norm(e.home)+"/library") {
		return false, false
	}
	segs := strings.Split(strings.TrimPrefix(n, "/"), "/")
	for _, s := range segs[:len(segs)-1] {
		if ext(s) == "app" {
			return false, true
		}
	}
	return ext(segs[len(segs)-1]) == "app", false
}

// cloudSynced: p is in a folder a sync service mirrors to the user's other devices, where removing it removes it
// everywhere.
func (e ruleEnv) cloudSynced(n string, parts []string, inHome func(string) string) bool {
	if e.goos == "darwin" && (within(n, inHome("Library/Mobile Documents")) || within(n, inHome("Library/CloudStorage"))) {
		return true
	}
	if len(parts) == 0 {
		return false
	}
	top := strings.ToLower(parts[0])
	return top == "dropbox" || e.goos == "windows" && (top == "onedrive" || strings.HasPrefix(top, "onedrive - ") || top == "iclouddrive" || top == "icloud drive")
}

// normRel is a home-relative name in the compared form.
func (e ruleEnv) normRel(r string) string {
	if caseless(e.goos) {
		return strings.ToLower(r)
	}
	return r
}

// keychains are where the system keeps passwords, relative to home.
func (e ruleEnv) keychains() []string {
	switch e.goos {
	case "darwin":
		return []string{"Library/Keychains"}
	case "linux":
		return []string{".local/share/keyrings"}
	case "windows":
		return []string{"AppData/Roaming/Microsoft/Credentials", "AppData/Local/Microsoft/Credentials", "AppData/Roaming/Microsoft/Protect"}
	}
	return nil
}

// appManaged: p is in a folder apps manage themselves (~/Library, ~/Applications, the trash, AppData), and not one of
// the folders the map may empty there (trash only: a direct child of ~/Library/Caches or AppData\Local\Temp, Xcode's
// DerivedData and what is in it).
func (e ruleEnv) appManaged(n string, parts []string, trash bool) bool {
	if len(parts) == 0 {
		return false
	}
	top := parts[0]
	switch e.goos {
	case "darwin":
		switch top {
		case "library":
			if trash && len(parts) == 3 && parts[1] == "caches" {
				return false
			}
			if trash && len(parts) >= 4 && parts[1] == "developer" && parts[2] == "xcode" && parts[3] == "deriveddata" && len(parts) <= 5 {
				return false
			}
			return true
		case "applications", ".trash":
			return true
		}
	case "linux":
		if len(parts) >= 3 && parts[0] == ".local" && parts[1] == "share" && parts[2] == "Trash" {
			return true
		}
	case "windows":
		if top == "appdata" {
			if trash && len(parts) == 4 && parts[1] == "local" && parts[2] == "temp" {
				return false
			}
			return true
		}
	}
	return false
}

// cacheAt is the catalog cache p is exactly, or "".
func (e ruleEnv) cacheAt(n string) string {
	for id, dir := range e.caches {
		if dir != "" && e.norm(dir) == n {
			return id
		}
	}
	return ""
}

// category is what a box at p is colored by.
func (e ruleEnv) category(p string) string {
	n, home := e.norm(p), e.norm(e.home)
	if e.cacheAt(n) != "" {
		return proto.UsageCategoryCache
	}
	if bundle, inside := e.appBundle(n); bundle || inside {
		return proto.UsageCategoryApps
	}
	if e.home == "" || !within(n, home) || n == home {
		return proto.UsageCategorySystem
	}
	parts := strings.Split(strings.TrimPrefix(n, strings.TrimSuffix(home, "/")+"/"), "/")
	for id := range e.caches {
		if within(n, e.norm(e.caches[id])) {
			return proto.UsageCategoryCache
		}
	}
	switch {
	case e.goos == "darwin" && parts[0] == "library":
		if len(parts) >= 2 && parts[1] == "caches" {
			return proto.UsageCategoryCache
		}
		return proto.UsageCategoryAppData
	case e.goos == "darwin" && parts[0] == "applications":
		return proto.UsageCategoryApps
	case e.goos == "windows" && parts[0] == "appdata":
		if len(parts) >= 3 && parts[1] == "local" && parts[2] == "temp" {
			return proto.UsageCategoryCache
		}
		return proto.UsageCategoryAppData
	case parts[0] == ".cache":
		return proto.UsageCategoryCache
	case strings.HasPrefix(parts[0], "."):
		return proto.UsageCategoryAppData
	}
	return proto.UsageCategoryPersonal
}

// display is p as the apps show it: "~/…" inside home, the path otherwise.
func (e ruleEnv) display(p string) string {
	if e.home == "" {
		return p
	}
	sep := "/"
	h, q := e.home, p
	if e.goos == "windows" {
		h, q = strings.ReplaceAll(h, `\`, "/"), strings.ReplaceAll(q, `\`, "/")
	}
	h = strings.TrimSuffix(path.Clean(h), "/")
	q = path.Clean(q)
	cmpH, cmpQ := h, q
	if caseless(e.goos) {
		cmpH, cmpQ = strings.ToLower(h), strings.ToLower(q)
	}
	switch {
	case cmpQ == cmpH:
		return "~"
	case strings.HasPrefix(cmpQ, cmpH+sep):
		return "~/" + q[len(h)+1:]
	}
	return p
}
