package filetools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"agent-harness/go/shell/vfs"
	"agent-harness/go/tools"
)

func newRig(t *testing.T, opts ...Option) (*tools.Registry, *vfs.VirtualFS, *FileTools) {
	t.Helper()
	v := vfs.New(nil)
	d := &vfs.BuiltinFilesystemDriver{FS: v}
	reg := tools.New()
	cwd := func() string { return "/work" }
	ft := RegisterAll(reg, d, cwd, opts...)
	return reg, v, ft
}

func mustExec(t *testing.T, reg *tools.Registry, name string, args map[string]any) any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := reg.Execute(context.Background(), name, raw)
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return out
}

func mustExecErr(t *testing.T, reg *tools.Registry, name string, args map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	_, err := reg.Execute(context.Background(), name, raw)
	if err == nil {
		t.Fatalf("expected error from %s, got nil", name)
	}
	return err.Error()
}

// ---------- Read ----------

func TestRead_Basic(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.txt", "hello\nworld")
	out := mustExec(t, reg, "Read", map[string]any{"file_path": "/work/a.txt"}).(string)
	if !strings.Contains(out, "     1\thello") || !strings.Contains(out, "     2\tworld") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRead_OffsetLimit(t *testing.T) {
	reg, v, _ := newRig(t)
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		if i > 1 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "line%d", i)
	}
	_ = v.WriteString("/work/big.txt", sb.String())
	out := mustExec(t, reg, "Read", map[string]any{"file_path": "/work/big.txt", "offset": 4, "limit": 2}).(string)
	if !strings.Contains(out, "     4\tline4") || !strings.Contains(out, "     5\tline5") {
		t.Fatalf("unexpected output: %q", out)
	}
	if strings.Contains(out, "line3") || strings.Contains(out, "line6") {
		t.Fatalf("output bled outside window: %q", out)
	}
}

func TestRead_Missing(t *testing.T) {
	reg, _, _ := newRig(t)
	msg := mustExecErr(t, reg, "Read", map[string]any{"file_path": "/nope.txt"})
	if !strings.Contains(msg, "does not exist") {
		t.Fatalf("expected does-not-exist error, got %q", msg)
	}
}

func TestRead_Directory(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/dir/a.txt", "x")
	msg := mustExecErr(t, reg, "Read", map[string]any{"file_path": "/work/dir"})
	if !strings.Contains(msg, "directory") {
		t.Fatalf("expected directory error, got %q", msg)
	}
}

func TestRead_Binary(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.Write("/work/blob.bin", []byte("text\x00stuff"))
	msg := mustExecErr(t, reg, "Read", map[string]any{"file_path": "/work/blob.bin"})
	if !strings.Contains(msg, "binary") {
		t.Fatalf("expected binary error, got %q", msg)
	}
}

// ---------- Write ----------

func TestWrite_Creates(t *testing.T) {
	reg, v, _ := newRig(t)
	out := mustExec(t, reg, "Write", map[string]any{"file_path": "/work/new.txt", "content": "hi"}).(string)
	if !strings.Contains(out, "written") {
		t.Fatalf("unexpected: %q", out)
	}
	got, _ := v.ReadString("/work/new.txt")
	if got != "hi" {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestWrite_Overwrites(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/x.txt", "old")
	mustExec(t, reg, "Write", map[string]any{"file_path": "/work/x.txt", "content": "new"})
	got, _ := v.ReadString("/work/x.txt")
	if got != "new" {
		t.Fatalf("content mismatch: %q", got)
	}
}

// ---------- Edit ----------

func TestEdit_Unique(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/c.txt", "alpha beta gamma")
	out := mustExec(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "beta", "new_string": "BETA",
	}).(string)
	if !strings.Contains(out, "edited") {
		t.Fatalf("unexpected: %q", out)
	}
	got, _ := v.ReadString("/work/c.txt")
	if got != "alpha BETA gamma" {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestEdit_Missing(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/c.txt", "hello")
	msg := mustExecErr(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "zzz", "new_string": "qqq",
	})
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not-found, got %q", msg)
	}
}

func TestEdit_Multiple_NoReplaceAll(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/c.txt", "x x x")
	msg := mustExecErr(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "x", "new_string": "y",
	})
	if !strings.Contains(msg, "Found 3 matches") {
		t.Fatalf("expected 3-matches, got %q", msg)
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/c.txt", "x x x")
	mustExec(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "x", "new_string": "y", "replace_all": true,
	})
	got, _ := v.ReadString("/work/c.txt")
	if got != "y y y" {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestEdit_ReadGate_Off(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/c.txt", "hello")
	out := mustExec(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "hello", "new_string": "bye",
	}).(string)
	if !strings.Contains(out, "edited") {
		t.Fatalf("unexpected: %q", out)
	}
}

func TestEdit_ReadGate_Blocks(t *testing.T) {
	reg, v, _ := newRig(t, EnforceReadGate())
	_ = v.WriteString("/work/c.txt", "hello")
	msg := mustExecErr(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "hello", "new_string": "bye",
	})
	if !strings.Contains(msg, "not been read") {
		t.Fatalf("expected read-gate error, got %q", msg)
	}
}

func TestEdit_ReadGate_PassesAfterRead(t *testing.T) {
	reg, v, _ := newRig(t, EnforceReadGate())
	_ = v.WriteString("/work/c.txt", "hello")
	mustExec(t, reg, "Read", map[string]any{"file_path": "/work/c.txt"})
	out := mustExec(t, reg, "Edit", map[string]any{
		"file_path": "/work/c.txt", "old_string": "hello", "new_string": "bye",
	}).(string)
	if !strings.Contains(out, "edited") {
		t.Fatalf("unexpected: %q", out)
	}
	got, _ := v.ReadString("/work/c.txt")
	if got != "bye" {
		t.Fatalf("content mismatch: %q", got)
	}
}

