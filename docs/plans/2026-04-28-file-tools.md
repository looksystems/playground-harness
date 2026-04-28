# File Tools — Plan

## Context

The agent-harness ships LLM-callable tools, skills, hooks, and middleware in four parallel implementations (Python, TypeScript, PHP, Go), with a swappable `FilesystemDriver` driving the virtual shell. It does **not** yet ship the equivalent of Claude Code's file toolset.

Goal: add five built-in tools — **Read, Write, Edit, Glob, Grep** — that:

1. Match Claude Code's observable behavior closely (parameter names, line-number formatting on Read, Edit's uniqueness rule, Glob's mtime sort, Grep's ripgrep-like flags).
2. Plug into each language's existing `FilesystemDriver` so swapping the shell backend (builtin / bashkit / OpenShell) automatically swaps the FS the tools see — no parallel FS abstraction.
3. Get a new cross-cutting guide (`docs/guides/file-tools.md`) following the same shape as `tools.md` / `middleware.md` / `events.md`, plus a `Read more` link from `docs/overview.md` and `README.md`.

## Design summary

### One driver, two surfaces

The `FilesystemDriver` interface already exposes `read`, `write`, `exists`, `remove`, `isDir`, `listdir`, `find(root, pattern)`, `stat`, `clone` in all four languages (`src/python/drivers.py`, `src/typescript/drivers.ts`, `src/php/FilesystemDriver.php`, `src/go/shell/vfs/driver.go`). The new file tools call **only** these methods — no driver changes required for the baseline implementation. Drivers that gain native search later can opt in via a follow-up.

Tool handlers reach the driver via the existing accessor on the agent:
- Python: `self.shell.fs` (via `HasShell`)
- TypeScript: `this.shell.fs`
- PHP: `$this->shell->fs()`
- Go: `agent.Shell.FS()`

### Driver gap to verify and close (small)

Two driver capabilities the spec needs but I have not yet confirmed exist:

1. **`stat` returns modification time** — Glob sorts results by mtime. If `stat` doesn't already include it, add `mtime` to the `FileInfo` struct/dict per language and update `BuiltinFilesystemDriver` to populate it from `VirtualFS`.
2. **Recursive `**` patterns** — `find(root, pattern)` uses fnmatch, which doesn't natively handle `**`. Rather than extending the driver, the **Glob tool itself** walks via `listdir` + `isDir` recursively and uses `find`/fnmatch only on each directory's filenames. This keeps the driver contract minimal and means Grep can reuse the same walker.

### Tool surface (Claude-Code-compatible parameters)

| Tool | Parameters | Behavior to match |
|------|------------|-------------------|
| **Read** | `file_path` (abs), `offset?`, `limit?` | `cat -n` style line-numbered output, default 2000 lines from line 1, empty file → empty-file marker. Non-text files (detected by null-byte sniff or extension allowlist — pick whichever is cheaper per language) return `{"error": "binary file not supported by Read; use a different tool"}`. |
| **Write** | `file_path` (abs), `content` | Overwrites or creates. Returns confirmation. |
| **Edit** | `file_path` (abs), `old_string`, `new_string`, `replace_all?` (default false) | Exact-string replacement. Errors if `old_string` not found, or appears more than once when `replace_all=false`. **Optional read-gate** (see "Read-before-Edit" below) — when enabled, additionally errors if the file hasn't been Read by this agent. |
| **Glob** | `pattern`, `path?` (defaults to cwd) | Returns absolute paths matching pattern (incl. `**`), sorted by mtime descending. |
| **Grep** | `pattern`, `path?`, `glob?`, `type?`, `-i?`, `-n?`, `-A?`, `-B?`, `-C?`, `output_mode?` ("content"\|"files_with_matches"\|"count", default "files_with_matches"), `head_limit?`, `multiline?` | Regex content search using each language's native regex engine. Filters by glob and a small built-in type map (`py`, `ts`, `php`, `go`, `js`, `md`, etc.). |

Parameter names match Claude Code exactly so prompt/skill content authored against Claude Code transfers cleanly.

### Read-before-Edit (opt-in)

