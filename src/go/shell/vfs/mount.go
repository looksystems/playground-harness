package vfs

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// MountStat is a lightweight stat for a path inside a MountSource.
type MountStat struct {
	IsDir bool
	Size  int64
	Mtime int64
}

// MountSource is a read-only provider of a directory tree, addressed by
// relative subpaths ("" is the mount root). Stat uses the comma-ok idiom for
// existence checks (no error for missing paths); List/Read return an error
// only for real I/O faults and are called after Stat confirms existence.
type MountSource interface {
	Stat(subpath string) (MountStat, bool)
	List(subpath string) ([]string, error)
	Read(subpath string) ([]byte, error)
}

// Mountable is the capability interface for filesystems that support mounting
// read-only sources. Implemented by *MountingFilesystemDriver (and forwarded
// by the builtin shell driver). Kept out of the core FilesystemDriver contract
// per ADR 0026 / ADR 0031.
type Mountable interface {
	Mount(mountPoint string, source MountSource)
	Unmount(mountPoint string)
	Mounts() []string
}

type mountEntry struct {
	point  string
	source MountSource
}

// MountingFilesystemDriver overlays read-only MountSources onto an inner
// writable FilesystemDriver using copy-up semantics: writes shadow the source
// in the inner layer (the source is never mutated), and deleting a source-only
// path errors (read-only, no whiteout/tombstone in v1).
//
// With an empty mount table every method is a transparent passthrough to inner.
// The mount slice is guarded by a RWMutex; inner is already thread-safe.
type MountingFilesystemDriver struct {
	inner  FilesystemDriver
	mu     sync.RWMutex
	mounts []mountEntry
}

// NewMountingFilesystemDriver wraps inner with an empty mount table.
func NewMountingFilesystemDriver(inner FilesystemDriver) *MountingFilesystemDriver {
	return &MountingFilesystemDriver{inner: inner}
}

// --- Mountable capability ---------------------------------------------------

func (m *MountingFilesystemDriver) Mount(mountPoint string, source MountSource) {
	mountPoint = norm(mountPoint)
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.mounts[:0:0]
	for _, e := range m.mounts {
		if e.point != mountPoint {
			filtered = append(filtered, e)
		}
	}
	m.mounts = append(filtered, mountEntry{point: mountPoint, source: source})
}

func (m *MountingFilesystemDriver) Unmount(mountPoint string) {
	mountPoint = norm(mountPoint)
	m.mu.Lock()
	defer m.mu.Unlock()
	var filtered []mountEntry
	for _, e := range m.mounts {
		if e.point != mountPoint {
			filtered = append(filtered, e)
		}
	}
	m.mounts = filtered
}

func (m *MountingFilesystemDriver) Mounts() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.mounts))
	for _, e := range m.mounts {
		out = append(out, e.point)
	}
	sort.Strings(out)
	return out
}

// --- routing helpers --------------------------------------------------------

