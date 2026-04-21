package openai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/llm"
	openaiprov "agent-harness/go/llm/openai"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// roundTripFunc lets us supply a fake http.RoundTripper from a simple
// function, adapting the request-shaped callback into the interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// recordingTransport remembers the bytes that were sent for later inspection.
type recordingTransport struct {
	mu       sync.Mutex
	lastBody []byte
	lastURL  string
	fn       roundTripFunc
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.lastBody = body
	r.lastURL = req.URL.String()
	r.mu.Unlock()
	// Replace the consumed body so downstream has something to read if it
	// ever reads it again (the fn may ignore it).
	req.Body = io.NopCloser(bytes.NewReader(body))
	return r.fn(req)
}

// jsonResponse is a convenience helper producing a standard OpenAI
// non-streaming JSON response body.
func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sseResponse packages a set of SSE data chunks as an OpenAI streaming body,
// including the terminal "[DONE]" sentinel.
func sseResponse(chunks []string) *http.Response {
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

func newTestClient(rt http.RoundTripper) *openaiprov.Client {
	hc := &http.Client{Transport: rt}
	return openaiprov.New(
		openaiprov.WithAPIKey("test"),
		openaiprov.WithBaseURL("http://mock.invalid"),
		openaiprov.WithHTTPClient(hc),
	)
}

// ------------------------------- Complete ----------------------------------

func TestComplete_PlainText(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"id": "cmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "gpt-4o-mini",
			"choices": [{
				"index": 0,
				"finish_reason": "stop",
				"message": {"role": "assistant", "content": "hello there"}
			}]
		}`), nil
	})

	client := newTestClient(rt)
	resp, err := client.Complete(context.Background(), llm.Request{
		Model: "gpt-4o-mini",
		Messages: []middleware.Message{
			{Role: "user", Content: "hi"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "assistant", resp.Message.Role)
	assert.Equal(t, "hello there", resp.Message.Content)
	assert.Empty(t, resp.Message.ToolCalls)
}

func TestComplete_ToolCalls(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"id": "cmpl-2",
			"object": "chat.completion",
			"created": 1700000001,
			"model": "gpt-4o-mini",
			"choices": [{
				"index": 0,
				"finish_reason": "tool_calls",
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [
						{"id": "call_1", "type": "function", "function": {"name": "search", "arguments": "{\"q\":\"x\"}"}},
						{"id": "call_2", "type": "function", "function": {"name": "fetch", "arguments": "{\"u\":\"y\"}"}}
					]
				}
			}]
		}`), nil
	})

	client := newTestClient(rt)
	resp, err := client.Complete(context.Background(), llm.Request{
		Model:    "gpt-4o-mini",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Message.ToolCalls, 2)
	assert.Equal(t, "call_1", resp.Message.ToolCalls[0].ID)
	assert.Equal(t, "search", resp.Message.ToolCalls[0].Name)
	assert.Equal(t, `{"q":"x"}`, resp.Message.ToolCalls[0].Arguments)
	assert.Equal(t, "call_2", resp.Message.ToolCalls[1].ID)
	assert.Equal(t, "fetch", resp.Message.ToolCalls[1].Name)
}

