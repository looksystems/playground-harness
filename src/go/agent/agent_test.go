package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fake LLM client
// ---------------------------------------------------------------------------

// fakeCompleteStep is a single scripted response for Complete.
type fakeCompleteStep struct {
	msg middleware.Message
	err error
}

// fakeStreamStep is a scripted sequence of chunks delivered for one Stream
// call. The fake client sends each chunk in order, then closes the channel.
// If err is non-nil, it is returned from Stream itself (before any chunks);
// terminalErr, if non-nil, is emitted on the terminal Chunk{Done:true, Err}.
type fakeStreamStep struct {
	chunks      []llm.Chunk
	err         error
	terminalErr error
	// delay is inserted before delivering each chunk; useful for ctx
	// cancellation tests.
	delay time.Duration
}

// fakeClient is a scripted llm.Client used across tests. Use
// scriptComplete/scriptStream to enqueue responses.
type fakeClient struct {
	mu              sync.Mutex
	completeScript  []fakeCompleteStep
	streamScript    []fakeStreamStep
	completeCalls   int
	streamCalls     int
	lastRequest     llm.Request
	observeRequests []llm.Request
}

func newFakeClient() *fakeClient { return &fakeClient{} }

func (f *fakeClient) scriptComplete(steps ...fakeCompleteStep) { f.completeScript = append(f.completeScript, steps...) }
func (f *fakeClient) scriptStream(steps ...fakeStreamStep)     { f.streamScript = append(f.streamScript, steps...) }

func (f *fakeClient) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	f.completeCalls++
	f.lastRequest = req
	// Capture a defensive copy of the messages so later mutations don't
	// shift what we saw.
	copied := append([]middleware.Message(nil), req.Messages...)
	f.observeRequests = append(f.observeRequests, llm.Request{
		Model:    req.Model,
		Messages: copied,
		Tools:    req.Tools,
	})
	if len(f.completeScript) == 0 {
		f.mu.Unlock()
		return llm.Response{}, fmt.Errorf("fakeClient: Complete script exhausted")
	}
	step := f.completeScript[0]
	f.completeScript = f.completeScript[1:]
	f.mu.Unlock()
	if step.err != nil {
		return llm.Response{}, step.err
	}
	return llm.Response{Message: step.msg}, nil
}

