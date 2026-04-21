package anthropic_test

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
	anthropicprov "agent-harness/go/llm/anthropic"
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
	req.Body = io.NopCloser(bytes.NewReader(body))
	return r.fn(req)
}

// jsonResponse is a convenience helper producing a standard Anthropic
// non-streaming JSON response body.
func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sseEvent is a single named SSE event.
type sseEvent struct {
	Name string
	Data string
}

// sseResponse packages a set of Anthropic streaming events as an HTTP response
// body.  Each event is emitted as `event: <name>\ndata: <payload>\n\n`.
func sseResponse(events []sseEvent) *http.Response {
	var buf bytes.Buffer
	for _, e := range events {
		fmt.Fprintf(&buf, "event: %s\ndata: %s\n\n", e.Name, e.Data)
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(&buf),
	}
}

func newTestClient(rt http.RoundTripper) *anthropicprov.Client {
	hc := &http.Client{Transport: rt}
	return anthropicprov.New(
		anthropicprov.WithAPIKey("test"),
		anthropicprov.WithBaseURL("http://mock.invalid"),
		anthropicprov.WithHTTPClient(hc),
	)
}

// ------------------------------- Complete ----------------------------------

func TestComplete_PlainText(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-sonnet-20241022",
			"content": [{"type": "text", "text": "hello there"}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 5, "output_tokens": 3}
		}`), nil
	})

	client := newTestClient(rt)
	resp, err := client.Complete(context.Background(), llm.Request{
		Model: "claude-3-5-sonnet-20241022",
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
			"id": "msg_2",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-sonnet-20241022",
			"content": [
				{"type": "text", "text": "calling tools"},
				{"type": "tool_use", "id": "toolu_1", "name": "search", "input": {"q": "x"}},
				{"type": "tool_use", "id": "toolu_2", "name": "fetch", "input": {"u": "y"}}
			],
			"stop_reason": "tool_use",
			"stop_sequence": null,
			"usage": {"input_tokens": 5, "output_tokens": 3}
		}`), nil
	})

	client := newTestClient(rt)
	resp, err := client.Complete(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "calling tools", resp.Message.Content)
	require.Len(t, resp.Message.ToolCalls, 2)
	assert.Equal(t, "toolu_1", resp.Message.ToolCalls[0].ID)
	assert.Equal(t, "search", resp.Message.ToolCalls[0].Name)
	// Arguments are re-serialised JSON; compare structurally to avoid
	// depending on key-order produced by the SDK's unmarshal/marshal cycle.
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Message.ToolCalls[0].Arguments), &got))
	assert.Equal(t, map[string]any{"q": "x"}, got)
	assert.Equal(t, "toolu_2", resp.Message.ToolCalls[1].ID)
	assert.Equal(t, "fetch", resp.Message.ToolCalls[1].Name)
}

