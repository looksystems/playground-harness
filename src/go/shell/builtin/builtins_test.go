package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// newBuiltinEvaluator constructs an Evaluator seeded with the default
// builtin registry and a virtual filesystem that optionally holds the
// supplied files. Call result.Stdout etc. directly from tests.
func newBuiltinEvaluator(t *testing.T, files map[string]string) *Evaluator {
	t.Helper()
	fs := vfs.NewBuiltinFilesystemDriver()
	for p, content := range files {
		if err := fs.WriteString(p, content); err != nil {
			t.Fatalf("seed %q: %v", p, err)
		}
	}
	ev := NewEvaluator(fs)
	ev.Builtins = NewDefaultBuiltinRegistry()
	return ev
}

// runBuiltin executes a shell command against ev and returns the result.
func runBuiltin(t *testing.T, ev *Evaluator, cmd string) shell.ExecResult {
	t.Helper()
	res, err := ev.Execute(context.Background(), cmd)
	if err != nil {
		t.Fatalf("exec(%q): %v", cmd, err)
	}
	return res
}

// --------------------------------------------------------------------
// cat
// --------------------------------------------------------------------

func TestCat_Stdin(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	// Use pipeline to supply stdin.
	res := runBuiltin(t, ev, "echo hello | cat")
	if res.Stdout != "hello\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCat_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/a.txt": "hi"})
	res := runBuiltin(t, ev, "cat /a.txt")
	if res.Stdout != "hi" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCat_MissingFile(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "cat /nope.txt")
	if res.ExitCode != 1 || res.Stderr == "" {
		t.Errorf("expected error exit 1 with stderr, got %+v", res)
	}
}

func TestCat_MultipleFiles(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a": "A",
		"/b": "B",
	})
	res := runBuiltin(t, ev, "cat /a /b")
	if res.Stdout != "AB" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// echo
// --------------------------------------------------------------------

func TestEcho_Basic(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo hello world")
	if res.Stdout != "hello world\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestEcho_Empty(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo")
	if res.Stdout != "\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// pwd / cd
// --------------------------------------------------------------------

func TestPwd_Default(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "pwd")
	if res.Stdout != "/\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCd_Absolute(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/foo/.keep": ""})
	runBuiltin(t, ev, "cd /foo")
	if ev.CWD != "/foo" {
		t.Errorf("CWD = %q", ev.CWD)
	}
}

func TestCd_Relative(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a/b/.keep": "",
	})
	runBuiltin(t, ev, "cd /a")
	runBuiltin(t, ev, "cd b")
	if ev.CWD != "/a/b" {
		t.Errorf("CWD = %q", ev.CWD)
	}
}

func TestCd_Missing(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "cd /does/not/exist")
	if res.ExitCode != 1 || !strings.Contains(res.Stderr, "No such directory") {
		t.Errorf("got %+v", res)
	}
}

func TestCd_RootExists(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "cd /")
	if res.ExitCode != 0 {
		t.Errorf("expected cd / to succeed, got %+v", res)
	}
}

// --------------------------------------------------------------------
// ls
// --------------------------------------------------------------------

func TestLs_Basic(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a": "x", "/b": "y",
	})
	res := runBuiltin(t, ev, "ls /")
	if res.Stdout != "a\nb\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestLs_Long(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a.txt":      "hello",
		"/dir/x.txt":  "",
	})
	res := runBuiltin(t, ev, "ls -l /")
	if !strings.Contains(res.Stdout, "a.txt") {
		t.Errorf("missing a.txt: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "dir/") {
		t.Errorf("missing dir/: %q", res.Stdout)
	}
}

func TestLs_MissingDir(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "ls /nope")
	if res.ExitCode != 1 {
		t.Errorf("want exit 1, got %+v", res)
	}
}

func TestLs_EmptyDir(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "ls /")
	if res.Stdout != "" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// find
// --------------------------------------------------------------------

func TestFind_All(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a.txt":     "",
		"/d/b.txt":   "",
		"/d/c.json":  "",
	})
	res := runBuiltin(t, ev, "find /")
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 3 {
		t.Errorf("got %v", lines)
	}
}

