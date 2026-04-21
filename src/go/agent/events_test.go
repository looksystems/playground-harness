package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/events"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeUserResponseEvent is a compact helper used across the tests below.
func makeUserResponseEvent() events.EventType {
	return events.EventType{
		Name:         "user_response",
		Description:  "Reply to the user",
		Schema:       map[string]any{"message": "string"},
		Instructions: "Emit one per reply.",
	}
}

// ---------------------------------------------------------------------------
// Compile-time capability assertion
// ---------------------------------------------------------------------------

func TestEventsHost_capability(t *testing.T) {
	// Exercised at compile time in hosts.go (var _ EventsHost = (*Agent)(nil))
	// — this test asserts the interface is publicly satisfied.
	var _ EventsHost = NewAgent("m", newFakeClient())
}

// ---------------------------------------------------------------------------
// Builder-level
// ---------------------------------------------------------------------------

func TestBuilder_Event_RegistersOnHost(t *testing.T) {
	et := makeUserResponseEvent()
	a, err := NewBuilder("m").Client(newFakeClient()).Event(et).Build(context.Background())
	require.NoError(t, err)

	reg := a.Events.Registry()
	require.Contains(t, reg, "user_response")
	assert.Equal(t, et.Description, reg["user_response"].Description)
}

func TestBuilder_Events_Variadic(t *testing.T) {
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Events(events.EventType{Name: "a"}, events.EventType{Name: "b"}).
		Build(context.Background())
	require.NoError(t, err)
	assert.Len(t, a.Events.Registry(), 2)
}

func TestBuilder_DefaultEvents_SetsDefaults(t *testing.T) {
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Events(events.EventType{Name: "a"}, events.EventType{Name: "b"}).
		DefaultEvents("a").
		Build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, a.Events.Defaults())
}

func TestBuilder_Event_DefaultsToAllWhenNotSet(t *testing.T) {
	// Registering events without DefaultEvents should fall back to all
	// registered names so the prompt is meaningfully populated.
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Events(events.EventType{Name: "a"}, events.EventType{Name: "b"}).
		Build(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, a.Events.Defaults())
}

// ---------------------------------------------------------------------------
// Prompt injection middleware
// ---------------------------------------------------------------------------

func TestEventPromptMiddleware_AppendsToExistingSystem(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "ok"},
	})

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		System("be nice").
		Event(makeUserResponseEvent()).
		Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	require.Len(t, seen, 2)
	assert.Equal(t, "system", seen[0].Role)
	// The original content is preserved.
	assert.True(t, strings.HasPrefix(seen[0].Content, "be nice"),
		"system prompt must keep the original content first")
	// The event block is appended.
	assert.Contains(t, seen[0].Content, "# Event Emission")
	assert.Contains(t, seen[0].Content, "## Event: user_response")
}

func TestEventPromptMiddleware_PrependsWhenNoSystem(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "ok"},
	})

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		Event(makeUserResponseEvent()).
		Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	require.GreaterOrEqual(t, len(seen), 2)
	assert.Equal(t, "system", seen[0].Role)
	assert.Contains(t, seen[0].Content, "# Event Emission")
}

func TestEventPromptMiddleware_NoEvents_NoInjection(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "ok"},
	})

	// No Event(...) call → no prompt middleware.
	a, err := NewBuilder("m").Client(fc).Streaming(false).Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	for _, m := range seen {
		assert.NotContains(t, m.Content, "# Event Emission",
			"no event prompt should be injected when no events are registered")
	}
}

// ---------------------------------------------------------------------------
// Streaming parser wiring
// ---------------------------------------------------------------------------

// TestRun_parserPipesEventsToBus verifies the end-to-end integration:
// a scripted stream contains a `---event ... ---` block, which the parser
// lifts out into a ParsedEvent that is published to the bus. The
// assistant message content must NOT contain the event block (cleaned
// text only).
func TestRun_parserPipesEventsToBus(t *testing.T) {
	fc := newFakeClient()
	// The first delta carries pre-event text; the second carries the
	// event block; the third is the post-event tail. The parser should
	// assemble them correctly regardless of the chunk boundaries.
	fc.scriptStream(fakeStreamStep{chunks: []llm.Chunk{
		{Content: "Hello world\n"},
		{Content: "---event\ntype: user_response\n"},
		{Content: "message: hi there\n---\n"},
		{Content: "Goodbye.\n"},
	}})

	et := makeUserResponseEvent()
	a, err := NewBuilder("m").
		Client(fc).
		Streaming(true).
		Event(et).
		Build(context.Background())
	require.NoError(t, err)

	// Subscribe to the bus before Run so we capture the event. Use a
	// buffered channel to avoid blocking the publish goroutine.
	got := make(chan events.ParsedEvent, 4)
	a.EventBus().Subscribe("user_response", func(_ context.Context, ev events.ParsedEvent, _ *events.MessageBus) error {
		got <- ev
		return nil
	})

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "ping"}})
	require.NoError(t, err)

	// The assistant content is the cleaned text (event block elided).
	assert.Contains(t, out, "Hello world")
	assert.Contains(t, out, "Goodbye.")
	assert.NotContains(t, out, "---event", "event delimiters must be removed from content")
	assert.NotContains(t, out, "message: hi there", "event body must be removed from content")

	// The bus must have received exactly one parsed event.
	select {
	case ev := <-got:
		assert.Equal(t, "user_response", ev.Type)
		assert.Equal(t, "hi there", ev.Data["message"])
	case <-time.After(time.Second):
		t.Fatalf("expected a user_response event on the bus, timed out")
	}
}

