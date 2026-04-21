package builtin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wordTok constructs a TokWord Token for assertion convenience.
func wordTok(value string, parts ...WordPart) Token {
	return Token{Kind: TokWord, Value: value, Parts: parts}
}

// unquoted is a helper to build a WpUnquoted WordPart.
func unquoted(s string) WordPart { return WordPart{Kind: WpUnquoted, Text: s} }
func single(s string) WordPart   { return WordPart{Kind: WpSingle, Text: s} }
func double(s string) WordPart   { return WordPart{Kind: WpDouble, Text: s} }

// kinds extracts the TokenKind slice from a token list (Pos is ignored).
func kinds(tokens []Token) []TokenKind {
	out := make([]TokenKind, len(tokens))
	for i, t := range tokens {
		out[i] = t.Kind
	}
	return out
}

// assertSameTokens checks that got matches expected on every field except
// Pos. Useful because expected tokens are written without knowing byte
// offsets.
func assertSameTokens(t *testing.T, got []Token, expected ...Token) {
	t.Helper()
	require.Len(t, got, len(expected), "token count mismatch: got=%v", got)
	for i, e := range expected {
		g := got[i]
		assert.Equal(t, e.Kind, g.Kind, "token %d: kind mismatch (got %s, want %s)", i, g.Kind, e.Kind)
		assert.Equal(t, e.Value, g.Value, "token %d: value mismatch", i)
		assert.Equal(t, e.Parts, g.Parts, "token %d: parts mismatch", i)
	}
}

// -----------------------------------------------------------------------
// Plain words
// -----------------------------------------------------------------------

