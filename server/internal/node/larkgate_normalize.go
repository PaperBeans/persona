package node

import (
	"bytes"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// normalizeLarkCommand removes inert here-documents and resolves immediately preceding
// literal command assignments solely for classification. It never executes the input.
func normalizeLarkCommand(cmd, cwd string) string {
	tree, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return cmd
	}
	// Restrict these transformations to a straight sequence: an assignment in a
	// conditional, loop or background job may never have happened.
	for _, stmt := range tree.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || stmt.Background || stmt.Negated || stmt.Coprocess {
			return cmd
		}
		for _, a := range call.Assigns {
			if a.Name == nil || a.Append || a.Index != nil || a.Array != nil || a.Value == nil {
				return cmd
			}
			if a.Name.Value == "IFS" || larkExecutableEnv[a.Name.Value] {
				return cmd // executable lookup and field splitting invalidate earlier inferences
			}
			if _, dynamic := executionWord(a.Value); dynamic {
				return cmd // expansions can assign shell state or alter executable lookup
			}
		}
	}
	vars := map[string]string{}
	changed, unsafe := false, false
	for _, stmt := range tree.Stmts {
		call := stmt.Cmd.(*syntax.CallExpr)
		if len(call.Args) == 0 {
			for _, a := range call.Assigns {
				if a.Name == nil {
					continue
				}
				delete(vars, a.Name.Value)
				if a.Append || a.Index != nil || a.Array != nil || a.Value == nil {
					continue
				}
				value, dynamic := executionWord(a.Value)
				if !dynamic && safeLiteralCommand(value) {
					vars[a.Name.Value] = value
				}
			}
			continue
		}
		words := make([]string, len(call.Args))
		for i, word := range call.Args {
			words[i], _ = executionWord(word)
		}
		suffix, ok := unwrapCommand(words)
		resolvedAbsolute := false
		if ok && len(suffix) > 0 {
			index := len(words) - len(suffix)
			dynamicArgs := false
			for i, arg := range call.Args {
				if i == index {
					continue
				}
				_, dynamic := executionWord(arg)
				if dynamic || wordExpandsToMany(arg) {
					dynamicArgs = true
					break
				}
			}
			if dynamicArgs {
				unsafe = true
				break // do not resolve the executable after another argument can change shell state
			}
			word := call.Args[index]
			if len(word.Parts) == 1 {
				if p, ok := word.Parts[0].(*syntax.ParamExp); ok && p.Param != nil && !p.Excl && !p.Length && !p.Width && p.Index == nil && p.Slice == nil && p.Repl == nil && p.Exp == nil && p.Names == 0 {
					if value, ok := vars[p.Param.Value]; ok {
						word.Parts = []syntax.WordPart{&syntax.Lit{Value: value}}
						resolvedAbsolute = filepath.IsAbs(value) && (index == 0 || filepath.Base(words[0]) == "timeout")
						changed = true
					}
				}
			}
		}
		// Only commands that cannot change the parent shell's state can take part.
		name := words[0]
		unchanged := resolvedAbsolute || map[string]bool{"echo": true, ":": true, "true": true, "false": true, "ls": true, "git": true, "go": true, "pwd": true, "date": true, "sleep": true, "cat": true, "tee": true, "head": true, "wc": true, "grep": true, "jq": true}[name]
		if !unchanged {
			unsafe = true
			break
		}
		if len(call.Assigns) != 0 || len(stmt.Redirs) != 0 {
			clear(vars)
		}
	}
	if unsafe {
		return cmd
	}

	// A heredoc fed only to a file writer is data, except when a later command
	// could execute it or its unquoted body contains an executable substitution.
	hasHeredoc, safe, goTest := false, true, false
	var bodies []*syntax.Word
	for _, stmt := range tree.Stmts {
		call := stmt.Cmd.(*syntax.CallExpr)
		if len(call.Args) == 0 {
			if len(stmt.Redirs) != 0 {
				safe = false
			}
			continue
		}
		name, dynamic := executionWord(call.Args[0])
		base := filepath.Base(name)
		// Do not infer safety from an interpreter denylist: build tools and
		// arbitrary programs can execute the file as well. Only these ordinary
		// file/inspection commands and the specified test invocation are inert.
		allowed := map[string]bool{"cat": true, "tee": true, "echo": true, "pwd": true, "ls": true, "head": true, "wc": true}[base]
		if base == "go" && len(call.Args) > 1 && call.Args[1].Lit() == "test" {
			allowed = true
			goTest = true
		}
		if !allowed || name != base || dynamic || dynamicCommandName(name) || len(call.Assigns) != 0 {
			safe = false
		}
		for _, arg := range call.Args[1:] {
			value, expanded := executionWord(arg)
			option, _, _ := strings.Cut(strings.TrimLeft(value, "-"), "=")
			if expanded || strings.HasPrefix(value, "-") && (option == "exec" || option == "toolexec") {
				safe = false
			}
		}
		for _, redir := range stmt.Redirs {
			if redir.Hdoc == nil {
				if (redir.Op != syntax.RdrOut && redir.Op != syntax.AppOut) || redir.Word == nil || !safeHeredocTarget(redir.Word.Lit(), cwd) {
					safe = false
				}
				continue
			}
			hasHeredoc = true
			bodies = append(bodies, redir.Hdoc)
			if base != "cat" && base != "tee" {
				safe = false
				continue
			}
			if base == "cat" {
				output := false
				for _, other := range stmt.Redirs {
					if (other.Op == syntax.RdrOut || other.Op == syntax.AppOut) && other.Word != nil && safeHeredocTarget(other.Word.Lit(), cwd) {
						output = true
					}
				}
				if !output || len(call.Args) != 1 {
					safe = false
				}
			} else {
				files := 0
				for _, arg := range call.Args[1:] {
					value := arg.Lit()
					if value == "-a" || value == "--append" {
						continue
					}
					if value == "" || strings.HasPrefix(value, "-") || !safeHeredocTarget(value, cwd) {
						safe = false
					}
					files++
				}
				if files == 0 {
					safe = false
				}
			}
			syntax.Walk(redir.Hdoc, func(n syntax.Node) bool {
				switch n.(type) {
				case *syntax.CmdSubst, *syntax.ProcSubst:
					safe = false
				}
				return true
			})
		}
	}
	if goTest {
		for _, body := range bodies {
			if !inertGoDocument(body) {
				safe = false
			}
		}
	}
	if hasHeredoc && safe {
		for _, stmt := range tree.Stmts {
			redirs := stmt.Redirs[:0]
			for _, r := range stmt.Redirs {
				if r.Hdoc == nil {
					redirs = append(redirs, r)
				}
			}
			stmt.Redirs = redirs
		}
		changed = true
	}
	if !changed {
		return cmd
	}
	var out bytes.Buffer
	if err := syntax.NewPrinter().Print(&out, tree); err != nil {
		return cmd
	}
	return out.String()
}

