// Package builtin — recursive-descent parser (M2.4).
//
// This file consumes the []Token produced by Tokenize() and emits an AST
// of the node types declared in ast.go. The grammar matches the Python
// reference at src/python/shell.py (`class Parser`, lines 356-639) with two
// intentional structural tweaks documented in ast.go:
//
//  1. AndOr is flat rather than a left-leaning binary tree.
//  2. SimpleCommand splits leading assignments into a dedicated slice.
//
// The parser never resolves expansions — `$FOO`, `$(date)`, globs, and
// tildes remain in Word.Parts for the expander. Reserved-word recognition
// is context-sensitive: only TokWord tokens in "command position" are
// promoted to if/then/for/etc. A token is in command position when it
// immediately follows start-of-input, a separator (`;`, `\n`, `|`, `&&`,
// `||`), an opening `(`, or another promoted keyword that opens a
// sub-grammar (`then`, `else`, `do`, `in`, `elif`).
//
// Nesting is bounded at 50 levels (matching Python) to stop pathological
// inputs from blowing the stack.
package builtin

import (
	"fmt"
	"regexp"
)

// maxParseDepth caps recursion to protect against adversarial input.
const maxParseDepth = 50

// assignmentRe matches the "NAME=rest" shape of an assignment prefix.
// NAME must be a portable shell variable name. The tokenizer yields the
// whole `NAME=rest` run as a single TokWord whose first Part is Unquoted
// (quoted bytes start a new Part), so checking `NAME=` against that first
// Part is sufficient.
var assignmentRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)

// parseError is a typed error with a position for richer diagnostics.
type parseError struct {
	Pos int
	Msg string
}

func (e *parseError) Error() string {
	return fmt.Sprintf("parse: %s at position %d", e.Msg, e.Pos)
}

// Parse consumes tokens and returns the top-level Node. The return is
// ALWAYS a *List (possibly empty), so callers have a stable outer shape
// regardless of the input. Inner one-element lists / pipelines / and-or
// chains are unwrapped for compactness.
func Parse(tokens []Token) (Node, error) {
	p := &parser{tokens: tokens}
	node, err := p.parseProgram()
	if err != nil {
		return nil, err
	}
	return node, nil
}

// parser holds the cursor state for a single Parse call.
type parser struct {
	tokens []Token
	pos    int
	depth  int
}

// peek returns the current token without advancing. If the cursor is past
// the end, returns a synthetic TokEOF.
func (p *parser) peek() Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return Token{Kind: TokEOF}
}

// peekAt returns the token at offset from the cursor (0 = current).
func (p *parser) peekAt(offset int) Token {
	j := p.pos + offset
	if j < 0 || j >= len(p.tokens) {
		return Token{Kind: TokEOF}
	}
	return p.tokens[j]
}

// advance returns the current token and moves the cursor forward.
func (p *parser) advance() Token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

// atEnd reports whether the cursor is at TokEOF.
func (p *parser) atEnd() bool {
	return p.peek().Kind == TokEOF
}

// errorf builds a parseError at the current position.
func (p *parser) errorf(format string, args ...any) error {
	t := p.peek()
	return &parseError{Pos: t.Pos, Msg: fmt.Sprintf(format, args...)}
}

// skipNewlines advances past any TokNewline tokens.
func (p *parser) skipNewlines() {
	for p.peek().Kind == TokNewline {
		p.advance()
	}
}

// skipSemiNewlines advances past any TokSemicolon / TokNewline tokens.
func (p *parser) skipSemiNewlines() {
	for {
		k := p.peek().Kind
		if k == TokNewline || k == TokSemicolon {
			p.advance()
			continue
		}
		break
	}
}

// isKeyword reports whether the current token is a TokWord whose raw value
// (unquoted, first part) matches kw — i.e. whether it is the keyword kw
// in command position. Quoted `kw` does NOT match.
func (p *parser) isKeyword(kw string) bool {
	t := p.peek()
	if t.Kind != TokWord {
		return false
	}
	return wordIsKeyword(t, kw)
}

// wordIsKeyword reports whether a TokWord token represents the bare
// keyword kw. The check is: the word consists of exactly one Unquoted
// part whose text equals kw.
func wordIsKeyword(t Token, kw string) bool {
	if len(t.Parts) != 1 {
		return false
	}
	p := t.Parts[0]
	return p.Kind == WpUnquoted && p.Text == kw
}

