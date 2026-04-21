// Package vfs provides an in-memory virtual filesystem with lazy file support,
// a FilesystemDriver interface, and a dirty-tracking wrapper for remote sync.
package vfs

import (
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// norm normalises a path to absolute, cleaned, forward-slash form.
// It mirrors Python's: os.path.normpath("/" + path), collapsing leading "//".
func norm(p string) string {
	// Trim surrounding whitespace
	p = strings.TrimSpace(p)
	// Prepend "/" so that relative paths become absolute, then clean.
	result := path.Clean("/" + p)
	// path.Clean already handles multiple slashes, ".", ".." — no // prefix possible.
	return result
}

// FileInfo holds stat-style metadata for a VFS entry.
type FileInfo struct {
	Path string
	Type string // "file" | "dir"
	Size int64
}

// VirtualFS is a dict-backed in-memory filesystem.
// Paths are normalised to absolute, forward-slash form.
// All exported methods are concurrent-safe.
type VirtualFS struct {
	mu    sync.RWMutex
	files map[string][]byte
	lazy  map[string]func() ([]byte, error)
}

// New constructs an empty VFS. The optional files map seeds initial content.
func New(files map[string][]byte) *VirtualFS {
	v := &VirtualFS{
		files: make(map[string][]byte),
		lazy:  make(map[string]func() ([]byte, error)),
	}
	for p, content := range files {
		cp := make([]byte, len(content))
		copy(cp, content)
		v.files[norm(p)] = cp
	}
	return v
}

// Write stores content at path (overwriting any existing file or lazy entry).
func (v *VirtualFS) Write(p string, content []byte) error {
	p = norm(p)
	cp := make([]byte, len(content))
	copy(cp, content)
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.lazy, p) // remove lazy if present
	v.files[p] = cp
	return nil
}

// WriteString is a convenience wrapper around Write.
func (v *VirtualFS) WriteString(p string, content string) error {
	return v.Write(p, []byte(content))
}

// WriteLazy registers a lazy provider; the provider is called on first Read
// and the result is cached.
func (v *VirtualFS) WriteLazy(p string, provider func() ([]byte, error)) error {
	p = norm(p)
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.files, p) // remove real file if present
	v.lazy[p] = provider
	return nil
}

// Read returns the content at path. Resolves lazy providers on demand.
// Returns fs.ErrNotExist if the path does not exist.
func (v *VirtualFS) Read(p string) ([]byte, error) {
	p = norm(p)

	// Fast path: check under read lock.
	v.mu.RLock()
	if data, ok := v.files[p]; ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		v.mu.RUnlock()
		return cp, nil
	}
	provider, hasLazy := v.lazy[p]
	v.mu.RUnlock()

	if !hasLazy {
		return nil, fs.ErrNotExist
	}

	// Resolve the lazy provider under write lock.
	v.mu.Lock()
	defer v.mu.Unlock()
	// Re-check after acquiring write lock (another goroutine may have resolved it).
	if data, ok := v.files[p]; ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		return cp, nil
	}
	provider, hasLazy = v.lazy[p]
	if !hasLazy {
		return nil, fs.ErrNotExist
	}
	data, err := provider()
	if err != nil {
		return nil, err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	delete(v.lazy, p)
	v.files[p] = cp
	return cp, nil
}

