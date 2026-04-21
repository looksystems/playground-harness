package events

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Helpers ----

// tokenStream splits a string into one-byte tokens and pushes them through a
// channel, mirroring Python's test `async def token_stream(text)` helper.
func tokenStream(ctx context.Context, text string) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		for _, r := range text {
			select {
			case ch <- string(r):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// chunkedTokenStream pushes the input in fixed-size chunks (bytes). Useful for
// exercising the line buffering under realistic LLM token boundaries.
func chunkedTokenStream(ctx context.Context, text string, chunk int) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		for i := 0; i < len(text); i += chunk {
			end := i + chunk
			if end > len(text) {
				end = len(text)
			}
			select {
			case ch <- text[i:end]:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// collect pulls everything from cleanText + events. It also drains any
// streaming channel on each event so the parser never blocks. Returns the
// joined cleanText, the events, and a map of eventIndex → streamed bytes.
func collect(t *testing.T, cleanText <-chan string, events <-chan ParsedEvent) (string, []ParsedEvent, map[int]string) {
	t.Helper()

	var mu sync.Mutex
	var textBuf strings.Builder
	var evs []ParsedEvent
	streamedByIdx := make(map[int]string)

	done := make(chan struct{})

	// Reader for cleanText.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := range cleanText {
			mu.Lock()
			textBuf.WriteString(s)
			mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		idx := 0
		for ev := range events {
			mu.Lock()
			evs = append(evs, ev)
			evIdx := idx
			mu.Unlock()

			if ev.Stream != nil {
				// Drain stream in a separate goroutine per event so we don't
				// block the events channel reader.
				wg.Add(1)
				go func(ch <-chan string, i int) {
					defer wg.Done()
					var sb strings.Builder
					for s := range ch {
						sb.WriteString(s)
					}
					mu.Lock()
					streamedByIdx[i] = sb.String()
					mu.Unlock()
				}(ev.Stream, evIdx)
			}
			idx++
		}
	}()

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collect timed out waiting for parser to finish")
	}

	return textBuf.String(), evs, streamedByIdx
}

// ---- Tests ----

func TestParser_PlainTextPassesThrough(t *testing.T) {
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Hello world, no events here.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Equal(t, text, got)
	assert.Empty(t, evs)
}

func TestParser_PlainTextNoTrailingNewline(t *testing.T) {
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Hello world, no trailing newline."
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Equal(t, text, got)
	assert.Empty(t, evs)
}

func TestParser_BufferedEventExtraction(t *testing.T) {
	et := EventType{
		Name:        "log_entry",
		Description: "A log entry",
		Schema: map[string]any{
			"data": map[string]any{"level": "string", "message": "string"},
		},
	}
	p := NewParser([]EventType{et})

	var cbEvents []ParsedEvent
	var mu sync.Mutex
	p.OnEvent(func(ev ParsedEvent) {
		mu.Lock()
		cbEvents = append(cbEvents, ev)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Before.\n---event\ntype: log_entry\ndata:\n  level: info\n  message: something happened\n---\nAfter.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Equal(t, "Before.\nAfter.\n", got)
	require.Len(t, evs, 1)
	assert.Equal(t, "log_entry", evs[0].Type)
	data := evs[0].Data["data"].(map[string]any)
	assert.Equal(t, "info", data["level"])
	assert.Equal(t, "something happened", data["message"])
	assert.Nil(t, evs[0].Stream, "buffered event should have no Stream channel")
	assert.NotEmpty(t, evs[0].Raw, "buffered event should have Raw YAML")

	// Callback fires too.
	mu.Lock()
	require.Len(t, cbEvents, 1)
	assert.Equal(t, "log_entry", cbEvents[0].Type)
	mu.Unlock()
}

func TestParser_StreamingEvent(t *testing.T) {
	et := EventType{
		Name:        "user_response",
		Description: "Response to user",
		Schema:      map[string]any{"data": map[string]any{"message": "string"}},
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"data.message"},
		},
	}
	p := NewParser([]EventType{et})

	var cbEvents []ParsedEvent
	var mu sync.Mutex
	p.OnEvent(func(ev ParsedEvent) {
		mu.Lock()
		cbEvents = append(cbEvents, ev)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Hi.\n---event\ntype: user_response\ndata:\n  message: Hello there friend\n---\nDone.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, streamed := collect(t, clean, events)

	assert.Equal(t, "Hi.\nDone.\n", got)
	require.Len(t, evs, 1)
	assert.Equal(t, "user_response", evs[0].Type)
	require.NotNil(t, evs[0].Stream, "streaming event must expose Stream channel")
	assert.Empty(t, evs[0].Raw, "streaming event Raw must be empty")

	assert.Contains(t, streamed[0], "Hello there friend",
		"streamed content must include initial value; got %q", streamed[0])

	// Callback fires before stream starts — so it should already be in the list.
	mu.Lock()
	require.Len(t, cbEvents, 1)
	mu.Unlock()
}

func TestParser_StreamingEvent_ChunkedTokens(t *testing.T) {
	et := EventType{
		Name:        "user_response",
		Description: "Response to user",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"data.message"},
		},
	}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stream arrives in 3-byte chunks — exercises line buffering across boundaries.
	text := "---event\ntype: user_response\ndata:\n  message: streaming across chunks\n---\n"
	clean, events := p.Wrap(ctx, chunkedTokenStream(ctx, text, 3))
	_, evs, streamed := collect(t, clean, events)

	require.Len(t, evs, 1)
	assert.Contains(t, streamed[0], "streaming across chunks")
}

func TestParser_UnrecognizedEventPassesAsText(t *testing.T) {
	// No event types registered.
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Before.\n---event\ntype: unknown_thing\ndata:\n  x: 1\n---\nAfter.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	// Python passes the unrecognised event through as raw text.
	assert.Contains(t, got, "---event")
	assert.Contains(t, got, "unknown_thing")
	assert.Contains(t, got, "Before.")
	assert.Contains(t, got, "After.")
	assert.Empty(t, evs)
}

func TestParser_MalformedYAMLPassesAsText(t *testing.T) {
	et := EventType{Name: "test", Description: "test"}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Invalid YAML mapping — never parseable.
	text := "Before.\n---event\n: this is not valid yaml [\n---\nAfter.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Contains(t, got, "Before.")
	assert.Contains(t, got, "After.")
	assert.Empty(t, evs)
}

func TestParser_IncompleteEventAtEOF(t *testing.T) {
	et := EventType{Name: "test", Description: "test"}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No closing `---` — Python falls back to plain text.
	text := "Before.\n---event\ntype: test\ndata:\n  x: 1"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Contains(t, got, "Before.")
	assert.Contains(t, got, "---event")
	assert.Empty(t, evs)
}

func TestParser_MultipleEvents(t *testing.T) {
	et := EventType{
		Name:        "log",
		Description: "A log",
		Schema:      map[string]any{"data": map[string]any{"msg": "string"}},
	}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "A\n---event\ntype: log\ndata:\n  msg: first\n---\nB\n---event\ntype: log\ndata:\n  msg: second\n---\nC\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Equal(t, "A\nB\nC\n", got)
	require.Len(t, evs, 2)
	assert.Equal(t, "first", evs[0].Data["data"].(map[string]any)["msg"])
	assert.Equal(t, "second", evs[1].Data["data"].(map[string]any)["msg"])
}

func TestParser_EmptyEventBodySkipped(t *testing.T) {
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty body → yaml parses to nil → missing `type` → falls through as text.
	text := "A\n---event\n---\nB\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Contains(t, got, "A")
	assert.Contains(t, got, "B")
	// The ---event block falls through as plain text (per Python's pass-through rule).
	assert.Contains(t, got, "---event")
	assert.Empty(t, evs)
}

func TestParser_EventDelimiterOnlyAtLineStart(t *testing.T) {
	// `---event` embedded within a quoted string in ordinary text should NOT
	// trigger a delimiter transition. Python's check is `line.strip() == "---event"`,
	// so only a line consisting solely of `---event` counts.
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Some text with \"---event\" inside a quote on one line.\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Equal(t, text, got)
	assert.Empty(t, evs)
}

func TestParser_UnterminatedEventStartAtEOF(t *testing.T) {
	// `---event` without a trailing newline at the very end of the stream
	// is NOT a complete event marker — Python flushes line_buffer as text.
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "Hello\n---event"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Contains(t, got, "Hello")
	assert.Contains(t, got, "---event")
	assert.Empty(t, evs)
}

func TestParser_StreamingCallbackFiresEarly(t *testing.T) {
	et := EventType{
		Name: "user_response",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"data.message"},
		},
	}
	p := NewParser([]EventType{et})

	cbFired := make(chan ParsedEvent, 1)
	p.OnEvent(func(ev ParsedEvent) {
		cbFired <- ev
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clean, events := p.Wrap(ctx, tokenStream(ctx, "---event\ntype: user_response\ndata:\n  message: hi\n---\n"))
	_, _, _ = collect(t, clean, events)

	select {
	case ev := <-cbFired:
		assert.Equal(t, "user_response", ev.Type)
		assert.NotNil(t, ev.Stream, "streaming event passed to callback must expose Stream")
	case <-time.After(2 * time.Second):
		t.Fatal("streaming callback did not fire")
	}
}

func TestParser_CtxCancelClosesChannels(t *testing.T) {
	p := NewParser(nil)
	ctx, cancel := context.WithCancel(context.Background())

	tokens := make(chan string)
	// Feed one token, then block without closing.
	go func() {
		tokens <- "some text\n"
		// do not close — simulate an upstream that hangs.
	}()

	clean, events := p.Wrap(ctx, tokens)

	// Drain a bit, then cancel.
	<-clean
	cancel()

	// Both channels must eventually close.
	deadline := time.After(2 * time.Second)
	for clean != nil || events != nil {
		select {
		case _, ok := <-clean:
			if !ok {
				clean = nil
			}
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-deadline:
			t.Fatal("channels did not close after ctx cancellation")
		}
	}
}

func TestParser_CtxCancelClosesStreamingChannel(t *testing.T) {
	et := EventType{
		Name: "user_response",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"data.message"},
		},
	}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())

	tokens := make(chan string)
	// Start a streaming event but don't terminate it.
	head := "---event\ntype: user_response\ndata:\n  message: start of message\n"
	go func() {
		for _, r := range head {
			select {
			case tokens <- string(r):
			case <-ctx.Done():
				return
			}
		}
		// Block forever (simulate upstream that hasn't finished yet).
		<-ctx.Done()
	}()

	clean, events := p.Wrap(ctx, tokens)

	// Drain cleanText in background.
	go func() {
		for range clean {
		}
	}()

	// Pull the streaming event.
	var streamCh <-chan string
	select {
	case ev := <-events:
		streamCh = ev.Stream
	case <-time.After(2 * time.Second):
		t.Fatal("streaming event did not arrive")
	}
	require.NotNil(t, streamCh)

	// Drain initial value from stream channel.
	select {
	case <-streamCh:
	case <-time.After(2 * time.Second):
		t.Fatal("initial stream value did not arrive")
	}

	// Cancel ctx — stream channel must close.
	cancel()

	select {
	case _, ok := <-streamCh:
		for ok {
			_, ok = <-streamCh
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streaming channel did not close after ctx cancellation")
	}
}

