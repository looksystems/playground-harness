// Package builtin — word expansion (M2.5).
//
// This file implements the third pass of the shell interpreter: given a
// parsed Word from the tokenizer/parser, produce the list of final
// argument strings that a command handler receives.
//
// The Go port matches the Python reference at src/python/shell.py
// (`_expand_word`, `_expand_dollar`, `_expand_brace_param`,
// `_command_substitution`) — same operators, same evaluation order, same
// quoting semantics. Where the task spec and the Python reference
// diverge, we match Python; divergences are documented inline.
//
// Divergences from the task description (following Python + the
// virtual-bash reference):
//
//   - No tilde expansion. Python doesn't implement it; the reference
//     doc's "Notable Omissions" lists it explicitly.
//   - No filename globbing on expanded words. Python doesn't do it;
//     globs in expanded words are passed through literally. Glob
//     patterns appear only in `case` clauses, `find -name`, and the
//     prefix/suffix/replace forms of parameter expansion.
//   - No word splitting on IFS. Python doesn't implement it — each Word
//     produces exactly ONE output string. Callers that want splitting
//     (e.g. for-loops, which explicitly split on whitespace) must do so
//     themselves. ExpandWord therefore returns a single-element slice
//     whenever it succeeds; we keep the []string signature for API
//     forward-compatibility and for the rare case that a future feature
//     (e.g. `$@`) legitimately multi-outputs.
//   - No `$@` / `$*` / `$#` / `$0` / positional parameters. Python
//     doesn't implement them either; the reference doc lists them as
//     omissions.
//
// Kept faithful to Python:
//
//   - `$?` reads state.ExitStatus.
//   - `$VAR` / `${VAR}` / all `${VAR op arg}` forms listed in the
//     reference doc.
//   - `$(...)` and `` `...` `` via state.SubCommand.
//   - `$((...))` via EvalArithmetic.
//   - Single-quoted segments are literal.
//   - Double-quoted segments undergo parameter/command/arith expansion
//     (already escape-reduced by the tokenizer).
//   - Unquoted segments undergo the same expansions; their literal text
//     is passed through verbatim.
//   - MAX_EXPANSIONS per call: 1000 (Python).
//   - MAX_VAR_SIZE per variable write: 100_000 (reference doc; Python
//     code itself uses 64K — the doc is authoritative across languages
//     and the task spec asks for 100K).
//   - Command substitution depth cap: 10 (Python).
package builtin

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"agent-harness/go/shell/vfs"
)

// Default safety limits — tuneable via ExpansionState.
const (
	defaultMaxSubDepth   = 10
	defaultMaxVarSize    = 100_000
	defaultMaxIterations = 10_000
	defaultMaxExpansions = 1_000
)

// ExpansionState is the mutable context threaded through a single
// expansion pass. The evaluator constructs it once per exec() call and
// passes it into every ExpandWord call for that top-level command.
//
// It is NOT safe for concurrent use — a single Shell / command stream
// owns its state throughout an exec().
type ExpansionState struct {
	// Vars is the variable map. Reads return "" for missing keys (per
	// POSIX). Writes go through directly — callers that need size
	// capping should use SetVar.
	Vars map[string]string

	// CWD is the shell's current working directory. Currently unused by
	// the expander (no tilde / no glob), but carried here so future
	// features and the evaluator can share the field.
	CWD string

	// ExitStatus is read by $?. The evaluator updates it after each
	// command.
	ExitStatus int

	// FS is the VFS used by future glob-expansion support. Currently
	// nil-safe: the expander doesn't touch it.
	FS vfs.FilesystemDriver

	// SubCommand runs a command string and returns its stdout (trailing
	// newline already stripped by the expander). Injected by the
	// evaluator to avoid the expander→evaluator import cycle. May be
	// nil, in which case $(...) and `...` return the empty string
	// without error — this matches Python when the shell isn't wired up
	// (but in practice Python always wires it).
	SubCommand func(ctx context.Context, cmd string) (string, error)

	// Positional is the $1 $2 ... $N array. Unused by current expansion
	// (positional parameters aren't supported per the reference doc's
	// Notable Omissions); carried here so the evaluator can thread it
	// through in case a future version adds support.
	Positional []string

	// Safety limits. Zero means "use default".
	MaxSubDepth   int
	MaxVarSize    int
	MaxIterations int
	MaxExpansions int

	// curSubDepth tracks the active nesting level of SubCommand calls.
	// Bumped in expandCommandSubst, decremented on return.
	curSubDepth int

	// expansionCount is the running tally checked against MaxExpansions.
	// The counter resets only when the caller (evaluator) explicitly
	// sets it to 0 — Python resets it on every exec().
	expansionCount int
}

