package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/agent"
	"agent-harness/go/filetools"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/shell/vfs"
	"agent-harness/go/tools"
)

// End-to-end: Agent.Mount (promoted from the embedded shell.Host) exposes a
// host folder to both the file tools and the shell, with copy-up on edit.

func TestAgentMount_VisibleToToolsAndShell(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hello.txt"), []byte("from-host"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.md"), []byte("# notes"), 0o644))

	d := builtin.NewBuiltinShellDriver()
	a := agent.NewAgentWithShell("m", &scriptedClient{}, d)
	require.NoError(t, a.Mount("/work", vfs.NewLocalFolderSource(root)))

	// Shell sees content.
	r, err := a.Exec(context.Background(), "cat /work/hello.txt")
	require.NoError(t, err)
	assert.Contains(t, r.Stdout, "from-host")

	// File tools, wired from the re-seated FS(), see the same content.
	reg := tools.New()
	filetools.RegisterAll(reg, a.Host.Driver.FS(), func() string { return "/" })
	out := mustExecMount(t, reg, "Read", map[string]any{"file_path": "/work/notes.md"})
	assert.Contains(t, fmt.Sprintf("%v", out), "# notes")

	globOut := mustExecMount(t, reg, "Glob", map[string]any{"pattern": "**/*.txt", "path": "/work"})
	assert.Contains(t, fmt.Sprintf("%v", globOut), "/work/hello.txt")
}

func TestAgentMount_EditCopyUpHostUnchanged(t *testing.T) {
	root := t.TempDir()
	hostFile := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(hostFile, []byte("from-host"), 0o644))

	d := builtin.NewBuiltinShellDriver()
	a := agent.NewAgentWithShell("m", &scriptedClient{}, d)
	require.NoError(t, a.Mount("/work", vfs.NewLocalFolderSource(root)))

	_, err := a.Exec(context.Background(), "echo edited > /work/hello.txt")
	require.NoError(t, err)
	r, err := a.Exec(context.Background(), "cat /work/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "edited", strings.TrimSpace(r.Stdout))

	data, err := os.ReadFile(hostFile)
	require.NoError(t, err)
	assert.Equal(t, "from-host", string(data))

	assert.Equal(t, []string{"/work"}, a.Mounts())
	require.NoError(t, a.Unmount("/work"))
	assert.Empty(t, a.Mounts())
}

func mustExecMount(t *testing.T, reg *tools.Registry, name string, args map[string]any) any {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := reg.Execute(context.Background(), name, raw)
	require.NoError(t, err)
	return out
}
