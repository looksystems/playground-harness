// Package builtin — filesystem / directory builtins.
//
// This file ports the Python `_cmd_*` methods in src/python/shell.py
// covering: cat, echo, pwd, cd, ls, find, mkdir, touch, cp, rm, stat,
// tree. Each builtin takes the shared (ctx, env, args, stdin) signature
// and returns shell.ExecResult. All file I/O goes through env.FS — no
// host filesystem access.
//
// Parity with Python: arg parsing, flag handling, and error messages
// follow the reference implementation faithfully. Deviations are called
// out inline and in the task report.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"agent-harness/go/shell"
)

// resolvePath joins p against env.CWD using forward-slash semantics,
// matching Python's Shell._resolve (os.path.normpath with forward-slash).
func resolvePath(env *ExecEnv, p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	cwd := "/"
	if env != nil && env.CWD != nil && *env.CWD != "" {
		cwd = *env.CWD
	}
	return path.Clean(path.Join(cwd, p))
}

// cwdOf returns the effective CWD string for env (defaulting to "/").
func cwdOf(env *ExecEnv) string {
	if env == nil || env.CWD == nil || *env.CWD == "" {
		return "/"
	}
	return *env.CWD
}

// BuiltinCat concatenates files or stdin, mirroring Python _cmd_cat.
// With no args, returns stdin. With args, reads each file and
// concatenates. Missing files produce a stderr error and exit 1.
func BuiltinCat(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if len(args) == 0 {
		return shell.ExecResult{Stdout: stdin}
	}
	var out strings.Builder
	for _, p := range args {
		if err := ctx.Err(); err != nil {
			return shell.ExecResult{ExitCode: 130, Stderr: err.Error() + "\n"}
		}
		abs := resolvePath(env, p)
		if env.FS == nil || !env.FS.Exists(abs) {
			return shell.ExecResult{
				Stderr:   fmt.Sprintf("%s\n", fs.ErrNotExist.Error()),
				ExitCode: 1,
			}
		}
		s, err := env.FS.ReadString(abs)
		if err != nil {
			return shell.ExecResult{
				Stderr:   err.Error() + "\n",
				ExitCode: 1,
			}
		}
		out.WriteString(s)
	}
	return shell.ExecResult{Stdout: out.String()}
}

// BuiltinEcho prints args joined by spaces, followed by a newline.
// Python _cmd_echo is deliberately minimal — no -n/-e flag handling.
func BuiltinEcho(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	return shell.ExecResult{Stdout: strings.Join(args, " ") + "\n"}
}

// BuiltinPwd prints the current working directory.
func BuiltinPwd(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	return shell.ExecResult{Stdout: cwdOf(env) + "\n"}
}

// BuiltinCd changes the working directory. The target defaults to "/".
// Matches Python: the directory must exist (or be root) to succeed.
func BuiltinCd(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	target := "/"
	if len(args) > 0 {
		target = args[0]
	}
	resolved := resolvePath(env, target)
	if env.FS != nil && !env.FS.IsDir(resolved) && resolved != "/" {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("cd: %s: No such directory\n", target),
			ExitCode: 1,
		}
	}
	if env.CWD != nil {
		*env.CWD = resolved
	}
	return shell.ExecResult{}
}

// BuiltinLs lists directory contents. Flags: -l (long format).
// Non-flag arg selects the target (defaults to cwd). Missing target
// produces a stderr error and exit 1.
func BuiltinLs(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	longFormat := false
	var paths []string
	for _, a := range args {
		if a == "-l" {
			longFormat = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Unknown flag: absorb silently (Python ignores unknown
			// flags in this naive parser — matches the reference).
			continue
		}
		paths = append(paths, a)
	}
	target := cwdOf(env)
	if len(paths) > 0 {
		target = paths[0]
	}
	resolved := resolvePath(env, target)

	if env.FS == nil {
		return shell.ExecResult{Stderr: "ls: no filesystem\n", ExitCode: 1}
	}
	entries, err := env.FS.Listdir(resolved)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("ls: %s: No such directory\n", target),
			ExitCode: 1,
		}
	}
	// Python treats a missing-but-accessible path as "no entries"; if the
	// directory is absent entirely, Listdir returns empty. To keep the
	// "No such directory" error behaviour, check IsDir explicitly when
	// the target isn't root.
	if resolved != "/" && !env.FS.IsDir(resolved) {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("ls: %s: No such directory\n", target),
			ExitCode: 1,
		}
	}
	if !longFormat {
		if len(entries) == 0 {
			return shell.ExecResult{}
		}
		return shell.ExecResult{Stdout: strings.Join(entries, "\n") + "\n"}
	}
	var lines []string
	for _, entry := range entries {
		full := path.Join(resolved, entry)
		info, err := env.FS.Stat(full)
		if err != nil {
			// Implicit directory: no stat entry. Python falls through
			// to "directory" display via _is_dir check; mimic.
			if env.FS.IsDir(full) {
				lines = append(lines, fmt.Sprintf("drwxr-xr-x  -  %s/", entry))
				continue
			}
			lines = append(lines, fmt.Sprintf("-rw-r--r--  %8d  %s", 0, entry))
			continue
		}
		if info.Type == "dir" {
			lines = append(lines, fmt.Sprintf("drwxr-xr-x  -  %s/", entry))
		} else {
			lines = append(lines, fmt.Sprintf("-rw-r--r--  %8d  %s", info.Size, entry))
		}
	}
	if len(lines) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(lines, "\n") + "\n"}
}