func TestComplete_HTTPError(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"boom"}}`)),
		}, nil
	})

	client := newTestClient(rt)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
}

func TestComplete_RequestBodyShape(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": "ok"}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}

	client := newTestClient(rec)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:       "claude-3-5-sonnet-20241022",
		Temperature: 0.5,
		MaxTokens:   256,
		Messages: []middleware.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []middleware.ToolCall{{ID: "toolu_1", Name: "echo", Arguments: `{"x":1}`}}},
			{Role: "tool", ToolCallID: "toolu_1", Content: `{"ok":true}`},
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
	assert.Equal(t, "claude-3-5-sonnet-20241022", body["model"])
	assert.InDelta(t, 0.5, body["temperature"], 0.0001)
	assert.EqualValues(t, 256, body["max_tokens"])

	// System messages must land in the top-level "system" field, NOT in
	// the messages array.
	sys, ok := body["system"].([]any)
	require.True(t, ok, "expected top-level system array, got %T", body["system"])
	require.Len(t, sys, 1)
	assert.Equal(t, "sys", sys[0].(map[string]any)["text"])

	msgs, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 3, "system message should be extracted; user + assistant + tool-as-user remain")

	// [0] = user
	user0 := msgs[0].(map[string]any)
	assert.Equal(t, "user", user0["role"])
	user0Content := user0["content"].([]any)
	require.Len(t, user0Content, 1)
	assert.Equal(t, "text", user0Content[0].(map[string]any)["type"])
	assert.Equal(t, "hi", user0Content[0].(map[string]any)["text"])

	// [1] = assistant with tool_use block
	asst := msgs[1].(map[string]any)
	assert.Equal(t, "assistant", asst["role"])
	asstContent := asst["content"].([]any)
	require.Len(t, asstContent, 1)
	tu := asstContent[0].(map[string]any)
	assert.Equal(t, "tool_use", tu["type"])
	assert.Equal(t, "toolu_1", tu["id"])
	assert.Equal(t, "echo", tu["name"])
	assert.Equal(t, map[string]any{"x": float64(1)}, tu["input"])

	// [2] = user with tool_result block
	user1 := msgs[2].(map[string]any)
	assert.Equal(t, "user", user1["role"])
	user1Content := user1["content"].([]any)
	require.Len(t, user1Content, 1)
	tr := user1Content[0].(map[string]any)
	assert.Equal(t, "tool_result", tr["type"])
	assert.Equal(t, "toolu_1", tr["tool_use_id"])

	// Tools schema should come through under the Anthropic shape.
	toolsField, ok := body["tools"].([]any)
	require.True(t, ok, "tools array present in body")
	require.Len(t, toolsField, 1)
	t0 := toolsField[0].(map[string]any)
	assert.Equal(t, "echo", t0["name"])
	assert.Equal(t, "echoes back", t0["description"])
	schema := t0["input_schema"].(map[string]any)
	assert.Equal(t, "object", schema["type"])
	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "x")
}

func TestComplete_SystemMessageExtraction(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": "ok"}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}

	client := newTestClient(rec)
	_, err := client.Complete(context.Background(), llm.Request{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hi"},
		},
	})
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.lastBody, &body))

	// Top-level system field carries the system prompt.
	sys, ok := body["system"].([]any)
	require.True(t, ok, "expected top-level system array, got %T", body["system"])
	require.Len(t, sys, 1)
	assert.Equal(t, "you are helpful", sys[0].(map[string]any)["text"])

	// Messages array must not contain a system entry.
	msgs := body["messages"].([]any)
	require.Len(t, msgs, 1)
	assert.Equal(t, "user", msgs[0].(map[string]any)["role"])
}

func TestComplete_ToolResultMapping(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": "ok"}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}

	client := newTestClient(rec)
	_, err := client.Complete(context.Background(), llm.Request{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []middleware.ToolCall{{ID: "toolu_1", Name: "echo", Arguments: `{"x":1}`}}},
			{Role: "tool", ToolCallID: "toolu_1", Content: `{"ok":true}`},
		},
	})
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.lastBody, &body))
	msgs := body["messages"].([]any)
	require.Len(t, msgs, 3)

	// Tool result shows up as user-role with a tool_result block.
	last := msgs[2].(map[string]any)
	assert.Equal(t, "user", last["role"])
	content := last["content"].([]any)
	require.Len(t, content, 1)
	tr := content[0].(map[string]any)
	assert.Equal(t, "tool_result", tr["type"])
	assert.Equal(t, "toolu_1", tr["tool_use_id"])
}

func TestComplete_MaxTokensDefault(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": "ok"}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}

	client := newTestClient(rec)
	_, err := client.Complete(context.Background(), llm.Request{
		// Note: no MaxTokens set.
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.lastBody, &body))
	assert.EqualValues(t, 4096, body["max_tokens"], "default max_tokens must be 4096")
}

func TestComplete_WithMaxTokensOption(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": "ok"}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}

	hc := &http.Client{Transport: rec}
	client := anthropicprov.New(
		anthropicprov.WithAPIKey("test"),
		anthropicprov.WithBaseURL("http://mock.invalid"),
		anthropicprov.WithHTTPClient(hc),
		anthropicprov.WithMaxTokens(1024),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.lastBody, &body))
	assert.EqualValues(t, 1024, body["max_tokens"])
}

func TestWithBaseURL_SetsEndpoint(t *testing.T) {
	rec := &recordingTransport{
		fn: func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"id": "x",
				"type": "message",
				"role": "assistant",
				"model": "m",
				"content": [{"type": "text", "text": ""}],
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 1, "output_tokens": 1}
			}`), nil
		},
	}
	hc := &http.Client{Transport: rec}

	client := anthropicprov.New(
		anthropicprov.WithAPIKey("test"),
		anthropicprov.WithBaseURL("http://custom.example/api/v99/"),
		anthropicprov.WithHTTPClient(hc),
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
	var gotHeader string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotHeader = req.Header.Get("X-Api-Key")
		return jsonResponse(`{
			"id": "x",
			"type": "message",
			"role": "assistant",
			"model": "m",
			"content": [{"type": "text", "text": ""}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`), nil
	})

	client := anthropicprov.New(
		anthropicprov.WithAPIKey("sk-ant-test-123"),
		anthropicprov.WithBaseURL("http://mock.invalid"),
		anthropicprov.WithHTTPClient(&http.Client{Transport: rt}),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test-123", gotHeader)
}

