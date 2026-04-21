package builtin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseString is the canonical TDD entry point: run Tokenize then Parse
// and return the top-level Node. Failing either stage fails the test.
func parseString(t *testing.T, src string) Node {
	t.Helper()
	tokens, err := Tokenize(src)
	require.NoError(t, err, "tokenize(%q) failed", src)
	node, err := Parse(tokens)
	require.NoError(t, err, "parse(%q) failed; tokens=%+v", src, tokens)
	return node
}

// parseStringErr is the sibling for negative tests.
func parseStringErr(t *testing.T, src string) error {
	t.Helper()
	tokens, err := Tokenize(src)
	require.NoError(t, err, "tokenize(%q) should not have failed", src)
	_, err = Parse(tokens)
	return err
}

// unwrapList returns the single statement inside a List, or the List
// itself if it has 0 or >1 statements. Many top-level assertions use this
// to ignore the guaranteed outer List wrapping.
func unwrapList(n Node) Node {
	if l, ok := n.(*List); ok && len(l.Statements) == 1 {
		return l.Statements[0]
	}
	return n
}

// asSimple asserts that n is a *SimpleCommand and returns it.
func asSimple(t *testing.T, n Node) *SimpleCommand {
	t.Helper()
	n = unwrapList(n)
	sc, ok := n.(*SimpleCommand)
	require.Truef(t, ok, "expected *SimpleCommand, got %T: %+v", n, n)
	return sc
}

// wordValues extracts Raw from a []Word slice.
func wordValues(ws []Word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Raw
	}
	return out
}

// -----------------------------------------------------------------------
// Empty input
// -----------------------------------------------------------------------

func TestParse_Empty(t *testing.T) {
	node := parseString(t, "")
	l, ok := node.(*List)
	require.True(t, ok)
	assert.Empty(t, l.Statements)
}

func TestParse_WhitespaceOnly(t *testing.T) {
	node := parseString(t, "   ")
	l, ok := node.(*List)
	require.True(t, ok)
	assert.Empty(t, l.Statements)
}

func TestParse_SemicolonsOnly(t *testing.T) {
	node := parseString(t, " ; ; ")
	l, ok := node.(*List)
	require.True(t, ok)
	assert.Empty(t, l.Statements)
}

// -----------------------------------------------------------------------
// Simple commands
// -----------------------------------------------------------------------

func TestParse_SingleCommand(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls"))
	assert.Equal(t, []string{"ls"}, wordValues(sc.Words))
	assert.Empty(t, sc.Assignments)
	assert.Empty(t, sc.Redirections)
}

func TestParse_CommandWithArgs(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls -la /tmp"))
	assert.Equal(t, []string{"ls", "-la", "/tmp"}, wordValues(sc.Words))
}

func TestParse_Assignment_Bare(t *testing.T) {
	sc := asSimple(t, parseString(t, "FOO=1"))
	require.Len(t, sc.Assignments, 1)
	assert.Equal(t, "FOO", sc.Assignments[0].Name)
	assert.Equal(t, "1", sc.Assignments[0].Value.Raw)
	assert.Empty(t, sc.Words)
}

func TestParse_Assignment_BeforeCommand(t *testing.T) {
	sc := asSimple(t, parseString(t, "FOO=1 ls -la"))
	require.Len(t, sc.Assignments, 1)
	assert.Equal(t, "FOO", sc.Assignments[0].Name)
	assert.Equal(t, "1", sc.Assignments[0].Value.Raw)
	assert.Equal(t, []string{"ls", "-la"}, wordValues(sc.Words))
}

func TestParse_Assignment_Multiple(t *testing.T) {
	sc := asSimple(t, parseString(t, "FOO=1 BAR=2 ls"))
	require.Len(t, sc.Assignments, 2)
	assert.Equal(t, "FOO", sc.Assignments[0].Name)
	assert.Equal(t, "BAR", sc.Assignments[1].Name)
	assert.Equal(t, []string{"ls"}, wordValues(sc.Words))
}

func TestParse_Assignment_EmptyValue(t *testing.T) {
	sc := asSimple(t, parseString(t, "FOO= ls"))
	require.Len(t, sc.Assignments, 1)
	assert.Equal(t, "FOO", sc.Assignments[0].Name)
	assert.Equal(t, "", sc.Assignments[0].Value.Raw)
	assert.Equal(t, []string{"ls"}, wordValues(sc.Words))
}

