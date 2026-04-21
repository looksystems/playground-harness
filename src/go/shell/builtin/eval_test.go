package builtin

import (
	"context"
	"strings"
	"testing"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// newTestEvaluator builds an Evaluator with a fresh VFS and the small
// set of test builtins the spec calls for (echo, cat, true, false, date,
// yes). Users of the helper can register additional builtins on top.
func newTestEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	fs := vfs.NewBuiltinFilesystemDriver()
	ev := NewEvaluator(fs)
	registerTestBuiltins(ev.Builtins)
	return ev
}

func registerTestBuiltins(reg *BuiltinRegistry) {
	reg.Register("echo", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: strings.Join(args, " ") + "\n"}
	})
	reg.Register("cat", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		// If args supplied, concatenate them as paths via VFS.
		if len(args) > 0 {
			var out strings.Builder
			for _, p := range args {
				abs := p
				if !strings.HasPrefix(abs, "/") && env.CWD != nil {
					abs = *env.CWD + "/" + p
				}
				if env.FS == nil || !env.FS.Exists(abs) {
					return shell.ExecResult{
						Stderr:   "cat: " + p + ": No such file or directory\n",
						ExitCode: 1,
					}
				}
				s, err := env.FS.ReadString(abs)
				if err != nil {
					return shell.ExecResult{
						Stderr:   "cat: " + err.Error() + "\n",
						ExitCode: 1,
					}
				}
				out.WriteString(s)
			}
			return shell.ExecResult{Stdout: out.String()}
		}
		return shell.ExecResult{Stdout: stdin}
	})
	reg.Register("true", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{}
	})
	reg.Register("false", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{ExitCode: 1}
	})
	reg.Register("date", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "Mon Jan 1\n"}
	})
	reg.Register("yes", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "y\n"}
	})
}

// Helper to run a source string and assert stdout/stderr/exit.
func run(t *testing.T, ev *Evaluator, src string) shell.ExecResult {
	t.Helper()
	res, err := ev.Execute(context.Background(), src)
	if err != nil {
		t.Fatalf("Execute(%q) unexpected error: %v", src, err)
	}
	return res
}

// ---------------------------------------------------------------------------
// Simple commands / command lookup
// ---------------------------------------------------------------------------

func TestExecute_UnknownCommand_Returns127(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "nosuchcommand arg")
	if res.ExitCode != 127 {
		t.Fatalf("exit code = %d, want 127", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "command not found") {
		t.Fatalf("stderr = %q, want 'command not found'", res.Stderr)
	}
}

func TestExecute_Echo(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo hi")
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hi\n")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestExecute_AssignmentPersists(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "FOO=1; echo $FOO")
	if res.Stdout != "1\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "1\n")
	}
	if v := ev.Env["FOO"]; v != "1" {
		t.Fatalf("FOO in env = %q, want %q", v, "1")
	}
}

func TestExecute_AssignmentScope_TemporaryOverlay(t *testing.T) {
	ev := newTestEvaluator(t)
	// echo is a builtin; FOO=1 echo $FOO should show FOO=1 during the
	// call but NOT leak to the following statement. Expansion of $FOO
	// happens before dispatch, so we need a builtin that reads env.
	ev.Builtins.Register("readfoo", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: env.Env["FOO"] + "\n"}
	})
	res := run(t, ev, "FOO=1 readfoo; echo \"after=$FOO\"")
	// First statement: FOO=1 visible to readfoo.
	// Second statement: $FOO should be empty.
	want := "1\nafter=\n"
	if res.Stdout != want {
		t.Fatalf("stdout = %q, want %q", res.Stdout, want)
	}
	if _, ok := ev.Env["FOO"]; ok {
		t.Fatalf("FOO should NOT persist in env, got %q", ev.Env["FOO"])
	}
}

func TestExecute_BareAssignment_NoCommand_ExitUnchanged(t *testing.T) {
	ev := newTestEvaluator(t)
	// Run false first to set $? to 1. Then a bare assignment must not
	// change $?.
	ev.setExitStatus(1)
	run(t, ev, "FOO=bar")
	if ev.ExitStatus != 1 {
		t.Fatalf("exit status after bare assignment = %d, want 1 (unchanged)", ev.ExitStatus)
	}
	if ev.Env["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want %q", ev.Env["FOO"], "bar")
	}
}

// ---------------------------------------------------------------------------
// Pipelines
// ---------------------------------------------------------------------------

func TestExecute_Pipeline_TwoStages(t *testing.T) {
	ev := newTestEvaluator(t)
	// cat with no args echoes stdin.
	res := run(t, ev, "echo hi | cat")
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hi\n")
	}
}

