package events

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
)

// ErrDepthExceeded is returned by Publish when the recursive publish depth
// exceeds the configured maximum, preventing runaway event cascades.
var ErrDepthExceeded = errors.New("events: message bus depth exceeded")

// Handler processes an event. It receives the event and the bus so that
// handlers can publish derived events. Errors are logged but not propagated,
// matching Python behaviour.
type Handler func(ctx context.Context, event ParsedEvent, bus *MessageBus) error

// CancelFunc removes a subscription when called.
type CancelFunc func()

// MessageBus is a concurrent publish/subscribe bus with wildcard subscriptions
// and a depth cap to prevent runaway event cascades.
//
// Design notes vs Python:
//   - Python uses a single _depth int incremented globally per Publish call.
//     We replicate that with an atomic int32 so concurrent Publish calls each
//     increment the shared counter before dispatching handlers (same semantics).
//   - Python asyncio.gather dispatches all handlers concurrently and treats
//     exceptions as warnings only. We mirror this with goroutines + WaitGroup
//     and recover panics so one bad handler cannot kill others.
//   - Wildcard "*" subscriptions receive every event, implemented via a
//     separate slice (same as Python's self._handlers["*"]).
type MessageBus struct {
	mu       sync.RWMutex
	handlers map[string][]handlerEntry
	depth    atomic.Int32
	maxDepth int32
}

type handlerEntry struct {
	id      uint64
	handler Handler
}

// globalID provides unique IDs for subscriptions to enable cancellation.
var globalID atomic.Uint64

// BusOption is a functional option for NewBus.
type BusOption func(*MessageBus)

// WithMaxDepth sets the maximum recursive publish depth. Defaults to 10.
func WithMaxDepth(n int) BusOption {
	return func(b *MessageBus) {
		b.maxDepth = int32(n)
	}
}

// NewBus constructs a MessageBus. MaxDepth defaults to 10 (matching Python).
func NewBus(opts ...BusOption) *MessageBus {
	b := &MessageBus{
		handlers: make(map[string][]handlerEntry),
		maxDepth: 10,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Subscribe registers a handler for an event type. Use "*" to match all events.
// Returns a CancelFunc that removes the subscription when called. Safe for
// concurrent use.
func (b *MessageBus) Subscribe(eventType string, h Handler) CancelFunc {
	id := globalID.Add(1)

	b.mu.Lock()
	b.handlers[eventType] = append(b.handlers[eventType], handlerEntry{id: id, handler: h})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		entries := b.handlers[eventType]
		for i, e := range entries {
			if e.id == id {
				b.handlers[eventType] = append(entries[:i], entries[i+1:]...)
				break
			}
		}
	}
}

// Publish dispatches the event to all matching handlers (exact type + wildcard
// "*") concurrently and waits for all to complete.
//
// Returns ErrDepthExceeded if the publish stack is too deep (a handler called
// Publish which called Publish ...). Individual handler errors are logged but
// not returned, matching Python's asyncio.gather(return_exceptions=True)
// behaviour. Handler panics are recovered and logged; remaining handlers still
// run.
func (b *MessageBus) Publish(ctx context.Context, event ParsedEvent) error {
	// Check depth before incrementing — Python: if self._depth >= self._max_depth
	if b.depth.Load() >= b.maxDepth {
		log.Printf("events: max publish depth %d reached, dropping event: %s", b.maxDepth, event.Type)
		return ErrDepthExceeded
	}

	b.depth.Add(1)
	defer b.depth.Add(-1)

	// Snapshot handlers under read lock.
	b.mu.RLock()
	specific := make([]Handler, len(b.handlers[event.Type]))
	for i, e := range b.handlers[event.Type] {
		specific[i] = e.handler
	}
	wildcards := make([]Handler, len(b.handlers["*"]))
	for i, e := range b.handlers["*"] {
		wildcards[i] = e.handler
	}
	b.mu.RUnlock()

	all := append(specific, wildcards...)
	if len(all) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	for _, h := range all {
		h := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("events: handler panic for %s: %v", event.Type, r)
				}
			}()
			if err := h(ctx, event, b); err != nil {
				log.Printf("events: handler error for %s: %v", event.Type, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// Subscribers returns the total number of handlers registered for eventType,
// including wildcard ("*") handlers that would also fire. For the wildcard
// itself, returns only the wildcard count (no double-counting).
func (b *MessageBus) Subscribers(eventType string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.handlers[eventType])
	if eventType != "*" {
		n += len(b.handlers["*"])
	}
	return n
}

// Clear removes all subscriptions. Primarily useful for test isolation.
func (b *MessageBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = make(map[string][]handlerEntry)
}
