package openshell

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	pb "agent-harness/go/proto/openshell"
	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// --- fake SSH transport ------------------------------------------------------

// fakeSSH lets tests script the response to each SSH invocation and
// inspect the commands that were sent.
type fakeSSH struct {
	mu     sync.Mutex
	script func(command string) (rawResult, error)
	calls  []string
}

func (f *fakeSSH) Exec(ctx context.Context, user, host string, port int, command string, env map[string]string) (rawResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, command)
	script := f.script
	f.mu.Unlock()
	if script == nil {
		return rawResult{}, nil
	}
	return script(command)
}

func (f *fakeSSH) lastCommand() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

// extractMarker pulls the marker out of a composed command by finding
// `printf '\n<marker>\n'` in the epilogue.
func extractMarker(command string) string {
	needle := `printf '\n`
	idx := strings.Index(command, needle)
	if idx < 0 {
		return ""
	}
	rest := command[idx+len(needle):]
	end := strings.Index(rest, `\n'`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// --- SSH transport tests -----------------------------------------------------

func TestDriver_Exec_SSH_ReturnsResult(t *testing.T) {
	fake := &fakeSSH{
		script: func(command string) (rawResult, error) {
			marker := extractMarker(command)
			return rawResult{
				Stdout:   "hello world\n" + marker + "\n",
				Stderr:   "warn",
				ExitCode: 0,
			}, nil
		},
	}
	d := New(withSSHTransport(fake))
	res, err := d.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hello world" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hello world")
	}
	if res.Stderr != "warn" {
		t.Fatalf("stderr = %q, want %q", res.Stderr, "warn")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
}

func TestDriver_Exec_SSH_PreambleContainsDirtyFile(t *testing.T) {
	fake := &fakeSSH{
		script: func(command string) (rawResult, error) {
			return rawResult{Stdout: "", ExitCode: 0}, nil
		},
	}
	d := New(withSSHTransport(fake), WithWorkspace("/ws"))
	if err := d.FS().WriteString("/foo.txt", "bar"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(context.Background(), "true")
	if err != nil {
		t.Fatal(err)
	}
	cmd := fake.lastCommand()
	// Remapped path should appear.
	if !strings.Contains(cmd, "/ws/foo.txt") {
		t.Fatalf("expected remapped path in command, got %q", cmd)
	}
	// Preamble should contain the base64 of "bar".
	encoded := base64.StdEncoding.EncodeToString([]byte("bar"))
	if !strings.Contains(cmd, encoded) {
		t.Fatalf("expected base64 %q in command, got %q", encoded, cmd)
	}
}

func TestDriver_Exec_SSH_SyncBackRemapsPaths(t *testing.T) {
	// Simulate a remote that wrote /ws/new.txt; we should see it at /new.txt.
	fake := &fakeSSH{
		script: func(command string) (rawResult, error) {
			marker := extractMarker(command)
			listing := "===FILE:/ws/new.txt===\n" +
				base64.StdEncoding.EncodeToString([]byte("fresh")) + "\n"
			return rawResult{
				Stdout: "\n" + marker + "\n" + listing,
			}, nil
		},
	}
	d := New(withSSHTransport(fake), WithWorkspace("/ws"))
	_, err := d.Exec(context.Background(), "make-file")
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.FS().ReadString("/new.txt")
	if err != nil {
		t.Fatalf("expected /new.txt to exist: %v", err)
	}
	if got != "fresh" {
		t.Fatalf("content = %q, want %q", got, "fresh")
	}
}

func TestDriver_ExecStream_SSHUnsupported(t *testing.T) {
	d := New(withSSHTransport(&fakeSSH{}))
	_, err := d.ExecStream(context.Background(), "x")
	if err != shell.ErrStreamingUnsupported {
		t.Fatalf("expected ErrStreamingUnsupported, got %v", err)
	}
}

// --- custom commands ---------------------------------------------------------

func TestDriver_CustomCommand_InterceptsBeforeRemote(t *testing.T) {
	fake := &fakeSSH{}
	d := New(withSSHTransport(fake))
	d.RegisterCommand("ping", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "pong", ExitCode: 0}
	})
	res, err := d.Exec(context.Background(), "ping abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "pong" {
		t.Fatalf("expected pong, got %q", res.Stdout)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("custom command should not have hit the transport, got %v", fake.calls)
	}
}