// isAnyKeyword returns the matched keyword if the current token is one
// of the provided keywords in command position, or "" otherwise.
func (p *parser) isAnyKeyword(kws ...string) string {
	t := p.peek()
	if t.Kind != TokWord {
		return ""
	}
	for _, kw := range kws {
		if wordIsKeyword(t, kw) {
			return kw
		}
	}
	return ""
}

// expectKeyword consumes the keyword kw or errors.
func (p *parser) expectKeyword(kw string) error {
	if !p.isKeyword(kw) {
		return p.errorf("expected %q", kw)
	}
	p.advance()
	return nil
}

// isCommandTerminator reports whether the current token ends a simple
// command.
//
// Deviation from Python: Python's `_is_command_terminator` also returns
// true for reserved-word keywords (then/elif/else/fi/do/done/esac). We
// match the task spec instead: reserved words are keywords only when in
// command position (first token of a fresh command). Inside a simple
// command they are plain words — so `echo if then fi` parses as a single
// command with four word args, matching bash's actual behaviour. This
// makes no difference to valid compound commands because they always put
// a `;` or `\n` between the body and the closing keyword.
func (p *parser) isCommandTerminator() bool {
	t := p.peek()
	switch t.Kind {
	case TokEOF, TokSemicolon, TokDoubleSemicolon, TokNewline,
		TokAndAnd, TokOrOr, TokPipe, TokRParen:
		return true
	}
	return false
}

// isCompoundEnd reports whether the current token is a compound-command
// terminator keyword (fi/done/esac) or an intermediate keyword
// (then/elif/else/do). Used by list parsing to stop before a compound
// boundary.
func (p *parser) isCompoundEnd() bool {
	return p.isAnyKeyword("fi", "done", "then", "elif", "else", "do", "esac") != ""
}

// enter increases the nesting depth and errors if the cap is exceeded.
func (p *parser) enter() error {
	p.depth++
	if p.depth > maxParseDepth {
		return p.errorf("nesting depth limit exceeded")
	}
	return nil
}

// leave decreases the nesting depth.
func (p *parser) leave() {
	p.depth--
}

// --------------------------------------------------------------------
// Top-level
// --------------------------------------------------------------------

// parseProgram consumes the entire token stream and returns a *List. The
// outer *List may be empty if the input has no statements.
func (p *parser) parseProgram() (Node, error) {
	n, err := p.parseList()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		// A stray `)` is unambiguously unbalanced.
		if p.peek().Kind == TokRParen {
			return nil, p.errorf("unbalanced ')'")
		}
		// Any other leftover (reserved keywords like stray `then`,
		// `fi`, `done`, etc.) is treated as a parse error. Python is
		// lenient here — it silently drops trailing tokens — but that
		// hides real bugs and is not required by any known caller.
		return nil, p.errorf("unexpected token %s (%q)", p.peek().Kind, p.peek().Value)
	}
	// Always return a *List at the top level. parseList unwraps
	// single-statement lists; re-wrap here if needed.
	if l, ok := n.(*List); ok {
		return l, nil
	}
	return &List{Statements: []Node{n}}, nil
}

// parseList parses a sequence of and-or chains separated by ; or \n.
// Returns a *List if 2+ statements, the inner node if 1, or an empty
// *List if 0.
func (p *parser) parseList() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	p.skipSemiNewlines()
	if p.atEnd() || p.isCompoundEnd() || p.peek().Kind == TokRParen {
		return &List{}, nil
	}

	first, err := p.parseAndOr()
	if err != nil {
		return nil, err
	}
	stmts := []Node{first}

	for {
		k := p.peek().Kind
		if k != TokSemicolon && k != TokNewline {
			break
		}
		p.skipSemiNewlines()
		if p.atEnd() || p.isCompoundEnd() || p.peek().Kind == TokRParen {
			break
		}
		next, err := p.parseAndOr()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, next)
	}

	if len(stmts) == 1 {
		return stmts[0], nil
	}
	return &List{Statements: stmts}, nil
}

