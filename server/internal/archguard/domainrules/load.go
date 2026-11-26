package domainrules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// The layers a package declares (docs/server/architecture.md "Domain model", "Layers").
const (
	layerKit      = "kit"
	layerCore     = "core"
	layerShared   = "core shared"
	layerAdapter  = "adapter"
	layerApp      = "application"
	layerSeam     = "application seam"
	layerLibrary  = "library"
	layerContract = "contract"
	layerTestkit  = "testkit"

	// undeclared is the layer of a package that declares none: it counts as application.
	undeclared = "undeclared"
)

// directivePrefix starts the line that declares a package's layer.
const directivePrefix = "//persona:layer"

// knownLayer says whether s is a layer a package may declare.
func knownLayer(s string) bool {
	switch s {
	case layerKit, layerCore, layerShared, layerAdapter, layerApp, layerSeam, layerLibrary, layerContract, layerTestkit:
		return true
	}
	return false
}

// A Pkg is one package directory under the tree's root, as Load read it.
type Pkg struct {
	Rel   string // the directory under the root, slash-separated ("internal/assistant/todo/timeline")
	Path  string // its import path
	Name  string // its package name ("" when the directory holds only an external test package)
	Layer string // the layer it declares, "" when it declares none
	Files []*File

	declared []directive // every //persona:layer line in its files, in order
	tree     *tree
	decls    *decls // its declarations, once Full has parsed it
}

// A directive is one //persona:layer line: the layer it names, where it is, and whether it is in a package comment.
type directive struct {
	layer  string
	pos    token.Position
	placed bool
}

// A File is one non-generated .go file of a package.
type File struct {
	Rel      string // the file under the root, slash-separated
	Test     bool   // a _test.go file
	External bool   // a test file of package <name>_test
	Imports  []Import
	Doc      string // the package comment's text, directives left out

	wire  bool      // its bytes hold ".Wire"
	visit bool      // its bytes hold ") Visit": it may declare a Visit method
	ast   *ast.File // the whole file, once parsed
	pkg   *Pkg
}

// An Import is one import of a file.
type Import struct {
	Path string // the import path
	Name string // the name it is imported under as written ("", ".", "_" or a name)
	Pos  token.Position
}

// A tree is where Load read the files: a directory on disk or files in memory, under one module path.
type tree struct {
	root   string            // the directory on disk; "" for files in memory
	files  map[string][]byte // the files in memory, by path under the root; on disk, the files that differ from it
	gone   map[string]bool   // on disk, the files to leave out
	module string
	fset   *token.FileSet
	parsed int // files parsed in full

	dirs      map[string]*Pkg      // the packages, by directory
	generated map[string][]*File   // the generated non-test files, which no package holds, by directory
	named     map[string]*typeDecl // what typeNamed found, by "<dir> <name>", nil for a type it did not
}

// Load reads every non-generated .go file under root, skipping testdata, vendor and hidden directories: each file's
// package clause, package comment and imports (parser.ImportsOnly with comments). module is root's import path.
func Load(root, module string) ([]*Pkg, error) { return LoadOver(root, module, nil, nil) }

