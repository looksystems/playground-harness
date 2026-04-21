package hooks

import (
	"context"
	"log"
	"runtime"
	"sync"

	"agent-harness/go/internal/util"
)

// Handler is any function invoked when an event is emitted. Arguments are
// passed opaquely as []any, matching the Python design where each event
// documents what arguments it provides.
type Handler func(ctx context.Context, args ...any)

// Hub is a thread-safe event-handler registry. The zero value is not
// usable; create one with NewHub.
//
// The type is named Hub rather than Registry so it has a distinct
// unqualified name from tools.Registry — which lets subsystems compose via
// anonymous struct embedding without collisions (see ADR 0031).
type Hub struct {
	mu       sync.RWMutex
	handlers map[Event][]Handler
}

// NewHub returns an initialised, ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{
		handlers: make(map[Event][]Handler),
	}
}

// On appends h to the list of handlers for event and returns the hub for
// chaining. It is safe to call On concurrently with other On, Off, or Emit
// calls.
func (r *Hub) On(event Event, h Handler) *Hub {
	r.mu.Lock()
	r.handlers[event] = append(r.handlers[event], h)
	r.mu.Unlock()
	return r
}

// Off removes all handlers for event. It is safe to call concurrently.
func (r *Hub) Off(event Event) {
	r.mu.Lock()
	delete(r.handlers, event)
	r.mu.Unlock()
}

// Handlers returns a defensive copy of the handlers registered for event.
// Mutating the returned slice does not affect the hub.
func (r *Hub) Handlers(event Event) []Handler {
	r.mu.RLock()
	src := r.handlers[event]
	out := make([]Handler, len(src))
	copy(out, src)
	r.mu.RUnlock()
	return out
}

// Emit invokes every handler registered for event concurrently and waits for
// all of them to finish before returning — mirroring asyncio.gather semantics.
//
// If ctx is already cancelled when Emit is called, no handlers are started and
// ctx.Err() is returned immediately.
//
// Panics inside individual handlers are recovered independently; a recovered
// panic is logged via log.Printf and does not prevent other handlers from
// running. Emit itself always returns nil unless the context was pre-cancelled.
func (r *Hub) Emit(ctx context.Context, event Event, args ...any) error {
	// Fast-path: honour a pre-cancelled context before touching the handlers.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	hs := r.Handlers(event)
	if len(hs) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(len(hs))
	for _, h := range hs {
		h := h // capture loop variable
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					log.Printf("hooks.Emit: recovered panic in handler for %q: %v\n%s", event, rec, buf[:n])
				}
			}()
			h(ctx, args...)
		}()
	}
	wg.Wait()
	return nil
}

// EmitAsync dispatches Emit on a separate goroutine via util.GoSafe and
// returns immediately (fire-and-forget). This mirrors the Python
// emit_fire_and_forget helper.
func (r *Hub) EmitAsync(ctx context.Context, event Event, args ...any) {
	util.GoSafe(func() {
		_ = r.Emit(ctx, event, args...)
	})
}
