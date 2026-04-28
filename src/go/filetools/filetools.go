// Package filetools provides built-in Read, Write, Edit, Glob, and Grep tools
// modelled on Claude Code's file toolset. All five tools delegate to a
// vfs.FilesystemDriver; swap the underlying driver and the tools follow.
package filetools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"agent-harness/go/shell/vfs"
	"agent-harness/go/tools"
)

var typeMap = map[string][]string{
	"py":   {".py"},
	"ts":   {".ts", ".tsx"},
	"js":   {".js", ".jsx", ".mjs", ".cjs"},
	"php":  {".php"},
	"go":   {".go"},
	"rs":   {".rs"},
	"java": {".java"},
	"rb":   {".rb"},
	"md":   {".md", ".markdown"},
	"txt":  {".txt"},
	"json": {".json"},
	"yaml": {".yaml", ".yml"},
	"sh":   {".sh", ".bash"},
	"html": {".html", ".htm"},
	"css":  {".css"},
	"sql":  {".sql"},
}

// Option configures a FileTools instance built with New.
type Option func(*FileTools)

// EnforceReadGate makes Edit reject calls to files that have not been Read in
// this session. Off by default (mirrors Python/TS/PHP register helpers).
func EnforceReadGate() Option {
	return func(f *FileTools) { f.enforceReadGate = true }
}

// FileTools holds the per-registration state shared across the five tools:
// the FS driver, a function to resolve the agent's current working directory,
// and the optional read-set.
type FileTools struct {
	fs              vfs.FilesystemDriver
	cwdFn           func() string
	enforceReadGate bool

	mu      sync.Mutex
	readSet map[string]struct{}
}