Each language's file-tools module owns a small per-agent read-set: a set of absolute paths the Read tool has populated this session. When the agent registers file tools with the gate enabled, Edit consults the read-set and rejects calls to unread paths with Claude Code's exact wording (`"File has not been read yet. Read it first before writing to it."`). Default off — flipped via the registration helper:

- Python: `register_file_tools(agent, enforce_read_gate=True)`
- TypeScript: `registerFileTools(agent, { enforceReadGate: true })`
- PHP: `FileTools::registerAll($agent, enforceReadGate: true)`
- Go: `filetools.RegisterAll(reg, fs, filetools.EnforceReadGate())` (functional option)

The read-set lives on the closure/instance returned by the registration helper — no changes to the agent or driver. Resetting (e.g. on context compaction) is out of scope for v1; the agent simply Reads again.

### Where the code lives

Mirror the conventions established by `tools.go` / `uses_tools.py`:

- Python: `src/python/file_tools.py` — module exporting `read_tool`, `write_tool`, `edit_tool`, `glob_tool`, `grep_tool` as `ToolDef` instances, plus `register_file_tools(agent)`.
- TypeScript: `src/typescript/file-tools.ts` — same five `ToolDef` exports + `registerFileTools(agent)`.
- PHP: `src/php/FileTools.php` — class with static factory methods returning `ToolDef`, plus `FileTools::registerAll($agent)`.
- Go: `src/go/filetools/filetools.go` — exported `Read`, `Write`, `Edit`, `Glob`, `Grep` constructors taking the FS driver and returning `tools.ToolDef`, plus `RegisterAll(reg *tools.Registry, fs vfs.FilesystemDriver)`.

The walker (recursive `**` + filtering) and the line-number formatter are private helpers within each module — small enough not to warrant their own file.

### Errors

Use the existing per-language error envelope (`{"error": "..."}` JSON). Match Claude Code's specific error wording where it carries information ("File has not been read yet" — skipped in v1; "String to replace not found in file"; "Found N matches of the string to replace, but replace_all is false"; etc.).

### Hook/event integration

The existing `TOOL_REGISTER` / `TOOL_CALL` / `TOOL_RESULT` / `TOOL_ERROR` events fire automatically since these are normal `ToolDef`s — no new hook events.

## Files to create

### Source

- `src/python/file_tools.py`
- `src/typescript/file-tools.ts`
- `src/php/FileTools.php`
- `src/go/filetools/filetools.go`

### Tests (one file per language, mirroring `uses_tools` test structure)

- `tests/python/test_file_tools.py` — pytest, exercises each tool against an in-memory `BuiltinFilesystemDriver`.
- `tests/typescript/file-tools.test.ts` — vitest.
- `tests/php/FileToolsTest.php` — PHPUnit.
- `src/go/filetools/filetools_test.go` — co-located, Go convention.

Coverage per tool: happy path, missing-file error, Edit unique-vs-non-unique, Edit read-gate (both enabled and disabled), Read on a binary fixture returning the error envelope, Glob mtime sort, Grep regex + glob filter + each `output_mode`.

### Docs

- `docs/guides/file-tools.md` — new guide following `tools.md` structure: Overview → Anatomy table → Per-language definition (`## Python`, `## TypeScript`, `## PHP`, `## Go`) → Behavior reference (the table above, expanded) → Driver-independence note → Read-before-Edit (opt-in) → Known limitations → See also.

### ADRs

- `docs/adr/0032-file-tools.md` — **File Tools.** Captures the load-bearing decisions, in the established ADR shape (Status / Context / Decision / Consequences / Alternatives):
  - **Scope:** five tools (Read, Write, Edit, Glob, Grep) — chosen to mirror Claude Code's surface so prompt/skill content authored against Claude Code transfers cleanly.
  - **Delegation:** file tools call `FilesystemDriver` directly; no parallel FS interface, no native search additions to the driver. Recursive walks (`**`, Grep traversal) live in the tool layer, keeping the driver contract minimal and bashkit/OpenShell driver-compatible without changes.
  - **Parameter compatibility:** parameter names (`file_path`, `old_string`, `new_string`, `replace_all`, `output_mode`, etc.) match Claude Code exactly; observable behavior (line-numbered Read, Edit uniqueness rule, Glob mtime sort, Grep ripgrep semantics) matches closely.
  - **Read-before-Edit:** opt-in, owned by the registration helper's closure; no agent or driver changes.
  - **Errors:** reuse the existing `{"error": "..."}` envelope; mirror Claude Code's specific wording where it carries information.
  - Cross-references: ADR 0016 (UsesTools), ADR 0026 (Shell and Filesystem Driver Contracts), ADR 0007 (Language-Idiomatic Implementations).

