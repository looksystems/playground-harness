// Package builtin — AST evaluator (M2.6).
//
// This file implements the fourth pass of the shell interpreter: walk the
// AST produced by the parser, resolve expansions via expansion.go, and
// dispatch commands through a BuiltinRegistry. The evaluator is the
// integration point that ties the tokenizer, parser, expansion, and
// builtin-handler layers together into a running shell.
//
// The Go port mirrors the Python reference at src/python/shell.py lines
// 787-1003 (`_eval_*` family) with the following structural differences,
// documented inline:
//
//   - AndOr is flat (Children+Ops) rather than a left-leaning binary
//     tree of AndNode/OrNode. The evaluator walks it left-to-right,
//     short-circuiting identically to Python.
//   - Assignments are carried on SimpleCommand.Assignments (not a
//     separate AssignmentNode type). A SimpleCommand with only
//     Assignments is treated exactly like Python's AssignmentNode path:
//     apply each assignment to the shell's Env, return empty
//     ExecResult. A SimpleCommand WITH a command word applies its
//     assignments only for the duration of that command (temporary
//     overlay), which matches bash semantics and Python's implicit
//     behaviour via separate AssignmentNode emission.
//   - Subshell (`(list)`) runs its body against a cloned ExecEnv (copy
//     of Env + CWD + ExitStatus) so mutations don't leak. The filesystem
//     is NOT cloned — Python's Shell doesn't clone fs for $(...), and
//     our virtual-bash reference doesn't treat subshell as a hard
//     sandbox.
//   - Command substitution recursion happens via Evaluator.Execute,
//     which resets the per-call iteration counter like Python's
//     _command_substitution does (saved/restored).
//
// Safety limits are threaded through two places: the evaluator owns the
// iteration counter (shared across all nested loops within one
// Execute), while ExpansionState owns the sub-depth cap and the
// per-expansion counter (reset at the start of each Execute).
package builtin

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// Default safety limits. Callers may override on Evaluator before
// Execute is called.
const (
	defaultEvalMaxIterations = 10_000
	defaultEvalMaxSubDepth   = 10
	defaultEvalMaxOutput     = 16_000
)

// Builtin is the evaluator-level handler signature. It receives the
// running ExecEnv so commands like `cd`, `export`, and `unset` can
// mutate shell state. Most builtins ignore ExecEnv and read
// args/stdin only.
type Builtin func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult

// ExecEnv is the mutable environment a running builtin can observe and
// modify. Fields are pointers/maps so a shell-affecting builtin can
// update state that the evaluator then re-reads.
//
// The evaluator constructs one ExecEnv per command invocation, pointing
// at its own CWD/Env/ExitStatus fields. Subshells receive a cloned
// ExecEnv so mutations don't leak to the parent.
type ExecEnv struct {
	// FS is the virtual filesystem. Builtins read/write through this.
	FS vfs.FilesystemDriver

	// CWD is a pointer to the evaluator's (or subshell's) CWD string
	// so `cd` can update it in place.
	CWD *string

	// Env is the shell's environment variables map. Writes via this
	// map are visible to the evaluator and future commands.
	Env map[string]string

	// ExitStatus is a pointer to the evaluator's exit status so
	// builtins that set `$?` directly (e.g. `cd` on failure) can do so.
	// Normal command return flows update this implicitly through the
	// returned ExecResult.
	ExitStatus *int
}

// BuiltinRegistry maps command names to evaluator-level Builtin
// handlers. It's safe for concurrent lookups and registrations.
// Handlers registered via RegisterUser use the public
// shell.CmdHandler shape (args, stdin) — those are wrapped
// internally so the evaluator can dispatch uniformly.
type BuiltinRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Builtin
}

// NewBuiltinRegistry constructs an empty BuiltinRegistry.
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{handlers: make(map[string]Builtin)}
}

// Register installs fn under name (replacing any prior entry). Returns
// the registry so calls can be chained.
func (r *BuiltinRegistry) Register(name string, fn Builtin) *BuiltinRegistry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = fn
	return r
}

