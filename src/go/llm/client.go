// Package llm defines the provider-neutral Client interface for chat completions,
// along with the Request/Response/Chunk types used throughout the harness.
//
// Concrete provider implementations (OpenAI, Anthropic, …) live in separate
// packages and satisfy the Client interface defined here.
package llm

import (
	"context"

	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// Re-export the shared message types as aliases so callers only need to import
// this package.
type Message = middleware.Message   // role = "system" | "user" | "assistant" | "tool"
type ToolCall = middleware.ToolCall // ID, Name, Arguments

// Request describes a single chat-completion invocation.
type Request struct {
	// Model is the model identifier string (e.g. "gpt-4o", "claude-3-5-sonnet-20241022").
	Model string

	// Messages is the conversation history to send.
	Messages []Message

	// Tools is the optional list of tool definitions to include in the call.
	// If empty, no tools are advertised to the model.
	Tools []tools.Def

	// Temperature controls sampling randomness.  Zero means use the provider
	// default.
	Temperature float64

	// MaxTokens caps the completion length.  Zero means use the provider default.
	MaxTokens int

	// Extra carries provider-specific options as a pass-through map,
	// equivalent to Python's **litellm_kwargs.
	Extra map[string]any
}

// Response is the aggregated, non-streaming result of a chat completion.
type Response struct {
	// Message is the assistant turn.  role is always "assistant"; ToolCalls
	// may be non-empty.
	Message Message
}

// Chunk is a single item delivered on the streaming channel returned by
// Client.Stream.  Exactly one semantic field should be meaningful per chunk:
//
//   - Content      — next text delta
//   - ToolCallID   — chunk belongs to a (possibly new) tool call
//   - Done=true    — terminal chunk; Err may be non-nil
//
// All fields except Done/Err are additive deltas to be accumulated by the
// consumer.
type Chunk struct {
	// Content is the next incremental text fragment (may be empty).
	Content string

	// ToolCallID is non-empty when this chunk contributes to a tool call.
	ToolCallID string

	// ToolName is the accumulated name delta for the tool call above.
	ToolName string

	// ToolArgs is the next delta of tool-arguments JSON.
	ToolArgs string

	// Done is true only on the terminal chunk.  When Done is true, Err may
	// contain the stream error (in-stream failure) or nil (clean end).
	Done bool

	// Err carries an error on the terminal chunk; Done will also be true.
	Err error
}

// Client is the provider contract for chat completions.
//
// Producers (providers) own the channel returned by Stream and must close it
// exactly once.  Consumers range over the channel and must never close it.
type Client interface {
	// Stream issues a streaming chat completion.  A non-nil error return means
	// the stream could not be opened (authentication failure, transport error,
	// etc.).  In-stream errors arrive as a terminal Chunk{Done: true, Err: err}
	// on the returned channel.
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)

	// Complete issues a non-streaming chat completion and returns the fully
	// aggregated response.
	Complete(ctx context.Context, req Request) (Response, error)
}
