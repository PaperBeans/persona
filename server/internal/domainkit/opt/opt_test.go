package opt_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
)

func TestMatchAndFold(t *testing.T) {
	some, none := opt.Some(7), opt.None[int]()
	var zero opt.Option[int]
	if some.IsNone() || !none.IsNone() || !zero.IsNone() {
		t.Error("IsNone")
	}
	var got []string
	some.Match(func(n int) { got = append(got, "some") }, func() { got = append(got, "none") })
	zero.Match(func(n int) { got = append(got, "some") }, func() { got = append(got, "none") })
	if strings.Join(got, ",") != "some,none" {
		t.Errorf("Match: %v", got)
	}
	double := func(o opt.Option[int]) int {
		return opt.Fold(o, func(n int) int { return 2 * n }, func() int { return -1 })
	}
	if double(some) != 14 || double(none) != -1 {
		t.Error("Fold")
	}
	defer func() {
		if _, ok := recover().(defect.Defect); !ok {
			t.Error("Match with a nil function is no defect")
		}
	}()
	some.Match(nil, func() {})
}

// Option has no Get and no Or: a caller says what to do in each case instead of asking for the value.
func TestOptionHasNoGetterOrDefault(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil {
				methods = append(methods, fn.Name.Name)
			}
		}
	}
	if len(methods) == 0 {
		t.Fatal("read no methods")
	}
	for _, m := range methods {
		if m == "Get" || m == "Or" || strings.HasPrefix(m, "Get") || strings.HasPrefix(m, "Or") || m == "Value" {
			t.Errorf("Option has %s: absence is read by Match and Fold only", m)
		}
	}
}