func TestComplete_HTTPError(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"boom"}}`)),
		}, nil
	})

	client := openaiprov.New(
		openaiprov.WithAPIKey("test"),
		openaiprov.WithBaseURL("http://mock.invalid"),
		openaiprov.WithHTTPClient(&http.Client{Transport: rt}),
		// Keep retries off in the underlying SDK so the test is quick.
		openaiprov.WithRequestOptions(),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "gpt-4o-mini",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
}

func TestComplete_RequestBodyShape(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"id":"x","object":"chat.completion","created":0,"model":"gpt-4o-mini","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`), nil
		},
	}

	client := newTestClient(rec)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:       "gpt-4o-mini",
		Temperature: 0.5,
		MaxTokens:   256,
		Messages: []middleware.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []middleware.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}},
			{Role: "tool", ToolCallID: "c1", Content: `{"ok":true}`},
		},
		Tools: []tools.Def{{
			Name:        "echo",
			Description: "echoes back",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"x"},
				"properties": map[string]any{
					"x": map[string]any{"type": "integer"},
				},
			},
		}},
	})
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.lastBody, &body))
	assert.Equal(t, "gpt-4o-mini", body["model"])
	assert.InDelta(t, 0.5, body["temperature"], 0.0001)
	assert.EqualValues(t, 256, body["max_completion_tokens"])

	msgs, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 4)
	assert.Equal(t, "system", msgs[0].(map[string]any)["role"])
	assert.Equal(t, "user", msgs[1].(map[string]any)["role"])
	asst := msgs[2].(map[string]any)
	assert.Equal(t, "assistant", asst["role"])
	tcs := asst["tool_calls"].([]any)
	require.Len(t, tcs, 1)
	tc0 := tcs[0].(map[string]any)
	assert.Equal(t, "c1", tc0["id"])
	assert.Equal(t, "echo", tc0["function"].(map[string]any)["name"])
	assert.Equal(t, `{"x":1}`, tc0["function"].(map[string]any)["arguments"])
	toolMsg := msgs[3].(map[string]any)
	assert.Equal(t, "tool", toolMsg["role"])
	assert.Equal(t, "c1", toolMsg["tool_call_id"])

	toolsField, ok := body["tools"].([]any)
	require.True(t, ok, "tools array present in body")
	require.Len(t, toolsField, 1)
	t0 := toolsField[0].(map[string]any)
	assert.Equal(t, "function", t0["type"])
	fn := t0["function"].(map[string]any)
	assert.Equal(t, "echo", fn["name"])
	assert.Equal(t, "echoes back", fn["description"])
	params := fn["parameters"].(map[string]any)
	assert.Equal(t, "object", params["type"])
}

func TestWithBaseURL_SetsEndpoint(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`), nil
		},
	}
	hc := &http.Client{Transport: rec}

	client := openaiprov.New(
		openaiprov.WithAPIKey("test"),
		openaiprov.WithBaseURL("http://custom.example/api/v99/"),
		openaiprov.WithHTTPClient(hc),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(rec.lastURL, "http://custom.example/api/v99/"),
		"expected baseURL to be honoured, got %q", rec.lastURL)
}

func TestWithAPIKey_SendsAuthHeader(t *testing.T) {
	var gotAuth string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		return jsonResponse(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`), nil
	})

	client := openaiprov.New(
		openaiprov.WithAPIKey("sk-test-123"),
		openaiprov.WithBaseURL("http://mock.invalid"),
		openaiprov.WithHTTPClient(&http.Client{Transport: rt}),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test-123", gotAuth)
}

// -------------------------------- Stream -----------------------------------

// collectChunks drains a channel with a per-chunk timeout, returning the
// collected chunks and the terminal one.  Any time-out surfaces a test
// failure.
func collectChunks(t *testing.T, ch <-chan llm.Chunk, max time.Duration) []llm.Chunk {
	t.Helper()
	var out []llm.Chunk
	timeout := time.After(max)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
			if c.Done {
				// Drain any straggler until close.
				for range ch {
				}
				return out
			}
		case <-timeout:
			t.Fatalf("timed out waiting for stream chunks (have %d so far)", len(out))
			return out
		}
	}
}

func TestStream_ContentOnly(t *testing.T) {
	chunks := []string{
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"lo "}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"world"}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return sseResponse(chunks), nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	got := collectChunks(t, ch, time.Second)
	require.GreaterOrEqual(t, len(got), 4)

	// Content deltas should appear in order.
	var content strings.Builder
	var sawDone bool
	for _, c := range got {
		if c.Content != "" {
			content.WriteString(c.Content)
		}
		if c.Done {
			sawDone = true
			assert.NoError(t, c.Err)
		}
	}
	assert.Equal(t, "hello world", content.String())
	assert.True(t, sawDone, "terminal Done chunk expected")
}

