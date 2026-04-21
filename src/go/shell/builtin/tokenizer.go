// Package builtin implements a pure-Go shell interpreter used by the
// BuiltinShellDriver. It is a port of the Python reference implementation at
// src/python/shell.py.
//
// This file provides the tokenizer: the first pass that turns a raw command
// string into a flat stream of tokens. The parser (next task) consumes the
// tokens and builds an AST.
//
// The shape of tokens intentionally matches the Python reference where
// behaviour is equivalent, with one deliberate refinement: quoted words are
// broken into WordParts so the expansion phase can distinguish single-quoted,
// double-quoted, and unquoted segments of a word without re-parsing. See the
// notes in Tokenize for divergences from the Python tokenizer.
package builtin

import (
	"fmt"
	"strings"
)

// TokenKind enumerates every kind of token the tokenizer can emit, plus the
// reserved-word kinds that the parser may promote TokWord tokens into later.
// (The tokenizer itself never emits reserved-word kinds — it matches the
// Python reference and leaves reserved-word recognition to the parser so
// that context-sensitive promotion is possible.)
type TokenKind int

const (
	// TokWord is any word, quoted or unquoted, including reserved words
	// like "if" / "then" / etc. The parser is responsible for promoting
	// reserved words in command position.
	TokWord TokenKind = iota

	// Operators -----------------------------------------------------------

	TokPipe           // |
	TokAndAnd         // &&
	TokOrOr           // ||
	TokSemicolon      // ;
	TokNewline        // \n
	TokRedirOut       // >
	TokRedirAppend    // >>
	TokRedirIn        // <
	TokRedirErr       // 2>
	TokRedirErrAppend // 2>>
	TokRedirErrOut    // 2>&1
	TokRedirBoth      // &> (also >& — Python treats them identically)
	TokLParen         // (
	TokRParen         // )
	TokLBrace         // {   (reserved for future parser use — not emitted)
	TokRBrace         // }   (reserved for future parser use — not emitted)
	TokEOF

	// Reserved words — NOT emitted by the tokenizer. Declared here so the
	// parser (M2.4) can promote TokWord tokens into these kinds when the
	// word appears in command position. Mirrors Python's KEYWORDS set plus
	// a few extras listed in the task spec.
	TokIf
	TokThen
	TokElse
	TokElif
	TokFi
	TokFor
	TokIn
	TokDo
	TokDone
	TokWhile
	TokUntil
	TokCase
	TokEsac
	TokFunction
	TokDoubleSemicolon // ;;
	TokDoubleLBracket  // [[  (reserved for future parser use)
	TokDoubleRBracket  // ]]  (reserved for future parser use)
)

// String returns a human-readable name for the TokenKind. Used by tests and
// error messages.
func (k TokenKind) String() string {
	switch k {
	case TokWord:
		return "WORD"
	case TokPipe:
		return "PIPE"
	case TokAndAnd:
		return "AND"
	case TokOrOr:
		return "OR"
	case TokSemicolon:
		return "SEMI"
	case TokNewline:
		return "NEWLINE"
	case TokRedirOut:
		return "REDIRECT_OUT"
	case TokRedirAppend:
		return "REDIRECT_APPEND"
	case TokRedirIn:
		return "REDIRECT_IN"
	case TokRedirErr:
		return "REDIRECT_ERR_OUT"
	case TokRedirErrAppend:
		return "REDIRECT_ERR_APPEND"
	case TokRedirErrOut:
		return "REDIRECT_ERR_DUP"
	case TokRedirBoth:
		return "REDIRECT_BOTH_OUT"
	case TokLParen:
		return "LPAREN"
	case TokRParen:
		return "RPAREN"
	case TokLBrace:
		return "LBRACE"
	case TokRBrace:
		return "RBRACE"
	case TokEOF:
		return "EOF"
	case TokIf:
		return "IF"
	case TokThen:
		return "THEN"
	case TokElse:
		return "ELSE"
	case TokElif:
		return "ELIF"
	case TokFi:
		return "FI"
	case TokFor:
		return "FOR"
	case TokIn:
		return "IN"
	case TokDo:
		return "DO"
	case TokDone:
		return "DONE"
	case TokWhile:
		return "WHILE"
	case TokUntil:
		return "UNTIL"
	case TokCase:
		return "CASE"
	case TokEsac:
		return "ESAC"
	case TokFunction:
		return "FUNCTION"
	case TokDoubleSemicolon:
		return "DSEMI"
	case TokDoubleLBracket:
		return "DLBRACKET"
	case TokDoubleRBracket:
		return "DRBRACKET"
	}
	return fmt.Sprintf("Token(%d)", int(k))
}

