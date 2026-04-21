// Package builtin — arithmetic evaluator (M2.5).
//
// This file evaluates bash-style arithmetic expressions used by the
// $((...)) expansion form. It is a direct port of Python's
// `_eval_arithmetic` / `_parse_arith_expr` / `_tokenize_arith`
// (src/python/shell.py, around lines 1009-1191).
//
// Divergence from bash: only the operators that the virtual-bash reference
// declares supported are implemented here. In particular, `**` (power),
// `++` / `--` (increment/decrement), `=` / `+=` / `-=` etc. (assignment
// inside arithmetic) are NOT supported. The task spec mentions them but
// the authoritative reference doc and the Python reference both omit them;
// matching Python keeps cross-language parity. Unknown operators become
// part of an error from parsePrimary (the token is treated as a name and
// resolves to 0, so the expression silently degrades — this matches
// Python, which returns 0 for malformed primaries).
//
// Supported operators (precedence, loose → tight):
//
//	ternary     ? :
//	logical     ||  &&
//	bit         |   ^   &
//	equality    ==  !=
//	relational  <   <=  >   >=
//	shift       <<  >>
//	add         +   -
//	mul         *   /   %
//	unary       +   -   !   ~
//	primary     ( expr )   INT   NAME
//
// Variables referenced in the expression are pre-resolved against
// state.Vars. An unset or non-numeric variable is 0 (Python behaviour).
package builtin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// arithVarRef matches either $NAME or ${NAME} (alphanumeric + _).
// Matches Python: `\$\{?(\w+)\}?` — note that `\w` in Python re includes
// underscore, same as Go.
var arithVarRef = regexp.MustCompile(`\$\{?(\w+)\}?`)

// arithBareName matches a plain identifier (not preceded by $).
// The Python reference runs this as a second sub over the already-$-resolved
// text, so by the time we apply it, no `$NAME` remain.
var arithBareName = regexp.MustCompile(`[A-Za-z_]\w*`)

// EvalArithmetic evaluates a bash-style arithmetic expression against
// state.Vars. Variables are inlined as their numeric value (or 0 if unset
// or non-numeric) before parsing. Returns the final integer value.
//
// Errors:
//   - Division by zero.
//   - Non-empty expression that fails to tokenize into at least one token
//     (e.g. pure whitespace is fine, but "$" alone isn't — it produces an
//     empty token stream after $-substitution, which the Python reference
//     returns 0 for; we match that).
func EvalArithmetic(expr string, state *ExpansionState) (int64, error) {
	// Two-pass variable substitution, matching Python exactly:
	//   1. Replace $NAME / ${NAME} with state.Vars[NAME] (or "0").
	//   2. Replace bare NAME with state.Vars[NAME] (or "0").
	// Note Python uses "0" as the fallback in _eval_arithmetic (unlike
	// _expand_dollar which uses "").
	var vars map[string]string
	if state != nil {
		vars = state.Vars
	}

	replaceVar := func(m string) string {
		// m is the full match "$NAME" or "${NAME}"; extract the name
		// group. regexp doesn't give us subgroups via ReplaceAllString,
		// so we use FindStringSubmatch logic via ReplaceAllStringFunc.
		sub := arithVarRef.FindStringSubmatch(m)
		if len(sub) < 2 {
			return "0"
		}
		name := sub[1]
		if v, ok := vars[name]; ok {
			if v == "" {
				return "0"
			}
			return v
		}
		return "0"
	}
	expanded := arithVarRef.ReplaceAllStringFunc(expr, replaceVar)

	expanded = arithBareName.ReplaceAllStringFunc(expanded, func(m string) string {
		if v, ok := vars[m]; ok {
			if v == "" {
				return "0"
			}
			return v
		}
		return "0"
	})

	tokens := tokenizeArith(strings.TrimSpace(expanded))

	p := &arithParser{tokens: tokens}
	val, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	// Python is permissive — trailing garbage is silently ignored. We
	// match that: no error if tokens remain.
	return val, nil
}

// tokenizeArith splits an arithmetic expression into tokens. Matches
// Python's _tokenize_arith: whitespace is skipped, two-char operators are
// greedy, single-char punctuation is one token each, runs of digits are
// one token. Anything else is skipped (Python's final `i += 1`), which
// means stray characters silently disappear — again, matching Python.
func tokenizeArith(expr string) []string {
	var out []string
	i := 0
	n := len(expr)
	for i < n {
		ch := expr[i]
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}
		// Two-char operators
		if i+1 < n {
			two := expr[i : i+2]
			switch two {
			case "||", "&&", "==", "!=", "<=", ">=", "<<", ">>":
				out = append(out, two)
				i += 2
				continue
			}
		}
		if strings.ContainsRune("+-*/%()^&|<>!~?:", rune(ch)) {
			out = append(out, string(ch))
			i++
			continue
		}
		if unicode.IsDigit(rune(ch)) {
			j := i
			for j < n && unicode.IsDigit(rune(expr[j])) {
				j++
			}
			out = append(out, expr[i:j])
			i = j
			continue
		}
		// Stray character — Python silently advances. We match.
		i++
	}
	return out
}

// arithParser is a recursive-descent parser over arithmetic tokens.
// Precedence climbs via a parse function per level, matching the Python
// reference's structure exactly.
type arithParser struct {
	tokens []string
	pos    int
}

func (p *arithParser) peek() string {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return ""
}