// ---------- Glob ----------

func TestGlob_Basic(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "x")
	_ = v.WriteString("/work/b.go", "y")
	_ = v.WriteString("/work/c.txt", "z")
	out := mustExec(t, reg, "Glob", map[string]any{"pattern": "*.go"}).([]string)
	sort.Strings(out)
	if !equalStr(out, []string{"/work/a.go", "/work/b.go"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGlob_Recursive(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "x")
	_ = v.WriteString("/work/sub/b.go", "y")
	_ = v.WriteString("/work/sub/deeper/c.go", "z")
	out := mustExec(t, reg, "Glob", map[string]any{"pattern": "**/*.go"}).([]string)
	sort.Strings(out)
	want := []string{"/work/a.go", "/work/sub/b.go", "/work/sub/deeper/c.go"}
	if !equalStr(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestGlob_MtimeSort(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/old.go", "x")
	_ = v.WriteString("/work/middle.go", "y")
	_ = v.WriteString("/work/new.go", "z")
	_ = v.WriteString("/work/new.go", "z2") // bump mtime
	out := mustExec(t, reg, "Glob", map[string]any{"pattern": "*.go"}).([]string)
	if out[0] != "/work/new.go" {
		t.Fatalf("expected new.go first, got %v", out)
	}
}

func TestGlob_PathDefaultsToCwd(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "x")
	_ = v.WriteString("/elsewhere/b.go", "y")
	out := mustExec(t, reg, "Glob", map[string]any{"pattern": "*.go"}).([]string)
	if !equalStr(out, []string{"/work/a.go"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGlob_ExplicitPath(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "x")
	_ = v.WriteString("/elsewhere/b.go", "y")
	out := mustExec(t, reg, "Glob", map[string]any{"pattern": "*.go", "path": "/elsewhere"}).([]string)
	if !equalStr(out, []string{"/elsewhere/b.go"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

// ---------- Grep ----------

func TestGrep_FilesWithMatches(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "package x\nfunc Foo() {}")
	_ = v.WriteString("/work/b.go", "package y\nfunc Bar() {}")
	_ = v.WriteString("/work/c.go", "var z int")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "^func"}).([]string)
	sort.Strings(out)
	if !equalStr(out, []string{"/work/a.go", "/work/b.go"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_Count(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "func\nfunc\nfunc")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "func", "output_mode": "count"})
	if !strings.Contains(fmt.Sprintf("%v", out), "/work/a.go") || !strings.Contains(fmt.Sprintf("%v", out), "3") {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_Content(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "alpha\nbeta TARGET here\ngamma")
	raw, _ := json.Marshal(map[string]any{
		"pattern":        "TARGET",
		"output_mode":    "content",
		"line_numbers":   true,
		"context_before": 1,
		"context_after":  1,
	})
	out, err := reg.Execute(context.Background(), "Grep", raw)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Marshal/unmarshal to assert against generic types.
	b, _ := json.Marshal(out)
	var got []map[string]any
	_ = json.Unmarshal(b, &got)
	if got[0]["path"] != "/work/a.go" {
		t.Fatalf("wrong path: %v", got[0])
	}
	matches := got[0]["matches"].([]any)
	first := matches[0].(map[string]any)
	if int(first["line"].(float64)) != 2 {
		t.Fatalf("wrong line: %v", first["line"])
	}
	ctx := first["context"].([]any)
	if len(ctx) != 3 {
		t.Fatalf("expected 3 context lines, got %d", len(ctx))
	}
	mid := ctx[1].(map[string]any)
	if mid["match"] != true || mid["text"] != "beta TARGET here" {
		t.Fatalf("unexpected mid: %v", mid)
	}
}

func TestGrep_GlobFilter(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "func")
	_ = v.WriteString("/work/a.txt", "func")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "func", "glob": "*.go"}).([]string)
	if !equalStr(out, []string{"/work/a.go"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_TypeFilter(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.go", "func")
	_ = v.WriteString("/work/a.py", "func")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "func", "type": "py"}).([]string)
	if !equalStr(out, []string{"/work/a.py"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_CaseInsensitive(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.txt", "Hello WORLD")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "world"}).([]string)
	if len(out) != 0 {
		t.Fatalf("expected no matches, got %v", out)
	}
	out = mustExec(t, reg, "Grep", map[string]any{"pattern": "world", "case_insensitive": true}).([]string)
	if !equalStr(out, []string{"/work/a.txt"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_Multiline(t *testing.T) {
	reg, v, _ := newRig(t)
	_ = v.WriteString("/work/a.txt", "alpha\nbeta\ngamma")
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "alpha.*gamma", "multiline": true}).([]string)
	if !equalStr(out, []string{"/work/a.txt"}) {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestGrep_HeadLimit(t *testing.T) {
	reg, v, _ := newRig(t)
	for i := 0; i < 5; i++ {
		_ = v.WriteString(fmt.Sprintf("/work/f%d.txt", i), "match")
	}
	out := mustExec(t, reg, "Grep", map[string]any{"pattern": "match", "head_limit": 2}).([]string)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %v", out)
	}
}

func TestGrep_UnknownType(t *testing.T) {
	reg, _, _ := newRig(t)
	msg := mustExecErr(t, reg, "Grep", map[string]any{"pattern": "x", "type": "bogus"})
	if !strings.Contains(msg, "Unknown file type") {
		t.Fatalf("expected unknown-type error, got %q", msg)
	}
}

// ---------- helpers ----------

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