// parseAndOr parses pipeline (('&&'|'||') pipeline)*. Returns a flat
// *AndOr with Children interleaved with Ops.
func (p *parser) parseAndOr() (Node, error) {
	first, err := p.parsePipeline()
	if err != nil {
		return nil, err
	}
	children := []Node{first}
	var ops []AndOrOp

	for {
		var op AndOrOp
		switch p.peek().Kind {
		case TokAndAnd:
			op = OpAnd
		case TokOrOr:
			op = OpOr
		default:
			if len(children) == 1 {
				return first, nil
			}
			return &AndOr{Children: children, Ops: ops}, nil
		}
		p.advance()
		p.skipNewlines()
		if p.atEnd() || p.isCommandTerminator() {
			return nil, p.errorf("expected command after %q", op)
		}
		next, err := p.parsePipeline()
		if err != nil {
			return nil, err
		}
		children = append(children, next)
		ops = append(ops, op)
	}
}

// parsePipeline parses command ('|' command)*.
func (p *parser) parsePipeline() (Node, error) {
	first, err := p.parseCommand()
	if err != nil {
		return nil, err
	}
	cmds := []Node{first}
	for p.peek().Kind == TokPipe {
		p.advance()
		p.skipNewlines()
		if p.atEnd() || p.isCommandTerminator() {
			return nil, p.errorf("expected command after %q", "|")
		}
		c, err := p.parseCommand()
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, c)
	}
	if len(cmds) == 1 {
		return first, nil
	}
	return &Pipeline{Commands: cmds}, nil
}

// parseCommand dispatches to the appropriate production based on the
// current token. Compound commands first, subshell next, simple command
// otherwise.
func (p *parser) parseCommand() (Node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	switch p.peek().Kind {
	case TokLParen:
		return p.parseSubshell()
	}

	switch p.isAnyKeyword("if", "for", "while", "until", "case") {
	case "if":
		return p.parseIf()
	case "for":
		return p.parseFor()
	case "while":
		return p.parseWhile(false)
	case "until":
		return p.parseWhile(true)
	case "case":
		return p.parseCase()
	}

	return p.parseSimpleCommand()
}

// --------------------------------------------------------------------
// Simple command
// --------------------------------------------------------------------

// parseSimpleCommand parses `[assignments]* [word]* [redirections]*` in
// any mixed order after the assignments prefix. Leading NAME=VALUE tokens
// are split into Assignments. Subsequent NAME=VALUE tokens are left as
// plain arg words (only leading assignments are special).
func (p *parser) parseSimpleCommand() (Node, error) {
	sc := &SimpleCommand{}

	// Phase 1: consume leading assignments. Stop as soon as we see a
	// non-assignment word or an operator.
	for !p.isCommandTerminator() {
		t := p.peek()
		if t.Kind != TokWord {
			break
		}
		name, value, ok := splitAssignment(t)
		if !ok {
			break
		}
		p.advance()
		sc.Assignments = append(sc.Assignments, Assignment{Name: name, Value: value})
	}

	// Phase 2: words + redirections mixed.
	for !p.isCommandTerminator() {
		t := p.peek()
		switch t.Kind {
		case TokWord:
			sc.Words = append(sc.Words, tokenToWord(t))
			p.advance()
		case TokRedirOut, TokRedirAppend, TokRedirIn,
			TokRedirErr, TokRedirErrAppend, TokRedirBoth:
			r, err := p.parseRedirectionWithTarget(t.Kind)
			if err != nil {
				return nil, err
			}
			sc.Redirections = append(sc.Redirections, r)
		case TokRedirErrOut:
			p.advance()
			sc.Redirections = append(sc.Redirections, Redirection{Kind: RedirErrOut})
		default:
			// Anything else is outside a simple command; stop.
			goto done
		}
	}
done:
	if len(sc.Assignments) == 0 && len(sc.Words) == 0 && len(sc.Redirections) == 0 {
		// Could happen if called with the cursor already at a
		// terminator (e.g. `| foo`). Return an error so the caller
		// knows the production failed.
		return nil, p.errorf("expected command")
	}
	return sc, nil
}

