// Package openshell implements a shell.Driver backed by an OpenShell
// sandbox. Two transports are supported:
//
//   - TransportSSH (default): commands run via `ssh user@host` as a
//     subprocess. There is no true "sandbox" — the remote host is
//     assumed to already be running one. This mode does not support
//     streaming execution.
//
//   - TransportGRPC: commands run via the OpenShell gRPC service
//     (CreateSandbox / ExecSandbox / DeleteSandbox). ExecSandbox is
//     server-streaming, so ExecStream forwards chunks as they arrive.
//
// In both modes the driver maintains a virtual filesystem locally and
// syncs dirty files to the remote side before each command, then reads
// the post-command file state back via the marker/epilogue protocol in
// the remotesync package.
//
// This file wires the pieces together and satisfies shell.Driver.
package openshell

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	pb "agent-harness/go/proto/openshell"
	"agent-harness/go/shell"
	"agent-harness/go/shell/remotesync"
	"agent-harness/go/shell/vfs"

	"google.golang.org/grpc"
)

// Transport selects between SSH subprocess and native gRPC streaming.
type Transport int

const (
	// TransportSSH runs commands via `ssh` subprocess. No streaming.
	TransportSSH Transport = iota
	// TransportGRPC uses the OpenShell gRPC API. Supports streaming.
	TransportGRPC
)

// Option configures a Driver at construction time.
type Option func(*Driver)

// WithTransport selects SSH or gRPC transport. Default TransportSSH.
func WithTransport(t Transport) Option {
	return func(d *Driver) { d.transport = t }
}

// WithEndpoint sets the gRPC endpoint. Ignored for SSH.
// Default "localhost:50051".
func WithEndpoint(endpoint string) Option {
	return func(d *Driver) { d.endpoint = endpoint }
}

// WithSSHEndpoint sets the SSH user@host:port. Defaults to
// "sandbox@localhost:2222".
func WithSSHEndpoint(user, host string, port int) Option {
	return func(d *Driver) {
		d.sshUser = user
		d.sshHost = host
		d.sshPort = port
	}
}

// WithWorkspace sets the remote workspace directory used for path
// remapping and as the epilogue's find-root. Default
// "/home/sandbox/workspace".
func WithWorkspace(path string) Option {
	return func(d *Driver) { d.workspace = path }
}

// WithPolicy attaches a SecurityPolicy. It is translated to a
// *pb.SandboxSpec on CreateSandbox (gRPC only).
func WithPolicy(p *shell.SecurityPolicy) Option {
	return func(d *Driver) { d.policy = p }
}

// WithFS overrides the local virtual filesystem. When nil (default) the
// driver constructs a fresh vfs.BuiltinFilesystemDriver.
func WithFS(fs vfs.FilesystemDriver) Option {
	return func(d *Driver) {
		if fs == nil {
			return
		}
		d.fs = vfs.NewDirtyTrackingFS(fs)
	}
}