// New constructs a FileTools bound to fs and cwdFn. cwdFn is consulted on every
// Glob / Grep call so the tools follow the agent's current working directory.
func New(fs vfs.FilesystemDriver, cwdFn func() string, opts ...Option) *FileTools {
	f := &FileTools{
		fs:      fs,
		cwdFn:   cwdFn,
		readSet: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// RegisterAll registers Read, Write, Edit, Glob, and Grep on reg. It returns
// the FileTools so callers can inspect the read-set.
func RegisterAll(reg *tools.Registry, fs vfs.FilesystemDriver, cwdFn func() string, opts ...Option) *FileTools {
	f := New(fs, cwdFn, opts...)
	reg.Register(f.ReadDef())
	reg.Register(f.WriteDef())
	reg.Register(f.EditDef())
	reg.Register(f.GlobDef())
	reg.Register(f.GrepDef())
	return f
}

// HasRead reports whether path has been Read in this session.
func (f *FileTools) HasRead(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.readSet[norm(p)]
	return ok
}

// ---------------------------------------------------------------------------
// Tool defs
// ---------------------------------------------------------------------------

type readArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (f *FileTools) ReadDef() tools.Def {
	return tools.Def{
		Name: "Read",
		Description: "Read a file from the virtual filesystem. Returns content with `cat -n` " +
			"style line numbers (1-indexed). Defaults to the first 2000 lines. Use " +
			"`offset` (line number to start from, 1-indexed) and `limit` to page " +
			"through larger files. Binary files (containing null bytes in the first " +
			"8KB) are rejected with an error.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "Absolute path of the file to read."},
				"offset":    map[string]any{"type": "integer", "description": "Line number to start from (1-indexed). Default 1."},
				"limit":     map[string]any{"type": "integer", "description": "Maximum number of lines to return. Default 2000."},
			},
			"required": []string{"file_path"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var a readArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			p := norm(a.FilePath)
			if !f.fs.Exists(p) {
				return nil, fmt.Errorf("File does not exist: %s", p)
			}
			if f.fs.IsDir(p) {
				return nil, fmt.Errorf("Path is a directory, not a file: %s", p)
			}
			data, err := f.fs.Read(p)
			if err != nil {
				return nil, err
			}
			if isBinary(data) {
				return nil, errors.New("binary file not supported by Read; use a different tool")
			}
			f.mu.Lock()
			f.readSet[p] = struct{}{}
			f.mu.Unlock()
			offset := a.Offset
			if offset == 0 {
				offset = 1
			}
			limit := a.Limit
			if limit == 0 {
				limit = 2000
			}
			return formatLines(string(data), offset, limit), nil
		},
	}
}

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (f *FileTools) WriteDef() tools.Def {
	return tools.Def{
		Name:        "Write",
		Description: "Write content to a file, creating it if it doesn't exist or overwriting if it does.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "Absolute path of the file to write."},
				"content":   map[string]any{"type": "string", "description": "The content to write."},
			},
			"required": []string{"file_path", "content"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var a writeArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			p := norm(a.FilePath)
			if err := f.fs.WriteString(p, a.Content); err != nil {
				return nil, err
			}
			f.mu.Lock()
			f.readSet[p] = struct{}{}
			f.mu.Unlock()
			return "File written: " + p, nil
		},
	}
}

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (f *FileTools) EditDef() tools.Def {
	return tools.Def{
		Name: "Edit",
		Description: "Replace exact strings in a file. Errors if `old_string` is not found, " +
			"or appears more than once when `replace_all` is false (the default). " +
			"When the read-gate is enabled at registration time, errors if the file " +
			"has not been Read in this session.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path":   map[string]any{"type": "string", "description": "Absolute path of the file to edit."},
				"old_string":  map[string]any{"type": "string", "description": "The exact string to replace."},
				"new_string":  map[string]any{"type": "string", "description": "The replacement string."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences. Default false."},
			},
			"required": []string{"file_path", "old_string", "new_string"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var a editArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			p := norm(a.FilePath)
			if !f.fs.Exists(p) {
				return nil, fmt.Errorf("File does not exist: %s", p)
			}
			f.mu.Lock()
			_, hasRead := f.readSet[p]
			f.mu.Unlock()
			if f.enforceReadGate && !hasRead {
				return nil, errors.New("File has not been read yet. Read it first before writing to it.")
			}
			data, err := f.fs.Read(p)
			if err != nil {
				return nil, err
			}
			text := string(data)
			if a.OldString == "" || !strings.Contains(text, a.OldString) {
				return nil, errors.New("String to replace not found in file")
			}
			var updated string
			if a.ReplaceAll {
				updated = strings.ReplaceAll(text, a.OldString, a.NewString)
			} else {
				count := strings.Count(text, a.OldString)
				if count > 1 {
					return nil, fmt.Errorf(
						"Found %d matches of the string to replace, but replace_all is false. "+
							"Provide a larger string with more surrounding context to make it unique, "+
							"or set replace_all to true.", count)
				}
				updated = strings.Replace(text, a.OldString, a.NewString, 1)
			}
			if err := f.fs.WriteString(p, updated); err != nil {
				return nil, err
			}
			return "File edited: " + p, nil
		},
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func (f *FileTools) GlobDef() tools.Def {
	return tools.Def{
		Name: "Glob",
		Description: "Match files by pattern (supports `*`, `?`, and recursive `**`) and return " +
			"absolute paths sorted by modification time, newest first. `path` defaults " +
			"to the shell's current working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern, e.g. `**/*.go`."},
				"path":    map[string]any{"type": "string", "description": "Directory to search in. Defaults to cwd."},
			},
			"required": []string{"pattern"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var a globArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			root := f.resolveRoot(a.Path)
			files := f.walk(root)
			prefix := strings.TrimRight(root, "/") + "/"
			var matched []string
			re, err := globToRegex(a.Pattern)
			if err != nil {
				return nil, err
			}
			for _, p := range files {
				rel := p
				if strings.HasPrefix(p, prefix) {
					rel = p[len(prefix):]
				}
				if re.MatchString(rel) {
					matched = append(matched, p)
				}
			}
			mtimeOf := func(p string) int64 {
				info, err := f.fs.Stat(p)
				if err != nil {
					return 0
				}
				return info.Mtime
			}
			sort.SliceStable(matched, func(i, j int) bool {
				return mtimeOf(matched[i]) > mtimeOf(matched[j])
			})
			if matched == nil {
				return []string{}, nil
			}
			return matched, nil
		},
	}
}

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	Type            string `json:"type,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	LineNumbers     bool   `json:"line_numbers,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"`
	HeadLimit       int    `json:"head_limit,omitempty"`
	Multiline       bool   `json:"multiline,omitempty"`
	ContextBefore   int    `json:"context_before,omitempty"`
	ContextAfter    int    `json:"context_after,omitempty"`
}

func (f *FileTools) GrepDef() tools.Def {
	return tools.Def{
		Name: "Grep",
		Description: "Search file contents with a regular expression (Go RE2 syntax). " +
			"Recursively walks `path` (default cwd) and matches against each file's " +
			"content. `output_mode` controls return shape: `files_with_matches` (default) " +
			"→ list of paths; `count` → list of {path, count}; `content` → list of " +
			"{path, matches} where each match is {line, context: [{line, text, match}]}. " +
			"Use `glob` or `type` (py, ts, js, php, go, rs, java, rb, md, txt, json, " +
			"yaml, sh, html, css, sql) to filter the file set. Set `multiline` true to " +
			"match across line boundaries; `line_numbers` adds line numbers to context " +
			"entries; `context_before`/`context_after` add surrounding lines.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":          map[string]any{"type": "string", "description": "RE2 regex."},
				"path":             map[string]any{"type": "string", "description": "Directory to search in. Defaults to cwd."},
				"glob":             map[string]any{"type": "string", "description": "Filter the file set by glob pattern."},
				"type":             map[string]any{"type": "string", "description": "Filter by built-in file type alias."},
				"case_insensitive": map[string]any{"type": "boolean", "description": "Case-insensitive match. Default false."},
				"line_numbers":     map[string]any{"type": "boolean", "description": "Include line numbers in context entries. Default false."},
				"output_mode":      map[string]any{"type": "string", "description": "files_with_matches (default), count, or content."},
				"head_limit":       map[string]any{"type": "integer", "description": "Return only the first N results."},
				"multiline":        map[string]any{"type": "boolean", "description": "Allow patterns to match across line boundaries. Default false."},
				"context_before":   map[string]any{"type": "integer", "description": "Lines of context before each match. Default 0."},
				"context_after":    map[string]any{"type": "integer", "description": "Lines of context after each match. Default 0."},
			},
			"required": []string{"pattern"},
		},
		Execute: func(ctx context.Context, raw []byte) (any, error) {
			var a grepArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			root := f.resolveRoot(a.Path)
			files := f.walk(root)
			prefix := strings.TrimRight(root, "/") + "/"

			if a.Glob != "" {
				re, err := globToRegex(a.Glob)
				if err != nil {
					return nil, err
				}
				files = filter(files, func(p string) bool {
					rel := p
					if strings.HasPrefix(p, prefix) {
						rel = p[len(prefix):]
					}
					return re.MatchString(rel)
				})
			}
			if a.Type != "" {
				exts, ok := typeMap[a.Type]
				if !ok {
					return nil, fmt.Errorf("Unknown file type: %s", a.Type)
				}
				files = filter(files, func(p string) bool {
					for _, e := range exts {
						if strings.HasSuffix(p, e) {
							return true
						}
					}
					return false
				})
			}

			compileFlags := ""
			if a.CaseInsensitive {
				compileFlags += "i"
			}
			lineRegex, err := regexp.Compile(applyFlags(a.Pattern, compileFlags))
			if err != nil {
				return nil, err
			}
			multilineFlags := compileFlags
			if a.Multiline {
				multilineFlags += "s"
			}
			multiRegex, err := regexp.Compile(applyFlags(a.Pattern, multilineFlags))
			if err != nil {
				return nil, err
			}

			mode := a.OutputMode
			if mode == "" {
				mode = "files_with_matches"
			}

			switch mode {
			case "files_with_matches":
				var out []string
				for _, p := range files {
					data, err := f.fs.Read(p)
					if err != nil {
						continue
					}
					text := string(data)
					var hit bool
					if a.Multiline {
						hit = multiRegex.MatchString(text)
					} else {
						for _, line := range strings.Split(text, "\n") {
							if lineRegex.MatchString(line) {
								hit = true
								break
							}
						}
					}
					if hit {
						out = append(out, p)
					}
				}
				if out == nil {
					out = []string{}
				}
				if a.HeadLimit > 0 && len(out) > a.HeadLimit {
					out = out[:a.HeadLimit]
				}
				return out, nil

			case "count":
				type entry struct {
					Path  string `json:"path"`
					Count int    `json:"count"`
				}
				var out []entry
				for _, p := range files {
					data, err := f.fs.Read(p)
					if err != nil {
						continue
					}
					text := string(data)
					var n int
					if a.Multiline {
						n = len(multiRegex.FindAllStringIndex(text, -1))
					} else {
						for _, line := range strings.Split(text, "\n") {
							if lineRegex.MatchString(line) {
								n++
							}
						}
					}
					if n > 0 {
						out = append(out, entry{Path: p, Count: n})
					}
				}
				if out == nil {
					out = []entry{}
				}
				if a.HeadLimit > 0 && len(out) > a.HeadLimit {
					out = out[:a.HeadLimit]
				}
				return out, nil

			case "content":
				type ctxEntry struct {
					Line  int    `json:"line,omitempty"`
					Text  string `json:"text"`
					Match bool   `json:"match"`
				}
				type matchEntry struct {
					Line    int        `json:"line"`
					Context []ctxEntry `json:"context"`
				}
				type fileEntry struct {
					Path    string       `json:"path"`
					Matches []matchEntry `json:"matches"`
				}
				var out []fileEntry
				for _, p := range files {
					data, err := f.fs.Read(p)
					if err != nil {
						continue
					}
					lines := strings.Split(string(data), "\n")
					var hits []matchEntry
					for idx, line := range lines {
						if !lineRegex.MatchString(line) {
							continue
						}
						start := idx - a.ContextBefore
						if start < 0 {
							start = 0
						}
						end := idx + a.ContextAfter + 1
						if end > len(lines) {
							end = len(lines)
						}
						var ctxs []ctxEntry
						for j := start; j < end; j++ {
							e := ctxEntry{Text: lines[j], Match: j == idx}
							if a.LineNumbers {
								e.Line = j + 1
							}
							ctxs = append(ctxs, e)
						}
						hits = append(hits, matchEntry{Line: idx + 1, Context: ctxs})
					}
					if len(hits) > 0 {
						out = append(out, fileEntry{Path: p, Matches: hits})
					}
					if a.HeadLimit > 0 && len(out) >= a.HeadLimit {
						break
					}
				}
				if out == nil {
					out = []fileEntry{}
				}
				return out, nil

			default:
				return nil, fmt.Errorf("Unknown output_mode: %s", mode)
			}
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (f *FileTools) resolveRoot(p string) string {
	if p == "" {
		cwd := "/"
		if f.cwdFn != nil {
			cwd = f.cwdFn()
		}
		return norm(cwd)
	}
	if strings.HasPrefix(p, "/") {
		return norm(p)
	}
	cwd := "/"
	if f.cwdFn != nil {
		cwd = f.cwdFn()
	}
	return norm(path.Join(cwd, p))
}

func (f *FileTools) walk(root string) []string {
	root = norm(root)
	if !f.fs.Exists(root) {
		return nil
	}
	if !f.fs.IsDir(root) {
		return []string{root}
	}
	var out []string
	stack := []string{root}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := f.fs.Listdir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e, ".") {
				continue
			}
			child := path.Join(d, e)
			if f.fs.IsDir(child) {
				stack = append(stack, child)
			} else {
				out = append(out, child)
			}
		}
	}
	return out
}

func norm(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func isBinary(data []byte) bool {
	end := len(data)
	if end > 8192 {
		end = 8192
	}
	for i := 0; i < end; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func formatLines(text string, offset, limit int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := offset
	if start < 1 {
		start = 1
	}
	end := start + limit - 1
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		if i > start {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%6d\t%s", i, lines[i-1])
	}
	return b.String()
}

func filter(in []string, keep func(string) bool) []string {
	out := in[:0]
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// applyFlags wraps a regex source with `(?<flags>)` if any flags are set.
func applyFlags(pattern, flags string) string {
	if flags == "" {
		return pattern
	}
	return "(?" + flags + ")" + pattern
}

func globToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		if c == '*' && i+1 < len(pattern) && pattern[i+1] == '*' {
			b.WriteString(".*")
			i += 2
			if i < len(pattern) && pattern[i] == '/' {
				i++
			}
		} else if c == '*' {
			b.WriteString("[^/]*")
			i++
		} else if c == '?' {
			b.WriteString("[^/]")
			i++
		} else {
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
