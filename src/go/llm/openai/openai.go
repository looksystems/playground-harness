// Package openai implements the llm.Client interface using the
// github.com/openai/openai-go SDK.
//
// It covers the OpenAI platform plus any OpenAI-compatible endpoint that
// speaks the same API shape — Ollama, OpenRouter, LM Studio, an external
// litellm proxy, and so on — via the WithBaseURL option.
//
// The streaming aggregation here mirrors the Python harness' _handle_stream
// (see src/python/base_agent.py): tool-call deltas are keyed by their index
// and the id/name/arguments fields are accumulated across chunks so that the
// consumer receives monotonically-complete values.
package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"

	goopenai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// Option configures a Client constructed with New.
type Option func(*config)

// config collects the settings gathered by Options before the underlying
// openai-go client is built.  Keeping the fields pointer-typed lets us detect
// "unset" vs "set to zero value" and apply sensible fall-backs (notably the
// OPENAI_API_KEY environment fallback).
type config struct {
	apiKey       *string
	baseURL      *string
	httpClient   option.HTTPClient
	extraOptions []option.RequestOption
}

// WithAPIKey sets the API key on the underlying openai-go client.
//
// If this option is not supplied, the OPENAI_API_KEY environment variable is
// used instead (mirroring the default openai-go behaviour).  If neither is
// set, the first call will typically fail with an authentication error from
// the remote endpoint.
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = &key }
}

// WithBaseURL sets the API base URL on the underlying openai-go client.
// Use this to point the provider at an OpenAI-compatible endpoint such as
// Ollama, OpenRouter, LM Studio, or a litellm proxy.
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = &url }
}

// WithHTTPClient injects a custom http.Client (or anything implementing
// option.HTTPClient).  Primarily intended for tests — a fake transport makes
// it possible to exercise the provider end-to-end without hitting the network.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithRequestOptions plumbs additional openai-go request options through to
// the underlying client (e.g. option.WithOrganization).  Kept as a deliberate
// escape hatch; prefer the dedicated With* options above where possible.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(c *config) { c.extraOptions = append(c.extraOptions, opts...) }
}

// Client is the openai-backed implementation of llm.Client.
//
// It is safe for concurrent use by multiple goroutines — the underlying
// openai-go client is itself goroutine-safe, and every Stream call builds its
// own accumulator map.
type Client struct {
	inner goopenai.Client
}

// New constructs a Client.  If WithAPIKey is not supplied and OPENAI_API_KEY
// is present in the environment, that value is used.  If neither is present
// the client is still returned, but the first call will almost certainly fail
// (the underlying SDK surfaces the authentication error).
func New(opts ...Option) *Client {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	reqOpts := make([]option.RequestOption, 0, 4+len(cfg.extraOptions))

	// The harness owns retry policy (see llm.WithRetry).  Disable the SDK's
	// own retry loop so we do not double-retry on transient failures.  If a
	// caller wants the SDK's retries back they can re-enable via
	// WithRequestOptions(option.WithMaxRetries(n)).
	reqOpts = append(reqOpts, option.WithMaxRetries(0))

	switch {
	case cfg.apiKey != nil:
		reqOpts = append(reqOpts, option.WithAPIKey(*cfg.apiKey))
	default:
		// Explicit env fallback so behaviour is deterministic regardless of
		// what the openai-go default reader happens to do in future versions.
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
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

	return &Client{inner: goopenai.NewClient(reqOpts...)}
}

// Complete performs a single non-streaming chat completion.
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	params, err := buildParams(req)
	if err != nil {
		return llm.Response{}, err
	}

	resp, err := c.inner.Chat.Completions.New(ctx, params)
	if err != nil {
		return llm.Response{}, err
	}
	if len(resp.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("openai: completion returned no choices")
	}

	return llm.Response{Message: convertResponseMessage(resp.Choices[0].Message)}, nil
}