func TestDriver_UnregisterCommand(t *testing.T) {
	fake := &fakeSSH{
		script: func(command string) (rawResult, error) {
			return rawResult{Stdout: ""}, nil
		},
	}
	d := New(withSSHTransport(fake))
	d.RegisterCommand("ping", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "pong"}
	})
	d.UnregisterCommand("ping")
	res, err := d.Exec(context.Background(), "ping x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout == "pong" {
		t.Fatalf("ping should have been unregistered")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected remote call, got %d", len(fake.calls))
	}
}

// --- Clone semantics ---------------------------------------------------------

func TestDriver_Clone_Isolated(t *testing.T) {
	parent := New(withSSHTransport(&fakeSSH{}))
	parent.RegisterCommand("p", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "parent"}
	})
	if err := parent.FS().WriteString("/x", "y"); err != nil {
		t.Fatal(err)
	}

	c := parent.Clone()

	// Child should see /x cloned.
	got, err := c.FS().ReadString("/x")
	if err != nil || got != "y" {
		t.Fatalf("clone didn't copy fs: got %q err %v", got, err)
	}

	// Writing through the child must not leak to parent.
	if err := c.FS().WriteString("/child-only", "c"); err != nil {
		t.Fatal(err)
	}
	if parent.FS().Exists("/child-only") {
		t.Fatalf("parent should not see child-only file")
	}

	// Env is cloned independently.
	parentWithEnv := New(withSSHTransport(&fakeSSH{}), WithEnv(map[string]string{"A": "1"}))
	child := parentWithEnv.Clone()
	childDriver, _ := child.(*Driver)
	childDriver.env["B"] = "2"
	if _, has := parentWithEnv.Env()["B"]; has {
		t.Fatalf("env leaked parent->child")
	}
}

func TestDriver_Capabilities(t *testing.T) {
	ssh := New(withSSHTransport(&fakeSSH{}))
	caps := ssh.Capabilities()
	if !caps[shell.CapCustomCommands] || !caps[shell.CapRemote] || !caps[shell.CapPolicies] {
		t.Fatalf("missing base caps: %v", caps)
	}
	if caps[shell.CapStreaming] {
		t.Fatalf("SSH transport should not advertise streaming")
	}

	grpc := New(WithTransport(TransportGRPC))
	if !grpc.Capabilities()[shell.CapStreaming] {
		t.Fatalf("gRPC transport should advertise streaming")
	}
}

// --- policy mapping ----------------------------------------------------------

func TestToSandboxSpec_MapsPolicy(t *testing.T) {
	pol := &shell.SecurityPolicy{
		FilesystemAllow: []string{"/tmp", "/var/work"},
		NetworkRules: []shell.NetworkRule{
			{CIDR: "10.0.0.0/8", Action: shell.AllowAction},
			{CIDR: "0.0.0.0/0", Action: shell.DenyAction},
		},
		Inference: shell.InferencePolicy{RoutingEnabled: true},
	}
	spec := toSandboxSpec(pol, "/ws")
	if spec.GetWorkspace() != "/ws" {
		t.Fatalf("workspace = %q", spec.GetWorkspace())
	}
	fs := spec.GetPolicy().GetFilesystem()
	if got := fs.GetReadWrite(); len(got) != 2 || got[0] != "/tmp" || got[1] != "/var/work" {
		t.Fatalf("read_write = %v", got)
	}
	nets := spec.GetPolicy().GetNetworkPolicies()
	if len(nets) != 2 {
		t.Fatalf("expected 2 network rules, got %d", len(nets))
	}
	if nets[0].GetCidr() != "10.0.0.0/8" || nets[0].GetAction() != pb.NetworkPolicy_ALLOW {
		t.Fatalf("allow rule mismapped: %+v", nets[0])
	}
	if nets[1].GetAction() != pb.NetworkPolicy_DENY {
		t.Fatalf("deny rule mismapped: %+v", nets[1])
	}
	if !spec.GetPolicy().GetInference().GetRoutingEnabled() {
		t.Fatalf("routing_enabled not plumbed through")
	}
}

