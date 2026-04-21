package middleware_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"agent-harness/go/middleware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// appendingMW appends a tag to every message's Content on Pre, and appends a
// suffix on Post. Used to verify ordering.
type appendingMW struct {
	middleware.Base
	tag string
}

func (m *appendingMW) Pre(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
	out := make([]middleware.Message, len(msgs))
	for i, msg := range msgs {
		msg.Content += "[pre:" + m.tag + "]"
		out[i] = msg
	}
	return out, nil
}

func (m *appendingMW) Post(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
	msg.Content += "[post:" + m.tag + "]"
	return msg, nil
}

// errorMW always returns an error on Pre/Post.
type errorMW struct {
	middleware.Base
	errPre  error
	errPost error
}

func (m *errorMW) Pre(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
	if m.errPre != nil {
		return nil, m.errPre
	}
	return msgs, nil
}

func (m *errorMW) Post(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
	if m.errPost != nil {
		return middleware.Message{}, m.errPost
	}
	return msg, nil
}

// ── Base ─────────────────────────────────────────────────────────────────────

func TestBase_IsValidMiddleware(t *testing.T) {
	// Base{} must satisfy the Middleware interface at compile time.
	var _ middleware.Middleware = middleware.Base{}
}

func TestBase_Pre_ReturnsInputUnchanged(t *testing.T) {
	ctx := context.Background()
	msgs := []middleware.Message{{Role: "user", Content: "hello"}}
	got, err := middleware.Base{}.Pre(ctx, msgs, nil)
	require.NoError(t, err)
	assert.Equal(t, msgs, got)
}

func TestBase_Post_ReturnsInputUnchanged(t *testing.T) {
	ctx := context.Background()
	msg := middleware.Message{Role: "assistant", Content: "hi"}
	got, err := middleware.Base{}.Post(ctx, msg, nil)
	require.NoError(t, err)
	assert.Equal(t, msg, got)
}

// ── Chain: Use + Snapshot ────────────────────────────────────────────────────

func TestChain_UseAddsMiddleware(t *testing.T) {
	c := middleware.NewChain()
	mw1 := &appendingMW{tag: "a"}
	mw2 := &appendingMW{tag: "b"}
	c.Use(mw1).Use(mw2)
	snap := c.Snapshot()
	require.Len(t, snap, 2)
	assert.Same(t, mw1, snap[0])
	assert.Same(t, mw2, snap[1])
}

func TestChain_Snapshot_IsIndependentCopy(t *testing.T) {
	c := middleware.NewChain()
	c.Use(&appendingMW{tag: "a"})
	snap1 := c.Snapshot()
	c.Use(&appendingMW{tag: "b"})
	snap2 := c.Snapshot()

	assert.Len(t, snap1, 1, "snap1 should not reflect later Use calls")
	assert.Len(t, snap2, 2)
}

// ── Chain: RunPre ────────────────────────────────────────────────────────────

func TestChain_RunPre_AllMiddlewareSeeMessagesInOrder(t *testing.T) {
	c := middleware.NewChain()
	c.Use(&appendingMW{tag: "1"}).Use(&appendingMW{tag: "2"}).Use(&appendingMW{tag: "3"})

	ctx := context.Background()
	msgs := []middleware.Message{{Role: "user", Content: "start"}}
	got, err := c.RunPre(ctx, msgs, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "start[pre:1][pre:2][pre:3]", got[0].Content)
}

func TestChain_RunPre_ShortCircuitsOnError(t *testing.T) {
	sentinel := errors.New("pre error")
	var reached bool

	c := middleware.NewChain()
	c.Use(&errorMW{errPre: sentinel})
	c.Use(middleware.NewMiddlewareFunc(
		func(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
			reached = true
			return msgs, nil
		},
		func(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
			return msg, nil
		},
	))

	_, err := c.RunPre(context.Background(), nil, nil)
	assert.ErrorIs(t, err, sentinel)
	assert.False(t, reached, "middleware after the error should not be called")
}

func TestChain_RunPre_EmptyChain(t *testing.T) {
	c := middleware.NewChain()
	msgs := []middleware.Message{{Role: "user", Content: "x"}}
	got, err := c.RunPre(context.Background(), msgs, nil)
	require.NoError(t, err)
	assert.Equal(t, msgs, got)
}

