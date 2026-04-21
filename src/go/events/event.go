// Package events defines the core event types and message bus used by the
// agent harness to communicate structured events between components.
package events

// StreamConfig controls how a specific field in an event is streamed.
// Mode defaults to "buffered"; set to "streaming" to enable token-by-token
// delivery. StreamFields are dotted paths (e.g. "content" or "block.text")
// identifying which field carries the stream, per ADR 0004.
type StreamConfig struct {
	// Mode is "buffered" (default) or "streaming".
	Mode string
	// StreamFields are dotted paths to fields that stream.
	StreamFields []string
}

// EventType defines a structured event the LLM can emit inline.
// It mirrors Python's EventType dataclass in event_stream_parser.py.
type EventType struct {
	Name        string
	Description string
	// Schema describes the event's YAML shape. Keys are field names; values
	// are either a string type annotation (e.g. "string", "integer") or a
	// map[string]any for nested fields. Mirrors Python's dict[str, Any].
	Schema       map[string]any
	Instructions string
	Streaming    StreamConfig
}

// ParsedEvent is a single event lifted out of the LLM stream.
// It mirrors Python's ParsedEvent dataclass in message_bus.py.
type ParsedEvent struct {
	// Type is the event name, matching an EventType.Name.
	Type string
	// Data is the parsed YAML payload, keyed by field name.
	Data map[string]any
	// Stream is non-nil for streaming events and delivers the streaming
	// field's content token-by-token. Consumers MUST drain or the producer
	// will block. Nil for buffered events.
	Stream <-chan string
	// Raw is the raw YAML text of the event. Empty for streaming events.
	Raw string
}
