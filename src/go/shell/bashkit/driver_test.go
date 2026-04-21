package bashkit

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// --- fake runner -------------------------------------------------------------

// fakeRunner scripts subprocess responses and records the commands
// bashkit would have been invoked with. Used in place of the real
// exec.CommandContext-backed runner so tests don't require a `bashkit`
// binary on PATH.
type fakeRunner struct {
	mu     sync.Mutex
	script func(binary, command string, env map[string]string) (rawResult, error)
	calls  []fakeCall
}

type fakeCall struct {
	Binary  string
	Command string
	Env     map[string]string
}

func (f *fakeRunner) Run(ctx context.Context, binary, command string, env map[string]string) (rawResult, error) {
	f.mu.Lock()
	envCopy := make(map[string]string, len(env))
	for k, v := range env {
		envCopy[k] = v
	}
	f.calls = append(f.calls, fakeCall{Binary: binary, Command: command, Env: envCopy})
	script := f.script
	f.mu.Unlock()
	if script == nil {
		return rawResult{}, nil
	}
	// Honour context cancellation even if the script doesn't.
	type scriptResult struct {
		raw rawResult
		err error
	}
	done := make(chan scriptResult, 1)
	go func() {
		raw, err := script(binary, command, env)
		done <- scriptResult{raw, err}
	}()
	select {
	case <-ctx.Done():
		return rawResult{ExitCode: -1}, ctx.Err()
	case r := <-done:
		return r.raw, r.err
	}
}

func (f *fakeRunner) lastCall() fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return fakeCall{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// extractMarker pulls the marker string out of a composed command by
// locating the `printf '\n<marker>\n'` in the epilogue.
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

// --- Exec --------------------------------------------------------------------

func TestDriver_Exec_ReturnsResult(t *testing.T) {
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			marker := extractMarker(command)
			return rawResult{
				Stdout:   "hi\n" + marker + "\n",
				Stderr:   "warn",
				ExitCode: 0,
			}, nil
		},
	}
	d := New(withRunner(fake))
	res, err := d.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hi" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hi")
	}
	if res.Stderr != "warn" {
		t.Fatalf("stderr = %q", res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}

	// The runner should have been asked to run `bashkit` by default.
	if got := fake.lastCall().Binary; got != "bashkit" {
		t.Fatalf("binary = %q, want %q", got, "bashkit")
	}
	// Composed command must embed the user command verbatim.
	if !strings.Contains(fake.lastCall().Command, "echo hi") {
		t.Fatalf("composed command missing user command: %q", fake.lastCall().Command)
	}
}

func TestDriver_Exec_PreambleContainsDirtyFile(t *testing.T) {
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			return rawResult{Stdout: "", ExitCode: 0}, nil
		},
	}
	d := New(withRunner(fake))
	if err := d.FS().WriteString("/foo.txt", "bar"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	cmd := fake.lastCall().Command
	if !strings.Contains(cmd, "/foo.txt") {
		t.Fatalf("expected /foo.txt in command, got %q", cmd)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("bar"))
	if !strings.Contains(cmd, encoded) {
		t.Fatalf("expected base64 %q in command, got %q", encoded, cmd)
	}
}

func TestDriver_Exec_SyncBackRoundTrip(t *testing.T) {
	// Simulate a subprocess that returns the marker followed by one file.
	// The driver should write `/new.txt` into the local VFS.
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			marker := extractMarker(command)
			listing := "===FILE:/new.txt===\n" +
				base64.StdEncoding.EncodeToString([]byte("fresh")) + "\n"
			return rawResult{
				Stdout: "output\n" + marker + "\n" + listing,
			}, nil
		},
	}
	d := New(withRunner(fake))
	res, err := d.Exec(context.Background(), "make-file")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "output" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "output")
	}
	got, err := d.FS().ReadString("/new.txt")
	if err != nil {
		t.Fatalf("expected /new.txt in VFS: %v", err)
	}
	if got != "fresh" {
		t.Fatalf("content = %q, want %q", got, "fresh")
	}
}

func TestDriver_Exec_NoMarkerMeansNoSyncBack(t *testing.T) {
	// If bashkit died before the epilogue ran, ParseOutput returns nil
	// for files — the VFS should be untouched and stdout passed through
	// verbatim.
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			return rawResult{Stdout: "fatal\n", Stderr: "boom", ExitCode: 127}, nil
		},
	}
	d := New(withRunner(fake))
	if err := d.FS().WriteString("/keep.txt", "keep"); err != nil {
		t.Fatal(err)
	}
	res, err := d.Exec(context.Background(), "explode")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "fatal\n" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if res.ExitCode != 127 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if !d.FS().Exists("/keep.txt") {
		t.Fatalf("existing file should survive a sync-back miss")
	}
}

// --- Custom commands ---------------------------------------------------------

