// Package middleware provides a pre/post transformer pipeline around LLM
// requests, ported from the Python HasMiddleware mixin.
//
// Execution model: both RunPre and RunPost iterate middlewares in registration
// order (the same direction for both), matching Python's _run_pre/_run_post.
// This is intentionally different from many HTTP-middleware frameworks that
// reverse the post direction.
package middleware

import (
	"context"
	"sync"
)

// Message is the shape passed through the middleware pipeline.  It mirrors the
// OpenAI chat-completion message dict used throughout the harness.
type Message struct {
	// Role is one of "system", "user", "assistant", or "tool".
	Role string

	// Content holds the text payload of the message.
	Content string

	// Name is the function/tool name; used for "tool" role messages.
	Name string

	// ToolCallID is the ID of the tool call that produced this message.
	ToolCallID string

	// ToolCalls holds any tool invocations present in an assistant message.
	ToolCalls []ToolCall
}

// ToolCall represents a single tool invocation inside an assistant message.
type ToolCall struct {
	// ID is the unique call identifier returned by the LLM.
	ID string

	// Name is the name of the tool being called.
	Name string

	// Arguments is the JSON-encoded argument string as returned by the LLM.
	Arguments string
}

// Middleware is a pre/post transformer around an LLM request.
//
// Pre runs before the LLM call; it may mutate, filter, or annotate the
// outgoing messages slice.  Post runs after the LLM call; it may mutate the
// assistant message.
//
// Both methods receive runCtx (typed as any to avoid a circular import with
// the agent package that owns RunContext).
type Middleware interface {
	Pre(ctx context.Context, messages []Message, runCtx any) ([]Message, error)
	Post(ctx context.Context, message Message, runCtx any) (Message, error)
}

// Base is a no-op Middleware.  Embed it in your own type and override only
// the half you need.
type Base struct{}

// Pre returns msgs unchanged.
func (Base) Pre(_ context.Context, msgs []Message, _ any) ([]Message, error) {
	return msgs, nil
}

// Post returns msg unchanged.
func (Base) Post(_ context.Context, msg Message, _ any) (Message, error) {
	return msg, nil
}

// MiddlewareFunc is a convenience adapter that lets you build a Middleware
// from two plain functions.  Pass nil for either function to fall back to the
// Base no-op behaviour.
type MiddlewareFunc struct {
	preFn  func(context.Context, []Message, any) ([]Message, error)
	postFn func(context.Context, Message, any) (Message, error)
}

// NewMiddlewareFunc constructs a MiddlewareFunc.  Nil functions become no-ops.
func NewMiddlewareFunc(
	pre func(context.Context, []Message, any) ([]Message, error),
	post func(context.Context, Message, any) (Message, error),
) MiddlewareFunc {
	return MiddlewareFunc{preFn: pre, postFn: post}
}

// Pre implements Middleware.
func (f MiddlewareFunc) Pre(ctx context.Context, msgs []Message, runCtx any) ([]Message, error) {
	if f.preFn == nil {
		return msgs, nil
	}
	return f.preFn(ctx, msgs, runCtx)
}

// Post implements Middleware.
func (f MiddlewareFunc) Post(ctx context.Context, msg Message, runCtx any) (Message, error) {
	if f.postFn == nil {
		return msg, nil
	}
	return f.postFn(ctx, msg, runCtx)
}

// Chain holds a sequence of middlewares executed in registration order for
// both Pre and Post passes.  It is safe for concurrent use.
type Chain struct {
	mu    sync.RWMutex
	items []Middleware
}

// NewChain returns an empty, ready-to-use Chain.
func NewChain() *Chain {
	return &Chain{}
}

// Use appends mw to the chain and returns the chain for fluent builder calls.
func (c *Chain) Use(mw Middleware) *Chain {
	c.mu.Lock()
	c.items = append(c.items, mw)
	c.mu.Unlock()
	return c
}

// Snapshot returns an independent copy of the current middleware slice.
func (c *Chain) Snapshot() []Middleware {
	c.mu.RLock()
	cp := make([]Middleware, len(c.items))
	copy(cp, c.items)
	c.mu.RUnlock()
	return cp
}

// RunPre feeds messages through each middleware's Pre in registration order.
// It short-circuits and returns the first error encountered.
func (c *Chain) RunPre(ctx context.Context, messages []Message, runCtx any) ([]Message, error) {
	snap := c.Snapshot()
	var err error
	for _, mw := range snap {
		messages, err = mw.Pre(ctx, messages, runCtx)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// RunPost feeds message through each middleware's Post in registration order.
// It short-circuits and returns the first error encountered.
func (c *Chain) RunPost(ctx context.Context, message Message, runCtx any) (Message, error) {
	snap := c.Snapshot()
	var err error
	for _, mw := range snap {
		message, err = mw.Post(ctx, message, runCtx)
		if err != nil {
			return Message{}, err
		}
	}
	return message, nil
}
