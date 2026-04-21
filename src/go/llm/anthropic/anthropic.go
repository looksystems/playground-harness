// Package anthropic implements the llm.Client interface using the
// github.com/anthropics/anthropic-sdk-go SDK, targeting Anthropic's native
// Messages API.
//
// Although the public With* options and the two Stream/Complete methods mirror
// the openai provider exactly, Anthropic's wire protocol is meaningfully
// different:
//
//   - System messages live in a top-level `system` field rather than on the
//     conversation array, so buildParams extracts them out of the request.
//   - Role is "user" | "assistant" only; tool-calls become ToolUseBlock content
//     on an assistant turn, and tool-results become a ToolResultBlock wrapped
//     inside a user turn.
//   - Streaming is event-based (message_start, content_block_start/delta/stop,
//     message_delta, message_stop) rather than OpenAI's chunked delta shape.
//     Tool-use arguments arrive via input_json_delta events that we forward as
//     incremental ToolArgs fragments.
//   - max_tokens is required by the API; if the caller does not specify one we
//     fall back to 4096.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// defaultMaxTokens is applied when llm.Request.MaxTokens is zero; Anthropic's
// Messages API requires an explicit max_tokens value.
const defaultMaxTokens = 4096

// Option configures a Client constructed with New.
type Option func(*config)

// config collects the settings gathered by Options before the underlying
// anthropic-sdk-go client is built.
type config struct {
	apiKey       *string
	baseURL      *string
	httpClient   option.HTTPClient
	maxTokens    *int
	extraOptions []option.RequestOption
}

// WithAPIKey sets the API key on the underlying anthropic-sdk-go client.
//
// If this option is not supplied, the ANTHROPIC_API_KEY environment variable
// is used instead (the SDK's DefaultClientOptions also honours this env var;
// we set it explicitly so the behaviour is deterministic regardless of any
// future SDK changes).
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = &key }
}

// WithBaseURL sets the API base URL on the underlying anthropic-sdk-go client.
// Primarily intended for tests or for routing via an Anthropic-compatible
// proxy.
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = &url }
}

// WithHTTPClient injects a custom http.Client.  Primarily intended for tests —
// a fake transport makes it possible to exercise the provider end-to-end
// without hitting the network.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithMaxTokens sets the default max_tokens value to use when the incoming
// llm.Request leaves MaxTokens at zero.  Anthropic requires max_tokens; if
// neither the request nor this option specifies one, the package default of
// 4096 is used.
func WithMaxTokens(n int) Option {
	return func(c *config) { c.maxTokens = &n }
}

// WithRequestOptions plumbs additional anthropic-sdk-go request options
// through to the underlying client.  Kept as a deliberate escape hatch; prefer
// the dedicated With* options above where possible.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(c *config) { c.extraOptions = append(c.extraOptions, opts...) }
}

// Client is the anthropic-backed implementation of llm.Client.
//
// It is safe for concurrent use by multiple goroutines — the underlying
// anthropic-sdk-go client is itself goroutine-safe, and every Stream call
// builds its own accumulator state.
type Client struct {
	inner     sdk.Client
	maxTokens int
}

// New constructs a Client.  If WithAPIKey is not supplied and ANTHROPIC_API_KEY
// is present in the environment, that value is used.  If neither is present
// the client is still returned, but the first call will almost certainly fail
// with an authentication error from the underlying SDK.
func New(opts ...Option) *Client {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	reqOpts := make([]option.RequestOption, 0, 4+len(cfg.extraOptions))

	// The harness owns retry policy (see llm.WithRetry).  Disable the SDK's
	// own retry loop so we do not double-retry on transient failures.
	reqOpts = append(reqOpts, option.WithMaxRetries(0))

	switch {
	case cfg.apiKey != nil:
		reqOpts = append(reqOpts, option.WithAPIKey(*cfg.apiKey))
	default:
		// Explicit env fallback so behaviour is deterministic regardless of
		// what the anthropic-sdk-go default reader happens to do in future
		// versions.
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			reqOpts = append(reqOpts, option.WithAPIKey(key))
		}
	}
	if cfg.baseURL != nil {
		reqOpts = append(reqOpts, option.WithBaseURL(*cfg.baseURL))
	}
	if cfg.httpClient != nil {
		reqOpts = append(reqOpts, option.WithHTTPClient(cfg.httpClient))
	}
	reqOpts = append(reqOpts, cfg.extraOptions...)

	maxTokens := defaultMaxTokens
	if cfg.maxTokens != nil {
		maxTokens = *cfg.maxTokens
	}

	return &Client{
		inner:     sdk.NewClient(reqOpts...),
		maxTokens: maxTokens,
	}
}

