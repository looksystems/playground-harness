package builtin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// newArithState returns a minimal ExpansionState for arithmetic tests.
func newArithState(vars map[string]string) *ExpansionState {
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	return &ExpansionState{Vars: cp}
}

func TestEvalArithmetic_Literal(t *testing.T) {
	s := newArithState(nil)

	cases := []struct {
		expr string
		want int64
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"  7  ", 7},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Addition(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"2 + 3", 5},
		{"10 - 4", 6},
		{"1 + 2 + 3", 6},
		{"10 - 3 - 2", 5}, // left-associative
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Multiplicative(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"2 * 3", 6},
		{"10 / 2", 5},
		{"10 / 3", 3}, // trunc towards zero
		{"7 % 3", 1},
		{"2 + 3 * 4", 14},
		{"(2 + 3) * 4", 20},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Unary(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"-5", -5},
		{"+5", 5},
		{"--5", 5},
		{"!0", 1},
		{"!1", 0},
		{"!!5", 1},
		{"~0", -1},
		{"~-1", 0},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Comparison(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"1 == 1", 1},
		{"1 == 2", 0},
		{"1 != 2", 1},
		{"1 < 2", 1},
		{"2 < 1", 0},
		{"2 > 1", 1},
		{"1 >= 1", 1},
		{"1 <= 0", 0},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Logical(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"1 && 1", 1},
		{"1 && 0", 0},
		{"0 && 1", 0},
		{"1 || 0", 1},
		{"0 || 0", 0},
		{"0 || 1", 1},
		{"5 && 7", 1}, // non-zero truthy → 1
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Bitwise(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"5 & 3", 1},
		{"5 | 3", 7},
		{"5 ^ 3", 6},
		{"1 << 4", 16},
		{"16 >> 2", 4},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Ternary(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"1 ? 10 : 20", 10},
		{"0 ? 10 : 20", 20},
		{"5 > 3 ? 1 : -1", 1},
		{"5 < 3 ? 1 : -1", -1},
		{"(5 > 0) ? 100 : 200", 100},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Variables(t *testing.T) {
	s := newArithState(map[string]string{
		"x": "5",
		"y": "10",
	})
	cases := []struct {
		expr string
		want int64
	}{
		{"x", 5},
		{"x + 1", 6},
		{"x * y", 50},
		{"$x", 5},
		{"${x}", 5},
		{"$x + $y", 15},
		{"${x} + ${y}", 15},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_UnsetVar(t *testing.T) {
	s := newArithState(nil)
	// Unset vars resolve to 0 (Python behaviour).
	got, err := EvalArithmetic("x + 5", s)
	require.NoError(t, err)
	require.Equal(t, int64(5), got)

	got, err = EvalArithmetic("$missing + 3", s)
	require.NoError(t, err)
	require.Equal(t, int64(3), got)
}

func TestEvalArithmetic_EmptyVar(t *testing.T) {
	s := newArithState(map[string]string{"x": ""})
	got, err := EvalArithmetic("x + 7", s)
	require.NoError(t, err)
	require.Equal(t, int64(7), got)
}

func TestEvalArithmetic_NonNumericVar(t *testing.T) {
	// A variable holding a non-numeric string resolves to 0 (the bare-name
	// replacement inlines the string and then the parser treats it as 0
	// on ParseInt failure).
	s := newArithState(map[string]string{"x": "abc"})
	got, err := EvalArithmetic("x + 5", s)
	require.NoError(t, err)
	require.Equal(t, int64(5), got)
}

func TestEvalArithmetic_DivideByZero(t *testing.T) {
	s := newArithState(nil)
	_, err := EvalArithmetic("5 / 0", s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "division by zero")

	_, err = EvalArithmetic("5 % 0", s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "division by zero")
}

func TestEvalArithmetic_Precedence(t *testing.T) {
	s := newArithState(nil)
	// Verify operator precedence matches bash.
	cases := []struct {
		expr string
		want int64
	}{
		{"1 + 2 * 3", 7},           // mul binds tighter
		{"2 + 3 == 5", 1},          // arith before equality
		{"1 == 1 && 2 == 2", 1},    // equality before &&
		{"1 && 1 || 0", 1},         // && before ||
		{"0 || 1 && 0", 0},         // && still tighter
		{"4 | 2 & 6", 6},           // & before |: 4 | (2 & 6) = 4 | 2 = 6
		{"1 << 2 + 1", 8},          // + before <<: 1 << 3
		{"~0 & 3", 3},              // unary tighter than &
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_Parens(t *testing.T) {
	s := newArithState(nil)
	cases := []struct {
		expr string
		want int64
	}{
		{"(1 + 2) * 3", 9},
		{"((1 + 2))", 3},
		{"(1 + (2 * 3))", 7},
	}
	for _, c := range cases {
		got, err := EvalArithmetic(c.expr, s)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

func TestEvalArithmetic_EmptyExpression(t *testing.T) {
	s := newArithState(nil)
	// Empty expression → 0 (Python silently returns 0 for missing primary).
	got, err := EvalArithmetic("", s)
	require.NoError(t, err)
	require.Equal(t, int64(0), got)

	got, err = EvalArithmetic("   ", s)
	require.NoError(t, err)
	require.Equal(t, int64(0), got)
}

func TestEvalArithmetic_NegativeResult(t *testing.T) {
	s := newArithState(nil)
	got, err := EvalArithmetic("3 - 10", s)
	require.NoError(t, err)
	require.Equal(t, int64(-7), got)
}

func TestTokenizeArith(t *testing.T) {
	// Spot-check the tokenizer to ensure two-char operators are greedy.
	// Note: tokenizeArith runs AFTER variable substitution, so it only
	// ever sees digits + operators — names have been inlined to their
	// values. Stray letters are silently dropped.
	got := tokenizeArith("1<<2 && 3>=4")
	require.Equal(t, []string{"1", "<<", "2", "&&", "3", ">=", "4"}, got)

	got = tokenizeArith("1 ? 2 : 3")
	require.Equal(t, []string{"1", "?", "2", ":", "3"}, got)

	// Letters silently dropped (matching Python's `i += 1` fallthrough).
	got = tokenizeArith("a + b")
	require.Equal(t, []string{"+"}, got)
}
