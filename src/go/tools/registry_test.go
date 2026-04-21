package tools_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/tools"
)

func makeAddTool() tools.Def {
	return tools.Tool(addFn, tools.Description("add two numbers"))
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := tools.New()
	d := makeAddTool()
	r.Register(d)

	got, ok := r.Get("add_fn")
	require.True(t, ok)
	assert.Equal(t, "add_fn", got.Name)
}

func TestRegistry_GetMissing(t *testing.T) {
	r := tools.New()
	_, ok := r.Get("nope")
	assert.False(t, ok)
}

func TestRegistry_Unregister(t *testing.T) {
	r := tools.New()
	r.Register(makeAddTool())
	removed := r.Unregister("add_fn")
	assert.True(t, removed)
	_, ok := r.Get("add_fn")
	assert.False(t, ok)
}

func TestRegistry_UnregisterMissing(t *testing.T) {
	r := tools.New()
	removed := r.Unregister("nonexistent")
	assert.False(t, removed)
}

func TestRegistry_ListSorted(t *testing.T) {
	r := tools.New()

	// register in non-alphabetical order
	r.Register(tools.Tool(addFn, tools.Name("z_tool"), tools.Description("z")))
	r.Register(tools.Tool(addFn, tools.Name("a_tool"), tools.Description("a")))
	r.Register(tools.Tool(addFn, tools.Name("m_tool"), tools.Description("m")))

	list := r.List()
	require.Len(t, list, 3)
	assert.Equal(t, "a_tool", list[0].Name)
	assert.Equal(t, "m_tool", list[1].Name)
	assert.Equal(t, "z_tool", list[2].Name)
}

func TestRegistry_Execute(t *testing.T) {
	r := tools.New()
	r.Register(makeAddTool())

	result, err := r.Execute(context.Background(), "add_fn", []byte(`{"a":10,"b":5}`))
	require.NoError(t, err)
	assert.Equal(t, 15, result)
}

func TestRegistry_ExecuteNotFound(t *testing.T) {
	r := tools.New()
	_, err := r.Execute(context.Background(), "missing", []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, tools.ErrNotFound)
}

func TestRegistry_Schemas(t *testing.T) {
	r := tools.New()
	r.Register(tools.Tool(addFn, tools.Description("add two numbers")))

	schemas := r.Schemas()
	require.Len(t, schemas, 1)

	entry := schemas[0]
	assert.Equal(t, "function", entry["type"])

	fn, ok := entry["function"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "add_fn", fn["name"])
	assert.Equal(t, "add two numbers", fn["description"])

	params, ok := fn["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", params["type"])
}

func TestRegistry_RegisterChaining(t *testing.T) {
	r := tools.New()
	ret := r.Register(makeAddTool())
	assert.Equal(t, r, ret, "Register should return the registry for chaining")
}

// ---- race detector --------------------------------------------------------

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := tools.New()
	r.Register(makeAddTool())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = r.Execute(context.Background(), "add_fn", []byte(`{"a":1,"b":2}`))
		}()
		go func() {
			defer wg.Done()
			_ = r.Schemas()
		}()
	}
	wg.Wait()
}