func TestTokenize_PlainWords(t *testing.T) {
	got, err := Tokenize("ls foo bar")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("ls", unquoted("ls")),
		wordTok("foo", unquoted("foo")),
		wordTok("bar", unquoted("bar")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_Empty(t *testing.T) {
	got, err := Tokenize("")
	require.NoError(t, err)
	assertSameTokens(t, got, Token{Kind: TokEOF})
}

func TestTokenize_WhitespaceOnly(t *testing.T) {
	got, err := Tokenize("   \t  ")
	require.NoError(t, err)
	assertSameTokens(t, got, Token{Kind: TokEOF})
}

func TestTokenize_TrailingWhitespace(t *testing.T) {
	got, err := Tokenize("ls   ")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("ls", unquoted("ls")),
		Token{Kind: TokEOF},
	)
}

// -----------------------------------------------------------------------
// Quoting
// -----------------------------------------------------------------------

func TestTokenize_SingleQuoted(t *testing.T) {
	got, err := Tokenize("'hello world'")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("hello world", single("hello world")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_SingleQuotedNoExpansion(t *testing.T) {
	// $VAR inside single quotes is literal.
	got, err := Tokenize(`'no $expansion here'`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("no $expansion here", single("no $expansion here")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_SingleQuoteSpansNewline(t *testing.T) {
	got, err := Tokenize("'a\nb'")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("a\nb", single("a\nb")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuoted(t *testing.T) {
	// Expansions are preserved as raw text for the expander.
	got, err := Tokenize(`"foo $BAR"`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("foo $BAR", double("foo $BAR")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuotedEscapes(t *testing.T) {
	// \" is reduced to ", \$ to $, \\ to \, \` to `
	got, err := Tokenize(`"hello\"world"`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok(`hello"world`, double(`hello"world`)),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuotedAllEscapes(t *testing.T) {
	got, err := Tokenize(`"a\\b\$c\` + "`" + `d"`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("a\\b$c`d", double("a\\b$c`d")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuotedUnrecognisedEscape(t *testing.T) {
	// \n (not recognised) is preserved as backslash + n.
	got, err := Tokenize(`"hello\nworld"`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok(`hello\nworld`, double(`hello\nworld`)),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuoteSpansNewline(t *testing.T) {
	got, err := Tokenize("\"a\nb\"")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("a\nb", double("a\nb")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleQuoteLineContinuation(t *testing.T) {
	// \<newline> inside double quotes is line continuation — both chars
	// are dropped.
	got, err := Tokenize("\"foo\\\nbar\"")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("foobar", double("foobar")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_MixedQuoting(t *testing.T) {
	// foo"bar"baz → single word with three parts.
	got, err := Tokenize(`foo"bar"baz`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("foobarbaz", unquoted("foo"), double("bar"), unquoted("baz")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_ConcatenatedQuotes(t *testing.T) {
	// 'a'"b"c → three parts of different kinds.
	got, err := Tokenize(`'a'"b"c`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("abc", single("a"), double("b"), unquoted("c")),
		Token{Kind: TokEOF},
	)
}

// -----------------------------------------------------------------------
// Backslash escapes (outside quotes)
// -----------------------------------------------------------------------

func TestTokenize_BackslashEscapeInWord(t *testing.T) {
	// hello\ world → one word "hello world"
	got, err := Tokenize(`hello\ world`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("hello world", unquoted("hello world")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_BackslashLineContinuation(t *testing.T) {
	// foo\<newline>bar → single word "foobar" (newline dropped).
	got, err := Tokenize("foo\\\nbar")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("foobar", unquoted("foobar")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_BackslashLineContinuationAtTopLevel(t *testing.T) {
	// \<newline> at top level (between commands) is consumed entirely.
	got, err := Tokenize("ls \\\nfoo")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("ls", unquoted("ls")),
		wordTok("foo", unquoted("foo")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_BackslashEscapeMetaChar(t *testing.T) {
	// \| inside a word escapes the pipe so it becomes part of the word.
	got, err := Tokenize(`a\|b`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("a|b", unquoted("a|b")),
		Token{Kind: TokEOF},
	)
}

// -----------------------------------------------------------------------
// Operators
// -----------------------------------------------------------------------

func TestTokenize_Pipe(t *testing.T) {
	got, err := Tokenize("a | b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokPipe, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "|", got[1].Value)
}

func TestTokenize_OrOr(t *testing.T) {
	got, err := Tokenize("a || b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokOrOr, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "||", got[1].Value)
}

func TestTokenize_AndAnd(t *testing.T) {
	got, err := Tokenize("a && b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokAndAnd, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "&&", got[1].Value)
}

func TestTokenize_Semicolon(t *testing.T) {
	got, err := Tokenize("a ; b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokSemicolon, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_Newline(t *testing.T) {
	got, err := Tokenize("a\nb")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokNewline, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_DoubleSemicolon(t *testing.T) {
	got, err := Tokenize("a ;; b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokDoubleSemicolon, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, ";;", got[1].Value)
}

func TestTokenize_RedirOut(t *testing.T) {
	got, err := Tokenize("a > b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirOut, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_RedirAppend(t *testing.T) {
	got, err := Tokenize("a >> b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirAppend, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_RedirIn(t *testing.T) {
	got, err := Tokenize("a < b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirIn, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_RedirErr(t *testing.T) {
	got, err := Tokenize("a 2> b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirErr, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "2>", got[1].Value)
}

func TestTokenize_RedirErrAppend(t *testing.T) {
	got, err := Tokenize("a 2>> b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirErrAppend, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "2>>", got[1].Value)
}

func TestTokenize_RedirErrOut(t *testing.T) {
	got, err := Tokenize("a 2>&1")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirErrOut, TokEOF}, kinds(got))
	assert.Equal(t, "2>&1", got[1].Value)
}

func TestTokenize_RedirBoth(t *testing.T) {
	got, err := Tokenize("a &> b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirBoth, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "&>", got[1].Value)
}

func TestTokenize_RedirBothAlias(t *testing.T) {
	// >& is an alias for &>.
	got, err := Tokenize("a >& b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokRedirBoth, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, ">&", got[1].Value)
}

func TestTokenize_LParenRParen(t *testing.T) {
	got, err := Tokenize("(a)")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokLParen, TokWord, TokRParen, TokEOF}, kinds(got))
}

func TestTokenize_TwoNotRedirErr(t *testing.T) {
	// A bare `2` inside a word is a digit, not a redirection lead.
	got, err := Tokenize("foo2> bar")
	require.NoError(t, err)
	// `foo2` is a word, then `>` is a redirect, then `bar`.
	assert.Equal(t, []TokenKind{TokWord, TokRedirOut, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "foo2", got[0].Value)
}

func TestTokenize_TwoRedirAfterPipe(t *testing.T) {
	// `| 2>` is redirection context (prev = space).
	got, err := Tokenize("a | 2> b")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokPipe, TokRedirErr, TokWord, TokEOF}, kinds(got))
}

// -----------------------------------------------------------------------
// Comments
// -----------------------------------------------------------------------

func TestTokenize_Comment(t *testing.T) {
	got, err := Tokenize("ls # a comment\nfoo")
	require.NoError(t, err)
	// ls, newline, foo
	assert.Equal(t, []TokenKind{TokWord, TokNewline, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "ls", got[0].Value)
	assert.Equal(t, "foo", got[2].Value)
}

func TestTokenize_CommentAtStart(t *testing.T) {
	got, err := Tokenize("# full-line comment\nls")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokNewline, TokWord, TokEOF}, kinds(got))
}

func TestTokenize_HashMidWord(t *testing.T) {
	// foo#bar → Python breaks at the `#`, stripping `bar` as a comment.
	// We match Python exactly — the task description was slightly off
	// here. Verified against src/python/shell.py lines 118–121 + 254.
	got, err := Tokenize("foo#bar")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("foo", unquoted("foo")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_CommentEOF(t *testing.T) {
	got, err := Tokenize("ls # no newline")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokEOF}, kinds(got))
}

// -----------------------------------------------------------------------
// Command substitution, arithmetic, parameter expansion
// -----------------------------------------------------------------------

func TestTokenize_CommandSubst(t *testing.T) {
	got, err := Tokenize("echo $(date)")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("$(date)", unquoted("$(date)")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_CommandSubstNested(t *testing.T) {
	got, err := Tokenize("echo $(echo $(date))")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("$(echo $(date))", unquoted("$(echo $(date))")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_CommandSubstWithParensInQuotes(t *testing.T) {
	// Parens inside single-quoted content inside $(...) must not change
	// nesting depth.
	got, err := Tokenize(`echo $(echo ')')`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok(`$(echo ')')`, unquoted(`$(echo ')')`)),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_ArithmeticSubst(t *testing.T) {
	got, err := Tokenize("echo $((2+3))")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("$((2+3))", unquoted("$((2+3))")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_ArithmeticSubstNestedParens(t *testing.T) {
	got, err := Tokenize("echo $((2+(3*4)))")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("$((2+(3*4)))", unquoted("$((2+(3*4)))")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_ParamExpansion(t *testing.T) {
	got, err := Tokenize("echo ${VAR:-default}")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("${VAR:-default}", unquoted("${VAR:-default}")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_ParamExpansionLength(t *testing.T) {
	got, err := Tokenize("echo ${#VAR}")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("${#VAR}", unquoted("${#VAR}")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_Backtick(t *testing.T) {
	got, err := Tokenize("echo `date`")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("`date`", unquoted("`date`")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DollarVar(t *testing.T) {
	// $VAR (no braces) is part of an unquoted word — the expander will
	// deal with it.
	got, err := Tokenize("echo $HOME/file")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("echo", unquoted("echo")),
		wordTok("$HOME/file", unquoted("$HOME/file")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_CmdSubstInsideDoubleQuote(t *testing.T) {
	// "count: $(wc -l)" — the inner content is a single WpDouble part
	// including the $(...) as raw text for the expander.
	got, err := Tokenize(`"count: $(wc -l)"`)
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("count: $(wc -l)", double("count: $(wc -l)")),
		Token{Kind: TokEOF},
	)
}

// -----------------------------------------------------------------------
// Reserved words — NOT promoted by the tokenizer (matches Python).
// -----------------------------------------------------------------------

func TestTokenize_ReservedWordsStayAsWords(t *testing.T) {
	// Python tokenises `if`, `then`, `fi` etc. as WORD tokens and lets
	// the parser recognise them by value. We do the same.
	got, err := Tokenize("if foo; then bar; fi")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{
		TokWord, // if
		TokWord, // foo
		TokSemicolon,
		TokWord, // then
		TokWord, // bar
		TokSemicolon,
		TokWord, // fi
		TokEOF,
	}, kinds(got))
	assert.Equal(t, "if", got[0].Value)
	assert.Equal(t, "then", got[3].Value)
	assert.Equal(t, "fi", got[6].Value)
}

// -----------------------------------------------------------------------
// Error cases
// -----------------------------------------------------------------------

func TestTokenize_UnterminatedSingleQuote(t *testing.T) {
	_, err := Tokenize(`echo 'hello`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated single quote")
	assert.Contains(t, err.Error(), "position 5")
}

func TestTokenize_UnterminatedDoubleQuote(t *testing.T) {
	_, err := Tokenize(`echo "hello`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated double quote")
}

func TestTokenize_UnterminatedCommandSubst(t *testing.T) {
	_, err := Tokenize(`echo $(date`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated $(...)")
}

func TestTokenize_UnterminatedParamExpansion(t *testing.T) {
	_, err := Tokenize(`echo ${VAR`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated ${...}")
}

func TestTokenize_UnterminatedArithmeticSubst(t *testing.T) {
	_, err := Tokenize(`echo $((2+3`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated $((...))")
}

func TestTokenize_UnterminatedBacktick(t *testing.T) {
	_, err := Tokenize("echo `date")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated backtick")
}

func TestTokenize_BareAmpersandError(t *testing.T) {
	// Python has an infinite-loop bug; we reject explicitly.
	_, err := Tokenize("sleep 10 &")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "background execution")
}

// -----------------------------------------------------------------------
// Combined / end-to-end
// -----------------------------------------------------------------------

func TestTokenize_PipelineWithRedirect(t *testing.T) {
	got, err := Tokenize("cat /file | grep foo > /out.txt")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{
		TokWord, // cat
		TokWord, // /file
		TokPipe,
		TokWord, // grep
		TokWord, // foo
		TokRedirOut,
		TokWord, // /out.txt
		TokEOF,
	}, kinds(got))
}

func TestTokenize_IfStatement(t *testing.T) {
	// [ is not an operator — it's part of the [ builtin's name.
	got, err := Tokenize(`if [ "$x" = "yes" ]; then echo matched; fi`)
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{
		TokWord, // if
		TokWord, // [
		TokWord, // "$x"
		TokWord, // =
		TokWord, // "yes"
		TokWord, // ]
		TokSemicolon,
		TokWord, // then
		TokWord, // echo
		TokWord, // matched
		TokSemicolon,
		TokWord, // fi
		TokEOF,
	}, kinds(got))
	assert.Equal(t, "[", got[1].Value)
	assert.Equal(t, "$x", got[2].Value) // double-quoted, content only
}

func TestTokenize_ForLoop(t *testing.T) {
	got, err := Tokenize("for f in a b c; do echo $f; done")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{
		TokWord, // for
		TokWord, // f
		TokWord, // in
		TokWord, // a
		TokWord, // b
		TokWord, // c
		TokSemicolon,
		TokWord, // do
		TokWord, // echo
		TokWord, // $f
		TokSemicolon,
		TokWord, // done
		TokEOF,
	}, kinds(got))
}

func TestTokenize_CaseStatement(t *testing.T) {
	got, err := Tokenize(`case $x in y) echo yes;; n) echo no;; esac`)
	require.NoError(t, err)
	// Not asserting full stream — just that `;;` becomes DSEMI.
	found := 0
	for _, tok := range got {
		if tok.Kind == TokDoubleSemicolon {
			found++
		}
	}
	assert.Equal(t, 2, found, "expected two ;; tokens")
}

// -----------------------------------------------------------------------
// Position tracking
// -----------------------------------------------------------------------

func TestTokenize_PositionTracking(t *testing.T) {
	got, err := Tokenize("ls foo")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, 0, got[0].Pos) // ls
	assert.Equal(t, 3, got[1].Pos) // foo
	assert.Equal(t, 6, got[2].Pos) // EOF at end of input
}

func TestTokenize_PositionWithOperator(t *testing.T) {
	got, err := Tokenize("a || b")
	require.NoError(t, err)
	require.Len(t, got, 4)
	assert.Equal(t, 0, got[0].Pos) // a
	assert.Equal(t, 2, got[1].Pos) // ||
	assert.Equal(t, 5, got[2].Pos) // b
}

// -----------------------------------------------------------------------
// Glob-looking content (tokenizer shouldn't care — it's all words)
// -----------------------------------------------------------------------

func TestTokenize_GlobChars(t *testing.T) {
	got, err := Tokenize("ls *.go")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("ls", unquoted("ls")),
		wordTok("*.go", unquoted("*.go")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_QuestionMarkGlob(t *testing.T) {
	got, err := Tokenize("ls ?.go")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("ls", unquoted("ls")),
		wordTok("?.go", unquoted("?.go")),
		Token{Kind: TokEOF},
	)
}

// -----------------------------------------------------------------------
// Curly braces are NOT operators — they're word characters (matches Python).
// -----------------------------------------------------------------------

func TestTokenize_BracesAreWordChars(t *testing.T) {
	// Python treats { and } as ordinary word characters (they're not in
	// the meta-char break list). This also matches the virtual-bash
	// reference which does NOT support brace expansion / function
	// syntax. We match Python exactly.
	got, err := Tokenize("{foo,bar}")
	require.NoError(t, err)
	assertSameTokens(t, got,
		wordTok("{foo,bar}", unquoted("{foo,bar}")),
		Token{Kind: TokEOF},
	)
}

func TestTokenize_DoubleBracketsAreWordChars(t *testing.T) {
	// Python treats [[ and ]] as word characters — same reason.
	got, err := Tokenize("[[ -z $x ]]")
	require.NoError(t, err)
	assert.Equal(t, []TokenKind{TokWord, TokWord, TokWord, TokWord, TokEOF}, kinds(got))
	assert.Equal(t, "[[", got[0].Value)
	assert.Equal(t, "]]", got[3].Value)
}