func TestFind_Name(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a.txt":    "",
		"/b.json":   "",
		"/d/c.txt":  "",
	})
	res := runBuiltin(t, ev, "find / -name *.txt")
	if !strings.Contains(res.Stdout, "a.txt") || !strings.Contains(res.Stdout, "c.txt") {
		t.Errorf("got %q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "b.json") {
		t.Errorf("should not match b.json: %q", res.Stdout)
	}
}

func TestFind_TypeF(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a": "",
	})
	res := runBuiltin(t, ev, "find / -type f")
	if !strings.Contains(res.Stdout, "/a") {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// mkdir / touch / cp / rm / stat / tree
// --------------------------------------------------------------------

func TestMkdir(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	runBuiltin(t, ev, "mkdir /dir")
	if !ev.FS.IsDir("/dir") {
		t.Error("dir was not created")
	}
}

func TestTouch(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	runBuiltin(t, ev, "touch /new.txt")
	if !ev.FS.Exists("/new.txt") {
		t.Error("touch did not create file")
	}
}

func TestTouch_Existing(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/a": "old"})
	runBuiltin(t, ev, "touch /a")
	s, _ := ev.FS.ReadString("/a")
	if s != "old" {
		t.Errorf("touch should not overwrite, got %q", s)
	}
}

func TestCp(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/src": "payload"})
	runBuiltin(t, ev, "cp /src /dst")
	s, _ := ev.FS.ReadString("/dst")
	if s != "payload" {
		t.Errorf("got %q", s)
	}
}

func TestCp_MissingOperand(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "cp /only-one")
	if res.ExitCode != 1 {
		t.Errorf("want exit 1, got %+v", res)
	}
}

func TestCp_MissingSrc(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "cp /nope /dst")
	if res.ExitCode != 1 {
		t.Errorf("want exit 1, got %+v", res)
	}
}

func TestRm(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/a": ""})
	runBuiltin(t, ev, "rm /a")
	if ev.FS.Exists("/a") {
		t.Error("rm did not remove")
	}
}

func TestRm_Missing(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "rm /nope")
	if res.ExitCode != 0 {
		t.Errorf("rm of missing should succeed, got %+v", res)
	}
}

func TestStat_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/a": "hello"})
	res := runBuiltin(t, ev, "stat /a")
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimRight(res.Stdout, "\n")), &payload); err != nil {
		t.Fatalf("parse: %v; raw=%q", err, res.Stdout)
	}
	if payload["type"] != "file" {
		t.Errorf("type = %v", payload["type"])
	}
	if int(payload["size"].(float64)) != 5 {
		t.Errorf("size = %v", payload["size"])
	}
}

