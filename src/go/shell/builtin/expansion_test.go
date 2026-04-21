package builtin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"agent-harness/go/shell/vfs"
)

// newExpState builds a fresh ExpansionState seeded with the given vars.
// Optional subCommand stubs out $(cmd) behaviour.
func newExpState(vars map[string]string, sub func(context.Context, string) (string, error)) *ExpansionState {
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	return &ExpansionState{Vars: cp, SubCommand: sub}
}

// singleWord constructs a Word with a single Unquoted part.
func singleWord(s string) Word {
	return Word{Parts: []WordPart{{Kind: WpUnquoted, Text: s}}}
}

// word constructs a Word from mixed parts.
func word(parts ...WordPart) Word {
	return Word{Parts: parts}
}

// mustExpand calls ExpandWord with a fresh background context and fails
// fast on error. Returns the single expanded string.
func mustExpand(t *testing.T, state *ExpansionState, w Word) string {
	t.Helper()
	got, err := ExpandWord(context.Background(), state, w)
	require.NoError(t, err)
	require.Len(t, got, 1)
	return got[0]
}

// -----------------------------------------------------------------------------
// Simple parameter expansion
// -----------------------------------------------------------------------------

func TestExpand_UnquotedLiteral(t *testing.T) {
	s := newExpState(nil, nil)
	require.Equal(t, "hello", mustExpand(t, s, singleWord("hello")))
}

func TestExpand_SingleQuotedLiteral(t *testing.T) {
	// Single-quoted content is verbatim — no `$VAR` expansion.
	s := newExpState(map[string]string{"FOO": "bar"}, nil)
	w := word(WordPart{Kind: WpSingle, Text: "$FOO ${BAR}"})
	require.Equal(t, "$FOO ${BAR}", mustExpand(t, s, w))
}

func TestExpand_SimpleVariable(t *testing.T) {
	s := newExpState(map[string]string{"FOO": "bar"}, nil)
	require.Equal(t, "bar", mustExpand(t, s, singleWord("$FOO")))
	require.Equal(t, "bar", mustExpand(t, s, singleWord("${FOO}")))
}

func TestExpand_UnsetVariable(t *testing.T) {
	s := newExpState(nil, nil)
	require.Equal(t, "", mustExpand(t, s, singleWord("$MISSING")))
	require.Equal(t, "", mustExpand(t, s, singleWord("${MISSING}")))
}

func TestExpand_ConcatenatedLiteralAndVar(t *testing.T) {
	s := newExpState(map[string]string{"X": "abc"}, nil)
	require.Equal(t, "pre-abc-post", mustExpand(t, s, singleWord("pre-$X-post")))
	require.Equal(t, "pre-abc-post", mustExpand(t, s, singleWord("pre-${X}-post")))
}

func TestExpand_DoubleQuotedWithVar(t *testing.T) {
	s := newExpState(map[string]string{"NAME": "World"}, nil)
	w := word(
		WordPart{Kind: WpDouble, Text: "Hello, $NAME!"},
	)
	require.Equal(t, "Hello, World!", mustExpand(t, s, w))
}

func TestExpand_MixedQuoting(t *testing.T) {
	s := newExpState(map[string]string{"X": "val"}, nil)
	// ${X}.'$X'."$X" → val.$X.val
	w := word(
		WordPart{Kind: WpUnquoted, Text: "${X}."},
		WordPart{Kind: WpSingle, Text: "$X"},
		WordPart{Kind: WpUnquoted, Text: "."},
		WordPart{Kind: WpDouble, Text: "$X"},
	)
	require.Equal(t, "val.$X.val", mustExpand(t, s, w))
}

func TestExpand_ExitStatus(t *testing.T) {
	// Exit status via state.ExitStatus when "?" isn't in Vars.
	s := newExpState(nil, nil)
	s.ExitStatus = 42
	require.Equal(t, "42", mustExpand(t, s, singleWord("$?")))

	// If Vars["?"] is set, that wins (matches Python which writes env["?"]).
	s.Vars = map[string]string{"?": "99"}
	require.Equal(t, "99", mustExpand(t, s, singleWord("$?")))
}