// Special files, symlinks and virtual filesystems can forward the heredoc to
// a running process. An unknown target is not proof of an ordinary file.
func safeHeredocTarget(name, cwd string) bool {
	if !safeLiteralCommand(name) {
		return false
	}
	if !filepath.IsAbs(name) {
		if !filepath.IsAbs(cwd) {
			return false
		}
		name = filepath.Join(cwd, name)
	}
	name = filepath.Clean(name)
	parent, err := filepath.EvalSymlinks(filepath.Dir(name))
	if err != nil {
		return false
	}
	for _, path := range []string{name, filepath.Join(parent, filepath.Base(name))} {
		for _, prefix := range []string{"/dev", "/proc", "/sys"} {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return false
			}
		}
	}
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return true
	}
	return err == nil && info.Mode().IsRegular()
}

// go test can execute generated tests and package initializers. Only a document
// containing comments and an optional package clause is inert for this purpose.
func inertGoDocument(body *syntax.Word) bool {
	value, dynamic := executionWord(body)
	if dynamic {
		return false
	}
	content := []byte(value)
	file := token.NewFileSet().AddFile("", -1, len(content))
	var scan scanner.Scanner
	valid := true
	scan.Init(file, content, func(token.Position, string) { valid = false }, scanner.ScanComments)
	packageName := false
	seenPackage := false
	for {
		_, tok, _ := scan.Scan()
		switch tok {
		case token.EOF:
			return valid && !packageName
		case token.COMMENT, token.SEMICOLON:
		case token.PACKAGE:
			if seenPackage || packageName {
				return false
			}
			seenPackage, packageName = true, true
		case token.IDENT:
			if !packageName {
				return false
			}
			packageName = false
		default:
			return false
		}
	}
}

func safeLiteralCommand(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._+-", c) {
			continue
		}
		return false
	}
	return true
}