// TestRun_noEvents_preservesContent asserts that when no events are
// registered the streaming path bypasses the parser entirely and the
// content is passed through unchanged — including characters that look
// like event delimiters but are not processed.
func TestRun_noEvents_preservesContent(t *testing.T) {
	fc := newFakeClient()
	fc.scriptStream(fakeStreamStep{chunks: []llm.Chunk{
		{Content: "Here is some literal ---event text that should survive.\n"},
		{Content: "Bye."},
	}})

	// Deliberately no Event(...) call.
	a, err := NewBuilder("m").Client(fc).Streaming(true).Build(context.Background())
	require.NoError(t, err)

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "q"}})
	require.NoError(t, err)

	// Parser is NOT engaged, so the literal "---event" remains intact.
	assert.Contains(t, out, "---event")
	assert.Contains(t, out, "Bye.")
}

// TestRun_parserWithToolCalls_routesIndependently asserts that tool-call
// chunks bypass the parser and feed the tool-call accumulator directly,
// while content chunks go through the parser. The assembled message
// must have both cleaned content AND the tool call.
func TestRun_parserWithToolCalls_routesIndependently(t *testing.T) {
	fc := newFakeClient()
	fc.scriptStream(
		// Turn 1: interleaved content + tool-call deltas.
		fakeStreamStep{chunks: []llm.Chunk{
			{Content: "Thinking...\n"},
			{ToolCallID: "tc_1", ToolName: "add", ToolArgs: `{"a":1,`},
			{ToolCallID: "tc_1", ToolName: "add", ToolArgs: `"b":2}`},
			{Content: "---event\ntype: user_response\nmessage: done\n---\n"},
		}},
		// Turn 2: plain reply so Run terminates.
		fakeStreamStep{chunks: []llm.Chunk{{Content: "result received"}}},
	)

	et := makeUserResponseEvent()
	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	addTool := tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description("add two ints"))

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(true).
		Event(et).
		Tool(addTool).
		Build(context.Background())
	require.NoError(t, err)

	// Capture the bus event.
	var eventsSeen atomic.Int32
	a.EventBus().Subscribe("user_response", func(_ context.Context, _ events.ParsedEvent, _ *events.MessageBus) error {
		eventsSeen.Add(1)
		return nil
	})

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "do it"}})
	require.NoError(t, err)
	assert.Equal(t, "result received", out)
	// Wait a moment for the fire-and-forget publish goroutine to complete.
	assert.Eventually(t, func() bool { return eventsSeen.Load() == 1 },
		time.Second, 10*time.Millisecond, "exactly one event should fire on the bus")

	// Turn 1's request to the LLM doesn't matter; turn 2 must include the
	// tool-result message and the assistant message with the cleaned
	// content (no event block).
	require.Len(t, fc.observeRequests, 2)
	turn2 := fc.observeRequests[1].Messages
	// Find the assistant message.
	var asst *middleware.Message
	for i := range turn2 {
		if turn2[i].Role == "assistant" {
			m := turn2[i]
			asst = &m
		}
	}
	require.NotNil(t, asst)
	assert.NotContains(t, asst.Content, "---event")
	assert.Contains(t, asst.Content, "Thinking...")
	require.Len(t, asst.ToolCalls, 1)
	assert.Equal(t, "add", asst.ToolCalls[0].Name)
	assert.Equal(t, `{"a":1,"b":2}`, asst.ToolCalls[0].Arguments)
}

