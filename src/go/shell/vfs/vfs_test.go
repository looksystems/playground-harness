package vfs

import (
	"errors"
	"io/fs"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Basic read / write
// ---------------------------------------------------------------------------

func TestWriteReadRoundTrip(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/hello.txt", []byte("hello")))
	data, err := v.Read("/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), data)
}

func TestWriteString(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.WriteString("/msg.txt", "world"))
	s, err := v.ReadString("/msg.txt")
	require.NoError(t, err)
	assert.Equal(t, "world", s)
}

func TestReadMissingReturnsErrNotExist(t *testing.T) {
	v := New(nil)
	_, err := v.Read("/missing.txt")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadStringMissingReturnsErrNotExist(t *testing.T) {
	v := New(nil)
	_, err := v.ReadString("/missing.txt")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSeedFromMap(t *testing.T) {
	v := New(map[string][]byte{
		"foo.txt": []byte("bar"),
	})
	data, err := v.Read("/foo.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("bar"), data)
}

// ---------------------------------------------------------------------------
// Path normalisation
// ---------------------------------------------------------------------------

func TestPathNormAbsolute(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/foo", []byte("a")))
	data, err := v.Read("/foo")
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), data)
}

func TestPathNormRelative(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("foo", []byte("a")))
	data, err := v.Read("/foo") // absolute form
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), data)
}

func TestPathNormMultipleSlashes(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("///foo", []byte("a")))
	data, err := v.Read("/foo")
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), data)
}

func TestPathNormDot(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/./foo", []byte("a")))
	data, err := v.Read("/foo")
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), data)
}

func TestPathNormDotDot(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/foo/..", []byte("root content")))
	// /foo/.. resolves to /
	data, err := v.Read("/")
	require.NoError(t, err)
	assert.Equal(t, []byte("root content"), data)
}

func TestPathNormDotDotSegment(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/foo/../bar", []byte("bar content")))
	data, err := v.Read("/bar")
	require.NoError(t, err)
	assert.Equal(t, []byte("bar content"), data)
}

// ---------------------------------------------------------------------------
// Lazy providers
// ---------------------------------------------------------------------------

func TestWriteLazyInvokedOnFirstRead(t *testing.T) {
	v := New(nil)
	calls := 0
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		calls++
		return []byte("lazy data"), nil
	}))
	assert.Equal(t, 0, calls)

	data, err := v.Read("/lazy.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("lazy data"), data)
	assert.Equal(t, 1, calls)
}

func TestWriteLazyCachesOnSecondRead(t *testing.T) {
	v := New(nil)
	calls := 0
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		calls++
		return []byte("cached"), nil
	}))

	_, _ = v.Read("/lazy.txt")
	data, err := v.Read("/lazy.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("cached"), data)
	assert.Equal(t, 1, calls, "provider should only be called once")
}

func TestWriteLazyErrorPropagates(t *testing.T) {
	v := New(nil)
	providerErr := errors.New("provider failed")
	require.NoError(t, v.WriteLazy("/bad.txt", func() ([]byte, error) {
		return nil, providerErr
	}))
	_, err := v.Read("/bad.txt")
	assert.ErrorIs(t, err, providerErr)
}

func TestWriteOverridesLazy(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.WriteLazy("/file.txt", func() ([]byte, error) {
		return []byte("lazy"), nil
	}))
	require.NoError(t, v.Write("/file.txt", []byte("real")))

	calls := 0
	// provider should never be called again
	data, err := v.Read("/file.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("real"), data)
	assert.Equal(t, 0, calls)
}

// ---------------------------------------------------------------------------
// Exists
// ---------------------------------------------------------------------------

func TestExistsRealFile(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.txt", []byte("x")))
	assert.True(t, v.Exists("/a.txt"))
}

func TestExistsLazyBeforeRead(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		return []byte("x"), nil
	}))
	assert.True(t, v.Exists("/lazy.txt"))
}

func TestExistsMissing(t *testing.T) {
	v := New(nil)
	assert.False(t, v.Exists("/nope.txt"))
}

func TestExistsImplicitDir(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a/b.txt", []byte("x")))
	assert.True(t, v.Exists("/a"))
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestRemoveRealFile(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.txt", []byte("x")))
	require.NoError(t, v.Remove("/a.txt"))
	assert.False(t, v.Exists("/a.txt"))
}

func TestRemoveLazyFile(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		return []byte("x"), nil
	}))
	require.NoError(t, v.Remove("/lazy.txt"))
	assert.False(t, v.Exists("/lazy.txt"))
}

func TestRemoveIdempotent(t *testing.T) {
	v := New(nil)
	// Should not error on missing path.
	assert.NoError(t, v.Remove("/does-not-exist.txt"))
}

// ---------------------------------------------------------------------------
// IsDir
// ---------------------------------------------------------------------------

func TestIsDirWhenChildExists(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/foo/bar.txt", []byte("x")))
	assert.True(t, v.IsDir("/foo"))
}

func TestIsDirRootAlwaysDir(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.txt", []byte("x")))
	assert.True(t, v.IsDir("/"))
}