func TestToSandboxSpec_NilPolicy(t *testing.T) {
	spec := toSandboxSpec(nil, "/ws")
	if spec.GetWorkspace() != "/ws" {
		t.Fatalf("workspace = %q", spec.GetWorkspace())
	}
	if spec.GetPolicy() == nil {
		t.Fatalf("policy should still be populated")
	}
}

// --- factory registration ----------------------------------------------------

func TestFactoryRegistration(t *testing.T) {
	drv, err := shell.DefaultFactory.Create("openshell", map[string]any{
		"transport": "ssh",
		"ssh_user":  "bob",
		"ssh_host":  "example.com",
		"ssh_port":  2022,
		"workspace": "/ws",
	})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := drv.(*Driver)
	if !ok {
		t.Fatalf("expected *Driver, got %T", drv)
	}
	if d.sshUser != "bob" || d.sshHost != "example.com" || d.sshPort != 2022 {
		t.Fatalf("ssh fields not set: %+v", d)
	}
	if d.workspace != "/ws" {
		t.Fatalf("workspace = %q", d.workspace)
	}
	if d.transport != TransportSSH {
		t.Fatalf("transport mismapped")
	}
}

func TestFactoryRegistration_GRPCClient(t *testing.T) {
	fake := &fakeOpenShellServer{}
	client := startBufconnServer(t, fake)
	drv, err := shell.DefaultFactory.Create("openshell", map[string]any{
		"transport":   "grpc",
		"grpc_client": client,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := drv.(*Driver)
	if d.transport != TransportGRPC {
		t.Fatalf("transport not grpc")
	}
	if d.grpcClient == nil {
		t.Fatalf("grpc client not injected")
	}
}

// --- gRPC transport (bufconn) ------------------------------------------------

// fakeOpenShellServer is a scripted in-memory OpenShell server used by
// the gRPC-transport tests. Each ExecSandbox call pops the next scripted
// response off the queue.
type fakeOpenShellServer struct {
	pb.UnimplementedOpenShellServer

	mu            sync.Mutex
	createCalls   int
	deleteCalls   []string
	execCommands  [][]string
	nextExecEvents [][]*pb.ExecSandboxEvent
	sandboxIDPrefix string
}

func (f *fakeOpenShellServer) CreateSandbox(ctx context.Context, req *pb.CreateSandboxRequest) (*pb.CreateSandboxResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	id := fmt.Sprintf("%ssandbox-%03d", f.sandboxIDPrefix, f.createCalls)
	return &pb.CreateSandboxResponse{
		Sandbox: &pb.Sandbox{SandboxId: id},
	}, nil
}

func (f *fakeOpenShellServer) DeleteSandbox(ctx context.Context, req *pb.DeleteSandboxRequest) (*pb.DeleteSandboxResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, req.GetName())
	return &pb.DeleteSandboxResponse{}, nil
}

func (f *fakeOpenShellServer) ExecSandbox(req *pb.ExecSandboxRequest, stream grpc.ServerStreamingServer[pb.ExecSandboxEvent]) error {
	f.mu.Lock()
	f.execCommands = append(f.execCommands, append([]string(nil), req.GetCommand()...))
	var events []*pb.ExecSandboxEvent
	if len(f.nextExecEvents) > 0 {
		events = f.nextExecEvents[0]
		f.nextExecEvents = f.nextExecEvents[1:]
	}
	f.mu.Unlock()
	if events == nil {
		// Default: minimal stream with just an exit=0 so drivers don't hang.
		events = []*pb.ExecSandboxEvent{
			{Event: &pb.ExecSandboxEvent_Exit{Exit: &pb.ExitStatus{Code: 0}}},
		}
	}
	for _, ev := range events {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeOpenShellServer) enqueue(events []*pb.ExecSandboxEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextExecEvents = append(f.nextExecEvents, events)
}

// startBufconnServer spins up an in-memory gRPC server serving srv and
// returns a connected client. The server is torn down on test cleanup.
func startBufconnServer(t *testing.T, srv pb.OpenShellServer) pb.OpenShellClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	pb.RegisterOpenShellServer(gs, srv)
	go func() {
		_ = gs.Serve(lis)
	}()
	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewOpenShellClient(conn)
}

// stdoutEvent / stderrEvent / exitEvent are tiny constructors that keep
// the test cases readable.
func stdoutEvent(data string) *pb.ExecSandboxEvent {
	return &pb.ExecSandboxEvent{Event: &pb.ExecSandboxEvent_Stdout{Stdout: &pb.StdoutChunk{Data: []byte(data)}}}
}
func stderrEvent(data string) *pb.ExecSandboxEvent {
	return &pb.ExecSandboxEvent{Event: &pb.ExecSandboxEvent_Stderr{Stderr: &pb.StderrChunk{Data: []byte(data)}}}
}
func exitEvent(code int32) *pb.ExecSandboxEvent {
	return &pb.ExecSandboxEvent{Event: &pb.ExecSandboxEvent_Exit{Exit: &pb.ExitStatus{Code: code}}}
}

func TestDriver_Exec_GRPC(t *testing.T) {
	srv := &fakeOpenShellServer{}
	client := startBufconnServer(t, srv)

	d := New(WithTransport(TransportGRPC), WithGRPCClient(client))
	// Queue: stdout "hi\n", then exit 0. Driver appends a marker via epilogue
	// so sync parsing will simply see "hi" as user stdout (no marker line).
	srv.enqueue([]*pb.ExecSandboxEvent{
		stdoutEvent("hi\n"),
		exitEvent(0),
	})

	res, err := d.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}

	// Verify CreateSandbox was called exactly once (lazy init).
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.createCalls != 1 {
		t.Fatalf("expected 1 CreateSandbox, got %d", srv.createCalls)
	}
	if len(srv.execCommands) != 1 {
		t.Fatalf("expected 1 ExecSandbox, got %d", len(srv.execCommands))
	}
	cmd := srv.execCommands[0]
	if len(cmd) != 3 || cmd[0] != "bash" || cmd[1] != "-c" {
		t.Fatalf("expected [bash -c ...], got %v", cmd)
	}
}

