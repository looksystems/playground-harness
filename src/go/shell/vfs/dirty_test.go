package vfs

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDirtyFS() *DirtyTrackingFS {
	return NewDirtyTrackingFS(NewBuiltinFilesystemDriver())
}

// ---------------------------------------------------------------------------
// Dirty tracking behaviour
// ---------------------------------------------------------------------------

func TestDirtyTrackingWriteMarksDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/a.txt", []byte("x")))
	assert.Contains(t, d.Dirty(), "/a.txt")
}

func TestDirtyTrackingWriteStringMarksDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.WriteString("/b.txt", "hello"))
	assert.Contains(t, d.Dirty(), "/b.txt")
}

func TestDirtyTrackingWriteLazyMarksDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.WriteLazy("/lazy.txt", func() ([]byte, error) {
		return []byte("x"), nil
	}))
	assert.Contains(t, d.Dirty(), "/lazy.txt")
}

func TestDirtyTrackingRemoveMarksDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/c.txt", []byte("x")))
	d.ClearDirty()
	require.NoError(t, d.Remove("/c.txt"))
	assert.Contains(t, d.Dirty(), "/c.txt")
}

func TestDirtyTrackingReadDoesNotMarkDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/r.txt", []byte("x")))
	d.ClearDirty()
	_, err := d.Read("/r.txt")
	require.NoError(t, err)
	assert.Empty(t, d.Dirty())
}

func TestDirtyTrackingReadStringDoesNotMarkDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.WriteString("/rs.txt", "x"))
	d.ClearDirty()
	_, err := d.ReadString("/rs.txt")
	require.NoError(t, err)
	assert.Empty(t, d.Dirty())
}

func TestDirtyTrackingClearDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/x.txt", []byte("x")))
	assert.NotEmpty(t, d.Dirty())
	d.ClearDirty()
	assert.Empty(t, d.Dirty())
}

func TestDirtyReturnsSortedCopy(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/z.txt", []byte("")))
	require.NoError(t, d.Write("/a.txt", []byte("")))
	require.NoError(t, d.Write("/m.txt", []byte("")))

	dirty := d.Dirty()
	assert.Equal(t, []string{"/a.txt", "/m.txt", "/z.txt"}, dirty)
}

// ---------------------------------------------------------------------------
// Pass-through delegation
// ---------------------------------------------------------------------------

func TestDirtyTrackingPassThroughExists(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/e.txt", []byte("x")))
	assert.True(t, d.Exists("/e.txt"))
	assert.False(t, d.Exists("/no.txt"))
}

func TestDirtyTrackingPassThroughIsDir(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/dir/file.txt", []byte("")))
	assert.True(t, d.IsDir("/dir"))
}

func TestDirtyTrackingPassThroughListdir(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/sub/f.txt", []byte("")))
	entries, err := d.Listdir("/sub")
	require.NoError(t, err)
	assert.Equal(t, []string{"f.txt"}, entries)
}

func TestDirtyTrackingPassThroughFind(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/foo.txt", []byte("")))
	results, err := d.Find("/", "*.txt")
	require.NoError(t, err)
	assert.Equal(t, []string{"/foo.txt"}, results)
}

func TestDirtyTrackingPassThroughStat(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/s.txt", []byte("abc")))
	info, err := d.Stat("/s.txt")
	require.NoError(t, err)
	assert.Equal(t, "file", info.Type)
	assert.Equal(t, int64(3), info.Size)
}

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

func TestDirtyTrackingCloneResetsDirty(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/a.txt", []byte("x")))
	assert.NotEmpty(t, d.Dirty())

	cloned := d.Clone().(*DirtyTrackingFS)
	assert.Empty(t, cloned.Dirty(), "clone should have empty dirty set")
}

func TestDirtyTrackingCloneInnerIsIndependent(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/a.txt", []byte("original")))

	cloned := d.Clone().(*DirtyTrackingFS)
	require.NoError(t, cloned.Write("/a.txt", []byte("cloned")))

	data, err := d.ReadString("/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", data)
}

func TestDirtyTrackingImplementsInterface(t *testing.T) {
	var _ FilesystemDriver = newDirtyFS()
}

// ---------------------------------------------------------------------------
// Concurrent safety
// ---------------------------------------------------------------------------

func TestDirtyTrackingConcurrentWrites(t *testing.T) {
	d := newDirtyFS()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = d.WriteString("/concurrent.txt", "value")
		}(i)
	}
	wg.Wait()
	assert.Contains(t, d.Dirty(), "/concurrent.txt")
}

func TestDirtyTrackingConcurrentReadWrite(t *testing.T) {
	d := newDirtyFS()
	require.NoError(t, d.Write("/shared.txt", []byte("init")))
	d.ClearDirty()

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = d.WriteString("/shared.txt", "updated")
		}()
		go func() {
			defer wg.Done()
			_, _ = d.Read("/shared.txt")
		}()
	}
	wg.Wait()
}