// redirKindFor maps a redirection TokenKind to its RedirKind. Only the
// variants that take a Target are listed here.
func redirKindFor(k TokenKind) (RedirKind, bool) {
	switch k {
	case TokRedirOut:
		return RedirOut, true
	case TokRedirAppend:
		return RedirAppend, true
	case TokRedirIn:
		return RedirIn, true
	case TokRedirErr:
		return RedirErr, true
	case TokRedirErrAppend:
		return RedirErrAppend, true
	case TokRedirBoth:
		return RedirBoth, true
	}
	return 0, false
}

// parseRedirectionWithTarget consumes `OP WORD` and returns the
// Redirection. Errors if WORD is missing.
func (p *parser) parseRedirectionWithTarget(k TokenKind) (Redirection, error) {
	kind, ok := redirKindFor(k)
	if !ok {
		return Redirection{}, p.errorf("internal: unexpected redirection kind %s", k)
	}
	opTok := p.advance()
	t := p.peek()
	if t.Kind != TokWord {
		return Redirection{}, &parseError{
			Pos: opTok.Pos,
			Msg: fmt.Sprintf("expected filename after %s", opTok.Value),
		}
	}
	p.advance()
	return Redirection{Kind: kind, Target: tokenToWord(t)}, nil
}

// splitAssignment returns (name, value, true) if tok is a TokWord whose
// first Part is an Unquoted segment matching NAME= ... Otherwise returns
// (_, _, false). The value Word contains all bytes AFTER the `=`, preserving
// the quoting contexts of any subsequent parts.
func splitAssignment(tok Token) (string, Word, bool) {
	if tok.Kind != TokWord || len(tok.Parts) == 0 {
		return "", Word{}, false
	}
	first := tok.Parts[0]
	if first.Kind != WpUnquoted {
		return "", Word{}, false
	}
	m := assignmentRe.FindStringSubmatch(first.Text)
	if m == nil {
		return "", Word{}, false
	}
	name := m[1]
	rest := first.Text[len(m[0]):]

	// Build the value Word. First part is the remainder of this
	// Unquoted segment (may be empty). Then append any trailing parts
	// from the original token unchanged.
	var parts []WordPart
	if rest != "" {
		parts = append(parts, WordPart{Kind: WpUnquoted, Text: rest})
	}
	if len(tok.Parts) > 1 {
		parts = append(parts, tok.Parts[1:]...)
	}
	// Compute Raw by concatenating part texts.
	raw := rest
	for _, p := range tok.Parts[1:] {
		raw += p.Text
	}
	return name, Word{Raw: raw, Parts: parts, Pos: tok.Pos + len(m[0])}, true
}

// tokenToWord converts a TokWord Token to a Word AST node.
func tokenToWord(t Token) Word {
	return Word{Raw: t.Value, Parts: t.Parts, Pos: t.Pos}
}

// --------------------------------------------------------------------
// Compound commands
// --------------------------------------------------------------------

// parseIf parses `if LIST then LIST (elif LIST then LIST)* (else LIST)? fi`.
func (p *parser) parseIf() (Node, error) {
	if err := p.expectKeyword("if"); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()

	cond, err := p.parseList()
	if err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	if err := p.expectKeyword("then"); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	thenBody, err := p.parseList()
	if err != nil {
		return nil, err
	}

	n := &If{Cond: cond, Then: thenBody}

	for p.isKeyword("elif") {
		p.advance()
		p.skipSemiNewlines()
		elifCond, err := p.parseList()
		if err != nil {
			return nil, err
		}
		p.skipSemiNewlines()
		if err := p.expectKeyword("then"); err != nil {
			return nil, err
		}
		p.skipSemiNewlines()
		elifBody, err := p.parseList()
		if err != nil {
			return nil, err
		}
		n.Elifs = append(n.Elifs, ElifClause{Cond: elifCond, Then: elifBody})
	}

	if p.isKeyword("else") {
		p.advance()
		p.skipSemiNewlines()
		elseBody, err := p.parseList()
		if err != nil {
			return nil, err
		}
		n.Else = elseBody
	}

	p.skipSemiNewlines()
	if err := p.expectKeyword("fi"); err != nil {
		return nil, err
	}
	return n, nil
}

