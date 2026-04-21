package vfs

import (
	"sort"
	"sync"
)

// DirtyTrackingFS wraps a FilesystemDriver and tracks which paths have been
// written to or removed, for syncing to a remote shell. Reads are pass-through.
type DirtyTrackingFS struct {
	Inner FilesystemDriver
	mu    sync.Mutex
	dirty map[string]bool
}

// NewDirtyTrackingFS wraps inner in a DirtyTrackingFS with an empty dirty set.
func NewDirtyTrackingFS(inner FilesystemDriver) *DirtyTrackingFS {
	return &DirtyTrackingFS{
		Inner: inner,
		dirty: make(map[string]bool),
	}
}

// Dirty returns a sorted copy of all currently-dirty paths.
func (d *DirtyTrackingFS) Dirty() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	paths := make([]string, 0, len(d.dirty))
	for p := range d.dirty {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// ClearDirty empties the dirty set.
func (d *DirtyTrackingFS) ClearDirty() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirty = make(map[string]bool)
}

func (d *DirtyTrackingFS) markDirty(path string) {
	d.mu.Lock()
	d.dirty[path] = true
	d.mu.Unlock()
}

// Write delegates to Inner and marks path dirty.
func (d *DirtyTrackingFS) Write(path string, content []byte) error {
	if err := d.Inner.Write(path, content); err != nil {
		return err
	}
	d.markDirty(path)
	return nil
}

// WriteString delegates to Inner and marks path dirty.
func (d *DirtyTrackingFS) WriteString(path string, content string) error {
	if err := d.Inner.WriteString(path, content); err != nil {
		return err
	}
	d.markDirty(path)
	return nil
}

// WriteLazy delegates to Inner and marks path dirty.
func (d *DirtyTrackingFS) WriteLazy(path string, provider func() ([]byte, error)) error {
	if err := d.Inner.WriteLazy(path, provider); err != nil {
		return err
	}
	d.markDirty(path)
	return nil
}

// Read delegates to Inner without marking dirty.
func (d *DirtyTrackingFS) Read(path string) ([]byte, error) {
	return d.Inner.Read(path)
}

// ReadString delegates to Inner without marking dirty.
func (d *DirtyTrackingFS) ReadString(path string) (string, error) {
	return d.Inner.ReadString(path)
}

// Exists delegates to Inner.
func (d *DirtyTrackingFS) Exists(path string) bool {
	return d.Inner.Exists(path)
}

// Remove delegates to Inner and marks path dirty.
func (d *DirtyTrackingFS) Remove(path string) error {
	if err := d.Inner.Remove(path); err != nil {
		return err
	}
	d.markDirty(path)
	return nil
}

// IsDir delegates to Inner.
func (d *DirtyTrackingFS) IsDir(path string) bool {
	return d.Inner.IsDir(path)
}

// Listdir delegates to Inner.
func (d *DirtyTrackingFS) Listdir(path string) ([]string, error) {
	return d.Inner.Listdir(path)
}

// Find delegates to Inner.
func (d *DirtyTrackingFS) Find(root, pattern string) ([]string, error) {
	return d.Inner.Find(root, pattern)
}

// Stat delegates to Inner.
func (d *DirtyTrackingFS) Stat(path string) (FileInfo, error) {
	return d.Inner.Stat(path)
}

// Clone returns a DirtyTrackingFS wrapping a clone of Inner with an empty
// dirty set, satisfying the FilesystemDriver interface.
func (d *DirtyTrackingFS) Clone() FilesystemDriver {
	return NewDirtyTrackingFS(d.Inner.Clone())
}