// BuiltinFind implements `find` with -name (glob) and -type (f|d)
// filters. No -maxdepth support in Python; we match.
func BuiltinFind(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	root := "."
	nameFilter := ""
	typeFilter := ""

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-name" && i+1 < len(args):
			nameFilter = args[i+1]
			i++
		case a == "-type" && i+1 < len(args):
			typeFilter = args[i+1]
			i++
		case !strings.HasPrefix(a, "-"):
			root = a
		}
	}

	if env.FS == nil {
		return shell.ExecResult{Stderr: "find: no filesystem\n", ExitCode: 1}
	}

	resolved := resolvePath(env, root)
	pattern := nameFilter
	if pattern == "" {
		pattern = "*"
	}
	results, err := env.FS.Find(resolved, pattern)
	if err != nil {
		return shell.ExecResult{
			Stderr:   fmt.Sprintf("find: %s\n", err.Error()),
			ExitCode: 1,
		}
	}

	// Apply type filter. Python's fs.find only returns files (no
	// directories), so type=d filter yields empty in practice. Match.
	if typeFilter == "f" {
		filtered := results[:0]
		for _, r := range results {
			if !env.FS.IsDir(r) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	} else if typeFilter == "d" {
		filtered := results[:0]
		for _, r := range results {
			if env.FS.IsDir(r) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	if len(results) == 0 {
		return shell.ExecResult{}
	}
	return shell.ExecResult{Stdout: strings.Join(results, "\n") + "\n"}
}

// BuiltinMkdir creates a directory by writing a .keep sentinel file.
// The VFS uses implicit directories (a dir exists iff a child path
// exists), so mkdir/.keep is how Python creates an "empty" dir.
func BuiltinMkdir(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if env.FS == nil {
		return shell.ExecResult{Stderr: "mkdir: no filesystem\n", ExitCode: 1}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		abs := resolvePath(env, a)
		if err := env.FS.WriteString(abs+"/.keep", ""); err != nil {
			return shell.ExecResult{
				Stderr:   fmt.Sprintf("mkdir: %s: %s\n", a, err.Error()),
				ExitCode: 1,
			}
		}
	}
	return shell.ExecResult{}
}

// BuiltinTouch creates an empty file at each path that doesn't already
// exist. Updating mtime is a no-op in VFS (no metadata).
func BuiltinTouch(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if env.FS == nil {
		return shell.ExecResult{Stderr: "touch: no filesystem\n", ExitCode: 1}
	}
	for _, a := range args {
		abs := resolvePath(env, a)
		if !env.FS.Exists(abs) {
			if err := env.FS.WriteString(abs, ""); err != nil {
				return shell.ExecResult{
					Stderr:   fmt.Sprintf("touch: %s: %s\n", a, err.Error()),
					ExitCode: 1,
				}
			}
		}
	}
	return shell.ExecResult{}
}

// BuiltinCp copies a single file from src to dst. Flags are stripped
// silently (Python does the same with its naive filter).
func BuiltinCp(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	pos := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) < 2 {
		return shell.ExecResult{Stderr: "cp: missing operand\n", ExitCode: 1}
	}
	if env.FS == nil {
		return shell.ExecResult{Stderr: "cp: no filesystem\n", ExitCode: 1}
	}
	src := resolvePath(env, pos[0])
	dst := resolvePath(env, pos[1])
	data, err := env.FS.Read(src)
	if err != nil {
		return shell.ExecResult{
			Stderr:   err.Error() + "\n",
			ExitCode: 1,
		}
	}
	if err := env.FS.Write(dst, data); err != nil {
		return shell.ExecResult{
			Stderr:   err.Error() + "\n",
			ExitCode: 1,
		}
	}
	return shell.ExecResult{}
}

// BuiltinRm removes files. Flags (-r, -f, etc.) are stripped silently;
// missing files are ignored (Python catches FileNotFoundError and
// continues).
func BuiltinRm(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if env.FS == nil {
		return shell.ExecResult{Stderr: "rm: no filesystem\n", ExitCode: 1}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		abs := resolvePath(env, a)
		// Remove is idempotent in the VFS (no-op for missing). Python
		// relies on FileNotFoundError being raised by fs.remove, but
		// our VFS Remove returns nil for missing — matches "rm -f"
		// semantics and Python's swallowed exception.
		_ = env.FS.Remove(abs)
	}
	return shell.ExecResult{}
}

// BuiltinStat prints file metadata as indented JSON, matching Python
// which uses json.dumps(..., indent=2). Python uses "type":"directory"
// for directories and {path,type,size} for files — we convert our VFS
// type string ("dir"→"directory") to preserve parity.
func BuiltinStat(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	if env.FS == nil {
		return shell.ExecResult{Stderr: "stat: no filesystem\n", ExitCode: 1}
	}
	for _, f := range args {
		if strings.HasPrefix(f, "-") {
			continue
		}
		abs := resolvePath(env, f)
		info, err := env.FS.Stat(abs)
		if err != nil {
			return shell.ExecResult{
				Stderr:   err.Error() + "\n",
				ExitCode: 1,
			}
		}
		var payload map[string]any
		if info.Type == "dir" {
			payload = map[string]any{
				"path": info.Path,
				"type": "directory",
			}
		} else {
			payload = map[string]any{
				"path": info.Path,
				"type": "file",
				"size": info.Size,
			}
		}
		// Python json.dumps(indent=2) produces keys sorted
		// lexicographically by default? Actually no: Python preserves
		// insertion order. We emit keys in the same order Python does
		// by constructing an ordered struct manually.
		var out string
		if info.Type == "dir" {
			out = fmt.Sprintf("{\n  \"path\": %s,\n  \"type\": \"directory\"\n}", jsonString(payload["path"].(string)))
		} else {
			out = fmt.Sprintf("{\n  \"path\": %s,\n  \"type\": \"file\",\n  \"size\": %d\n}",
				jsonString(info.Path), info.Size)
		}
		return shell.ExecResult{Stdout: out + "\n"}
	}
	return shell.ExecResult{}
}

// jsonString returns a JSON-quoted string, delegating to encoding/json
// so escape rules match Python's json.dumps.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// Shouldn't happen for strings; fall back to a literal.
		return fmt.Sprintf("%q", s)
	}
	return string(b)
}

