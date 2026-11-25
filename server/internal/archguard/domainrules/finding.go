package domainrules

import (
	"go/ast"
	"go/token"
)

// A Finding is one broken rule: where, which rule, the package it counts against, and its message.
type Finding struct {
	Pos  token.Position
	Rule string // the rule's id, which is also its baseline key
	Key  string // the package it counts against: the core for out-* rules, the package itself otherwise
	Msg  string // RULE <id> <what>. WHY ... FIX ... GOOD ... see docs/server/architecture.md "Domain model"
	Edge string // for core-import, path-no-core and layer-declared: the name a frozen list may hold
}

// String is the finding as the tests print it: "<file>:<line>: RULE ...".
func (f Finding) String() string {
	if f.Pos.Filename == "" {
		return f.Msg
	}
	return f.Pos.Filename + ":" + itoa(f.Pos.Line) + ": " + f.Msg
}

// findings collects the findings of one pass.
type findings struct{ list []Finding }

func (fs *findings) add(pos token.Position, id, key, what string) {
	fs.list = append(fs.list, Finding{Pos: pos, Rule: id, Key: key, Msg: message(id, what)})
}

func (fs *findings) edge(pos token.Position, id, key, edge, what string) {
	fs.list = append(fs.list, Finding{Pos: pos, Rule: id, Key: key, Msg: message(id, what), Edge: edge})
}

// at is a file's position for a node.
func at(f *File, n ast.Node) token.Position { return f.pos(n) }

// first is where a package is: its first non-test file's package clause, else its first file's.
func first(p *Pkg) token.Position {
	for _, f := range p.Files {
		if !f.Test {
			return token.Position{Filename: f.Rel, Line: 1, Column: 1}
		}
	}
	if len(p.Files) > 0 {
		return token.Position{Filename: p.Files[0].Rel, Line: 1, Column: 1}
	}
	return token.Position{Filename: p.Rel}
}

// display is a package's name in messages: its directory under the root ("." for the root).
func display(p *Pkg) string {
	if p.Rel == "" {
		return "."
	}
	return p.Rel
}
