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
	"agent-harness/go/shell"
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
// It embeds the three subsystems anonymously — *hooks.Hub, *tools.Registry
// and *middleware.Chain — so their methods are promoted onto *Agent. The
// unqualified subsystem type names (Hub, Registry, Chain) are deliberately
// distinct so the embeddings do not collide (see ADR 0031 Consequences).
//
// Chain .On/.Register/.Use via the Builder for multi-subsystem fluent
// configuration. After Build, each subsystem's methods are available on the
// Agent but return the subsystem type (not *Agent): e.g. a.On(...) returns
// *hooks.Hub, a.Register(...) returns *tools.Registry. This is deliberate —
// users who want the fluent agent.On(...).Use(...).Register(...) shape should
// keep chaining on the Builder before calling Build.
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

	*hooks.Hub
	*tools.Registry
	*middleware.Chain

	// Host is the shell subsystem. Nil when no shell driver was supplied.
	// When non-nil, *Agent satisfies the ShellHost capability interface via
	// the embedded Host's promoted Exec / RegisterCommand methods.
	*shell.Host

	client llm.Client
}

// NewAgent constructs an Agent with all subsystems eagerly initialised and
// defaults taken from Python base_agent.py. Prefer Builder for idiomatic
// construction.
//
// The shell subsystem is not installed: *Agent.Host is nil. Use
// NewAgentWithShell (or Builder.Shell) to attach a shell driver.
func NewAgent(model string, client llm.Client) *Agent {
	return &Agent{
		Model:      model,
		MaxTurns:   defaultMaxTurns,
		MaxRetries: defaultMaxRetries,
		Stream:     defaultStream,
		Hub:        hooks.NewHub(),
		Registry:   tools.New(),
		Chain:      middleware.NewChain(),
		client:     client,
	}
}

// NewAgentWithShell constructs an Agent with an attached shell subsystem.
// When driver is nil the shell package's DefaultFactory is consulted; if
// that cannot yield a driver either, Host.Driver stays nil and Exec will
// return an error.
//
// The Host is wired to the Agent's hook hub so SHELL_* events surface
// through the same registry that consumers observe via Agent.On.
func NewAgentWithShell(model string, client llm.Client, driver shell.Driver) *Agent {
	a := NewAgent(model, client)
	host := shell.NewHost(driver)
	host.SetHub(a.Hub)
	a.Host = host
	return a
}

// HasShell reports whether this Agent has an attached shell subsystem.
// Use this as a runtime predicate before invoking ShellHost methods —
// *Agent promotes shell.Host methods via embedding, so calling Exec /
// RegisterCommand on an Agent without a shell will panic on nil pointer
// dereference.
func (a *Agent) HasShell() bool {
	return a.Host != nil
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

			// Unknown tools: mirror Python uses_tools.py which envelopes
			// the failure as the tool message and keeps the conversation
			// going. We intentionally skip ToolCall/ToolResult for these
			// — there is no real call to observe — but fire ToolError so
			// observers still see the failure.
			if _, ok := a.Registry.Get(tc.Name); !ok {
				envelope, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("Unknown tool: %s", tc.Name)})
				if err := a.Emit(ctx, hooks.ToolError, tc.Name, tools.ErrNotFound); err != nil {
					return "", err
				}
				messages = append(messages, middleware.Message{
					Role:       "tool",
					Name:       tc.Name,
					ToolCallID: tc.ID,
					Content:    string(envelope),
				})
				continue
			}

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
// Callers are expected to have already checked that name is registered (the
// Run loop does this so it can envelope unknown-tool calls without firing
// ToolCall/ToolResult). As a defensive fallback, if Execute still returns
// tools.ErrNotFound we bubble it up as a dispatch error; all other runtime
// errors are converted to `{"error":"<msg>"}` envelopes and returned as the
// tool message content so the conversation can continue — matching Python's
// _execute_tool.
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
