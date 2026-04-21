package agent

import (
	"context"

	"agent-harness/go/events"
	"agent-harness/go/middleware"
)

// eventPromptMiddleware injects the "# Event Emission" section describing
// the registered event types into the system prompt on every Pre pass.
// It is the Go port of Python's SkillPromptMiddleware, adapted to events.
//
// The block is appended to the existing system message if one is present
// at index 0 of messages (matching Python's behaviour); otherwise a new
// system message carrying only the block is prepended.
//
// If no events resolve as active (e.g. none registered, or defaults
// empty) the middleware is a no-op and returns the messages unchanged.
type eventPromptMiddleware struct {
	host *events.Host
}

// Pre injects the event-emission prompt block. Non-mutating on messages
// — callers may safely reuse their slice.
func (m *eventPromptMiddleware) Pre(
	_ context.Context,
	messages []middleware.Message,
	_ any,
) ([]middleware.Message, error) {
	active := m.host.ResolveActive(nil)
	if len(active) == 0 {
		return messages, nil
	}
	block := events.BuildPrompt(active)
	if block == "" {
		return messages, nil
	}

	// Match Python's SkillPromptMiddleware framing: a blank-line
	// separator + horizontal rule + header. Keeps the event block
	// visually distinct from any pre-existing system prompt.
	decorated := "\n\n---\n" + block

	out := make([]middleware.Message, len(messages))
	copy(out, messages)

	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = out[i].Content + decorated
			return out, nil
		}
	}

	// No system message present — prepend one with the block content.
	sys := middleware.Message{Role: "system", Content: block}
	return append([]middleware.Message{sys}, out...), nil
}

// Post is a no-op — event prompts do not transform assistant messages.
func (m *eventPromptMiddleware) Post(
	_ context.Context,
	msg middleware.Message,
	_ any,
) (middleware.Message, error) {
	return msg, nil
}
