# File Tools

The harness ships five built-in tools — **Read, Write, Edit, Glob, Grep** — modelled on Claude Code's file toolset. Parameter names and observable behaviour match Claude Code closely so prompts, skills, and Markdown instructions written against Claude Code transfer cleanly. All five tools delegate to the agent's `FilesystemDriver`, so swapping the shell backend (builtin / bashkit / OpenShell) automatically swaps the filesystem they see — there is no parallel filesystem abstraction to keep in sync.

This guide covers the surface across the four implementations. For the Tools layer the file tools sit on top of, see the [Tools guide](tools.md). For the FS layer below, see [Virtual Bash](virtual-bash-reference.md) and [ADR 0026 — Shell and Filesystem Driver Contracts](../adr/0026-shell-driver-contracts.md).

## Anatomy of the toolset

| Tool | Purpose |
|------|---------|
| **Read** | Return file contents formatted with `cat -n`-style line numbers (1-indexed). Supports `offset` (line number to start from) and `limit` (max lines, default 2000). Rejects binary files. |
| **Write** | Create or overwrite a file with the given content. |
| **Edit** | Replace exact strings in a file. Errors if the target string is not found, or appears more than once when `replace_all` is false. Optionally enforces a read-before-edit gate. |
| **Glob** | Match files by pattern (supports `*`, `?`, and recursive `**`). Returns absolute paths sorted by modification time, newest first. |
| **Grep** | Search file contents with a regular expression. Three output modes (`files_with_matches` default, `count`, `content`), with `glob` / `type` filters and optional context lines. |

All five accept the same parameter shape across languages — JSON shapes are identical so the wire-format the LLM sees is portable.

## One driver, two surfaces

The file tools call only the existing `FilesystemDriver` interface methods (`read`, `write`, `exists`, `isDir`, `listdir`, `stat`). Recursive walks for `**` patterns and Grep traversal happen *in the tool layer*, using `listdir` + `isDir` — they are not pushed down into the driver, which keeps third-party drivers (bashkit, OpenShell) compatible without changes.

The single concrete addition to the driver was an **mtime field on stat**. `VirtualFS` now maintains a per-instance monotonic counter that increments on every `write` / `writeLazy`; `stat()` exposes it as the `mtime` field (Python/TS dict, PHP array, Go `FileInfo.Mtime`). Glob's "newest first" ordering is the only surface that depends on it — drivers that don't track mtime can return zero and Glob will sort stably as a fallback.