// WordPartKind distinguishes the three quoting contexts a word segment can
// have. The expansion phase uses this to know which expansions to fire:
//
//   - WpUnquoted: parameter / command / arithmetic expansion fire; globbing
//     and tilde expansion are eligible.
//   - WpSingle: literal content — no expansion of any kind.
//   - WpDouble: parameter / command / arithmetic expansion fire but globbing
//     and word splitting do not.
type WordPartKind int

const (
	// WpUnquoted is a segment outside any quotes. It retains raw text for
	// the expander, including $VAR / ${...} / $(...) / $((...))) / `...`
	// / globs / tildes / and any backslash-escaped characters (the
	// escape is already processed — the backslash is dropped and the
	// next character kept literally).
	WpUnquoted WordPartKind = iota
	// WpSingle is a segment inside single quotes. Content is verbatim;
	// backslashes are literal.
	WpSingle
	// WpDouble is a segment inside double quotes. Content preserves
	// expansions for the expander (they are NOT processed here), but
	// recognised escape sequences (\", \\, \`, \$) have already been
	// reduced to their target character.
	WpDouble
)

// String returns a human-readable name for the WordPartKind.
func (k WordPartKind) String() string {
	switch k {
	case WpUnquoted:
		return "Unquoted"
	case WpSingle:
		return "Single"
	case WpDouble:
		return "Double"
	}
	return fmt.Sprintf("WordPartKind(%d)", int(k))
}

// WordPart is one quoting-context segment of a word.
type WordPart struct {
	Kind WordPartKind
	Text string
}

// Token is a single lexed token.
type Token struct {
	Kind TokenKind
	// Value is the textual value of the token. For TokWord, Value is the
	// concatenation of all WordPart.Text values (i.e. the word as it would
	// appear to the expander if quoting were erased). For operators,
	// Value is the operator text (e.g. "||", "2>&1").
	Value string
	// Parts breaks the word into its quoted/unquoted segments so the
	// expander can do expansion with correct quoting semantics. Nil for
	// non-word tokens.
	Parts []WordPart
	// Pos is the byte offset in the original input where this token begins.
	Pos int
}

// tokenizer holds the cursor state for a single Tokenize call.
type tokenizer struct {
	src []byte
	i   int
	out []Token
}

// Tokenize converts input into a token stream. The final token is always
// TokEOF. Errors are returned for:
//   - unterminated single/double quotes
//   - unterminated $(...) / $((...)) / ${...}
//   - unterminated backticks
//   - a bare `&` (background execution is not supported; see shell.py bug
//     notes in package docs)
func Tokenize(input string) ([]Token, error) {
	t := &tokenizer{src: []byte(input)}
	for t.i < len(t.src) {
		if err := t.step(); err != nil {
			return nil, err
		}
	}
	t.emit(Token{Kind: TokEOF, Pos: len(t.src)})
	return t.out, nil
}

