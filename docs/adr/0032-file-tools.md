# ADR 0032: File Tools

## Status

Accepted

## Context

The harness ships LLM-callable tools, skills, hooks, middleware, and a shared `FilesystemDriver` interface — but it has no built-in equivalent of Claude Code's file toolset (Read, Write, Edit, Glob, Grep). Agents that want LLM-driven file manipulation either expose them ad-hoc per project or fall through to the `exec` tool and rely on the model emitting `cat`/`sed`/`grep` shell commands.

Two pressures push toward a shared implementation:

1. **Prompt portability.** Skills, system prompts, and Markdown instructions written for Claude Code reference these tools by name and parameter shape. Reproducing the same surface here means that prompt content transfers without rewrites.
2. **Driver coupling.** The filesystem the tools operate on must be the same one the virtual shell sees, so that `cat foo.txt` and `Read({ file_path: "foo.txt" })` agree. Defining a parallel "tool filesystem" interface would invite divergence and double the surface that bashkit/OpenShell drivers need to satisfy.

## Decision

Add five built-in file tools — **Read, Write, Edit, Glob, Grep** — implemented in each of Python, TypeScript, PHP, and Go, with these load-bearing choices:

**1. Mirror Claude Code's parameter names and observable behaviour.**

Parameter names (`file_path`, `old_string`, `new_string`, `replace_all`, `output_mode`, `glob`, `type`, `case_insensitive`, `head_limit`, `multiline`, `context_before`, `context_after`) match Claude Code exactly. Observable behaviour matches closely: `cat -n` style line-numbered Read output, Edit's "old_string must be unique unless replace_all" rule, Glob's mtime-descending sort, Grep's `files_with_matches` / `count` / `content` output modes. Cross-language differences are kept to the regex engine (Python `re`, JS `RegExp`, PCRE, Go RE2) and are documented as a portability caveat in the guide.

**2. Delegate to the existing `FilesystemDriver` — no parallel FS interface.**

The five tools call only the methods already on `FilesystemDriver`: `read`, `write`, `exists`, `is_dir`, `listdir`, `stat`, plus the new `mtime` field on `stat` (see point 3). Recursive walks for `**` patterns and Grep traversal happen *in the tool layer* via repeated `listdir` + `is_dir` calls, not as a new driver method. This keeps the driver contract minimal: bashkit, OpenShell, and any future driver get the file tools for free as long as they satisfy the existing interface.

**3. Add `mtime` to `stat`, populated from a per-instance monotonic counter.**

`Glob` sorts by mtime descending; `stat` previously returned only `path`/`type`/`size`. The smallest viable change was to add a single `mtime` field across all four `VirtualFS` implementations, populated by an in-process counter that increments on every `write`/`writeLazy`. Real wall-clock time was rejected for two reasons: (a) tests that write multiple files in sequence would tie at the same `time.time()`, requiring artificial sleeps; (b) a counter is deterministic and doesn't depend on system time. Drivers that wrap real filesystems can populate `mtime` from `os.stat().st_mtime_ns` if they want — Glob only reads the field for ordering and accepts zero as a fallback.

**4. Read-before-Edit enforcement is opt-in.**

Claude Code rejects Edits to files the agent has not Read in the current session. The harness ships this as opt-in (`enforce_read_gate=True` / `{ enforceReadGate: true }` / `enforceReadGate: true` / `filetools.EnforceReadGate()`), default off. The read-set lives on the registration helper's closure or instance — no change to the agent or the driver. When a session compacts and the model forgets it Read a file, it just Reads again; persisting the set across compaction is out of scope.

**5. Errors flow through the existing `{"error": "..."}` envelope.**

Tool handlers raise/return errors using each language's idiom; the existing `executeTool` / `_execute_tool` / `Registry.Execute` wrapper converts them to JSON. No new error machinery. Specific error wording (`"String to replace not found in file"`, `"Found N matches of the string to replace, but replace_all is false"`, `"File has not been read yet. Read it first before writing to it."`) matches Claude Code where the wording carries information.

## Consequences

- Each `VirtualFS` implementation gained a `_mtimes` map and a monotonic counter; `stat()` now returns a `mtime` field. Existing stat tests use field-by-field assertions, so the change is non-breaking.
- A single registration helper per language adds all five tools at once. The tools appear on `agent.tools` / `getTools()` / `Registry.List()` like any other tool — they participate in `TOOL_REGISTER` / `TOOL_CALL` / `TOOL_RESULT` / `TOOL_ERROR` events automatically.
- Glob's `**` semantics and Grep's traversal walk through `listdir` + `is_dir`, not native FS recursion. For typical agent payloads (a few hundred files in the VFS) this is fast. Drivers that want native recursive search can later add an optional `search()` method as a follow-up; the file tools would prefer it when present and fall back to the walker otherwise.
- Agents that want to ban file writes can simply skip `register_file_tools` and provide a Read-only subset, or wrap the helper to register only Read+Glob+Grep.
- Skill content authored against Claude Code's tools (e.g. step-by-step instructions that say "Read the file, then Edit X to Y") works against this harness without editing.
- Each language's regex engine handles Grep — patterns that exercise engine-specific features (lookbehind, possessive quantifiers, recursive groups) are not portable across language implementations of the harness. The portable subset is documented in the guide.

## Alternatives considered

- **Define a separate `ToolFilesystem` interface.** Rejected: doubles the surface drivers must implement and invites the shell-FS and tool-FS to drift. The existing driver already exposes everything Read/Write/Edit/Glob/Grep need.
- **Add `glob` and `grep` methods to the driver.** Tempting (drivers like bashkit could shell out to `find`/`rg` natively), but adds breaking surface to every driver and most drivers wouldn't have a faster implementation than the tool-side walker. Defer until there's a demonstrated need; expose an optional capability rather than a required one.
- **Skip Read-before-Edit entirely.** Rejected because the rule is observable behaviour, and the gate lives in ~30 lines per language with no driver/agent changes — opt-in keeps the friction low for users who don't want it.
- **Use real `time.time()` for mtime.** Rejected because tests that write files in sequence would tie at the same instant, requiring synthetic sleeps. A monotonic counter is deterministic, fast, and sufficient for ordering — which is the only thing Glob needs.

## See also

- [ADR 0016](0016-tool-registration.md) — Tool registration and schema generation
- [ADR 0026](0026-shell-driver-contracts.md) — Shell and filesystem driver contracts
- [ADR 0007](0007-language-idiomatic-implementations.md) — Language-idiomatic implementations
- [File Tools guide](../guides/file-tools.md)
