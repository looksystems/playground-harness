// Package skills defines the Skill interface, optional capability interfaces,
// SkillContext, and the Base embedding struct for the Go agent harness.
//
// Design (ADR 0031): a small required Skill interface + narrow optional
// capability interfaces. The SkillManager type-asserts each capability at
// mount time rather than requiring one monolithic interface.
//
// # Divergences from Python
//
//   - Dependencies() returns []Skill instances rather than []type[Skill], because
//     Go cannot instantiate by type; callers supply live instances.
//   - Setup/Teardown are synchronous (return error) rather than async; the Go
//     runtime's goroutine model makes this cleaner.
//   - AutoName uses reflection on the concrete struct type rather than
//     __init_subclass__; the result is identical for well-named types.
package skills

import (
	"context"
	"reflect"
	"strings"

	"agent-harness/go/hooks"
	"agent-harness/go/internal/util"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
	"agent-harness/go/tools"
)

// ---------------------------------------------------------------------------
// Required interface
// ---------------------------------------------------------------------------

// Skill is the minimum surface every skill must implement.
// Pass any value satisfying this interface to SkillManager.Mount.
type Skill interface {
	// Name returns the skill's identifying name. If it returns "", SkillManager
	// calls AutoName to derive the name from the concrete struct type.
	Name() string

	// Description is a one-line human-readable description of the skill.
	Description() string

	// Version is a semantic version string for the skill. Base returns "0.1.0".
	Version() string

	// Instructions is injected into the system prompt by SkillPromptMiddleware.
	Instructions() string
}

// ---------------------------------------------------------------------------
// Optional capability interfaces
// ---------------------------------------------------------------------------

// Dependencies declares other skills that must be mounted before this one.
// Cycles are detected and rejected by SkillManager.
//
// Note: diverges from Python which uses class references. Go returns instances
// because Go has no way to instantiate an arbitrary type. Documented in the
// Go porting guide (ADR 0031).
type Dependencies interface {
	Dependencies() []Skill
}

// Setuppable is called once when the skill is mounted.
// Return an error to abort the mount.
//
// Note: Python's setup is async; Go uses a synchronous return value.
type Setuppable interface {
	Setup(ctx context.Context, sctx *SkillContext) error
}

// Teardown is called when the agent shuts down or the skill is unmounted.
// Errors are logged but do not abort shutdown.
type Teardown interface {
	Teardown(ctx context.Context, sctx *SkillContext) error
}

// ToolsContributor registers tools with the agent.
type ToolsContributor interface {
	Tools() []tools.Def
}

// MiddlewareContributor registers middleware with the agent.
type MiddlewareContributor interface {
	Middleware() []middleware.Middleware
}

// HooksContributor registers hook handlers with the agent.
// SkillManager iterates the returned map and calls Hub.On for each entry.
type HooksContributor interface {
	Hooks() map[hooks.Event][]hooks.Handler
}

// CommandsContributor registers shell commands with the agent.
type CommandsContributor interface {
	Commands() map[string]shell.CmdHandler
}

// ---------------------------------------------------------------------------
// SkillContext
// ---------------------------------------------------------------------------

// SkillContext is passed to Setup and Teardown. It carries a reference to the
// skill itself, an opaque handle to the owning agent, and any per-mount
// configuration supplied by the caller.
//
// Agent is typed as any to avoid an import cycle with the agent package;
// callers type-assert to *agent.Agent when they need the concrete type.
type SkillContext struct {
	// Skill is the skill being set up or torn down.
	Skill Skill

	// Agent is the owning *agent.Agent; type-assert at call site.
	Agent any

	// Config is user-supplied per-mount configuration. May be nil.
	Config map[string]any
}

// ---------------------------------------------------------------------------
// AutoName
// ---------------------------------------------------------------------------

// AutoName returns the snake_case name for a skill.
//
// If s.Name() returns a non-empty string, that value is returned unchanged
// (explicit naming wins over auto-derivation).
//
// Otherwise the concrete struct type name is extracted via reflection,
// "Skill" suffix is stripped (WebBrowsingSkill → "WebBrowsing"), and the
// result is converted to snake_case (→ "web_browsing").
//
// Edge cases:
//   - Pointer receivers: the pointer is dereferenced to obtain the struct name.
//   - Anonymous / unnamed types (e.g. struct{}): AutoName panics with a
//     descriptive message, because unnamed types cannot produce a meaningful
//     skill name. Register such skills with an explicit Name() override.
//   - Generic instantiations: the full instantiated name (e.g.
//     "FooSkill[int]") is used as-is after stripping "Skill"; this may produce
//     an odd result. Prefer explicit Name() for generic skills.
func AutoName(s Skill) string {
	if n := s.Name(); n != "" {
		return n
	}

	t := reflect.TypeOf(s)
	// Dereference pointer types (e.g. *WebBrowsingSkill → WebBrowsingSkill).
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil {
		panic("skills.AutoName: cannot derive name from nil type")
	}

	typeName := t.Name()
	if typeName == "" {
		panic("skills.AutoName: cannot derive name from an anonymous or unnamed struct type; " +
			"override Name() to provide an explicit name")
	}

	// Strip trailing "Skill" suffix and convert to snake_case.
	// util.SnakeCase already handles the TrimSuffix("Skill") step,
	// so we delegate entirely.
	raw := strings.TrimSuffix(typeName, "Skill")
	if raw == "" {
		// The type was literally named "Skill"; return "skill" to avoid empty string.
		return "skill"
	}
	return util.SnakeCase(raw)
}
