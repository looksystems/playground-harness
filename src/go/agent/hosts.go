package agent

import (
	"context"

	"agent-harness/go/hooks"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
	"agent-harness/go/tools"
)

// HooksHost is the capability a component needs from an Agent (or any other
// host) to wire hook handlers and emit events.
//
// *Agent satisfies this interface via the embedded *hooks.Hub.
type HooksHost interface {
	On(hooks.Event, hooks.Handler) *hooks.Hub
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

// ShellHost is the capability a component needs from an Agent to execute
// shell commands and register custom commands.
//
// *Agent satisfies this interface at runtime via the embedded *shell.Host —
// but only when a shell driver has been attached (see HasShell). Calling
// these methods on an Agent without a shell panics on nil pointer
// dereference. No compile-time assertion is added for this reason: the
// interface is satisfied per-instance, not per-type.
type ShellHost interface {
	Exec(ctx context.Context, command string) (shell.ExecResult, error)
	RegisterCommand(name string, handler shell.CmdHandler)
}

// compile-time assertions — *Agent satisfies each capability interface.
// ShellHost is also included because *shell.Host's method set is promoted
// whether or not the embedded pointer is nil — the assertion is a
// compile-time check on the method set, not on the runtime value. Calling
// Exec / RegisterCommand on an Agent constructed without a shell driver
// will panic; guard with HasShell when dispatching generically.
var (
	_ HooksHost      = (*Agent)(nil)
	_ ToolsHost      = (*Agent)(nil)
	_ MiddlewareHost = (*Agent)(nil)
	_ ShellHost      = (*Agent)(nil)
)