// RegisterUser adapts a shell.CmdHandler into a Builtin and registers
// it. User handlers don't see ExecEnv — they get only args and stdin,
// mirroring shell.Driver.RegisterCommand.
func (r *BuiltinRegistry) RegisterUser(name string, fn shell.CmdHandler) *BuiltinRegistry {
	wrapped := func(_ context.Context, _ *ExecEnv, args []string, stdin string) shell.ExecResult {
		return fn(args, stdin)
	}
	return r.Register(name, wrapped)
}

// Unregister removes a command. No-op if absent. Returns the registry
// so calls can be chained.
func (r *BuiltinRegistry) Unregister(name string) *BuiltinRegistry {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handlers, name)
	return r
}

// Get returns the handler for name and whether it was found.
func (r *BuiltinRegistry) Get(name string) (Builtin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

// Names returns the sorted list of registered command names. Used by
// the driver for capability advertisement / debugging.
func (r *BuiltinRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Evaluator executes parsed AST and holds the shared state that
// persists across statements in a single Execute call. The evaluator is
// NOT safe for concurrent Execute calls; the driver (M2.8) serialises
// access with a mutex.
type Evaluator struct {
	// FS is the virtual filesystem used for redirections and by
	// builtins. Required — the zero-value evaluator can't run commands
	// that touch the filesystem; tests that don't use I/O may pass nil.
	FS vfs.FilesystemDriver

	// CWD is the shell's current working directory (absolute,
	// forward-slash). Updated by cd.
	CWD string

	// Env is the shell variable map. The evaluator stores $? here as
	// env["?"] so that both ExpansionState readers and test code can
	// introspect it from one place.
	Env map[string]string

	// Builtins is the dispatch registry. Construct with
	// NewBuiltinRegistry if nil at Execute time.
	Builtins *BuiltinRegistry

	// ExitStatus is the result of the most recently completed command.
	// Mirrored into Env["?"] after every dispatch.
	ExitStatus int

	// Safety limits. Zero means "use default".
	MaxIterations int
	MaxSubDepth   int
	MaxOutput     int

	// NotFoundHandler mirrors shell.Driver.NotFoundHandler — if
	// non-nil, called when a command name isn't in the registry.
	// Returning a non-nil *ExecResult supplies the response; returning
	// nil falls through to the default "command not found" reply.
	NotFoundHandler shell.NotFoundHandler

	// iterationCounter is the per-Execute counter shared across all
	// nested loops. Reset at the start of every Execute.
	iterationCounter int
}

// NewEvaluator constructs an Evaluator with sensible defaults:
// CWD="/", empty Env, fresh BuiltinRegistry, and the default safety
// limits. The caller can override any field before invoking Execute.
func NewEvaluator(fs vfs.FilesystemDriver) *Evaluator {
	return &Evaluator{
		FS:            fs,
		CWD:           "/",
		Env:           map[string]string{},
		Builtins:      NewBuiltinRegistry(),
		MaxIterations: defaultEvalMaxIterations,
		MaxSubDepth:   defaultEvalMaxSubDepth,
		MaxOutput:     defaultEvalMaxOutput,
	}
}

// effective accessors guard the zero-value case.
func (e *Evaluator) maxIterations() int {
	if e.MaxIterations > 0 {
		return e.MaxIterations
	}
	return defaultEvalMaxIterations
}

func (e *Evaluator) maxSubDepth() int {
	if e.MaxSubDepth > 0 {
		return e.MaxSubDepth
	}
	return defaultEvalMaxSubDepth
}

func (e *Evaluator) maxOutput() int {
	if e.MaxOutput > 0 {
		return e.MaxOutput
	}
	return defaultEvalMaxOutput
}

// Execute parses source, walks the resulting AST, and returns the
// aggregated result. This is the primary entry point used by the
// driver (M2.8).
//
// The returned error is always nil in the current implementation:
// tokenizer / parser failures are wrapped as ExecResult with exit code
// 2 and a stderr "parse error: ..." message, matching Python's exec()
// contract. The signature keeps `error` in case future work wants to
// surface non-shell errors (e.g. context cancellation) distinctly.
func (e *Evaluator) Execute(ctx context.Context, source string) (shell.ExecResult, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return shell.ExecResult{}, nil
	}

	tokens, err := Tokenize(source)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("parse error: %s\n", err.Error()),
			ExitCode: 2,
		}, nil
	}
	node, err := Parse(tokens)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("parse error: %s\n", err.Error()),
			ExitCode: 2,
		}, nil
	}
	return e.ExecuteAST(ctx, node)
}