// WithCWD sets the driver's current working directory (defaults to "/").
func WithCWD(cwd string) Option {
	return func(d *Driver) { d.cwd = cwd }
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

// WithGRPCClient injects a pre-built gRPC client. When set the driver
// skips Dial and uses this stub directly — intended for testing with a
// bufconn-backed server.
func WithGRPCClient(client pb.OpenShellClient) Option {
	return func(d *Driver) { d.grpcClient = client }
}

// withSSHTransport injects a scripted SSH transport (for tests). It is
// intentionally unexported — production code uses defaultSSHTransport.
func withSSHTransport(t sshTransport) Option {
	return func(d *Driver) { d.ssh = t }
}

// Driver executes commands in an OpenShell sandbox. Implements shell.Driver.
type Driver struct {
	mu sync.Mutex

	// config
	transport Transport
	endpoint  string
	sshUser   string
	sshHost   string
	sshPort   int
	workspace string
	policy    *shell.SecurityPolicy

	// local state
	fs       *vfs.DirtyTrackingFS
	cwd      string
	env      map[string]string
	commands map[string]shell.CmdHandler
	notFound shell.NotFoundHandler

	// transport plumbing
	ssh        sshTransport
	grpcClient pb.OpenShellClient
	grpcConn   *grpc.ClientConn // owned when Dial was used
	sandboxID  string
}

// New constructs a Driver. The sandbox is created lazily on the first
// Exec / ExecStream invocation.
func New(opts ...Option) *Driver {
	d := &Driver{
		transport: TransportSSH,
		endpoint:  "localhost:50051",
		sshUser:   "sandbox",
		sshHost:   "localhost",
		sshPort:   2222,
		workspace: "/home/sandbox/workspace",
		fs:        vfs.NewDirtyTrackingFS(vfs.NewBuiltinFilesystemDriver()),
		cwd:       "/",
		env:       make(map[string]string),
		commands:  make(map[string]shell.CmdHandler),
	}
	for _, o := range opts {
		o(d)
	}
	if d.ssh == nil {
		d.ssh = defaultSSHTransport()
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
// command is forwarded to the remote transport — they do not participate
// in pipelines, matching Python.
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

// Capabilities advertises the driver's supported optional features. The
// streaming flag is transport-dependent: only TransportGRPC streams
// natively.
func (d *Driver) Capabilities() map[string]bool {
	return map[string]bool{
		shell.CapCustomCommands: true,
		shell.CapRemote:         true,
		shell.CapPolicies:       true,
		shell.CapStreaming:      d.transport == TransportGRPC,
	}
}

// Clone returns an independent driver with cloned VFS + env + commands
// and a fresh sandbox (sandboxID is reset; a new CreateSandbox will run
// on the next Exec).
func (d *Driver) Clone() shell.Driver {
	d.mu.Lock()
	defer d.mu.Unlock()

	var clonedPolicy *shell.SecurityPolicy
	if d.policy != nil {
		cp := *d.policy
		cp.AllowedCommands = append([]string(nil), d.policy.AllowedCommands...)
		cp.FilesystemAllow = append([]string(nil), d.policy.FilesystemAllow...)
		cp.NetworkRules = append([]shell.NetworkRule(nil), d.policy.NetworkRules...)
		clonedPolicy = &cp
	}

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
		transport:  d.transport,
		endpoint:   d.endpoint,
		sshUser:    d.sshUser,
		sshHost:    d.sshHost,
		sshPort:    d.sshPort,
		workspace:  d.workspace,
		policy:     clonedPolicy,
		fs:         clonedFS,
		cwd:        d.cwd,
		env:        clonedEnv,
		commands:   clonedCmds,
		notFound:   d.notFound,
		ssh:        d.ssh,
		grpcClient: d.grpcClient,
		// grpcConn and sandboxID deliberately zero — the clone runs an
		// independent sandbox lifecycle.
	}
}

// Exec runs command against the remote sandbox and returns the aggregated
// result. Custom commands are intercepted locally before any remote call.
func (d *Driver) Exec(ctx context.Context, command string) (shell.ExecResult, error) {
	if res, ok := d.tryCustomCommand(command); ok {
		return res, nil
	}

	preamble, epilogue, marker := d.buildSyncFrame()
	full := composeCommand(preamble, command, epilogue)

	raw, err := d.runRaw(ctx, full)
	if err != nil {
		return shell.ExecResult{}, err
	}

	userStdout, files := remotesync.ParseOutput(raw.Stdout, marker)
	if files != nil {
		remapped := d.unmapFiles(files)
		if err := remotesync.ApplyBack(ctx, d.fs, remapped); err != nil {
			return shell.ExecResult{}, err
		}
	}

	return shell.ExecResult{
		Stdout:   userStdout,
		Stderr:   raw.Stderr,
		ExitCode: raw.ExitCode,
	}, nil
}

// ExecStream runs command via the gRPC transport and returns a channel
// that delivers stdout/stderr/exit events as they arrive from the server.
// Sync-back runs once the stream closes. Returns ErrStreamingUnsupported
// when the driver is configured for SSH.
func (d *Driver) ExecStream(ctx context.Context, command string) (<-chan shell.ExecStreamEvent, error) {
	if d.transport != TransportGRPC {
		return nil, shell.ErrStreamingUnsupported
	}

	if res, ok := d.tryCustomCommand(command); ok {
		// Emit the synthetic result on the stream, same shape as a remote run.
		cap := 1
		if res.Stdout != "" {
			cap++
		}
		if res.Stderr != "" {
			cap++
		}
		ch := make(chan shell.ExecStreamEvent, cap)
		if res.Stdout != "" {
			ch <- shell.ExecStreamEvent{Kind: shell.StreamStdout, Data: res.Stdout}
		}
		if res.Stderr != "" {
			ch <- shell.ExecStreamEvent{Kind: shell.StreamStderr, Data: res.Stderr}
		}
		ch <- shell.ExecStreamEvent{Kind: shell.StreamExit, ExitCode: res.ExitCode}
		close(ch)
		return ch, nil
	}

	if err := d.ensureSandbox(ctx); err != nil {
		return nil, err
	}

	preamble, epilogue, marker := d.buildSyncFrame()
	full := composeCommand(preamble, command, epilogue)

	client, err := d.getGRPCClient(ctx)
	if err != nil {
		return nil, err
	}

	req := &pb.ExecSandboxRequest{
		SandboxId: d.sandboxID,
		Command:   []string{"bash", "-c", full},
		Env:       copyStringMap(d.env),
	}
	stream, err := client.ExecSandbox(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openshell: ExecSandbox: %w", err)
	}

	out := make(chan shell.ExecStreamEvent, 16)
	go func() {
		defer close(out)
		var stdoutAccum strings.Builder
		err := drainStream(stream, func(event *pb.ExecSandboxEvent) error {
			switch ev := event.GetEvent().(type) {
			case *pb.ExecSandboxEvent_Stdout:
				chunk := string(ev.Stdout.GetData())
				stdoutAccum.WriteString(chunk)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- shell.ExecStreamEvent{Kind: shell.StreamStdout, Data: chunk}:
				}
			case *pb.ExecSandboxEvent_Stderr:
				chunk := string(ev.Stderr.GetData())
				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- shell.ExecStreamEvent{Kind: shell.StreamStderr, Data: chunk}:
				}
			case *pb.ExecSandboxEvent_Exit:
				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- shell.ExecStreamEvent{Kind: shell.StreamExit, ExitCode: int(ev.Exit.GetCode())}:
				}
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			// Non-cancellation error: expose it on the stream as a
			// stderr event followed by a synthetic exit with code -1.
			select {
			case out <- shell.ExecStreamEvent{Kind: shell.StreamStderr, Data: err.Error()}:
			default:
			}
			select {
			case out <- shell.ExecStreamEvent{Kind: shell.StreamExit, ExitCode: -1}:
			default:
			}
			return
		}
		// Sync-back after the stream closes.
		_, files := remotesync.ParseOutput(stdoutAccum.String(), marker)
		if files != nil {
			remapped := d.unmapFiles(files)
			_ = remotesync.ApplyBack(ctx, d.fs, remapped)
		}
	}()

	return out, nil
}

// Close tears down the gRPC sandbox (if any) and the dialled connection.
// Safe to call multiple times.
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var firstErr error
	if d.sandboxID != "" && d.transport == TransportGRPC && d.grpcClient != nil {
		_, err := d.grpcClient.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			Name: d.sandboxID,
		})
		if err != nil {
			firstErr = fmt.Errorf("openshell: DeleteSandbox: %w", err)
		}
	}
	d.sandboxID = ""
	if d.grpcConn != nil {
		if err := d.grpcConn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		d.grpcConn = nil
	}
	return firstErr
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// tryCustomCommand returns a non-ok result if the first word of command
// matches a registered custom command — the handler is invoked locally
// and the result is returned. Empty input returns ok=false.
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

