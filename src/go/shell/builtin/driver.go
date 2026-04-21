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
//
// Concurrency model (post-review-fix I3):
//
//   - execMu is a plain sync.Mutex that serialises Execute calls. It is
//     held for the full duration of every Exec so the non-thread-safe
//     Evaluator is only driven by one goroutine at a time.
//   - mu is a sync.RWMutex that protects the publicly-observable
//     snapshot fields (cwd, env). Readers take RLock briefly and copy
//     the snapshot; Exec takes mu.Lock ONLY at its commit point to
//     publish the post-run state into those fields.
//
// Readers no longer block for the duration of a long Exec — in the old
// implementation, CWD() and Env() contended with the single mu.Lock
// held across the whole Evaluator run. Now they only wait for the
// short commit.
type BuiltinShellDriver struct {
	mu     sync.RWMutex
	execMu sync.Mutex
	fs     vfs.FilesystemDriver
	eval   *Evaluator
	// cwd/env are the publicly-observable snapshots of the evaluator
	// state, published at the end of each Exec. Readers see the last
	// committed snapshot; they never see the live mid-run map the
	// evaluator is mutating.
	cwd string
	env map[string]string
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

// WithSecurityPolicy attaches a security policy. When the policy's
// AllowedCommands slice is non-empty, the default builtin registry is
// filtered down to just those commands, matching Python's
// Shell(allowed_commands=...) semantics. Custom commands registered
// later via RegisterCommand are always available regardless of the
// policy — only the initial built-in set is filtered.
//
// Other fields on the policy (FilesystemAllow, NetworkRules, Inference)
// are recorded but not enforced by the builtin driver; OpenShell's
// sandboxed drivers consume those.
func WithSecurityPolicy(p *shell.SecurityPolicy) Option {
	return func(d *BuiltinShellDriver) {
		d.policy = p
		if p == nil || len(p.AllowedCommands) == 0 {
			return
		}
		d.eval.Builtins = filteredDefaultRegistry(p.AllowedCommands)
	}
}

// filteredDefaultRegistry returns a BuiltinRegistry containing only the
// default builtins whose name appears in allowed. Names in allowed that
// are not default builtins are ignored — users who want unknown names
// to resolve must register them via Driver.RegisterCommand.
func filteredDefaultRegistry(allowed []string) *BuiltinRegistry {
	full := NewDefaultBuiltinRegistry()
	filtered := NewBuiltinRegistry()
	allowSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowSet[name] = struct{}{}
	}
	for _, name := range full.Names() {
		if _, ok := allowSet[name]; !ok {
			continue
		}
		if h, ok := full.Get(name); ok {
			filtered.Register(name, h)
		}
	}
	return filtered
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
	// Seed the public snapshot from the evaluator's initial state so
	// CWD()/Env() work correctly before the first Exec.
	d.cwd = d.eval.CWD
	d.env = make(map[string]string, len(d.eval.Env))
	for k, v := range d.eval.Env {
		d.env[k] = v
	}
	return d
}

// ---------------------------------------------------------------------------
// shell.Driver implementation
// ---------------------------------------------------------------------------

// FS returns the filesystem backing this driver. FS is immutable for
// the lifetime of the driver (only Clone allocates a new one) so the
// RLock is a near-instant read.
func (d *BuiltinShellDriver) FS() vfs.FilesystemDriver {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.fs
}

// CWD returns the current working directory. Readers take a brief
// RLock on the committed snapshot and do not block on an in-flight
// Exec — see the type-level comment for the lock split rationale.
func (d *BuiltinShellDriver) CWD() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cwd
}

// Env returns a defensive copy of the current environment. Readers
// take a brief RLock on the committed snapshot and do not block on an
// in-flight Exec.
func (d *BuiltinShellDriver) Env() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	m := make(map[string]string, len(d.env))
	for k, v := range d.env {
		m[k] = v
	}
	return m
}

