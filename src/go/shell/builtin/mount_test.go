package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/shell/builtin"
	"agent-harness/go/shell/vfs"
)

// End-to-end: mounting a host folder is visible to the shell builtins and the
// re-seated FS() handle, and editing a mounted file copies up without touching
// the host file.

func TestMount_ShellSeesContent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hello.txt"), []byte("from-host"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.md"), []byte("# notes"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "deep.txt"), []byte("deep-content"), 0o644))

	d := builtin.NewBuiltinShellDriver()
	d.Mount("/work", vfs.NewLocalFolderSource(root))

	ctx := context.Background()
	r, err := d.Exec(ctx, "ls /work")
	require.NoError(t, err)
	assert.Contains(t, r.Stdout, "hello.txt")

	r, err = d.Exec(ctx, "cat /work/hello.txt")
	require.NoError(t, err)
	assert.Contains(t, r.Stdout, "from-host")

	r, err = d.Exec(ctx, "find /work -name '*.md'")
	require.NoError(t, err)
	assert.Contains(t, r.Stdout, "notes.md")

	r, err = d.Exec(ctx, "grep -r deep /work")
	require.NoError(t, err)
	assert.Contains(t, r.Stdout, "deep-content")

	// FS() was re-seated to the mount-aware driver.
	got, err := d.FS().ReadString("/work/sub/deep.txt")
	require.NoError(t, err)
	assert.Equal(t, "deep-content", got)
}

func TestMount_EditCopyUpHostUnchanged(t *testing.T) {
	root := t.TempDir()
	hostFile := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(hostFile, []byte("from-host"), 0o644))

	d := builtin.NewBuiltinShellDriver()
	d.Mount("/work", vfs.NewLocalFolderSource(root))

	ctx := context.Background()
	_, err := d.Exec(ctx, "echo edited > /work/hello.txt")
	require.NoError(t, err)

	r, err := d.Exec(ctx, "cat /work/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "edited", strings.TrimSpace(r.Stdout))

	// Host file is untouched (read-only source + copy-up).
	data, err := os.ReadFile(hostFile)
	require.NoError(t, err)
	assert.Equal(t, "from-host", string(data))
}

func TestMount_MountsListing(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	assert.Empty(t, d.Mounts())
	d.Mount("/work", vfs.NewLocalFolderSource(t.TempDir()))
	assert.Equal(t, []string{"/work"}, d.Mounts())
	d.Unmount("/work")
	assert.Empty(t, d.Mounts())
}