func (f *fakeClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	f.mu.Lock()
	f.streamCalls++
	f.lastRequest = req
	copied := append([]middleware.Message(nil), req.Messages...)
	f.observeRequests = append(f.observeRequests, llm.Request{
		Model:    req.Model,
		Messages: copied,
		Tools:    req.Tools,
	})
	if len(f.streamScript) == 0 {
		f.mu.Unlock()
		return nil, fmt.Errorf("fakeClient: Stream script exhausted")
	}
	step := f.streamScript[0]
	f.streamScript = f.streamScript[1:]
	f.mu.Unlock()
	if step.err != nil {
		return nil, step.err
	}
	ch := make(chan llm.Chunk, 16)
	go func() {
		defer close(ch)
		for _, c := range step.chunks {
			if step.delay > 0 {
				select {
				case <-time.After(step.delay):
				case <-ctx.Done():
					ch <- llm.Chunk{Done: true, Err: ctx.Err()}
					return
				}
			}
			select {
			case ch <- c:
			case <-ctx.Done():
				return
			}
		}
		terminal := llm.Chunk{Done: true}
		if step.terminalErr != nil {
			terminal.Err = step.terminalErr
		}
		select {
		case ch <- terminal:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// ---------------------------------------------------------------------------
// Basic construction tests
// ---------------------------------------------------------------------------

func TestNewAgent_initialisesSubsystems(t *testing.T) {
	a := NewAgent("m", newFakeClient())
	require.NotNil(t, a.Hub, "hooks hub must be initialised")
	require.NotNil(t, a.Registry, "tools registry must be initialised")
	require.NotNil(t, a.Chain, "middleware chain must be initialised")
	require.Equal(t, defaultMaxTurns, a.MaxTurns)
	require.Equal(t, defaultMaxRetries, a.MaxRetries)
	require.True(t, a.Stream)
}

func TestBuilder_Build_requiresClient(t *testing.T) {
	_, err := NewBuilder("gpt").Build(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Client")
}

func TestBuilder_Build_requiresModel(t *testing.T) {
	_, err := NewBuilder("").Client(newFakeClient()).Build(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Model")
}

func TestBuilder_Build_appliesFluentConfig(t *testing.T) {
	fc := newFakeClient()
	td := tools.Def{
		Name:        "noop",
		Description: "",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Execute:     func(_ context.Context, _ []byte) (any, error) { return nil, nil },
	}
	mw := middleware.NewMiddlewareFunc(nil, nil)
	var sawRunStart atomic.Bool

	a, err := NewBuilder("m").
		System("sys").
		MaxTurns(3).
		MaxRetries(7).
		Streaming(false).
		Client(fc).
		Tool(td).
		Use(mw).
		On(hooks.RunStart, func(context.Context, ...any) { sawRunStart.Store(true) }).
		Build(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "sys", a.System)
	assert.Equal(t, 3, a.MaxTurns)
	assert.Equal(t, 7, a.MaxRetries)
	assert.False(t, a.Stream)

	_, ok := a.Get("noop")
	assert.True(t, ok, "tool should be registered")
	assert.Len(t, a.Snapshot(), 1, "middleware should be registered")

	// Fire RunStart manually to verify the hook was wired.
	require.NoError(t, a.Emit(context.Background(), hooks.RunStart))
	assert.True(t, sawRunStart.Load())
}

func TestCapabilityInterfaces_compileTime(t *testing.T) {
	// All three assertions are compile-time in hosts.go; this test simply
	// makes it obvious that these interfaces are part of the public API.
	var _ HooksHost = NewAgent("m", newFakeClient())
	var _ ToolsHost = NewAgent("m", newFakeClient())
	var _ MiddlewareHost = NewAgent("m", newFakeClient())
}

// ---------------------------------------------------------------------------
// Run-loop integration tests
// ---------------------------------------------------------------------------

// hookRecorder captures the order in which events fired. Safe for concurrent
// use (hooks.Emit dispatches handlers on separate goroutines).
type hookRecorder struct {
	mu     sync.Mutex
	events []hooks.Event
}

func (r *hookRecorder) record(e hooks.Event) hooks.Handler {
	return func(_ context.Context, _ ...any) {
		r.mu.Lock()
		r.events = append(r.events, e)
		r.mu.Unlock()
	}
}

func (r *hookRecorder) seen() []hooks.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]hooks.Event, len(r.events))
	copy(out, r.events)
	return out
}

// Scenario 1: plain reply, no tools.
func TestRun_plainReply_nonStreaming(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "hello"},
	})

	a, err := NewBuilder("m").Client(fc).Streaming(false).Build(context.Background())
	require.NoError(t, err)

	rec := &hookRecorder{}
	for _, e := range []hooks.Event{hooks.RunStart, hooks.LLMRequest, hooks.LLMResponse, hooks.RunEnd} {
		a.On(e, rec.record(e))
	}

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
	assert.Equal(t, 1, fc.completeCalls)

	seen := rec.seen()
	// Hooks fire concurrently, but Emit waits for each event's handlers
	// before returning; events across Emit calls are sequenced by Run. The
	// recorder should therefore see them in exactly this order.
	require.Equal(t, []hooks.Event{hooks.RunStart, hooks.LLMRequest, hooks.LLMResponse, hooks.RunEnd}, seen)
}