func TestParse_Assignment_NotNameLikeStaysArg(t *testing.T) {
	// "1FOO=x" does not match NAME pattern — stays as a word.
	sc := asSimple(t, parseString(t, "1FOO=x ls"))
	assert.Empty(t, sc.Assignments)
	assert.Equal(t, []string{"1FOO=x", "ls"}, wordValues(sc.Words))
}

func TestParse_Assignment_MidCommandStaysArg(t *testing.T) {
	// Only LEADING assignments are extracted.
	sc := asSimple(t, parseString(t, "ls FOO=1"))
	assert.Empty(t, sc.Assignments)
	assert.Equal(t, []string{"ls", "FOO=1"}, wordValues(sc.Words))
}

// -----------------------------------------------------------------------
// Pipelines
// -----------------------------------------------------------------------

func TestParse_Pipeline_Two(t *testing.T) {
	node := unwrapList(parseString(t, "ls | grep foo"))
	p, ok := node.(*Pipeline)
	require.Truef(t, ok, "expected *Pipeline, got %T", node)
	require.Len(t, p.Commands, 2)
	assert.Equal(t, []string{"ls"}, wordValues(asSimple(t, p.Commands[0]).Words))
	assert.Equal(t, []string{"grep", "foo"}, wordValues(asSimple(t, p.Commands[1]).Words))
}

func TestParse_Pipeline_Three(t *testing.T) {
	node := unwrapList(parseString(t, "ls | grep foo | wc -l"))
	p, ok := node.(*Pipeline)
	require.Truef(t, ok, "expected *Pipeline, got %T", node)
	require.Len(t, p.Commands, 3)
}

func TestParse_Pipeline_NewlineAfterPipe(t *testing.T) {
	// `a |\nb` — newline after pipe should be absorbed.
	node := unwrapList(parseString(t, "a |\nb"))
	p, ok := node.(*Pipeline)
	require.True(t, ok)
	require.Len(t, p.Commands, 2)
}

func TestParse_Pipeline_SingleCommandIsUnwrapped(t *testing.T) {
	// No pipe → should NOT wrap in Pipeline.
	node := unwrapList(parseString(t, "ls"))
	_, ok := node.(*Pipeline)
	assert.False(t, ok, "single command should not be wrapped in Pipeline")
}

// -----------------------------------------------------------------------
// AndOr
// -----------------------------------------------------------------------

func TestParse_AndOr_And(t *testing.T) {
	node := unwrapList(parseString(t, "a && b"))
	ao, ok := node.(*AndOr)
	require.Truef(t, ok, "expected *AndOr, got %T", node)
	require.Len(t, ao.Children, 2)
	require.Equal(t, []AndOrOp{OpAnd}, ao.Ops)
}

func TestParse_AndOr_Or(t *testing.T) {
	node := unwrapList(parseString(t, "a || b"))
	ao := node.(*AndOr)
	require.Len(t, ao.Children, 2)
	assert.Equal(t, []AndOrOp{OpOr}, ao.Ops)
}

func TestParse_AndOr_Chain(t *testing.T) {
	node := unwrapList(parseString(t, "a && b || c"))
	ao, ok := node.(*AndOr)
	require.True(t, ok)
	require.Len(t, ao.Children, 3)
	assert.Equal(t, []AndOrOp{OpAnd, OpOr}, ao.Ops)
}

func TestParse_AndOr_NewlineAfterOp(t *testing.T) {
	node := unwrapList(parseString(t, "a &&\nb"))
	ao, ok := node.(*AndOr)
	require.True(t, ok)
	require.Len(t, ao.Children, 2)
}

// -----------------------------------------------------------------------
// Lists
// -----------------------------------------------------------------------

func TestParse_List_Semicolons(t *testing.T) {
	node := parseString(t, "a; b; c")
	l, ok := node.(*List)
	require.True(t, ok)
	require.Len(t, l.Statements, 3)
}

func TestParse_List_Newlines(t *testing.T) {
	node := parseString(t, "a\nb\nc")
	l := node.(*List)
	require.Len(t, l.Statements, 3)
}

func TestParse_List_TrailingSeparator(t *testing.T) {
	node := parseString(t, "a; b;")
	l := node.(*List)
	require.Len(t, l.Statements, 2)
}

func TestParse_List_MixedSeparators(t *testing.T) {
	node := parseString(t, "a\n\nb;c\n")
	l := node.(*List)
	require.Len(t, l.Statements, 3)
}

