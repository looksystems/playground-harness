package shell_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeStubFactory returns a FactoryFunc that always produces a *stubDriver.
func makeStubFactory(t *testing.T) shell.FactoryFunc {
	t.Helper()
	return func(opts map[string]any) (shell.Driver, error) {
		return &stubDriver{fs: vfs.NewBuiltinFilesystemDriver()}, nil
	}
}

// ---------------------------------------------------------------------------
// NewFactory
// ---------------------------------------------------------------------------

func TestNewFactory_EmptyRegistry(t *testing.T) {
	f := shell.NewFactory()
	_, err := f.Create("anything", nil)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Register + Create
// ---------------------------------------------------------------------------

func TestFactory_RegisterAndCreate(t *testing.T) {
	f := shell.NewFactory()
	f.Register("stub", makeStubFactory(t))

	d, err := f.Create("stub", nil)
	require.NoError(t, err)
	assert.NotNil(t, d)
}

func TestFactory_CreatePassesOpts(t *testing.T) {
	f := shell.NewFactory()
	var gotOpts map[string]any

	f.Register("capture", func(opts map[string]any) (shell.Driver, error) {
		gotOpts = opts
		return &stubDriver{fs: vfs.NewBuiltinFilesystemDriver()}, nil
	})

	opts := map[string]any{"cwd": "/tmp", "debug": true}
	_, err := f.Create("capture", opts)
	require.NoError(t, err)
	assert.Equal(t, "/tmp", gotOpts["cwd"])
	assert.Equal(t, true, gotOpts["debug"])
}

func TestFactory_CreateUnknownReturnsError(t *testing.T) {
	f := shell.NewFactory()
	_, err := f.Create("nonexistent", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

// ---------------------------------------------------------------------------
// SetDefault
// ---------------------------------------------------------------------------

func TestFactory_SetDefault(t *testing.T) {
	f := shell.NewFactory()
	f.Register("alpha", makeStubFactory(t))
	f.Register("beta", makeStubFactory(t))
	f.SetDefault("alpha")

	d, err := f.Create("", nil)
	require.NoError(t, err)
	assert.NotNil(t, d)
}

func TestFactory_SetDefault_SwitchesDefault(t *testing.T) {
	var lastCreated string

	factoryFor := func(name string) shell.FactoryFunc {
		return func(_ map[string]any) (shell.Driver, error) {
			lastCreated = name
			return &stubDriver{fs: vfs.NewBuiltinFilesystemDriver()}, nil
		}
	}

	f := shell.NewFactory()
	f.Register("first", factoryFor("first"))
	f.Register("second", factoryFor("second"))

	f.SetDefault("first")
	_, err := f.Create("", nil)
	require.NoError(t, err)
	assert.Equal(t, "first", lastCreated)

	f.SetDefault("second")
	_, err = f.Create("", nil)
	require.NoError(t, err)
	assert.Equal(t, "second", lastCreated)
}

func TestFactory_CreateEmptyNoDefault(t *testing.T) {
	f := shell.NewFactory()
	f.Register("stub", makeStubFactory(t))
	// No SetDefault call

	_, err := f.Create("", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no driver name")
}

// ---------------------------------------------------------------------------
// Register overwrites
// ---------------------------------------------------------------------------

func TestFactory_RegisterOverwrites(t *testing.T) {
	var callCount int
	f := shell.NewFactory()
	f.Register("x", func(_ map[string]any) (shell.Driver, error) {
		callCount++
		return &stubDriver{fs: vfs.NewBuiltinFilesystemDriver()}, nil
	})
	// Overwrite with a different factory
	f.Register("x", func(_ map[string]any) (shell.Driver, error) {
		callCount += 10
		return &stubDriver{fs: vfs.NewBuiltinFilesystemDriver()}, nil
	})
	_, err := f.Create("x", nil)
	require.NoError(t, err)
	assert.Equal(t, 10, callCount, "second factory should replace first")
}

// ---------------------------------------------------------------------------
// Chaining
// ---------------------------------------------------------------------------

func TestFactory_ChainRegisterAndSetDefault(t *testing.T) {
	f := shell.NewFactory().
		Register("stub", makeStubFactory(t)).
		SetDefault("stub")

	d, err := f.Create("", nil)
	require.NoError(t, err)
	assert.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Concurrency safety
// ---------------------------------------------------------------------------

func TestFactory_ConcurrentRegisterAndCreate(t *testing.T) {
	f := shell.NewFactory()
	f.Register("safe", makeStubFactory(t))
	f.SetDefault("safe")

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			// Mix of register and create operations.
			if i%2 == 0 {
				f.Register("safe", makeStubFactory(t))
			} else {
				_, _ = f.Create("safe", nil)
			}
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// DefaultFactory package-level variable
// ---------------------------------------------------------------------------

func TestDefaultFactory_Exists(t *testing.T) {
	assert.NotNil(t, shell.DefaultFactory)
}

// Ensure the DefaultFactory is a distinct object from a fresh NewFactory().
func TestDefaultFactory_IsDistinct(t *testing.T) {
	fresh := shell.NewFactory()
	// Register something on the fresh factory; DefaultFactory should not be affected.
	fresh.Register("isolated", makeStubFactory(t))
	_, err := shell.DefaultFactory.Create("isolated", nil)
	assert.Error(t, err, "isolated factory should not appear in DefaultFactory")
}

// ---------------------------------------------------------------------------
// stubDriver methods needed only in this file (ExecStream etc. already in driver_test.go)
// The type is defined in driver_test.go (same package shell_test).
// We add a tiny compile-check here to make sure all Driver methods are covered.
// ---------------------------------------------------------------------------

var _ shell.Driver = (*stubDriver)(nil)

// contextKey avoids a lint complaint about unused import.
var _ = context.Background
