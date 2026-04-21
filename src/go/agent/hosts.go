package agent

import (
	"context"

	"agent-harness/go/hooks"
	"agent-harness/go/middleware"
	"agent-harness/go/tools"
)

// HooksHost is the capability a component needs from an Agent (or any other
// host) to wire hook handlers and emit events.
//
// *Agent satisfies this interface via the embedded *hooks.Registry.
type HooksHost interface {
	On(hooks.Event, hooks.Handler) *hooks.Registry
	Emit(context.Context, hooks.Event, ...any) error
}

// ToolsHost is the capability a component needs from an Agent to register and
// dispatch tools.
//
// *Agent satisfies this interface via the embedded *tools.Registry.
type ToolsHost interface {
	Register(tools.Def) *tools.Registry
	Execute(context.Context, string, []byte) (any, error)
}

// MiddlewareHost is the capability a component needs from an Agent to append
// pre/post middleware to the chain.
//
// *Agent satisfies this interface via the embedded *middleware.Chain.
type MiddlewareHost interface {
	Use(middleware.Middleware) *middleware.Chain
}

// compile-time assertions — *Agent satisfies each capability interface.
var (
	_ HooksHost      = (*Agent)(nil)
	_ ToolsHost      = (*Agent)(nil)
	_ MiddlewareHost = (*Agent)(nil)
)