// Scenario 2: one tool-call round trip.
func TestRun_toolRoundTrip_nonStreaming(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(
		fakeCompleteStep{msg: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{{
				ID:        "tc_1",
				Name:      "add",
				Arguments: `{"a":1,"b":41}`,
			}},
		}},
		fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "the answer is 42"}},
	)

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	addTool := tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description("add two ints"))

	a, err := NewBuilder("m").Client(fc).Streaming(false).Tool(addTool).Build(context.Background())
	require.NoError(t, err)

	var toolResultSeen atomic.Int32
	a.On(hooks.ToolResult, func(_ context.Context, args ...any) {
		toolResultSeen.Add(1)
		require.Len(t, args, 2)
		assert.Equal(t, "add", args[0])
	})

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "what?"}})
	require.NoError(t, err)
	assert.Equal(t, "the answer is 42", out)
	assert.Equal(t, 2, fc.completeCalls)
	assert.EqualValues(t, 1, toolResultSeen.Load())

	// The second Complete call must have seen the appended tool message.
	require.Len(t, fc.observeRequests, 2)
	msgs := fc.observeRequests[1].Messages
	// Expect: user, assistant(with tool_calls), tool
	require.GreaterOrEqual(t, len(msgs), 3)
	last := msgs[len(msgs)-1]
	assert.Equal(t, "tool", last.Role)
	assert.Equal(t, "tc_1", last.ToolCallID)
	assert.Equal(t, "add", last.Name)
	// Result should be JSON-encoded integer 42.
	assert.Equal(t, "42", last.Content)
}

// Scenario 3: streaming tool-call aggregation across multiple deltas.
func TestRun_streamingToolCallAggregation(t *testing.T) {
	fc := newFakeClient()
	fc.scriptStream(
		// First turn: streamed tool call assembled from multiple chunks.
		fakeStreamStep{chunks: []llm.Chunk{
			// name+id arrive first, empty args.
			{ToolCallID: "tc_x", ToolName: "add", ToolArgs: ""},
			// args arrive in pieces. ToolName is the accumulated-so-far
			// value from the provider, so subsequent chunks carry the same
			// name.
			{ToolCallID: "tc_x", ToolName: "add", ToolArgs: `{"a":1,`},
			{ToolCallID: "tc_x", ToolName: "add", ToolArgs: `"b":2}`},
		}},
		// Second turn: plain "done".
		fakeStreamStep{chunks: []llm.Chunk{
			{Content: "done"},
		}},
	)

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var observedArgs addArgs
	addTool := tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		observedArgs = args
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description(""))

	a, err := NewBuilder("m").Client(fc).Streaming(true).Tool(addTool).Build(context.Background())
	require.NoError(t, err)

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "sum"}})
	require.NoError(t, err)
	assert.Equal(t, "done", out)
	assert.Equal(t, 2, fc.streamCalls)
	assert.Equal(t, 1, observedArgs.A)
	assert.Equal(t, 2, observedArgs.B)
}

// Scenario 4: a middleware mutates the message list before the LLM call.
func TestRun_middlewareMutatesMessages(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "ok"},
	})

	appendSystemNote := middleware.NewMiddlewareFunc(
		func(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
			return append(msgs, middleware.Message{Role: "system", Content: "[note]"}), nil
		},
		nil,
	)

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		Use(appendSystemNote).
		Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	// Expect the note at the end.
	require.NotEmpty(t, seen)
	last := seen[len(seen)-1]
	assert.Equal(t, "system", last.Role)
	assert.Equal(t, "[note]", last.Content)
}

// Scenario 5: tool error does not abort the run.
func TestRun_toolError_doesNotAbort(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(
		fakeCompleteStep{msg: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{{
				ID:        "tc_err",
				Name:      "boom",
				Arguments: `{}`,
			}},
		}},
		fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "recovered"}},
	)

	type empty struct{}
	boomTool := tools.Tool(func(_ context.Context, _ empty) (any, error) {
		return nil, errors.New("kaboom")
	}, tools.Name("boom"), tools.Description(""))

	a, err := NewBuilder("m").Client(fc).Streaming(false).Tool(boomTool).Build(context.Background())
	require.NoError(t, err)

	var sawToolError atomic.Bool
	a.On(hooks.ToolError, func(_ context.Context, args ...any) {
		sawToolError.Store(true)
		require.Len(t, args, 2)
	})

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "go"}})
	require.NoError(t, err)
	assert.Equal(t, "recovered", out)
	assert.True(t, sawToolError.Load())

	// Second request should contain a tool message with a JSON error envelope.
	require.Len(t, fc.observeRequests, 2)
	msgs := fc.observeRequests[1].Messages
	var toolMsg *middleware.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			m := msgs[i]
			toolMsg = &m
			break
		}
	}
	require.NotNil(t, toolMsg)
	var envelope map[string]string
	require.NoError(t, json.Unmarshal([]byte(toolMsg.Content), &envelope))
	assert.Equal(t, "kaboom", envelope["error"])
}

