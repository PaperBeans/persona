package hub

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Personas (docs/apple/persona.md): a look for Persona — its name, face, the animated dot in the Persona tab's top bar and the
// colors — as a package: persona.json and the files it names. A package is a directory in ~/.persona/personas/<id>/ or
// one built into personad (personas/ beside this file: a look the project ships to every hub, nwverse); a directory of the
// same id wins, so a look can be tried on one hub before it ships. The phone pulls the one it shows and caches it; the
// hub only lists and serves them. A package that does not parse, names a file it does not have or is too big is left
// out of the list (and logged), so the phone never gets half a look.
//
// A tiered package (persona.json's "tiers") is one look whose face follows a level the hub reads (persona_tiers.go):
// the list gives the current tier's manifest and a rev of that tier's files, so to a phone it is a plain package that
// changed. Its own persona.json is the default tier's, for hubs that do not know tiers.

const (
	personaFormat = 1
	// A package is a few hundred KB of PNGs; the bounds keep a mistake (a 4K source image left in) off the phones.
	personaMaxFile  = 4 << 20
	personaMaxTotal = 16 << 20
)

// builtinPersonas are the looks built into personad: personas/<id>/, the generated packages (scripts/assets/personas).
//
//go:embed personas
var builtinPersonas embed.FS

var personaIDPattern = lazyre.New(`^[a-z0-9][a-z0-9-]{0,31}$`)

// personaStates are the dot's states; idle is the one a package must have (the app shows it for any it lacks).
var personaStates = []string{"idle", "thinking", "working", "needs_you", "offline"}

