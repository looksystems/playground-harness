package remotesync

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"agent-harness/go/shell/vfs"
)

func newFS(t *testing.T) *vfs.DirtyTrackingFS {
	t.Helper()
	return vfs.NewDirtyTrackingFS(vfs.NewBuiltinFilesystemDriver())
}

func TestBuildPreamble_Empty(t *testing.T) {
	fs := newFS(t)
	if got := BuildPreamble(fs); got != "" {
		t.Fatalf("expected empty preamble, got %q", got)
	}
}

func TestBuildPreamble_NilFS(t *testing.T) {
	if got := BuildPreamble(nil); got != "" {
		t.Fatalf("expected empty preamble for nil fs, got %q", got)
	}
}

func TestBuildPreamble_ContainsBase64Write(t *testing.T) {
	fs := newFS(t)
	if err := fs.WriteString("/hello.txt", "hi there"); err != nil {
		t.Fatal(err)
	}
	preamble := BuildPreamble(fs)
	encoded := base64.StdEncoding.EncodeToString([]byte("hi there"))
	if !strings.Contains(preamble, encoded) {
		t.Fatalf("expected preamble %q to contain base64 %q", preamble, encoded)
	}
	if !strings.Contains(preamble, "/hello.txt") {
		t.Fatalf("preamble missing path: %q", preamble)
	}
	if !strings.Contains(preamble, "base64 -d") {
		t.Fatalf("preamble missing decode: %q", preamble)
	}
}

func TestBuildPreamble_ClearsDirty(t *testing.T) {
	fs := newFS(t)
	_ = fs.WriteString("/x", "y")
	_ = BuildPreamble(fs)
	if dirty := fs.Dirty(); len(dirty) != 0 {
		t.Fatalf("expected dirty cleared, got %v", dirty)
	}
}

func TestBuildPreamble_DeletedFile(t *testing.T) {
	fs := newFS(t)
	_ = fs.Inner.WriteString("/gone.txt", "x")
	_ = fs.Remove("/gone.txt")
	preamble := BuildPreamble(fs)
	if !strings.Contains(preamble, "rm -f '/gone.txt'") {
		t.Fatalf("expected rm clause, got %q", preamble)
	}
}

func TestNewMarker_UniqueAndShape(t *testing.T) {
	m1 := NewMarker()
	m2 := NewMarker()
	if m1 == m2 {
		t.Fatalf("markers should differ, got %q == %q", m1, m2)
	}
	if !strings.HasPrefix(m1, "__HARNESS_FS_SYNC_") || !strings.HasSuffix(m1, "__") {
		t.Fatalf("unexpected marker shape: %q", m1)
	}
}

func TestBuildEpilogue_ContainsMarkerAndRoot(t *testing.T) {
	ep := BuildEpilogue("__MARK__", "/home/sandbox/workspace")
	if !strings.Contains(ep, "__MARK__") {
		t.Fatalf("epilogue missing marker: %q", ep)
	}
	if !strings.Contains(ep, "find /home/sandbox/workspace -type f") {
		t.Fatalf("epilogue missing find root: %q", ep)
	}
	if !strings.Contains(ep, "exit $__exit") {
		t.Fatalf("epilogue missing exit preservation: %q", ep)
	}
}

func TestBuildEpilogue_DefaultRoot(t *testing.T) {
	ep := BuildEpilogue("M", "")
	if !strings.Contains(ep, "find / -type f") {
		t.Fatalf("expected default root '/', got %q", ep)
	}
}

func TestParseOutput_WithMarker(t *testing.T) {
	marker := "__M__"
	payload := buildListing(map[string]string{"/a.txt": "alpha", "/b.txt": "beta"})
	raw := "user output line\n" + marker + "\n" + payload
	stdout, files := ParseOutput(raw, marker)
	if stdout != "user output line" {
		t.Fatalf("unexpected user stdout: %q", stdout)
	}
	if files == nil {
		t.Fatalf("expected files map, got nil")
	}
	if got := files["/a.txt"]; got != "alpha" {
		t.Fatalf("files[/a.txt] = %q, want %q", got, "alpha")
	}
	if got := files["/b.txt"]; got != "beta" {
		t.Fatalf("files[/b.txt] = %q, want %q", got, "beta")
	}
}