// ReadString is a convenience wrapper around Read.
func (v *VirtualFS) ReadString(p string) (string, error) {
	data, err := v.Read(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Exists reports whether path exists (real, lazy, or an implicit directory).
func (v *VirtualFS) Exists(p string) bool {
	p = norm(p)
	v.mu.RLock()
	defer v.mu.RUnlock()
	if _, ok := v.files[p]; ok {
		return true
	}
	if _, ok := v.lazy[p]; ok {
		return true
	}
	return v.isDirLocked(p)
}

// Remove deletes the file at path. No-op if missing; returns nil.
func (v *VirtualFS) Remove(p string) error {
	p = norm(p)
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.files, p)
	delete(v.lazy, p)
	return nil
}

// isDirLocked reports whether path is a directory.
// Must be called with at least a read lock held.
func (v *VirtualFS) isDirLocked(p string) bool {
	prefix := strings.TrimRight(p, "/") + "/"
	for k := range v.files {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range v.lazy {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// IsDir reports whether path is a directory. A path is a directory iff some
// other path has path+"/" as its prefix.
func (v *VirtualFS) IsDir(p string) bool {
	p = norm(p)
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.isDirLocked(p)
}

// allPathsLocked returns the union of real and lazy path keys.
// Must be called with at least a read lock held.
func (v *VirtualFS) allPathsLocked() []string {
	seen := make(map[string]struct{}, len(v.files)+len(v.lazy))
	for k := range v.files {
		seen[k] = struct{}{}
	}
	for k := range v.lazy {
		seen[k] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	return result
}

// Listdir returns the immediate children of path (file and directory names,
// no trailing slash on directories). Sorted.
func (v *VirtualFS) Listdir(p string) ([]string, error) {
	p = norm(p)
	// Build the prefix: "path/" but handle root specially.
	prefix := strings.TrimRight(p, "/") + "/"
	if prefix == "//" {
		prefix = "/"
	}

	v.mu.RLock()
	all := v.allPathsLocked()
	v.mu.RUnlock()

	entries := make(map[string]struct{})
	for _, child := range all {
		if !strings.HasPrefix(child, prefix) || child == prefix {
			continue
		}
		rest := child[len(prefix):]
		// The first segment of rest is the immediate child.
		entry := strings.SplitN(rest, "/", 2)[0]
		if entry != "" {
			entries[entry] = struct{}{}
		}
	}

	result := make([]string, 0, len(entries))
	for e := range entries {
		result = append(result, e)
	}
	sort.Strings(result)
	return result, nil
}

// Find returns all file paths under root matching the glob pattern.
// Pattern uses path.Match syntax (?, *, []).  "*" matches all files.
func (v *VirtualFS) Find(root, pattern string) ([]string, error) {
	root = strings.TrimRight(norm(root), "/")

	v.mu.RLock()
	all := v.allPathsLocked()
	v.mu.RUnlock()

	sort.Strings(all)

	var results []string
	for _, p := range all {
		if !strings.HasPrefix(p, root) {
			continue
		}
		// Match against the basename.
		base := path.Base(p)
		matched, err := path.Match(pattern, base)
		if err != nil {
			return nil, err
		}
		if matched {
			results = append(results, p)
		}
	}
	return results, nil
}

// Stat returns FileInfo-style metadata for path.
// Directories return {Type:"dir", Size:0}. Missing paths return an error.
func (v *VirtualFS) Stat(p string) (FileInfo, error) {
	p = norm(p)

	v.mu.RLock()
	isDir := v.isDirLocked(p)
	_, hasFile := v.files[p]
	_, hasLazy := v.lazy[p]
	v.mu.RUnlock()

	if isDir {
		return FileInfo{Path: p, Type: "dir", Size: 0}, nil
	}
	if hasFile || hasLazy {
		// Read to get actual size (also resolves lazy).
		data, err := v.Read(p)
		if err != nil {
			return FileInfo{}, err
		}
		return FileInfo{Path: p, Type: "file", Size: int64(len(data))}, nil
	}
	return FileInfo{}, fs.ErrNotExist
}

// Clone returns an independent deep-copy of the VFS.
// File contents are deep-copied; lazy providers are shared (same as Python).
func (v *VirtualFS) Clone() *VirtualFS {
	v.mu.RLock()
	defer v.mu.RUnlock()

	newFiles := make(map[string][]byte, len(v.files))
	for k, data := range v.files {
		cp := make([]byte, len(data))
		copy(cp, data)
		newFiles[k] = cp
	}
	newLazy := make(map[string]func() ([]byte, error), len(v.lazy))
	for k, fn := range v.lazy {
		newLazy[k] = fn
	}
	return &VirtualFS{
		files: newFiles,
		lazy:  newLazy,
	}
}