// ExecuteAST evaluates an already-parsed AST node. Resets the
// iteration counter and applies the final MaxOutput cap before
// returning.
func (e *Evaluator) ExecuteAST(ctx context.Context, root Node) (shell.ExecResult, error) {
	if e.Env == nil {
		e.Env = map[string]string{}
	}
	if e.Builtins == nil {
		e.Builtins = NewBuiltinRegistry()
	}

	e.iterationCounter = 0

	result, err := e.evalNode(ctx, root, "")
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("%s\n", err.Error()),
			ExitCode: 1,
		}, nil
	}

	// Cap final stdout. Matches Python: only stdout is checked, and the
	// truncation marker names the full pre-truncation length so callers
	// can see how much was dropped.
	if cap := e.maxOutput(); cap > 0 && len(result.Stdout) > cap {
		truncated := result.Stdout[:cap] + fmt.Sprintf("\n... [truncated, %d total chars]", len(result.Stdout))
		result = shell.ExecResult{
			Stdout:   truncated,
			Stderr:   result.Stderr,
			ExitCode: result.ExitCode,
		}
	}
	return result, nil
}

// evalNode dispatches on node kind. Mirrors Python's _eval.
func (e *Evaluator) evalNode(ctx context.Context, node Node, stdin string) (shell.ExecResult, error) {
	if node == nil {
		return shell.ExecResult{}, nil
	}
	switch n := node.(type) {
	case *SimpleCommand:
		return e.evalSimpleCommand(ctx, n, stdin)
	case *Pipeline:
		return e.evalPipeline(ctx, n, stdin)
	case *List:
		return e.evalList(ctx, n, stdin)
	case *AndOr:
		return e.evalAndOr(ctx, n, stdin)
	case *If:
		return e.evalIf(ctx, n, stdin)
	case *For:
		return e.evalFor(ctx, n, stdin)
	case *While:
		return e.evalWhile(ctx, n, stdin)
	case *Case:
		return e.evalCase(ctx, n, stdin)
	case *Subshell:
		return e.evalSubshell(ctx, n, stdin)
	}
	return shell.ExecResult{}, nil
}

// newExpansionState constructs an ExpansionState over the evaluator's
// current env/cwd/exit-status. SubCommand is wired to a recursive
// Execute that preserves the iteration counter across the call (like
// Python's _command_substitution).
func (e *Evaluator) newExpansionState(ctx context.Context) *ExpansionState {
	st := &ExpansionState{
		Vars:        e.Env,
		CWD:         e.CWD,
		ExitStatus:  e.ExitStatus,
		MaxSubDepth: e.maxSubDepth(),
		MaxVarSize:  defaultMaxVarSize,
		FS:          e.FS,
		SubCommand: func(ctx context.Context, cmd string) (string, error) {
			saved := e.iterationCounter
			// Disable the final MaxOutput cap for the inner exec so a
			// substituted command doesn't yield the truncation
			// footer inside the parent command line. Python's
			// _command_substitution calls exec(), which IS subject to
			// max_output — we match that but keep the cap reasonable.
			res, err := e.Execute(ctx, cmd)
			e.iterationCounter = saved
			if err != nil {
				return "", err
			}
			out := res.Stdout
			// Strip exactly one trailing newline (POSIX); the
			// expander wraps this helper but is nil-safe about the
			// strip, so the double-strip is a no-op.
			return strings.TrimRight(out, "\n"), nil
		},
	}
	return st
}

// ---------------------------------------------------------------------------
// SimpleCommand
// ---------------------------------------------------------------------------

