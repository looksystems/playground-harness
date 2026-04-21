// Package bashkit implements a shell.Driver backed by the `bashkit` CLI.
//
// Each Exec spawns a fresh `bashkit -c '<command>'` subprocess (stateless,
// mirroring the TypeScript and PHP ports). VFS state is round-tripped
// across the subprocess boundary via the remotesync preamble/epilogue
// protocol: dirty files are materialised on disk by the preamble, the
// user command runs, and the epilogue prints a unique marker followed by
// a base64-encoded listing of every file under "/". The driver parses
// that listing and writes it back into the in-memory VFS.
//
// Shell state (variables, functions, cwd) does NOT persist between Exec
// calls — each call is an independent subprocess. Drivers that need
// persistence across commands should use the OpenShell driver instead.
package bashkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"agent-harness/go/shell"
	"agent-harness/go/shell/remotesync"
	"agent-harness/go/shell/vfs"
)

// defaultBinary is the CLI name we spawn when no WithBinary option is
// provided. It is resolved against $PATH at Exec time.
const defaultBinary = "bashkit"

// defaultTimeout bounds a single subprocess invocation; chosen to match
// the TS/Python `subprocess.run(timeout=30)` reference.
const defaultTimeout = 30 * time.Second

// rawResult mirrors the shape used by openshell's transports: the
// decoupled stdout/stderr/exitCode tuple both the production and fake
// shellRunner agree on before we wrap it into a shell.ExecResult.
type rawResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// shellRunner is the indirection the driver uses to invoke the bashkit
// CLI. Extracted so tests can script responses without a real binary on
// disk (mirrors the sshTransport pattern in openshell/ssh.go).
type shellRunner interface {
	// Run executes command with `<binary> -c '<command>'`. env is merged
	// into the subprocess environment on top of os.Environ(). Timeout is
	// enforced via the supplied context or an internal deadline (runners
	// decide; the default runner uses exec.CommandContext).
	Run(ctx context.Context, binary, command string, env map[string]string) (rawResult, error)
}

// Option configures a Driver at construction time.
type Option func(*Driver)

// WithBinary overrides the CLI binary name/path. Defaults to "bashkit".
func WithBinary(path string) Option {
	return func(d *Driver) {
		if path != "" {
			d.binary = path
		}
	}
}

// WithFS overrides the local virtual filesystem. When nil (default) the
// driver constructs a fresh vfs.BuiltinFilesystemDriver. The provided FS
// is wrapped in a DirtyTrackingFS so the driver can observe writes.
func WithFS(fs vfs.FilesystemDriver) Option {
	return func(d *Driver) {
		if fs == nil {
			return
		}
		d.fs = vfs.NewDirtyTrackingFS(fs)
	}
}

// WithCWD sets the driver's current working directory (defaults to "/").
// The cwd is reported via Driver.CWD but is not forwarded to the
// subprocess — bashkit always runs with its own cwd.
func WithCWD(cwd string) Option {
	return func(d *Driver) {
		if cwd != "" {
			d.cwd = cwd
		}
	}
}

// WithEnv seeds the environment map. A defensive copy is made.
func WithEnv(env map[string]string) Option {
	return func(d *Driver) {
		m := make(map[string]string, len(env))
		for k, v := range env {
			m[k] = v
		}
		d.env = m
	}
}

// WithTimeout sets the per-exec subprocess timeout. Zero or negative
// values fall back to the default (30s).
func WithTimeout(dur time.Duration) Option {
	return func(d *Driver) {
		if dur > 0 {
			d.timeout = dur
		}
	}
}

// withRunner injects a scripted shellRunner (for tests). Intentionally
// unexported — production code uses the default exec-backed runner.
func withRunner(r shellRunner) Option {
	return func(d *Driver) { d.runner = r }
}

// Driver executes commands by spawning the bashkit CLI. Implements
// shell.Driver.
type Driver struct {
	mu sync.Mutex

	binary  string
	timeout time.Duration

	fs       *vfs.DirtyTrackingFS
	cwd      string
	env      map[string]string
	commands map[string]shell.CmdHandler
	notFound shell.NotFoundHandler

	runner shellRunner
}