func TestExpand_DollarThenNonName(t *testing.T) {
	// A stray `$` followed by a non-name char yields a literal `$`.
	s := newExpState(nil, nil)
	require.Equal(t, "$!", mustExpand(t, s, singleWord("$!")))
	require.Equal(t, "$-", mustExpand(t, s, singleWord("$-")))
}

func TestExpand_VariableInMiddleOfWord(t *testing.T) {
	s := newExpState(map[string]string{"X": "M"}, nil)
	// Braces disambiguate the name.
	require.Equal(t, "AMB", mustExpand(t, s, singleWord("A${X}B")))
	// Without braces, `$XB` tries to read var `XB` which is unset → empty.
	require.Equal(t, "A", mustExpand(t, s, singleWord("A$XB")))
}

// -----------------------------------------------------------------------------
// Parameter expansion operators
// -----------------------------------------------------------------------------

func TestExpand_DefaultUnset(t *testing.T) {
	s := newExpState(nil, nil)
	require.Equal(t, "fallback", mustExpand(t, s, singleWord("${X:-fallback}")))
}

func TestExpand_DefaultEmpty(t *testing.T) {
	s := newExpState(map[string]string{"X": ""}, nil)
	require.Equal(t, "fallback", mustExpand(t, s, singleWord("${X:-fallback}")))
}

func TestExpand_DefaultSet(t *testing.T) {
	s := newExpState(map[string]string{"X": "val"}, nil)
	require.Equal(t, "val", mustExpand(t, s, singleWord("${X:-fallback}")))
}

func TestExpand_DefaultExpandsContent(t *testing.T) {
	s := newExpState(map[string]string{"Y": "from-y"}, nil)
	// Default is expanded: `$Y` inside resolves.
	require.Equal(t, "from-y", mustExpand(t, s, singleWord("${X:-$Y}")))
}

func TestExpand_AssignAssigns(t *testing.T) {
	s := newExpState(nil, nil)
	got := mustExpand(t, s, singleWord("${X:=default}"))
	require.Equal(t, "default", got)
	require.Equal(t, "default", s.Vars["X"])
}

func TestExpand_AssignSkipsWhenSet(t *testing.T) {
	s := newExpState(map[string]string{"X": "original"}, nil)
	got := mustExpand(t, s, singleWord("${X:=overwritten}"))
	require.Equal(t, "original", got)
	require.Equal(t, "original", s.Vars["X"])
}

func TestExpand_Length(t *testing.T) {
	s := newExpState(map[string]string{"X": "hello"}, nil)
	require.Equal(t, "5", mustExpand(t, s, singleWord("${#X}")))

	s2 := newExpState(nil, nil)
	require.Equal(t, "0", mustExpand(t, s2, singleWord("${#MISSING}")))
}

func TestExpand_SubstringOffset(t *testing.T) {
	s := newExpState(map[string]string{"X": "abcdefg"}, nil)
	require.Equal(t, "cdefg", mustExpand(t, s, singleWord("${X:2}")))
	require.Equal(t, "", mustExpand(t, s, singleWord("${X:99}")))
}

func TestExpand_SubstringOffsetLength(t *testing.T) {
	s := newExpState(map[string]string{"X": "abcdefg"}, nil)
	require.Equal(t, "cd", mustExpand(t, s, singleWord("${X:2:2}")))
	// Length past end clamps to remaining.
	require.Equal(t, "fg", mustExpand(t, s, singleWord("${X:5:99}")))
}

func TestExpand_SubstringNegativeOffset(t *testing.T) {
	// `${X:-3}` is the default-substitution form, NOT a negative offset.
	// To express negative offset you'd need `${X: -3}` with a space,
	// but our regex requires no space. Python's regex matches `-\d+`
	// directly (with no space). Verify that.
	s := newExpState(map[string]string{"X": "abcdefg"}, nil)
	// Python's regex: `^(\w+):(-?\d+)(?::(\d+))?$` — yes, `-3` matches
	// as "offset=-3", so `${X:-3}` is ambiguous. Python's cascade
	// checks the substring regex BEFORE the default regex, so this
	// resolves to the substring form.
	got := mustExpand(t, s, singleWord("${X:-3}"))
	// offset = -3, so "efg".
	require.Equal(t, "efg", got)
}

