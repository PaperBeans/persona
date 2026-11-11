package node

import (
	"io"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/js"
)

// A release installer is small. Bound lexing of the untrusted source, without executing it or constructing an AST.
const lcuInstallerSourceMost = 1 << 20
const lcuInstallerTokensMost = 200000

// lcuForwardingInstallerCompatible accepts the reviewed installer's recognizable structure, including the binding
// from argv to the runtime-only flag and the return before setup. This is a conservative token check, not proof of
// JavaScript semantics: unreachable code, changed imports or other statements can still change behavior.
func lcuForwardingInstallerCompatible(source []byte) (compatible bool) {
	defer func() {
		if recover() != nil {
			compatible = false
		}
	}()
	tokens, depths, ok := lcuInstallerTokens(source)
	if !ok {
		return false
	}
	find := func(start, end, depth int, want ...string) int {
		for i := start; i+len(want) <= end; i++ {
			if depths[i] != depth {
				continue
			}
			matched := true
			for j, token := range want {
				if tokens[i+j] != token {
					matched = false
					break
				}
			}
			if matched {
				return i
			}
		}
		return -1
	}
	options := find(0, len(tokens), 0, "const", "OPTIONS", "=", "{")
	main := find(0, len(tokens), 0, "export", "async", "function", "main", "(", "argv", ")", "{")
	if options < 0 || main < 0 || options >= main || !lcuStatementStart(tokens, options) || !lcuStatementStart(tokens, main) {
		return false
	}
	optionsEnd := lcuClosingBrace(tokens, depths, options+3)
	if optionsEnd < 0 || optionsEnd >= main {
		return false
	}
	option := find(options+4, optionsEnd, 1, "<runtime-only>", ":", "{", "type", ":", "<boolean>")
	if option < 0 || find(option+1, optionsEnd, 1, "<runtime-only>", ":") >= 0 {
		return false
	}
	open := main + 7
	end := lcuClosingBrace(tokens, depths, open)
	if end < 0 {
		return false
	}
	values := find(open+1, end, 1, "let", "values", ";")
	try := find(open+1, end, 1, "try", "{")
	if try < 0 || !lcuStatementStart(tokens, try) {
		return false
	}
	tryEnd := lcuClosingBrace(tokens, depths, try+1)
	if tryEnd < 0 || tryEnd >= end {
		return false
	}
	parseArgs := find(try+2, tryEnd, 2, "values", "=", "parse", "(", "argv", ",", "OPTIONS", ")", ";")
	flag := find(open+1, end, 1, "const", "runtimeOnly", "=", "values", "[", "<runtime-only>", "]", ";")
	if values < 0 || parseArgs <= values || flag <= parseArgs || !lcuStatementStart(tokens, values) ||
		!lcuStatementStart(tokens, parseArgs) || !lcuStatementStart(tokens, flag) {
		return false
	}
	legacy := find(parseArgs+1, flag, 1, "if", "(", "legacy", "&&", "!", "values", ".", "agent", ".", "length", "&&", "!", "values", ".", "export", ")",
		"values", "[", "<runtime-only>", "]", "=", "true", ";")
	// Reject a discarded parse result or direct reassignment that disconnects these two bindings.
	for i := open + 1; i+1 < end; i++ {
		if tokens[i] == "values" && tokens[i+1] == "=" && i != parseArgs {
			return false
		}
		if tokens[i] == "runtimeOnly" && tokens[i+1] == "=" && i != flag+1 {
			return false
		}
		if tokens[i] == "values" && tokens[i+1] == "[" {
			// No later indexed assignment may overwrite the parsed flag, including an escaped or computed key.
			// The reviewed installer's only exception sets the flag to true for its older positional form.
			brackets, close := 1, -1
			for j := i + 2; j < end && j < i+66; j++ {
				switch tokens[j] {
				case "[":
					brackets++
				case "]":
					brackets--
					if brackets == 0 {
						close = j
					}
				}
				if close >= 0 {
					break
				}
			}
			if close < 0 {
				return false
			}
			if close+1 < end && lcuAssignmentToken(tokens[close+1]) && (legacy < 0 || i != legacy+16) {
				return false
			}
			if i > open+1 && (tokens[i-1] == "delete" || tokens[i-1] == "++" || tokens[i-1] == "--") {
				return false
			}
		}
		if i+3 < end && tokens[i] == "values" && tokens[i+1] == "." && lcuAssignmentToken(tokens[i+3]) {
			return false
		}
	}

	returned := -1
	for at := flag + 8; at < end; {
		branch := find(at, end, 1, "if", "(", "runtimeOnly", ")", "{")
		if branch < 0 {
			break
		}
		if !lcuStatementStart(tokens, branch) {
			return false
		}
		close := lcuClosingBrace(tokens, depths, branch+4)
		if close < 0 || close >= end {
			return false
		}
		// The final statement in the branch must be an unconditional return at that branch's own depth.
		if close >= branch+8 && depths[close-3] == 2 && tokens[close-3] == "return" &&
			tokens[close-2] == "0" && tokens[close-1] == ";" &&
			(tokens[close-4] == "{" || tokens[close-4] == "}" || tokens[close-4] == ";") {
			returned = close
			break
		}
		at = close + 1
	}
	if returned < 0 || !lcuSetupCallAfter(tokens, depths, returned+1, end) {
		return false
	}
	return find(end+1, len(tokens), 0, "if", "(", "isMain", "(", "import", ".", "meta", ")", ")",
		"run", "(", "<string>", ",", "(", ")", "=>", "main", "(", "process", ".", "argv", ".", "slice", "(", "2", ")", ")", ")", ";") >= 0
}

