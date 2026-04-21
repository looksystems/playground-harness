// Package builtin — text-processing builtins.
//
// Ports the Python `_cmd_*` methods for grep, head, tail, wc, sort,
// uniq, cut, tr, sed, tee. Behaviour matches src/python/shell.py; flag
// parsing is deliberately naive (strips any arg starting with "-" and
// checks for the specific flag token) exactly like the Python reference.
package builtin

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"agent-harness/go/shell"
)

// BuiltinGrep searches for a pattern in stdin or in one or more files.
// Flags: -i (case-insensitive), -v (invert), -c (count only),
// -n (line numbers), -l (filenames), -r/-rn (recursive).
func BuiltinGrep(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	caseI := contains(args, "-i")
	countOnly := contains(args, "-c")
	lineNumbers := contains(args, "-n") || contains(args, "-rn")
	invert := contains(args, "-v")
	recursive := contains(args, "-r") || contains(args, "-rn")
	filenames := contains(args, "-l")

	positional := stripFlags(args)
	if len(positional) == 0 {
		return shell.ExecResult{Stderr: "grep: missing pattern\n", ExitCode: 2}
	}
	pattern := positional[0]
	targets := positional[1:]

	flags := ""
	if caseI {
		flags = "(?i)"
	}
	regex, err := regexp.Compile(flags + pattern)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("grep: invalid pattern: %s\n", err.Error()),
			ExitCode: 2,
		}
	}

	// grepText returns (matches, err). When ctx is cancelled mid-scan
	// it returns the matches collected so far alongside the cancel
	// error; the caller turns that into exit 130 so callers can
	// distinguish "nothing matched" from "scan was interrupted".
	grepText := func(text, label string) ([]string, error) {
		var matches []string
		lines := splitLines(text)
		for i, line := range lines {
			// Cancellation check every 256 lines keeps per-line
			// overhead negligible on small inputs while still
			// responding promptly on long ones.
			if i&0xff == 0 {
				if err := ctx.Err(); err != nil {
					return matches, err
				}
			}
			isMatch := regex.MatchString(line)
			if invert {
				isMatch = !isMatch
			}
			if isMatch {
				prefix := ""
				if label != "" {
					prefix = label + ":"
				}
				num := ""
				if lineNumbers {
					num = fmt.Sprintf("%d:", i+1)
				}
				matches = append(matches, prefix+num+line)
			}
		}
		return matches, nil
	}

	var allMatches []string
	var matchedFiles []string

	switch {
	case len(targets) == 0 && stdin != "":
		m, gerr := grepText(stdin, "")
		if gerr != nil {
			return shell.ExecResult{ExitCode: 130, Stderr: gerr.Error() + "\n"}
		}
		allMatches = m
	case recursive && len(targets) > 0:
		if env.FS == nil {
			return shell.ExecResult{Stderr: "grep: no filesystem\n", ExitCode: 2}
		}
		for _, target := range targets {
			resolved := resolvePath(env, target)
			paths, err := env.FS.Find(resolved, "*")
			if err != nil {
				continue
			}
			for _, fp := range paths {
				if err := ctx.Err(); err != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
				}
				text, rerr := env.FS.ReadString(fp)
				if rerr != nil {
					continue
				}
				m, gerr := grepText(text, fp)
				if gerr != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: gerr.Error() + "\n"}
				}
				if len(m) > 0 {
					matchedFiles = append(matchedFiles, fp)
					allMatches = append(allMatches, m...)
				}
			}
		}
	default:
		if env.FS == nil && len(targets) > 0 {
			return shell.ExecResult{Stderr: "grep: no filesystem\n", ExitCode: 2}
		}
		for _, target := range targets {
			abs := resolvePath(env, target)
			if env.FS == nil || !env.FS.Exists(abs) {
				return shell.ExecResult{
					Stderr:   fmt.Sprintf("grep: %s: No such file\n", target),
					ExitCode: 2,
				}
			}
			text, rerr := env.FS.ReadString(abs)
			if rerr != nil {
				return shell.ExecResult{
					Stderr:   fmt.Sprintf("grep: %s: %s\n", target, rerr.Error()),
					ExitCode: 2,
				}
			}
			label := ""
			if len(targets) > 1 {
				label = target
			}
			m, gerr := grepText(text, label)
			if gerr != nil {
				return shell.ExecResult{ExitCode: 130, Stderr: gerr.Error() + "\n"}
			}
			if len(m) > 0 {
				matchedFiles = append(matchedFiles, target)
				allMatches = append(allMatches, m...)
			}
		}
	}

	if filenames {
		exit := 1
		if len(matchedFiles) > 0 {
			exit = 0
		}
		out := ""
		if len(matchedFiles) > 0 {
			out = strings.Join(matchedFiles, "\n") + "\n"
		}
		return shell.ExecResult{Stdout: out, ExitCode: exit}
	}
	if countOnly {
		exit := 1
		if len(allMatches) > 0 {
			exit = 0
		}
		return shell.ExecResult{
			Stdout:   fmt.Sprintf("%d\n", len(allMatches)),
			ExitCode: exit,
		}
	}
	exit := 1
	if len(allMatches) > 0 {
		exit = 0
	}
	out := ""
	if len(allMatches) > 0 {
		out = strings.Join(allMatches, "\n") + "\n"
	}
	return shell.ExecResult{Stdout: out, ExitCode: exit}
}

