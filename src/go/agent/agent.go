// Package agent composes the harness subsystems (hooks, tools, middleware,
// llm) into a runnable Agent with a Builder-driven construction API.
//
// It is the Go port of Python's StandardAgent + AgentBuilder. See
// src/python/base_agent.py for the canonical Run loop and
// src/python/agent_builder.py for the builder.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// Default configuration values, matching Python base_agent.py defaults.
const (
	defaultMaxTurns   = 20
	defaultMaxRetries = 2
	defaultStream     = true
)

// Agent is the composed harness entry point.
//
// It embeds the two subsystems whose unqualified type names are distinct —
// *tools.Registry and *middleware.Chain — so their methods are promoted onto
// *Agent. The hooks registry shares its unqualified type name with the tools
// registry (both are named "Registry"), so it is stored as a named Hooks
// field, and a small set of forwarder methods (On, Emit, EmitAsync, Off,
// Handlers) expose its API on *Agent.
//
// A single Agent may be shared across concurrent Run invocations — all
// subsystems are concurrent-safe and Run keeps no shared per-run state on the
// Agent itself.
type Agent struct {
	// Model is the LLM model identifier (e.g. "gpt-4o").
	Model string

	// System is the optional system prompt prepended to the initial message
	// slice during Run if no system message is already at index 0.
	System string

	// MaxTurns caps the number of LLM round-trips Run will perform.
	MaxTurns int

	// MaxRetries is carried as configuration so it can be read by retry
	// middleware or observed by callers. Agent.Run itself does not retry —
	// retry policy is applied by wrapping the llm.Client with
	// llm.WithRetry(...) at construction time. Matches the Python config
	// surface, where base_agent.max_retries is also only consumed by the
	// wrapping retry layer.
	MaxRetries int

	// Stream selects between client.Stream and client.Complete.
	Stream bool

	// Hooks is the event registry. On/Emit/Off/Handlers/EmitAsync are
	// forwarded from here via methods on *Agent.
	Hooks *hooks.Registry

	*tools.Registry
	*middleware.Chain

	client llm.Client
}

// NewAgent constructs an Agent with all subsystems eagerly initialised and
// defaults taken from Python base_agent.py. Prefer Builder for idiomatic
// construction.
func NewAgent(model string, client llm.Client) *Agent {
	return &Agent{
		Model:      model,
		MaxTurns:   defaultMaxTurns,
		MaxRetries: defaultMaxRetries,
		Stream:     defaultStream,
		Hooks:      hooks.New(),
		Registry:   tools.New(),
		Chain:      middleware.NewChain(),
		client:     client,
	}
}

// ---------------------------------------------------------------------------
// Hooks forwarders.  These exist because *hooks.Registry shares its
// unqualified type-name with *tools.Registry and so cannot be embedded
// alongside it.  Each method is a thin pass-through.
// ---------------------------------------------------------------------------

// On registers h as a handler for the given hook event and returns the
// underlying hooks.Registry for chained calls.
func (a *Agent) On(event hooks.Event, h hooks.Handler) *hooks.Registry {
	return a.Hooks.On(event, h)
}

// Emit invokes every handler registered for event and waits for them to
// complete. It honours ctx cancellation.
func (a *Agent) Emit(ctx context.Context, event hooks.Event, args ...any) error {
	return a.Hooks.Emit(ctx, event, args...)
}

// EmitAsync dispatches event on a detached goroutine (fire-and-forget).
func (a *Agent) EmitAsync(ctx context.Context, event hooks.Event, args ...any) {
	a.Hooks.EmitAsync(ctx, event, args...)
}

// Off removes all handlers registered for event.
func (a *Agent) Off(event hooks.Event) {
	a.Hooks.Off(event)
}

// Handlers returns a defensive copy of the handlers registered for event.
func (a *Agent) Handlers(event hooks.Event) []hooks.Handler {
	return a.Hooks.Handlers(event)
}

// ---------------------------------------------------------------------------
// Run loop
// ---------------------------------------------------------------------------