// parseFor parses `for NAME (in WORD*)? (; | \n) do LIST done`.
func (p *parser) parseFor() (Node, error) {
	if err := p.expectKeyword("for"); err != nil {
		return nil, err
	}
	t := p.peek()
	if t.Kind != TokWord {
		return nil, p.errorf("expected variable name after 'for'")
	}
	p.advance()
	variable := t.Value

	// Optional `in word*`. A separator (; or \n) between the variable
	// and `in` is permitted by bash but not by our reference; we match
	// Python which skips semicolons/newlines before `in`.
	p.skipSemiNewlines()

	var items []Word
	if p.isKeyword("in") {
		p.advance()
		items = []Word{} // non-nil: explicit `in`
		for p.peek().Kind == TokWord && !p.isCompoundEnd() {
			items = append(items, tokenToWord(p.advance()))
		}
	}

	p.skipSemiNewlines()
	if err := p.expectKeyword("do"); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	body, err := p.parseList()
	if err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	if err := p.expectKeyword("done"); err != nil {
		return nil, err
	}
	return &For{Var: variable, Items: items, Body: body}, nil
}

// parseWhile parses `while|until LIST (; | \n) do LIST done`. until=true
// for until-loops.
func (p *parser) parseWhile(until bool) (Node, error) {
	kw := "while"
	if until {
		kw = "until"
	}
	if err := p.expectKeyword(kw); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	cond, err := p.parseList()
	if err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	if err := p.expectKeyword("do"); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	body, err := p.parseList()
	if err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	if err := p.expectKeyword("done"); err != nil {
		return nil, err
	}
	return &While{Cond: cond, Body: body, Until: until}, nil
}

// parseCase parses `case WORD in (case-clause)* esac`. A case-clause is
// `[(] PATTERN (| PATTERN)* ) [LIST] ;;`. The `;;` is optional before
// `esac` for the final clause.
func (p *parser) parseCase() (Node, error) {
	if err := p.expectKeyword("case"); err != nil {
		return nil, err
	}
	t := p.peek()
	if t.Kind != TokWord {
		return nil, p.errorf("expected word after 'case'")
	}
	p.advance()
	caseWord := tokenToWord(t)

	p.skipSemiNewlines()
	if err := p.expectKeyword("in"); err != nil {
		return nil, err
	}
	p.skipSemiNewlines()

	n := &Case{Word: caseWord}

	for !p.isKeyword("esac") && !p.atEnd() {
		// Optional leading `(`.
		if p.peek().Kind == TokLParen {
			p.advance()
		}

		var patterns []Word
		first := p.peek()
		if first.Kind != TokWord {
			return nil, p.errorf("expected pattern in case clause")
		}
		p.advance()
		patterns = append(patterns, tokenToWord(first))
		for p.peek().Kind == TokPipe {
			p.advance()
			nt := p.peek()
			if nt.Kind != TokWord {
				return nil, p.errorf("expected pattern after '|' in case clause")
			}
			p.advance()
			patterns = append(patterns, tokenToWord(nt))
		}
		if p.peek().Kind != TokRParen {
			return nil, p.errorf("expected ')' in case clause")
		}
		p.advance()
		p.skipSemiNewlines()

		var body Node
		// If the clause is empty (`;;` or `esac` immediately), leave
		// Body nil. Otherwise parse a list.
		if p.peek().Kind != TokDoubleSemicolon && !p.isKeyword("esac") {
			b, err := p.parseList()
			if err != nil {
				return nil, err
			}
			body = b
		}

		n.Clauses = append(n.Clauses, CaseClause{Patterns: patterns, Body: body})

		// `;;` is optional before `esac` for the final clause.
		if p.peek().Kind == TokDoubleSemicolon {
			p.advance()
		}
		p.skipSemiNewlines()
	}

	if err := p.expectKeyword("esac"); err != nil {
		return nil, err
	}
	return n, nil
}

// parseSubshell parses `( LIST )`.
func (p *parser) parseSubshell() (Node, error) {
	if p.peek().Kind != TokLParen {
		return nil, p.errorf("expected '('")
	}
	p.advance()
	body, err := p.parseList()
	if err != nil {
		return nil, err
	}
	p.skipSemiNewlines()
	if p.peek().Kind != TokRParen {
		return nil, p.errorf("expected ')'")
	}
	p.advance()
	return &Subshell{Body: body}, nil
}