// -----------------------------------------------------------------------
// Redirections
// -----------------------------------------------------------------------

func TestParse_Redir_Out(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls > out.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirOut, sc.Redirections[0].Kind)
	assert.Equal(t, "out.txt", sc.Redirections[0].Target.Raw)
}

func TestParse_Redir_Append(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls >> out.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirAppend, sc.Redirections[0].Kind)
}

func TestParse_Redir_In(t *testing.T) {
	sc := asSimple(t, parseString(t, "cat < in.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirIn, sc.Redirections[0].Kind)
	assert.Equal(t, "in.txt", sc.Redirections[0].Target.Raw)
}

func TestParse_Redir_Err(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls 2> err.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirErr, sc.Redirections[0].Kind)
	assert.Equal(t, "err.txt", sc.Redirections[0].Target.Raw)
}

func TestParse_Redir_ErrAppend(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls 2>> err.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirErrAppend, sc.Redirections[0].Kind)
}

func TestParse_Redir_ErrOut(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls 2>&1"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirErrOut, sc.Redirections[0].Kind)
}

func TestParse_Redir_Both(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls &> out.txt"))
	require.Len(t, sc.Redirections, 1)
	assert.Equal(t, RedirBoth, sc.Redirections[0].Kind)
	assert.Equal(t, "out.txt", sc.Redirections[0].Target.Raw)
}

func TestParse_Redir_Multiple(t *testing.T) {
	sc := asSimple(t, parseString(t, "ls 2>&1 > out"))
	require.Len(t, sc.Redirections, 2)
	assert.Equal(t, RedirErrOut, sc.Redirections[0].Kind)
	assert.Equal(t, RedirOut, sc.Redirections[1].Kind)
	assert.Equal(t, "out", sc.Redirections[1].Target.Raw)
}

func TestParse_Redir_Interleaved(t *testing.T) {
	// Redirections can appear between args.
	sc := asSimple(t, parseString(t, "ls -la > out -h"))
	assert.Equal(t, []string{"ls", "-la", "-h"}, wordValues(sc.Words))
	require.Len(t, sc.Redirections, 1)
}

func TestParse_Redir_MissingTarget(t *testing.T) {
	for _, src := range []string{"ls >", "ls >>", "ls <", "ls 2>", "ls 2>>", "ls &>"} {
		err := parseStringErr(t, src)
		assert.Error(t, err, "expected error for %q", src)
	}
}

// -----------------------------------------------------------------------
// If
// -----------------------------------------------------------------------

func TestParse_If_Simple(t *testing.T) {
	node := unwrapList(parseString(t, "if true; then echo hi; fi"))
	ifn, ok := node.(*If)
	require.Truef(t, ok, "expected *If, got %T", node)
	require.NotNil(t, ifn.Cond)
	require.NotNil(t, ifn.Then)
	assert.Empty(t, ifn.Elifs)
	assert.Nil(t, ifn.Else)
}

func TestParse_If_Else(t *testing.T) {
	node := unwrapList(parseString(t, "if x; then a; else b; fi"))
	ifn := node.(*If)
	require.NotNil(t, ifn.Else)
}

func TestParse_If_Elif(t *testing.T) {
	node := unwrapList(parseString(t, "if x; then a; elif y; then b; else c; fi"))
	ifn := node.(*If)
	require.Len(t, ifn.Elifs, 1)
	require.NotNil(t, ifn.Else)
}

func TestParse_If_MultipleElifs(t *testing.T) {
	node := unwrapList(parseString(t, "if x; then a; elif y; then b; elif z; then c; fi"))
	ifn := node.(*If)
	require.Len(t, ifn.Elifs, 2)
	assert.Nil(t, ifn.Else)
}

func TestParse_If_EmptyBody(t *testing.T) {
	node := unwrapList(parseString(t, "if true; then fi"))
	ifn, ok := node.(*If)
	require.True(t, ok)
	// Then is a List with no statements.
	l, ok := ifn.Then.(*List)
	require.True(t, ok)
	assert.Empty(t, l.Statements)
}

func TestParse_If_NewlinesInsteadOfSemicolons(t *testing.T) {
	src := "if true\nthen\n  echo hi\nfi"
	node := unwrapList(parseString(t, src))
	_, ok := node.(*If)
	require.True(t, ok)
}

