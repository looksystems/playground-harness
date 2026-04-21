package events

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeEvent is a helper to create a minimal ParsedEvent for testing.
func makeEvent(eventType string) ParsedEvent {
	return ParsedEvent{
		Type: eventType,
		Data: map[string]any{"type": eventType},
	}
}

// ---- Basic subscribe / publish ----

func TestPublish_HandlerReceivesEvent(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	var got ParsedEvent
	b.Subscribe("test.event", func(_ context.Context, ev ParsedEvent, _ *MessageBus) error {
		got = ev
		return nil
	})

	err := b.Publish(ctx, makeEvent("test.event"))
	require.NoError(t, err)
	assert.Equal(t, "test.event", got.Type)
}

func TestPublish_MultipleHandlersSameType_AllFire(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	var mu sync.Mutex
	fired := []int{}

	for i := 0; i < 3; i++ {
		i := i
		b.Subscribe("evt", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
			mu.Lock()
			fired = append(fired, i)
			mu.Unlock()
			return nil
		})
	}

	err := b.Publish(ctx, makeEvent("evt"))
	require.NoError(t, err)
	assert.Len(t, fired, 3, "all three handlers should fire")
}

func TestPublish_HandlersForOtherEventDoNotFire(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	fired := false
	b.Subscribe("other.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		fired = true
		return nil
	})

	err := b.Publish(ctx, makeEvent("my.event"))
	require.NoError(t, err)
	assert.False(t, fired, "handler for different event type must not fire")
}

// ---- Wildcard ----

func TestPublish_WildcardHandlerReceivesAllEvents(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	var mu sync.Mutex
	seen := []string{}

	b.Subscribe("*", func(_ context.Context, ev ParsedEvent, _ *MessageBus) error {
		mu.Lock()
		seen = append(seen, ev.Type)
		mu.Unlock()
		return nil
	})

	for _, typ := range []string{"a", "b", "c"} {
		require.NoError(t, b.Publish(ctx, makeEvent(typ)))
	}
	assert.ElementsMatch(t, []string{"a", "b", "c"}, seen)
}

func TestPublish_SpecificAndWildcardBothFire(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	var specificFired, wildcardFired bool
	b.Subscribe("my.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		specificFired = true
		return nil
	})
	b.Subscribe("*", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		wildcardFired = true
		return nil
	})

	require.NoError(t, b.Publish(ctx, makeEvent("my.event")))
	assert.True(t, specificFired, "specific handler should fire")
	assert.True(t, wildcardFired, "wildcard handler should fire")
}

// ---- CancelFunc ----

func TestSubscribe_CancelRemovesHandler(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	fired := false
	cancel := b.Subscribe("cancelable", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		fired = true
		return nil
	})

	cancel()

	require.NoError(t, b.Publish(ctx, makeEvent("cancelable")))
	assert.False(t, fired, "cancelled handler must not fire")
}

func TestSubscribe_CancelIsIdempotent(t *testing.T) {
	b := NewBus()
	cancel := b.Subscribe("ev", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error { return nil })
	// Calling cancel twice must not panic.
	cancel()
	cancel()
}

// ---- Depth limit ----

func TestPublish_DepthExceeded_ReturnsError(t *testing.T) {
	b := NewBus(WithMaxDepth(3))
	ctx := context.Background()

	// A handler that re-publishes the same event, creating a chain.
	var publishErr atomic.Value
	b.Subscribe("recurse", func(ctx context.Context, ev ParsedEvent, bus *MessageBus) error {
		err := bus.Publish(ctx, ev)
		if err != nil {
			publishErr.Store(err)
		}
		return nil
	})

	// Outer publish should succeed (depth 0→1).
	outerErr := b.Publish(ctx, makeEvent("recurse"))
	// The outer call itself is fine; depth exceeded happens inside the chain.
	_ = outerErr

	// Give goroutines time to finish.
	time.Sleep(50 * time.Millisecond)

	stored := publishErr.Load()
	require.NotNil(t, stored, "depth exceeded error should have been captured")
	assert.ErrorIs(t, stored.(error), ErrDepthExceeded)
}

// ---- Handler errors are logged, not propagated ----

func TestPublish_HandlerErrorNotPropagated(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	b.Subscribe("err.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		return errors.New("something went wrong")
	})

	err := b.Publish(ctx, makeEvent("err.event"))
	assert.NoError(t, err, "handler errors must not propagate to Publish caller")
}

// ---- Panic recovery ----

func TestPublish_HandlerPanicRecovered_OtherHandlersStillFire(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	var otherFired bool
	b.Subscribe("panic.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		panic("boom")
	})
	b.Subscribe("panic.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		otherFired = true
		return nil
	})

	err := b.Publish(ctx, makeEvent("panic.event"))
	assert.NoError(t, err, "Publish must not propagate panic")
	assert.True(t, otherFired, "non-panicking handler must still fire")
}

// ---- Subscribers count ----

func TestSubscribers_CountsCorrectly(t *testing.T) {
	b := NewBus()

	assert.Equal(t, 0, b.Subscribers("my.event"))

	b.Subscribe("my.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error { return nil })
	assert.Equal(t, 1, b.Subscribers("my.event"))

	b.Subscribe("*", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error { return nil })
	// specific (1) + wildcard (1) = 2
	assert.Equal(t, 2, b.Subscribers("my.event"))

	// Wildcard query itself: just the wildcard count (no doubling)
	assert.Equal(t, 1, b.Subscribers("*"))
}

// ---- Clear ----

func TestClear_RemovesAllSubscriptions(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	fired := false
	b.Subscribe("ev", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		fired = true
		return nil
	})
	b.Subscribe("*", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
		fired = true
		return nil
	})

	b.Clear()

	require.NoError(t, b.Publish(ctx, makeEvent("ev")))
	assert.False(t, fired, "no handlers should fire after Clear")
	assert.Equal(t, 0, b.Subscribers("ev"))
	assert.Equal(t, 0, b.Subscribers("*"))
}

// ---- Concurrent safety (race detector) ----

func TestConcurrentSubscribePublish_NoRace(t *testing.T) {
	b := NewBus()
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			// Mix of subscribe, publish, and cancel.
			cancel := b.Subscribe("race.event", func(_ context.Context, _ ParsedEvent, _ *MessageBus) error {
				return nil
			})
			if i%2 == 0 {
				cancel()
			}
			_ = b.Publish(ctx, makeEvent("race.event"))
		}()
	}

	wg.Wait()
}

// ---- No handlers — publish is a no-op ----

func TestPublish_NoHandlers_ReturnsNil(t *testing.T) {
	b := NewBus()
	err := b.Publish(context.Background(), makeEvent("nothing.here"))
	assert.NoError(t, err)
}

// ---- WithMaxDepth option ----

func TestWithMaxDepth_CustomDepth(t *testing.T) {
	b := NewBus(WithMaxDepth(1))
	ctx := context.Background()

	// Depth is 0. First Publish: depth becomes 1. Handler publishes again:
	// depth is already 1 >= maxDepth(1) → ErrDepthExceeded inside handler.
	var innerErr atomic.Value
	b.Subscribe("deep", func(ctx context.Context, ev ParsedEvent, bus *MessageBus) error {
		err := bus.Publish(ctx, ev)
		if err != nil {
			innerErr.Store(err)
		}
		return nil
	})

	require.NoError(t, b.Publish(ctx, makeEvent("deep")))

	time.Sleep(20 * time.Millisecond)
	stored := innerErr.Load()
	require.NotNil(t, stored)
	assert.ErrorIs(t, stored.(error), ErrDepthExceeded)
}