// Run executes the agent loop against the given message slice and returns the
// final assistant text.
//
// Algorithm (port of Python BaseAgent.run):
//
//  1. Defensive-copy messages and optionally prepend the system prompt.
//  2. Emit RunStart.
//  3. For each turn up to MaxTurns:
//     (a) middleware Pre — short-circuit on error;
//     (b) collect tool schemas (none if no tools are registered);
//     (c) emit LLMRequest;
//     (d) call client.Stream or client.Complete depending on Stream;
//     (e) middleware Post — short-circuit on error;
//     (f) emit LLMResponse;
//     (g) append the assistant message; if it has no tool calls, emit
//     RunEnd and return its content;
//     (h) otherwise execute each tool call, appending a `tool` message
//     per call. Tool runtime errors become `{"error":"..."}`
//     envelopes (matching Python) and do NOT abort the run; only
//     dispatch failures (unknown tool) do.
//  4. If the turn budget is exhausted, emit RunEnd and return the last
//     assistant content.
//
// Tool result serialisation uses json.Marshal. If Marshal fails, it falls
// back to json.Marshal(fmt.Sprintf("%v", result)), matching Python's
// json.dumps(..., default=str).
func (a *Agent) Run(ctx context.Context, messages []middleware.Message) (string, error) {
	if a.client == nil {
		return "", errors.New("agent: Run called with nil llm.Client")
	}

	// Honour a pre-cancelled context immediately.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Defensive copy so callers can reuse their slice (Python does
	// deepcopy(messages)).
	messages = append([]middleware.Message(nil), messages...)

	// Prepend system prompt (matches Python _build_system_prompt seam):
	// only if a system prompt is configured AND the caller didn't already
	// put one at index 0. If they did, replace its content.
	if a.System != "" {
		if len(messages) > 0 && messages[0].Role == "system" {
			messages[0].Content = a.System
		} else {
			sys := middleware.Message{Role: "system", Content: a.System}
			messages = append([]middleware.Message{sys}, messages...)
		}
	}

	runCtx := &RunContext{
		Agent:    a,
		Turn:     0,
		Metadata: map[string]any{},
	}

	if err := a.Emit(ctx, hooks.RunStart, runCtx); err != nil {
		return "", err
	}

	// lastContent tracks the assistant text most recently produced; used
	// when the turn budget is exhausted so the caller gets the best-effort
	// reply rather than an empty string.
	var lastContent string

	for turn := 0; turn < a.MaxTurns; turn++ {
		runCtx.Turn = turn

		if err := ctx.Err(); err != nil {
			return "", err
		}

		// (a) middleware Pre
		var err error
		messages, err = a.RunPre(ctx, messages, runCtx)
		if err != nil {
			return "", err
		}

		// (b) tool schemas — forward as tools.Def slice to the client via
		// Request.Tools. The provider is responsible for marshalling into
		// its wire format. Pass nil (not an empty slice) when empty so
		// clients that treat nil-vs-empty differently see "no tools".
		var toolDefs []tools.Def
		if toolList := a.Registry.List(); len(toolList) > 0 {
			toolDefs = toolList
		}

		req := llm.Request{
			Model:    a.Model,
			Messages: messages,
			Tools:    toolDefs,
		}

		// (c) emit LLMRequest
		if err := a.Emit(ctx, hooks.LLMRequest, messages, toolDefs); err != nil {
			return "", err
		}

		// (d) dispatch to the LLM
		assistant, err := a.callLLM(ctx, req)
		if err != nil {
			return "", err
		}

		// (e) middleware Post
		assistant, err = a.RunPost(ctx, assistant, runCtx)
		if err != nil {
			return "", err
		}

		// (f) emit LLMResponse
		if err := a.Emit(ctx, hooks.LLMResponse, assistant); err != nil {
			return "", err
		}

		// (g) append assistant, remember content
		messages = append(messages, assistant)
		lastContent = assistant.Content

		// terminal turn (no tool calls)?
		if len(assistant.ToolCalls) == 0 {
			if err := a.Emit(ctx, hooks.RunEnd, runCtx); err != nil {
				return "", err
			}
			return assistant.Content, nil
		}

		// (h) execute tool calls in the order they were returned.
		for _, tc := range assistant.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", err
			}

			argsBytes := []byte(tc.Arguments)
			if err := a.Emit(ctx, hooks.ToolCall, tc.Name, argsBytes); err != nil {
				return "", err
			}

			toolContent, dispatchErr := a.executeTool(ctx, tc.Name, argsBytes)
			if dispatchErr != nil {
				return "", dispatchErr
			}

			messages = append(messages, middleware.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    toolContent,
			})
		}
	}

	// Turn budget exhausted — emit RunEnd and return the last assistant
	// text. Python does the same: the loop simply exits without calling
	// any extra hook.
	if err := a.Emit(ctx, hooks.RunEnd, runCtx); err != nil {
		return "", err
	}
	return lastContent, nil
}