// buildSyncFrame constructs (preamble, epilogue, marker) for the next
// remote exec. Paths are remapped to the workspace on the real transport
// path; the test-friendly gRPC-client injection path also writes files
// into the workspace so the same logic applies — the caller's workspace
// is authoritative.
//
// A fresh marker is generated for every call so concurrent Exec /
// ExecStream requests cannot collide on their file-listing output.
func (d *Driver) buildSyncFrame() (preamble, epilogue, marker string) {
	marker = remotesync.NewMarker()

	// Build a remapped preamble that writes files into the workspace.
	parts := []string{fmt.Sprintf("mkdir -p %s", d.workspace)}
	for _, path := range d.fs.Dirty() {
		remote := d.remapPath(path)
		if d.fs.Exists(path) && !d.fs.IsDir(path) {
			content, err := d.fs.ReadString(path)
			if err != nil {
				continue
			}
			encoded := encodeBase64(content)
			parts = append(parts, fmt.Sprintf(
				"mkdir -p $(dirname '%s') && printf '%%s' '%s' | base64 -d > '%s'",
				remote, encoded, remote,
			))
		} else if !d.fs.Exists(path) {
			parts = append(parts, fmt.Sprintf("rm -f '%s'", remote))
		}
	}
	d.fs.ClearDirty()
	preamble = strings.Join(parts, " && ")
	epilogue = remotesync.BuildEpilogue(marker, d.workspace)
	return preamble, epilogue, marker
}

