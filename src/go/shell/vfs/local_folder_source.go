package vfs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalFolderSource mirrors a host directory as a read-only MountSource.
//
// Path-escape-safe: the real absolute root is resolved once, and every subpath
// is joined, resolved (following symlinks), and asserted to live at or under
// the root — defeating "../" and symlink escapes. Absolute subpaths are
// rejected; out-of-root paths are treated as not-found.
type LocalFolderSource struct {
	root string
}

// NewLocalFolderSource constructs a source rooted at the given host directory.
func NewLocalFolderSource(root string) *LocalFolderSource {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		abs, aerr := filepath.Abs(root)
		if aerr != nil {
			resolved = filepath.Clean(root)
		} else {
			resolved = abs
		}
	}
	return &LocalFolderSource{root: resolved}
}

// resolve maps a relative subpath onto a confined host path, or returns
// ("", false) if it would escape the root or is absolute.
func (s *LocalFolderSource) resolve(subpath string) (string, bool) {
	if filepath.IsAbs(subpath) {
		return "", false
	}
	joined := filepath.Join(s.root, filepath.FromSlash(subpath))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// Path may not exist yet — fall back to the lexically cleaned join so
		// not-found is distinguishable from escape.
		resolved = filepath.Clean(joined)
	}
	if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(os.PathSeparator)) {
		return "", false
	}
	return resolved, true
}

func (s *LocalFolderSource) Stat(subpath string) (MountStat, bool) {
	resolved, ok := s.resolve(subpath)
	if !ok {
		return MountStat{}, false
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return MountStat{}, false
	}
	return MountStat{
		IsDir: info.IsDir(),
		Size:  info.Size(),
		Mtime: info.ModTime().Unix(),
	}, true
}

func (s *LocalFolderSource) List(subpath string) ([]string, error) {
	resolved, ok := s.resolve(subpath)
	if !ok {
		return nil, os.ErrNotExist
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (s *LocalFolderSource) Read(subpath string) ([]byte, error) {
	resolved, ok := s.resolve(subpath)
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(resolved)
}