// Complete performs a single non-streaming message completion.
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return llm.Response{}, err
	}

	resp, err := c.inner.Messages.New(ctx, params)
	if err != nil {
		return llm.Response{}, err
	}
	if resp == nil {
		return llm.Response{}, fmt.Errorf("anthropic: message response was nil")
	}

	return llm.Response{Message: convertResponseMessage(resp)}, nil
}

// Stream opens a streaming message completion and returns a channel of
// llm.Chunk.  The producer goroutine owns the channel and closes it exactly
// once — after delivering the terminal Chunk{Done: true} (with Err populated
// on error).  Consumers must range over the channel and must not close it.
//
// Context cancellation is honoured in the send loop: if ctx is done while we
// are blocked trying to deliver a chunk, the goroutine emits a terminal
// Chunk{Done: true, Err: ctx.Err()} and exits.
func (c *Client) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	stream := c.inner.Messages.NewStreaming(ctx, params)

	ch := make(chan llm.Chunk, 8)

	go func() {
		defer close(ch)
		defer stream.Close()

		// toolBlocks tracks ToolUseBlock state keyed by the streaming event
		// `index`.  Anthropic streams tool-use in three phases:
		//   1. content_block_start — delivers the block's id + name
		//   2. content_block_delta (input_json_delta) — delivers incremental
		//      argument JSON fragments
		//   3. content_block_stop
		// We accumulate id/name from phase 1 and forward each fragment in
		// phase 2 as a fresh Chunk carrying the accumulated id + name plus
		// the current delta.
		type toolBlock struct {
			ID   string
			Name string
		}
		toolBlocks := make(map[int64]*toolBlock)

		emit := func(chunk llm.Chunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// emitTerminal delivers the terminal chunk.  Unlike emit it does
		// not short-circuit on ctx.Done — a cancelled ctx is the most common
		// reason we need to send a terminal Err chunk, so dropping it would
		// swallow the very signal the consumer needs.  We first attempt a
		// non-blocking send (the buffer is usually free) and only fall back
		// to a blocking send that races against ctx.Done to avoid wedging
		// if the consumer has abandoned the channel entirely.
		emitTerminal := func(chunk llm.Chunk) {
			select {
			case ch <- chunk:
				return
			default:
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
			}
		}

		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "content_block_start":
				// Anthropic only embeds tool id+name on the start event.
				cb := ev.ContentBlock
				if cb.Type == "tool_use" {
					toolBlocks[ev.Index] = &toolBlock{ID: cb.ID, Name: cb.Name}
					// Emit a chunk so that the consumer sees the call
					// exists before args arrive.  ToolArgs is empty on
					// this first chunk.
					if !emit(llm.Chunk{
						ToolCallID: cb.ID,
						ToolName:   cb.Name,
					}) {
						emitTerminal(llm.Chunk{Done: true, Err: ctx.Err()})
						return
					}
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						if !emit(llm.Chunk{Content: ev.Delta.Text}) {
							emitTerminal(llm.Chunk{Done: true, Err: ctx.Err()})
							return
						}
					}
				case "input_json_delta":
					block, ok := toolBlocks[ev.Index]
					if !ok {
						// Arguments for a block we never saw start: treat
						// the block as anonymous and forward the fragment
						// with empty id/name.  This keeps the consumer
						// making progress even on degenerate inputs.
						block = &toolBlock{}
						toolBlocks[ev.Index] = block
					}
					if !emit(llm.Chunk{
						ToolCallID: block.ID,
						ToolName:   block.Name,
						ToolArgs:   ev.Delta.PartialJSON,
					}) {
						emitTerminal(llm.Chunk{Done: true, Err: ctx.Err()})
						return
					}
				}
			case "content_block_stop", "message_start", "message_delta":
				// These carry useful metadata (stop_reason, usage) but the
				// harness' Chunk shape has no home for it today — skip.
			case "message_stop":
				emitTerminal(llm.Chunk{Done: true})
				return
			}
		}

		// stream.Err() returns the first error encountered while iterating;
		// treat it as the stream's terminal failure.  Include ctx errors if
		// we fell out of the loop because the context was cancelled.
		if err := stream.Err(); err != nil {
			emitTerminal(llm.Chunk{Done: true, Err: err})
			return
		}
		if err := ctx.Err(); err != nil {
			emitTerminal(llm.Chunk{Done: true, Err: err})
			return
		}
		emitTerminal(llm.Chunk{Done: true})
	}()

	return ch, nil
}

// buildParams converts an llm.Request into an anthropic-sdk-go
// MessageNewParams value, including the system-message extraction and
// tool-result wrapping required by Anthropic's native API shape.
func (c *Client) buildParams(req llm.Request) (sdk.MessageNewParams, error) {
	system, messages, err := convertMessages(req.Messages)
	if err != nil {
		return sdk.MessageNewParams{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = c.maxTokens
	}

	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(maxTokens),
		Messages:  messages,
	}
	if len(system) > 0 {
		params.System = system
	}
	if req.Temperature != 0 {
		params.Temperature = param.NewOpt(req.Temperature)
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools)
	}
	return params, nil
}