// composeCommand joins preamble, user command and epilogue into a single
// shell string. A non-empty preamble is ANDed in; the epilogue is always
// appended so we capture post-exec state.
func composeCommand(preamble, command, epilogue string) string {
	if preamble == "" {
		return command + epilogue
	}
	return preamble + " && " + command + epilogue
}

// remapPath converts a VFS path ("/hello.txt") to the workspace-absolute
// path on the remote side ("/home/sandbox/workspace/hello.txt").
func (d *Driver) remapPath(vfsPath string) string {
	return strings.TrimRight(d.workspace, "/") + "/" + strings.TrimLeft(vfsPath, "/")
}

// unmapPath is the inverse of remapPath.
func (d *Driver) unmapPath(remotePath string) string {
	prefix := strings.TrimRight(d.workspace, "/") + "/"
	if strings.HasPrefix(remotePath, prefix) {
		return "/" + remotePath[len(prefix):]
	}
	return remotePath
}

// unmapFiles rewrites the keys of a files map from remote workspace paths
// to VFS paths, ready for remotesync.ApplyBack.
func (d *Driver) unmapFiles(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for k, v := range files {
		out[d.unmapPath(k)] = v
	}
	return out
}

// runRaw dispatches to the active transport.
func (d *Driver) runRaw(ctx context.Context, command string) (rawResult, error) {
	if err := d.ensureSandbox(ctx); err != nil {
		return rawResult{}, err
	}
	if d.transport == TransportGRPC {
		return d.runRawGRPC(ctx, command)
	}
	return d.ssh.Exec(ctx, d.sshUser, d.sshHost, d.sshPort, command, d.env)
}

// runRawGRPC invokes ExecSandbox and aggregates the stream into a
// rawResult. Used by synchronous Exec.
func (d *Driver) runRawGRPC(ctx context.Context, command string) (rawResult, error) {
	client, err := d.getGRPCClient(ctx)
	if err != nil {
		return rawResult{}, err
	}
	req := &pb.ExecSandboxRequest{
		SandboxId: d.sandboxID,
		Command:   []string{"bash", "-c", command},
		Env:       copyStringMap(d.env),
	}
	stream, err := client.ExecSandbox(ctx, req)
	if err != nil {
		return rawResult{}, fmt.Errorf("openshell: ExecSandbox: %w", err)
	}

	var stdout, stderr strings.Builder
	exit := 0
	err = drainStream(stream, func(event *pb.ExecSandboxEvent) error {
		switch ev := event.GetEvent().(type) {
		case *pb.ExecSandboxEvent_Stdout:
			stdout.Write(ev.Stdout.GetData())
		case *pb.ExecSandboxEvent_Stderr:
			stderr.Write(ev.Stderr.GetData())
		case *pb.ExecSandboxEvent_Exit:
			exit = int(ev.Exit.GetCode())
		}
		return nil
	})
	if err != nil {
		return rawResult{}, err
	}
	return rawResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
	}, nil
}

