package fail_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
)

func raises(t *testing.T, op string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		d, ok := recover().(defect.Defect)
		if !ok || !strings.HasPrefix(d.Error(), op+": ") {
			t.Errorf("want a defect from %s, got %v", op, d)
		}
	}()
	f()
}

func TestCodesAndParts(t *testing.T) {
	for _, bad := range []string{"", "Inbox.item", "inbox item", "inbox-item", "ünicode"} {
		raises(t, "fail.NewCode", func() { fail.NewCode(bad) })
	}
	for _, bad := range []string{"", "machine_id", "a part", "a-part"} {
		raises(t, "fail.NewPart", func() { fail.NewPart(bad) })
	}
	if fail.NewCode("inbox.item.not_done").Wire() != "inbox.item.not_done" {
		t.Error("a code's Wire is not its text")
	}
	if fail.NewPart("searchText").Wire() != "searchText" {
		t.Error("a part's Wire is not its text")
	}
}

func TestTheBasesRefuseZeroCodesAndParts(t *testing.T) {
	var code fail.Code
	var part fail.Part
	raises(t, "fail.NewInvalid", func() { fail.NewInvalid(code, fail.NewPart("machine")) })
	raises(t, "fail.NewInvalid", func() { fail.NewInvalid(fail.NewCode("a.b"), part) })
	raises(t, "fail.NewRefusal", func() { fail.NewRefusal(code) })
	raises(t, "fail.NewAbsence", func() { fail.NewAbsence(code) })
	raises(t, "fail.NewConflict", func() { fail.NewConflict(code) })
	var invalid fail.Invalid
	raises(t, "fail.NewInvalids", func() { fail.NewInvalids(invalid) })
	raises(t, "fail.Invalids.With", func() { fail.NewInvalids(fail.NewInvalid(fail.NewCode("a"), fail.NewPart("b"))).With(invalid) })
	var none fail.Invalids
	raises(t, "fail.Invalids.Accept", func() { domaintest.Describe(none) })
}

// A base's zero value is no failure: visiting it is a defect, so a family type built empty (notFound{} over a
// fail.Absence) never reaches a caller as a failure with no code.
func TestTheZeroBasesRefuseToBeVisited(t *testing.T) {
	var invalid fail.Invalid
	var refusal fail.Refusal
	var absence fail.Absence
	var conflict fail.Conflict
	raises(t, "fail.Invalid.Accept", func() { domaintest.Describe(invalid) })
	raises(t, "fail.Refusal.Accept", func() { domaintest.Describe(refusal) })
	raises(t, "fail.Absence.Accept", func() { domaintest.Describe(absence) })
	raises(t, "fail.Conflict.Accept", func() { domaintest.Describe(conflict) })
}

// Each base sends its own visitor call, so a Refusal is never read as an Absence.
func TestEachBaseVisitsItsOwnKind(t *testing.T) {
	code := fail.NewCode("x.y")
	for _, c := range []struct {
		f    fail.Fail
		want string
	}{
		{fail.NewInvalid(code, fail.NewPart("machine")), "invalid x.y machine"},
		{fail.NewRefusal(code), "refused x.y"},
		{fail.NewAbsence(code), "missing x.y"},
		{fail.NewConflict(code), "conflict x.y"},
	} {
		if got := domaintest.Describe(c.f); got != c.want {
			t.Errorf("Describe = %q, want %q", got, c.want)
		}
	}
}

func TestInvalidsKeepTheirOrder(t *testing.T) {
	first := fail.NewInvalids(fail.NewInvalid(fail.NewCode("a.one"), fail.NewPart("one")))
	two := first.With(fail.NewInvalid(fail.NewCode("a.two"), fail.NewPart("two")))
	three := two.With(fail.NewInvalid(fail.NewCode("a.three"), fail.NewPart("three")))
	other := two.With(fail.NewInvalid(fail.NewCode("a.other"), fail.NewPart("other")))
	if got := domaintest.Describe(three); got != "invalid a.one one; invalid a.two two; invalid a.three three" {
		t.Errorf("three = %q", got)
	}
	if got := domaintest.Describe(other); got != "invalid a.one one; invalid a.two two; invalid a.other other" {
		t.Errorf("a second With on the same list changed the first: %q", got)
	}
	if got := domaintest.Describe(first); got != "invalid a.one one" {
		t.Errorf("With changed its receiver: %q", got)
	}
}

// Codes and parts cannot be compared: comparing them does not compile, so nothing decides on a code's text. The case
// is compiled by go vet on a temporary copy of the kit's fail and defect packages.
func TestCodesAndPartsDoNotCompare(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	dir := t.TempDir()
	const module = "kitcopy.test"
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module "+module+"\n\ngo 1.27\n")
	for _, pkg := range []string{"fail", "defect"} {
		src := filepath.Join("..", pkg)
		files, err := filepath.Glob(filepath.Join(src, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			text, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(pkg, filepath.Base(file)), strings.ReplaceAll(string(text),
				"github.com/nwverse/persona/server/internal/domainkit/", module+"/"))
		}
	}
	for _, c := range []struct{ name, expr string }{
		{"codes", `fail.NewCode("a.b") == fail.NewCode("a.b")`},
		{"parts", `fail.NewPart("a") == fail.NewPart("a")`},
	} {
		write(filepath.Join("compare", c.name, "compare.go"), "package compare\n\nimport \""+module+
			"/fail\"\n\nvar Same = "+c.expr+"\n")
		cmd := exec.Command(goBin, "vet", "./compare/"+c.name)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot be compared") {
			t.Errorf("comparing %s: go vet says %v:\n%s\nwant a compile error that they cannot be compared", c.name, err, out)
		}
	}
	// The same copy compiles without the comparison, so the error above is the comparison's.
	write("use/use.go", "package use\n\nimport \""+module+"/fail\"\n\nvar Code = fail.NewCode(\"a.b\")\n")
	cmd := exec.Command(goBin, "vet", "./use")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("the copy of the kit does not compile: %v\n%s", err, out)
	}
}