func TestChain_RunPre_MutationIsPropagated(t *testing.T) {
	// Each middleware sees the output of the previous one.
	c := middleware.NewChain()
	c.Use(middleware.NewMiddlewareFunc(
		func(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
			msgs[0].Content = "mutated-by-first"
			return msgs, nil
		},
		nil,
	))
	c.Use(middleware.NewMiddlewareFunc(
		func(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
			msgs[0].Content += "+second"
			return msgs, nil
		},
		nil,
	))

	msgs := []middleware.Message{{Role: "user", Content: "original"}}
	got, err := c.RunPre(context.Background(), msgs, nil)
	require.NoError(t, err)
	assert.Equal(t, "mutated-by-first+second", got[0].Content)
}

// ── Chain: RunPost ───────────────────────────────────────────────────────────

func TestChain_RunPost_AllMiddlewareSeeMessageInOrder(t *testing.T) {
	c := middleware.NewChain()
	c.Use(&appendingMW{tag: "A"}).Use(&appendingMW{tag: "B"})

	ctx := context.Background()
	msg := middleware.Message{Role: "assistant", Content: "base"}
	got, err := c.RunPost(ctx, msg, nil)
	require.NoError(t, err)
	assert.Equal(t, "base[post:A][post:B]", got.Content)
}

func TestChain_RunPost_ShortCircuitsOnError(t *testing.T) {
	sentinel := errors.New("post error")
	c := middleware.NewChain()
	var reached bool
	c.Use(&errorMW{errPost: sentinel})
	c.Use(middleware.NewMiddlewareFunc(
		nil,
		func(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
			reached = true
			return msg, nil
		},
	))

	_, err := c.RunPost(context.Background(), middleware.Message{}, nil)
	assert.ErrorIs(t, err, sentinel)
	assert.False(t, reached)
}

func TestChain_RunPost_EmptyChain(t *testing.T) {
	c := middleware.NewChain()
	msg := middleware.Message{Role: "assistant", Content: "y"}
	got, err := c.RunPost(context.Background(), msg, nil)
	require.NoError(t, err)
	assert.Equal(t, msg, got)
}

func TestChain_RunPost_SameOrderAsPre(t *testing.T) {
	// Post runs in registration order (same as Pre), not reversed.
	var order []string
	makeRecorder := func(name string) middleware.Middleware {
		return middleware.NewMiddlewareFunc(
			nil,
			func(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
				order = append(order, name)
				return msg, nil
			},
		)
	}

	c := middleware.NewChain()
	c.Use(makeRecorder("first")).Use(makeRecorder("second")).Use(makeRecorder("third"))
	_, err := c.RunPost(context.Background(), middleware.Message{}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second", "third"}, order)
}

// ── ToolCall / rich Message fields ───────────────────────────────────────────

func TestMessage_ToolCallFields(t *testing.T) {
	msg := middleware.Message{
		Role: "assistant",
		ToolCalls: []middleware.ToolCall{
			{ID: "tc1", Name: "my_fn", Arguments: `{"x":1}`},
		},
	}
	assert.Equal(t, "tc1", msg.ToolCalls[0].ID)
}

func TestMessage_ToolResultFields(t *testing.T) {
	msg := middleware.Message{
		Role:       "tool",
		ToolCallID: "tc1",
		Name:       "my_fn",
		Content:    "result",
	}
	assert.Equal(t, "tc1", msg.ToolCallID)
	assert.Equal(t, "my_fn", msg.Name)
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestChain_ConcurrentUseAndRunPre(t *testing.T) {
	// Race detector test: concurrent Use while RunPre is executing.
	c := middleware.NewChain()

	const goroutines = 50
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Use(&appendingMW{tag: fmt.Sprintf("%d", i)})
		}(i)
	}

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs := []middleware.Message{{Role: "user", Content: "c"}}
			_, _ = c.RunPre(context.Background(), msgs, nil)
		}()
	}

	wg.Wait()
	// The chain should have between 0 and goroutines entries; just confirm no
	// data race (the race detector will flag any unsafe access).
	snap := c.Snapshot()
	assert.LessOrEqual(t, len(snap), goroutines)
}

func TestChain_ConcurrentUseAndRunPost(t *testing.T) {
	c := middleware.NewChain()

	const goroutines = 50
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Use(&appendingMW{tag: fmt.Sprintf("%d", i)})
		}(i)
	}

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.RunPost(context.Background(), middleware.Message{Role: "assistant"}, nil)
		}()
	}

	wg.Wait()
}
