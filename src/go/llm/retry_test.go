package llm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClient is an in-memory Client for tests.
// completeErrors defines what Complete returns on successive calls (nil = success).
// streamErrors defines what Stream returns on successive calls  (nil = success).
// When the slice is exhausted the call succeeds with a zero value.
type fakeClient struct {
	completeErrors []error
	streamErrors   []error
	completeCalls  int32
	streamCalls    int32

	// successResponse returned when Complete succeeds.
	successResponse Response
	// successChan returned when Stream succeeds.
	successChan <-chan Chunk
}

func (f *fakeClient) Complete(_ context.Context, _ Request) (Response, error) {
	idx := int(atomic.AddInt32(&f.completeCalls, 1)) - 1
	if idx < len(f.completeErrors) && f.completeErrors[idx] != nil {
		return Response{}, f.completeErrors[idx]
	}
	return f.successResponse, nil
}

func (f *fakeClient) Stream(_ context.Context, _ Request) (<-chan Chunk, error) {
	idx := int(atomic.AddInt32(&f.streamCalls, 1)) - 1
	if idx < len(f.streamErrors) && f.streamErrors[idx] != nil {
		return nil, f.streamErrors[idx]
	}
	ch := f.successChan
	if ch == nil {
		// Return a closed channel with a Done chunk.
		c := make(chan Chunk, 1)
		c <- Chunk{Done: true}
		close(c)
		ch = c
	}
	return ch, nil
}

// noopSleep is an injectable sleep that returns immediately.
func noopSleep(_ context.Context, _ time.Duration) error { return nil }

// --- Complete retry tests ---

func TestWithRetry_Complete_SucceedsOnFirstAttempt(t *testing.T) {
	inner := &fakeClient{
		successResponse: Response{Message: Message{Role: "assistant", Content: "hello"}},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	resp, err := c.Complete(context.Background(), Request{})
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Message.Content)
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.completeCalls))
}

func TestWithRetry_Complete_RetriesAndSucceeds(t *testing.T) {
	sentinel := errors.New("transient")
	inner := &fakeClient{
		completeErrors:  []error{sentinel, sentinel},
		successResponse: Response{Message: Message{Role: "assistant", Content: "ok"}},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	resp, err := c.Complete(context.Background(), Request{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Message.Content)
	assert.Equal(t, int32(3), atomic.LoadInt32(&inner.completeCalls))
}

func TestWithRetry_Complete_ExhaustsRetries(t *testing.T) {
	sentinel := errors.New("always fails")
	inner := &fakeClient{
		completeErrors: []error{sentinel, sentinel, sentinel},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	_, err := c.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.Equal(t, sentinel, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&inner.completeCalls))
}

func TestWithRetry_Complete_NoRetryOnContextCanceled(t *testing.T) {
	inner := &fakeClient{
		completeErrors: []error{context.Canceled},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	_, err := c.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// Should stop after the first call.
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.completeCalls))
}

func TestWithRetry_Complete_NoRetryOnDeadlineExceeded(t *testing.T) {
	inner := &fakeClient{
		completeErrors: []error{context.DeadlineExceeded},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	_, err := c.Complete(context.Background(), Request{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.completeCalls))
}

func TestWithRetry_Complete_SleepCancelledReturnsCancelErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sentinel := errors.New("transient")
	inner := &fakeClient{
		completeErrors: []error{sentinel, sentinel, sentinel},
	}
	// Sleep cancels the context on first invocation.
	sleepCalled := 0
	cancelSleep := func(c context.Context, _ time.Duration) error {
		sleepCalled++
		cancel()
		return context.Canceled
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(cancelSleep))

	_, err := c.Complete(ctx, Request{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, sleepCalled, "sleep should be called once before ctx cancel stops the loop")
}

// --- Stream retry tests ---

func TestWithRetry_Stream_SucceedsOnFirstAttempt(t *testing.T) {
	ch := make(chan Chunk, 1)
	ch <- Chunk{Done: true}
	close(ch)

	inner := &fakeClient{successChan: ch}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	got, err := c.Stream(context.Background(), Request{})
	require.NoError(t, err)
	assert.Equal(t, (<-chan Chunk)(ch), got)
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.streamCalls))
}