// TestRun_ctxCancelMidStream_withParser verifies that cancelling the
// context while the parser is pipelined returns ctx.Err() and does not
// leak goroutines/subscriptions — the bus is left clean for subsequent
// runs.
func TestRun_ctxCancelMidStream_withParser(t *testing.T) {
	fc := newFakeClient()
	fc.scriptStream(fakeStreamStep{
		chunks: []llm.Chunk{
			{Content: "a"},
			{Content: "b"},
			{Content: "c"},
			{Content: "d"},
		},
		delay: 20 * time.Millisecond,
	})

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(true).
		Event(makeUserResponseEvent()).
		Build(context.Background())
	require.NoError(t, err)

	// A dummy subscriber to exercise the publish path; it should not
	// hold references after Run returns.
	var subCalls atomic.Int32
	cancelSub := a.EventBus().Subscribe("*", func(_ context.Context, _ events.ParsedEvent, _ *events.MessageBus) error {
		subCalls.Add(1)
		return nil
	})
	defer cancelSub()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()

	_, err = a.Run(ctx, []middleware.Message{{Role: "user", Content: "q"}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)

	// Subscriber should still be registered (we did not unsubscribe) —
	// meaning the bus was not clobbered, only the in-flight goroutines
	// stopped. Subsequent Publish should still reach it.
	require.NoError(t, a.EventBus().Publish(context.Background(), events.ParsedEvent{Type: "user_response"}))
	assert.Eventually(t, func() bool { return subCalls.Load() >= 1 },
		time.Second, 10*time.Millisecond)
}

// TestRun_parserWithNoActiveEvents_fallsBackToPlainAggregator verifies
// the optimisation where even a Host with registered types but no
// defaults (ResolveActive returns empty) uses the plain aggregator —
// so the parser path isn't engaged needlessly.
func TestRun_parserWithNoActiveEvents_fallsBackToPlainAggregator(t *testing.T) {
	fc := newFakeClient()
	// A literal "---event" that would be elided if the parser ran.
	fc.scriptStream(fakeStreamStep{chunks: []llm.Chunk{
		{Content: "---event literal survives\n"},
	}})

	et := events.EventType{Name: "noop"}
	a, err := NewBuilder("m").
		Client(fc).
		Streaming(true).
		Event(et).
		DefaultEvents(). // empty — ResolveActive returns nothing
		Build(context.Background())
	require.NoError(t, err)

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "q"}})
	require.NoError(t, err)
	assert.Contains(t, out, "---event literal survives")
}

// ---------------------------------------------------------------------------
// pipeStreamThroughParser unit tests (no full Run loop)
// ---------------------------------------------------------------------------

func TestPipeStreamThroughParser_IntegratesContentAndToolCalls(t *testing.T) {
	parser := events.NewParser([]events.EventType{makeUserResponseEvent()})
	bus := events.NewBus()

	var got events.ParsedEvent
	var gotCount atomic.Int32
	doneSub := make(chan struct{}, 1)
	bus.Subscribe("user_response", func(_ context.Context, ev events.ParsedEvent, _ *events.MessageBus) error {
		got = ev
		gotCount.Add(1)
		select {
		case doneSub <- struct{}{}:
		default:
		}
		return nil
	})

	ch := make(chan llm.Chunk, 16)
	go func() {
		defer close(ch)
		ch <- llm.Chunk{Content: "hi\n---event\ntype: user_response\nmessage: yo\n---\n"}
		ch <- llm.Chunk{ToolCallID: "t", ToolName: "f", ToolArgs: "{}"}
		ch <- llm.Chunk{Done: true}
	}()

	msg, err := pipeStreamThroughParser(context.Background(), ch, parser, bus)
	require.NoError(t, err)
	assert.NotContains(t, msg.Content, "---event")
	assert.Contains(t, msg.Content, "hi")
	require.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, "f", msg.ToolCalls[0].Name)

	select {
	case <-doneSub:
	case <-time.After(time.Second):
		t.Fatal("event was not published")
	}
	assert.EqualValues(t, 1, gotCount.Load())
	assert.Equal(t, "user_response", got.Type)
}

func TestPipeStreamThroughParser_PropagatesChunkErr(t *testing.T) {
	parser := events.NewParser(nil)
	bus := events.NewBus()

	ch := make(chan llm.Chunk, 2)
	sentinel := errors.New("boom")
	ch <- llm.Chunk{Done: true, Err: sentinel}
	close(ch)

	_, err := pipeStreamThroughParser(context.Background(), ch, parser, bus)
	require.ErrorIs(t, err, sentinel)
}

func TestPipeStreamThroughParser_ConcurrentSubscriberDoesNotBlock(t *testing.T) {
	parser := events.NewParser([]events.EventType{makeUserResponseEvent()})
	bus := events.NewBus()

	// A slow subscriber — publishes are fire-and-forget, so Run must
	// NOT block on this.
	var wg sync.WaitGroup
	wg.Add(1)
	bus.Subscribe("user_response", func(_ context.Context, _ events.ParsedEvent, _ *events.MessageBus) error {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	ch := make(chan llm.Chunk, 4)
	go func() {
		defer close(ch)
		ch <- llm.Chunk{Content: "---event\ntype: user_response\nmessage: x\n---\n"}
		ch <- llm.Chunk{Done: true}
	}()

	start := time.Now()
	msg, err := pipeStreamThroughParser(context.Background(), ch, parser, bus)
	require.NoError(t, err)
	// Function returns before the slow subscriber is done.
	assert.Less(t, time.Since(start), 80*time.Millisecond,
		"pipeStreamThroughParser must not block on slow subscribers")
	assert.Empty(t, msg.Content, "event block is elided; nothing else in stream")

	// Make sure the subscriber eventually completes (else wg will hang on teardown).
	wg.Wait()
}