// Scenario 5b: unknown tool call gets enveloped and the run continues,
// matching Python uses_tools.py behaviour. ToolCall/ToolResult must NOT fire;
// ToolError fires exactly once.
func TestRun_unknownTool_envelopedAndContinues(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(
		fakeCompleteStep{msg: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{{
				ID:        "tc_bogus",
				Name:      "does_not_exist",
				Arguments: `{}`,
			}},
		}},
		fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "ok anyway"}},
	)

	// Deliberately register NO tools.
	a, err := NewBuilder("m").Client(fc).Streaming(false).Build(context.Background())
	require.NoError(t, err)

	var toolCallCount, toolResultCount, toolErrorCount atomic.Int32
	a.On(hooks.ToolCall, func(_ context.Context, _ ...any) { toolCallCount.Add(1) })
	a.On(hooks.ToolResult, func(_ context.Context, _ ...any) { toolResultCount.Add(1) })
	a.On(hooks.ToolError, func(_ context.Context, _ ...any) { toolErrorCount.Add(1) })

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "go"}})
	require.NoError(t, err, "unknown tool must not abort the run")
	assert.Equal(t, "ok anyway", out)
	assert.Equal(t, 2, fc.completeCalls)

	// Verify hook invocations.
	assert.EqualValues(t, 0, toolCallCount.Load(), "ToolCall must NOT fire for unknown tools")
	assert.EqualValues(t, 0, toolResultCount.Load(), "ToolResult must NOT fire for unknown tools")
	assert.EqualValues(t, 1, toolErrorCount.Load(), "ToolError fires once")

	// The second LLM request must contain a tool message with the
	// unknown-tool envelope.
	require.Len(t, fc.observeRequests, 2)
	msgs := fc.observeRequests[1].Messages
	var toolMsg *middleware.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			m := msgs[i]
			toolMsg = &m
			break
		}
	}
	require.NotNil(t, toolMsg, "unknown-tool path must append a tool message")
	assert.Equal(t, "tc_bogus", toolMsg.ToolCallID)
	assert.Equal(t, "does_not_exist", toolMsg.Name)

	var envelope map[string]string
	require.NoError(t, json.Unmarshal([]byte(toolMsg.Content), &envelope))
	assert.Equal(t, "Unknown tool: does_not_exist", envelope["error"])
}

// Scenario 6: MaxTurns=1 must not loop even if the LLM returns tool calls.
func TestRun_maxTurnsCap(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{msg: middleware.Message{
		Role:    "assistant",
		Content: "partial",
		ToolCalls: []middleware.ToolCall{{
			ID:        "tc_loop",
			Name:      "noop",
			Arguments: `{}`,
		}},
	}})

	type empty struct{}
	noopTool := tools.Tool(func(_ context.Context, _ empty) (any, error) { return "ok", nil },
		tools.Name("noop"), tools.Description(""))

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		MaxTurns(1).
		Tool(noopTool).
		Build(context.Background())
	require.NoError(t, err)

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "x"}})
	require.NoError(t, err)
	assert.Equal(t, "partial", out)
	assert.Equal(t, 1, fc.completeCalls, "Run must not loop past MaxTurns even with outstanding tool calls")
}

// Scenario 7: context cancellation mid-stream returns ctx.Err().
func TestRun_ctxCancelMidStream(t *testing.T) {
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

	a, err := NewBuilder("m").Client(fc).Streaming(true).Build(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()

	_, err = a.Run(ctx, []middleware.Message{{Role: "user", Content: "q"}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
}

// ---------------------------------------------------------------------------
// System-prompt prepending
// ---------------------------------------------------------------------------

func TestRun_systemPrompt_prependsWhenMissing(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "ok"}})

	a, err := NewBuilder("m").Client(fc).Streaming(false).System("be nice").Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	require.Len(t, seen, 2)
	assert.Equal(t, "system", seen[0].Role)
	assert.Equal(t, "be nice", seen[0].Content)
}