// evalSimpleCommand dispatches one command. There are three shapes:
//
//  1. Assignments only, no Words: apply each assignment to e.Env
//     permanently, return empty ExecResult (ExitCode=0).
//  2. Redirections only (rare; e.g. `> out.txt`): open target for
//     writing then return empty ExecResult. Python handles this by
//     returning empty ExecResult() early when args is empty — we
//     match that.
//  3. Words (with or without Assignments): expand words, overlay
//     Assignments onto env for the duration of the command, dispatch
//     to handler, apply redirections on the result.
func (e *Evaluator) evalSimpleCommand(ctx context.Context, n *SimpleCommand, stdin string) (shell.ExecResult, error) {
	state := e.newExpansionState(ctx)

	// (1) Assignment-only form.
	if len(n.Words) == 0 {
		// Python's _eval_command returns an empty ExecResult when
		// both args and redirects are empty. If there ARE
		// redirects but no words, Python applies them... but the
		// only way to reach that path is a command like `> foo`,
		// which our parser emits as a SimpleCommand with empty
		// Words, empty Assignments, and redirects. Python's
		// behaviour there is to fall through to the top of
		// _eval_command which returns ExecResult() — so we match.
		if len(n.Assignments) == 0 {
			return shell.ExecResult{}, nil
		}
		for _, a := range n.Assignments {
			parts, err := ExpandWord(ctx, state, a.Value)
			if err != nil {
				return shell.ExecResult{}, err
			}
			val := ""
			if len(parts) > 0 {
				val = parts[0]
			}
			state.SetVar(a.Name, val)
		}
		// Assignment-only leaves $? unchanged (bash). Python keeps
		// env["?"] as whatever it was — we do the same.
		return shell.ExecResult{}, nil
	}

	// (2) Assignments + words: temporarily overlay assignments onto
	// env for the duration of this command. Python's AST distinguishes
	// AssignmentNode (bare) from CommandNode-with-leading-assignments
	// and the latter path isn't wired up in the reference — but bash
	// semantics clearly require a temporary overlay, and the task spec
	// explicitly asks for this behaviour.
	savedVals := make(map[string]string, len(n.Assignments))
	savedSet := make(map[string]bool, len(n.Assignments))
	for _, a := range n.Assignments {
		parts, err := ExpandWord(ctx, state, a.Value)
		if err != nil {
			e.restoreOverlay(savedVals, savedSet)
			return shell.ExecResult{}, err
		}
		val := ""
		if len(parts) > 0 {
			val = parts[0]
		}
		if v, ok := e.Env[a.Name]; ok {
			savedVals[a.Name] = v
			savedSet[a.Name] = true
		} else {
			savedSet[a.Name] = false
		}
		// Overlay the assignment directly on e.Env so
		// state.Vars (which aliases the same map) sees it during
		// expansion of the subsequent args.
		capped := val
		if cap := defaultMaxVarSize; cap > 0 && len(capped) > cap {
			capped = capped[:cap]
		}
		e.Env[a.Name] = capped
	}
	defer e.restoreOverlay(savedVals, savedSet)

	// Expand command-line words.
	expanded, err := ExpandWords(ctx, state, n.Words)
	if err != nil {
		return shell.ExecResult{}, err
	}
	// Python splits expanded args only to drop empties produced by
	// variable expansions like `$EMPTY`. We mimic: skip empty strings.
	words := make([]string, 0, len(expanded))
	for _, w := range expanded {
		if w != "" {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return shell.ExecResult{}, nil
	}

	cmdName := words[0]
	args := words[1:]

	// Process input redirections BEFORE dispatch so the handler sees
	// the correct stdin.
	effectiveStdin := stdin
	for _, r := range n.Redirections {
		if r.Kind != RedirIn {
			continue
		}
		target, err := e.expandRedirTarget(ctx, state, r.Target)
		if err != nil {
			return shell.ExecResult{}, err
		}
		abs := e.resolvePath(target)
		if e.FS == nil || !e.FS.Exists(abs) {
			e.setExitStatus(1)
			return shell.ExecResult{
				Stderr:   fmt.Sprintf("%s: No such file or directory\n", target),
				ExitCode: 1,
			}, nil
		}
		s, rerr := e.FS.ReadString(abs)
		if rerr != nil {
			e.setExitStatus(1)
			return shell.ExecResult{
				Stderr:   fmt.Sprintf("%s: %s\n", target, rerr.Error()),
				ExitCode: 1,
			}, nil
		}
		effectiveStdin = s
	}

	// Dispatch.
	handler, found := e.Builtins.Get(cmdName)
	var result shell.ExecResult
	if !found {
		if e.NotFoundHandler != nil {
			custom := e.NotFoundHandler(ctx, cmdName, args, effectiveStdin)
			if custom != nil {
				result = *custom
			} else {
				result = shell.ExecResult{
					Stderr:   fmt.Sprintf("%s: command not found\n", cmdName),
					ExitCode: 127,
				}
			}
		} else {
			result = shell.ExecResult{
				Stderr:   fmt.Sprintf("%s: command not found\n", cmdName),
				ExitCode: 127,
			}
		}
	} else {
		execEnv := &ExecEnv{
			FS:         e.FS,
			CWD:        &e.CWD,
			Env:        e.Env,
			ExitStatus: &e.ExitStatus,
		}
		result = handler(ctx, execEnv, args, effectiveStdin)
	}

	e.setExitStatus(result.ExitCode)

	// Output redirections apply after the handler runs.
	for _, r := range n.Redirections {
		if r.Kind == RedirIn {
			continue
		}
		if r.Kind == RedirErrOut {
			// 2>&1: merge stderr into stdout, clear stderr.
			result = shell.ExecResult{
				Stdout:   result.Stdout + result.Stderr,
				Stderr:   "",
				ExitCode: result.ExitCode,
			}
			continue
		}
		target, err := e.expandRedirTarget(ctx, state, r.Target)
		if err != nil {
			return shell.ExecResult{}, err
		}
		abs := e.resolvePath(target)

		switch r.Kind {
		case RedirOut:
			if werr := e.writeFile(abs, result.Stdout, false); werr != nil {
				return shell.ExecResult{}, werr
			}
			result = shell.ExecResult{Stderr: result.Stderr, ExitCode: result.ExitCode}
		case RedirAppend:
			if werr := e.writeFile(abs, result.Stdout, true); werr != nil {
				return shell.ExecResult{}, werr
			}
			result = shell.ExecResult{Stderr: result.Stderr, ExitCode: result.ExitCode}
		case RedirErr:
			if werr := e.writeFile(abs, result.Stderr, false); werr != nil {
				return shell.ExecResult{}, werr
			}
			result = shell.ExecResult{Stdout: result.Stdout, ExitCode: result.ExitCode}
		case RedirErrAppend:
			if werr := e.writeFile(abs, result.Stderr, true); werr != nil {
				return shell.ExecResult{}, werr
			}
			result = shell.ExecResult{Stdout: result.Stdout, ExitCode: result.ExitCode}
		case RedirBoth:
			if werr := e.writeFile(abs, result.Stdout+result.Stderr, false); werr != nil {
				return shell.ExecResult{}, werr
			}
			result = shell.ExecResult{ExitCode: result.ExitCode}
		}
	}

	return result, nil
}

// restoreOverlay rolls back the temporary assignment overlay.
func (e *Evaluator) restoreOverlay(savedVals map[string]string, savedSet map[string]bool) {
	for name, hadBefore := range savedSet {
		if hadBefore {
			e.Env[name] = savedVals[name]
		} else {
			delete(e.Env, name)
		}
	}
}

// setExitStatus updates both the numeric field and env["?"] (so $?
// expansion sees the freshest value).
func (e *Evaluator) setExitStatus(code int) {
	e.ExitStatus = code
	if e.Env != nil {
		e.Env["?"] = strconv.Itoa(code)
	}
}

// expandRedirTarget runs ExpandWord against the single Word used as a
// redirection target and returns the joined string. Empty expansions
// yield "".
func (e *Evaluator) expandRedirTarget(ctx context.Context, state *ExpansionState, w Word) (string, error) {
	parts, err := ExpandWord(ctx, state, w)
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], nil
}