func TestExecute_Pipeline_ThreeStages(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("upper", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: strings.ToUpper(stdin)}
	})
	res := run(t, ev, "echo hello | upper | cat")
	if res.Stdout != "HELLO\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "HELLO\n")
	}
}

// ---------------------------------------------------------------------------
// Lists
// ---------------------------------------------------------------------------

func TestExecute_List_Sequential(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo a; echo b")
	if res.Stdout != "a\nb\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "a\nb\n")
	}
}

func TestExecute_List_LastExitCodeWins(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "false; true")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
	res = run(t, ev, "true; false")
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", res.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// AndOr
// ---------------------------------------------------------------------------

func TestExecute_AndOr_AndSuccess(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "true && echo yes")
	if res.Stdout != "yes\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "yes\n")
	}
}

func TestExecute_AndOr_OrFailure(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "false || echo no")
	if res.Stdout != "no\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "no\n")
	}
}

func TestExecute_AndOr_ShortCircuit(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "true && false && echo nope")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty (short-circuit)", res.Stdout)
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", res.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// If / Elif / Else
// ---------------------------------------------------------------------------

func TestExecute_If_Then(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "if true; then echo y; fi")
	if res.Stdout != "y\n" {
		t.Fatalf("stdout = %q, want y", res.Stdout)
	}
}

func TestExecute_If_Else(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "if false; then echo y; else echo n; fi")
	if res.Stdout != "n\n" {
		t.Fatalf("stdout = %q, want n", res.Stdout)
	}
}

func TestExecute_If_Elif(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "if false; then echo a; elif true; then echo b; fi")
	if res.Stdout != "b\n" {
		t.Fatalf("stdout = %q, want b", res.Stdout)
	}
}

// ---------------------------------------------------------------------------
// For
// ---------------------------------------------------------------------------

func TestExecute_For(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "for i in a b c; do echo $i; done")
	if res.Stdout != "a\nb\nc\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "a\nb\nc\n")
	}
}

func TestExecute_For_EmptyList(t *testing.T) {
	ev := newTestEvaluator(t)
	// `for i in` without anything after — parser may reject. Use a
	// controlled "for over expansion of empty var".
	ev.Env["LIST"] = ""
	res := run(t, ev, "for i in $LIST; do echo $i; done")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// While / Until
// ---------------------------------------------------------------------------

func TestExecute_While_NeverRuns(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "while false; do echo x; done")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", res.Stdout)
	}
}

func TestExecute_While_MaxIterations(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.MaxIterations = 5
	res := run(t, ev, "while true; do echo x; done")
	if !strings.Contains(res.Stderr, "Maximum iteration limit exceeded") {
		t.Fatalf("stderr = %q, want 'Maximum iteration limit exceeded'", res.Stderr)
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", res.ExitCode)
	}
}

func TestExecute_Until(t *testing.T) {
	ev := newTestEvaluator(t)
	// Use assignment to break the loop after one iteration: we
	// bump I via $((I+1)) and stop when I is non-empty/non-zero.
	// Simpler: run `until true` — condition succeeds immediately so
	// the body never runs.
	res := run(t, ev, "until true; do echo x; done")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", res.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Case
// ---------------------------------------------------------------------------

func TestExecute_Case_PrefixMatch(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "case abc in a*) echo a;; b*) echo b;; esac")
	if res.Stdout != "a\n" {
		t.Fatalf("stdout = %q, want a", res.Stdout)
	}
}

func TestExecute_Case_Default(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "case xyz in a*) echo a;; *) echo default;; esac")
	if res.Stdout != "default\n" {
		t.Fatalf("stdout = %q, want default", res.Stdout)
	}
}

func TestExecute_Case_NoMatch_Empty(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "case xyz in a*) echo a;; b*) echo b;; esac")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", res.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Subshell
// ---------------------------------------------------------------------------

func TestExecute_Subshell_NoLeak(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "(FOO=1); echo \"FOO=$FOO\"")
	if res.Stdout != "FOO=\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "FOO=\n")
	}
	if _, ok := ev.Env["FOO"]; ok {
		t.Fatalf("FOO leaked out of subshell: %q", ev.Env["FOO"])
	}
}

func TestExecute_Subshell_OutputVisible(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "(echo hi)")
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Redirections
// ---------------------------------------------------------------------------

func TestExecute_Redirect_Out(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo hi > /out.txt")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q, want empty after redirect", res.Stdout)
	}
	contents, err := ev.FS.ReadString("/out.txt")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if contents != "hi\n" {
		t.Fatalf("file contents = %q, want %q", contents, "hi\n")
	}
}