// step consumes one token-or-skip from the input.
func (t *tokenizer) step() error {
	c := t.src[t.i]

	// Whitespace (but not newline — newline is a token).
	if c == ' ' || c == '\t' {
		t.i++
		return nil
	}

	// Backslash at top level: line continuation if next char is \n,
	// otherwise treat as start of a word (and the word branch will
	// handle the escape).
	if c == '\\' && t.i+1 < len(t.src) && t.src[t.i+1] == '\n' {
		t.i += 2
		return nil
	}

	if c == '\n' {
		t.emit(Token{Kind: TokNewline, Value: "\n", Pos: t.i})
		t.i++
		return nil
	}

	// Comment: `#` at top-of-outer-loop context (i.e. not inside a word)
	// consumes through end-of-line. Python matches exactly this: `#` is
	// a word-break character, so a `#` after word content is handled by
	// the word loop breaking, then we re-enter here and strip the
	// comment.
	if c == '#' {
		for t.i < len(t.src) && t.src[t.i] != '\n' {
			t.i++
		}
		return nil
	}

	// Two-character operators and compound redirections --------------------

	if c == ';' && t.peek(1) == ';' {
		t.emit(Token{Kind: TokDoubleSemicolon, Value: ";;", Pos: t.i})
		t.i += 2
		return nil
	}
	if c == ';' {
		t.emit(Token{Kind: TokSemicolon, Value: ";", Pos: t.i})
		t.i++
		return nil
	}

	if c == '|' && t.peek(1) == '|' {
		t.emit(Token{Kind: TokOrOr, Value: "||", Pos: t.i})
		t.i += 2
		return nil
	}
	if c == '|' {
		t.emit(Token{Kind: TokPipe, Value: "|", Pos: t.i})
		t.i++
		return nil
	}

	if c == '&' && t.peek(1) == '&' {
		t.emit(Token{Kind: TokAndAnd, Value: "&&", Pos: t.i})
		t.i += 2
		return nil
	}
	if c == '&' && t.peek(1) == '>' {
		t.emit(Token{Kind: TokRedirBoth, Value: "&>", Pos: t.i})
		t.i += 2
		return nil
	}
	// Bare & — Python has an infinite-loop bug here (falls into word
	// branch which then breaks immediately without consuming). We reject
	// with an explicit error since background execution is not
	// supported.
	if c == '&' {
		return fmt.Errorf("tokenize: background execution (&) not supported at position %d", t.i)
	}

	if c == '>' && t.peek(1) == '>' {
		t.emit(Token{Kind: TokRedirAppend, Value: ">>", Pos: t.i})
		t.i += 2
		return nil
	}
	// `>&` is an alias for `&>` (matches Python REDIRECT_BOTH_OUT and the
	// virtual-bash reference).
	if c == '>' && t.peek(1) == '&' {
		t.emit(Token{Kind: TokRedirBoth, Value: ">&", Pos: t.i})
		t.i += 2
		return nil
	}
	if c == '>' {
		t.emit(Token{Kind: TokRedirOut, Value: ">", Pos: t.i})
		t.i++
		return nil
	}
	if c == '<' {
		t.emit(Token{Kind: TokRedirIn, Value: "<", Pos: t.i})
		t.i++
		return nil
	}

	if c == '(' {
		t.emit(Token{Kind: TokLParen, Value: "(", Pos: t.i})
		t.i++
		return nil
	}
	if c == ')' {
		t.emit(Token{Kind: TokRParen, Value: ")", Pos: t.i})
		t.i++
		return nil
	}

	// 2> / 2>> / 2>&1 — only recognised when the `2` sits at the start of
	// a would-be-word (i.e. preceded by whitespace, start-of-input, or
	// an operator). This matches the Python reference: a bare `2` not in
	// that context is just a digit inside a word.
	if c == '2' && t.peek(1) == '>' && t.redirErrContext() {
		if t.peek(2) == '>' {
			t.emit(Token{Kind: TokRedirErrAppend, Value: "2>>", Pos: t.i})
			t.i += 3
			return nil
		}
		if t.peek(2) == '&' && t.peek(3) == '1' {
			t.emit(Token{Kind: TokRedirErrOut, Value: "2>&1", Pos: t.i})
			t.i += 4
			return nil
		}
		t.emit(Token{Kind: TokRedirErr, Value: "2>", Pos: t.i})
		t.i += 2
		return nil
	}

	// Otherwise: word.
	return t.readWord()
}