func TestParser_Register(t *testing.T) {
	// Start with none; register at runtime.
	p := NewParser(nil)
	p.Register(EventType{
		Name:   "late_reg",
		Schema: map[string]any{"data": "string"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "---event\ntype: late_reg\ndata: hello\n---\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	got, evs, _ := collect(t, clean, events)

	assert.Empty(t, got, "event block fully consumed; no plain text")
	require.Len(t, evs, 1)
	assert.Equal(t, "late_reg", evs[0].Type)
}

func TestParser_StreamingFieldMissing_FallsBackToBuffered(t *testing.T) {
	// If an event is declared streaming but the stream field never materialises
	// in the YAML (e.g. wrong path), the event still finalises as buffered at
	// the closing `---`. Matches Python: _try_detect_streaming returns None →
	// stays in EVENT_BODY → _finalize_event handles it at the end.
	et := EventType{
		Name: "user_response",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"data.missing"},
		},
	}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "---event\ntype: user_response\ndata:\n  message: hi\n---\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	_, evs, _ := collect(t, clean, events)

	require.Len(t, evs, 1)
	assert.Nil(t, evs[0].Stream, "without stream field present, event is buffered")
	assert.NotEmpty(t, evs[0].Raw)
}

func TestParser_StreamingScalarInt(t *testing.T) {
	// stringifyScalar test: a non-string streaming value (int) should be
	// stringified — matches Python's str(obj[last_key]).
	et := EventType{
		Name: "counter",
		Streaming: StreamConfig{
			Mode:         "streaming",
			StreamFields: []string{"n"},
		},
	}
	p := NewParser([]EventType{et})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "---event\ntype: counter\nn: 42\n---\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	_, evs, streamed := collect(t, clean, events)

	require.Len(t, evs, 1)
	assert.Equal(t, "42", streamed[0])
}

func TestParser_OnEventCallbackErrorDoesNotCrash(t *testing.T) {
	et := EventType{Name: "test"}
	p := NewParser([]EventType{et})

	p.OnEvent(func(ev ParsedEvent) {
		panic("intentional test panic")
	})

	called := 0
	p.OnEvent(func(ev ParsedEvent) {
		called++
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	text := "---event\ntype: test\n---\n"
	clean, events := p.Wrap(ctx, tokenStream(ctx, text))
	_, evs, _ := collect(t, clean, events)

	require.Len(t, evs, 1)
	assert.Equal(t, 1, called, "second callback runs even after first panics")
}
