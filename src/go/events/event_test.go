package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamConfig_Defaults(t *testing.T) {
	sc := StreamConfig{}
	assert.Equal(t, "", sc.Mode, "zero-value Mode should be empty (caller interprets as buffered)")
	assert.Nil(t, sc.StreamFields)
}

func TestStreamConfig_Streaming(t *testing.T) {
	sc := StreamConfig{Mode: "streaming", StreamFields: []string{"content", "block.text"}}
	assert.Equal(t, "streaming", sc.Mode)
	require.Len(t, sc.StreamFields, 2)
	assert.Equal(t, "content", sc.StreamFields[0])
	assert.Equal(t, "block.text", sc.StreamFields[1])
}

func TestEventType_LiteralConstruction(t *testing.T) {
	et := EventType{
		Name:        "tool_call",
		Description: "LLM wants to call a tool",
		Schema: map[string]any{
			"tool": "string",
			"args": map[string]any{"input": "string"},
		},
		Instructions: "Emit when calling a tool",
		Streaming:    StreamConfig{Mode: "buffered"},
	}

	assert.Equal(t, "tool_call", et.Name)
	assert.Equal(t, "LLM wants to call a tool", et.Description)
	assert.Equal(t, "buffered", et.Streaming.Mode)

	schema := et.Schema
	require.NotNil(t, schema)
	assert.Equal(t, "string", schema["tool"])

	nested, ok := schema["args"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", nested["input"])
}

func TestEventType_EmptyStreaming(t *testing.T) {
	et := EventType{
		Name:   "ping",
		Schema: map[string]any{},
	}
	// StreamConfig zero value
	assert.Equal(t, "", et.Streaming.Mode)
	assert.Nil(t, et.Streaming.StreamFields)
}

func TestParsedEvent_Shape(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "hello"
	close(ch)

	pe := ParsedEvent{
		Type:   "output",
		Data:   map[string]any{"content": "hello"},
		Stream: ch,
		Raw:    "type: output\ncontent: hello",
	}

	assert.Equal(t, "output", pe.Type)
	assert.Equal(t, "hello", pe.Data["content"])
	assert.Equal(t, "hello world", func() string {
		// Just verify Stream is non-nil and readable
		_ = <-pe.Stream
		return "hello world"
	}())
	assert.Equal(t, "type: output\ncontent: hello", pe.Raw)
}

func TestParsedEvent_NilStream_BufferedEvent(t *testing.T) {
	pe := ParsedEvent{
		Type: "status",
		Data: map[string]any{"state": "idle"},
		Raw:  "type: status\nstate: idle",
	}
	assert.Nil(t, pe.Stream, "buffered event should have nil Stream")
	assert.Equal(t, "status", pe.Type)
}
