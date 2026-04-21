// Package builtin — misc builtins: jq, export, printf, test/[/[[,
// true, false.
//
// Behaviour mirrors src/python/shell.py — the `test` family in
// particular is a naive port of `_eval_test` (supports !-prefix
// negation, unary `-f/-d/-e/-z/-n`, single-arg truthy, and the seven
// binary operators `= != -eq -ne -lt -gt -le -ge`). The Python
// reference does NOT implement short-circuit `-a`/`-o`, so we don't
// either.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"agent-harness/go/shell"
)

// BuiltinTrue always succeeds.
func BuiltinTrue(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	return shell.ExecResult{}
}

// BuiltinFalse always fails with exit 1.
func BuiltinFalse(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	return shell.ExecResult{ExitCode: 1}
}

// BuiltinTest implements `test` / `[ ]` / `[[ ]]` evaluation.
func BuiltinTest(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if evalTest(env, args) {
		return shell.ExecResult{}
	}
	return shell.ExecResult{ExitCode: 1}
}

// BuiltinBracket matches `[ EXPR ]` — same as test with trailing "]".
func BuiltinBracket(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if len(args) == 0 || args[len(args)-1] != "]" {
		return shell.ExecResult{Stderr: "[: missing ']'\n", ExitCode: 2}
	}
	if evalTest(env, args[:len(args)-1]) {
		return shell.ExecResult{}
	}
	return shell.ExecResult{ExitCode: 1}
}

// BuiltinDoubleBracket matches `[[ EXPR ]]` — Python collapses this
// onto the same evaluator as `[`/`test`, so we do the same.
func BuiltinDoubleBracket(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if len(args) == 0 || args[len(args)-1] != "]]" {
		return shell.ExecResult{Stderr: "[[: missing ']]'\n", ExitCode: 2}
	}
	if evalTest(env, args[:len(args)-1]) {
		return shell.ExecResult{}
	}
	return shell.ExecResult{ExitCode: 1}
}

// evalTest evaluates a test expression. Mirrors Python's _eval_test.
func evalTest(env *ExecEnv, args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "!" {
		return !evalTest(env, args[1:])
	}
	if len(args) == 2 {
		op, operand := args[0], args[1]
		switch op {
		case "-f":
			p := resolvePath(env, operand)
			return env.FS != nil && env.FS.Exists(p) && !env.FS.IsDir(p)
		case "-d":
			p := resolvePath(env, operand)
			return env.FS != nil && env.FS.IsDir(p)
		case "-e":
			p := resolvePath(env, operand)
			return env.FS != nil && (env.FS.Exists(p) || env.FS.IsDir(p))
		case "-z":
			return len(operand) == 0
		case "-n":
			return len(operand) > 0
		}
	}
	if len(args) == 1 {
		return len(args[0]) > 0
	}
	if len(args) == 3 {
		left, op, right := args[0], args[1], args[2]
		switch op {
		case "=":
			return left == right
		case "!=":
			return left != right
		case "-eq":
			return parseIntOr(left, 0) == parseIntOr(right, 0)
		case "-ne":
			return parseIntOr(left, 0) != parseIntOr(right, 0)
		case "-lt":
			return parseIntOr(left, 0) < parseIntOr(right, 0)
		case "-gt":
			return parseIntOr(left, 0) > parseIntOr(right, 0)
		case "-le":
			return parseIntOr(left, 0) <= parseIntOr(right, 0)
		case "-ge":
			return parseIntOr(left, 0) >= parseIntOr(right, 0)
		}
	}
	return false
}