// maxSubDepth returns the effective sub-depth cap.
func (s *ExpansionState) maxSubDepth() int {
	if s.MaxSubDepth > 0 {
		return s.MaxSubDepth
	}
	return defaultMaxSubDepth
}

// maxVarSize returns the effective variable size cap.
func (s *ExpansionState) maxVarSize() int {
	if s.MaxVarSize > 0 {
		return s.MaxVarSize
	}
	return defaultMaxVarSize
}

// maxExpansions returns the effective expansion count cap.
func (s *ExpansionState) maxExpansions() int {
	if s.MaxExpansions > 0 {
		return s.MaxExpansions
	}
	return defaultMaxExpansions
}

// trackExpansion bumps the running counter and enforces the cap.
// Matches Python's _track_expansion: first hit OVER the limit errors
// (not at-limit). The state may be nil-safe if the caller constructs
// without bounds — we guard for that.
func (s *ExpansionState) trackExpansion() error {
	s.expansionCount++
	if s.expansionCount > s.maxExpansions() {
		return fmt.Errorf("maximum expansion limit exceeded")
	}
	return nil
}

// SetVar writes a variable, capping the stored value at maxVarSize.
// Matches Python's _eval_assignment which slices the value to
// MAX_VAR_SIZE. Callers that bypass this (direct map writes) are
// responsible for their own capping — we leave direct map access
// available for flexibility.
func (s *ExpansionState) SetVar(name, value string) {
	if cap := s.maxVarSize(); cap > 0 && len(value) > cap {
		value = value[:cap]
	}
	if s.Vars == nil {
		s.Vars = map[string]string{}
	}
	s.Vars[name] = value
}

// getVar is the internal read helper; returns "" for missing keys.
func (s *ExpansionState) getVar(name string) string {
	if s.Vars == nil {
		return ""
	}
	return s.Vars[name]
}

// ExpandWord expands one AST Word into the argument strings it produces.
//
// In the current implementation a Word always expands to a single
// output string (no word splitting, no globbing — matching Python). The
// []string return shape is reserved for future multi-value forms.
//
// The three WordPart kinds are handled as:
//
//   - WpSingle: literal; no expansion of any kind.
//   - WpDouble: param/cmd/arith expansion on embedded `$` / backtick
//     syntax; result appended verbatim.
//   - WpUnquoted: same expansions as WpDouble. Literal text is passed
//     through verbatim (no globbing). A leading `~` is NOT expanded
//     (Python doesn't do tilde).
func ExpandWord(ctx context.Context, state *ExpansionState, word Word) ([]string, error) {
	var sb strings.Builder
	for _, part := range word.Parts {
		switch part.Kind {
		case WpSingle:
			sb.WriteString(part.Text)
		case WpDouble:
			expanded, err := expandDollarRun(ctx, state, part.Text, true)
			if err != nil {
				return nil, err
			}
			sb.WriteString(expanded)
		case WpUnquoted:
			expanded, err := expandDollarRun(ctx, state, part.Text, false)
			if err != nil {
				return nil, err
			}
			sb.WriteString(expanded)
		}
	}
	return []string{sb.String()}, nil
}