func TestStream_ToolCallAggregation(t *testing.T) {
	// Model sends:
	//   1. Tool-call arrives with id + name fragment "sea"
	//   2. Name continues with "rch"
	//   3. Arguments stream: `{"q":"`
	//   4. Arguments continue: `hello"}`
	//   5. Final finish chunk
	chunks := []string{
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","function":{"name":"sea","arguments":""}}]}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"rch"}}]}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\""}}]}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"hello\"}"}}]}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return sseResponse(chunks), nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	got := collectChunks(t, ch, time.Second)

	// Filter tool-call-carrying chunks.
	var toolChunks []llm.Chunk
	var doneChunk *llm.Chunk
	for i := range got {
		c := got[i]
		if c.ToolCallID != "" || c.ToolName != "" || c.ToolArgs != "" {
			toolChunks = append(toolChunks, c)
		}
		if c.Done {
			dc := c
			doneChunk = &dc
		}
	}
	require.Len(t, toolChunks, 4, "one chunk per tool-call delta")

	// After the first delta, every subsequent chunk should carry the
	// accumulated id + accumulated name.
	for _, c := range toolChunks {
		assert.Equal(t, "call_abc", c.ToolCallID)
	}
	assert.Equal(t, "sea", toolChunks[0].ToolName)
	assert.Equal(t, "search", toolChunks[1].ToolName)
	assert.Equal(t, "search", toolChunks[2].ToolName)
	assert.Equal(t, "search", toolChunks[3].ToolName)

	// Each arg delta surfaces exactly as received.
	assert.Equal(t, "", toolChunks[0].ToolArgs)
	assert.Equal(t, "", toolChunks[1].ToolArgs)
	assert.Equal(t, `{"q":"`, toolChunks[2].ToolArgs)
	assert.Equal(t, `hello"}`, toolChunks[3].ToolArgs)

	require.NotNil(t, doneChunk)
	assert.NoError(t, doneChunk.Err)
}

func TestStream_HTTPErrorSurfacesAsTerminalChunkOrReturn(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"boom"}}`)),
		}, nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	// The openai-go streaming API returns the stream with a deferred error —
	// so either Stream returned an error immediately (acceptable) or the
	// first chunk we see is Done+Err.
	if err != nil {
		return
	}
	require.NotNil(t, ch)
	got := collectChunks(t, ch, time.Second)
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.True(t, last.Done)
	assert.Error(t, last.Err)
}

// slowBody emits bytes slowly so the stream consumer is guaranteed to be
// mid-iteration when the surrounding context is cancelled.  It honours ctx
// for correct goroutine shut-down.
type slowBody struct {
	chunks  []string
	i       int
	delay   time.Duration
	ctx     context.Context
	pending *bytes.Reader
}

func (s *slowBody) Read(p []byte) (int, error) {
	for {
		if s.pending != nil && s.pending.Len() > 0 {
			return s.pending.Read(p)
		}
		if s.i >= len(s.chunks) {
			return 0, io.EOF
		}
		// Wait before emitting the next chunk, but return on ctx cancel.
		select {
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		case <-time.After(s.delay):
		}
		raw := "data: " + s.chunks[s.i] + "\n\n"
		s.pending = bytes.NewReader([]byte(raw))
		s.i++
	}
}

func (s *slowBody) Close() error { return nil }

func TestStream_ContextCancellation(t *testing.T) {
	chunks := []string{
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"one "}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"two "}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"three "}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"four"}}]}`,
		`{"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &slowBody{chunks: chunks, delay: 50 * time.Millisecond, ctx: ctx},
		}, nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(ctx, llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	// Drain one chunk then cancel.
	select {
	case c, ok := <-ch:
		require.True(t, ok, "expected at least one chunk before cancellation")
		assert.NotEmpty(t, c.Content)
	case <-time.After(time.Second):
		t.Fatal("did not receive first content chunk in time")
	}
	cancel()

	// After cancel the channel must close in a bounded time; any remaining
	// chunks are fine, but the channel must be closed.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed as required
			}
		case <-deadline:
			t.Fatal("channel did not close after ctx cancellation")
		}
	}
}

// sanity: make sure Stream respects the ctx error reporting path even if we
// try to cancel before any bytes arrive.
func TestStream_PreCancelledContext(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := newTestClient(rt)
	ch, err := client.Stream(ctx, llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	// Either the initial call surfaces the ctx error, or the stream opens
	// and emits a terminal Err chunk.  Both are acceptable.
	if err != nil {
		assert.True(t, errors.Is(err, context.Canceled))
		return
	}
	require.NotNil(t, ch)
	got := collectChunks(t, ch, time.Second)
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.True(t, last.Done)
}

func TestNew_NoAPIKey_FallsBackToEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-key")

	var gotAuth string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		return jsonResponse(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`), nil
	})

	// Note: no WithAPIKey here.
	client := openaiprov.New(
		openaiprov.WithBaseURL("http://mock.invalid"),
		openaiprov.WithHTTPClient(&http.Client{Transport: rt}),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-env-key", gotAuth)
}
