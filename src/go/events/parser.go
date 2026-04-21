package events

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Delimiters marking event boundaries in the LLM token stream.
const (
	eventStartDelimiter = "---event"
	eventEndDelimiter   = "---"
)

// parserState tracks where we are in the three-state streaming state machine.
type parserState int

const (
	stateText parserState = iota
	stateEventBody
	stateStreaming
)

// defaultStreamBuffer mirrors Python's default asyncio.Queue() (unbounded, but
// in practice the consumer drains token-by-token). We pick a small buffer so
// goroutine back-pressure is bounded but doesn't stall typical flows.
const defaultStreamBuffer = 100

// Parser extracts events from an LLM token stream. Ports Python's
// EventStreamParser — same state machine, same framing rules.
//
// Architecture (per ADR 0032):
//
//	Parser is a producer that takes a token channel as input and
//	returns two output channels: cleanText (LLM output with event
//	blocks elided) and events (parsed events).
type Parser struct {
	mu              sync.RWMutex
	registry        map[string]EventType
	callbacks       []func(ParsedEvent)
	maxStreamBuffer int
}

// NewParser constructs a Parser that knows about the given event types.
// Unknown event types are silently ignored during parsing (matches Python).
func NewParser(types []EventType) *Parser {
	reg := make(map[string]EventType, len(types))
	for _, t := range types {
		reg[t.Name] = t
	}
	return &Parser{
		registry:        reg,
		maxStreamBuffer: defaultStreamBuffer,
	}
}

// Register adds an event type (or overwrites one with the same name). Safe
// for concurrent use.
func (p *Parser) Register(t EventType) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.registry[t.Name] = t
}

// OnEvent registers a callback fired synchronously on every completed event,
// AS AN ADDITIONAL seam for consumers that don't want channels. Callbacks are
// called from the parser goroutine; they should not block.
func (p *Parser) OnEvent(cb func(ParsedEvent)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callbacks = append(p.callbacks, cb)
}

// snapshotEventType returns the EventType for name (if registered) under lock.
func (p *Parser) snapshotEventType(name string) (EventType, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	et, ok := p.registry[name]
	return et, ok
}

// fireEvent invokes every registered callback for the given event, recovering
// panics and logging errors — matches Python's try/except per callback.
func (p *Parser) fireEvent(event ParsedEvent) {
	p.mu.RLock()
	cbs := make([]func(ParsedEvent), len(p.callbacks))
	copy(cbs, p.callbacks)
	p.mu.RUnlock()

	for _, cb := range cbs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("events: parser callback panic: %v", r)
				}
			}()
			cb(event)
		}()
	}
}

// Wrap consumes tokens and emits cleaned text + parsed events on separate
// channels. The producer owns both channels and closes them when the input
// closes (or ctx cancels).
//
//   - cleanText: the LLM stream with `---event ... ---` blocks removed.
//   - events: each ParsedEvent as they complete.
//
// Consumers MUST drain BOTH channels or the producer blocks. ctx cancellation
// closes both.
//
// For a streaming event, ParsedEvent.Stream is non-nil and delivers the
// streaming field's content tokenwise. It is closed when the terminating
// `---` arrives, the input closes, or ctx cancels. Consumers of the streaming
// event MUST drain the Stream channel too, OR ignore it and drain it on
// cleanup.
func (p *Parser) Wrap(ctx context.Context, tokens <-chan string) (<-chan string, <-chan ParsedEvent) {
	cleanText := make(chan string)
	events := make(chan ParsedEvent)

	go p.run(ctx, tokens, cleanText, events)

	return cleanText, events
}