// LoadOver is Load with some files as they were elsewhere: changed holds the text of each file that differs from the
// disk (or is missing there), by path under the root, and gone the files on disk to leave out. It rebuilds another
// commit's tree from the files a change touched, without reading every file into memory.
func LoadOver(root, module string, changed map[string]string, gone map[string]bool) ([]*Pkg, error) {
	t := &tree{root: root, module: module, fset: token.NewFileSet(), files: map[string][]byte{}, gone: gone,
		generated: map[string][]*File{}, named: map[string]*typeDecl{}}
	for rel, text := range changed {
		t.files[rel] = []byte(text)
	}
	var rels []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") ||
				strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if _, over := changed[rel]; !over && !gone[rel] {
				rels = append(rels, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for rel := range changed {
		if strings.HasSuffix(rel, ".go") && !skipped(rel) && !gone[rel] {
			rels = append(rels, rel)
		}
	}
	return t.load(rels)
}

// LoadFiles is Load over files in memory: their text by path under the root (slash-separated), under module.
func LoadFiles(module string, files map[string]string) ([]*Pkg, error) {
	t := &tree{module: module, files: map[string][]byte{}, fset: token.NewFileSet(), generated: map[string][]*File{},
		named: map[string]*typeDecl{}}
	var rels []string
	for rel, text := range files {
		t.files[rel] = []byte(text)
		if strings.HasSuffix(rel, ".go") && !skipped(rel) {
			rels = append(rels, rel)
		}
	}
	return t.load(rels)
}

// skipped says whether a path under the root is one Load skips: inside a testdata, vendor or hidden directory, or a
// hidden file.
func skipped(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, d := range parts[:len(parts)-1] {
		if d == "testdata" || d == "vendor" || strings.HasPrefix(d, ".") || strings.HasPrefix(d, "_") {
			return true
		}
	}
	name := parts[len(parts)-1]
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Parsed is how many files of pkgs' trees have been parsed in full so far, by the rules and the counts.
func Parsed(pkgs []*Pkg) int {
	n := 0
	seen := map[*tree]bool{}
	for _, p := range pkgs {
		if !seen[p.tree] {
			seen[p.tree] = true
			n += p.tree.parsed
		}
	}
	return n
}

// Undeclared is the directories of pkgs, under the root, whose files carry no //persona:layer line at all: the names
// archguard/testdata/domain_layers/undeclared.txt freezes (scripts/test/name_freezes.py reads them through archguard's
// TestDomainLayerNames).
func Undeclared(pkgs []*Pkg) []string {
	var out []string
	for _, p := range pkgs {
		if len(p.declared) == 0 {
			out = append(out, display(p))
		}
	}
	sortStrings(out)
	return out
}

func (t *tree) read(rel string) ([]byte, error) {
	if src, ok := t.files[rel]; ok {
		return src, nil
	}
	if t.root == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(t.root, filepath.FromSlash(rel)))
}

// load reads the files' heads and groups them by directory.
func (t *tree) load(rels []string) ([]*Pkg, error) {
	sortStrings(rels)
	byDir := map[string]*Pkg{}
	var pkgs []*Pkg
	for _, rel := range rels {
		src, err := t.read(rel)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(t.fset, rel, src, parser.ImportsOnly|parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		dir := ""
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			dir = rel[:i]
		}
		if ast.IsGenerated(f) {
			// Left out of the rules, but a contract's types may be declared there (typeNamed).
			if !strings.HasSuffix(rel, "_test.go") {
				t.generated[dir] = append(t.generated[dir], &File{Rel: rel})
			}
			continue
		}
		p := byDir[dir]
		if p == nil {
			p = &Pkg{Rel: dir, Path: t.module, tree: t}
			if dir != "" {
				p.Path = t.module + "/" + dir
			}
			byDir[dir] = p
			pkgs = append(pkgs, p)
		}
		file := &File{Rel: rel, Test: strings.HasSuffix(rel, "_test.go"), pkg: p,
			wire: strings.Contains(string(src), ".Wire"), visit: strings.Contains(string(src), ") Visit")}
		name := f.Name.Name
		file.External = file.Test && strings.HasSuffix(name, "_test")
		if !file.External && p.Name == "" {
			p.Name = name
		}
		if f.Doc != nil {
			file.Doc = f.Doc.Text()
		}
		for _, c := range f.Comments {
			if c.Pos() > f.Package {
				break
			}
			for _, line := range c.List {
				if !strings.HasPrefix(line.Text, directivePrefix) {
					continue
				}
				layer := strings.TrimPrefix(line.Text, directivePrefix)
				if strings.HasPrefix(layer, " ") {
					layer = layer[1:]
				} else {
					layer = "" // //persona:layercore is no directive of any layer
				}
				if !file.External {
					p.declared = append(p.declared, directive{layer, t.fset.Position(line.Pos()), c == f.Doc})
				}
			}
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, "\"`")
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			}
			file.Imports = append(file.Imports, Import{path, name, t.fset.Position(imp.Pos())})
		}
		p.Files = append(p.Files, file)
	}
	for _, p := range pkgs {
		for _, d := range p.declared {
			if d.placed && knownLayer(d.layer) {
				p.Layer = d.layer
				break
			}
		}
	}
	t.dirs = byDir
	for dir, files := range t.generated {
		p := byDir[dir]
		if p == nil {
			// A package whose files are all generated: a stand-in that holds them, which no rule runs over.
			p = &Pkg{Rel: dir, Path: t.module, tree: t}
			if dir != "" {
				p.Path = t.module + "/" + dir
			}
		}
		for _, f := range files {
			f.pkg = p
		}
	}
	return pkgs, nil
}

// parse reads f in full, once.
func (f *File) parse() error {
	if f.ast != nil {
		return nil
	}
	t := f.pkg.tree
	src, err := t.read(f.Rel)
	if err != nil {
		return err
	}
	a, err := parser.ParseFile(t.fset, f.Rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	f.ast = a
	t.parsed++
	return nil
}

// Full parses every file of p in full and indexes its declarations.
func (p *Pkg) Full() error {
	for _, f := range p.Files {
		if err := f.parse(); err != nil {
			return err
		}
	}
	p.decls = index(p)
	return nil
}

// pos is where n is, as a position under the tree's root.
func (f *File) pos(n ast.Node) token.Position { return f.pkg.tree.fset.Position(n.Pos()) }

// importName is the name f refers to an import by: its name as written, else the last element of its path (the
// package name for every package of the tree and the standard library; a versioned path's element before /vN).
func importName(imp Import) string {
	if imp.Name != "" {
		return imp.Name
	}
	parts := strings.Split(imp.Path, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && allDigits(last[1:]) {
		last = parts[len(parts)-2]
	}
	return strings.ReplaceAll(last, "-", "")
}

func allDigits(s string) bool {
	_, ok := atoi(s)
	return ok
}