func TestParse_If_MissingFi(t *testing.T) {
	err := parseStringErr(t, "if true; then echo hi")
	assert.Error(t, err)
}

func TestParse_If_MissingThen(t *testing.T) {
	err := parseStringErr(t, "if true; echo hi; fi")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// For
// -----------------------------------------------------------------------

func TestParse_For_Basic(t *testing.T) {
	node := unwrapList(parseString(t, "for i in a b c; do echo $i; done"))
	fn, ok := node.(*For)
	require.Truef(t, ok, "expected *For, got %T", node)
	assert.Equal(t, "i", fn.Var)
	assert.Equal(t, []string{"a", "b", "c"}, wordValues(fn.Items))
	require.NotNil(t, fn.Body)
}

func TestParse_For_EmptyItems(t *testing.T) {
	// `for x in; do ...; done` — `in` with no items.
	node := unwrapList(parseString(t, "for x in; do echo hi; done"))
	fn := node.(*For)
	require.NotNil(t, fn.Items, "Items should be non-nil for explicit 'in'")
	assert.Len(t, fn.Items, 0)
}

func TestParse_For_NoIn(t *testing.T) {
	// `for x; do ...; done` — positional-parameters form.
	node := unwrapList(parseString(t, "for x; do echo hi; done"))
	fn := node.(*For)
	assert.Nil(t, fn.Items, "Items should be nil when no 'in' keyword")
}

func TestParse_For_MissingDone(t *testing.T) {
	err := parseStringErr(t, "for i in a; do echo hi")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// While / Until
// -----------------------------------------------------------------------

func TestParse_While(t *testing.T) {
	node := unwrapList(parseString(t, "while true; do echo hi; done"))
	wn, ok := node.(*While)
	require.Truef(t, ok, "expected *While, got %T", node)
	assert.False(t, wn.Until)
}

func TestParse_Until(t *testing.T) {
	node := unwrapList(parseString(t, "until false; do echo hi; done"))
	wn, ok := node.(*While)
	require.True(t, ok)
	assert.True(t, wn.Until)
}

func TestParse_While_MissingDone(t *testing.T) {
	err := parseStringErr(t, "while true; do echo hi")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// Case
// -----------------------------------------------------------------------

func TestParse_Case_Simple(t *testing.T) {
	src := "case $x in a|b) echo ab;; c) echo c;; *) echo other;; esac"
	node := unwrapList(parseString(t, src))
	cn, ok := node.(*Case)
	require.Truef(t, ok, "expected *Case, got %T", node)
	assert.Equal(t, "$x", cn.Word.Raw)
	require.Len(t, cn.Clauses, 3)
	assert.Equal(t, []string{"a", "b"}, wordValues(cn.Clauses[0].Patterns))
	assert.Equal(t, []string{"c"}, wordValues(cn.Clauses[1].Patterns))
	assert.Equal(t, []string{"*"}, wordValues(cn.Clauses[2].Patterns))
}

func TestParse_Case_LeadingParen(t *testing.T) {
	// POSIX allows `(pattern)` — leading paren is optional.
	src := "case x in (a) echo a;; esac"
	node := unwrapList(parseString(t, src))
	cn := node.(*Case)
	require.Len(t, cn.Clauses, 1)
}

func TestParse_Case_EmptyBody(t *testing.T) {
	src := "case x in a) ;; esac"
	node := unwrapList(parseString(t, src))
	cn := node.(*Case)
	require.Len(t, cn.Clauses, 1)
}

func TestParse_Case_FinalDSemiOptional(t *testing.T) {
	// The final `;;` before `esac` may be omitted. We insert a newline
	// so the echo's arg list terminates before `esac` (without the
	// newline, `esac` would be parsed as an arg to echo — it is only a
	// reserved word in command position).
	src := "case x in a) echo a\nesac"
	node := unwrapList(parseString(t, src))
	cn := node.(*Case)
	require.Len(t, cn.Clauses, 1)
	assert.Equal(t, []string{"a"}, wordValues(cn.Clauses[0].Patterns))
}

func TestParse_Case_MissingEsac(t *testing.T) {
	err := parseStringErr(t, "case x in a) echo a;;")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// Subshell
// -----------------------------------------------------------------------

func TestParse_Subshell(t *testing.T) {
	node := unwrapList(parseString(t, "(a; b)"))
	sh, ok := node.(*Subshell)
	require.Truef(t, ok, "expected *Subshell, got %T", node)
	l, ok := sh.Body.(*List)
	require.True(t, ok)
	require.Len(t, l.Statements, 2)
}

func TestParse_Subshell_Nested(t *testing.T) {
	node := unwrapList(parseString(t, "(a; (b; c))"))
	outer, ok := node.(*Subshell)
	require.True(t, ok)
	innerList, ok := outer.Body.(*List)
	require.True(t, ok)
	// Second statement should itself be a Subshell.
	require.Len(t, innerList.Statements, 2)
	_, ok = innerList.Statements[1].(*Subshell)
	assert.True(t, ok)
}

func TestParse_Subshell_Unbalanced(t *testing.T) {
	err := parseStringErr(t, "(a; b")
	assert.Error(t, err)
}

func TestParse_Subshell_ExtraClose(t *testing.T) {
	err := parseStringErr(t, "a; b)")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// Compound inside pipeline / AndOr
// -----------------------------------------------------------------------

func TestParse_Compound_InPipeline(t *testing.T) {
	node := unwrapList(parseString(t, "if true; then echo hi; fi | cat"))
	p, ok := node.(*Pipeline)
	require.True(t, ok)
	require.Len(t, p.Commands, 2)
	_, ok = p.Commands[0].(*If)
	assert.True(t, ok)
}

func TestParse_Compound_InAndOr(t *testing.T) {
	node := unwrapList(parseString(t, "true && for i in a; do echo $i; done"))
	ao, ok := node.(*AndOr)
	require.True(t, ok)
	require.Len(t, ao.Children, 2)
	_, ok = ao.Children[1].(*For)
	assert.True(t, ok)
}

// -----------------------------------------------------------------------
// Dangling operators
// -----------------------------------------------------------------------

func TestParse_DanglingPipe(t *testing.T) {
	err := parseStringErr(t, "ls |")
	assert.Error(t, err)
}

func TestParse_DanglingAnd(t *testing.T) {
	err := parseStringErr(t, "ls &&")
	assert.Error(t, err)
}

func TestParse_DanglingOr(t *testing.T) {
	err := parseStringErr(t, "ls ||")
	assert.Error(t, err)
}

func TestParse_BareIf(t *testing.T) {
	// Reserved word with no body should error.
	err := parseStringErr(t, "if")
	assert.Error(t, err)
}

// -----------------------------------------------------------------------
// Reserved words as arguments
// -----------------------------------------------------------------------

func TestParse_ReservedWordAsArg(t *testing.T) {
	// `if` is reserved only in command position. After `echo` it's a plain word.
	sc := asSimple(t, parseString(t, "echo if then fi"))
	assert.Equal(t, []string{"echo", "if", "then", "fi"}, wordValues(sc.Words))
}

func TestParse_QuotedReservedWord(t *testing.T) {
	// Quoted "if" should not be a reserved word even in command position.
	sc := asSimple(t, parseString(t, "'if' true"))
	// The first word's Raw is "if" (content) but Parts[0].Kind is Single,
	// so the parser must NOT treat it as the reserved word.
	assert.Equal(t, []string{"if", "true"}, wordValues(sc.Words))
}

// -----------------------------------------------------------------------
// Nested lists
// -----------------------------------------------------------------------

func TestParse_NestedIfInFor(t *testing.T) {
	src := "for i in a b; do if x; then echo $i; fi; done"
	node := unwrapList(parseString(t, src))
	fn, ok := node.(*For)
	require.True(t, ok)
	// Body should be a single If (unwrapped list).
	body := unwrapList(fn.Body)
	_, ok = body.(*If)
	assert.True(t, ok)
}

func TestParse_ListInsidePipelineCommand(t *testing.T) {
	// `(a; b) | cat` — list inside subshell, piped.
	node := unwrapList(parseString(t, "(a; b) | cat"))
	p, ok := node.(*Pipeline)
	require.True(t, ok)
	_, ok = p.Commands[0].(*Subshell)
	assert.True(t, ok)
}

// -----------------------------------------------------------------------
// Leading semicolon
// -----------------------------------------------------------------------

func TestParse_LeadingSemicolon(t *testing.T) {
	// `; a` — leading separator should be silently skipped (matches Python).
	node := parseString(t, "; a")
	l, ok := node.(*List)
	require.True(t, ok)
	require.Len(t, l.Statements, 1)
}