func TestDriver_Exec_GRPC_SyncBack(t *testing.T) {
	srv := &fakeOpenShellServer{}
	client := startBufconnServer(t, srv)
	d := New(WithTransport(TransportGRPC), WithGRPCClient(client), WithWorkspace("/ws"))

	// We don't know the marker in advance — it's generated per-call — so
	// queue an interceptor that reads the request and synthesises a
	// matching sync output. The simplest way: script a single event that
	// writes a pre-known marker. Because we can't observe the marker
	// before the call, we use a custom fakeServer method instead.
	//
	// Alternative: the fake returns the composed command's marker by
	// parsing the Command[2] string before sending events.

	// Replace ExecSandbox behaviour: we queue a marker-aware event set
	// by inspecting the request inside a one-shot listener.
	srv2 := &markerAwareServer{}
	client2 := startBufconnServer(t, srv2)

	d2 := New(WithTransport(TransportGRPC), WithGRPCClient(client2), WithWorkspace("/ws"))
	_, err := d2.Exec(context.Background(), "touch thing")
	if err != nil {
		t.Fatal(err)
	}
	got, err := d2.FS().ReadString("/thing")
	if err != nil {
		t.Fatalf("expected /thing in VFS: %v", err)
	}
	if got != "payload" {
		t.Fatalf("got %q, want %q", got, "payload")
	}

	_ = d // silence unused
}

// markerAwareServer parses the composed command, extracts the marker,
// and streams back a sync-output that includes one fake file.
type markerAwareServer struct {
	pb.UnimplementedOpenShellServer
}

func (m *markerAwareServer) CreateSandbox(ctx context.Context, req *pb.CreateSandboxRequest) (*pb.CreateSandboxResponse, error) {
	return &pb.CreateSandboxResponse{Sandbox: &pb.Sandbox{SandboxId: "sb-1"}}, nil
}

func (m *markerAwareServer) ExecSandbox(req *pb.ExecSandboxRequest, stream grpc.ServerStreamingServer[pb.ExecSandboxEvent]) error {
	cmd := strings.Join(req.GetCommand(), " ")
	marker := extractMarker(cmd)
	listing := "===FILE:/ws/thing===\n" + base64.StdEncoding.EncodeToString([]byte("payload")) + "\n"
	stdout := "\n" + marker + "\n" + listing
	if err := stream.Send(stdoutEvent(stdout)); err != nil {
		return err
	}
	return stream.Send(exitEvent(0))
}

