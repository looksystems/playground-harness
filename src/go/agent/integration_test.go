package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/agent"
	"agent-harness/go/hooks"
	"agent-harness/go/llm/openai"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// This file is the agent-level HTTP integration test that was missing from
// the original M1 drop. Every other agent test uses a fake llm.Client; here
// we wire a real openai.New(...) provider up to a mock http.RoundTripper and
// run a two-turn conversation end-to-end. That exercises the full stack —
// request building, streaming/non-streaming dispatch, response parsing, tool
// execution, hook invocation — against the same wire shape a real endpoint
// produces.

// scriptedTransport is a programmable http.RoundTripper that records request
// bodies and replies with scripted responses keyed by call index. It is safe
// for concurrent access because the openai-go client does not parallelise
// chat completion calls.
type scriptedTransport struct {
	mu       sync.Mutex
	calls    int
	bodies   [][]byte
	urls     []string
	replies  []func() *http.Response
	lastErr  error
	observed []map[string]any
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, _ := io.ReadAll(req.Body)
	s.bodies = append(s.bodies, body)
	s.urls = append(s.urls, req.URL.String())

	// Parse and record the request JSON so assertions can inspect the
	// messages/tools that were actually sent.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		s.observed = append(s.observed, parsed)
	} else {
		s.observed = append(s.observed, nil)
	}

	if s.calls >= len(s.replies) {
		s.lastErr = fmt.Errorf("scriptedTransport: unexpected call #%d (script length %d)", s.calls+1, len(s.replies))
		return nil, s.lastErr
	}
	replyFn := s.replies[s.calls]
	s.calls++

	// Restore body for any observer that looks at req.Body downstream.
	req.Body = io.NopCloser(bytes.NewReader(body))
	return replyFn(), nil
}

// jsonResp produces a ready-made OpenAI-shaped non-streaming JSON response
// with the given body.
func jsonResp(body string) func() *http.Response {
	return func() *http.Response {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		}
	}
}

// sseResp packages a list of SSE chunks (already JSON-encoded) as an OpenAI
// streaming body with the terminal [DONE] marker.
func sseResp(chunks []string) func() *http.Response {
	return func() *http.Response {
		var buf bytes.Buffer
		for _, c := range chunks {
			fmt.Fprintf(&buf, "data: %s\n\n", c)
		}
		buf.WriteString("data: [DONE]\n\n")
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(&buf),
		}
	}
}

// TestAgent_HTTPIntegration_NonStreaming drives a two-turn conversation where
// the first LLM response asks for a tool call and the second returns a plain
// assistant reply. It verifies:
//
//   - both HTTP requests are made against the configured base URL
//   - the tool is executed and its result is carried into the second request
//   - the final assistant content is returned by Run
//   - RunStart/LLMRequest/ToolCall/ToolResult/LLMResponse/RunEnd hooks fire
func TestAgent_HTTPIntegration_NonStreaming(t *testing.T) {
	transport := &scriptedTransport{
		replies: []func() *http.Response{
			// Turn 1: assistant emits a tool_call to add(2, 40).
			jsonResp(`{
				"id": "cmpl-1",
				"object": "chat.completion",
				"created": 1700000000,
				"model": "gpt-4o-mini",
				"choices": [{
					"index": 0,
					"finish_reason": "tool_calls",
					"message": {
						"role": "assistant",
						"content": "",
						"tool_calls": [
							{"id": "call_add", "type": "function", "function": {"name": "add", "arguments": "{\"a\":2,\"b\":40}"}}
						]
					}
				}]
			}`),
			// Turn 2: plain reply.
			jsonResp(`{
				"id": "cmpl-2",
				"object": "chat.completion",
				"created": 1700000001,
				"model": "gpt-4o-mini",
				"choices": [{
					"index": 0,
					"finish_reason": "stop",
					"message": {"role": "assistant", "content": "the sum is 42"}
				}]
			}`),
		},
	}
	httpClient := &http.Client{Transport: transport}

	client := openai.New(
		openai.WithAPIKey("test-key"),
		openai.WithBaseURL("http://mock.invalid/v1/"),
		openai.WithHTTPClient(httpClient),
	)

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var toolRan atomic.Int32
	addTool := tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		toolRan.Add(1)
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description("add two ints"))

	a, err := agent.NewBuilder("gpt-4o-mini").
		Client(client).
		Tool(addTool).
		Streaming(false).
		Build(context.Background())
	require.NoError(t, err)

	// Record hook invocations to prove the end-to-end wiring fires.
	var gotEvents []hooks.Event
	var evMu sync.Mutex
	track := func(e hooks.Event) hooks.Handler {
		return func(_ context.Context, _ ...any) {
			evMu.Lock()
			gotEvents = append(gotEvents, e)
			evMu.Unlock()
		}
	}
	for _, e := range []hooks.Event{hooks.RunStart, hooks.LLMRequest, hooks.ToolCall, hooks.ToolResult, hooks.LLMResponse, hooks.RunEnd} {
		a.On(e, track(e))
	}

	out, runErr := a.Run(context.Background(), []middleware.Message{
		{Role: "user", Content: "what is 2 + 40?"},
	})
	require.NoError(t, runErr)
	assert.Equal(t, "the sum is 42", out)
	assert.EqualValues(t, 1, toolRan.Load(), "tool must have been executed exactly once")

	// Two HTTP calls, both against the configured baseURL.
	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Equal(t, 2, transport.calls)
	for _, u := range transport.urls {
		assert.Contains(t, u, "mock.invalid", "every request must go to the configured baseURL")
	}

	// Request #1 must include the tool definition.
	req1 := transport.observed[0]
	require.NotNil(t, req1, "first request body must parse as JSON")
	toolsField, ok := req1["tools"].([]any)
	require.True(t, ok, "first request must advertise the tool")
	require.Len(t, toolsField, 1)
	fn := toolsField[0].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, "add", fn["name"])

	// Request #2 must contain the tool message with the executed result.
	req2 := transport.observed[1]
	require.NotNil(t, req2, "second request body must parse as JSON")
	msgs, ok := req2["messages"].([]any)
	require.True(t, ok)
	var toolMsg map[string]any
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "tool" {
			toolMsg = mm
			break
		}
	}
	require.NotNil(t, toolMsg, "second request must carry the tool response message")
	assert.Equal(t, "call_add", toolMsg["tool_call_id"])
	assert.Equal(t, "42", toolMsg["content"])

	// Hook ordering (each Emit waits, so events are sequenced by Run).
	// Expected sequence, independent of hook concurrency within a single
	// Emit: RunStart, LLMRequest(1), ToolCall, ToolResult, LLMRequest(2),
	// LLMResponse(2), RunEnd. The first LLMResponse fires between LLMRequest
	// and ToolCall (post turn-1 response); include it in the assertion so
	// the ordering is unambiguous.
	evMu.Lock()
	defer evMu.Unlock()
	assert.Equal(t, []hooks.Event{
		hooks.RunStart,
		hooks.LLMRequest,
		hooks.LLMResponse,
		hooks.ToolCall,
		hooks.ToolResult,
		hooks.LLMRequest,
		hooks.LLMResponse,
		hooks.RunEnd,
	}, gotEvents)
}