func TestNew_NoAPIKey_FallsBackToEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-env-key")

	var gotHeader string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotHeader = req.Header.Get("X-Api-Key")
		return jsonResponse(`{
			"id": "x",
			"type": "message",
			"role": "assistant",
			"model": "m",
			"content": [{"type": "text", "text": ""}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`), nil
	})

	// Note: no WithAPIKey here.
	client := anthropicprov.New(
		anthropicprov.WithBaseURL("http://mock.invalid"),
		anthropicprov.WithHTTPClient(&http.Client{Transport: rt}),
	)
	_, err := client.Complete(context.Background(), llm.Request{
		Model:    "m",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-env-key", gotHeader)
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
	events := []sseEvent{
		{Name: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`},
		{Name: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo "}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`},
		{Name: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`},
		{Name: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`},
		{Name: "message_stop", Data: `{"type":"message_stop"}`},
	}
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return sseResponse(events), nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	got := collectChunks(t, ch, time.Second)
	require.GreaterOrEqual(t, len(got), 4)

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
	// Model streams:
	//   1. Tool-use block starts with id + full name
	//   2. Arguments stream in two partial_json deltas
	//   3. Terminal events
	events := []sseEvent{
		{Name: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`},
		{Name: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_abc","name":"search","input":{}}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\""}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"hello\"}"}}`},
		{Name: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`},
		{Name: "message_delta", Data: `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":3}}`},
		{Name: "message_stop", Data: `{"type":"message_stop"}`},
	}
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return sseResponse(events), nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	got := collectChunks(t, ch, time.Second)

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
	// One chunk for the block-start (announces id+name) plus two arg deltas.
	require.Len(t, toolChunks, 3)

	// Every tool chunk should carry the id + name accumulated from the
	// content_block_start event.
	for _, c := range toolChunks {
		assert.Equal(t, "toolu_abc", c.ToolCallID)
		assert.Equal(t, "search", c.ToolName)
	}

	// First chunk is the block-start announcement: empty args.
	assert.Equal(t, "", toolChunks[0].ToolArgs)
	// Remaining chunks carry the incremental JSON.
	assert.Equal(t, `{"q":"`, toolChunks[1].ToolArgs)
	assert.Equal(t, `hello"}`, toolChunks[2].ToolArgs)

	require.NotNil(t, doneChunk)
	assert.NoError(t, doneChunk.Err)
}

func TestStream_HTTPErrorSurfacesAsTerminalChunkOrReturn(t *testing.T) {
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"boom"}}`)),
		}, nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(context.Background(), llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	// Either the initial call surfaces the error, or the terminal chunk does.
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
	events  []sseEvent
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
		if s.i >= len(s.events) {
			return 0, io.EOF
		}
		select {
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		case <-time.After(s.delay):
		}
		e := s.events[s.i]
		raw := fmt.Sprintf("event: %s\ndata: %s\n\n", e.Name, e.Data)
		s.pending = bytes.NewReader([]byte(raw))
		s.i++
	}
}

func (s *slowBody) Close() error { return nil }

func TestStream_ContextCancellation(t *testing.T) {
	events := []sseEvent{
		{Name: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`},
		{Name: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one "}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"two "}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"three "}}`},
		{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"four"}}`},
		{Name: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`},
		{Name: "message_stop", Data: `{"type":"message_stop"}`},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &slowBody{events: events, delay: 50 * time.Millisecond, ctx: ctx},
		}, nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(ctx, llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	// Drain one content chunk then cancel.  We may see message_start pass
	// by silently (it produces no user-facing chunk), so keep reading until
	// we find one with content.
	deadline := time.After(time.Second)
	sawContent := false
waitContent:
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before any content chunk")
			}
			if c.Content != "" {
				sawContent = true
				break waitContent
			}
			if c.Done {
				t.Fatal("stream ended before any content chunk")
			}
		case <-deadline:
			t.Fatal("did not receive first content chunk in time")
		}
	}
	require.True(t, sawContent)
	cancel()

	// After cancel the channel must close in a bounded time.
	deadline = time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after ctx cancellation")
		}
	}
}