Because the tools go through the driver, **mounted content is transparently visible** to Read, Glob, and Grep. When a host folder (or any [`MountSource`](virtual-fs.md#mounts-programmatic-sources)) is mounted, the first mount re-seats the agent's fs to a mount-aware driver, so `Glob("/work/**/*.txt")`, `Grep`, and `Read("/work/...")` enumerate and read the merged tree exactly as they do for in-memory files — no tool changes required.

## Defining the file tools

Each language exports a single registration helper — call it once per agent and you get all five tools.

### Python

```python
from src.python.file_tools import register_file_tools

register_file_tools(agent)                      # default: read-gate off
register_file_tools(agent, enforce_read_gate=True)  # opt-in: Edit requires prior Read
```

Returns a `_FileToolsState` so tests can inspect the read-set. The five tools are registered as standard `ToolDef`s on the agent's `UsesTools` registry — they show up in `agent.tools` and `agent.toolsSchema()` like any other tool.

### TypeScript

```typescript
import { registerFileTools } from "./file-tools.js";

registerFileTools(agent);
registerFileTools(agent, { enforceReadGate: true });
```

Returns a `FileToolsState` for inspection. Tools dispatch through the same `executeTool(name, args)` path as user-defined tools.

### PHP

```php
use AgentHarness\FileTools;

FileTools::registerAll($agent);
FileTools::registerAll($agent, enforceReadGate: true);
```

Returns the `FileTools` instance, which holds the read-set on `$ft->readSet()`. The factory adds the five `ToolDef`s via `$agent->registerTool()` like any trait-composed tool.

### Go

```go
import (
    "agent-harness/go/filetools"
    "agent-harness/go/tools"
)

ft := filetools.RegisterAll(reg, fs, cwdFn)
ft  = filetools.RegisterAll(reg, fs, cwdFn, filetools.EnforceReadGate())
```

`reg` is the `*tools.Registry`, `fs` is the `vfs.FilesystemDriver`, and `cwdFn` is a function returning the agent's current working directory (Glob/Grep call it on every invocation, so it can change between calls). Returns the `*FileTools` so callers can use `ft.HasRead(path)` to inspect the read-set.

## Behaviour reference

### Read

```
Read(file_path: string, offset?: int = 1, limit?: int = 2000) -> string
```

Reads `file_path` and returns up to `limit` lines starting at line `offset` (1-indexed). Output format matches `cat -n`: six-character right-aligned line number, tab, content. Trailing newlines are stripped before formatting so the line count is the visible number of lines, not the raw split count.

**Errors:**

- `File does not exist: <path>` — the path is missing.
- `Path is a directory, not a file: <path>` — the path is a directory.
- `binary file not supported by Read; use a different tool` — null byte detected in the first 8 KB.

### Write

```
Write(file_path: string, content: string) -> string
```

Overwrites or creates `file_path`. Returns `File written: <path>` on success. The path is added to the read-set (so a follow-up Edit with the read-gate enabled will pass).

### Edit

```
Edit(file_path: string, old_string: string, new_string: string, replace_all?: bool = false) -> string
```

Replaces `old_string` with `new_string` in `file_path`. Without `replace_all`, requires `old_string` to appear exactly once.

**Errors:**

- `File does not exist: <path>` — missing target.
- `String to replace not found in file` — `old_string` does not appear.
- `Found N matches of the string to replace, but replace_all is false. Provide a larger string with more surrounding context to make it unique, or set replace_all to true.` — multiple matches.
- `File has not been read yet. Read it first before writing to it.` — only when the read-gate is enabled and Read has not been called for this path in the current session.

### Glob

```
Glob(pattern: string, path?: string) -> list[string]
```

Walks `path` (default = the agent's `shell.cwd`) recursively, matches each file's path *relative to `path`* against `pattern`, and returns the absolute paths sorted by mtime descending. Pattern syntax:

- `*` — any characters except `/`
- `?` — one character except `/`
- `**` — any characters including `/` (cross-segment); a trailing `/` is consumed so `**/foo` and `**foo` both work

Hidden directories (entries starting with `.`) are skipped during the walk.

### Grep

```
Grep(
    pattern: string,
    path?: string,
    glob?: string,
    type?: string,
    case_insensitive?: bool = false,
    line_numbers?: bool = false,
    output_mode?: "files_with_matches" | "count" | "content" = "files_with_matches",
    head_limit?: int,
    multiline?: bool = false,
    context_before?: int = 0,
    context_after?: int = 0,
) -> any
```

Recursively walks `path` and searches each file's content with the regular expression in `pattern` (Python `re`, JS `RegExp`, PHP PCRE, Go RE2). Optional pre-filters: `glob` (gitignore-style pattern against the relative path) and `type` (alias from a built-in extension map: `py`, `ts`, `js`, `php`, `go`, `rs`, `java`, `rb`, `md`, `txt`, `json`, `yaml`, `sh`, `html`, `css`, `sql`).

Output shape depends on `output_mode`:

- **`files_with_matches`** (default) — `["/path/to/a", "/path/to/b", ...]`
- **`count`** — `[{"path": "/path/to/a", "count": 3}, ...]`
- **`content`** — `[{"path": "/path/to/a", "matches": [{"line": 12, "context": [{"text": "...", "match": false, "line": 11}, {"text": "...", "match": true, "line": 12}, ...]}]}]` — `line` keys are added to context entries only when `line_numbers` is true; `context_before` and `context_after` set the surrounding-line counts.

`head_limit` truncates the result list. `multiline` enables `re.MULTILINE | re.DOTALL` (Python), `s` flag (JS/PHP/Go) so the pattern can span line boundaries.

## Read-before-Edit (opt-in)

Claude Code enforces "you must Read a file before you Edit it" — a guardrail against the model fabricating contents. The harness ships this as opt-in:

- **Default:** Edit accepts any path. The framework does not assume the model has session memory of prior reads.
- **Enabled:** the registration helper returns an object that owns a per-agent set of paths the Read tool has populated. Edit consults the set and rejects unread paths with the same error wording Claude Code uses.

The set is in-memory only; if your session compacts and the agent forgets it Read a file, it just Reads again. Persisting the set across compaction is not done — see [Out of scope](#out-of-scope) below.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Registration helper | `register_file_tools(agent, *, enforce_read_gate=False)` | `registerFileTools(agent, { enforceReadGate })` | `FileTools::registerAll($agent, enforceReadGate: false)` | `filetools.RegisterAll(reg, fs, cwdFn, opts...)` |
| State object returned | `_FileToolsState` (read-set) | `FileToolsState` | `FileTools` (use `->readSet()`) | `*FileTools` (use `.HasRead(p)`) |
| Module path | `src/python/file_tools.py` | `src/typescript/file-tools.ts` | `src/php/FileTools.php` | `src/go/filetools/filetools.go` |
| Regex engine | Python `re` | JS `RegExp` | PCRE (`preg_match`) | Go RE2 (`regexp`) |
| Glob recursion | Tool-side walk via `listdir` + `is_dir` | Tool-side walk | Tool-side walk | Tool-side walk |
| mtime source | `VirtualFS._mtimes` (monotonic counter) | `VirtualFS._mtimes` | `VirtualFS::$mtimes` | `VirtualFS.mtimes` |
| Read-set scope | Closure on the registration call | Closure on the registration call | `FileTools` instance | `*FileTools` instance |

## Known limitations

- **Pure-language Grep.** Grep is implemented in each host language's regex engine. There is no shell-out to `rg` even when it is on `$PATH`. For typical agent payloads (small VFS, dozens of files) the overhead is negligible; for large corpora consider a dedicated retrieval tool.
- **No image / PDF / notebook support on Read.** Binaries return a clear error envelope. Multimodal Read is provider-specific and out of scope for v1.
- **Read-set does not persist across context compaction.** If the agent's transcript is compacted between Read and Edit, the read-set still says the path was read — a benign deviation from Claude Code (which compacts the same way). If the harness instance itself is reconstructed, the read-set starts empty and the agent must Read again.
- **Pattern semantics differ slightly from `git`/`rsync` glob conventions.** `**` here matches zero-or-more characters including `/`. `pattern` is anchored to the path *relative to* the search root, not to the absolute path.
- **Regex differences across host engines.** Python `re`, JS `RegExp`, PCRE, and Go RE2 disagree on advanced features (lookbehind, recursive groups, possessive quantifiers). Patterns that exercise engine-specific features are not portable across language implementations of the harness; stick to common subset (basic anchors, character classes, alternation, quantifiers) for portability.

## Out of scope

- **Native ripgrep acceleration.** A follow-up could let the builtin driver shell out to `rg` when available, but the abstraction stays clean only if it's a driver capability, not a tool concern.
- **Image / PDF / notebook Read parsing.** Tied to provider capabilities; separate piece of work.
- **Read-set persistence across compaction.** In-memory only by design.

## See also

- [Tools guide](tools.md) — the registration model the file tools sit on
- [Virtual Filesystem](virtual-fs.md) — how the FS underneath these tools is used and implemented
- [Virtual Bash reference](virtual-bash-reference.md) — what the shell side of the same FS exposes
- [ADR 0026 — Shell and Filesystem Driver Contracts](../adr/0026-shell-driver-contracts.md)
- [ADR 0032 — File Tools](../adr/0032-file-tools.md)
- [Python guide: Tools](python.md#tools) · [TypeScript guide: Tools](typescript.md#tools) · [PHP guide: Tools](php.md#tools) · [Go guide: Tools](go.md#tools)