func TestExecute_Redirect_Append(t *testing.T) {
	ev := newTestEvaluator(t)
	run(t, ev, "echo a > /out.txt")
	run(t, ev, "echo b >> /out.txt")
	s, _ := ev.FS.ReadString("/out.txt")
	if s != "a\nb\n" {
		t.Fatalf("file = %q, want %q", s, "a\nb\n")
	}
}

func TestExecute_Redirect_InAndCat(t *testing.T) {
	ev := newTestEvaluator(t)
	_ = ev.FS.WriteString("/in.txt", "hello")
	res := run(t, ev, "cat < /in.txt")
	if res.Stdout != "hello" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hello")
	}
}

func TestExecute_Redirect_Err(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("oops", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stderr: "boom\n", ExitCode: 1}
	})
	res := run(t, ev, "oops 2> /err.log")
	if res.Stderr != "" {
		t.Fatalf("stderr = %q, want empty (redirected)", res.Stderr)
	}
	s, _ := ev.FS.ReadString("/err.log")
	if s != "boom\n" {
		t.Fatalf("err.log = %q, want %q", s, "boom\n")
	}
}

func TestExecute_Redirect_ErrToOut(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("mixed", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "out\n", Stderr: "err\n"}
	})
	res := run(t, ev, "mixed 2>&1")
	// 2>&1 merges stderr into stdout.
	if res.Stdout != "out\nerr\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "out\nerr\n")
	}
	if res.Stderr != "" {
		t.Fatalf("stderr = %q, want empty", res.Stderr)
	}
}

func TestExecute_Redirect_Both(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("mixed", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "a\n", Stderr: "b\n"}
	})
	res := run(t, ev, "mixed &> /all.log")
	if res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("streams should be redirected; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	s, _ := ev.FS.ReadString("/all.log")
	if s != "a\nb\n" {
		t.Fatalf("all.log = %q, want %q", s, "a\nb\n")
	}
}

func TestExecute_Redirect_In_MissingFile(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "cat < /missing.txt")
	if res.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "No such file") {
		t.Fatalf("stderr = %q", res.Stderr)
	}
}

// ---------------------------------------------------------------------------
// Command substitution
// ---------------------------------------------------------------------------

func TestExecute_CmdSub(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo $(date)")
	if res.Stdout != "Mon Jan 1\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "Mon Jan 1\n")
	}
}

func TestExecute_CmdSub_Nested(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo $(echo $(echo hi))")
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

func TestExecute_CmdSub_Backtick(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo `date`")
	if res.Stdout != "Mon Jan 1\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "Mon Jan 1\n")
	}
}

// ---------------------------------------------------------------------------
// $? — exit status
// ---------------------------------------------------------------------------

func TestExecute_ExitStatus(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("ret", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{ExitCode: 5}
	})
	res := run(t, ev, "ret; echo $?")
	if res.Stdout != "5\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "5\n")
	}
}

// ---------------------------------------------------------------------------
// Safety limits: iteration cap, output cap
// ---------------------------------------------------------------------------

func TestExecute_NestedLoops_UnderCap(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "for i in 1 2 3 4 5 6 7 8 9 10; do for j in a b; do echo x; done; done")
	// 10 * 2 = 20 iterations, each prints "x\n".
	want := strings.Repeat("x\n", 20)
	if res.Stdout != want {
		t.Fatalf("stdout has %d chars, want %d", len(res.Stdout), len(want))
	}
}

func TestExecute_MaxOutput_Truncates(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.MaxOutput = 10
	ev.Builtins.Register("bigout", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: strings.Repeat("x", 100)}
	})
	res := run(t, ev, "bigout")
	if !strings.HasPrefix(res.Stdout, strings.Repeat("x", 10)) {
		t.Fatalf("stdout does not start with 10 x's: %q", res.Stdout[:20])
	}
	if !strings.Contains(res.Stdout, "truncated") {
		t.Fatalf("stdout missing truncation marker: %q", res.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Parse error surface
// ---------------------------------------------------------------------------

func TestExecute_ParseError(t *testing.T) {
	ev := newTestEvaluator(t)
	// Unterminated double-quote string — tokenizer rejects.
	res := run(t, ev, "echo \"unterminated")
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "parse error") {
		t.Fatalf("stderr = %q, want parse error", res.Stderr)
	}
}

func TestExecute_EmptyInput(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "")
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("empty exec = %+v, want zero-value", res)
	}
	res = run(t, ev, "   \n  ")
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("whitespace-only exec = %+v, want zero-value", res)
	}
}