// Exec parses and evaluates command. Concurrent Exec calls are
// serialised via execMu so the non-thread-safe Evaluator is only
// driven by one goroutine at a time. execMu is a plain Mutex — distinct
// from mu — so reader goroutines calling CWD / Env / FS see the
// previously-committed snapshot without waiting for a long-running
// Exec to finish.
//
// After Execute returns, Exec briefly takes mu.Lock to publish the
// evaluator's post-run CWD and a copy of its Env map into the snapshot
// fields. Mid-Exec readers therefore see a pre-Exec view, not torn
// intermediate state; post-Exec readers see the result.
func (d *BuiltinShellDriver) Exec(ctx context.Context, command string) (shell.ExecResult, error) {
	d.execMu.Lock()
	defer d.execMu.Unlock()

	result, err := d.eval.Execute(ctx, command)

	// Publish the post-run snapshot. A fresh copy of Env ensures
	// readers that retain a map returned by Env() aren't racing with
	// the evaluator's in-place mutations on the next Exec.
	d.mu.Lock()
	d.cwd = d.eval.CWD
	newEnv := make(map[string]string, len(d.eval.Env))
	for k, v := range d.eval.Env {
		newEnv[k] = v
	}
	d.env = newEnv
	d.mu.Unlock()

	return result, err
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
//
// execMu is held so a concurrent Exec cannot observe a partially-
// registered name (the BuiltinRegistry has its own mutex but we also
// want d.userCmds to stay consistent with it).
func (d *BuiltinShellDriver) RegisterCommand(name string, handler shell.CmdHandler) {
	d.execMu.Lock()
	defer d.execMu.Unlock()
	d.eval.Builtins.RegisterUser(name, handler)
	d.userCmds[name] = handler
}

// UnregisterCommand removes a previously registered command.
func (d *BuiltinShellDriver) UnregisterCommand(name string) {
	d.execMu.Lock()
	defer d.execMu.Unlock()
	d.eval.Builtins.Unregister(name)
	delete(d.userCmds, name)
}

// Clone returns an independent driver with deep-copied state: new VFS,
// new env map, same CWD string (strings are immutable in Go), fresh
// default builtin registry plus any user-registered commands, and the
// same not-found handler reference.
func (d *BuiltinShellDriver) Clone() shell.Driver {
	// Serialise against in-flight Exec so we don't clone a torn map.
	d.execMu.Lock()
	defer d.execMu.Unlock()
	d.mu.RLock()
	defer d.mu.RUnlock()

	clonedFS := d.fs.Clone()

	clonedEnv := make(map[string]string, len(d.eval.Env))
	for k, v := range d.eval.Env {
		clonedEnv[k] = v
	}

	reg := NewDefaultBuiltinRegistry()
	if d.policy != nil && len(d.policy.AllowedCommands) > 0 {
		reg = filteredDefaultRegistry(d.policy.AllowedCommands)
	}
	clonedUserCmds := make(map[string]shell.CmdHandler, len(d.userCmds))
	for name, handler := range d.userCmds {
		reg.RegisterUser(name, handler)
		clonedUserCmds[name] = handler
	}

	nd := &BuiltinShellDriver{
		fs:       clonedFS,
		userCmds: clonedUserCmds,
		policy:   d.policy, // shallow copy; SecurityPolicy is read-only
		cwd:      d.cwd,
		env: func() map[string]string {
			m := make(map[string]string, len(d.env))
			for k, v := range d.env {
				m[k] = v
			}
			return m
		}(),
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
	d.execMu.Lock()
	defer d.execMu.Unlock()
	return d.eval.NotFoundHandler
}

// SetNotFoundHandler replaces the not-found handler (pass nil to clear).
func (d *BuiltinShellDriver) SetNotFoundHandler(h shell.NotFoundHandler) {
	d.execMu.Lock()
	defer d.execMu.Unlock()
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