// ensureSandbox lazily creates a sandbox on first use. For SSH this is a
// no-op beyond remembering a pseudo-id; for gRPC it issues CreateSandbox.
func (d *Driver) ensureSandbox(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sandboxID != "" {
		return nil
	}
	if d.transport != TransportGRPC {
		d.sandboxID = fmt.Sprintf("%s@%s:%d", d.sshUser, d.sshHost, d.sshPort)
		return nil
	}
	// gRPC path: CreateSandbox.
	client, err := d.getGRPCClientLocked(ctx)
	if err != nil {
		return err
	}
	req := &pb.CreateSandboxRequest{
		Name: randomSandboxName(),
		Spec: toSandboxSpec(d.policy, d.workspace),
	}
	resp, err := client.CreateSandbox(ctx, req)
	if err != nil {
		return fmt.Errorf("openshell: CreateSandbox: %w", err)
	}
	if sb := resp.GetSandbox(); sb != nil && sb.GetSandboxId() != "" {
		d.sandboxID = sb.GetSandboxId()
	} else {
		// Fall back to the request name so subsequent RPCs have something
		// to send — matches Python's best-effort lookup.
		d.sandboxID = req.GetName()
	}
	return nil
}

// getGRPCClient returns the driver's gRPC stub, dialling on first use.
// Thread-safe wrapper around getGRPCClientLocked.
func (d *Driver) getGRPCClient(ctx context.Context) (pb.OpenShellClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getGRPCClientLocked(ctx)
}

// getGRPCClientLocked requires d.mu held.
func (d *Driver) getGRPCClientLocked(ctx context.Context) (pb.OpenShellClient, error) {
	if d.grpcClient != nil {
		return d.grpcClient, nil
	}
	client, conn, err := dialClient(ctx, d.endpoint)
	if err != nil {
		return nil, err
	}
	d.grpcClient = client
	d.grpcConn = conn
	return client, nil
}

// encodeBase64 wraps base64.StdEncoding in a readable name at the call site.
func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// -------------------------------------------------------------------------
// Factory registration
// -------------------------------------------------------------------------

// init registers "openshell" with shell.DefaultFactory. The factory
// reads the same option names that are documented on the With* helpers
// from the opts map (transport, endpoint, ssh_user, ssh_host, ssh_port,
// workspace, cwd, env, policy, grpc_client, fs).
func init() {
	shell.DefaultFactory.Register("openshell", factoryFunc)
}

func factoryFunc(opts map[string]any) (shell.Driver, error) {
	var built []Option

	if raw, ok := opts["transport"]; ok {
		switch v := raw.(type) {
		case Transport:
			built = append(built, WithTransport(v))
		case string:
			switch strings.ToLower(v) {
			case "grpc":
				built = append(built, WithTransport(TransportGRPC))
			case "ssh", "":
				built = append(built, WithTransport(TransportSSH))
			default:
				return nil, fmt.Errorf("openshell: unknown transport %q", v)
			}
		default:
			return nil, fmt.Errorf("openshell: transport must be string or Transport, got %T", raw)
		}
	}
	if endpoint, ok := opts["endpoint"].(string); ok && endpoint != "" {
		built = append(built, WithEndpoint(endpoint))
	}

	// SSH endpoint is optionally specified piecewise.
	sshUser, _ := opts["ssh_user"].(string)
	sshHost, _ := opts["ssh_host"].(string)
	sshPort, _ := opts["ssh_port"].(int)
	if sshUser != "" || sshHost != "" || sshPort != 0 {
		if sshUser == "" {
			sshUser = "sandbox"
		}
		if sshHost == "" {
			sshHost = "localhost"
		}
		if sshPort == 0 {
			sshPort = 2222
		}
		built = append(built, WithSSHEndpoint(sshUser, sshHost, sshPort))
	}

	if workspace, ok := opts["workspace"].(string); ok && workspace != "" {
		built = append(built, WithWorkspace(workspace))
	}
	if cwd, ok := opts["cwd"].(string); ok && cwd != "" {
		built = append(built, WithCWD(cwd))
	}
	if env, ok := opts["env"].(map[string]string); ok {
		built = append(built, WithEnv(env))
	}
	if pol, ok := opts["policy"].(*shell.SecurityPolicy); ok {
		built = append(built, WithPolicy(pol))
	}
	if fs, ok := opts["fs"].(vfs.FilesystemDriver); ok {
		built = append(built, WithFS(fs))
	}
	if client, ok := opts["grpc_client"].(pb.OpenShellClient); ok {
		built = append(built, WithGRPCClient(client))
	}

	return New(built...), nil
}

// copyStringMap returns a shallow copy suitable for a proto map field.
func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
