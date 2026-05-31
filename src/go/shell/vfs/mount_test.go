package vfs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource is an in-memory read-only MountSource for wrapper unit tests.
type fakeSource struct {
	files map[string][]byte
}

func newFakeSource(files map[string]string) *fakeSource {
	m := make(map[string][]byte, len(files))
	for k, v := range files {
		m[k] = []byte(v)
	}
	return &fakeSource{files: m}
}

func (s *fakeSource) dirs() map[string]struct{} {
	dirs := map[string]struct{}{"": {}}
	for p := range s.files {
		segs := strings.Split(p, "/")
		for i := 1; i < len(segs); i++ {
			dirs[strings.Join(segs[:i], "/")] = struct{}{}
		}
	}
	return dirs
}

func (s *fakeSource) Stat(subpath string) (MountStat, bool) {
	subpath = trimSlashes(subpath)
	if c, ok := s.files[subpath]; ok {
		return MountStat{IsDir: false, Size: int64(len(c))}, true
	}
	if _, ok := s.dirs()[subpath]; ok {
		return MountStat{IsDir: true}, true
	}
	return MountStat{}, false
}

func (s *fakeSource) List(subpath string) ([]string, error) {
	subpath = trimSlashes(subpath)
	prefix := ""
	if subpath != "" {
		prefix = subpath + "/"
	}
	set := map[string]struct{}{}
	for p := range s.files {
		if len(p) > len(prefix) && p[:len(prefix)] == prefix {
			rest := p[len(prefix):]
			set[strings.SplitN(rest, "/", 2)[0]] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (s *fakeSource) Read(subpath string) ([]byte, error) {
	return s.files[trimSlashes(subpath)], nil
}

func trimSlashes(p string) string {
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	for len(p) > 0 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	return p
}

func newMounting() *MountingFilesystemDriver {
	return NewMountingFilesystemDriver(NewBuiltinFilesystemDriver())
}

func TestMountEmptyTablePassthrough(t *testing.T) {
	inner := NewBuiltinFilesystemDriver()
	require.NoError(t, inner.WriteString("/a.txt", "hi"))
	fs := NewMountingFilesystemDriver(inner)
	got, err := fs.ReadString("/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "hi", got)
	assert.Empty(t, fs.Mounts())
}

func TestMountLongestPrefixRouting(t *testing.T) {
	fs := newMounting()
	fs.Mount("/a", newFakeSource(map[string]string{"x.txt": "outer"}))
	fs.Mount("/a/b", newFakeSource(map[string]string{"y.txt": "inner"}))
	got, err := fs.ReadString("/a/b/y.txt")
	require.NoError(t, err)
	assert.Equal(t, "inner", got)
	got, err = fs.ReadString("/a/x.txt")
	require.NoError(t, err)
	assert.Equal(t, "outer", got)
}

func TestMountReadFromSource(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "world"}))
	got, err := fs.ReadString("/work/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "world", got)
}

func TestMountExistsMountPointAndAncestor(t *testing.T) {
	fs := newMounting()
	fs.Mount("/deep/mnt", newFakeSource(map[string]string{"f.txt": "x"}))
	assert.True(t, fs.Exists("/deep/mnt"))
	assert.True(t, fs.Exists("/deep"))
	assert.True(t, fs.Exists("/deep/mnt/f.txt"))
	assert.False(t, fs.Exists("/deep/mnt/nope.txt"))
	assert.True(t, fs.IsDir("/deep"))
	assert.True(t, fs.IsDir("/deep/mnt"))
	assert.False(t, fs.IsDir("/deep/mnt/f.txt"))
}

func TestMountListdirMergeDedupSorted(t *testing.T) {
	inner := NewBuiltinFilesystemDriver()
	require.NoError(t, inner.WriteString("/work/local.txt", "L"))
	require.NoError(t, inner.WriteString("/work/shared.txt", "edited"))
	fs := NewMountingFilesystemDriver(inner)
	fs.Mount("/work", newFakeSource(map[string]string{"shared.txt": "orig", "sub/deep.txt": "d"}))
	got, err := fs.Listdir("/work")
	require.NoError(t, err)
	assert.Equal(t, []string{"local.txt", "shared.txt", "sub"}, got)
}

func TestMountListdirRootShowsMountPoint(t *testing.T) {
	fs := newMounting()
	fs.Mount("/repo", newFakeSource(map[string]string{"a.txt": "x"}))
	got, err := fs.Listdir("/")
	require.NoError(t, err)
	assert.Contains(t, got, "repo")
}

func TestMountCopyUpShadowing(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "original"}))
	got, _ := fs.ReadString("/work/hello.txt")
	assert.Equal(t, "original", got)
	require.NoError(t, fs.WriteString("/work/hello.txt", "edited"))
	got, _ = fs.ReadString("/work/hello.txt")
	assert.Equal(t, "edited", got)
	names, _ := fs.Listdir("/work")
	count := 0
	for _, n := range names {
		if n == "hello.txt" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestMountRemoveCopiedUpUnshadows(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "original"}))
	require.NoError(t, fs.WriteString("/work/hello.txt", "edited"))
	require.NoError(t, fs.Remove("/work/hello.txt"))
	got, err := fs.ReadString("/work/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", got)
}