// TestAgent_HTTPIntegration_Streaming runs the same two-turn conversation
// against the streaming endpoint. The request shape, response parsing, and
// tool execution must all work end-to-end via the SSE code path.
func TestAgent_HTTPIntegration_Streaming(t *testing.T) {
	// Turn 1 SSE: streamed tool-call emission. The provider splits name/id
	// and arguments across multiple chunks.
	turn1 := []string{
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_add","function":{"name":"add","arguments":""}}]}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":2,\"b\":40}"}}]}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	// Turn 2 SSE: plain textual reply streamed as deltas.
	turn2 := []string{
		`{"id":"c2","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"the sum "}}]}`,
		`{"id":"c2","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"is 42"}}]}`,
		`{"id":"c2","object":"chat.completion.chunk","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}

	transport := &scriptedTransport{
		replies: []func() *http.Response{sseResp(turn1), sseResp(turn2)},
	}
	httpClient := &http.Client{Transport: transport}

	client := openai.New(
		openai.WithAPIKey("test-key"),
		openai.WithBaseURL("http://mock.invalid/v1/"),
		openai.WithHTTPClient(httpClient),
	)

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var toolRan atomic.Int32
	addTool := tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		toolRan.Add(1)
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description("add two ints"))

	a, err := agent.NewBuilder("gpt-4o-mini").
		Client(client).
		Tool(addTool).
		Streaming(true).
		Build(context.Background())
	require.NoError(t, err)

	var toolResultCount atomic.Int32
	a.On(hooks.ToolResult, func(_ context.Context, _ ...any) {
		toolResultCount.Add(1)
	})

	out, runErr := a.Run(context.Background(), []middleware.Message{
		{Role: "user", Content: "what is 2 + 40?"},
	})
	require.NoError(t, runErr)
	assert.Equal(t, "the sum is 42", out)
	assert.EqualValues(t, 1, toolRan.Load())
	assert.EqualValues(t, 1, toolResultCount.Load())

	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Equal(t, 2, transport.calls, "streaming path must still take two turns")

	// Both requests should have asked for a streaming response.
	req1 := transport.observed[0]
	require.NotNil(t, req1)
	assert.Equal(t, true, req1["stream"], "streaming flag must be set on the first request")
	req2 := transport.observed[1]
	require.NotNil(t, req2)
	assert.Equal(t, true, req2["stream"], "streaming flag must be set on the second request")

	// Second request must carry the tool result message.
	msgs, ok := req2["messages"].([]any)
	require.True(t, ok)
	var toolMsg map[string]any
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "tool" {
			toolMsg = mm
			break
		}
	}
	require.NotNil(t, toolMsg)
	assert.Equal(t, "call_add", toolMsg["tool_call_id"])
	assert.Equal(t, "42", toolMsg["content"])
}
