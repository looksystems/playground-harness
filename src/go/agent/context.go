package agent

// RunContext carries per-Run state threaded through hooks and middleware.
//
// It is intentionally passed to middleware as the opaque `any` slot (see
// middleware.Middleware) so the middleware package does not import the agent
// package. Consumers who need the concrete fields can type-assert on
// *RunContext.
type RunContext struct {
	// Agent is the agent that is currently running. It is safe to read but
	// middleware/hooks should treat it as read-only.
	Agent *Agent

	// Turn is the zero-based turn index currently executing. It is
	// incremented once per LLM call — tool-call sub-steps within a turn do
	// not advance it. Matches Python BaseAgent.run's loop counter.
	Turn int

	// Metadata is a free-form per-run scratch map available to middleware
	// and hooks for coordinating across turns.
	Metadata map[string]any
}