// personaManifest is what the hub checks in persona.json; the app reads the rest (colors, frame rates, about).
type personaManifest struct {
	Format int    `json:"format"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar struct {
		Sizes map[string]string `json:"sizes"`
	} `json:"avatar"`
	Dot struct {
		States map[string]struct {
			File string `json:"file"`
		} `json:"states"`
	} `json:"dot"`
	Tiers *personaTiers `json:"tiers,omitempty"`
}

// personaTiers is a tiered package's "tiers": where its level comes from, the tier shown without one, and each tier's
// manifest (a persona.json of its own, its files named from the package's root).
type personaTiers struct {
	Source  personaTierSource `json:"source"`
	Default string            `json:"default"`
	Levels  []personaTier     `json:"levels"`
}

type personaTierSource struct {
	// Kind is how the hub reads URL; nwverse-board: NWVERSE's contribution board (persona_tiers.go).
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type personaTier struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	When     tierWhen `json:"when"`
	Manifest string   `json:"manifest"`
}

// tierWhen is the board's record a tier stands for: a level, or (not hatched yet) how the newcomer's trial stands.
type tierWhen struct {
	Level  *int   `json:"level,omitempty"`
	Newbie string `json:"newbie,omitempty"`
}

func (t *personaTiers) tier(key string) *personaTier {
	for i := range t.Levels {
		if t.Levels[i].Key == key {
			return &t.Levels[i]
		}
	}
	return nil
}

// personaInfo is one entry of GET /api/v1/personas: the manifest as written, and a rev that changes with any byte of
// what the phone downloads (the phone keeps a copy per rev).
type personaInfo struct {
	ID       string          `json:"id"`
	Rev      string          `json:"rev"`
	Bytes    int64           `json:"bytes"`
	Manifest json.RawMessage `json:"manifest"`
}

// personaPackage is a package that checks out: where its files are and what they are.
type personaPackage struct {
	id    string
	fsys  fs.FS
	files map[string]int64 // every regular file (slash paths, dot files left out) → its size
	total int64
	raw   []byte // persona.json as written
	m     personaManifest
	// source is the extension it comes from (looks.go; nil: the hub's own).
	source *lookSource
}

func (h *Hub) personasDir() string { return filepath.Join(h.cfg.dir, "personas") }

// builtinPersonaFS is personad's own packages (nil in unit tests, which set fstest maps of their own).
func builtinPersonaFS() fs.FS {
	sub, err := fs.Sub(builtinPersonas, "personas")
	if err != nil {
		return nil
	}
	return sub
}

// personaPackages are the packages that check out: the built-in ones first (a directory of the same id in their
// place), then the directories, by id.
func (h *Hub) personaPackages() []*personaPackage {
	lookSwapMu.RLock()
	defer lookSwapMu.RUnlock()
	return h.personaPackagesLocked()
}

func (h *Hub) personaPackagesLocked() []*personaPackage {
	dirs := map[string]*personaPackage{}
	var dirIDs []string
	if entries, err := os.ReadDir(h.personasDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p, err := openPersona(os.DirFS(filepath.Join(h.personasDir(), e.Name())), e.Name())
			if err != nil {
				h.logf("persona %s left out: %v", e.Name(), err)
				continue
			}
			dirs[p.id] = p
			dirIDs = append(dirIDs, p.id)
		}
	}
	out := []*personaPackage{}
	for _, id := range h.builtinPersonaIDs() {
		if p := dirs[id]; p != nil {
			out = append(out, p)
			delete(dirs, id)
			continue
		}
		p, err := openPersona(mustSub(h.builtinLooks, id), id)
		if err != nil {
			h.logf("built-in persona %s left out: %v", id, err)
			continue
		}
		out = append(out, p)
	}
	for _, id := range dirIDs {
		if p := dirs[id]; p != nil {
			out = append(out, p)
		}
	}
	// Then the extensions' looks (looks.go): a package of the hub's of the same id keeps its place.
	for _, p := range h.extensionLooks() {
		if !slices.ContainsFunc(out, func(q *personaPackage) bool { return q.id == p.id }) {
			out = append(out, p)
		}
	}
	return out
}

func (h *Hub) builtinPersonaIDs() []string {
	if h.builtinLooks == nil {
		return nil
	}
	entries, err := fs.ReadDir(h.builtinLooks, ".")
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return emptyFS{}
	}
	return sub
}

// emptyFS is a file system with nothing in it (a package that is not there).
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// personaPackage is the package of that id: its directory when that checks out, else the built-in one.
func (h *Hub) personaPackage(id string) (*personaPackage, error) {
	lookSwapMu.RLock()
	defer lookSwapMu.RUnlock()
	return h.personaPackageLocked(id)
}

func (h *Hub) personaPackageLocked(id string) (*personaPackage, error) {
	if !personaIDPattern.MatchString(id) {
		return nil, fmt.Errorf("no persona %q", id)
	}
	p, dirErr := openPersona(os.DirFS(filepath.Join(h.personasDir(), id)), id)
	if dirErr == nil {
		return p, nil
	}
	for _, b := range h.builtinPersonaIDs() {
		if b == id {
			return openPersona(mustSub(h.builtinLooks, id), id)
		}
	}
	for _, p := range h.extensionLooks() {
		if p.id == id {
			return p, nil
		}
	}
	return nil, dirErr
}

// personas lists the packages that check out, each as a phone sees it (a tiered one at its current tier).
func (h *Hub) personas() []personaInfo {
	lookSwapMu.RLock()
	defer lookSwapMu.RUnlock()
	out := []personaInfo{}
	for _, p := range h.personaPackagesLocked() {
		info, err := h.personaInfo(p)
		if err != nil {
			h.logf("persona %s left out: %v", p.id, err)
			continue
		}
		out = append(out, info)
	}
	return out
}

// personaInfo is the package's entry in the list: the whole package with its rev, or a tiered one's current tier.
func (h *Hub) personaInfo(p *personaPackage) (personaInfo, error) {
	if p.m.Tiers == nil {
		names := make([]string, 0, len(p.files))
		for f := range p.files {
			names = append(names, f)
		}
		rev, err := personaRev(p.fsys, "", names)
		if err != nil {
			return personaInfo{}, err
		}
		return personaInfo{ID: p.id, Rev: rev, Bytes: p.total, Manifest: p.raw}, nil
	}
	// The tier the level says, else the default one: a tier that does not check out leaves the phone on the default.
	keys := []string{h.personaTierKey(p.id, p.m.Tiers), p.m.Tiers.Default}
	var lastErr error
	for _, key := range keys {
		t := p.m.Tiers.tier(key)
		if t == nil {
			lastErr = fmt.Errorf("no tier %q", key)
			continue
		}
		raw, m, err := p.tierManifest(t)
		if err != nil {
			lastErr = fmt.Errorf("tier %s: %w", key, err)
			h.logf("persona %s: %v", p.id, lastErr)
			continue
		}
		names := manifestFiles(m)
		rev, err := personaRev(p.fsys, "tier:"+key+"\x00"+string(raw), names)
		if err != nil {
			return personaInfo{}, err
		}
		bytes := int64(len(raw))
		for _, f := range names {
			bytes += p.files[f]
		}
		return personaInfo{ID: p.id, Rev: rev, Bytes: bytes, Manifest: raw}, nil
	}
	return personaInfo{}, lastErr
}

// tierManifest reads a tier's persona.json and holds it to a package's rules (its files are the package's).
func (p *personaPackage) tierManifest(t *personaTier) ([]byte, personaManifest, error) {
	if _, err := p.file(t.Manifest); err != nil {
		return nil, personaManifest{}, err
	}
	raw, err := fs.ReadFile(p.fsys, t.Manifest)
	if err != nil {
		return nil, personaManifest{}, err
	}
	m, err := p.checkManifest(raw)
	if err != nil {
		return nil, personaManifest{}, err
	}
	if m.Tiers != nil {
		return nil, personaManifest{}, errors.New("a tier's manifest has tiers of its own")
	}
	return raw, m, nil
}

// personaRev sums the files (and what else the phone is told, the tier) into a rev.
func personaRev(fsys fs.FS, salt string, names []string) (string, error) {
	sort.Strings(names)
	sum := sha256.New()
	sum.Write([]byte(salt))
	for _, f := range names {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(sum, "%s\x00%d\x00", f, len(b))
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

func manifestFiles(m personaManifest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, f := range m.Avatar.Sizes {
		add(f)
	}
	for _, st := range m.Dot.States {
		add(st.File)
	}
	return out
}

// openPersona checks the package in fsys (its root is the package's directory): persona.json, the files it names and
// the size; for a tiered one, that the tiers are there and their manifests name files the package has.
func openPersona(fsys fs.FS, id string) (*personaPackage, error) {
	if !personaIDPattern.MatchString(id) {
		return nil, errors.New("the directory's name is not an id (a-z, 0-9, -)")
	}
	if extension.ReservedLookID(id) {
		return nil, fmt.Errorf("%q is a reserved look id", id)
	}
	files, total, err := personaFiles(fsys)
	if err != nil {
		return nil, err
	}
	if total > personaMaxTotal {
		return nil, fmt.Errorf("%d bytes, more than %d", total, personaMaxTotal)
	}
	p := &personaPackage{id: id, fsys: fsys, files: files, total: total}
	if _, ok := files["persona.json"]; !ok {
		return nil, errors.New("no persona.json")
	}
	if p.raw, err = fs.ReadFile(fsys, "persona.json"); err != nil {
		return nil, err
	}
	if p.m, err = p.checkManifest(p.raw); err != nil {
		return nil, fmt.Errorf("persona.json: %w", err)
	}
	if t := p.m.Tiers; t != nil {
		if len(t.Levels) == 0 {
			return nil, errors.New("tiers without levels")
		}
		if t.tier(t.Default) == nil {
			return nil, fmt.Errorf("the default tier %q is not one of the levels", t.Default)
		}
		if t.Source.Kind != boardKindNWVerse || !strings.HasPrefix(t.Source.URL, "https://") {
			return nil, fmt.Errorf("tiers from %q at %q: this hub reads %s over https", t.Source.Kind, t.Source.URL, boardKindNWVerse)
		}
		if _, _, err := p.tierManifest(t.tier(t.Default)); err != nil {
			return nil, fmt.Errorf("default tier %s: %w", t.Default, err)
		}
	}
	return p, nil
}

// checkManifest parses a persona.json of this package and checks what the hub holds every manifest to.
func (p *personaPackage) checkManifest(raw []byte) (personaManifest, error) {
	var m personaManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	switch {
	case m.Format != personaFormat:
		return m, fmt.Errorf("format %d, this hub reads %d", m.Format, personaFormat)
	case m.ID != p.id:
		return m, fmt.Errorf("id %q is not the directory's name", m.ID)
	case strings.TrimSpace(m.Name) == "":
		return m, errors.New("no name")
	case len(m.Avatar.Sizes) == 0:
		return m, errors.New("no avatar")
	}
	if _, ok := m.Dot.States["idle"]; !ok {
		return m, errors.New("no idle dot")
	}
	for s := range m.Dot.States {
		if !isPersonaState(s) {
			return m, fmt.Errorf("unknown dot state %q", s)
		}
	}
	for _, f := range manifestFiles(m) {
		if _, err := p.file(f); err != nil {
			return m, err
		}
	}
	return m, nil
}

func isPersonaState(s string) bool {
	for _, x := range personaStates {
		if s == x {
			return true
		}
	}
	return false
}

// file is a file of the package by its name in a manifest or a request: a relative path inside the package, no dot
// files, a regular file (not a link out of it) within the size bound. It returns the name to open.
func (p *personaPackage) file(name string) (string, error) {
	clean := path.Clean(name)
	if name == "" || path.IsAbs(name) || clean != name || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("file %q: not a plain path inside the package", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("file %q: hidden", name)
		}
	}
	size, ok := p.files[clean]
	if !ok {
		return "", fmt.Errorf("file %q: not a regular file of the package", name)
	}
	if size > personaMaxFile {
		return "", fmt.Errorf("file %q: %d bytes, more than %d", name, size, personaMaxFile)
	}
	return clean, nil
}

// personaFiles is every regular file in the package (slash paths), dot files and links left out, and their total size.
func personaFiles(fsys fs.FS) (map[string]int64, int64, error) {
	files := map[string]int64{}
	var total int64
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[p] = info.Size()
		total += info.Size()
		return nil
	})
	return files, total, err
}

func (h *Hub) personaRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/personas", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"personas": h.personas()})
	})
	// A file of a package that checks out (any of its files: a manifest's, or a preview next to them).
	api.HandleFunc("GET /api/v1/personas/{id}/{file...}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		lookSwapMu.RLock()
		p, err := h.personaPackageLocked(id)
		if err != nil {
			lookSwapMu.RUnlock()
			writeErr(w, http.StatusNotFound, fmt.Errorf("no persona %q", id))
			return
		}
		name, err := p.file(r.PathValue("file"))
		if err != nil {
			lookSwapMu.RUnlock()
			writeErr(w, http.StatusNotFound, err)
			return
		}
		f, err := p.fsys.Open(name)
		if err != nil {
			lookSwapMu.RUnlock()
			writeErr(w, http.StatusNotFound, err)
			return
		}
		// At the rev the apps were told (?rev=), a file never changes: the browser may keep it.
		immutable := false
		if rev := r.URL.Query().Get("rev"); rev != "" {
			info, err := h.personaInfo(p)
			immutable = err == nil && info.Rev == rev
		}
		lookSwapMu.RUnlock()
		defer f.Close()
		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		personaFileHeaders(w, name, immutable)
		_, _ = io.Copy(w, f)
	})
}