func TestDriver_CustomCommand_InterceptsBeforeSubprocess(t *testing.T) {
	fake := &fakeRunner{}
	d := New(withRunner(fake))
	d.RegisterCommand("ping", func(args []string, stdin string) shell.ExecResult {
		if len(args) != 1 || args[0] != "abc" {
			t.Errorf("args = %v", args)
		}
		return shell.ExecResult{Stdout: "pong", ExitCode: 0}
	})
	res, err := d.Exec(context.Background(), "ping abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "pong" {
		t.Fatalf("expected pong, got %q", res.Stdout)
	}
	if fake.callCount() != 0 {
		t.Fatalf("custom command should not have hit the subprocess")
	}
}

func TestDriver_UnregisterCommand(t *testing.T) {
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			return rawResult{Stdout: ""}, nil
		},
	}
	d := New(withRunner(fake))
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
	if fake.callCount() != 1 {
		t.Fatalf("expected subprocess call, got %d", fake.callCount())
	}
}

// --- ExecStream --------------------------------------------------------------

func TestDriver_ExecStream_Unsupported(t *testing.T) {
	d := New(withRunner(&fakeRunner{}))
	ch, err := d.ExecStream(context.Background(), "x")
	if err != shell.ErrStreamingUnsupported {
		t.Fatalf("expected ErrStreamingUnsupported, got %v", err)
	}
	if ch != nil {
		t.Fatalf("channel should be nil on error")
	}
}

// --- Capabilities ------------------------------------------------------------

func TestDriver_Capabilities(t *testing.T) {
	d := New(withRunner(&fakeRunner{}))
	caps := d.Capabilities()
	if !caps[shell.CapCustomCommands] {
		t.Errorf("missing CapCustomCommands")
	}
	if !caps[shell.CapRemote] {
		t.Errorf("missing CapRemote")
	}
	if caps[shell.CapStateful] {
		t.Errorf("bashkit CLI is stateless; should not advertise CapStateful")
	}
	if caps[shell.CapStreaming] {
		t.Errorf("bashkit CLI is not streamable; should not advertise CapStreaming")
	}
	if caps[shell.CapPolicies] {
		t.Errorf("bashkit CLI does not implement policies")
	}
}

// --- Clone semantics ---------------------------------------------------------

func TestDriver_Clone_Isolated(t *testing.T) {
	parent := New(withRunner(&fakeRunner{}))
	parent.RegisterCommand("p", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "parent"}
	})
	if err := parent.FS().WriteString("/x", "y"); err != nil {
		t.Fatal(err)
	}

	c := parent.Clone()

	got, err := c.FS().ReadString("/x")
	if err != nil || got != "y" {
		t.Fatalf("clone didn't copy fs: got %q err %v", got, err)
	}

	// Writing through the child must not leak to parent.
	if err := c.FS().WriteString("/child-only", "c"); err != nil {
		t.Fatal(err)
	}
	if parent.FS().Exists("/child-only") {
		t.Fatalf("parent should not see /child-only")
	}

	// Env is cloned independently.
	parentEnv := New(withRunner(&fakeRunner{}), WithEnv(map[string]string{"A": "1"}))
	child := parentEnv.Clone()
	childDriver, _ := child.(*Driver)
	childDriver.env["B"] = "2"
	if _, has := parentEnv.Env()["B"]; has {
		t.Fatalf("env leaked parent->child")
	}

	// Registered commands are cloned: unregistering on the child must
	// not affect the parent.
	cd := c.(*Driver)
	cd.UnregisterCommand("p")
	if _, has := parent.commands["p"]; !has {
		t.Fatalf("parent's custom command was removed by child.Unregister")
	}
}

func TestDriver_Clone_SharesRunnerAndBinary(t *testing.T) {
	fake := &fakeRunner{}
	parent := New(withRunner(fake), WithBinary("/opt/bashkit"), WithTimeout(5*time.Second))
	c := parent.Clone().(*Driver)
	if c.binary != "/opt/bashkit" {
		t.Fatalf("binary not shared: %q", c.binary)
	}
	if c.timeout != 5*time.Second {
		t.Fatalf("timeout not shared: %v", c.timeout)
	}
	// Runner is the same instance: a call via the child must show up in
	// the fake's calls.
	_, _ = c.Exec(context.Background(), "true")
	if fake.callCount() != 1 {
		t.Fatalf("clone did not share runner")
	}
}

// --- Environment plumbing ----------------------------------------------------

func TestDriver_Exec_EnvPassedToRunner(t *testing.T) {
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			return rawResult{}, nil
		},
	}
	d := New(withRunner(fake), WithEnv(map[string]string{"FOO": "bar"}))
	if _, err := d.Exec(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	if got := fake.lastCall().Env["FOO"]; got != "bar" {
		t.Fatalf("env[FOO] = %q, want bar", got)
	}
}

// --- Timeout -----------------------------------------------------------------