// BuiltinHead returns the first N lines (default 10) from stdin or the
// first file argument. Supports `-n N` and `-N` forms.
func BuiltinHead(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	n := 10
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n" && i+1 < len(args):
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return shell.ExecResult{Stderr: "head: invalid count\n", ExitCode: 1}
			}
			n = v
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1 && isAllDigits(a[1:]):
			v, _ := strconv.Atoi(a[1:])
			n = v
		default:
			files = append(files, a)
		}
	}

	text := stdin
	if len(files) > 0 {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "head: no filesystem\n", ExitCode: 1}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
		}
		text = s
	}
	lines := splitLines(text)
	if n < 0 {
		n = 0
	}
	if n > len(lines) {
		n = len(lines)
	}
	// Honour cancellation once up front — head is O(1) per line once
	// the slice is cut, but very large inputs can make splitLines
	// itself expensive; a single check at entry is cheap insurance.
	if err := ctx.Err(); err != nil {
		return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
	}
	out := lines[:n]
	if len(out) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(out, "\n") + "\n"}
}

// BuiltinTail returns the last N lines (default 10) from stdin or the
// first file argument.
func BuiltinTail(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	n := 10
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n" && i+1 < len(args):
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return shell.ExecResult{Stderr: "tail: invalid count\n", ExitCode: 1}
			}
			n = v
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1 && isAllDigits(a[1:]):
			v, _ := strconv.Atoi(a[1:])
			n = v
		default:
			files = append(files, a)
		}
	}

	text := stdin
	if len(files) > 0 {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "tail: no filesystem\n", ExitCode: 1}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
		}
		text = s
	}
	lines := splitLines(text)
	if n < 0 {
		n = 0
	}
	if n > len(lines) {
		n = len(lines)
	}
	if err := ctx.Err(); err != nil {
		return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
	}
	out := lines[len(lines)-n:]
	if len(out) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(out, "\n") + "\n"}
}

// BuiltinWc counts lines, words, or characters. Default: "  LC  WC  CC  FILE\n".
func BuiltinWc(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	linesOnly := contains(args, "-l")
	wordsOnly := contains(args, "-w")
	charsOnly := contains(args, "-c")
	files := stripFlags(args)

	var text string
	if len(files) == 0 {
		text = stdin
	} else {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "wc: no filesystem\n", ExitCode: 1}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
		}
		text = s
	}
	if err := ctx.Err(); err != nil {
		return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
	}
	lc := strings.Count(text, "\n")
	wc := len(strings.Fields(text))
	cc := utf8.RuneCountInString(text)
	// Python uses len(text) — byte length for ASCII, but str length
	// for unicode (code points, not bytes). Python's `len(text)` on a
	// string counts characters (code points). Match that via RuneCount.
	// (Python len on str is O(1) and counts code points.)

	if linesOnly {
		return shell.ExecResult{Stdout: fmt.Sprintf("%d\n", lc)}
	}
	if wordsOnly {
		return shell.ExecResult{Stdout: fmt.Sprintf("%d\n", wc)}
	}
	if charsOnly {
		return shell.ExecResult{Stdout: fmt.Sprintf("%d\n", cc)}
	}
	label := ""
	if len(files) > 0 {
		label = " " + files[0]
	}
	return shell.ExecResult{Stdout: fmt.Sprintf("  %d  %d  %d%s\n", lc, wc, cc, label)}
}