// resolvePath joins path against CWD using forward-slash semantics.
// Matches Python's _resolve (os.path.normpath over "/"-joined).
func (e *Evaluator) resolvePath(p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	return path.Clean(path.Join(e.CWD, p))
}

// writeFile writes content to p, optionally appending to existing
// content. Uses the FS driver; returns an error if write fails.
func (e *Evaluator) writeFile(p, content string, appendMode bool) error {
	if e.FS == nil {
		return fmt.Errorf("redirection requires a filesystem")
	}
	if appendMode && e.FS.Exists(p) {
		existing, err := e.FS.ReadString(p)
		if err != nil {
			return err
		}
		return e.FS.WriteString(p, existing+content)
	}
	return e.FS.WriteString(p, content)
}

// ---------------------------------------------------------------------------
// Pipeline / List / AndOr
// ---------------------------------------------------------------------------

// evalPipeline runs commands left-to-right, stitching stdout → stdin.
// All stderr accumulates. Exit code is the LAST command's.
func (e *Evaluator) evalPipeline(ctx context.Context, n *Pipeline, stdin string) (shell.ExecResult, error) {
	currentStdin := stdin
	var lastResult shell.ExecResult
	var allStderr strings.Builder
	for _, cmd := range n.Commands {
		// Honour cancellation between pipeline stages so callers can
		// interrupt a multi-stage pipeline without waiting for the
		// next (potentially long-running) builtin to finish. We
		// preserve the accumulated stderr so the caller still sees
		// what was produced before cancellation.
		if err := ctx.Err(); err != nil {
			return shell.ExecResult{
				Stdout:   lastResult.Stdout,
				Stderr:   allStderr.String() + err.Error() + "\n",
				ExitCode: 130,
			}, nil
		}
		res, err := e.evalNode(ctx, cmd, currentStdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		allStderr.WriteString(res.Stderr)
		currentStdin = res.Stdout
		lastResult = res
	}
	e.setExitStatus(lastResult.ExitCode)
	return shell.ExecResult{
		Stdout:   lastResult.Stdout,
		Stderr:   allStderr.String(),
		ExitCode: lastResult.ExitCode,
	}, nil
}

// evalList runs statements sequentially, accumulating stdout+stderr
// across all. Exit code is the LAST statement's.
func (e *Evaluator) evalList(ctx context.Context, n *List, stdin string) (shell.ExecResult, error) {
	var out strings.Builder
	var errb strings.Builder
	exit := 0
	for _, s := range n.Statements {
		// Honour cancellation between statements so an interrupted
		// script returns its partial output instead of silently
		// running every remaining command.
		if err := ctx.Err(); err != nil {
			errb.WriteString(err.Error() + "\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 130,
			}, nil
		}
		r, err := e.evalNode(ctx, s, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		out.WriteString(r.Stdout)
		errb.WriteString(r.Stderr)
		exit = r.ExitCode
	}
	return shell.ExecResult{
		Stdout:   out.String(),
		Stderr:   errb.String(),
		ExitCode: exit,
	}, nil
}

// evalAndOr walks the flat chain left-to-right, short-circuiting.
// Matches Python's nested AndNode/OrNode behaviour under
// left-associative flattening: accumulate stdout/stderr across all
// evaluated (non-skipped) branches.
func (e *Evaluator) evalAndOr(ctx context.Context, n *AndOr, stdin string) (shell.ExecResult, error) {
	if len(n.Children) == 0 {
		return shell.ExecResult{}, nil
	}
	// Run the first child.
	res, err := e.evalNode(ctx, n.Children[0], stdin)
	if err != nil {
		return shell.ExecResult{}, err
	}
	var out strings.Builder
	var errb strings.Builder
	out.WriteString(res.Stdout)
	errb.WriteString(res.Stderr)
	exit := res.ExitCode
	for i, op := range n.Ops {
		// Honour cancellation between chained commands — long &&/||
		// chains should not continue running after ctx is cancelled.
		if err := ctx.Err(); err != nil {
			errb.WriteString(err.Error() + "\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 130,
			}, nil
		}
		runNext := false
		if op == OpAnd && exit == 0 {
			runNext = true
		}
		if op == OpOr && exit != 0 {
			runNext = true
		}
		if !runNext {
			continue
		}
		next, err := e.evalNode(ctx, n.Children[i+1], stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		out.WriteString(next.Stdout)
		errb.WriteString(next.Stderr)
		exit = next.ExitCode
	}
	return shell.ExecResult{
		Stdout:   out.String(),
		Stderr:   errb.String(),
		ExitCode: exit,
	}, nil
}

// ---------------------------------------------------------------------------
// Control flow: If / For / While / Case / Subshell
// ---------------------------------------------------------------------------

// evalIf walks the if/elif/else chain. The first condition that exits 0
// wins; its body runs. If no condition matches, Else runs (if set).
// Condition stdout is accumulated with body stdout in the winning
// branch, mirroring Python.
func (e *Evaluator) evalIf(ctx context.Context, n *If, stdin string) (shell.ExecResult, error) {
	// Condition.
	cond, err := e.evalNode(ctx, n.Cond, stdin)
	if err != nil {
		return shell.ExecResult{}, err
	}
	if cond.ExitCode == 0 {
		body, err := e.evalNode(ctx, n.Then, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		return shell.ExecResult{
			Stdout:   cond.Stdout + body.Stdout,
			Stderr:   cond.Stderr + body.Stderr,
			ExitCode: body.ExitCode,
		}, nil
	}
	// Elifs.
	for _, clause := range n.Elifs {
		c, err := e.evalNode(ctx, clause.Cond, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		if c.ExitCode == 0 {
			body, err := e.evalNode(ctx, clause.Then, stdin)
			if err != nil {
				return shell.ExecResult{}, err
			}
			return shell.ExecResult{
				Stdout:   c.Stdout + body.Stdout,
				Stderr:   c.Stderr + body.Stderr,
				ExitCode: body.ExitCode,
			}, nil
		}
	}
	// Else.
	if n.Else != nil {
		return e.evalNode(ctx, n.Else, stdin)
	}
	return shell.ExecResult{}, nil
}

// evalFor iterates over Items, setting Var to each item before
// running Body. The positional form (Items==nil) is currently an empty
// iteration — positional parameters aren't supported per the
// virtual-bash reference doc's Notable Omissions and neither the
// expander nor any builtin reads them.
func (e *Evaluator) evalFor(ctx context.Context, n *For, stdin string) (shell.ExecResult, error) {
	state := e.newExpansionState(ctx)

	var items []string
	if n.Items != nil {
		// Python splits each expanded word on whitespace — matches
		// bash's behaviour where `for i in $LIST` spreads a
		// multi-word variable into separate loop iterations.
		for _, w := range n.Items {
			parts, err := ExpandWord(ctx, state, w)
			if err != nil {
				return shell.ExecResult{}, err
			}
			for _, p := range parts {
				for _, f := range strings.Fields(p) {
					if f != "" {
						items = append(items, f)
					}
				}
			}
		}
	}

	var out strings.Builder
	var errb strings.Builder
	exit := 0
	for _, item := range items {
		// Honour cancellation at the top of each iteration so
		// interrupting a long loop returns partial output with exit
		// code 130 (SIGINT convention), not the last body's exit.
		if err := ctx.Err(); err != nil {
			errb.WriteString(err.Error() + "\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 130,
			}, nil
		}
		e.iterationCounter++
		if e.iterationCounter > e.maxIterations() {
			errb.WriteString("Maximum iteration limit exceeded\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 1,
			}, nil
		}
		e.Env[n.Var] = item
		r, err := e.evalNode(ctx, n.Body, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		out.WriteString(r.Stdout)
		errb.WriteString(r.Stderr)
		exit = r.ExitCode
	}
	return shell.ExecResult{
		Stdout:   out.String(),
		Stderr:   errb.String(),
		ExitCode: exit,
	}, nil
}

// evalWhile runs Body while Cond exits 0 (for while) or !=0 (for
// until). Both forms share the same iteration cap.
func (e *Evaluator) evalWhile(ctx context.Context, n *While, stdin string) (shell.ExecResult, error) {
	var out strings.Builder
	var errb strings.Builder
	exit := 0
	for {
		// Honour cancellation at the top of each iteration.
		if err := ctx.Err(); err != nil {
			errb.WriteString(err.Error() + "\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 130,
			}, nil
		}
		e.iterationCounter++
		if e.iterationCounter > e.maxIterations() {
			errb.WriteString("Maximum iteration limit exceeded\n")
			return shell.ExecResult{
				Stdout:   out.String(),
				Stderr:   errb.String(),
				ExitCode: 1,
			}, nil
		}
		cond, err := e.evalNode(ctx, n.Cond, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		// While: stop when cond fails. Until: stop when cond succeeds.
		if !n.Until && cond.ExitCode != 0 {
			break
		}
		if n.Until && cond.ExitCode == 0 {
			break
		}
		body, err := e.evalNode(ctx, n.Body, stdin)
		if err != nil {
			return shell.ExecResult{}, err
		}
		out.WriteString(body.Stdout)
		errb.WriteString(body.Stderr)
		exit = body.ExitCode
	}
	return shell.ExecResult{
		Stdout:   out.String(),
		Stderr:   errb.String(),
		ExitCode: exit,
	}, nil
}

// evalCase matches Word against each clause's Patterns in order. The
// first match wins; patterns expand (so `$VAR)` works) and glob-to-regex
// via expansion.go. A wildcard "*" always matches.
func (e *Evaluator) evalCase(ctx context.Context, n *Case, stdin string) (shell.ExecResult, error) {
	state := e.newExpansionState(ctx)
	parts, err := ExpandWord(ctx, state, n.Word)
	if err != nil {
		return shell.ExecResult{}, err
	}
	word := ""
	if len(parts) > 0 {
		word = parts[0]
	}
	for _, clause := range n.Clauses {
		for _, pat := range clause.Patterns {
			pparts, err := ExpandWord(ctx, state, pat)
			if err != nil {
				return shell.ExecResult{}, err
			}
			expanded := ""
			if len(pparts) > 0 {
				expanded = pparts[0]
			}
			if expanded == "*" || globToRegex(expanded).MatchString(word) {
				if clause.Body == nil {
					return shell.ExecResult{}, nil
				}
				return e.evalNode(ctx, clause.Body, stdin)
			}
		}
	}
	return shell.ExecResult{}, nil
}

// evalSubshell runs Body against a cloned ExecEnv so mutations
// (assignments, cd, exit status) don't leak to the parent. The
// filesystem is NOT cloned — see package-level notes.
func (e *Evaluator) evalSubshell(ctx context.Context, n *Subshell, stdin string) (shell.ExecResult, error) {
	if n.Body == nil {
		return shell.ExecResult{}, nil
	}
	// Clone env + cwd.
	clonedEnv := make(map[string]string, len(e.Env))
	for k, v := range e.Env {
		clonedEnv[k] = v
	}
	sub := &Evaluator{
		FS:               e.FS,
		CWD:              e.CWD,
		Env:              clonedEnv,
		Builtins:         e.Builtins,
		ExitStatus:       e.ExitStatus,
		MaxIterations:    e.MaxIterations,
		MaxSubDepth:      e.MaxSubDepth,
		MaxOutput:        e.MaxOutput,
		NotFoundHandler:  e.NotFoundHandler,
		iterationCounter: e.iterationCounter,
	}
	res, err := sub.evalNode(ctx, n.Body, stdin)
	// Propagate the iteration counter back so the parent sees the
	// work done in the subshell (safety caps are global per
	// Execute, not scoped per-subshell).
	e.iterationCounter = sub.iterationCounter
	return res, err
}