func TestDriver_ExecStream_GRPC_ChunksInOrder(t *testing.T) {
	srv := &fakeOpenShellServer{}
	client := startBufconnServer(t, srv)
	d := New(WithTransport(TransportGRPC), WithGRPCClient(client))

	srv.enqueue([]*pb.ExecSandboxEvent{
		stdoutEvent("one"),
		stdoutEvent("two"),
		stderrEvent("warn"),
		exitEvent(3),
	})

	ch, err := d.ExecStream(context.Background(), "cmd")
	if err != nil {
		t.Fatal(err)
	}

	var events []shell.ExecStreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}
	if events[0].Kind != shell.StreamStdout || events[0].Data != "one" {
		t.Fatalf("event[0] = %+v", events[0])
	}
	if events[1].Kind != shell.StreamStdout || events[1].Data != "two" {
		t.Fatalf("event[1] = %+v", events[1])
	}
	if events[2].Kind != shell.StreamStderr || events[2].Data != "warn" {
		t.Fatalf("event[2] = %+v", events[2])
	}
	if events[3].Kind != shell.StreamExit || events[3].ExitCode != 3 {
		t.Fatalf("event[3] = %+v", events[3])
	}
}

func TestDriver_ExecStream_GRPC_CustomCommandIntercept(t *testing.T) {
	srv := &fakeOpenShellServer{}
	client := startBufconnServer(t, srv)
	d := New(WithTransport(TransportGRPC), WithGRPCClient(client))
	d.RegisterCommand("hello", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "world", ExitCode: 0}
	})
	ch, err := d.ExecStream(context.Background(), "hello you")
	if err != nil {
		t.Fatal(err)
	}
	var got []shell.ExecStreamEvent
	for e := range ch {
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events (stdout + exit), got %d", len(got))
	}
	if got[0].Kind != shell.StreamStdout || got[0].Data != "world" {
		t.Fatalf("stdout event mismatch: %+v", got[0])
	}
	if got[1].Kind != shell.StreamExit {
		t.Fatalf("final event should be exit: %+v", got[1])
	}

	// Custom command should bypass remote.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.execCommands) != 0 {
		t.Fatalf("remote should not have been called: %v", srv.execCommands)
	}
}

func TestDriver_Close_DeletesSandbox(t *testing.T) {
	srv := &fakeOpenShellServer{}
	client := startBufconnServer(t, srv)
	d := New(WithTransport(TransportGRPC), WithGRPCClient(client))
	srv.enqueue([]*pb.ExecSandboxEvent{exitEvent(0)})
	if _, err := d.Exec(context.Background(), "noop"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteSandbox, got %d", len(srv.deleteCalls))
	}
	if srv.deleteCalls[0] != "sandbox-001" {
		t.Fatalf("unexpected sandbox id deleted: %q", srv.deleteCalls[0])
	}
}

func TestDriver_Close_Idempotent(t *testing.T) {
	d := New(withSSHTransport(&fakeSSH{}))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

// --- misc --------------------------------------------------------------------

func TestDriver_FS_Default(t *testing.T) {
	d := New(withSSHTransport(&fakeSSH{}))
	if _, ok := d.FS().(*vfs.DirtyTrackingFS); !ok {
		t.Fatalf("expected dirty-tracking fs")
	}
}

func TestDriver_WithFS_Wraps(t *testing.T) {
	inner := vfs.NewBuiltinFilesystemDriver()
	d := New(withSSHTransport(&fakeSSH{}), WithFS(inner))
	dt, ok := d.FS().(*vfs.DirtyTrackingFS)
	if !ok {
		t.Fatalf("expected DirtyTrackingFS")
	}
	_ = dt
}

func TestDriver_NotFoundHandler_RoundTrip(t *testing.T) {
	d := New(withSSHTransport(&fakeSSH{}))
	if d.NotFoundHandler() != nil {
		t.Fatalf("expected nil handler initially")
	}
	h := func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		return &shell.ExecResult{ExitCode: 42}
	}
	d.SetNotFoundHandler(h)
	if d.NotFoundHandler() == nil {
		t.Fatalf("expected handler set")
	}
	d.SetNotFoundHandler(nil)
	if d.NotFoundHandler() != nil {
		t.Fatalf("expected handler cleared")
	}
}