// TestStream_MidStreamCancel_EmitsTerminalErr mirrors the openai provider's
// regression test: a ctx cancel that wins the send-race inside emit() must
// still deliver a terminal chunk carrying ctx.Err().
func TestStream_MidStreamCancel_EmitsTerminalErr(t *testing.T) {
	// Produce many small content deltas so the buffered send channel fills
	// up before the consumer reads.
	var events []sseEvent
	events = append(events, sseEvent{Name: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`})
	events = append(events, sseEvent{Name: "content_block_start", Data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`})
	for i := 0; i < 64; i++ {
		events = append(events, sseEvent{
			Name: "content_block_delta",
			Data: fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, fmt.Sprintf("c%02d ", i)),
		})
	}
	events = append(events, sseEvent{Name: "content_block_stop", Data: `{"type":"content_block_stop","index":0}`})
	events = append(events, sseEvent{Name: "message_stop", Data: `{"type":"message_stop"}`})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &slowBody{events: events, delay: 0, ctx: ctx},
		}, nil
	})

	client := newTestClient(rt)
	ch, err := client.Stream(ctx, llm.Request{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []middleware.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	// Drain one content chunk then wait for the buffer to fill.
	deadline := time.After(time.Second)
waitContent:
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before any content chunk")
			}
			if c.Content != "" {
				break waitContent
			}
			if c.Done {
				t.Fatal("stream ended before any content chunk")
			}
		case <-deadline:
			t.Fatal("did not receive first content chunk in time")
		}
	}
	time.Sleep(30 * time.Millisecond)
	cancel()

	var last llm.Chunk
	sawAny := false
	deadline = time.After(2 * time.Second)
drain:
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				break drain
			}
			sawAny = true
			last = c
		case <-deadline:
			t.Fatal("channel did not close after ctx cancellation")
		}
	}

	require.True(t, sawAny, "expected at least one chunk to be observed")
	assert.True(t, last.Done, "final chunk must be Done=true after mid-stream cancel")
	require.Error(t, last.Err, "final chunk must carry ctx.Err()")
	assert.True(t, errors.Is(last.Err, context.Canceled), "final chunk Err should be context.Canceled, got %v", last.Err)
}