// BuiltinTree prints a recursive visual tree rooted at the target
// (default cwd). Uses the Unicode box-drawing characters Python uses.
func BuiltinTree(ctx context.Context, env *ExecEnv, args []string, stdin string) shell.ExecResult {
	target := cwdOf(env)
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		target = a
		break
	}
	if env.FS == nil {
		return shell.ExecResult{Stderr: "tree: no filesystem\n", ExitCode: 1}
	}
	resolved := resolvePath(env, target)
	lines := []string{resolved}
	if err := treeRecurse(ctx, env, resolved, "", &lines); err != nil {
		return shell.ExecResult{Stderr: err.Error() + "\n", ExitCode: 1}
	}
	return shell.ExecResult{Stdout: strings.Join(lines, "\n") + "\n"}
}

func treeRecurse(ctx context.Context, env *ExecEnv, p, prefix string, lines *[]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := env.FS.Listdir(p)
	if err != nil {
		return err
	}
	// Listdir returns sorted; preserve deterministic order.
	sort.Strings(entries)
	for i, entry := range entries {
		isLast := i == len(entries)-1
		connector := "\u251c\u2500\u2500 "
		if isLast {
			connector = "\u2514\u2500\u2500 "
		}
		full := path.Join(p, entry)
		isDir := env.FS.IsDir(full)
		suffix := ""
		if isDir {
			suffix = "/"
		}
		*lines = append(*lines, prefix+connector+entry+suffix)
		if isDir {
			extension := "\u2502   "
			if isLast {
				extension = "    "
			}
			if err := treeRecurse(ctx, env, full, prefix+extension, lines); err != nil {
				return err
			}
		}
	}
	return nil
}
