package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"agent-harness/go/hooks"
	"agent-harness/go/tools"
)

// Host embeds a shell Driver into an Agent.
//
// It:
//   - owns the underlying shell.Driver;
//   - emits SHELL_* hooks (shell_call, shell_result, shell_cwd, shell_not_found,
//     command_register, command_unregister) on the wrapped calls;
//   - auto-registers a single `exec` tool describing the shell, so the LLM
//     can invoke `exec(command=...)` to run shell commands.
//
// A Host is the Go port of Python's HasShell mixin (src/python/has_shell.py).
//
// Concurrency: Host itself holds a RWMutex around the Hub pointer so SetHub
// is safe with concurrent Exec. The Driver contract is concurrent-safe by
// convention.
type Host struct {
	// Driver is the active shell backend. Swap by assigning directly if
	// needed; the not-found handler wired by NewHost stays attached to the
	// original driver instance and will not follow the swap. Users who need
	// a different driver should construct a new Host.
	Driver Driver

	mu  sync.RWMutex
	hub *hooks.Hub
}

// NewHost constructs a host around a Driver. If driver is nil the package
// default factory is consulted for a driver (typically the builtin shell).
// If no default is registered an ExecResult-less fallback Host is returned
// — the caller should supply a Driver before using Exec.
func NewHost(driver Driver) *Host {
	if driver == nil {
		d, err := DefaultFactory.Create("", nil)
		if err == nil {
			driver = d
		}
	}
	h := &Host{Driver: driver}
	if driver != nil {
		// Wire the not-found handler so SHELL_NOT_FOUND fires whenever the
		// driver encounters an unknown command.
		driver.SetNotFoundHandler(h.makeNotFoundHandler())
	}
	return h
}

// SetHub plumbs in the hook hub (called by the Agent constructor once it
// has both subsystems). Passing nil disables hook emission.
func (h *Host) SetHub(hub *hooks.Hub) {
	h.mu.Lock()
	h.hub = hub
	h.mu.Unlock()
}

// Exec is the primary user-facing entry point. It emits the SHELL_* hook
// surface around the underlying Driver.Exec call. On CWD change, also emits
// ShellCwd with (oldCWD, newCWD).
//
// Hooks fire via EmitAsync (fire-and-forget) to match Python's
// emit_fire_and_forget behaviour — hook handlers must never block the exec
// path.
func (h *Host) Exec(ctx context.Context, command string) (ExecResult, error) {
	if h.Driver == nil {
		return ExecResult{}, fmt.Errorf("shell.Host: no Driver configured")
	}

	h.emit(ctx, hooks.ShellCall, command)

	oldCWD := h.Driver.CWD()
	result, err := h.Driver.Exec(ctx, command)
	// Even on error, emit SHELL_RESULT with whatever result the driver
	// produced — matches Python which emits post-exec unconditionally.
	h.emit(ctx, hooks.ShellResult, command, result)

	newCWD := h.Driver.CWD()
	if newCWD != oldCWD {
		h.emit(ctx, hooks.ShellCwd, oldCWD, newCWD)
	}

	return result, err
}

// RegisterCommand registers a custom command and emits CommandRegister
// asynchronously. Wraps Driver.RegisterCommand.
func (h *Host) RegisterCommand(name string, handler CmdHandler) {
	if h.Driver == nil {
		return
	}
	h.Driver.RegisterCommand(name, handler)
	h.emit(context.Background(), hooks.CommandRegister, name)
}

// UnregisterCommand removes a previously registered command and emits
// CommandUnregister.
func (h *Host) UnregisterCommand(name string) {
	if h.Driver == nil {
		return
	}
	h.Driver.UnregisterCommand(name)
	h.emit(context.Background(), hooks.CommandUnregister, name)
}

// emit is a thin wrapper around Hub.EmitAsync that no-ops when the hub
// is nil.
func (h *Host) emit(ctx context.Context, event hooks.Event, args ...any) {
	h.mu.RLock()
	hub := h.hub
	h.mu.RUnlock()
	if hub == nil {
		return
	}
	hub.EmitAsync(ctx, event, args...)
}

// makeNotFoundHandler builds a NotFoundHandler closure bound to this Host
// that emits ShellNotFound with (cmd, args, stdin) and always falls through
// to the driver's default "command not found" behaviour.
func (h *Host) makeNotFoundHandler() NotFoundHandler {
	return func(ctx context.Context, cmd string, args []string, stdin string) *ExecResult {
		h.emit(ctx, hooks.ShellNotFound, cmd, args, stdin)
		return nil
	}
}

// ---------------------------------------------------------------------------
// exec tool (auto-registered on the Agent)
// ---------------------------------------------------------------------------

// shellToolDescription is the canonical description string advertised to
// the LLM. It mirrors Python's HasShell._register_shell_tool description
// verbatim so cross-language parity tests can compare byte-for-byte (with
// minor Go-idiomatic phrasing tweaks where relevant).
const shellToolDescription = "Execute a bash command in the virtual filesystem. " +
	"Commands: ls, cat, grep, find, head, tail, wc, sort, uniq, " +
	"cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, " +
	"echo, stat, test, printf, true, false. " +
	"Operators: pipes (|), redirects (>, >>), && (and), || (or), ; (sequence). " +
	"Flow control: if/then/elif/else/fi, for/in/do/done, while/do/done, case/in/esac. " +
	"Features: VAR=assignment, $(cmd) substitution, $((expr)) arithmetic, " +
	"${var:-default} expansion, ${var:=default}, ${#var}, " +
	"${var:offset:length}, ${var//pat/repl}, ${var%suffix}, ${var##prefix}. " +
	"Custom commands registered via RegisterCommand() are also available."

// shellToolArgs mirrors Python's {"command": "..."} payload.
type shellToolArgs struct {
	Command string `json:"command"`
}

// ShellTool returns the `exec` tools.Def the LLM uses to run shell commands.
// The handler calls Host.Exec and formats the ExecResult using the same
// rules as Python's _register_shell_tool:
//
//   - Stdout non-empty → append verbatim.
//   - Stderr non-empty → append "[stderr] <stderr>".
//   - Non-zero exit → append "[exit code: N]".
//   - If the above leaves the output empty, return "(no output)".
func (h *Host) ShellTool() tools.Def {
	return tools.Def{
		Name:        "exec",
		Description: shellToolDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
			},
			"required": []string{"command"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var args shellToolArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("shell: exec tool: unmarshal args: %w", err)
			}
			result, err := h.Exec(ctx, args.Command)
			if err != nil {
				return nil, err
			}
			return formatExecResult(result), nil
		},
	}
}

// formatExecResult renders an ExecResult the same way Python's exec tool
// handler does. Kept as a package-level helper so both the tool and tests
// can call it directly.
func formatExecResult(r ExecResult) string {
	var out string
	if r.Stdout != "" {
		out += r.Stdout
	}
	if r.Stderr != "" {
		out += "[stderr] " + r.Stderr
	}
	if r.ExitCode != 0 {
		out += fmt.Sprintf("[exit code: %d]", r.ExitCode)
	}
	if out == "" {
		return "(no output)"
	}
	return out
}