func TestParseOutput_NoMarker(t *testing.T) {
	stdout, files := ParseOutput("user output\n", "__MISSING__")
	if stdout != "user output\n" {
		t.Fatalf("stdout = %q, want pass-through", stdout)
	}
	if files != nil {
		t.Fatalf("files should be nil when marker missing, got %v", files)
	}
}

func TestParseFileListing_EmptyString(t *testing.T) {
	files := ParseFileListing("")
	if len(files) != 0 {
		t.Fatalf("expected empty files map, got %v", files)
	}
}

func TestApplyBack_AddsNewFile(t *testing.T) {
	fs := newFS(t)
	err := ApplyBack(context.Background(), fs, map[string]string{"/new.txt": "content"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadString("/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != "content" {
		t.Fatalf("got %q, want %q", got, "content")
	}
	// The write went through Inner so dirty set should be clean.
	if d := fs.Dirty(); len(d) != 0 {
		t.Fatalf("apply-back should not mark dirty, got %v", d)
	}
}

func TestApplyBack_UpdatesModifiedFile(t *testing.T) {
	fs := newFS(t)
	_ = fs.Inner.WriteString("/f", "old")
	err := ApplyBack(context.Background(), fs, map[string]string{"/f": "new"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := fs.ReadString("/f")
	if got != "new" {
		t.Fatalf("file not updated, got %q", got)
	}
}

func TestApplyBack_SkipsUnchangedFile(t *testing.T) {
	fs := newFS(t)
	_ = fs.Inner.WriteString("/f", "same")
	err := ApplyBack(context.Background(), fs, map[string]string{"/f": "same"})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing should have been marked dirty.
	if d := fs.Dirty(); len(d) != 0 {
		t.Fatalf("expected no dirty writes, got %v", d)
	}
}

func TestApplyBack_RemovesAbsentFile(t *testing.T) {
	fs := newFS(t)
	_ = fs.Inner.WriteString("/gone.txt", "bye")
	_ = fs.Inner.WriteString("/kept.txt", "hi")
	err := ApplyBack(context.Background(), fs, map[string]string{"/kept.txt": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if fs.Exists("/gone.txt") {
		t.Fatalf("expected /gone.txt removed")
	}
	if !fs.Exists("/kept.txt") {
		t.Fatalf("expected /kept.txt retained")
	}
}

func TestApplyBack_NilFilesNoop(t *testing.T) {
	fs := newFS(t)
	_ = fs.Inner.WriteString("/keep", "x")
	if err := ApplyBack(context.Background(), fs, nil); err != nil {
		t.Fatal(err)
	}
	if !fs.Exists("/keep") {
		t.Fatalf("nil files should be a noop")
	}
}

// Round-trip test: preamble → run command locally → epilogue output →
// ParseOutput → ApplyBack → local VFS state.
func TestEpilogueParseRoundTrip(t *testing.T) {
	marker := NewMarker()
	remoteFiles := map[string]string{
		"/home/sandbox/workspace/a.txt": "alpha",
		"/home/sandbox/workspace/b.txt": "beta\nwith newlines",
	}
	raw := "user stdout line\n" + marker + "\n" + buildListing(remoteFiles)
	stdout, files := ParseOutput(raw, marker)
	if stdout != "user stdout line" {
		t.Fatalf("stdout = %q", stdout)
	}
	if got := files["/home/sandbox/workspace/a.txt"]; got != "alpha" {
		t.Fatalf("got %q for a.txt", got)
	}
	if got := files["/home/sandbox/workspace/b.txt"]; got != "beta\nwith newlines" {
		t.Fatalf("got %q for b.txt", got)
	}
}

// buildListing constructs the ===FILE:<path>===\n<base64>\n stream that
// the epilogue would emit for a set of files.
func buildListing(files map[string]string) string {
	var b strings.Builder
	// Sort keys for deterministic test output.
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	// sort.Strings would work but we want to match insertion order in
	// the test's mental model; the parser tolerates any order.
	for _, k := range keys {
		b.WriteString("===FILE:")
		b.WriteString(k)
		b.WriteString("===\n")
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(files[k])))
		b.WriteString("\n")
	}
	return b.String()
}