func (p *arithParser) advance() string {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *arithParser) parseExpr() (int64, error) {
	return p.parseTernary()
}

func (p *arithParser) parseTernary() (int64, error) {
	val, err := p.parseOr()
	if err != nil {
		return 0, err
	}
	if p.peek() == "?" {
		p.advance()
		truthy, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.peek() == ":" {
			p.advance()
		}
		falsy, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if val != 0 {
			return truthy, nil
		}
		return falsy, nil
	}
	return val, nil
}

func (p *arithParser) parseOr() (int64, error) {
	val, err := p.parseAnd()
	if err != nil {
		return 0, err
	}
	for p.peek() == "||" {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return 0, err
		}
		if val != 0 || right != 0 {
			val = 1
		} else {
			val = 0
		}
	}
	return val, nil
}

func (p *arithParser) parseAnd() (int64, error) {
	val, err := p.parseBitOr()
	if err != nil {
		return 0, err
	}
	for p.peek() == "&&" {
		p.advance()
		right, err := p.parseBitOr()
		if err != nil {
			return 0, err
		}
		if val != 0 && right != 0 {
			val = 1
		} else {
			val = 0
		}
	}
	return val, nil
}

func (p *arithParser) parseBitOr() (int64, error) {
	val, err := p.parseBitXor()
	if err != nil {
		return 0, err
	}
	for p.peek() == "|" {
		p.advance()
		right, err := p.parseBitXor()
		if err != nil {
			return 0, err
		}
		val |= right
	}
	return val, nil
}

func (p *arithParser) parseBitXor() (int64, error) {
	val, err := p.parseBitAnd()
	if err != nil {
		return 0, err
	}
	for p.peek() == "^" {
		p.advance()
		right, err := p.parseBitAnd()
		if err != nil {
			return 0, err
		}
		val ^= right
	}
	return val, nil
}

func (p *arithParser) parseBitAnd() (int64, error) {
	val, err := p.parseEquality()
	if err != nil {
		return 0, err
	}
	for p.peek() == "&" {
		p.advance()
		right, err := p.parseEquality()
		if err != nil {
			return 0, err
		}
		val &= right
	}
	return val, nil
}

func (p *arithParser) parseEquality() (int64, error) {
	val, err := p.parseRelational()
	if err != nil {
		return 0, err
	}
	for p.peek() == "==" || p.peek() == "!=" {
		op := p.advance()
		right, err := p.parseRelational()
		if err != nil {
			return 0, err
		}
		if op == "==" {
			if val == right {
				val = 1
			} else {
				val = 0
			}
		} else {
			if val != right {
				val = 1
			} else {
				val = 0
			}
		}
	}
	return val, nil
}

func (p *arithParser) parseRelational() (int64, error) {
	val, err := p.parseShift()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "<" && op != ">" && op != "<=" && op != ">=" {
			break
		}
		p.advance()
		right, err := p.parseShift()
		if err != nil {
			return 0, err
		}
		var b bool
		switch op {
		case "<":
			b = val < right
		case ">":
			b = val > right
		case "<=":
			b = val <= right
		case ">=":
			b = val >= right
		}
		if b {
			val = 1
		} else {
			val = 0
		}
	}
	return val, nil
}

func (p *arithParser) parseShift() (int64, error) {
	val, err := p.parseAdd()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "<<" && op != ">>" {
			break
		}
		p.advance()
		right, err := p.parseAdd()
		if err != nil {
			return 0, err
		}
		if right < 0 {
			return 0, fmt.Errorf("negative shift count")
		}
		if op == "<<" {
			val <<= uint64(right)
		} else {
			val >>= uint64(right)
		}
	}
	return val, nil
}

func (p *arithParser) parseAdd() (int64, error) {
	val, err := p.parseMul()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "+" && op != "-" {
			break
		}
		p.advance()
		right, err := p.parseMul()
		if err != nil {
			return 0, err
		}
		if op == "+" {
			val += right
		} else {
			val -= right
		}
	}
	return val, nil
}

func (p *arithParser) parseMul() (int64, error) {
	val, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "*" && op != "/" && op != "%" {
			break
		}
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case "*":
			val *= right
		case "/":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			// Python uses int(val / right) which is trunc-towards-zero.
			// Go's integer / for signed ints is also trunc-towards-zero,
			// so `/` matches without special handling.
			val = val / right
		case "%":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			val = val % right
		}
	}
	return val, nil
}

func (p *arithParser) parseUnary() (int64, error) {
	switch p.peek() {
	case "-":
		p.advance()
		v, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return -v, nil
	case "+":
		p.advance()
		return p.parseUnary()
	case "!":
		p.advance()
		v, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		if v == 0 {
			return 1, nil
		}
		return 0, nil
	case "~":
		p.advance()
		v, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return ^v, nil
	}
	return p.parsePrimary()
}

func (p *arithParser) parsePrimary() (int64, error) {
	if p.peek() == "(" {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.peek() == ")" {
			p.advance()
		}
		return val, nil
	}
	tok := p.advance()
	if tok == "" {
		return 0, nil
	}
	// Python uses plain int(tok), which accepts only decimal. Python's
	// reference does NOT special-case hex/octal (the tokenizer only
	// accumulates `isdigit()` runs and returns no letters, so "0xff"
	// would already be lost at tokenize time). Match that: try decimal
	// parse; on failure return 0.
	v, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		return 0, nil
	}
	return v, nil
}
