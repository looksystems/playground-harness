package agent

import (
	"context"
	"errors"

	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// Builder is the fluent construction API for Agent. It is the Go port of
// Python's AgentBuilder (src/python/agent_builder.py).
//
// Typical use:
//
//	a, err := agent.NewBuilder("gpt-4o").
//	    System("You are helpful.").
//	    Client(myClient).
//	    Tool(addTool).
//	    Use(retryMW).
//	    On(hooks.RunStart, onStart).
//	    Build(ctx)
//
// Build validates required fields (Client, Model) and returns an error
// rather than panicking.
type Builder struct {
	model      string
	system     string
	maxTurns   int
	maxRetries int
	stream     bool
	streamSet  bool
	client     llm.Client
	tools      []tools.Def
	middleware []middleware.Middleware
	hooks      []hookBinding
}

type hookBinding struct {
	event   hooks.Event
	handler hooks.Handler
}

// NewBuilder starts a new Builder for the given model. Defaults mirror
// Python base_agent.py: MaxTurns=20, MaxRetries=2, Streaming=true.
func NewBuilder(model string) *Builder {
	return &Builder{
		model:      model,
		maxTurns:   defaultMaxTurns,
		maxRetries: defaultMaxRetries,
		stream:     defaultStream,
	}
}

// System sets the system prompt. Empty string means "no system prompt".
func (b *Builder) System(prompt string) *Builder {
	b.system = prompt
	return b
}

// MaxTurns sets the maximum number of LLM round-trips Run will perform.
func (b *Builder) MaxTurns(n int) *Builder {
	b.maxTurns = n
	return b
}

// MaxRetries sets the retry budget carried on the resulting Agent.
func (b *Builder) MaxRetries(n int) *Builder {
	b.maxRetries = n
	return b
}

// Streaming enables or disables streaming mode for the LLM call.
func (b *Builder) Streaming(enable bool) *Builder {
	b.stream = enable
	b.streamSet = true
	return b
}

// Client sets the llm.Client the Agent uses. Required.
func (b *Builder) Client(c llm.Client) *Builder {
	b.client = c
	return b
}

// Tool registers a single tool definition on the resulting Agent.
func (b *Builder) Tool(d tools.Def) *Builder {
	b.tools = append(b.tools, d)
	return b
}

// Tools registers several tool definitions at once.
func (b *Builder) Tools(ds ...tools.Def) *Builder {
	b.tools = append(b.tools, ds...)
	return b
}

// Use appends mw to the middleware chain of the resulting Agent.
func (b *Builder) Use(mw middleware.Middleware) *Builder {
	b.middleware = append(b.middleware, mw)
	return b
}

// On registers a hook handler on the resulting Agent.
func (b *Builder) On(e hooks.Event, h hooks.Handler) *Builder {
	b.hooks = append(b.hooks, hookBinding{event: e, handler: h})
	return b
}

// Build assembles the Agent. It returns an error when required pieces are
// missing: Client (non-nil) and Model (non-empty). It never panics.
//
// The ctx argument is accepted for parity with the Python coroutine but is
// currently unused; future expansions (skill mount, shell bootstrap) will
// consume it.
func (b *Builder) Build(_ context.Context) (*Agent, error) {
	if b.model == "" {
		return nil, errors.New("agent.Builder: Model must be set (NewBuilder was called with empty string?)")
	}
	if b.client == nil {
		return nil, errors.New("agent.Builder: Client is required — call Builder.Client(c) before Build")
	}

	a := NewAgent(b.model, b.client)
	a.System = b.system
	a.MaxTurns = b.maxTurns
	a.MaxRetries = b.maxRetries
	if b.streamSet {
		a.Stream = b.stream
	}

	for _, d := range b.tools {
		a.Register(d)
	}
	for _, mw := range b.middleware {
		a.Use(mw)
	}
	for _, hb := range b.hooks {
		a.Hooks.On(hb.event, hb.handler)
	}
	return a, nil
}