func lcuSetupCallAfter(tokens []string, depths []int, start, end int) bool {
	for i := start; i+4 < end; i++ {
		if depths[i] == 1 && (tokens[i] == "const" || tokens[i] == "let") && lcuStatementStart(tokens, i) &&
			tokens[i+2] == "=" && tokens[i+3] == "setupArguments" && tokens[i+4] == "(" {
			return true
		}
	}
	return false
}

func lcuStatementStart(tokens []string, at int) bool {
	return at == 0 || tokens[at-1] == "{" || tokens[at-1] == "}" || tokens[at-1] == ";"
}

func lcuAssignmentToken(token string) bool {
	switch token {
	case "=", "+=", "-=", "*=", "/=", "%=", "**=", "&&=", "||=", "??=", "&=", "|=", "^=", "<<=", ">>=", ">>>=", "++", "--":
		return true
	}
	return false
}

// lcuInstallerTokens discards comments and treats ordinary strings and template literals as opaque values. It
// rejects slash operators because the lexer alone cannot distinguish division from a regular-expression literal.
func lcuInstallerTokens(source []byte) ([]string, []int, bool) {
	if len(source) == 0 || len(source) > lcuInstallerSourceMost {
		return nil, nil, false
	}
	lexer := js.NewLexer(parse.NewInputBytes(source))
	var tokens []string
	var depths []int
	depth, templateDepth := 0, 0
	add := func(token string) bool {
		if len(tokens) >= lcuInstallerTokensMost {
			return false
		}
		tokens = append(tokens, token)
		depths = append(depths, depth)
		switch token {
		case "{":
			depth++
		case "}":
			depth--
		}
		return depth >= 0
	}
	for {
		kind, raw := lexer.Next()
		if kind == js.ErrorToken {
			return tokens, depths, lexer.Err() == io.EOF && depth == 0 && templateDepth == 0
		}
		switch kind {
		case js.TemplateStartToken:
			templateDepth++
			if templateDepth == 1 && !add("<template>") {
				return nil, nil, false
			}
			continue
		case js.TemplateEndToken:
			templateDepth--
			if templateDepth < 0 {
				return nil, nil, false
			}
			continue
		case js.TemplateMiddleToken:
			if templateDepth == 0 {
				return nil, nil, false
			}
			continue
		case js.TemplateToken:
			if templateDepth == 0 && !add("<template>") {
				return nil, nil, false
			}
			continue
		}
		if templateDepth > 0 {
			continue
		}
		switch kind {
		case js.WhitespaceToken, js.LineTerminatorToken, js.CommentToken, js.CommentLineTerminatorToken:
			continue
		case js.DivToken, js.DivEqToken:
			return nil, nil, false
		case js.StringToken:
			token := "<string>"
			switch string(raw) {
			case `'runtime-only'`, `"runtime-only"`:
				token = "<runtime-only>"
			case `'boolean'`, `"boolean"`:
				token = "<boolean>"
			}
			if !add(token) {
				return nil, nil, false
			}
		default:
			if !add(string(raw)) {
				return nil, nil, false
			}
		}
	}
}

func lcuClosingBrace(tokens []string, depths []int, open int) int {
	if open >= len(tokens) || tokens[open] != "{" {
		return -1
	}
	for i := open + 1; i < len(tokens); i++ {
		if tokens[i] == "}" && depths[i] == depths[open]+1 {
			return i
		}
	}
	return -1
}
