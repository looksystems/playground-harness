package vfs

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinFilesystemDriverDelegates(t *testing.T) {
	d := NewBuiltinFilesystemDriver()

	// Write / Read
	require.NoError(t, d.Write("/hello.txt", []byte("hi")))
	data, err := d.Read("/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hi"), data)

	// WriteString / ReadString
	require.NoError(t, d.WriteString("/str.txt", "string content"))
	s, err := d.ReadString("/str.txt")
	require.NoError(t, err)
	assert.Equal(t, "string content", s)

	// WriteLazy
	require.NoError(t, d.WriteLazy("/lazy.txt", func() ([]byte, error) {
		return []byte("lazy"), nil
	}))
	assert.True(t, d.Exists("/lazy.txt"))
	data, err = d.Read("/lazy.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("lazy"), data)

	// Exists
	assert.True(t, d.Exists("/hello.txt"))
	assert.False(t, d.Exists("/nope.txt"))

	// Remove
	require.NoError(t, d.Remove("/hello.txt"))
	assert.False(t, d.Exists("/hello.txt"))

	// IsDir
	require.NoError(t, d.Write("/sub/file.txt", []byte("")))
	assert.True(t, d.IsDir("/sub"))

	// Listdir
	entries, err := d.Listdir("/")
	require.NoError(t, err)
	assert.Contains(t, entries, "sub")

	// Find
	results, err := d.Find("/", "*.txt")
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	// Stat
	require.NoError(t, d.Write("/stat.txt", []byte("123")))
	info, err := d.Stat("/stat.txt")
	require.NoError(t, err)
	assert.Equal(t, "file", info.Type)
	assert.Equal(t, int64(3), info.Size)

	// Clone returns a FilesystemDriver
	clone := d.Clone()
	require.NotNil(t, clone)
	// Clone is independent
	require.NoError(t, clone.Write("/extra.txt", []byte("extra")))
	assert.False(t, d.Exists("/extra.txt"))
}

func TestBuiltinFilesystemDriverReadMissing(t *testing.T) {
	d := NewBuiltinFilesystemDriver()
	_, err := d.Read("/missing.txt")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestBuiltinFilesystemDriverImplementsInterface(t *testing.T) {
	// Compile-time assertion via interface assignment.
	var _ FilesystemDriver = NewBuiltinFilesystemDriver()
}