// executeTool runs a single tool call, fires the matching hooks, and returns
// the JSON-encoded content that should become the `tool` message's body.
//
// It returns a dispatch-level error only when the tool is not registered
// (tools.ErrNotFound); all tool runtime errors are converted to
// `{"error":"<msg>"}` envelopes and returned as the tool message content so
// the conversation can continue — matching Python's _execute_tool.
func (a *Agent) executeTool(ctx context.Context, name string, args []byte) (string, error) {
	result, err := a.Registry.Execute(ctx, name, args)
	if err != nil {
		if errors.Is(err, tools.ErrNotFound) {
			return "", err
		}
		// Emit ToolError so observers see the failure, then serialise the
		// envelope. Python uses json.dumps({"error": str(e)}).
		if emitErr := a.Emit(ctx, hooks.ToolError, name, err); emitErr != nil {
			return "", emitErr
		}
		envelope, mErr := json.Marshal(map[string]string{"error": err.Error()})
		if mErr != nil {
			// Should never happen for a string map; fall back to a
			// hand-crafted envelope just in case.
			return fmt.Sprintf(`{"error":%q}`, err.Error()), nil
		}
		return string(envelope), nil
	}

	if err := a.Emit(ctx, hooks.ToolResult, name, result); err != nil {
		return "", err
	}

	// Serialise the result as JSON. Mirror Python's json.dumps(result,
	// default=str) fallback: if the value is not JSON-marshallable, fall
	// back to fmt.Sprintf("%v", result).
	encoded, err := json.Marshal(result)
	if err != nil {
		fallback, fbErr := json.Marshal(fmt.Sprintf("%v", result))
		if fbErr != nil {
			return "", fmt.Errorf("agent: serialise tool %q result: %w", name, err)
		}
		return string(fallback), nil
	}
	return string(encoded), nil
}

// callLLM dispatches a single LLM call via the embedded client, honouring the
// Stream flag and delegating streaming aggregation to aggregateStream.
func (a *Agent) callLLM(ctx context.Context, req llm.Request) (middleware.Message, error) {
	if a.Stream {
		ch, err := a.client.Stream(ctx, req)
		if err != nil {
			return middleware.Message{}, err
		}
		return aggregateStream(ctx, ch)
	}
	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		return middleware.Message{}, err
	}
	return resp.Message, nil
}

// streamToolAccum is the per-id accumulator used while aggregating a stream
// into a single assistant Message.
type streamToolAccum struct {
	id    string
	name  string
	args  []byte
	first int // stable insertion order
}

// aggregateStream drains the streaming channel and assembles a single
// assistant middleware.Message in the same shape Python's _handle_stream
// produces.
//
// The llm.Chunk protocol (see llm.Chunk docs) pre-aggregates ToolName: each
// tool-call chunk carries the accumulated-so-far name for that id. ToolArgs
// is an additive delta. Matching the OpenAI provider's contract, we:
//
//   - concatenate Chunk.Content across chunks, in arrival order
//   - index tool-call accumulators by ToolCallID
//   - for each accumulator, track the latest non-empty ToolName and append
//     every ToolArgs delta
//
// Tool calls are emitted in the order their first chunk arrived.
func aggregateStream(ctx context.Context, ch <-chan llm.Chunk) (middleware.Message, error) {
	var content []byte
	accums := map[string]*streamToolAccum{}
	var nextOrder int

	for {
		select {
		case <-ctx.Done():
			return middleware.Message{}, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				// Producer closed without a terminal Done chunk. If the
				// context was cancelled concurrently Go's select may
				// have picked this arm non-deterministically — re-check
				// ctx.Err() so a cancelled run still surfaces the
				// cancellation instead of masquerading as a clean
				// end-of-stream.
				if err := ctx.Err(); err != nil {
					return middleware.Message{}, err
				}
				return finaliseStream(content, accums), nil
			}
			if chunk.Err != nil {
				return middleware.Message{}, chunk.Err
			}
			if chunk.Done {
				return finaliseStream(content, accums), nil
			}

			if chunk.Content != "" {
				content = append(content, chunk.Content...)
			}

			if chunk.ToolCallID != "" {
				entry, exists := accums[chunk.ToolCallID]
				if !exists {
					entry = &streamToolAccum{id: chunk.ToolCallID, first: nextOrder}
					nextOrder++
					accums[chunk.ToolCallID] = entry
				}
				if chunk.ToolName != "" {
					// Provider contract: ToolName is accumulated. Keep the
					// latest non-empty value.
					entry.name = chunk.ToolName
				}
				if chunk.ToolArgs != "" {
					entry.args = append(entry.args, chunk.ToolArgs...)
				}
			}
		}
	}
}

// finaliseStream converts the accumulated content and tool-call map into a
// middleware.Message in deterministic order (first-arrival of the id).
func finaliseStream(content []byte, accums map[string]*streamToolAccum) middleware.Message {
	msg := middleware.Message{Role: "assistant", Content: string(content)}
	if len(accums) == 0 {
		return msg
	}
	ordered := make([]*streamToolAccum, 0, len(accums))
	for _, acc := range accums {
		ordered = append(ordered, acc)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].first < ordered[j].first
	})
	msg.ToolCalls = make([]middleware.ToolCall, len(ordered))
	for i, acc := range ordered {
		msg.ToolCalls[i] = middleware.ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: string(acc.args),
		}
	}
	return msg
}