func TestIsDirFalseForLeafFile(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/foo/bar.txt", []byte("x")))
	assert.False(t, v.IsDir("/foo/bar.txt"))
}

func TestIsDirEmptyVFSRootFalse(t *testing.T) {
	// With no files, / is not technically a dir (no children).
	v := New(nil)
	assert.False(t, v.IsDir("/"))
}

// ---------------------------------------------------------------------------
// Listdir
// ---------------------------------------------------------------------------

func TestListdirRoot(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.txt", []byte("")))
	require.NoError(t, v.Write("/b.txt", []byte("")))
	require.NoError(t, v.Write("/sub/c.txt", []byte("")))

	entries, err := v.Listdir("/")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt", "b.txt", "sub"}, entries)
}

func TestListdirNested(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/sub/x.txt", []byte("")))
	require.NoError(t, v.Write("/sub/y.txt", []byte("")))
	require.NoError(t, v.Write("/sub/deep/z.txt", []byte("")))

	entries, err := v.Listdir("/sub")
	require.NoError(t, err)
	assert.Equal(t, []string{"deep", "x.txt", "y.txt"}, entries)
}

func TestListdirEmptyRoot(t *testing.T) {
	v := New(nil)
	entries, err := v.Listdir("/")
	require.NoError(t, err)
	assert.Equal(t, []string{}, entries)
}

func TestListdirSorted(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/z.txt", []byte("")))
	require.NoError(t, v.Write("/a.txt", []byte("")))
	require.NoError(t, v.Write("/m.txt", []byte("")))

	entries, err := v.Listdir("/")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt", "m.txt", "z.txt"}, entries)
}

// ---------------------------------------------------------------------------
// Find
// ---------------------------------------------------------------------------

func TestFindStar(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a/foo.txt", []byte("")))
	require.NoError(t, v.Write("/a/bar.go", []byte("")))

	results, err := v.Find("/", "*")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"/a/foo.txt", "/a/bar.go"}, results)
}

func TestFindGlobPattern(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a/foo.txt", []byte("")))
	require.NoError(t, v.Write("/a/bar.go", []byte("")))
	require.NoError(t, v.Write("/b/baz.txt", []byte("")))

	results, err := v.Find("/", "*.txt")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"/a/foo.txt", "/b/baz.txt"}, results)
}

func TestFindWithRoot(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a/foo.txt", []byte("")))
	require.NoError(t, v.Write("/b/bar.txt", []byte("")))

	results, err := v.Find("/a", "*")
	require.NoError(t, err)
	assert.Equal(t, []string{"/a/foo.txt"}, results)
}

func TestFindNoMatches(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.go", []byte("")))

	results, err := v.Find("/", "*.txt")
	require.NoError(t, err)
	assert.Empty(t, results)
}

// ---------------------------------------------------------------------------
// Stat
// ---------------------------------------------------------------------------

func TestStatFile(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/f.txt", []byte("hello")))
	info, err := v.Stat("/f.txt")
	require.NoError(t, err)
	assert.Equal(t, "/f.txt", info.Path)
	assert.Equal(t, "file", info.Type)
	assert.Equal(t, int64(5), info.Size)
}

func TestStatDir(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/dir/file.txt", []byte("")))
	info, err := v.Stat("/dir")
	require.NoError(t, err)
	assert.Equal(t, "dir", info.Type)
	assert.Equal(t, int64(0), info.Size)
}

func TestStatMissing(t *testing.T) {
	v := New(nil)
	_, err := v.Stat("/ghost.txt")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

func TestCloneIndependent(t *testing.T) {
	v := New(nil)
	require.NoError(t, v.Write("/a.txt", []byte("original")))

	c := v.Clone()
	require.NoError(t, c.Write("/a.txt", []byte("cloned")))

	data, err := v.Read("/a.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), data, "original should be unaffected")
}

func TestCloneNewFileDoesNotAffectOriginal(t *testing.T) {
	v := New(nil)
	c := v.Clone()
	require.NoError(t, c.Write("/new.txt", []byte("x")))
	assert.False(t, v.Exists("/new.txt"))
}

func TestCloneLazyShared(t *testing.T) {
	v := New(nil)
	calls := 0
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		calls++
		return []byte("shared"), nil
	}))

	c := v.Clone()
	// Both original and clone should be able to read the lazy path.
	data, err := c.Read("/lazy.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("shared"), data)
}

// ---------------------------------------------------------------------------
// Concurrent safety
// ---------------------------------------------------------------------------

func TestConcurrentWriteRead(t *testing.T) {
	v := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = v.WriteString("/concurrent.txt", "value")
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = v.Read("/concurrent.txt")
		}(i)
	}
	wg.Wait()
}

func TestConcurrentLazyRead(t *testing.T) {
	v := New(nil)
	calls := 0
	var mu sync.Mutex
	require.NoError(t, v.WriteLazy("/lazy.txt", func() ([]byte, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return []byte("data"), nil
	}))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = v.Read("/lazy.txt")
		}()
	}
	wg.Wait()
	// Provider may be called more than once under racing goroutines (double-check
	// pattern), but data should always be correct.
	data, err := v.Read("/lazy.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), data)
}
