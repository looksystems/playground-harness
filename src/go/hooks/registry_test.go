package hooks_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-harness/go/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOnEmitFiresHandlerWithArgs verifies that On + Emit invokes the handler
// with the correct context and arguments.
func TestOnEmitFiresHandlerWithArgs(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var gotCtx context.Context
	var gotArgs []any
	done := make(chan struct{})

	r.On(hooks.RunStart, func(c context.Context, args ...any) {
		gotCtx = c
		gotArgs = args
		close(done)
	})

	err := r.Emit(ctx, hooks.RunStart, "arg1", 42)
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler was not called")
	}

	assert.Equal(t, ctx, gotCtx)
	assert.Equal(t, []any{"arg1", 42}, gotArgs)
}

// TestMultipleHandlersAllFire ensures all registered handlers for an event are invoked.
func TestMultipleHandlersAllFire(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var mu sync.Mutex
	count := 0
	inc := func(_ context.Context, _ ...any) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	r.On(hooks.RunStart, inc)
	r.On(hooks.RunStart, inc)
	r.On(hooks.RunStart, inc)

	err := r.Emit(ctx, hooks.RunStart)
	require.NoError(t, err)

	mu.Lock()
	got := count
	mu.Unlock()
	assert.Equal(t, 3, got)
}

// TestHandlersForOtherEventsDoNotFire verifies that handlers registered for a
// different event are not invoked.
func TestHandlersForOtherEventsDoNotFire(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var fired bool
	r.On(hooks.RunEnd, func(_ context.Context, _ ...any) {
		fired = true
	})

	err := r.Emit(ctx, hooks.RunStart)
	require.NoError(t, err)

	assert.False(t, fired, "handler for RunEnd should not fire on RunStart")
}

// TestOffRemovesHandlers verifies that Off prevents previously registered
// handlers from being called.
func TestOffRemovesHandlers(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var fired bool
	r.On(hooks.ToolCall, func(_ context.Context, _ ...any) {
		fired = true
	})

	r.Off(hooks.ToolCall)

	err := r.Emit(ctx, hooks.ToolCall)
	require.NoError(t, err)

	assert.False(t, fired, "handler should not fire after Off")
}

// TestHandlersReturnsDefensiveCopy ensures Handlers returns a copy, not the
// internal slice.
func TestHandlersReturnsDefensiveCopy(t *testing.T) {
	r := hooks.New()
	r.On(hooks.RunStart, func(_ context.Context, _ ...any) {})

	h1 := r.Handlers(hooks.RunStart)
	h2 := r.Handlers(hooks.RunStart)

	require.Len(t, h1, 1)
	require.Len(t, h2, 1)

	// Mutate the returned slice; the registry must not be affected.
	h1[0] = nil
	h3 := r.Handlers(hooks.RunStart)
	require.Len(t, h3, 1)
	assert.NotNil(t, h3[0])
}

// TestEmitRecoversPanicAndStillInvokesOthers verifies that a panicking handler
// does not prevent other handlers from running and that Emit still returns nil.
func TestEmitRecoversPanicAndStillInvokesOthers(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var counter int32

	r.On(hooks.Error, func(_ context.Context, _ ...any) {
		panic("intentional panic")
	})
	r.On(hooks.Error, func(_ context.Context, _ ...any) {
		atomic.AddInt32(&counter, 1)
	})

	err := r.Emit(ctx, hooks.Error)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter), "non-panicking handler must still run")
}

// TestEmitReturnsCtxErrWhenPreCancelled checks that Emit returns ctx.Err()
// immediately when the context is already cancelled, without invoking handlers.
func TestEmitReturnsCtxErrWhenPreCancelled(t *testing.T) {
	r := hooks.New()

	var fired bool
	r.On(hooks.LLMRequest, func(_ context.Context, _ ...any) {
		fired = true
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := r.Emit(ctx, hooks.LLMRequest)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, fired, "no handlers should fire on a pre-cancelled ctx")
}

// TestEmitAsyncReturnsImmediately checks that EmitAsync is fire-and-forget.
func TestEmitAsyncReturnsImmediately(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	done := make(chan struct{})
	r.On(hooks.SkillMount, func(_ context.Context, _ ...any) {
		time.Sleep(50 * time.Millisecond)
		close(done)
	})

	start := time.Now()
	r.EmitAsync(ctx, hooks.SkillMount)
	elapsed := time.Since(start)

	// EmitAsync must return well before the handler finishes.
	assert.Less(t, elapsed, 40*time.Millisecond, "EmitAsync must return before handler completes")

	// But the handler should eventually complete.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not complete")
	}
}

// TestConcurrentOnEmit is a race-detector test: concurrent On and Emit must
// not cause data races.
func TestConcurrentOnEmit(t *testing.T) {
	r := hooks.New()
	ctx := context.Background()

	var wg sync.WaitGroup
	const goroutines = 20

	// Spin up goroutines that register handlers concurrently.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.On(hooks.TokenStream, func(_ context.Context, _ ...any) {})
		}()
	}

	// Spin up goroutines that emit concurrently.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Emit(ctx, hooks.TokenStream)
		}()
	}

	wg.Wait()
}