// BuiltinSort sorts lines. Flags: -r (reverse), -n (numeric), -u (unique).
func BuiltinSort(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	reverse := contains(args, "-r")
	numeric := contains(args, "-n")
	unique := contains(args, "-u")
	files := stripFlags(args)

	text := stdin
	if len(files) > 0 {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "sort: no filesystem\n", ExitCode: 1}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
		}
		text = s
	}
	lines := splitLines(text)

	// Cancellation check before the sort — the sort itself is a single
	// O(n log n) call we can't easily interleave, but we can at least
	// bail out if the caller already gave up before we started.
	if err := ctx.Err(); err != nil {
		return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
	}
	if numeric {
		numericRe := regexp.MustCompile(`^-?\d+\.?\d*`)
		keyOf := func(s string) float64 {
			m := numericRe.FindString(s)
			if m == "" {
				return 0
			}
			v, _ := strconv.ParseFloat(m, 64)
			return v
		}
		// Stable sort by numeric key, then reverse if asked.
		sort.SliceStable(lines, func(i, j int) bool {
			a, b := keyOf(lines[i]), keyOf(lines[j])
			if reverse {
				return a > b
			}
			return a < b
		})
	} else {
		if reverse {
			sort.Sort(sort.Reverse(sort.StringSlice(lines)))
		} else {
			sort.Strings(lines)
		}
	}
	if unique {
		seen := map[string]struct{}{}
		out := lines[:0]
		for i, l := range lines {
			if shouldCheckCtx(i) {
				if err := ctx.Err(); err != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
				}
			}
			if _, ok := seen[l]; ok {
				continue
			}
			seen[l] = struct{}{}
			out = append(out, l)
		}
		lines = out
	}
	if len(lines) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(lines, "\n") + "\n"}
}

// BuiltinUniq collapses consecutive identical lines. Flag: -c (prepend
// count). Python doesn't support -d/-u in this minimal port; we match.
func BuiltinUniq(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	count := contains(args, "-c")
	lines := splitLines(stdin)
	var result []string
	var prev *string
	cnt := 0
	flush := func() {
		if prev == nil {
			return
		}
		if count {
			result = append(result, fmt.Sprintf("  %d %s", cnt, *prev))
		} else {
			result = append(result, *prev)
		}
	}
	for i, line := range lines {
		if shouldCheckCtx(i) {
			if err := ctx.Err(); err != nil {
				return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
			}
		}
		l := line
		if prev != nil && *prev == l {
			cnt++
			continue
		}
		flush()
		prev = &lines[i]
		cnt = 1
	}
	flush()
	if len(result) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(result, "\n") + "\n"}
}

// BuiltinCut selects columns. Flags: -d DELIM (default tab), -f LIST
// (comma-separated 1-indexed fields, supporting ranges like 2-4).
func BuiltinCut(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	delimiter := "\t"
	var fields []int
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d" && i+1 < len(args):
			delimiter = args[i+1]
			i++
		case a == "-f" && i+1 < len(args):
			for _, part := range strings.Split(args[i+1], ",") {
				if strings.Contains(part, "-") {
					bits := strings.SplitN(part, "-", 2)
					start := 1
					end := 100
					if bits[0] != "" {
						if v, err := strconv.Atoi(bits[0]); err == nil {
							start = v
						}
					}
					if bits[1] != "" {
						if v, err := strconv.Atoi(bits[1]); err == nil {
							end = v
						}
					}
					for k := start; k <= end; k++ {
						fields = append(fields, k)
					}
				} else {
					if v, err := strconv.Atoi(part); err == nil {
						fields = append(fields, v)
					}
				}
			}
			i++
		}
	}
	lines := splitLines(stdin)
	var out []string
	for i, line := range lines {
		if shouldCheckCtx(i) {
			if err := ctx.Err(); err != nil {
				return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
			}
		}
		parts := strings.Split(line, delimiter)
		var selected []string
		for _, f := range fields {
			if f > 0 && f <= len(parts) {
				selected = append(selected, parts[f-1])
			}
		}
		out = append(out, strings.Join(selected, delimiter))
	}
	if len(out) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(out, "\n") + "\n"}
}