### Plan archive

- This very plan, finalized, gets saved as `docs/plans/2026-04-28-file-tools.md` (matching the `YYYY-MM-DD-<slug>.md` convention used throughout `docs/plans/`).

## Files to modify

- `docs/overview.md` — add `File Tools` link in the cross-cutting guides list.
- `README.md` — add `File Tools` bullet under "Cross-Cutting Guides".
- `docs/adr/README.md` — add `0032 — File Tools` to the index (likely under the "Mixins" / built-in tools cluster alongside ADR 0016).
- `src/python/drivers.py`, `src/typescript/drivers.ts`, `src/php/FilesystemDriver.php` (+ `BuiltinFilesystemDriver.php`), `src/go/shell/vfs/driver.go` — **only if** verification (step 1 below) shows `mtime` is missing from `stat`. Add `mtime` field, populate from `VirtualFS`, leave existing fields intact. Note this in ADR 0032 if it happens.

## Implementation order

1. **Verify driver capabilities.** Read the four `FilesystemDriver` interface files plus their `BuiltinFilesystemDriver` and `VirtualFS` implementations. Confirm whether `stat` returns mtime. If not, extend (smallest possible diff) and update the driver tests in step 6.
2. **Python first** (richest reflection model — quickest to prototype): implement `file_tools.py`, including the recursive walker and line-number formatter. Write tests.
3. **Port to TypeScript**, then **PHP**, then **Go**. Keep the algorithm identical; only the language idioms differ.
4. **Cross-language parity check**: run all four test suites, confirm equivalent fixtures produce equivalent outputs (especially Grep regex and Glob ordering).
5. **Write the guide** (`docs/guides/file-tools.md`) once the four implementations agree — easier to write the doc against working code.
6. **Write ADR 0032** (`docs/adr/0032-file-tools.md`) and add it to `docs/adr/README.md`. Doing this after the implementation is intentional: any drift between the planned design and what shipped gets recorded honestly in the ADR rather than hidden.
7. **Wire links** in `docs/overview.md` and `README.md`.
8. **Archive this plan** at `docs/plans/2026-04-28-file-tools.md` (copy of the final form of this file).

## Verification

- Per-language: `pytest tests/python/test_file_tools.py`, `npx vitest run tests/typescript/file-tools.test.ts`, `vendor/bin/phpunit tests/php/FileToolsTest.php`, `cd src/go && go test ./filetools/...`.
- Cross-language smoke: write a small fixture (a few files including nested dirs, distinct mtimes, mixed content) and assert all four implementations produce equivalent Glob ordering, Grep matches, and Read line-number output. Ideally a single fixture set under `tests/fixtures/file-tools/` consumed by all four test suites.
- Driver-swap proof: run the Python tests with the `BuiltinFilesystemDriver`, then construct an agent with a stub driver that wraps an alternate map and re-run a subset to confirm the file tools follow the driver. (Bashkit/OpenShell aren't required to participate — they're proven by interface conformance, not invoked.)
- Documentation rendering: open `docs/guides/file-tools.md`, `docs/overview.md`, `README.md` and confirm links resolve and headings match the established style.

## Out of scope (deliberate, follow-up tickets)

- **Native ripgrep acceleration.** v1's Grep is pure-language. A follow-up could let the builtin driver shell out to `rg` when available — but the abstraction stays clean only if it's a driver capability, not a tool concern.
- **Image / PDF / notebook Read parsing.** v1 rejects binaries with a clear error. Multimodal Read is a separate, larger piece of work tied to provider capabilities.
- **Read-set persistence across compaction.** The opt-in read-gate's set is in-memory only. If a session compaction wipes the agent's view of what it read, it just Reads again. Persisting the set is a future concern if it proves annoying in practice.