// peek returns src[i+offset] or -1 if out of range.
func (t *tokenizer) peek(offset int) int {
	j := t.i + offset
	if j < 0 || j >= len(t.src) {
		return -1
	}
	return int(t.src[j])
}

// redirErrContext reports whether the current `2` is preceded by a character
// that starts a fresh word (so 2>/2>>/2>&1 are redirection tokens). Matches
// Python's `prev in " \t\n|;&()"` check.
func (t *tokenizer) redirErrContext() bool {
	if t.i == 0 {
		return true
	}
	prev := t.src[t.i-1]
	switch prev {
	case ' ', '\t', '\n', '|', ';', '&', '(', ')':
		return true
	}
	return false
}

// emit appends a token to the output stream.
func (t *tokenizer) emit(tok Token) {
	t.out = append(t.out, tok)
}

// readWord reads a word, accumulating WordParts by quoting context. On
// completion (word-boundary hit) emits a single TokWord if any content was
// accumulated, otherwise emits nothing. Parts with consecutive equal Kind
// are merged.
func (t *tokenizer) readWord() error {
	start := t.i
	var parts []WordPart
	// Current unquoted segment builder — we accumulate unquoted text into
	// a buffer and flush it as a WpUnquoted part whenever we hit a
	// quoted segment or end-of-word.
	var unq strings.Builder

	flushUnq := func() {
		if unq.Len() > 0 {
			parts = append(parts, WordPart{Kind: WpUnquoted, Text: unq.String()})
			unq.Reset()
		}
	}

	for t.i < len(t.src) {
		ch := t.src[t.i]

		switch {
		case ch == '\'':
			flushUnq()
			s, err := t.readSingleQuoted()
			if err != nil {
				return err
			}
			parts = append(parts, WordPart{Kind: WpSingle, Text: s})

		case ch == '"':
			flushUnq()
			s, err := t.readDoubleQuoted()
			if err != nil {
				return err
			}
			parts = append(parts, WordPart{Kind: WpDouble, Text: s})

		case ch == '$' && t.peek(1) == '(' && t.peek(2) == '(':
			// Arithmetic expansion $((...)) — atomic.
			s, err := t.readArithmetic()
			if err != nil {
				return err
			}
			unq.WriteString(s)

		case ch == '$' && t.peek(1) == '(':
			// Command substitution $(...) — atomic.
			s, err := t.readCommandSubst()
			if err != nil {
				return err
			}
			unq.WriteString(s)

		case ch == '$' && t.peek(1) == '{':
			// Parameter expansion ${...} — atomic.
			s, err := t.readBraceParam()
			if err != nil {
				return err
			}
			unq.WriteString(s)

		case ch == '`':
			// Backtick command substitution — atomic.
			s, err := t.readBacktick()
			if err != nil {
				return err
			}
			unq.WriteString(s)

		case ch == '\\':
			// Escape next character (including newline, which acts as
			// line continuation). If `\` is the last char of input,
			// consume it and stop.
			if t.i+1 >= len(t.src) {
				t.i++
				break
			}
			next := t.src[t.i+1]
			if next == '\n' {
				// Line continuation: drop both chars.
				t.i += 2
				continue
			}
			unq.WriteByte(next)
			t.i += 2

		case isWordBreak(ch):
			// Word boundary.
			flushUnq()
			if len(parts) > 0 {
				t.emit(buildWordToken(parts, start))
			}
			return nil

		default:
			unq.WriteByte(ch)
			t.i++
		}
	}

	// End of input reached while building a word.
	flushUnq()
	if len(parts) > 0 {
		t.emit(buildWordToken(parts, start))
	}
	return nil
}

// buildWordToken produces a TokWord token from accumulated WordParts,
// merging adjacent parts with the same Kind and computing the aggregate
// Value.
func buildWordToken(parts []WordPart, pos int) Token {
	// Merge adjacent parts of identical kind to keep the stream tidy.
	merged := parts[:0]
	for _, p := range parts {
		if len(merged) > 0 && merged[len(merged)-1].Kind == p.Kind {
			merged[len(merged)-1].Text += p.Text
			continue
		}
		merged = append(merged, p)
	}
	var value strings.Builder
	for _, p := range merged {
		value.WriteString(p.Text)
	}
	return Token{Kind: TokWord, Value: value.String(), Parts: merged, Pos: pos}
}