func TestDriver_Exec_TimeoutCancelsRunner(t *testing.T) {
	// Runner blocks until its context is cancelled; assert the driver's
	// internal timeout cancels promptly.
	fake := &fakeRunner{
		script: func(binary, command string, env map[string]string) (rawResult, error) {
			// Intentionally block "forever" — the runner wrapper listens
			// on ctx.Done and returns early when the driver cancels.
			time.Sleep(5 * time.Second)
			return rawResult{}, nil
		},
	}
	d := New(withRunner(fake), WithTimeout(50*time.Millisecond))

	start := time.Now()
	_, err := d.Exec(context.Background(), "slow")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout not honoured: took %v", elapsed)
	}
}

// --- Default runner: smoke -----------------------------------------------

// The default runner needs to wrap exec.ExitError semantics into
// rawResult.ExitCode without returning a Go error for non-zero exit.
// We can't invoke a real bashkit binary in CI, but we can exercise the
// adapter with `/bin/sh -c 'exit 7'` as a stand-in.
func TestDefaultRunner_NonZeroExit(t *testing.T) {
	r := defaultRunner{}
	// Use /bin/sh as a stand-in for bashkit; any POSIX shell satisfies
	// the `-c <script>` interface we rely on.
	raw, err := r.Run(context.Background(), "/bin/sh", "exit 7", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.ExitCode != 7 {
		t.Fatalf("exit = %d, want 7", raw.ExitCode)
	}
}

func TestDefaultRunner_EchoStdout(t *testing.T) {
	r := defaultRunner{}
	raw, err := r.Run(context.Background(), "/bin/sh", "printf hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.Stdout != "hello" {
		t.Fatalf("stdout = %q", raw.Stdout)
	}
	if raw.ExitCode != 0 {
		t.Fatalf("exit = %d", raw.ExitCode)
	}
}

func TestDefaultRunner_BinaryNotFound(t *testing.T) {
	r := defaultRunner{}
	_, err := r.Run(context.Background(), "/definitely/not/a/real/bashkit-binary", "true", nil)
	if err == nil {
		t.Fatalf("expected error when binary is missing")
	}
}

// --- Factory registration ----------------------------------------------------

func TestFactoryRegistration(t *testing.T) {
	drv, err := shell.DefaultFactory.Create("bashkit", map[string]any{
		"binary":  "/opt/bashkit",
		"cwd":     "/w",
		"env":     map[string]string{"K": "v"},
		"timeout": 7 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := drv.(*Driver)
	if !ok {
		t.Fatalf("expected *Driver, got %T", drv)
	}
	if d.binary != "/opt/bashkit" {
		t.Fatalf("binary = %q", d.binary)
	}
	if d.cwd != "/w" {
		t.Fatalf("cwd = %q", d.cwd)
	}
	if d.env["K"] != "v" {
		t.Fatalf("env not set: %v", d.env)
	}
	if d.timeout != 7*time.Second {
		t.Fatalf("timeout = %v", d.timeout)
	}
}

func TestFactoryRegistration_Minimal(t *testing.T) {
	drv, err := shell.DefaultFactory.Create("bashkit", nil)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := drv.(*Driver)
	if d.binary != "bashkit" {
		t.Fatalf("default binary = %q", d.binary)
	}
	if d.timeout != 30*time.Second {
		t.Fatalf("default timeout = %v", d.timeout)
	}
}

func TestFactoryRegistration_BadTimeoutType(t *testing.T) {
	_, err := shell.DefaultFactory.Create("bashkit", map[string]any{
		"timeout": "30s", // strings are not accepted
	})
	if err == nil {
		t.Fatalf("expected error for string timeout")
	}
}

// --- misc --------------------------------------------------------------------

func TestDriver_FS_Default(t *testing.T) {
	d := New(withRunner(&fakeRunner{}))
	if _, ok := d.FS().(*vfs.DirtyTrackingFS); !ok {
		t.Fatalf("expected DirtyTrackingFS")
	}
}

func TestDriver_WithFS_Wraps(t *testing.T) {
	inner := vfs.NewBuiltinFilesystemDriver()
	d := New(withRunner(&fakeRunner{}), WithFS(inner))
	if _, ok := d.FS().(*vfs.DirtyTrackingFS); !ok {
		t.Fatalf("expected DirtyTrackingFS")
	}
}

func TestDriver_NotFoundHandler_RoundTrip(t *testing.T) {
	d := New(withRunner(&fakeRunner{}))
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

func TestDriver_CWD_Default(t *testing.T) {
	d := New(withRunner(&fakeRunner{}))
	if d.CWD() != "/" {
		t.Fatalf("CWD = %q", d.CWD())
	}
}

func TestDriver_CWD_Override(t *testing.T) {
	d := New(withRunner(&fakeRunner{}), WithCWD("/work"))
	if d.CWD() != "/work" {
		t.Fatalf("CWD = %q", d.CWD())
	}
}
