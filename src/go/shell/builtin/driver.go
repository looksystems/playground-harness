// Package builtin — BuiltinShellDriver (M2.8).
//
// This file wires the tokenizer, parser, expansion, and evaluator layers
// (M2.3–M2.7) together into a shell.Driver implementation. It mirrors
// Python's BuiltinShellDriver (src/python/drivers.py lines ~151-222).
//
// Design decisions:
//
//   - Eager evaluator construction: NewBuiltinShellDriver creates the
//     Evaluator immediately so CWD() / Env() can always delegate to it
//     without a lazy-init guard.
//
//   - State sharing via Evaluator: the driver does NOT hold its own
//     separate cwd/env fields. CWD() and Env() read directly from
//     d.eval.CWD and d.eval.Env so `cd` / `export` side-effects are
//     immediately visible — no drift.
//
//   - Mutex discipline: Exec / ExecStream hold d.mu.Lock() for the full
//     duration so CWD/Env mutations are serialised. CWD() / Env() / FS()
//     acquire d.mu.RLock() so reads are safe from another goroutine.
//
//   - ExecStream emulation: the builtin interpreter is synchronous.
//     ExecStream runs Exec then emits {Stdout, Stderr?, Exit} and closes
//     the channel. This keeps the shape consistent with the OpenShell
//     driver (M5) which genuinely streams.
//
//   - Clone: deep-copies FS, env, cwd, and user-registered commands.
//     Starts with a fresh NewDefaultBuiltinRegistry() plus any user
//     commands copied from the parent. The not-found handler is copied by
//     reference (functions are value types in Go).
//
//   - Factory init(): registers "builtin" with shell.DefaultFactory and
//     sets it as the default. This is the one allowed global-state exception
//     documented in the cross-cutting architecture notes.
package builtin

import (
	"context"
	"sync"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// BuiltinShellDriver implements shell.Driver using the pure-Go interpreter
// defined in this package (tokenizer → parser → expansion → evaluator).
type BuiltinShellDriver struct {
	mu   sync.RWMutex
	fs   vfs.FilesystemDriver
	eval *Evaluator
	// userCmds tracks names registered via RegisterCommand so Clone can
	// replicate them in the new driver's registry.
	userCmds map[string]shell.CmdHandler
	policy   *shell.SecurityPolicy
}

// Option configures a BuiltinShellDriver.
type Option func(*BuiltinShellDriver)

// WithFS sets the virtual filesystem backing the driver. If not called,
// a fresh vfs.BuiltinFilesystemDriver is used.
func WithFS(fs vfs.FilesystemDriver) Option {
	return func(d *BuiltinShellDriver) { d.fs = fs }
}

// WithCWD sets the initial working directory. Defaults to "/".
func WithCWD(cwd string) Option {
	return func(d *BuiltinShellDriver) { d.eval.CWD = cwd }
}

// WithEnv sets the initial environment. A defensive copy is made.
func WithEnv(env map[string]string) Option {
	return func(d *BuiltinShellDriver) {
		m := make(map[string]string, len(env))
		for k, v := range env {
			m[k] = v
		}
		d.eval.Env = m
	}
}

// WithSecurityPolicy attaches a security policy (currently recorded but
// not enforced by the builtin driver).
func WithSecurityPolicy(p *shell.SecurityPolicy) Option {
	return func(d *BuiltinShellDriver) { d.policy = p }
}

// NewBuiltinShellDriver constructs a driver with sensible defaults:
//   - VFS: fresh vfs.BuiltinFilesystemDriver (empty)
//   - CWD: "/"
//   - Env: empty map
//   - Builtins: NewDefaultBuiltinRegistry()
func NewBuiltinShellDriver(opts ...Option) *BuiltinShellDriver {
	fs := vfs.NewBuiltinFilesystemDriver()
	d := &BuiltinShellDriver{
		fs:       fs,
		userCmds: make(map[string]shell.CmdHandler),
		eval: &Evaluator{
			FS:            fs,
			CWD:           "/",
			Env:           make(map[string]string),
			Builtins:      NewDefaultBuiltinRegistry(),
			MaxIterations: defaultEvalMaxIterations,
			MaxSubDepth:   defaultEvalMaxSubDepth,
			MaxOutput:     defaultEvalMaxOutput,
		},
	}
	for _, o := range opts {
		o(d)
	}
	// Keep d.fs and d.eval.FS in sync when WithFS was applied.
	d.eval.FS = d.fs
	return d
}

// ---------------------------------------------------------------------------
// shell.Driver implementation
// ---------------------------------------------------------------------------

// FS returns the filesystem backing this driver.
func (d *BuiltinShellDriver) FS() vfs.FilesystemDriver {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.fs
}

// CWD returns the current working directory.
func (d *BuiltinShellDriver) CWD() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.eval.CWD
}

// Env returns a defensive copy of the current environment.
func (d *BuiltinShellDriver) Env() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	m := make(map[string]string, len(d.eval.Env))
	for k, v := range d.eval.Env {
		m[k] = v
	}
	return m
}

// Exec parses and evaluates command. Concurrent calls are serialised.
func (d *BuiltinShellDriver) Exec(ctx context.Context, command string) (shell.ExecResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.eval.Execute(ctx, command)
}