// convertMessages maps the harness Message slice to Anthropic's MessageParam
// slice.  Role=system entries are extracted into the returned []TextBlockParam
// (destined for the top-level `system` field); role=tool entries are wrapped
// in a ToolResultBlock inside a user-role MessageParam.
func convertMessages(msgs []middleware.Message) ([]sdk.TextBlockParam, []sdk.MessageParam, error) {
	var system []sdk.TextBlockParam
	out := make([]sdk.MessageParam, 0, len(msgs))
	for i, m := range msgs {
		switch m.Role {
		case "system":
			if m.Content != "" {
				system = append(system, sdk.TextBlockParam{Text: m.Content})
			}
		case "user":
			out = append(out, sdk.MessageParam{
				Role:    sdk.MessageParamRoleUser,
				Content: []sdk.ContentBlockParamUnion{sdk.NewTextBlock(m.Content)},
			})
		case "assistant":
			blocks := make([]sdk.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				// Anthropic expects the tool input as a deserialised
				// object, not a JSON string; decode the harness'
				// stringified form and forward the typed value.  If
				// arguments are empty use an empty object — the API
				// rejects tool_use blocks without an input field.
				var input any = map[string]any{}
				if tc.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						return nil, nil, fmt.Errorf("anthropic: message %d tool call %q has invalid JSON arguments: %w", i, tc.ID, err)
					}
				}
				blocks = append(blocks, sdk.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			if len(blocks) == 0 {
				// Empty assistant turn would be rejected; skip.
				continue
			}
			out = append(out, sdk.MessageParam{
				Role:    sdk.MessageParamRoleAssistant,
				Content: blocks,
			})
		case "tool":
			if m.ToolCallID == "" {
				return nil, nil, fmt.Errorf("anthropic: message %d has role=tool but empty ToolCallID", i)
			}
			// Anthropic expects tool results to arrive as a user turn
			// containing ToolResultBlocks.  If the previous converted
			// message is already a user turn, append onto its content so
			// consecutive tool-results collapse into a single user turn
			// (Anthropic merges consecutive same-role turns server-side,
			// but keeping the wire shape tidy aids debugging).
			block := sdk.NewToolResultBlock(m.ToolCallID, m.Content, false)
			if n := len(out); n > 0 && out[n-1].Role == sdk.MessageParamRoleUser {
				out[n-1].Content = append(out[n-1].Content, block)
			} else {
				out = append(out, sdk.MessageParam{
					Role:    sdk.MessageParamRoleUser,
					Content: []sdk.ContentBlockParamUnion{block},
				})
			}
		default:
			return nil, nil, fmt.Errorf("anthropic: message %d has unsupported role %q", i, m.Role)
		}
	}
	return system, out, nil
}

// convertTools converts tools.Def entries into ToolUnionParam values.
//
// Parameters is passed straight through — tools.Def.Parameters is already an
// OpenAI-style JSON schema object, which Anthropic's input_schema accepts by
// pulling out the `properties` and `required` fields.
func convertTools(defs []tools.Def) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, len(defs))
	for i, d := range defs {
		schema := sdk.ToolInputSchemaParam{}
		if d.Parameters != nil {
			if props, ok := d.Parameters["properties"]; ok {
				schema.Properties = props
			}
			if req, ok := d.Parameters["required"].([]string); ok {
				schema.Required = req
			} else if req, ok := d.Parameters["required"].([]any); ok {
				// Support loosely-typed JSON schemas that came back as
				// []any from a map-based decoder.
				strs := make([]string, 0, len(req))
				for _, v := range req {
					if s, ok := v.(string); ok {
						strs = append(strs, s)
					}
				}
				schema.Required = strs
			}
		}
		tp := &sdk.ToolParam{
			Name:        d.Name,
			InputSchema: schema,
		}
		if d.Description != "" {
			tp.Description = param.NewOpt(d.Description)
		}
		out[i] = sdk.ToolUnionParam{OfTool: tp}
	}
	return out
}

// convertResponseMessage maps a non-streaming sdk.Message response onto the
// harness' middleware.Message shape.  Text blocks are concatenated into
// Content; ToolUse blocks become ToolCalls (with Input marshalled back to a
// JSON string for middleware consumers that still expect the OpenAI-style
// stringified arguments).
func convertResponseMessage(m *sdk.Message) middleware.Message {
	out := middleware.Message{Role: "assistant"}
	var text string
	var calls []middleware.ToolCall
	for _, block := range m.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			// block.Input is already a json.RawMessage.
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, middleware.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
	}
	out.Content = text
	if len(calls) > 0 {
		out.ToolCalls = calls
	}
	return out
}