// isWordBreak reports whether ch terminates an unquoted word segment.
// Matches Python's break list: space, tab, newline, and the operator
// lead characters. Note that `{` and `}` are NOT breaks (matching Python);
// neither are `[` and `]`.
func isWordBreak(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '|', ';', '&', '>', '<', '(', ')', '#':
		return true
	}
	return false
}

// readSingleQuoted reads from the opening `'` through the closing `'`,
// returning the content between them. Content is verbatim (no escapes).
func (t *tokenizer) readSingleQuoted() (string, error) {
	start := t.i
	t.i++ // skip opening '
	var b strings.Builder
	for t.i < len(t.src) && t.src[t.i] != '\'' {
		b.WriteByte(t.src[t.i])
		t.i++
	}
	if t.i >= len(t.src) {
		return "", fmt.Errorf("tokenize: unterminated single quote opened at position %d", start)
	}
	t.i++ // skip closing '
	return b.String(), nil
}

// readDoubleQuoted reads from the opening `"` through the closing `"`,
// returning the content with recognised escape sequences (\", \\, \`, \$)
// reduced. Other backslashes are preserved literally (matching bash). The
// content is NOT expanded — that happens later.
func (t *tokenizer) readDoubleQuoted() (string, error) {
	start := t.i
	t.i++ // skip opening "
	var b strings.Builder
	for t.i < len(t.src) && t.src[t.i] != '"' {
		ch := t.src[t.i]
		if ch == '\\' && t.i+1 < len(t.src) {
			next := t.src[t.i+1]
			switch next {
			case '"', '\\', '`', '$', '\n':
				// Recognised escape: emit target char (or drop,
				// for \<newline> which is a line continuation).
				if next != '\n' {
					b.WriteByte(next)
				}
				t.i += 2
				continue
			}
			// Unrecognised escape: preserve backslash + next char.
			b.WriteByte('\\')
			b.WriteByte(next)
			t.i += 2
			continue
		}
		b.WriteByte(ch)
		t.i++
	}
	if t.i >= len(t.src) {
		return "", fmt.Errorf("tokenize: unterminated double quote opened at position %d", start)
	}
	t.i++ // skip closing "
	return b.String(), nil
}

// readCommandSubst reads a `$(...)` substitution as an atomic chunk,
// preserving the outer `$(` and `)` in the returned text. Nested parens
// inside quotes are handled with the same quote-aware scanning as the
// Python reference.
func (t *tokenizer) readCommandSubst() (string, error) {
	start := t.i
	// Consume $(
	var b strings.Builder
	b.WriteByte('$')
	b.WriteByte('(')
	t.i += 2
	depth := 1
	for t.i < len(t.src) && depth > 0 {
		ch := t.src[t.i]
		switch {
		case ch == '(':
			depth++
			b.WriteByte(ch)
			t.i++
		case ch == ')':
			depth--
			if depth == 0 {
				b.WriteByte(ch)
				t.i++
				return b.String(), nil
			}
			b.WriteByte(ch)
			t.i++
		case ch == '\'':
			// Copy single-quoted content verbatim.
			b.WriteByte(ch)
			t.i++
			for t.i < len(t.src) && t.src[t.i] != '\'' {
				b.WriteByte(t.src[t.i])
				t.i++
			}
			if t.i >= len(t.src) {
				return "", fmt.Errorf("tokenize: unterminated single quote inside $(...) starting at position %d", start)
			}
			b.WriteByte(t.src[t.i]) // closing '
			t.i++
		case ch == '"':
			b.WriteByte(ch)
			t.i++
			for t.i < len(t.src) && t.src[t.i] != '"' {
				if t.src[t.i] == '\\' && t.i+1 < len(t.src) {
					b.WriteByte(t.src[t.i])
					b.WriteByte(t.src[t.i+1])
					t.i += 2
					continue
				}
				b.WriteByte(t.src[t.i])
				t.i++
			}
			if t.i >= len(t.src) {
				return "", fmt.Errorf("tokenize: unterminated double quote inside $(...) starting at position %d", start)
			}
			b.WriteByte(t.src[t.i]) // closing "
			t.i++
		case ch == '`':
			// Nested backticks — scan atomically.
			b.WriteByte(ch)
			t.i++
			for t.i < len(t.src) && t.src[t.i] != '`' {
				b.WriteByte(t.src[t.i])
				t.i++
			}
			if t.i >= len(t.src) {
				return "", fmt.Errorf("tokenize: unterminated backtick inside $(...) starting at position %d", start)
			}
			b.WriteByte(t.src[t.i])
			t.i++
		default:
			b.WriteByte(ch)
			t.i++
		}
	}
	return "", fmt.Errorf("tokenize: unterminated $(...) starting at position %d", start)
}

