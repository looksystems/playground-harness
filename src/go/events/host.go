package events

import "sync"

// Host composes the events subsystem for an Agent: the registry of
// known event types, a default set to activate per run, the message
// bus, and the parser. It does not run the parser itself — the agent's
// Run loop does that, feeding the LLM token stream through Host.Parser.
//
// Host is the Go port of Python's EmitsEvents mixin
// (src/python/emits_events.py). Where the Python version is an optional
// mixin that StandardAgent composes in, the Go Agent always has a Host
// (constructed by NewAgent) so the Bus is available without extra
// plumbing. This closes the Python TODO ("parser is not wired to bus")
// by default in Go.
//
// All methods are safe for concurrent use.
type Host struct {
	mu       sync.RWMutex
	registry map[string]EventType
	defaults []string
	bus      *MessageBus
	parser   *Parser
}

// NewHost constructs an empty Host with a fresh MessageBus and Parser.
// The parser starts with no registered event types; Register adds them
// to both the registry and the parser in one step.
func NewHost() *Host {
	return &Host{
		registry: make(map[string]EventType),
		bus:      NewBus(),
		parser:   NewParser(nil),
	}
}

// Register adds (or overwrites) an event type in the registry. The
// parser is updated in-place to recognise the new type. Returns the
// host for fluent chaining.
func (h *Host) Register(t EventType) *Host {
	h.mu.Lock()
	h.registry[t.Name] = t
	h.mu.Unlock()
	// Parser.Register is concurrent-safe with its own lock.
	h.parser.Register(t)
	return h
}

// Registry returns a snapshot of the registered event types.
func (h *Host) Registry() map[string]EventType {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]EventType, len(h.registry))
	for k, v := range h.registry {
		out[k] = v
	}
	return out
}

// SetDefaults sets the default event names activated per run (matches
// Python's `default_events` attribute). Non-registered names are
// silently ignored at ResolveActive time — the same behaviour Python
// has in _resolve_active_events.
func (h *Host) SetDefaults(names ...string) *Host {
	h.mu.Lock()
	h.defaults = append([]string(nil), names...)
	h.mu.Unlock()
	return h
}

// Defaults returns a snapshot of the default event names.
func (h *Host) Defaults() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, len(h.defaults))
	copy(out, h.defaults)
	return out
}

// Bus returns the MessageBus so consumers can Subscribe.
func (h *Host) Bus() *MessageBus {
	return h.bus
}

// Parser returns the Parser that knows about the currently-registered
// event types. It is a stable reference — Register mutates the parser
// in place rather than swapping it out, so long-lived references
// remain valid after new types are registered.
func (h *Host) Parser() *Parser {
	return h.parser
}

// ResolveActive returns the EventType instances active for a run.
//
//   - If events is nil/empty: return types for the defaults, skipping
//     any default names that are not registered (matches Python's
//     _resolve_active_events).
//   - If an element is a string, it is looked up in the registry
//     (missing names are skipped, not errored).
//   - If an element is an EventType, it is used directly — supporting
//     ad-hoc events without pre-registration, matching Python's
//     isinstance(item, EventType) branch.
//   - Other element types are silently skipped.
func (h *Host) ResolveActive(events []any) []EventType {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(events) == 0 {
		out := make([]EventType, 0, len(h.defaults))
		for _, name := range h.defaults {
			if et, ok := h.registry[name]; ok {
				out = append(out, et)
			}
		}
		return out
	}

	out := make([]EventType, 0, len(events))
	for _, item := range events {
		switch v := item.(type) {
		case string:
			if et, ok := h.registry[v]; ok {
				out = append(out, et)
			}
		case EventType:
			out = append(out, v)
		}
	}
	return out
}
