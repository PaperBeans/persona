// Package lazyre is a regular expression compiled the first time it is used, for package-level patterns.
//
// A package-level regexp.MustCompile runs when the program starts, in every program that links the package, whether
// or not it ever matches anything. personad and persona-node link all of the server, and Claude Code waits for
// `personad hook` on each of Persona's hook events (#1308): compiling the server's four hundred patterns took about
// a third of that hook's start. A package-level pattern is therefore lazyre.New, which
// keeps the call sites as they were (the same methods); guard_test.go keeps it that way. A contract package, which
// depends on the standard library only (docs/server/architecture.md "Contract packages"), uses sync.OnceValue instead.
//
// In a test binary New compiles at once, as regexp.MustCompile did: a broken pattern still fails every test of its
// package, not only the one that reaches it (the standard library's internal/lazyregexp does the same).
//
//persona:layer library
package lazyre

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Regexp is a regexp.Regexp compiled from its source on first use; safe for concurrent use.
type Regexp struct {
	src   string
	gated bool // NewGated: some methods look for gate's literals first
	once  sync.Once
	rx    *regexp.Regexp
	gate  *gate // gate.go: what every match starts with; nil: none
}

// inTest: a `go test` binary (pkg.test), where patterns compile at once.
var inTest = len(os.Args) > 0 && strings.HasSuffix(strings.TrimSuffix(os.Args[0], ".exe"), ".test")

// New is regexp.MustCompile(src), compiled when it is first used (at once in a test binary); a bad src panics then.
func New(src string) *Regexp {
	r := &Regexp{src: src}
	if inTest {
		r.Regexp()
	}
	return r
}

// Regexp is the compiled expression.
func (r *Regexp) Regexp() *regexp.Regexp {
	r.once.Do(func() {
		r.rx = regexp.MustCompile(r.src)
		if r.gated {
			r.gate = newGate(r.src)
		}
	})
	return r.rx
}

// String is the source text, as regexp.Regexp's String.
func (r *Regexp) String() string { return r.src }

// The methods of regexp.Regexp the server uses, unchanged (a gated pattern's MatchString and its string replacements
// look for its literals first).

func (r *Regexp) MatchString(s string) bool {
	rx := r.Regexp()
	if r.gate != nil && !r.gate.open(s) {
		return false
	}
	return rx.MatchString(s)
}
func (r *Regexp) Match(b []byte) bool                  { return r.Regexp().Match(b) }
func (r *Regexp) FindString(s string) string           { return r.Regexp().FindString(s) }
func (r *Regexp) FindStringIndex(s string) []int       { return r.Regexp().FindStringIndex(s) }
func (r *Regexp) FindStringSubmatch(s string) []string { return r.Regexp().FindStringSubmatch(s) }
func (r *Regexp) FindStringSubmatchIndex(s string) []int {
	return r.Regexp().FindStringSubmatchIndex(s)
}
func (r *Regexp) FindAllString(s string, n int) []string { return r.Regexp().FindAllString(s, n) }
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int {
	return r.Regexp().FindAllStringIndex(s, n)
}
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.Regexp().FindAllStringSubmatch(s, n)
}
func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.Regexp().FindAllStringSubmatchIndex(s, n)
}
func (r *Regexp) Find(b []byte) []byte             { return r.Regexp().Find(b) }
func (r *Regexp) FindIndex(b []byte) []int         { return r.Regexp().FindIndex(b) }
func (r *Regexp) FindSubmatch(b []byte) [][]byte   { return r.Regexp().FindSubmatch(b) }
func (r *Regexp) FindSubmatchIndex(b []byte) []int { return r.Regexp().FindSubmatchIndex(b) }
func (r *Regexp) FindAll(b []byte, n int) [][]byte { return r.Regexp().FindAll(b, n) }
func (r *Regexp) FindAllIndex(b []byte, n int) [][]int {
	return r.Regexp().FindAllIndex(b, n)
}
func (r *Regexp) FindAllSubmatch(b []byte, n int) [][][]byte {
	return r.Regexp().FindAllSubmatch(b, n)
}
func (r *Regexp) FindAllSubmatchIndex(b []byte, n int) [][]int {
	return r.Regexp().FindAllSubmatchIndex(b, n)
}
func (r *Regexp) ReplaceAllString(src, repl string) string {
	rx := r.Regexp()
	if r.gate != nil && !r.gate.open(src) {
		return src // what regexp gives back (as a copy) when nothing matches
	}
	return rx.ReplaceAllString(src, repl)
}
func (r *Regexp) ReplaceAllLiteralString(src, repl string) string {
	rx := r.Regexp()
	if r.gate != nil && !r.gate.open(src) {
		return src
	}
	return rx.ReplaceAllLiteralString(src, repl)
}
func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	rx := r.Regexp()
	if r.gate != nil && !r.gate.open(src) {
		return src // what regexp gives back when nothing matches
	}
	return rx.ReplaceAllStringFunc(src, repl)
}
func (r *Regexp) ReplaceAll(src, repl []byte) []byte { return r.Regexp().ReplaceAll(src, repl) }
func (r *Regexp) ReplaceAllFunc(src []byte, repl func([]byte) []byte) []byte {
	return r.Regexp().ReplaceAllFunc(src, repl)
}
func (r *Regexp) Split(s string, n int) []string { return r.Regexp().Split(s, n) }
func (r *Regexp) SubexpNames() []string          { return r.Regexp().SubexpNames() }
func (r *Regexp) SubexpIndex(name string) int    { return r.Regexp().SubexpIndex(name) }
func (r *Regexp) NumSubexp() int                 { return r.Regexp().NumSubexp() }