// readArithmetic reads a `$((expr))` arithmetic expansion as an atomic
// chunk. Tracks paren depth so the correct `))` is the terminator.
func (t *tokenizer) readArithmetic() (string, error) {
	start := t.i
	var b strings.Builder
	b.WriteString("$((")
	t.i += 3
	depth := 1 // depth of OPEN parens AFTER the initial $((
	for t.i < len(t.src) {
		ch := t.src[t.i]
		if ch == '(' {
			depth++
			b.WriteByte(ch)
			t.i++
			continue
		}
		if ch == ')' {
			// Two cases: regular `)` (depth > 1) or the terminator
			// `))` (depth == 1).
			if depth == 1 {
				if t.i+1 < len(t.src) && t.src[t.i+1] == ')' {
					b.WriteString("))")
					t.i += 2
					return b.String(), nil
				}
				// Single `)` at depth 1 without a match — fall
				// through and treat as a parse error from the
				// arithmetic evaluator's perspective. For the
				// tokenizer we simply consume and keep going,
				// bringing depth to 0 which will cause the
				// outer loop to detect an unterminated state.
				depth--
				b.WriteByte(ch)
				t.i++
				continue
			}
			depth--
			b.WriteByte(ch)
			t.i++
			continue
		}
		b.WriteByte(ch)
		t.i++
	}
	return "", fmt.Errorf("tokenize: unterminated $((...)) starting at position %d", start)
}

// readBraceParam reads a `${...}` parameter expansion as an atomic chunk.
// Tracks brace depth (Python only tracks depth 1, but bash allows nested
// `${...${...}...}` for indirect expansion — we match Python: nested
// braces increase depth so we terminate on the matching close).
func (t *tokenizer) readBraceParam() (string, error) {
	start := t.i
	var b strings.Builder
	b.WriteString("${")
	t.i += 2
	depth := 1
	for t.i < len(t.src) {
		ch := t.src[t.i]
		if ch == '{' {
			depth++
			b.WriteByte(ch)
			t.i++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				b.WriteByte(ch)
				t.i++
				return b.String(), nil
			}
			b.WriteByte(ch)
			t.i++
			continue
		}
		b.WriteByte(ch)
		t.i++
	}
	return "", fmt.Errorf("tokenize: unterminated ${...} starting at position %d", start)
}

// readBacktick reads a `` `cmd` `` backtick substitution as an atomic chunk,
// including the surrounding backticks in the returned text.
func (t *tokenizer) readBacktick() (string, error) {
	start := t.i
	var b strings.Builder
	b.WriteByte('`')
	t.i++
	for t.i < len(t.src) && t.src[t.i] != '`' {
		b.WriteByte(t.src[t.i])
		t.i++
	}
	if t.i >= len(t.src) {
		return "", fmt.Errorf("tokenize: unterminated backtick starting at position %d", start)
	}
	b.WriteByte('`')
	t.i++
	return b.String(), nil
}