func TestMountRemoveSourceOnlyRaises(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "original"}))
	err := fs.Remove("/work/hello.txt")
	assert.Error(t, err)
	// source still visible
	got, _ := fs.ReadString("/work/hello.txt")
	assert.Equal(t, "original", got)
}

func TestMountFindMerged(t *testing.T) {
	inner := NewBuiltinFilesystemDriver()
	require.NoError(t, inner.WriteString("/work/local.md", "L"))
	fs := NewMountingFilesystemDriver(inner)
	fs.Mount("/work", newFakeSource(map[string]string{"a.md": "x", "sub/b.md": "y", "c.txt": "z"}))
	got, err := fs.Find("/work", "*.md")
	require.NoError(t, err)
	sort.Strings(got)
	assert.Equal(t, []string{"/work/a.md", "/work/local.md", "/work/sub/b.md"}, got)
}

func TestMountStatSourceFile(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "world"}))
	fi, err := fs.Stat("/work/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "file", fi.Type)
	assert.Equal(t, int64(5), fi.Size)
	d, err := fs.Stat("/work")
	require.NoError(t, err)
	assert.Equal(t, "dir", d.Type)
}

func TestMountCloneWritableDivergesSourceShared(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"hello.txt": "original"}))
	require.NoError(t, fs.WriteString("/work/local.txt", "L"))
	clone := fs.Clone().(*MountingFilesystemDriver)
	require.NoError(t, clone.WriteString("/work/local.txt", "changed"))
	got, _ := fs.ReadString("/work/local.txt")
	assert.Equal(t, "L", got)
	got, _ = clone.ReadString("/work/local.txt")
	assert.Equal(t, "changed", got)
	assert.Equal(t, fs.Mounts(), clone.Mounts())
	got, _ = clone.ReadString("/work/hello.txt")
	assert.Equal(t, "original", got)
}

func TestMountUnmount(t *testing.T) {
	fs := newMounting()
	fs.Mount("/work", newFakeSource(map[string]string{"a.txt": "x"}))
	assert.Equal(t, []string{"/work"}, fs.Mounts())
	fs.Unmount("/work")
	assert.Empty(t, fs.Mounts())
	assert.False(t, fs.Exists("/work/a.txt"))
}

func TestMountIsMountable(t *testing.T) {
	var _ Mountable = newMounting()
}

// ---------------------------------------------------------------------------
// LocalFolderSource vs a real temp dir
// ---------------------------------------------------------------------------

func TestLocalFolderSourceNestedFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("beta"), 0o644))

	src := NewLocalFolderSource(root)
	st, ok := src.Stat("")
	require.True(t, ok)
	assert.True(t, st.IsDir)
	st, ok = src.Stat("a.txt")
	require.True(t, ok)
	assert.False(t, st.IsDir)
	assert.Equal(t, int64(5), st.Size)

	names, err := src.List("")
	require.NoError(t, err)
	sort.Strings(names)
	assert.Equal(t, []string{"a.txt", "sub"}, names)
	names, err = src.List("sub")
	require.NoError(t, err)
	assert.Equal(t, []string{"b.txt"}, names)

	data, err := src.Read("a.txt")
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(data))
	data, err = src.Read("sub/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "beta", string(data))

	_, ok = src.Stat("nope.txt")
	assert.False(t, ok)
}

func TestLocalFolderSourceParentTraversalCannotEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "in.txt"), []byte("inside"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "secret.txt"), []byte("secret"), 0o644))

	src := NewLocalFolderSource(root)
	_, ok := src.Stat("../secret.txt")
	assert.False(t, ok)
}

func TestLocalFolderSourceSymlinkCannotEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	require.NoError(t, os.Mkdir(root, 0o755))
	outside := filepath.Join(base, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unsupported")
	}

	src := NewLocalFolderSource(root)
	_, ok := src.Stat("link.txt")
	assert.False(t, ok)
}