func TestWithRetry_Stream_RetriesAndSucceeds(t *testing.T) {
	sentinel := errors.New("transient open error")
	inner := &fakeClient{
		streamErrors: []error{sentinel, sentinel},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	got, err := c.Stream(context.Background(), Request{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, int32(3), atomic.LoadInt32(&inner.streamCalls))
}

func TestWithRetry_Stream_ExhaustsRetries(t *testing.T) {
	sentinel := errors.New("open always fails")
	inner := &fakeClient{
		streamErrors: []error{sentinel, sentinel, sentinel},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	_, err := c.Stream(context.Background(), Request{})
	require.Error(t, err)
	assert.Equal(t, sentinel, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&inner.streamCalls))
}

func TestWithRetry_Stream_NoRetryOnContextCanceled(t *testing.T) {
	inner := &fakeClient{
		streamErrors: []error{context.Canceled},
	}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	_, err := c.Stream(context.Background(), Request{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.streamCalls))
}

func TestWithRetry_Stream_InStreamErrorNotRetried(t *testing.T) {
	// The channel is returned successfully (no open error) but delivers a
	// terminal error chunk.  The decorator must NOT retry; the channel is
	// returned to the caller as-is.
	ch := make(chan Chunk, 1)
	ch <- Chunk{Done: true, Err: errors.New("in-stream failure")}
	close(ch)

	inner := &fakeClient{successChan: ch}
	c := WithRetry(inner, MaxRetries(2), withSleepFn(noopSleep))

	got, err := c.Stream(context.Background(), Request{})
	require.NoError(t, err, "no open error expected")
	assert.NotNil(t, got)
	// Only one Stream call should have been made.
	assert.Equal(t, int32(1), atomic.LoadInt32(&inner.streamCalls))

	// The in-stream error is readable from the channel by the consumer.
	chunk := <-got
	assert.True(t, chunk.Done)
	assert.Error(t, chunk.Err)
}

// --- Backoff delay tests ---

func TestBackoffDelay(t *testing.T) {
	max := 10 * time.Second
	assert.Equal(t, 1*time.Second, backoffDelay(0, max))
	assert.Equal(t, 2*time.Second, backoffDelay(1, max))
	assert.Equal(t, 4*time.Second, backoffDelay(2, max))
	assert.Equal(t, 8*time.Second, backoffDelay(3, max))
	assert.Equal(t, 10*time.Second, backoffDelay(4, max)) // capped
	assert.Equal(t, 10*time.Second, backoffDelay(10, max))
}

func TestWithRetry_DefaultOptions(t *testing.T) {
	// WithRetry with no options should default to 2 retries and 10s cap.
	sentinel := errors.New("fail")
	inner := &fakeClient{
		completeErrors: []error{sentinel, sentinel, sentinel},
	}
	// Override sleep to avoid real delays in default-options test.
	c := WithRetry(inner, withSleepFn(noopSleep))

	_, err := c.Complete(context.Background(), Request{})
	require.Error(t, err)
	// 3 calls = 1 initial + 2 retries (default MaxRetries=2).
	assert.Equal(t, int32(3), atomic.LoadInt32(&inner.completeCalls))
}

// --- Type alias tests ---

func TestTypeAliases(t *testing.T) {
	// llm.Message and middleware.Message must be the same type (alias).
	msg := Message{Role: "user", Content: "hello"}
	assert.Equal(t, "user", msg.Role)

	tc := ToolCall{ID: "id1", Name: "my_tool", Arguments: `{"k":"v"}`}
	assert.Equal(t, "id1", tc.ID)
}

// --- Chunk shape tests ---

func TestChunkShapes(t *testing.T) {
	// Plain content chunk.
	c := Chunk{Content: "hello"}
	assert.Equal(t, "hello", c.Content)
	assert.False(t, c.Done)
	assert.Nil(t, c.Err)

	// Terminal success chunk.
	done := Chunk{Done: true}
	assert.True(t, done.Done)
	assert.Nil(t, done.Err)

	// Terminal error chunk.
	e := errors.New("boom")
	errChunk := Chunk{Done: true, Err: e}
	assert.True(t, errChunk.Done)
	assert.Equal(t, e, errChunk.Err)

	// Tool-call chunk.
	tc := Chunk{ToolCallID: "tc-1", ToolName: "my_tool", ToolArgs: `{"a":1}`}
	assert.Equal(t, "tc-1", tc.ToolCallID)
	assert.Equal(t, "my_tool", tc.ToolName)
	assert.Equal(t, `{"a":1}`, tc.ToolArgs)
}