func TestRun_systemPrompt_replacesExistingSystem(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "ok"}})

	a, err := NewBuilder("m").Client(fc).Streaming(false).System("override").Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{
		{Role: "system", Content: "original"},
		{Role: "user", Content: "hi"},
	})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	seen := fc.observeRequests[0].Messages
	require.Len(t, seen, 2)
	assert.Equal(t, "system", seen[0].Role)
	assert.Equal(t, "override", seen[0].Content)
}

// ---------------------------------------------------------------------------
// Stream aggregation unit test — no full Run loop.
// ---------------------------------------------------------------------------

func TestAggregateStream_assemblesContentAndTools(t *testing.T) {
	ch := make(chan llm.Chunk, 8)
	ch <- llm.Chunk{Content: "he"}
	ch <- llm.Chunk{Content: "llo"}
	ch <- llm.Chunk{ToolCallID: "id1", ToolName: "a", ToolArgs: "{"}
	ch <- llm.Chunk{ToolCallID: "id2", ToolName: "b", ToolArgs: "{\"k\":1}"}
	ch <- llm.Chunk{ToolCallID: "id1", ToolName: "a", ToolArgs: "}"}
	ch <- llm.Chunk{Done: true}
	close(ch)

	msg, err := aggregateStream(context.Background(), ch)
	require.NoError(t, err)
	assert.Equal(t, "assistant", msg.Role)
	assert.Equal(t, "hello", msg.Content)
	require.Len(t, msg.ToolCalls, 2)
	// id1 arrived first.
	assert.Equal(t, "id1", msg.ToolCalls[0].ID)
	assert.Equal(t, "a", msg.ToolCalls[0].Name)
	assert.Equal(t, "{}", msg.ToolCalls[0].Arguments)
	assert.Equal(t, "id2", msg.ToolCalls[1].ID)
	assert.Equal(t, `{"k":1}`, msg.ToolCalls[1].Arguments)
}

func TestAggregateStream_propagatesChunkErr(t *testing.T) {
	ch := make(chan llm.Chunk, 2)
	sentinel := errors.New("stream blew up")
	ch <- llm.Chunk{Done: true, Err: sentinel}
	close(ch)

	_, err := aggregateStream(context.Background(), ch)
	require.ErrorIs(t, err, sentinel)
}

// TestAggregateStream_ClosedChannelWithCancelledCtx ensures ctx cancellation
// is not masked by a producer that closed the channel without emitting a
// terminal chunk. Go's select is non-deterministic when both arms are ready,
// so without a ctx.Err() check on the !ok branch the aggregator occasionally
// returned a clean empty message despite the context being cancelled.
func TestAggregateStream_ClosedChannelWithCancelledCtx(t *testing.T) {
	ch := make(chan llm.Chunk)
	close(ch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // both arms of the select will be ready on first iteration.

	// Run many trials so we exercise the non-determinism of the select.
	for i := 0; i < 200; i++ {
		_, err := aggregateStream(ctx, ch)
		require.Error(t, err, "iteration %d: cancelled ctx must not be masked by closed channel", i)
		require.ErrorIs(t, err, context.Canceled)
	}
}

// TestRun_aggregateStream_raceWithClosedChannel is an end-to-end variant: a
// pre-cancelled ctx passed through Run should surface context.Canceled even
// when the fake producer closes its channel without emitting a terminal
// chunk.
func TestRun_aggregateStream_raceWithClosedChannel(t *testing.T) {
	fc := newFakeClient()
	// Empty chunks slice means the goroutine immediately closes the
	// channel after starting, without ever sending a Done.
	fc.scriptStream(fakeStreamStep{chunks: nil})

	a, err := NewBuilder("m").Client(fc).Streaming(true).Build(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, runErr := a.Run(ctx, []middleware.Message{{Role: "user", Content: "q"}})
	require.Error(t, runErr)
	assert.True(t, errors.Is(runErr, context.Canceled), "expected context.Canceled, got %v", runErr)
}
