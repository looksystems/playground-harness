package agent

import (
	"context"
	"errors"

	"agent-harness/go/events"
	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
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

	// shell subsystem
	shellDriver  shell.Driver
	shellDriverSet bool
	shellCommands []shellCmdBinding

	// events subsystem
	eventTypes    []events.EventType
	defaultEvents []string
}

type shellCmdBinding struct {
	name    string
	handler shell.CmdHandler
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

// Shell attaches a shell driver to the resulting Agent. Pass nil to install
// the default builtin driver via shell.DefaultFactory. Calling Shell at
// least once triggers auto-registration of the `exec` tool on Build.
func (b *Builder) Shell(driver shell.Driver) *Builder {
	b.shellDriver = driver
	b.shellDriverSet = true
	return b
}

// Command registers a custom shell command on the resulting Agent. Implies
// Shell(nil) if Shell was not called — the builtin driver is used.
func (b *Builder) Command(name string, handler shell.CmdHandler) *Builder {
	b.shellCommands = append(b.shellCommands, shellCmdBinding{name: name, handler: handler})
	if !b.shellDriverSet {
		b.shellDriverSet = true
	}
	return b
}

// Event registers a single event type on the resulting Agent's events
// subsystem. Registering at least one event triggers the automatic
// prompt-injection middleware at Build time, matching Python's
// SkillPromptMiddleware pattern.
func (b *Builder) Event(t events.EventType) *Builder {
	b.eventTypes = append(b.eventTypes, t)
	return b
}

// Events registers several event types at once.
func (b *Builder) Events(ts ...events.EventType) *Builder {
	b.eventTypes = append(b.eventTypes, ts...)
	return b
}

// DefaultEvents sets the names of event types activated per run when
// no explicit list is provided. Names not matching any registered type
// are silently skipped at resolve time (matches Python's default_events).
func (b *Builder) DefaultEvents(names ...string) *Builder {
	b.defaultEvents = append([]string(nil), names...)
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

	var a *Agent
	if b.shellDriverSet {
		a = NewAgentWithShell(b.model, b.client, b.shellDriver)
	} else {
		a = NewAgent(b.model, b.client)
	}
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
		a.Hub.On(hb.event, hb.handler)
	}

	// The `exec` tool is already registered by NewAgentWithShell when
	// the shell subsystem is attached. Here we only need to wire up
	// any custom commands supplied via Builder.Command(...).
	if a.Host != nil {
		for _, cmd := range b.shellCommands {
			a.Host.RegisterCommand(cmd.name, cmd.handler)
		}
	}

	// Events subsystem: register event types, wire defaults, install
	// prompt-injection middleware if any are registered. Ported from
	// Python's SkillPromptMiddleware pattern — the divergence from
	// Python here is that Go auto-installs the prompt, closing the
	// "parser is not wired to bus" TODO by default.
	if len(b.eventTypes) > 0 {
		for _, et := range b.eventTypes {
			a.Events.Register(et)
		}
		// If user did not call DefaultEvents, default to all
		// registered names (matches the active-skills semantics of
		// SkillPromptMiddleware). If they did, honour their list
		// verbatim — non-registered names are skipped at resolve.
		if len(b.defaultEvents) == 0 {
			names := make([]string, 0, len(b.eventTypes))
			for _, et := range b.eventTypes {
				names = append(names, et.Name)
			}
			a.Events.SetDefaults(names...)
		} else {
			a.Events.SetDefaults(b.defaultEvents...)
		}
		a.Use(&eventPromptMiddleware{host: a.Events})
	} else if len(b.defaultEvents) > 0 {
		// Defaults set without any registered types — honour them
		// (ResolveActive will skip missing names). No middleware,
		// because there is nothing to prompt about.
		a.Events.SetDefaults(b.defaultEvents...)
	}
	return a, nil
}