func TestExpand_ReplaceFirst(t *testing.T) {
	s := newExpState(map[string]string{"X": "foo bar foo"}, nil)
	require.Equal(t, "baz bar foo", mustExpand(t, s, singleWord("${X/foo/baz}")))
}

func TestExpand_ReplaceAll(t *testing.T) {
	s := newExpState(map[string]string{"X": "foo bar foo"}, nil)
	require.Equal(t, "baz bar baz", mustExpand(t, s, singleWord("${X//foo/baz}")))
}

func TestExpand_ReplaceEmptyPattern(t *testing.T) {
	s := newExpState(map[string]string{"X": "abc"}, nil)
	// Empty pattern leaves value untouched.
	require.Equal(t, "abc", mustExpand(t, s, singleWord("${X//}")))
}

func TestExpand_StripPrefixShortest(t *testing.T) {
	s := newExpState(map[string]string{"X": "aabbcc"}, nil)
	require.Equal(t, "abbcc", mustExpand(t, s, singleWord("${X#a}")))
	// Glob `*` — shortest match is the empty string.
	require.Equal(t, "aabbcc", mustExpand(t, s, singleWord("${X#*}")))
}

func TestExpand_StripPrefixLongest(t *testing.T) {
	s := newExpState(map[string]string{"X": "aabbcc"}, nil)
	require.Equal(t, "cc", mustExpand(t, s, singleWord("${X##*b}")))
}

func TestExpand_StripSuffixShortest(t *testing.T) {
	s := newExpState(map[string]string{"X": "aabbcc"}, nil)
	require.Equal(t, "aabbc", mustExpand(t, s, singleWord("${X%c}")))
}

func TestExpand_StripSuffixLongest(t *testing.T) {
	s := newExpState(map[string]string{"X": "aabbcc"}, nil)
	// `%%a*` removes the longest suffix matching `a*` — that's the
	// whole string since `a*` matches from the first 'a'.
	require.Equal(t, "", mustExpand(t, s, singleWord("${X%%a*}")))
	// Something more discriminating: strip longest `b*` suffix from aabbcc.
	require.Equal(t, "aa", mustExpand(t, s, singleWord("${X%%b*}")))
}

func TestExpand_StripNoMatch(t *testing.T) {
	s := newExpState(map[string]string{"X": "abc"}, nil)
	require.Equal(t, "abc", mustExpand(t, s, singleWord("${X#xyz}")))
	require.Equal(t, "abc", mustExpand(t, s, singleWord("${X%xyz}")))
}

func TestExpand_UnrecognisedBraceForm(t *testing.T) {
	// Python falls through to "" for unrecognised shapes.
	s := newExpState(nil, nil)
	// `${-X}` is not a supported form — Python returns "".
	require.Equal(t, "", mustExpand(t, s, singleWord("${-X}")))
}

// -----------------------------------------------------------------------------
// Command substitution
// -----------------------------------------------------------------------------

func TestExpand_CommandSubst(t *testing.T) {
	var captured string
	sub := func(_ context.Context, cmd string) (string, error) {
		captured = cmd
		return "captured-output\n", nil
	}
	s := newExpState(nil, sub)
	got := mustExpand(t, s, singleWord("$(date)"))
	require.Equal(t, "captured-output", got)
	require.Equal(t, "date", captured)
}

func TestExpand_CommandSubstBacktick(t *testing.T) {
	sub := func(_ context.Context, cmd string) (string, error) {
		return "bt-output\n", nil
	}
	s := newExpState(nil, sub)
	// Backticks appear in an unquoted WordPart as literal backticks.
	require.Equal(t, "bt-output", mustExpand(t, s, singleWord("`echo hi`")))
}