// ExpandWords expands a slice of Words in order, concatenating their
// outputs. A zero-length input yields a nil slice.
func ExpandWords(ctx context.Context, state *ExpansionState, words []Word) ([]string, error) {
	if len(words) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		got, err := ExpandWord(ctx, state, w)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// expandDollarRun scans a single chunk of WordPart.Text (already
// post-tokenizer, so escape sequences inside double-quoted segments are
// reduced and single-quoted chunks never reach here) and expands every
// `$`-anchored or backtick-anchored substitution it finds.
//
// The `inDouble` flag currently only documents the caller's intent —
// behaviour is identical for both contexts because Python's
// _expand_word takes the same code path for `$...` / backtick whether
// inside double quotes or outside (single quotes never reach here).
// The flag is kept so future tweaks can special-case double quotes
// without rewiring callers.
func expandDollarRun(ctx context.Context, state *ExpansionState, s string, inDouble bool) (string, error) {
	var b strings.Builder
	i := 0
	n := len(s)
	for i < n {
		c := s[i]
		if c == '$' {
			val, consumed, err := expandDollar(ctx, state, s, i)
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i += consumed
			continue
		}
		if c == '`' {
			val, consumed, err := expandBacktick(ctx, state, s, i)
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			i += consumed
			continue
		}
		b.WriteByte(c)
		i++
	}
	_ = inDouble
	return b.String(), nil
}

// expandDollar dispatches on the character after `$`:
//
//	$((...))  arithmetic
//	$(...)    command substitution
//	${...}    parameter expansion (with operators)
//	$?        exit status
//	$NAME     simple variable
//	$<other>  literal "$" + rewind one char (matches Python: `return "$",1`)
func expandDollar(ctx context.Context, state *ExpansionState, s string, i int) (string, int, error) {
	if err := state.trackExpansion(); err != nil {
		return "", 0, err
	}

	n := len(s)

	// $((...)) — arithmetic
	if i+2 < n && s[i+1] == '(' && s[i+2] == '(' {
		depth := 2
		j := i + 3
		for j < n && depth > 0 {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			j++
		}
		inner := s[i+3 : j-2]
		val, err := EvalArithmetic(inner, state)
		if err != nil {
			return "", 0, err
		}
		return strconv.FormatInt(val, 10), j - i, nil
	}

	// $(...) — command substitution
	if i+1 < n && s[i+1] == '(' {
		depth := 1
		j := i + 2
		for j < n && depth > 0 {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
			j++
		}
		inner := s[i+2 : j-1]
		val, err := runCommandSubst(ctx, state, inner)
		if err != nil {
			return "", 0, err
		}
		return val, j - i, nil
	}

	// ${...} — parameter expansion with operators
	if i+1 < n && s[i+1] == '{' {
		depth := 1
		j := i + 2
		for j < n && depth > 0 {
			switch s[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			j++
		}
		inner := s[i+2 : j-1]
		val, err := expandBraceParam(ctx, state, inner)
		if err != nil {
			return "", 0, err
		}
		return val, j - i, nil
	}

	// $? — exit status. Python reads env["?"] (the evaluator writes
	// that after each command). We read ExitStatus directly for a
	// typed interface, falling back to env["?"] if present so the
	// evaluator can set either.
	if i+1 < n && s[i+1] == '?' {
		if v, ok := state.Vars["?"]; ok && v != "" {
			return v, 2, nil
		}
		return strconv.Itoa(state.ExitStatus), 2, nil
	}

	// $NAME — longest run of [A-Za-z0-9_]
	j := i + 1
	for j < n && isVarChar(s[j]) {
		j++
	}
	if j == i+1 {
		// $<something-else>: emit literal "$" and advance one char
		// (Python returns `"$", 1` here).
		return "$", 1, nil
	}
	name := s[i+1 : j]
	return state.getVar(name), j - i, nil
}

// isVarChar matches Python's `\w` check (alphanumeric + underscore).
func isVarChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}

// expandBacktick handles `` `cmd` `` substitution. Tokenizer has
// already delivered this as a literal `` `...` `` text inside a
// WordPart, so we just find the matching backtick and dispatch.
// Mirrors Python's _expand_backtick.
func expandBacktick(ctx context.Context, state *ExpansionState, s string, i int) (string, int, error) {
	if err := state.trackExpansion(); err != nil {
		return "", 0, err
	}
	j := i + 1
	for j < len(s) && s[j] != '`' {
		j++
	}
	inner := s[i+1 : j]
	val, err := runCommandSubst(ctx, state, inner)
	if err != nil {
		return "", 0, err
	}
	// Consume through the closing backtick (if present). Python does
	// `j - i + 1` which always accounts for the closer; if we ran off
	// the end of the string we still advance past it to avoid an
	// infinite loop.
	return val, j - i + 1, nil
}

// runCommandSubst invokes state.SubCommand with depth tracking. The
// trailing newline of the captured stdout is stripped (POSIX).
//
// Depth cap matches Python's _command_substitution (>= 10 errors).
func runCommandSubst(ctx context.Context, state *ExpansionState, cmd string) (string, error) {
	if state.curSubDepth >= state.maxSubDepth() {
		return "", fmt.Errorf("command substitution recursion depth exceeded")
	}
	if state.SubCommand == nil {
		// Python always has a shell wired up — if the caller passed
		// nil, return empty string rather than erroring so callers
		// testing expansion in isolation don't have to stub one.
		return "", nil
	}
	state.curSubDepth++
	defer func() { state.curSubDepth-- }()

	out, err := state.SubCommand(ctx, cmd)
	if err != nil {
		return "", err
	}
	// Strip exactly one trailing newline (Python: `if out.endswith("\n"):
	// out = out[:-1]` — single \n only, preserving runs).
	if strings.HasSuffix(out, "\n") {
		out = out[:len(out)-1]
	}
	return out, nil
}

// Regex table for _expand_brace_param. Each regex is anchored at both
// ends so longer forms win over shorter. The order in expandBraceParam
// matters: we check the most-specific patterns first, matching Python's
// fall-through cascade.
var (
	// `${var:-default}` — uses `\w+` to match the name (matches
	// Python). The default portion runs to end-of-string.
	bpDefaultRe = regexp.MustCompile(`^(\w+):-(.*)$`)
	// `${var:=default}` — assign-if-missing.
	bpAssignRe = regexp.MustCompile(`^(\w+):=(.*)$`)
	// `${var:offset}` or `${var:offset:length}` — offset/length are
	// signed ints, length is optional and positive (Python only
	// allows positive length in its regex).
	bpSubstringRe = regexp.MustCompile(`^(\w+):(-?\d+)(?::(\d+))?$`)
	// Global replace: `${var//pat/repl}`.
	bpGlobalSubRe = regexp.MustCompile(`^(\w+)//([^/]*)/(.*)$`)
	// First replace: `${var/pat/repl}`.
	bpFirstSubRe = regexp.MustCompile(`^(\w+)/([^/]*)/(.*)$`)
	// Greedy suffix removal: `${var%%pat}`.
	bpGreedySuffixRe = regexp.MustCompile(`^(\w+)%%(.+)$`)
	// Shortest suffix removal: `${var%pat}`.
	bpSuffixRe = regexp.MustCompile(`^(\w+)%(.+)$`)
	// Greedy prefix removal: `${var##pat}`.
	bpGreedyPrefixRe = regexp.MustCompile(`^(\w+)##(.+)$`)
	// Shortest prefix removal: `${var#pat}`.
	bpPrefixRe = regexp.MustCompile(`^(\w+)#(.+)$`)
	// Simple: `${var}`.
	bpSimpleRe = regexp.MustCompile(`^(\w+)$`)
)

// expandBraceParam handles the body of `${...}` — everything between
// the braces. Mirrors Python's _expand_brace_param, regex-for-regex.
func expandBraceParam(ctx context.Context, state *ExpansionState, expr string) (string, error) {
	// `${#var}` — string length (must come first because `#` would
	// otherwise be eaten by the prefix-removal regex).
	if strings.HasPrefix(expr, "#") {
		name := expr[1:]
		return strconv.Itoa(len(state.getVar(name))), nil
	}

	// `${var:offset[:length]}` — substring. Comes before `${var:-...}`
	// to match Python ordering, but note that our bpSubstringRe requires
	// the offset to be purely numeric, so `:-` / `:=` won't accidentally
	// match here.
	if m := bpSubstringRe.FindStringSubmatch(expr); m != nil {
		val := state.getVar(m[1])
		offset, err := strconv.Atoi(m[2])
		if err != nil {
			return "", err
		}
		if offset < 0 {
			offset = len(val) + offset
			if offset < 0 {
				offset = 0
			}
		}
		if offset > len(val) {
			offset = len(val)
		}
		if m[3] == "" {
			return val[offset:], nil
		}
		length, err := strconv.Atoi(m[3])
		if err != nil {
			return "", err
		}
		end := offset + length
		if end > len(val) {
			end = len(val)
		}
		if end < offset {
			end = offset
		}
		return val[offset:end], nil
	}

	// `${var:-default}`
	if m := bpDefaultRe.FindStringSubmatch(expr); m != nil {
		v, ok := state.Vars[m[1]]
		if ok && v != "" {
			return v, nil
		}
		// Expand the default. Python calls _expand_word on the raw
		// string, which means `$VAR` etc. inside the default get
		// expanded. We use expandDollarRun, which performs the same
		// subset on a flat string.
		return expandDollarRun(ctx, state, m[2], false)
	}

	// `${var:=default}` — assign-if-empty.
	if m := bpAssignRe.FindStringSubmatch(expr); m != nil {
		v, ok := state.Vars[m[1]]
		if ok && v != "" {
			return v, nil
		}
		expanded, err := expandDollarRun(ctx, state, m[2], false)
		if err != nil {
			return "", err
		}
		state.SetVar(m[1], expanded)
		return expanded, nil
	}

	// `${var//pat/repl}` — global replace.
	if m := bpGlobalSubRe.FindStringSubmatch(expr); m != nil {
		val := state.getVar(m[1])
		pat := m[2]
		repl := m[3]
		if pat == "" {
			return val, nil
		}
		// Python uses .replace(pat, repl) which is plain substring
		// replacement — NOT glob. We match Python.
		return strings.ReplaceAll(val, pat, repl), nil
	}

	// `${var/pat/repl}` — first replace.
	if m := bpFirstSubRe.FindStringSubmatch(expr); m != nil {
		val := state.getVar(m[1])
		pat := m[2]
		repl := m[3]
		if pat == "" {
			return val, nil
		}
		// Python uses .find(pat) + slice — again plain substring.
		idx := strings.Index(val, pat)
		if idx == -1 {
			return val, nil
		}
		return val[:idx] + repl + val[idx+len(pat):], nil
	}

	// `${var%%pat}` — greedy suffix.
	if m := bpGreedySuffixRe.FindStringSubmatch(expr); m != nil {
		return removePattern(state.getVar(m[1]), m[2], "suffix", true), nil
	}
	// `${var%pat}` — shortest suffix.
	if m := bpSuffixRe.FindStringSubmatch(expr); m != nil {
		return removePattern(state.getVar(m[1]), m[2], "suffix", false), nil
	}
	// `${var##pat}` — greedy prefix.
	if m := bpGreedyPrefixRe.FindStringSubmatch(expr); m != nil {
		return removePattern(state.getVar(m[1]), m[2], "prefix", true), nil
	}
	// `${var#pat}` — shortest prefix.
	if m := bpPrefixRe.FindStringSubmatch(expr); m != nil {
		return removePattern(state.getVar(m[1]), m[2], "prefix", false), nil
	}

	// `${var}` — plain.
	if m := bpSimpleRe.FindStringSubmatch(expr); m != nil {
		return state.getVar(m[1]), nil
	}

	// Python's _expand_brace_param falls through to "" for any
	// unrecognised shape — match that.
	return "", nil
}

// removePattern strips a glob pattern from either end of val. Mirrors
// Python's _remove_pattern verbatim — including the brute-force scan
// over all possible lengths.
func removePattern(val, pattern, side string, greedy bool) string {
	re := globToRegex(pattern)

	if side == "prefix" {
		if greedy {
			// Longest first.
			for i := len(val); i >= 0; i-- {
				if re.MatchString(val[:i]) {
					return val[i:]
				}
			}
		} else {
			// Shortest first.
			for i := 0; i <= len(val); i++ {
				if re.MatchString(val[:i]) {
					return val[i:]
				}
			}
		}
	} else { // suffix
		if greedy {
			// Longest suffix = smallest i.
			for i := 0; i <= len(val); i++ {
				if re.MatchString(val[i:]) {
					return val[:i]
				}
			}
		} else {
			// Shortest suffix = largest i.
			for i := len(val); i >= 0; i-- {
				if re.MatchString(val[i:]) {
					return val[:i]
				}
			}
		}
	}
	return val
}

// globToRegex compiles a shell glob into an anchored regex. Matches
// Python's _glob_to_regex: only `*` and `?` are special. Character
// classes `[...]` are NOT supported (reference doc "Notable
// Omissions"). Any other regex metacharacter is escaped.
func globToRegex(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	// Pattern compiled from glob — the QuoteMeta call guarantees it is
	// always valid Go regex syntax.
	return regexp.MustCompile(b.String())
}