// New constructs a Driver. Binary availability is not checked up front —
// the first Exec that can't locate `bashkit` returns a clear error,
// matching the Python/TS "resolve lazily" approach.
func New(opts ...Option) *Driver {
	d := &Driver{
		binary:   defaultBinary,
		timeout:  defaultTimeout,
		fs:       vfs.NewDirtyTrackingFS(vfs.NewBuiltinFilesystemDriver()),
		cwd:      "/",
		env:      make(map[string]string),
		commands: make(map[string]shell.CmdHandler),
	}
	for _, o := range opts {
		o(d)
	}
	if d.runner == nil {
		d.runner = defaultRunner{}
	}
	return d
}

// -------------------------------------------------------------------------
// shell.Driver contract
// -------------------------------------------------------------------------

// FS returns the local (dirty-tracking) filesystem backing this driver.
func (d *Driver) FS() vfs.FilesystemDriver {
	return d.fs
}

// CWD returns the current working directory.
func (d *Driver) CWD() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cwd
}

// Env returns a defensive copy of the environment map.
func (d *Driver) Env() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := make(map[string]string, len(d.env))
	for k, v := range d.env {
		m[k] = v
	}
	return m
}

// RegisterCommand adds (or replaces) a local custom command handler.
// Custom commands are resolved by first-word match in Exec *before* the
// command is forwarded to the subprocess — they do not participate in
// pipelines, matching the TypeScript/Python drivers.
func (d *Driver) RegisterCommand(name string, handler shell.CmdHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.commands[name] = handler
}

// UnregisterCommand removes a previously registered custom command.
func (d *Driver) UnregisterCommand(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.commands, name)
}

// NotFoundHandler returns the current not-found handler, or nil.
func (d *Driver) NotFoundHandler() shell.NotFoundHandler {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.notFound
}

// SetNotFoundHandler replaces the not-found handler; pass nil to clear.
func (d *Driver) SetNotFoundHandler(h shell.NotFoundHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.notFound = h
}

// Capabilities advertises the driver's supported optional features.
// Bashkit-CLI is stateless (subprocess per exec) and non-streaming; only
// custom-command intercept and "remote" (subprocess boundary) apply.
func (d *Driver) Capabilities() map[string]bool {
	return map[string]bool{
		shell.CapCustomCommands: true,
		shell.CapRemote:         true,
	}
}

// Clone returns an independent driver with cloned VFS + env + commands.
// The runner, binary path, and timeout are shared (stateless config).
func (d *Driver) Clone() shell.Driver {
	d.mu.Lock()
	defer d.mu.Unlock()

	clonedEnv := make(map[string]string, len(d.env))
	for k, v := range d.env {
		clonedEnv[k] = v
	}
	clonedCmds := make(map[string]shell.CmdHandler, len(d.commands))
	for k, v := range d.commands {
		clonedCmds[k] = v
	}

	// fs.Clone() returns a fresh DirtyTrackingFS (empty dirty set).
	clonedFS, _ := d.fs.Clone().(*vfs.DirtyTrackingFS)
	if clonedFS == nil {
		clonedFS = vfs.NewDirtyTrackingFS(d.fs.Clone())
	}

	return &Driver{
		binary:   d.binary,
		timeout:  d.timeout,
		fs:       clonedFS,
		cwd:      d.cwd,
		env:      clonedEnv,
		commands: clonedCmds,
		notFound: d.notFound,
		runner:   d.runner,
	}
}

// Exec runs command in a fresh bashkit subprocess. Custom commands are
// intercepted locally before any subprocess is spawned.
func (d *Driver) Exec(ctx context.Context, command string) (shell.ExecResult, error) {
	if res, ok := d.tryCustomCommand(command); ok {
		return res, nil
	}

	d.mu.Lock()
	preamble := remotesync.BuildPreamble(d.fs)
	marker := remotesync.NewMarker()
	epilogue := remotesync.BuildEpilogue(marker, "/")
	binary := d.binary
	env := copyStringMap(d.env)
	timeout := d.timeout
	runner := d.runner
	d.mu.Unlock()

	full := composeCommand(preamble, command, epilogue)

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	raw, err := runner.Run(runCtx, binary, full, env)
	if err != nil {
		return shell.ExecResult{
			Stdout:   raw.Stdout,
			Stderr:   raw.Stderr,
			ExitCode: raw.ExitCode,
		}, err
	}

	userStdout, files := remotesync.ParseOutput(raw.Stdout, marker)
	if files != nil {
		if applyErr := remotesync.ApplyBack(ctx, d.fs, files); applyErr != nil {
			return shell.ExecResult{}, applyErr
		}
	}

	return shell.ExecResult{
		Stdout:   userStdout,
		Stderr:   raw.Stderr,
		ExitCode: raw.ExitCode,
	}, nil
}