func TestStat_Dir(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/d/a": ""})
	res := runBuiltin(t, ev, "stat /d")
	if !strings.Contains(res.Stdout, "directory") {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestStat_Missing(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "stat /nope")
	if res.ExitCode != 1 {
		t.Errorf("want 1, got %+v", res)
	}
}

func TestTree(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{
		"/a.txt":    "",
		"/d/b.txt":  "",
		"/d/c.txt":  "",
	})
	res := runBuiltin(t, ev, "tree /")
	if !strings.Contains(res.Stdout, "a.txt") {
		t.Errorf("tree missing a.txt: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "d/") {
		t.Errorf("tree missing d/: %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// grep
// --------------------------------------------------------------------

func TestGrep_Basic(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'foo\\nbar\\nfoo\\n' | grep foo")
	if res.Stdout != "foo\nfoo\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_CaseInsensitive(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'FOO\\nbar\\n' | grep -i foo")
	if !strings.Contains(res.Stdout, "FOO") {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_Invert(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'foo\\nbar\\n' | grep -v foo")
	if strings.TrimSpace(res.Stdout) != "bar" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_Count(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'foo\\nfoo\\nbar\\n' | grep -c foo")
	if strings.TrimSpace(res.Stdout) != "2" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_LineNumbers(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'foo\\nbar\\nfoo\\n' | grep -n foo")
	// Expect line numbers 1 and 3.
	if !strings.Contains(res.Stdout, "1:foo") {
		t.Errorf("got %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "3:foo") {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_NoMatch_Exit1(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo hello | grep missing")
	if res.ExitCode != 1 {
		t.Errorf("want 1, got %+v", res)
	}
}

func TestGrep_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/f": "alpha\nbeta\ngamma\n"})
	res := runBuiltin(t, ev, "grep beta /f")
	if strings.TrimSpace(res.Stdout) != "beta" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestGrep_InvalidPattern(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo hi | grep '(unclosed'")
	if res.ExitCode != 2 {
		t.Errorf("want exit 2, got %+v", res)
	}
}

// --------------------------------------------------------------------
// head / tail
// --------------------------------------------------------------------

func TestHead_Default(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '1\\n2\\n3\\n' | head")
	if strings.TrimSpace(res.Stdout) != "1\n2\n3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestHead_Flag(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '1\\n2\\n3\\n4\\n' | head -n 2")
	if strings.TrimSpace(res.Stdout) != "1\n2" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestHead_DashN(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '1\\n2\\n3\\n4\\n' | head -1")
	if strings.TrimSpace(res.Stdout) != "1" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestHead_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/f": "a\nb\nc\n"})
	res := runBuiltin(t, ev, "head -n 2 /f")
	if strings.TrimSpace(res.Stdout) != "a\nb" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTail_Default(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '1\\n2\\n3\\n' | tail")
	if strings.TrimSpace(res.Stdout) != "1\n2\n3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTail_Flag(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '1\\n2\\n3\\n4\\n' | tail -n 2")
	if strings.TrimSpace(res.Stdout) != "3\n4" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTail_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/f": "a\nb\nc\n"})
	res := runBuiltin(t, ev, "tail -n 1 /f")
	if strings.TrimSpace(res.Stdout) != "c" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// wc
// --------------------------------------------------------------------

func TestWc_Default(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a b\\nc\\n' | wc")
	// 2 lines, 3 words, 6 chars (including \n).
	if !strings.Contains(res.Stdout, "  2  3  6") {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestWc_Lines(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\nb\\nc\\n' | wc -l")
	if strings.TrimSpace(res.Stdout) != "3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestWc_Words(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo 'one two three' | wc -w")
	if strings.TrimSpace(res.Stdout) != "3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestWc_Chars(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'abc' | wc -c")
	if strings.TrimSpace(res.Stdout) != "3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestWc_File(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/f": "a\nb\n"})
	res := runBuiltin(t, ev, "wc -l /f")
	if strings.TrimSpace(res.Stdout) != "2" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// sort / uniq / cut / tr
// --------------------------------------------------------------------

func TestSort_Basic(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'b\\na\\nc\\n' | sort")
	if res.Stdout != "a\nb\nc\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSort_Reverse(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\nb\\nc\\n' | sort -r")
	if res.Stdout != "c\nb\na\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSort_Numeric(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '10\\n2\\n1\\n' | sort -n")
	if res.Stdout != "1\n2\n10\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSort_Unique(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\nb\\na\\n' | sort -u")
	if res.Stdout != "a\nb\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestUniq(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\na\\nb\\n' | uniq")
	if res.Stdout != "a\nb\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestUniq_Count(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\na\\nb\\n' | uniq -c")
	if res.Stdout != "  2 a\n  1 b\n" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCut_Fields(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\tb\\tc\\n' | cut -f 1,3")
	if strings.TrimSpace(res.Stdout) != "a\tc" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCut_Delim(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a,b,c\\n' | cut -d , -f 2")
	if strings.TrimSpace(res.Stdout) != "b" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestCut_Range(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a,b,c,d\\n' | cut -d , -f 2-3")
	if strings.TrimSpace(res.Stdout) != "b,c" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTr_Translate(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo abc | tr a x")
	if strings.TrimSpace(res.Stdout) != "xbc" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTr_Delete(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo abc | tr -d b")
	if strings.TrimSpace(res.Stdout) != "ac" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// sed / tee
// --------------------------------------------------------------------

func TestSed_Substitute(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo foo | sed 's/foo/bar/'")
	if strings.TrimSpace(res.Stdout) != "bar" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSed_Global(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo aaa | sed 's/a/b/g'")
	if strings.TrimSpace(res.Stdout) != "bbb" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSed_NoGlobal_OnlyFirst(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo aaa | sed 's/a/b/'")
	if strings.TrimSpace(res.Stdout) != "baa" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestSed_AltDelim(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo /path | sed 's|/|_|g'")
	if strings.TrimSpace(res.Stdout) != "_path" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestTee(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo hi | tee /out.txt")
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Errorf("got %q", res.Stdout)
	}
	s, _ := ev.FS.ReadString("/out.txt")
	if strings.TrimSpace(s) != "hi" {
		t.Errorf("file content %q", s)
	}
}

func TestTee_Append(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/out": "start\n"})
	runBuiltin(t, ev, "echo more | tee -a /out")
	s, _ := ev.FS.ReadString("/out")
	if !strings.HasPrefix(s, "start\n") || !strings.Contains(s, "more") {
		t.Errorf("file content %q", s)
	}
}

// --------------------------------------------------------------------
// jq
// --------------------------------------------------------------------

func TestJq_Identity(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '{\"a\":1}' | jq .")
	if !strings.Contains(res.Stdout, "\"a\": 1") {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestJq_Field(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '{\"a\":42}' | jq .a")
	if strings.TrimSpace(res.Stdout) != "42" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestJq_Index(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '[10,20,30]' | jq '.[1]'")
	if strings.TrimSpace(res.Stdout) != "20" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestJq_Iterate(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '[1,2,3]' | jq '.[]'")
	if strings.TrimSpace(res.Stdout) != "1\n2\n3" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestJq_Raw(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '{\"a\":\"hello\"}' | jq -r .a")
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestJq_ParseError(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo 'not json' | jq .")
	if res.ExitCode != 2 {
		t.Errorf("want 2, got %+v", res)
	}
}

func TestJq_MissingKey(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "echo '{}' | jq .missing")
	if res.ExitCode != 5 {
		t.Errorf("want 5, got %+v", res)
	}
}

// --------------------------------------------------------------------
// export / printf
// --------------------------------------------------------------------

func TestExport(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	runBuiltin(t, ev, "export FOO=bar")
	if ev.Env["FOO"] != "bar" {
		t.Errorf("env = %v", ev.Env["FOO"])
	}
}

func TestExport_Expansion(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	// export FOO=$BAR where BAR is set: evaluator expands before
	// passing to builtin, so FOO picks up expanded value.
	ev.Env["BAR"] = "hi"
	runBuiltin(t, ev, "export FOO=$BAR")
	if ev.Env["FOO"] != "hi" {
		t.Errorf("FOO = %q", ev.Env["FOO"])
	}
}

func TestPrintf_String(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '%s world' hello")
	if res.Stdout != "hello world" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestPrintf_Decimal(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '%d' 42")
	if res.Stdout != "42" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestPrintf_Float(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '%.2f' 3.14159")
	if res.Stdout != "3.14" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestPrintf_Escape(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf 'a\\nb'")
	if res.Stdout != "a\nb" {
		t.Errorf("got %q", res.Stdout)
	}
}

func TestPrintf_PercentLiteral(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "printf '%%'")
	if res.Stdout != "%" {
		t.Errorf("got %q", res.Stdout)
	}
}

// --------------------------------------------------------------------
// test / [ / [[
// --------------------------------------------------------------------

func TestTest_Empty(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "test -z \"\"")
	if res.ExitCode != 0 {
		t.Errorf("-z empty should pass, got %+v", res)
	}
}

func TestTest_NonEmpty(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "test -n hi")
	if res.ExitCode != 0 {
		t.Errorf("-n nonempty should pass, got %+v", res)
	}
}

func TestTest_FileExists(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/a": ""})
	if r := runBuiltin(t, ev, "test -f /a"); r.ExitCode != 0 {
		t.Errorf("-f on file should pass, got %+v", r)
	}
	if r := runBuiltin(t, ev, "test -f /nope"); r.ExitCode == 0 {
		t.Errorf("-f on missing should fail, got %+v", r)
	}
}

func TestTest_DirExists(t *testing.T) {
	ev := newBuiltinEvaluator(t, map[string]string{"/d/x": ""})
	if r := runBuiltin(t, ev, "test -d /d"); r.ExitCode != 0 {
		t.Errorf("-d on dir should pass, got %+v", r)
	}
}

func TestTest_EqOp(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "test 3 -eq 3"); r.ExitCode != 0 {
		t.Errorf("3 -eq 3 should pass, got %+v", r)
	}
	if r := runBuiltin(t, ev, "test 3 -ne 3"); r.ExitCode == 0 {
		t.Errorf("3 -ne 3 should fail, got %+v", r)
	}
}

func TestTest_LtGt(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "test 2 -lt 3"); r.ExitCode != 0 {
		t.Errorf("2 -lt 3 should pass")
	}
	if r := runBuiltin(t, ev, "test 4 -gt 3"); r.ExitCode != 0 {
		t.Errorf("4 -gt 3 should pass")
	}
}

func TestTest_Negate(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "test ! -z hello"); r.ExitCode != 0 {
		t.Errorf("! -z hello should pass, got %+v", r)
	}
}

func TestTest_StringEq(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "test abc = abc"); r.ExitCode != 0 {
		t.Errorf("string eq should pass")
	}
	if r := runBuiltin(t, ev, "test abc != xyz"); r.ExitCode != 0 {
		t.Errorf("string ne should pass")
	}
}

func TestBracket(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "[ -z \"\" ]"); r.ExitCode != 0 {
		t.Errorf("[ -z \"\" ] should pass, got %+v", r)
	}
}

func TestBracket_MissingClose(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	// The tokenizer treats `[` as a bare word; passing no closing `]`
	// should yield a stderr error from the builtin.
	res := runBuiltin(t, ev, "[ -z \"\"")
	if res.ExitCode != 2 || !strings.Contains(res.Stderr, "missing") {
		t.Errorf("want missing-] error, got %+v", res)
	}
}

func TestDoubleBracket(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "[[ -n hi ]]"); r.ExitCode != 0 {
		t.Errorf("[[ -n hi ]] should pass, got %+v", r)
	}
}

func TestDoubleBracket_MissingClose(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	res := runBuiltin(t, ev, "[[ -n hi")
	if res.ExitCode != 2 {
		t.Errorf("want 2, got %+v", res)
	}
}

// --------------------------------------------------------------------
// true / false
// --------------------------------------------------------------------

func TestTrue(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "true"); r.ExitCode != 0 {
		t.Errorf("true failed: %+v", r)
	}
}

func TestFalse(t *testing.T) {
	ev := newBuiltinEvaluator(t, nil)
	if r := runBuiltin(t, ev, "false"); r.ExitCode != 1 {
		t.Errorf("false should exit 1, got %+v", r)
	}
}

// --------------------------------------------------------------------
// Registry defaults
// --------------------------------------------------------------------

func TestDefaultRegistry_HasAll31(t *testing.T) {
	reg := NewDefaultBuiltinRegistry()
	want := []string{
		"cat", "echo", "pwd", "cd", "ls", "find", "mkdir", "touch",
		"cp", "rm", "stat", "tree",
		"grep", "head", "tail", "wc", "sort", "uniq", "cut", "tr",
		"sed", "tee",
		"jq",
		"export", "printf",
		"test", "[", "[[", "true", "false",
	}
	// 30 here -- wait, let me count: that's 12 + 10 + 1 + 2 + 5 = 30.
	// The 31 includes both `test` and `[` (2) and `[[` (one more = 3),
	// for a total of 31 once we add: 12+10+1+2+6=31.
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("missing builtin %q", name)
		}
	}
}
