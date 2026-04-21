package tools_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/tools"
)

// ---- named function -------------------------------------------------------

type AddArgs struct {
	A int `json:"a" desc:"first number"`
	B int `json:"b" desc:"second number"`
}

func addFn(ctx context.Context, args AddArgs) (int, error) {
	return args.A + args.B, nil
}

func TestTool_NamedFunctionDerivesName(t *testing.T) {
	d := tools.Tool(addFn)
	// runtime name will be something like "agent-harness/go/tools_test.addFn"
	// SnakeCase of the final segment "addFn" → "add_fn"
	assert.Equal(t, "add_fn", d.Name)
}

func TestTool_ExplicitNameOverrides(t *testing.T) {
	d := tools.Tool(addFn, tools.Name("my_add"))
	assert.Equal(t, "my_add", d.Name)
}

func TestTool_DescriptionOption(t *testing.T) {
	d := tools.Tool(addFn, tools.Description("add two numbers"))
	assert.Equal(t, "add two numbers", d.Description)
}

func TestTool_SchemaFromArgsStruct(t *testing.T) {
	d := tools.Tool(addFn)
	params := d.Parameters
	require.NotNil(t, params)
	assert.Equal(t, "object", params["type"])

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	aProp := props["a"].(map[string]any)
	assert.Equal(t, "integer", aProp["type"])
	assert.Equal(t, "first number", aProp["description"])
}

func TestTool_Execute(t *testing.T) {
	d := tools.Tool(addFn)
	result, err := d.Execute(context.Background(), []byte(`{"a":3,"b":4}`))
	require.NoError(t, err)
	assert.Equal(t, 7, result)
}

// ---- anonymous fn requires Name -------------------------------------------

func TestTool_AnonymousPanicsWithoutName(t *testing.T) {
	assert.Panics(t, func() {
		tools.Tool(func(ctx context.Context, args AddArgs) (int, error) { return 0, nil })
	})
}

func TestTool_AnonymousWithNameOK(t *testing.T) {
	assert.NotPanics(t, func() {
		tools.Tool(
			func(ctx context.Context, args AddArgs) (int, error) { return 0, nil },
			tools.Name("anon_add"),
		)
	})
}

// ---- wrong shape panics ---------------------------------------------------

func TestTool_PanicsIfNotFunction(t *testing.T) {
	assert.Panics(t, func() { tools.Tool(42) })
}

func TestTool_PanicsOnWrongParamCount(t *testing.T) {
	assert.Panics(t, func() {
		// only one param instead of (ctx, args)
		tools.Tool(func(ctx context.Context) (int, error) { return 0, nil }, tools.Name("bad"))
	})
}

func TestTool_PanicsOnWrongFirstParam(t *testing.T) {
	assert.Panics(t, func() {
		// first param is not context.Context
		tools.Tool(func(x int, args AddArgs) (int, error) { return 0, nil }, tools.Name("bad"))
	})
}

func TestTool_PanicsOnWrongReturnCount(t *testing.T) {
	assert.Panics(t, func() {
		// returns only one value
		tools.Tool(func(ctx context.Context, args AddArgs) int { return 0 }, tools.Name("bad"))
	})
}

func TestTool_PanicsOnWrongReturnError(t *testing.T) {
	assert.Panics(t, func() {
		// second return is not error
		tools.Tool(func(ctx context.Context, args AddArgs) (int, int) { return 0, 0 }, tools.Name("bad"))
	})
}

func TestTool_PanicsOnNonStructArgs(t *testing.T) {
	assert.Panics(t, func() {
		// second param is not a struct
		tools.Tool(func(ctx context.Context, args string) (int, error) { return 0, nil }, tools.Name("bad"))
	})
}