func TestExpand_CommandSubstStripsTrailingNewlineOnlyOnce(t *testing.T) {
	// Python strips exactly one trailing \n; two newlines leave one.
	sub := func(_ context.Context, cmd string) (string, error) {
		return "line\n\n", nil
	}
	s := newExpState(nil, sub)
	require.Equal(t, "line\n", mustExpand(t, s, singleWord("$(cmd)")))
}

func TestExpand_CommandSubstInDoubleQuotes(t *testing.T) {
	sub := func(_ context.Context, cmd string) (string, error) {
		return "three", nil
	}
	s := newExpState(nil, sub)
	w := word(WordPart{Kind: WpDouble, Text: "count: $(wc -l)"})
	require.Equal(t, "count: three", mustExpand(t, s, w))
}

func TestExpand_CommandSubstNil(t *testing.T) {
	// A nil SubCommand returns empty — tests can expand in isolation.
	s := newExpState(nil, nil)
	require.Equal(t, "", mustExpand(t, s, singleWord("$(anything)")))
}

func TestExpand_CommandSubstError(t *testing.T) {
	sub := func(_ context.Context, cmd string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	s := newExpState(nil, sub)
	_, err := ExpandWord(context.Background(), s, singleWord("$(fail)"))
	require.Error(t, err)
}

func TestExpand_CommandSubstDepthCap(t *testing.T) {
	// Recursive substitution: the sub callback itself calls ExpandWord
	// (simulating a shell where $(cmd) runs more substitutions).
	s := &ExpansionState{MaxSubDepth: 3}
	s.SubCommand = func(ctx context.Context, cmd string) (string, error) {
		// Every inner call triggers another $(...) expansion.
		out, err := ExpandWord(ctx, s, singleWord("$(inner)"))
		if err != nil {
			return "", err
		}
		return strings.Join(out, ""), nil
	}
	_, err := ExpandWord(context.Background(), s, singleWord("$(outer)"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "recursion depth exceeded")
}

// -----------------------------------------------------------------------------
// Arithmetic expansion via $((...))
// -----------------------------------------------------------------------------

func TestExpand_ArithmeticSimple(t *testing.T) {
	s := newExpState(nil, nil)
	require.Equal(t, "5", mustExpand(t, s, singleWord("$((2 + 3))")))
	require.Equal(t, "-5", mustExpand(t, s, singleWord("$((2 - 7))")))
}

func TestExpand_ArithmeticWithVars(t *testing.T) {
	s := newExpState(map[string]string{"X": "4"}, nil)
	require.Equal(t, "10", mustExpand(t, s, singleWord("$((X * 2 + 2))")))
	require.Equal(t, "10", mustExpand(t, s, singleWord("$(($X * 2 + 2))")))
}

func TestExpand_ArithmeticNested(t *testing.T) {
	s := newExpState(nil, nil)
	require.Equal(t, "20", mustExpand(t, s, singleWord("$(((2 + 3) * 4))")))
}

func TestExpand_ArithmeticDivByZero(t *testing.T) {
	s := newExpState(nil, nil)
	_, err := ExpandWord(context.Background(), s, singleWord("$((1 / 0))"))
	require.Error(t, err)
}

// -----------------------------------------------------------------------------
// Multiple words
// -----------------------------------------------------------------------------

func TestExpandWords_Multiple(t *testing.T) {
	s := newExpState(map[string]string{"A": "1", "B": "2"}, nil)
	words := []Word{singleWord("$A"), singleWord("$B"), singleWord("three")}
	got, err := ExpandWords(context.Background(), s, words)
	require.NoError(t, err)
	require.Equal(t, []string{"1", "2", "three"}, got)
}

func TestExpandWords_Empty(t *testing.T) {
	s := newExpState(nil, nil)
	got, err := ExpandWords(context.Background(), s, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

// -----------------------------------------------------------------------------
// Variable size cap
// -----------------------------------------------------------------------------

func TestExpand_VarSizeCap(t *testing.T) {
	s := &ExpansionState{
		Vars:       map[string]string{},
		MaxVarSize: 10,
	}
	// SetVar truncates.
	big := strings.Repeat("x", 100)
	s.SetVar("X", big)
	require.Len(t, s.Vars["X"], 10)
	// And `${X:=default}` also caps.
	s2 := &ExpansionState{
		Vars:       map[string]string{},
		MaxVarSize: 5,
	}
	got := mustExpand(t, s2, singleWord("${X:=hello-world}"))
	require.Equal(t, "hello-world", got)    // return value NOT capped (matches Python behaviour)
	require.Equal(t, "hello", s2.Vars["X"]) // stored value is capped
}

// -----------------------------------------------------------------------------
// Expansion count cap
// -----------------------------------------------------------------------------

func TestExpand_ExpansionCountCap(t *testing.T) {
	s := &ExpansionState{
		Vars:          map[string]string{"X": "v"},
		MaxExpansions: 3,
	}
	// Four `$X` in one word → four trackExpansion() calls → 4th errors.
	_, err := ExpandWord(context.Background(), s, singleWord("$X$X$X$X"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum expansion limit exceeded")
}

// -----------------------------------------------------------------------------
// Low-level helpers
// -----------------------------------------------------------------------------

func TestGlobToRegex(t *testing.T) {
	cases := []struct {
		pattern string
		s       string
		match   bool
	}{
		{"*.txt", "hello.txt", true},
		{"*.txt", "hello.md", false},
		{"?oo", "foo", true},
		{"?oo", "fooo", false},
		{"a*b", "acb", true},
		{"a*b", "ab", true},
		{"a*b", "ac", false},
	}
	for _, c := range cases {
		re := globToRegex(c.pattern)
		require.Equal(t, c.match, re.MatchString(c.s), fmt.Sprintf("pattern=%q s=%q", c.pattern, c.s))
	}
}

func TestRemovePattern_Prefix(t *testing.T) {
	require.Equal(t, "bcde", removePattern("abcde", "a", "prefix", false))
	require.Equal(t, "cde", removePattern("abcde", "?b", "prefix", false))
	require.Equal(t, "e", removePattern("abcde", "*d", "prefix", true))
	require.Equal(t, "", removePattern("abcde", "*", "prefix", true))
	// Greedy finds the longest match (including `a` exact and `*` wildcard)
	// — for `abcde` with `*` the longest matching prefix is the full string.
}

func TestRemovePattern_Suffix(t *testing.T) {
	require.Equal(t, "abcd", removePattern("abcde", "e", "suffix", false))
	require.Equal(t, "abc", removePattern("abcde", "?e", "suffix", false))
	require.Equal(t, "a", removePattern("abcde", "b*", "suffix", true))
	require.Equal(t, "", removePattern("abcde", "*", "suffix", true))
}

// -----------------------------------------------------------------------------
// Additional edge cases
// -----------------------------------------------------------------------------

func TestExpand_EmptyWord(t *testing.T) {
	s := newExpState(nil, nil)
	// A Word with no parts expands to an empty string.
	got, err := ExpandWord(context.Background(), s, Word{})
	require.NoError(t, err)
	require.Equal(t, []string{""}, got)
}

func TestExpand_EmptyDoubleQuoted(t *testing.T) {
	s := newExpState(nil, nil)
	w := word(WordPart{Kind: WpDouble, Text: ""})
	require.Equal(t, "", mustExpand(t, s, w))
}

func TestExpand_NestedBraceExpansionNotSupported(t *testing.T) {
	// Indirect expansion `${!VAR}` isn't supported by the reference.
	// Python falls through to "" for unknown shapes; verify.
	s := newExpState(map[string]string{"X": "Y", "Y": "value"}, nil)
	// `${!X}` — Python's _expand_brace_param doesn't recognise `!`
	// as an operator; it tries the regex table, none match, returns "".
	require.Equal(t, "", mustExpand(t, s, singleWord("${!X}")))
}

func TestExpand_DollarAtEnd(t *testing.T) {
	s := newExpState(nil, nil)
	// A lone trailing `$` is a literal dollar (Python treats `$<eof>`
	// via the final branch: j==i+1, returns "$").
	require.Equal(t, "a$", mustExpand(t, s, singleWord("a$")))
}

func TestExpand_DoubleDollar(t *testing.T) {
	// `$$` — first `$` attempts to read a variable name. Since `$` is
	// not a name char, j==i+1 → returns literal `$`. Then outer loop
	// consumes one char and sees the second `$`, same pattern.
	s := newExpState(nil, nil)
	require.Equal(t, "$$", mustExpand(t, s, singleWord("$$")))
}

func TestExpand_MultipleVarsConcatenated(t *testing.T) {
	s := newExpState(map[string]string{"A": "x", "B": "y", "C": "z"}, nil)
	require.Equal(t, "xyz", mustExpand(t, s, singleWord("$A$B$C")))
	require.Equal(t, "xyz", mustExpand(t, s, singleWord("${A}${B}${C}")))
}

func TestExpand_DoubleQuotedNoExpansionInsideSingle(t *testing.T) {
	// Interleaving: double(has $X) + single($X literal).
	s := newExpState(map[string]string{"X": "expanded"}, nil)
	w := word(
		WordPart{Kind: WpDouble, Text: "a=$X"},
		WordPart{Kind: WpSingle, Text: " b=$X"},
	)
	require.Equal(t, "a=expanded b=$X", mustExpand(t, s, w))
}

func TestExpand_CommandSubstNested(t *testing.T) {
	// Simulate nested $(cmd1 $(cmd2)) — the tokenizer already balanced
	// parens and handed us the outer body intact. When the expander
	// asks for $(cmd1 $(cmd2)), the SubCommand receives
	// "cmd1 $(cmd2)". A real evaluator would run that through another
	// expansion pass. Here, we verify the cmd string is passed through
	// verbatim.
	var seen []string
	sub := func(_ context.Context, cmd string) (string, error) {
		seen = append(seen, cmd)
		return "", nil
	}
	s := newExpState(nil, sub)
	_, err := ExpandWord(context.Background(), s, singleWord("$(cmd1 $(cmd2))"))
	require.NoError(t, err)
	require.Equal(t, []string{"cmd1 $(cmd2)"}, seen)
}

func TestExpand_StripGlobQuestionMark(t *testing.T) {
	s := newExpState(map[string]string{"X": "abcdef"}, nil)
	// `?` matches a single char; shortest prefix match of `?` is 1 char.
	require.Equal(t, "bcdef", mustExpand(t, s, singleWord("${X#?}")))
	// `??` matches two chars.
	require.Equal(t, "cdef", mustExpand(t, s, singleWord("${X#??}")))
}

func TestExpand_ReplaceInDoubleQuotes(t *testing.T) {
	s := newExpState(map[string]string{"X": "hello world"}, nil)
	w := word(WordPart{Kind: WpDouble, Text: "${X// /_}"})
	require.Equal(t, "hello_world", mustExpand(t, s, w))
}

func TestExpand_ArithmeticExitStatus(t *testing.T) {
	// $((1 + 1)) in an assignment default — make sure it still works
	// via the full chain.
	s := newExpState(nil, nil)
	require.Equal(t, "v-2", mustExpand(t, s, singleWord("v-$((1 + 1))")))
}

// -----------------------------------------------------------------------------
// FS wiring (not used by expansion, but verify the field accepts a driver)
// -----------------------------------------------------------------------------

func TestExpand_FSFieldAcceptsDriver(t *testing.T) {
	// Smoke test: an FS driver can be attached without affecting
	// expansion (globbing isn't implemented — the driver is there for
	// forward compatibility with M2.7+).
	s := &ExpansionState{
		Vars: map[string]string{"X": "hello"},
		FS:   vfs.NewBuiltinFilesystemDriver(),
	}
	require.Equal(t, "hello", mustExpand(t, s, singleWord("$X")))
}