// ---------------------------------------------------------------------------
// NotFoundHandler
// ---------------------------------------------------------------------------

func TestExecute_NotFoundHandler(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.NotFoundHandler = func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		return &shell.ExecResult{Stdout: "handled:" + cmd + "\n"}
	}
	res := run(t, ev, "magic")
	if res.Stdout != "handled:magic\n" {
		t.Fatalf("stdout = %q, want handled:magic", res.Stdout)
	}
}

func TestExecute_NotFoundHandler_FallThrough(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.NotFoundHandler = func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		return nil
	}
	res := run(t, ev, "magic")
	if res.ExitCode != 127 {
		t.Fatalf("exit = %d, want 127", res.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// BuiltinRegistry surface
// ---------------------------------------------------------------------------

func TestBuiltinRegistry_RegisterGetUnregister(t *testing.T) {
	r := NewBuiltinRegistry()
	r.Register("foo", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "foo!\n"}
	})
	if _, ok := r.Get("foo"); !ok {
		t.Fatalf("expected foo to be registered")
	}
	r.Unregister("foo")
	if _, ok := r.Get("foo"); ok {
		t.Fatalf("expected foo to be unregistered")
	}
}

func TestBuiltinRegistry_RegisterUser_AdaptsShellHandler(t *testing.T) {
	r := NewBuiltinRegistry()
	r.RegisterUser("shout", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: strings.ToUpper(strings.Join(args, " ") + stdin)}
	})
	h, ok := r.Get("shout")
	if !ok {
		t.Fatalf("shout not found")
	}
	got := h(context.Background(), nil, []string{"hi"}, "!")
	if got.Stdout != "HI!" {
		t.Fatalf("stdout = %q, want %q", got.Stdout, "HI!")
	}
}

func TestBuiltinRegistry_Names_Sorted(t *testing.T) {
	r := NewBuiltinRegistry()
	r.Register("zeta", nil).Register("alpha", nil).Register("mu", nil)
	names := r.Names()
	want := []string{"alpha", "mu", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(names), len(want), names)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("names[%d] = %q, want %q", i, names[i], n)
		}
	}
}

// ---------------------------------------------------------------------------
// Additional: redirect-after-write followed by cat, parameter expansion
// ---------------------------------------------------------------------------

func TestExecute_RedirectThenCat(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo hi > /out.txt; cat /out.txt")
	if res.Stdout != "hi\n" {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}

func TestExecute_ForOverVar(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Env["LIST"] = "x y z"
	res := run(t, ev, "for i in $LIST; do echo $i; done")
	if res.Stdout != "x\ny\nz\n" {
		t.Fatalf("stdout = %q, want x\\ny\\nz", res.Stdout)
	}
}

func TestExecute_ArithmeticExpansion(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo $((2 + 3))")
	if res.Stdout != "5\n" {
		t.Fatalf("stdout = %q, want 5", res.Stdout)
	}
}

func TestExecute_PipelineExitPropagation(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "echo hi | false")
	if res.ExitCode != 1 {
		t.Fatalf("pipeline exit = %d, want 1 (last cmd)", res.ExitCode)
	}
}

func TestExecute_AssignmentOverlay_RestoresPriorValue(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Env["FOO"] = "orig"
	ev.Builtins.Register("readfoo", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: env.Env["FOO"] + "\n"}
	})
	res := run(t, ev, "FOO=tmp readfoo; echo $FOO")
	if res.Stdout != "tmp\norig\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "tmp\norig\n")
	}
	if ev.Env["FOO"] != "orig" {
		t.Fatalf("FOO = %q, want orig (restored)", ev.Env["FOO"])
	}
}

func TestExecute_ExecEnvAllowsCdMutation(t *testing.T) {
	ev := newTestEvaluator(t)
	ev.Builtins.Register("cd", func(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
		if len(args) == 0 {
			return shell.ExecResult{}
		}
		*env.CWD = args[0]
		return shell.ExecResult{}
	})
	run(t, ev, "cd /tmp")
	if ev.CWD != "/tmp" {
		t.Fatalf("CWD = %q, want /tmp", ev.CWD)
	}
}

func TestExecute_ForSetsVarVisibleToExpansion(t *testing.T) {
	ev := newTestEvaluator(t)
	res := run(t, ev, "for i in 1 2; do echo \"item=$i\"; done")
	if res.Stdout != "item=1\nitem=2\n" {
		t.Fatalf("stdout = %q, want item=1\\nitem=2", res.Stdout)
	}
}