// run is the main parser loop. It owns cleanText, events, and any in-flight
// streaming channels, closing them on exit.
func (p *Parser) run(ctx context.Context, tokens <-chan string, cleanText chan<- string, events chan<- ParsedEvent) {
	defer close(cleanText)
	defer close(events)

	state := stateText
	var lineBuffer strings.Builder
	var eventLines []string
	var streamCh chan string // non-nil iff state == stateStreaming

	// closeStream safely closes the in-flight streaming channel, if any.
	closeStream := func() {
		if streamCh != nil {
			close(streamCh)
			streamCh = nil
		}
	}
	defer closeStream()

	// emitText writes a chunk to cleanText, honouring ctx cancellation.
	// Returns false if ctx is done.
	emitText := func(s string) bool {
		select {
		case cleanText <- s:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// emitStream writes a chunk to the in-flight streaming channel, honouring
	// ctx cancellation. Returns false if ctx is done.
	emitStream := func(s string) bool {
		if streamCh == nil {
			return true
		}
		select {
		case streamCh <- s:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// emitEvent publishes to the events channel. Returns false if ctx is done.
	emitEvent := func(ev ParsedEvent) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// processLine handles a single complete line (no trailing newline).
	// Returns false if ctx is done.
	processLine := func(line string) bool {
		switch state {
		case stateText:
			if strings.TrimSpace(line) == eventStartDelimiter {
				state = stateEventBody
				eventLines = eventLines[:0]
				return true
			}
			return emitText(line + "\n")

		case stateEventBody:
			if strings.TrimSpace(line) == eventEndDelimiter {
				handled := p.finalizeEvent(eventLines, emitEvent)
				if !handled {
					// Unrecognised or malformed — pass through as text.
					if !emitText(eventStartDelimiter + "\n") {
						return false
					}
					for _, el := range eventLines {
						if !emitText(el + "\n") {
							return false
						}
					}
					if !emitText(eventEndDelimiter + "\n") {
						return false
					}
				}
				state = stateText
				eventLines = eventLines[:0]
				return true
			}

			eventLines = append(eventLines, line)
			// Try to detect streaming event — if detected, transition.
			if ev, ch, initial, ok := p.tryDetectStreaming(eventLines); ok {
				streamCh = ch
				state = stateStreaming
				// Fire callbacks before yielding via channel to preserve
				// Python ordering (_fire_event happens inside the detect).
				p.fireEvent(ev)
				if !emitEvent(ev) {
					return false
				}
				// Seed the stream channel with the initial value if any.
				if initial != "" {
					if !emitStream(initial) {
						return false
					}
				}
			}
			return true

		case stateStreaming:
			if strings.TrimSpace(line) == eventEndDelimiter {
				closeStream()
				state = stateText
				return true
			}
			return emitStream(line + "\n")
		}
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case tok, ok := <-tokens:
			if !ok {
				// End of input — drain buffered state, then return.
				p.handleEOF(&state, &lineBuffer, &eventLines, &streamCh, emitText, emitStream)
				return
			}

			lineBuffer.WriteString(tok)

			// Split on "\n" repeatedly, processing each complete line.
			// We intentionally avoid bufio.Scanner (64KB token limit and
			// line-oriented buffering don't match Python's byte-level
			// incremental ingestion).
			for {
				buf := lineBuffer.String()
				idx := strings.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := buf[:idx]
				rest := buf[idx+1:]
				lineBuffer.Reset()
				lineBuffer.WriteString(rest)
				if !processLine(line) {
					return
				}
			}
		}
	}
}

// handleEOF processes leftover line_buffer content at end of stream, matching
// Python's trailing block. It does NOT close output channels (the caller does).
func (p *Parser) handleEOF(
	state *parserState,
	lineBuffer *strings.Builder,
	eventLines *[]string,
	streamCh *chan string,
	emitText func(string) bool,
	emitStream func(string) bool,
) {
	remaining := lineBuffer.String()

	if remaining != "" {
		switch *state {
		case stateText:
			emitText(remaining)
		case stateEventBody:
			// Incomplete event — dump as text (match Python).
			if !emitText(eventStartDelimiter + "\n") {
				return
			}
			if !emitText(strings.Join(*eventLines, "\n") + "\n") {
				return
			}
			if strings.TrimSpace(remaining) != "" {
				emitText(remaining)
			}
		case stateStreaming:
			if *streamCh != nil {
				if strings.TrimSpace(remaining) != "" {
					if !emitStream(remaining) {
						return
					}
				}
				close(*streamCh)
				*streamCh = nil
			}
		}
		return
	}

	// No trailing buffered content, but state may still be non-TEXT.
	switch *state {
	case stateEventBody:
		if !emitText(eventStartDelimiter + "\n") {
			return
		}
		emitText(strings.Join(*eventLines, "\n"))
	case stateStreaming:
		if *streamCh != nil {
			close(*streamCh)
			*streamCh = nil
		}
	}
}

// finalizeEvent parses a complete buffered event and fires callbacks + channel.
// Returns true if handled (valid event, known type); false if the block should
// be passed through as plain text.
func (p *Parser) finalizeEvent(lines []string, emit func(ParsedEvent) bool) bool {
	raw := strings.Join(lines, "\n")

	var data map[string]any
	if err := yaml.Unmarshal([]byte(raw), &data); err != nil {
		log.Printf("events: malformed event YAML: %v", err)
		return false
	}
	if data == nil {
		log.Printf("events: event missing 'type' field")
		return false
	}

	typeVal, ok := data["type"]
	if !ok {
		log.Printf("events: event missing 'type' field")
		return false
	}
	name, ok := typeVal.(string)
	if !ok {
		return false
	}

	if _, ok := p.snapshotEventType(name); !ok {
		return false
	}

	ev := ParsedEvent{
		Type: name,
		Data: data,
		Raw:  raw,
	}
	p.fireEvent(ev)
	return emit(ev)
}

// tryDetectStreaming parses accumulated YAML lines and, if they form the head
// of a known streaming event, returns a ParsedEvent + its stream channel.
// Matches Python's _try_detect_streaming behaviour: checks field presence via
// dotted-path lookup, returns (_, _, true) as soon as a streaming field is
// present in the parsed data.
//
// Divergence from task description (follows Python exactly): we do NOT enforce
// the "last field in insertion order" rule from ADR 0004. Python's
// implementation only checks for field *presence* after YAML unmarshal
// succeeds — the convention relies on the declared schema putting the stream
// field last in the YAML body. yaml.v3 here parses into map[string]any; key
// order is lost, which is fine because we do not use order.
func (p *Parser) tryDetectStreaming(lines []string) (ParsedEvent, chan string, string, bool) {
	raw := strings.Join(lines, "\n")

	var data map[string]any
	if err := yaml.Unmarshal([]byte(raw), &data); err != nil {
		return ParsedEvent{}, nil, "", false
	}
	if data == nil {
		return ParsedEvent{}, nil, "", false
	}

	typeVal, ok := data["type"]
	if !ok {
		return ParsedEvent{}, nil, "", false
	}
	name, ok := typeVal.(string)
	if !ok {
		return ParsedEvent{}, nil, "", false
	}

	et, ok := p.snapshotEventType(name)
	if !ok || et.Streaming.Mode != "streaming" {
		return ParsedEvent{}, nil, "", false
	}

	for _, sf := range et.Streaming.StreamFields {
		parts := strings.Split(sf, ".")
		var obj any = data
		walkOK := true
		for _, part := range parts[:len(parts)-1] {
			m, isMap := obj.(map[string]any)
			if !isMap {
				walkOK = false
				break
			}
			v, present := m[part]
			if !present {
				walkOK = false
				break
			}
			obj = v
		}
		if !walkOK {
			continue
		}
		m, isMap := obj.(map[string]any)
		if !isMap {
			continue
		}
		lastKey := parts[len(parts)-1]
		v, present := m[lastKey]
		if !present {
			continue
		}

		initial := stringifyScalar(v)

		ch := make(chan string, p.maxStreamBuffer)
		ev := ParsedEvent{
			Type:   name,
			Data:   data,
			Stream: ch,
		}
		return ev, ch, initial, true
	}

	return ParsedEvent{}, nil, "", false
}

// stringifyScalar converts a YAML scalar value to its string form, matching
// Python's str(obj[last_key]) which casts ints/floats/bools/None to text.
func stringifyScalar(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", v)
	}
}