// ExecStream is unsupported: the preamble/epilogue protocol requires the
// full subprocess output to be captured before the sync marker can be
// located. Returns ErrStreamingUnsupported.
func (d *Driver) ExecStream(_ context.Context, _ string) (<-chan shell.ExecStreamEvent, error) {
	return nil, shell.ErrStreamingUnsupported
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// tryCustomCommand returns a non-ok result if the first word of command
// matches a registered custom command — the handler is invoked locally
// with the remaining args as argv and an empty stdin. Empty input
// returns ok=false.
func (d *Driver) tryCustomCommand(command string) (shell.ExecResult, bool) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return shell.ExecResult{}, false
	}
	d.mu.Lock()
	handler, ok := d.commands[parts[0]]
	d.mu.Unlock()
	if !ok {
		return shell.ExecResult{}, false
	}
	return handler(parts[1:], ""), true
}

// composeCommand joins preamble, user command, and epilogue into a
// single shell string. A non-empty preamble is ANDed in; the epilogue is
// always appended so we capture post-exec file state.
func composeCommand(preamble, command, epilogue string) string {
	if preamble == "" {
		return command + epilogue
	}
	return preamble + " && " + command + epilogue
}

// copyStringMap returns a shallow copy suitable for passing to a runner
// without leaking driver state.
func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// -------------------------------------------------------------------------
// Default runner (exec.CommandContext-backed)
// -------------------------------------------------------------------------

// defaultRunner spawns the bashkit CLI via os/exec.
type defaultRunner struct{}

// Run implements shellRunner.
func (defaultRunner) Run(ctx context.Context, binary, command string, env map[string]string) (rawResult, error) {
	cmd := exec.CommandContext(ctx, binary, "-c", command)

	merged := os.Environ()
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}
	cmd.Env = merged

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee := new(exec.ExitError); errors.As(err, &ee) {
			exitCode = ee.ExitCode()
			err = nil // non-zero exit is returned via rawResult, not error
		} else if ctx.Err() != nil {
			return rawResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: -1,
			}, fmt.Errorf("bashkit: %w", ctx.Err())
		} else {
			return rawResult{}, fmt.Errorf("bashkit: %w", err)
		}
	}

	return rawResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// -------------------------------------------------------------------------
// Factory registration
// -------------------------------------------------------------------------

// init registers "bashkit" with shell.DefaultFactory. The factory reads
// the same option names documented on the With* helpers from the opts
// map (binary, cwd, env, fs, timeout).
func init() {
	shell.DefaultFactory.Register("bashkit", factoryFunc)
}

func factoryFunc(opts map[string]any) (shell.Driver, error) {
	var built []Option

	if binary, ok := opts["binary"].(string); ok && binary != "" {
		built = append(built, WithBinary(binary))
	}
	if cwd, ok := opts["cwd"].(string); ok && cwd != "" {
		built = append(built, WithCWD(cwd))
	}
	if env, ok := opts["env"].(map[string]string); ok {
		built = append(built, WithEnv(env))
	}
	if fs, ok := opts["fs"].(vfs.FilesystemDriver); ok {
		built = append(built, WithFS(fs))
	}
	if raw, ok := opts["timeout"]; ok {
		switch v := raw.(type) {
		case time.Duration:
			built = append(built, WithTimeout(v))
		case int:
			built = append(built, WithTimeout(time.Duration(v)))
		case int64:
			built = append(built, WithTimeout(time.Duration(v)))
		default:
			return nil, fmt.Errorf("bashkit: timeout must be time.Duration or int, got %T", raw)
		}
	}

	return New(built...), nil
}