func parseIntOr(s string, fallback int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// exportRegexp matches `NAME=VALUE` with Python's identifier rules.
var exportRegexp = regexp.MustCompile(`^([A-Za-z_]\w*)=(.*)$`)

// BuiltinExport parses NAME=VALUE pairs and writes them into env.Env.
// Python calls _expand_word on VALUE — we don't have access to the full
// expansion state from a builtin (it lives on the Evaluator), so VALUE
// is taken verbatim. The evaluator's argument expansion already ran
// before our handler was invoked, so in practice assignments like
// `export FOO=$BAR` arrive with BAR already substituted.
func BuiltinExport(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	for _, a := range args {
		m := exportRegexp.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		if env.Env != nil {
			env.Env[m[1]] = m[2]
		}
	}
	return shell.ExecResult{}
}

// BuiltinPrintf implements a small printf subset: %s, %d, %f (with
// precision), %%, \n, \t, \\.
func BuiltinPrintf(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if len(args) == 0 {
		return shell.ExecResult{}
	}
	fmtStr := args[0]
	fmtArgs := args[1:]
	argIdx := 0
	var result strings.Builder
	i := 0
	n := len(fmtStr)
	for i < n {
		if fmtStr[i] == '\\' {
			i++
			if i < n {
				switch fmtStr[i] {
				case 'n':
					result.WriteByte('\n')
				case 't':
					result.WriteByte('\t')
				case '\\':
					result.WriteByte('\\')
				default:
					result.WriteByte(fmtStr[i])
				}
				i++
			}
			continue
		}
		if fmtStr[i] == '%' && i+1 < n {
			i++
			if fmtStr[i] == '%' {
				result.WriteByte('%')
				i++
				continue
			}
			spec := ""
			for i < n && (isFlagOrDigit(fmtStr[i])) {
				spec += string(fmtStr[i])
				i++
			}
			if i < n {
				typeChar := fmtStr[i]
				i++
				var arg string
				if argIdx < len(fmtArgs) {
					arg = fmtArgs[argIdx]
				}
				argIdx++
				switch typeChar {
				case 's':
					result.WriteString(arg)
				case 'd':
					v, err := strconv.Atoi(strings.TrimSpace(arg))
					if err != nil {
						// Python: str(int(arg)) or "0" on error. We emit "0".
						result.WriteString("0")
					} else {
						result.WriteString(strconv.Itoa(v))
					}
				case 'f':
					num, err := strconv.ParseFloat(strings.TrimSpace(arg), 64)
					if err != nil {
						num = 0.0
					}
					prec := 6
					if m := precRegexp.FindStringSubmatch(spec); m != nil {
						prec, _ = strconv.Atoi(m[1])
					}
					result.WriteString(strconv.FormatFloat(num, 'f', prec, 64))
				default:
					result.WriteString(arg)
				}
			}
			continue
		}
		result.WriteByte(fmtStr[i])
		i++
	}
	return shell.ExecResult{Stdout: result.String()}
}

var precRegexp = regexp.MustCompile(`\.(\d+)`)

func isFlagOrDigit(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == '-'
}

// BuiltinJq implements a tiny jq subset: `.`, `.field`, `.field.sub`,
// `.[n]`, `.[]`. Non-trivial expressions return an error.
// Flags: -r (raw string output when result is a string).
func BuiltinJq(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	raw := contains(args, "-r")
	pos := stripFlags(args)
	query := "."
	if len(pos) > 0 {
		query = pos[0]
	}
	files := pos[1:]

	text := stdin
	if len(files) > 0 {
		if env.FS == nil {
			return shell.ExecResult{Stderr: "jq: no filesystem\n", ExitCode: 2}
		}
		s, err := env.FS.ReadString(resolvePath(env, files[0]))
		if err != nil {
			return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 2}
		}
		text = s
	}
	var data any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("jq: parse error: %s\n", err.Error()),
			ExitCode: 2,
		}
	}
	result, err := jqQuery(data, query)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("jq: error: %s\n", err.Error()),
			ExitCode: 5,
		}
	}
	// Special case: when the query ends in `[]` and result is a list,
	// Python prints each item on its own line.
	if arr, ok := result.([]any); ok && strings.HasSuffix(query, "[]") {
		var parts []string
		for i, item := range arr {
			if i&0xff == 0 {
				if err := ctx.Err(); err != nil {
					return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
				}
			}
			if raw {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
					continue
				}
			}
			b, _ := jqMarshal(item, false)
			parts = append(parts, string(b))
		}
		return shell.ExecResult{Stdout: strings.Join(parts, "\n") + "\n"}
	}
	if raw {
		if s, ok := result.(string); ok {
			return shell.ExecResult{Stdout: s + "\n"}
		}
	}
	b, _ := jqMarshal(result, true)
	return shell.ExecResult{Stdout: string(b) + "\n"}
}

// jqPartRe matches the query part tokens Python's re.findall uses:
// `\.\w+`, `\[\d+\]`, `\[\]`.
var jqPartRe = regexp.MustCompile(`\.\w+|\[\d+\]|\[\]`)

// jqQuery walks `query` over data, mirroring Python's _jq_query.
func jqQuery(data any, query string) (any, error) {
	if query == "." {
		return data, nil
	}
	parts := jqPartRe.FindAllString(query, -1)
	current := data
	for _, part := range parts {
		switch {
		case part == "[]":
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("Cannot iterate over non-array")
			}
			return arr, nil
		case strings.HasPrefix(part, "["):
			idxStr := part[1 : len(part)-1]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, fmt.Errorf("invalid index: %s", idxStr)
			}
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("Cannot index non-array")
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index out of range: %d", idx)
			}
			current = arr[idx]
		case strings.HasPrefix(part, "."):
			key := part[1:]
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Cannot access field on non-object")
			}
			val, exists := obj[key]
			if !exists {
				return nil, fmt.Errorf("missing key: %s", key)
			}
			current = val
		}
	}
	return current, nil
}

// jqMarshal renders a value as JSON. Python uses json.dumps(indent=2)
// for aggregate values and json.dumps with no indent for iterated items
// (via the [] form). indent controls which style we use.
func jqMarshal(v any, indent bool) ([]byte, error) {
	// Python's json.dumps emits integers without decimals and floats
	// with. encoding/json emits float64 numbers with decimals even
	// when the value is integral. Normalise integral floats back to
	// int64 so the output looks like Python's.
	v = normaliseJSONNumber(v)
	if indent {
		return json.MarshalIndent(v, "", "  ")
	}
	return json.Marshal(v)
}

// normaliseJSONNumber converts integral float64 values to int64 so
// encoding/json prints them without a decimal point (matching Python).
func normaliseJSONNumber(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normaliseJSONNumber(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normaliseJSONNumber(vv)
		}
		return out
	}
	return v
}