// ExecStream runs command synchronously then emits events on the returned
// channel: stdout (if non-empty), stderr (if non-empty), then exit. The
// channel is closed after the exit event. This emulation keeps the same
// observable shape as OpenShell's genuine streaming driver.
func (d *BuiltinShellDriver) ExecStream(ctx context.Context, command string) (<-chan shell.ExecStreamEvent, error) {
	result, err := d.Exec(ctx, command)
	if err != nil {
		return nil, err
	}

	// Buffer enough for all events so the sender never blocks.
	capacity := 1 // exit event always
	if result.Stdout != "" {
		capacity++
	}
	if result.Stderr != "" {
		capacity++
	}
	ch := make(chan shell.ExecStreamEvent, capacity)

	if result.Stdout != "" {
		ch <- shell.ExecStreamEvent{Kind: shell.StreamStdout, Data: result.Stdout}
	}
	if result.Stderr != "" {
		ch <- shell.ExecStreamEvent{Kind: shell.StreamStderr, Data: result.Stderr}
	}
	ch <- shell.ExecStreamEvent{Kind: shell.StreamExit, ExitCode: result.ExitCode}
	close(ch)

	return ch, nil
}

// RegisterCommand adds (or replaces) a user-defined command. The handler is
// wrapped into a Builtin and installed in the evaluator's registry, and also
// stored in d.userCmds so Clone can reproduce it.
func (d *BuiltinShellDriver) RegisterCommand(name string, handler shell.CmdHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.eval.Builtins.RegisterUser(name, handler)
	d.userCmds[name] = handler
}

// UnregisterCommand removes a previously registered command.
func (d *BuiltinShellDriver) UnregisterCommand(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.eval.Builtins.Unregister(name)
	delete(d.userCmds, name)
}

// Clone returns an independent driver with deep-copied state: new VFS,
// new env map, same CWD string (strings are immutable in Go), fresh
// default builtin registry plus any user-registered commands, and the
// same not-found handler reference.
func (d *BuiltinShellDriver) Clone() shell.Driver {
	d.mu.RLock()
	defer d.mu.RUnlock()

	clonedFS := d.fs.Clone()

	clonedEnv := make(map[string]string, len(d.eval.Env))
	for k, v := range d.eval.Env {
		clonedEnv[k] = v
	}

	reg := NewDefaultBuiltinRegistry()
	clonedUserCmds := make(map[string]shell.CmdHandler, len(d.userCmds))
	for name, handler := range d.userCmds {
		reg.RegisterUser(name, handler)
		clonedUserCmds[name] = handler
	}

	nd := &BuiltinShellDriver{
		fs:       clonedFS,
		userCmds: clonedUserCmds,
		policy:   d.policy, // shallow copy; SecurityPolicy is read-only
		eval: &Evaluator{
			FS:              clonedFS,
			CWD:             d.eval.CWD,
			Env:             clonedEnv,
			Builtins:        reg,
			ExitStatus:      d.eval.ExitStatus,
			MaxIterations:   d.eval.MaxIterations,
			MaxSubDepth:     d.eval.MaxSubDepth,
			MaxOutput:       d.eval.MaxOutput,
			NotFoundHandler: d.eval.NotFoundHandler,
		},
	}
	return nd
}

// NotFoundHandler returns the current not-found handler.
func (d *BuiltinShellDriver) NotFoundHandler() shell.NotFoundHandler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.eval.NotFoundHandler
}

// SetNotFoundHandler replaces the not-found handler (pass nil to clear).
func (d *BuiltinShellDriver) SetNotFoundHandler(h shell.NotFoundHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.eval.NotFoundHandler = h
}

// Capabilities advertises supported optional features.
func (d *BuiltinShellDriver) Capabilities() map[string]bool {
	return map[string]bool{
		shell.CapCustomCommands: true,
		shell.CapStateful:       true,
		// CapStreaming: not native — ExecStream emulates by buffering.
		// CapPolicies:  SecurityPolicy is accepted but not enforced.
		// CapRemote:    always local.
	}
}

// ---------------------------------------------------------------------------
// Factory registration
// ---------------------------------------------------------------------------

// init registers "builtin" with shell.DefaultFactory and sets it as the
// default. This is the first init() in the Go port and the one allowed
// global-state exception: the factory is the package-level singleton that
// lets callers create drivers by name without hard-coding types.
func init() {
	shell.DefaultFactory.Register("builtin", factoryFunc)
	shell.DefaultFactory.SetDefault("builtin")
}

func factoryFunc(opts map[string]any) (shell.Driver, error) {
	var driverOpts []Option

	if cwd, ok := opts["cwd"].(string); ok && cwd != "" {
		driverOpts = append(driverOpts, WithCWD(cwd))
	}
	if env, ok := opts["env"].(map[string]string); ok {
		driverOpts = append(driverOpts, WithEnv(env))
	}
	if fs, ok := opts["fs"].(vfs.FilesystemDriver); ok && fs != nil {
		driverOpts = append(driverOpts, WithFS(fs))
	}

	return NewBuiltinShellDriver(driverOpts...), nil
}