// Stream opens a streaming chat completion and returns a channel of
// llm.Chunk.  The producer goroutine owns the channel and closes it exactly
// once — after delivering the terminal Chunk{Done: true} (with Err populated
// on error).  Consumers must range over the channel and must not close it.
//
// Context cancellation is honoured in the send loop: if ctx is done while we
// are blocked trying to deliver a chunk, the goroutine closes the channel and
// exits without further sends.
func (c *Client) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}

	stream := c.inner.Chat.Completions.NewStreaming(ctx, params)

	ch := make(chan llm.Chunk, 8)

	go func() {
		defer close(ch)
		defer stream.Close()

		// Accumulators keyed by tool-call index, matching the Python
		// _handle_stream logic.
		toolCalls := make(map[int64]*accumTool)

		// emit sends a non-terminal chunk, honouring ctx cancellation.  If
		// the context is cancelled while we are blocked on the send, we give
		// up and let the caller see the channel close (no further sends).
		emit := func(chunk llm.Chunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// emitTerminal delivers the terminal chunk.  Unlike emit it does
		// not short-circuit on ctx.Done — a cancelled ctx is the most
		// common reason we need to send a terminal Err chunk, so dropping
		// it would swallow the very signal the consumer needs.  We first
		// attempt a non-blocking send (the buffer is usually free) and
		// only fall back to a blocking send that races against ctx.Done
		// to avoid wedging if the consumer has abandoned the channel
		// entirely.
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
			if len(ev.Choices) == 0 {
				continue
			}
			delta := ev.Choices[0].Delta

			if delta.Content != "" {
				if !emit(llm.Chunk{Content: delta.Content}) {
					// emit returned false because ctx.Done() won the
					// send race.  Deliver a terminal chunk carrying
					// ctx.Err() so consumers always see a Done marker
					// — emitTerminal itself tolerates a cancelled ctx.
					emitTerminal(llm.Chunk{Done: true, Err: ctx.Err()})
					return
				}
			}

			for _, tc := range delta.ToolCalls {
				entry, ok := toolCalls[tc.Index]
				if !ok {
					entry = &accumTool{}
					toolCalls[tc.Index] = entry
				}
				if tc.ID != "" {
					entry.ID = tc.ID
				}
				if tc.Function.Name != "" {
					entry.Name += tc.Function.Name
				}
				argsDelta := tc.Function.Arguments
				if argsDelta != "" {
					entry.Arguments += argsDelta
				}
				// Emit every time we see a tool-call delta so consumers
				// never need to do their own accumulation.  Forward the
				// current id + accumulated name, and this chunk's arg
				// delta (empty if this fragment contained only an id or
				// name, which still usefully signals the call exists).
				if !emit(llm.Chunk{
					ToolCallID: entry.ID,
					ToolName:   entry.Name,
					ToolArgs:   argsDelta,
				}) {
					// Same as above: surface ctx.Err() as the terminal
					// chunk so mid-stream cancellation is not silently
					// dropped.
					emitTerminal(llm.Chunk{Done: true, Err: ctx.Err()})
					return
				}
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

// accumTool is the streaming accumulator used to build up tool-call state
// across successive delta chunks.
type accumTool struct {
	ID        string
	Name      string
	Arguments string
}

// buildParams converts an llm.Request into the openai-go params struct.
func buildParams(req llm.Request) (goopenai.ChatCompletionNewParams, error) {
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return goopenai.ChatCompletionNewParams{}, err
	}

	params := goopenai.ChatCompletionNewParams{
		Model:    shared.ChatModel(req.Model),
		Messages: messages,
	}
	if req.Temperature != 0 {
		params.Temperature = param.NewOpt(req.Temperature)
	}
	if req.MaxTokens != 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(req.MaxTokens))
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools)
	}
	return params, nil
}

// convertMessages maps the harness Message slice to openai-go's union type.
func convertMessages(msgs []middleware.Message) ([]goopenai.ChatCompletionMessageParamUnion, error) {
	out := make([]goopenai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for i, m := range msgs {
		switch m.Role {
		case "system":
			out = append(out, goopenai.SystemMessage(m.Content))
		case "user":
			out = append(out, goopenai.UserMessage(m.Content))
		case "assistant":
			// Build the assistant param directly so we can populate ToolCalls
			// alongside any textual content.
			asst := goopenai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				asst.Content = goopenai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: param.NewOpt(m.Content),
				}
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]goopenai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for j, tc := range m.ToolCalls {
					calls[j] = goopenai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: goopenai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					}
				}
				asst.ToolCalls = calls
			}
			out = append(out, goopenai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
		case "tool":
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("openai: message %d has role=tool but empty ToolCallID", i)
			}
			out = append(out, goopenai.ToolMessage(m.Content, m.ToolCallID))
		default:
			return nil, fmt.Errorf("openai: message %d has unsupported role %q", i, m.Role)
		}
	}
	return out, nil
}

// convertTools converts tools.Def entries into ChatCompletionToolParam values.
//
// Parameters is passed straight through — tools.Def.Parameters is already the
// OpenAI-shaped JSON schema object.
func convertTools(defs []tools.Def) []goopenai.ChatCompletionToolParam {
	out := make([]goopenai.ChatCompletionToolParam, len(defs))
	for i, d := range defs {
		fn := shared.FunctionDefinitionParam{
			Name:       d.Name,
			Parameters: shared.FunctionParameters(d.Parameters),
		}
		if d.Description != "" {
			fn.Description = param.NewOpt(d.Description)
		}
		out[i] = goopenai.ChatCompletionToolParam{Function: fn}
	}
	return out
}

// convertResponseMessage maps the SDK's assistant message back to the
// harness' middleware.Message shape (including any tool calls).
func convertResponseMessage(m goopenai.ChatCompletionMessage) middleware.Message {
	out := middleware.Message{
		Role:    "assistant",
		Content: m.Content,
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]middleware.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			calls[i] = middleware.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}
		}
		out.ToolCalls = calls
	}
	return out
}