// BuiltinTr translates characters. With -d, deletes characters in SET1.
// Without -d, translates SET1 chars to SET2 (truncated to |SET1|).
func BuiltinTr(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	delete := contains(args, "-d")
	pos := stripFlags(args)
	if delete && len(pos) > 0 {
		chars := map[rune]struct{}{}
		for _, r := range pos[0] {
			chars[r] = struct{}{}
		}
		var b strings.Builder
		count := 0
		for _, r := range stdin {
			if shouldCheckCtx(count) {
				if err := ctx.Err(); err != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
				}
			}
			count++
			if _, skip := chars[r]; skip {
				continue
			}
			b.WriteRune(r)
		}
		return shell.ExecResult{Stdout: b.String()}
	}
	if len(pos) >= 2 {
		set1 := []rune(pos[0])
		set2 := []rune(pos[1])
		// Python's str.maketrans(set1, set2[:len(set1)]) requires
		// set2 to be at least as long as set1 — truncation logic. If
		// set2 is shorter, Python raises ValueError. Match Python
		// behaviour by trimming set1 to len(set2) when set2 is shorter
		// (this matches what Python's set2[:len(set1)] produces when
		// set2 is longer; when shorter, Python errors, but we should
		// degrade gracefully: only translate the overlap).
		n := len(set1)
		if len(set2) < n {
			n = len(set2)
		}
		table := map[rune]rune{}
		for i := 0; i < n; i++ {
			table[set1[i]] = set2[i]
		}
		var b strings.Builder
		count := 0
		for _, r := range stdin {
			if shouldCheckCtx(count) {
				if err := ctx.Err(); err != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
				}
			}
			count++
			if mapped, ok := table[r]; ok {
				b.WriteRune(mapped)
			} else {
				b.WriteRune(r)
			}
		}
		return shell.ExecResult{Stdout: b.String()}
	}
	return shell.ExecResult{Stdout: stdin}
}

// BuiltinSed implements minimal sed: `s/pattern/replacement/[g]` only.
// Flags: -e EXPR selects the expression; otherwise the first positional
// arg is the expression and remaining args are input files.
func BuiltinSed(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	var files []string
	expr := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" && i+1 < len(args):
			expr = args[i+1]
			i++
		case !strings.HasPrefix(a, "-"):
			if expr == "" {
				expr = a
			} else {
				files = append(files, a)
			}
		}
	}
	if expr == "" {
		return shell.ExecResult{Stdout: stdin}
	}
	if err := ctx.Err(); err != nil {
		return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
	}
	text := stdin
	if len(files) > 0 {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "sed: no filesystem\n", ExitCode: 1}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
		}
		text = s
	}
	pat, repl, flagsStr, ok := parseSedExpr(expr)
	if !ok {
		return shell.ExecResult{Stdout: text}
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return shell.ExecResult{Stdout: text}
	}
	// Python's re.sub: `\1` is the back-reference form; Go regexp uses
	// $1. Translate the replacement minimally to preserve parity:
	// - `\&` in sed means the full match; Python `re.sub` treats `\g<0>`
	//   for that. Since Python's _cmd_sed doesn't pre-process the
	//   replacement, it uses `re.sub` semantics where `\1..\9` are
	//   back-refs and `\\` is literal backslash. Go regexp uses `$1`.
	// We translate `\N` → `$N` and `\\` → `$$`.
	goRepl := translateSedRepl(repl)
	if strings.Contains(flagsStr, "g") {
		out := re.ReplaceAllString(text, goRepl)
		return shell.ExecResult{Stdout: out}
	}
	// Count=1: only replace the first match. Go's regexp has no
	// built-in count param — emulate by finding the first match's
	// indices and stitching.
	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return shell.ExecResult{Stdout: text}
	}
	replaced := string(re.ExpandString(nil, goRepl, text, loc))
	out := text[:loc[0]] + replaced + text[loc[1]:]
	return shell.ExecResult{Stdout: out}
}

