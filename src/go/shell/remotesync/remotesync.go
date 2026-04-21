// Package remotesync provides shared utilities for shell drivers that
// execute commands in an external process (SSH, gRPC, subprocess) and need
// to round-trip virtual-filesystem state across the boundary.
//
// The contract mirrors Python's src/python/_remote_sync.py:
//
//   - BuildPreamble emits shell commands that materialise every dirty
//     file from the local VFS on the remote side before the user's
//     command runs.
//
//   - BuildEpilogue emits a trailing script that captures the exit
//     code, prints a unique marker, then dumps every file under a root
//     as ===FILE:<path>===\n<base64 content> blocks.
//
//   - ParseOutput splits raw output at the marker into (userStdout,
//     filesMap). A nil map signals the command died before the epilogue
//     ran — callers should treat that as "no sync-back data".
//
//   - ApplyBack diffs the parsed files against the local VFS and writes
//     back the deltas (adds, updates, and deletions). It uses the
//     DirtyTrackingFS's inner driver to bypass the dirty-set during
//     sync-back so the next preamble doesn't re-upload files the remote
//     just handed back.
//
// The package has no transport-specific concerns — OpenShell and bashkit
// drivers both share it.
package remotesync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"agent-harness/go/shell/vfs"
)

// BuildPreamble returns shell commands that sync the dirty files from a
// local VFS to a remote shell's workspace. Commands are joined with
// " && " so the chain short-circuits on the first failure. The dirty set
// is cleared as a side-effect. Returns empty string if nothing is dirty.
func BuildPreamble(fs *vfs.DirtyTrackingFS) string {
	if fs == nil {
		return ""
	}
	paths := fs.Dirty()
	if len(paths) == 0 {
		return ""
	}
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		if fs.Exists(path) && !fs.IsDir(path) {
			content, err := fs.ReadString(path)
			if err != nil {
				continue
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(content))
			parts = append(parts, fmt.Sprintf(
				"mkdir -p $(dirname '%s') && printf '%%s' '%s' | base64 -d > '%s'",
				path, encoded, path,
			))
		} else if !fs.Exists(path) {
			parts = append(parts, fmt.Sprintf("rm -f '%s'", path))
		}
	}
	fs.ClearDirty()
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " && ")
}

// NewMarker returns a cryptographically-random, unique marker string used
// to delimit the epilogue's file-listing output. The format matches
// Python's __HARNESS_FS_SYNC_<16hex>__ so traces are legible across both
// implementations.
func NewMarker() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand.Read should never fail on a supported OS; if it
		// does the caller will surface it via the shell command.
		return "__HARNESS_FS_SYNC_fallback__"
	}
	return "__HARNESS_FS_SYNC_" + hex.EncodeToString(buf[:]) + "__"
}

// BuildEpilogue returns shell commands that capture all file state after
// the user's command, emitting a marker followed by ===FILE:<path>===
// blocks with base64-encoded contents. The original command's exit code
// is preserved via `__exit=$?` / `exit $__exit`.
//
// root is the filesystem root to scan ("/" for pure virtual filesystems;
// "/home/sandbox/workspace" for an OpenShell sandbox) — use a narrow
// root to avoid scanning system files.
func BuildEpilogue(marker, root string) string {
	if root == "" {
		root = "/"
	}
	return fmt.Sprintf(
		"; __exit=$?; printf '\\n%s\\n'; "+
			"find %s -type f 2>/dev/null -exec sh -c "+
			"'for f; do printf \"===FILE:%%s===\\n\" \"$f\"; base64 \"$f\"; done' _ {} +; "+
			"exit $__exit",
		marker, root,
	)
}

// ParseOutput splits the raw output into (userStdout, filesMap) by
// locating the marker. filesMap is nil if the marker is not present
// (indicating the command died before the epilogue ran).
func ParseOutput(raw, marker string) (string, map[string]string) {
	needle := "\n" + marker + "\n"
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return raw, nil
	}
	userStdout := raw[:idx]
	syncData := raw[idx+len(needle):]
	return userStdout, parseFileListing(syncData)
}

// ParseFileListing parses a ===FILE:path=== delimited base64 stream into
// a path→content map. Exposed for drivers that run the sync-back script
// as a separate command (without a leading marker).
func ParseFileListing(stdout string) map[string]string {
	return parseFileListing(stdout)
}

func parseFileListing(stdout string) map[string]string {
	const (
		startMarker = "===FILE:"
		endMarker   = "==="
	)
	files := make(map[string]string)
	currentPath := ""
	var contentLines []string

	flush := func() {
		if currentPath == "" {
			return
		}
		encoded := strings.Join(contentLines, "")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			files[currentPath] = encoded
		} else {
			files[currentPath] = string(decoded)
		}
	}

	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, startMarker) && strings.HasSuffix(line, endMarker) &&
			len(line) > len(startMarker)+len(endMarker) {
			flush()
			currentPath = line[len(startMarker) : len(line)-len(endMarker)]
			contentLines = contentLines[:0]
			continue
		}
		if currentPath != "" {
			contentLines = append(contentLines, line)
		}
	}
	flush()
	return files
}

// ApplyBack updates the local VFS with new/modified/deleted files from
// the remote side:
//
//   - New remote files are added to the VFS.
//   - Modified remote files overwrite the local copy.
//   - Files present locally but absent remotely are removed.
//
// Writes go through DirtyTrackingFS.Inner so the dirty-set isn't polluted
// — the next preamble should not re-upload content that the remote just
// returned.
func ApplyBack(ctx context.Context, fs *vfs.DirtyTrackingFS, files map[string]string) error {
	if fs == nil || files == nil {
		return nil
	}

	// Enumerate all current VFS files (non-dirs) under "/".
	all, err := fs.Find("/", "*")
	if err != nil {
		return fmt.Errorf("remotesync: enumerate local fs: %w", err)
	}
	local := make(map[string]struct{}, len(all))
	for _, p := range all {
		if !fs.IsDir(p) {
			local[p] = struct{}{}
		}
	}

	// Add or update files present remotely.
	for path, newContent := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, existsLocally := local[path]; !existsLocally {
			if err := fs.Inner.WriteString(path, newContent); err != nil {
				return fmt.Errorf("remotesync: write %q: %w", path, err)
			}
			continue
		}
		existing, err := fs.ReadString(path)
		if err != nil {
			// If the file vanished between Find and Read, treat as new.
			if err := fs.Inner.WriteString(path, newContent); err != nil {
				return fmt.Errorf("remotesync: write %q: %w", path, err)
			}
			continue
		}
		if existing != newContent {
			if err := fs.Inner.WriteString(path, newContent); err != nil {
				return fmt.Errorf("remotesync: update %q: %w", path, err)
			}
		}
	}

	// Remove local files not present remotely.
	for path := range local {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, stillRemote := files[path]; stillRemote {
			continue
		}
		if !fs.Exists(path) {
			continue
		}
		if err := fs.Inner.Remove(path); err != nil {
			return fmt.Errorf("remotesync: remove %q: %w", path, err)
		}
	}
	return nil
}