// resolveMount returns the longest matching mount entry and the subpath
// relative to its mount point.
func (m *MountingFilesystemDriver) resolveMount(p string) (mountEntry, string, bool) {
	p = norm(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	var best mountEntry
	var bestSub string
	found := false
	for _, e := range m.mounts {
		var sub string
		switch {
		case p == e.point:
			sub = ""
		case e.point == "/":
			sub = strings.TrimPrefix(p, "/")
		case strings.HasPrefix(p, e.point+"/"):
			sub = p[len(e.point)+1:]
		default:
			continue
		}
		if !found || len(e.point) > len(best.point) {
			best, bestSub, found = e, sub, true
		}
	}
	return best, bestSub, found
}

func (m *MountingFilesystemDriver) isMountPointOrAncestor(p string) bool {
	p = norm(p)
	prefix := p + "/"
	if p == "/" {
		prefix = "/"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.mounts {
		if e.point == p || strings.HasPrefix(e.point, prefix) {
			return true
		}
	}
	return false
}

// --- FilesystemDriver -------------------------------------------------------

func (m *MountingFilesystemDriver) Write(p string, content []byte) error {
	return m.inner.Write(p, content)
}

func (m *MountingFilesystemDriver) WriteString(p, content string) error {
	return m.inner.WriteString(p, content)
}

func (m *MountingFilesystemDriver) WriteLazy(p string, provider func() ([]byte, error)) error {
	return m.inner.WriteLazy(p, provider)
}

func (m *MountingFilesystemDriver) Read(p string) ([]byte, error) {
	if m.inner.Exists(p) && !m.inner.IsDir(p) {
		return m.inner.Read(p)
	}
	if e, sub, ok := m.resolveMount(p); ok {
		if st, found := e.source.Stat(sub); found && !st.IsDir {
			return e.source.Read(sub)
		}
	}
	return m.inner.Read(p) // returns fs.ErrNotExist
}

func (m *MountingFilesystemDriver) ReadString(p string) (string, error) {
	data, err := m.Read(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *MountingFilesystemDriver) Exists(p string) bool {
	if m.inner.Exists(p) {
		return true
	}
	if m.isMountPointOrAncestor(p) {
		return true
	}
	if e, sub, ok := m.resolveMount(p); ok {
		if _, found := e.source.Stat(sub); found {
			return true
		}
	}
	return false
}

func (m *MountingFilesystemDriver) Remove(p string) error {
	if m.inner.Exists(p) {
		return m.inner.Remove(p)
	}
	if e, sub, ok := m.resolveMount(p); ok {
		if _, found := e.source.Stat(sub); found {
			return fmt.Errorf("%s: read-only mount source (cannot delete)", norm(p))
		}
	}
	return fs.ErrNotExist
}

func (m *MountingFilesystemDriver) IsDir(p string) bool {
	if m.inner.IsDir(p) {
		return true
	}
	if m.isMountPointOrAncestor(p) {
		return true
	}
	if e, sub, ok := m.resolveMount(p); ok {
		if st, found := e.source.Stat(sub); found && st.IsDir {
			return true
		}
	}
	return false
}

func (m *MountingFilesystemDriver) Listdir(p string) ([]string, error) {
	p = norm(p)
	set := map[string]struct{}{}
	if names, err := m.inner.Listdir(p); err == nil {
		for _, n := range names {
			set[n] = struct{}{}
		}
	}
	if e, sub, ok := m.resolveMount(p); ok {
		if st, found := e.source.Stat(sub); found && st.IsDir {
			if names, err := e.source.List(sub); err == nil {
				for _, n := range names {
					set[n] = struct{}{}
				}
			}
		}
	}
	prefix := p + "/"
	if p == "/" {
		prefix = "/"
	}
	m.mu.RLock()
	for _, e := range m.mounts {
		if strings.HasPrefix(e.point, prefix) {
			rest := e.point[len(prefix):]
			seg := strings.SplitN(rest, "/", 2)[0]
			if seg != "" {
				set[seg] = struct{}{}
			}
		}
	}
	m.mu.RUnlock()
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m *MountingFilesystemDriver) Find(root, pattern string) ([]string, error) {
	root = norm(root)
	var results []string
	var walk func(dir string) error
	walk = func(dir string) error {
		names, err := m.Listdir(dir)
		if err != nil {
			return err
		}
		for _, name := range names {
			full := dir + "/" + name
			if dir == "/" {
				full = "/" + name
			}
			isDir := m.IsDir(full)
			if !isDir {
				matched, err := path.Match(pattern, name)
				if err != nil {
					return err
				}
				if matched {
					results = append(results, full)
				}
			}
			if isDir {
				if err := walk(full); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if m.IsDir(root) {
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	sort.Strings(results)
	return results, nil
}

func (m *MountingFilesystemDriver) Stat(p string) (FileInfo, error) {
	if m.inner.Exists(p) {
		return m.inner.Stat(p)
	}
	np := norm(p)
	if m.isMountPointOrAncestor(np) {
		return FileInfo{Path: np, Type: "dir"}, nil
	}
	if e, sub, ok := m.resolveMount(np); ok {
		if st, found := e.source.Stat(sub); found {
			if st.IsDir {
				return FileInfo{Path: np, Type: "dir"}, nil
			}
			return FileInfo{Path: np, Type: "file", Size: st.Size, Mtime: st.Mtime}, nil
		}
	}
	return FileInfo{}, fs.ErrNotExist
}

// Clone clones the inner writable layer and copies the mount table by
// reference (sources are read-only and shareable).
func (m *MountingFilesystemDriver) Clone() FilesystemDriver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]mountEntry, len(m.mounts))
	copy(cp, m.mounts)
	return &MountingFilesystemDriver{inner: m.inner.Clone(), mounts: cp}
}