// parseSedExpr parses a `s<delim>PAT<delim>REPL<delim>FLAGS` expression.
// Matches Python's `s(.)(.*?)\1(.*?)\1(\w*)` which uses the first
// character after `s` as the delimiter and lazily captures PAT and
// REPL up to the next unescaped delimiter. Returns (pattern,
// replacement, flags, ok).
func parseSedExpr(expr string) (string, string, string, bool) {
	if len(expr) < 4 || expr[0] != 's' {
		return "", "", "", false
	}
	delim := expr[1]
	body := expr[2:]
	// Find the first unescaped delim: the pattern part.
	patEnd := -1
	for i := 0; i < len(body); i++ {
		if body[i] == delim {
			patEnd = i
			break
		}
	}
	if patEnd == -1 {
		return "", "", "", false
	}
	pat := body[:patEnd]
	rest := body[patEnd+1:]
	// Find the next unescaped delim: the replacement part.
	replEnd := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == delim {
			replEnd = i
			break
		}
	}
	if replEnd == -1 {
		return "", "", "", false
	}
	repl := rest[:replEnd]
	flags := rest[replEnd+1:]
	// Flags must be \w* per Python — letters, digits, underscore.
	for _, c := range flags {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_') {
			return "", "", "", false
		}
	}
	return pat, repl, flags, true
}

// translateSedRepl converts a Python-style `re.sub` replacement into a
// Go `regexp.Regexp.ExpandString` template: `\N` → `$N`, `\\` → `$$`,
// and bare `$` → `$$` to avoid Go's expansion catching it. Matches the
// subset Python users of our minimal sed are likely to touch.
func translateSedRepl(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			b.WriteString("$$")
			continue
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		// Escape sequence.
		if i+1 >= len(s) {
			b.WriteByte('\\')
			continue
		}
		next := s[i+1]
		switch {
		case next >= '0' && next <= '9':
			b.WriteByte('$')
			b.WriteByte(next)
		case next == '\\':
			b.WriteByte('\\')
		case next == 'n':
			b.WriteByte('\n')
		case next == 't':
			b.WriteByte('\t')
		case next == '&':
			b.WriteString("${0}")
		default:
			b.WriteByte(next)
		}
		i++
	}
	return b.String()
}

// BuiltinTee copies stdin to each listed file and also returns it on
// stdout. With -a, appends to existing files.
func BuiltinTee(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	appendMode := contains(args, "-a")
	files := stripFlags(args)
	if env.FS == nil && len(files) > 0 {
		return shell.ExecResult{Stderr: "tee: no filesystem\n", ExitCode: 1}
	}
	for _, f := range files {
		abs := resolvePath(env, f)
		if appendMode && env.FS.Exists(abs) {
			existing, err := env.FS.ReadString(abs)
			if err != nil {
				return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
			}
			if err := env.FS.WriteString(abs, existing+stdin); err != nil {
				return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
			}
		} else {
			if err := env.FS.WriteString(abs, stdin); err != nil {
				return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
			}
		}
	}
	return shell.ExecResult{Stdout: stdin}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// contains reports whether args contains x.
func contains(args []string, x string) bool {
	for _, a := range args {
		if a == x {
			return true
		}
	}
	return false
}

// stripFlags returns args with any element starting with "-" removed.
// Matches Python's `[a for a in args if not a.startswith("-")]`.
func stripFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// isAllDigits reports whether every rune of s is a decimal digit.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// splitLines mirrors Python's str.splitlines: splits on newlines and
// drops the trailing empty element that strings.Split leaves behind.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ctxCheckStride is the iteration interval at which loop-heavy
// builtins poll ctx.Err. 256 iterations keeps the per-check overhead
// negligible on small inputs while still responding promptly on large
// ones (the evaluator's own MaxIterations cap is 10,000, so at most
// ~40 checks per run).
const ctxCheckStride = 256

// shouldCheckCtx returns true when i is at a cancellation checkpoint.
// Callers combine with ctx.Err() and bail out when the context has been
// cancelled. Using bit-mask avoids the modulus cost on the hot path.
func shouldCheckCtx(i int) bool {
	return i&(ctxCheckStride-1) == 0
}
