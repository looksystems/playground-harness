// Package shell defines the Driver interface, ExecResult, streaming types,
// and related contracts that every shell backend must satisfy.
package shell

import (
	"context"
	"errors"

	"agent-harness/go/shell/vfs"
)

// ExecResult is the aggregated result of a synchronous shell execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// StreamKind identifies the kind of an ExecStreamEvent.
type StreamKind int

const (
	StreamStdout StreamKind = iota
	StreamStderr
	StreamExit
)

// ExecStreamEvent is one event on the streaming execution channel.
type ExecStreamEvent struct {
	Kind     StreamKind
	Data     string // populated for StreamStdout / StreamStderr
	ExitCode int    // populated for StreamExit
}

// CmdHandler is the signature registered via Driver.RegisterCommand.
// args are the positional arguments after the command name; stdin is the
// piped input (empty string if none). The handler returns a synchronous
// ExecResult — matching Python's Callable[[list[str], str], ExecResult].
type CmdHandler func(args []string, stdin string) ExecResult

// Capability strings advertised by Driver.Capabilities.
const (
	CapCustomCommands = "custom_commands"
	CapStateful       = "stateful"
	CapStreaming       = "streaming"
	CapPolicies       = "policies"
	CapRemote         = "remote"
)

// ErrStreamingUnsupported is returned by ExecStream on drivers that do not
// support streaming execution.
var ErrStreamingUnsupported = errors.New("shell: streaming execution not supported by this driver")

// NotFoundHandler is called when a command is not found. Return a non-nil
// *ExecResult to supply a custom response; return nil to fall through to the
// driver's default behaviour (typically "<cmd>: command not found" on stderr
// with exit code 127).
type NotFoundHandler func(ctx context.Context, cmd string, args []string, stdin string) *ExecResult

// Driver is the contract every shell backend must satisfy.
type Driver interface {
	// FS returns the filesystem backing this shell.
	FS() vfs.FilesystemDriver

	// CWD returns the current working directory (absolute, forward-slash).
	CWD() string

	// Env returns a copy of the environment variables map.
	Env() map[string]string

	// Exec runs command and returns the aggregated result. ctx cancels
	// execution; drivers that cannot cancel mid-command should still honour
	// ctx at call boundaries.
	Exec(ctx context.Context, command string) (ExecResult, error)

	// ExecStream runs command and returns a read-only channel of events.
	// The channel is producer-owned and closed when the command finishes.
	// Drivers that do not support streaming return ErrStreamingUnsupported.
	ExecStream(ctx context.Context, command string) (<-chan ExecStreamEvent, error)

	// RegisterCommand adds (or replaces) a custom command handler.
	RegisterCommand(name string, handler CmdHandler)

	// UnregisterCommand removes a previously registered custom command.
	UnregisterCommand(name string)

	// Clone returns an independent driver with cloned state (fs, env, cwd,
	// registered commands, and not-found handler).
	Clone() Driver

	// NotFoundHandler returns the current not-found handler (may be nil).
	NotFoundHandler() NotFoundHandler

	// SetNotFoundHandler replaces the not-found handler. Pass nil to restore
	// default behaviour.
	SetNotFoundHandler(NotFoundHandler)

	// Capabilities advertises which optional features the driver supports.
	// Keys are the Cap* constants; only present-and-true entries are active.
	Capabilities() map[string]bool
}
