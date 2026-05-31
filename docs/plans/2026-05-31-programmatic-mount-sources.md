# Plan: Programmatic Mount Sources for the Virtual Filesystem

## Context

Today the VFS can only hold materialised content (`write`) or one-shot per-path lazy
providers (`write_lazy`). Neither can represent a *live directory tree of unknown shape*
sourced from somewhere else — a host folder, a GitHub repo, Slack messages, a DB table —
where `listdir`/`find`/`stat` must enumerate lazily and reads come from the backing source.

We want a small, expressive abstraction that lets host code **mount** programmatically-derived
content as browsable files, so the agent explores it with the same file tools and shell
commands it already knows — no bespoke tool per data source. **Folder mirroring** is the first
concrete source (and the acceptance test); GitHub/Slack/DB sources are future implementations
of the same contract.

Decisions locked with the user:
- **Mutability:** copy-up overlay. Writes under a mount shadow the source in the in-memory
  layer (host source never touched). Deleting a source-only path **raises** (read-only); no
  whiteout/tombstone state in v1.
- **Scope:** builtin in-process driver only. Remote-driver (bashkit/OpenShell) mount sync is
  explicitly **out of scope** for v1 and documented as a limitation.

## Design

Two new contracts + one wrapper + one capability interface. The core `FilesystemDriver`
contract is **not** widened.

### 1. `MountSource` (read-only provider, relative subpaths)
Three methods, scoped to a path *relative* to the mount point (`""` = mount root):
- `stat(subpath) -> MountStat | null` — `null`/`None`/`(_, false)` means "not found" (existence
  is checked constantly; no exceptions for control flow). `MountStat { isDir: bool, size?, mtime? }`.
- `list(subpath) -> string[]` — immediate child **names** of a directory subpath.
- `read(subpath) -> content` — file content (`str|bytes` Py, `string` TS/PHP, `[]byte` Go).

`read`/`list` may throw on missing (only called after `stat` confirms existence). Go uses the
comma-ok idiom `Stat(subpath) (MountStat, bool)`; `List`/`Read` return `error` for real I/O faults.

### 2. `MountingFilesystemDriver` (implements `FilesystemDriver`)
Wraps an inner writable `FilesystemDriver` (the existing builtin/VFS) + an ordered mount table
`(mountPoint -> MountSource)`. Modeled on the existing `DirtyTrackingFS` wrapper.

Routing helper `resolveMount(path) -> (mountPoint, source, subpath) | None`: normalize, pick the
**longest** matching mount prefix, strip it to a relative `subpath`. Per-method behaviour
(overlay-first / copy-up):
- `write` / `write_lazy` → always `inner` (copy-up; inner shadows source).
- `read` / `read_text` → inner if it has the path, else `source.read(subpath)`, else inner's
  not-found error. (Overlay-first ⇒ edited content wins.)
- `exists` → inner OR `source.stat(subpath) != null` OR path is a mount point / ancestor of one.
- `is_dir` (+ private `_is_dir`/`_isDir` alias, see wiring) → inner OR mount-point/ancestor OR
  `source.stat(subpath).isDir`.
- `listdir` → **merge + dedup, sorted**: `inner.listdir` ∪ `source.list(subpath)` (when subpath
  is a source dir) ∪ mount-point first-segments rooted at this dir (so `ls /` shows `repo` when
  `/repo` is mounted).
- `stat` → inner; else synthesised dir for a mount-point/ancestor; else map `source.stat` →
  the port's stat dict/FileInfo (default size/mtime 0 when omitted); else not-found.
- `find` → recursive walk over the **merged** `listdir`+`is_dir`, basename-match like
  `VirtualFS.find`. Implemented in the driver (the shell's `find`/`grep -r` call `fs.find`
  directly, so it must return merged results).
- `remove` → if inner has it, `inner.remove` (un-shadow the copy-up); if only in a source,
  **raise** read-only/not-found (locked decision — no tombstone).
- `clone` → `inner.clone()`; **copy the mount table by reference** (sources are read-only,
  shareable — consistent with existing lazy-provider clone semantics).
- Go only: guard the mount slice with a `sync.RWMutex` (RLock reads, Lock mount/unmount); inner
  is already thread-safe. Python/TS/PHP single-threaded, no lock (matches `VirtualFS`).
- Empty mount table ⇒ every method is a transparent passthrough to inner (zero behavioural drift).

### 3. `Mountable` capability interface (NOT in core contract)
`mount(mountPoint, source)`, `unmount(mountPoint)`, `mounts() -> string[]`. Implemented only by
`MountingFilesystemDriver`. Host code check-and-calls: Python `isinstance`/`Protocol`, TS type
guard, PHP `instanceof`, Go `fs.(vfs.Mountable)`. Honors ADR 0026 (minimal core) and ADR 0031
(Go narrow capability interfaces).

### 4. `LocalFolderSource` (first concrete source)
Mirrors a host directory, read-only, path-escape-safe. `stat`/`list`/`read` map onto host fs
(`os.scandir`/`statSync`+`readdirSync`/`scandir`/`os.ReadDir` etc.). **Confinement:** resolve the
real absolute root once; for every `subpath`, lexically normalize, join, resolve, and assert the
result is `root` or under `root + sep` (defeats `../` and symlink escapes). Reject absolute
subpaths. Out-of-root ⇒ treated as not-found.

## Load-bearing wiring fact (verified)

The mount layer must be visible to **both** the file tools (which go through `agent.fs` /
`FilesystemDriver`) **and** the shell builtins (`ls`/`cat`/`grep`/`find`). These use different fs
objects in 3 of 4 ports:
- **Go** (`src/go/shell/builtin/driver.go:170,188`): shell evaluator and `FS()` share one
  `vfs.FilesystemDriver`. Clean — install via `WithFS(mountingDriver)`; nothing else.
- **Python** (`src/python/shell.py:667`) & **TS** (`src/typescript/shell.ts:708`): `Shell.fs` is a
  concrete `VirtualFS` and the shell reaches into **private** `_is_dir`/`_isDir`
  (`shell.py:1504`, `shell.ts:1592`). The wrapper must expose that private alias and be installable
  as `Shell.fs`; the tools' driver must point at the **same** wrapper instance.
- **PHP** (`src/php/Shell.php:743`): `public VirtualFS $fs`, concretely typed, but uses only the
  **public** `isDir`. Needs a shared `FilesystemLike` interface (both `VirtualFS` and the wrapper
  implement it) so the property type can widen. Most invasive single edit; do PHP last.

v1 keeps the localized fix (private alias + `Shell.fs` widening). Unifying the Python/TS/PHP shells
onto `FilesystemDriver` (as Go already is) is the cleaner long-term refactor — **out of scope**.

## Exposure / wiring: lazy upgrade on first mount

Zero overhead on the no-mount path. Until `mount()` is called, the fs is the plain builtin
driver/VFS exactly as today. On first `mount(point, source)`: wrap the current writable fs in a
`MountingFilesystemDriver`, re-seat it as **both** the shell's `fs` and the tools' driver instance,
then register the source. Subsequent mounts just append to the table.

Expose `mount`/`unmount`/`mounts` on the agent's shell mixin (`HasShell` / `Host`). Optional
follow-up: `AgentBuilder.mount(...)` convenience (not v1-blocking).

## Rollout order

**Python → Go → TS → PHP.** Python is the reference spec (loosest typing, cheapest place to
discover the wiring seam). Go second validates the *clean* design (shared interface, no
duck-typing) and de-risks parity early. PHP last (strictest typing, most invasive `Shell.fs` edit;
benefits from three settled references). Within each language land **contract + wrapper + source**
together (so typed ports compile), then wiring, then tests.

### File-by-file

**Python** (`src/python/`)
- NEW `mount.py` — `MountSource` (ABC), `MountStat`, `MountingFilesystemDriver(FilesystemDriver)`
  with `_is_dir` alias, `Mountable` (runtime-checkable `Protocol`).
- NEW `mount_sources.py` — `LocalFolderSource(MountSource)`.
- MODIFY `has_shell.py` — `mount`/`unmount`/`mounts` with lazy upgrade + re-seat `shell.fs`.
- MODIFY `shell.py` — widen the `fs` annotation (duck-typed; likely no logic change once the
  wrapper exposes `_is_dir`).

**Go** (`src/go/shell/`)
- NEW `vfs/mount.go` — `MountSource` & `MountStat`, `MountingFilesystemDriver` (mirrors `dirty.go`,
  `sync.RWMutex`), `Mountable` interface.
- NEW `vfs/local_folder_source.go` — `LocalFolderSource`.
- MODIFY `builtin/driver.go` — small convenience (`WithMounts` or document `WithFS`); mounting
  flows through the existing interface seam.
- MODIFY host/agent wiring — `Mount`/`Unmount`/`Mounts` passthrough that asserts
  `FS().(vfs.Mountable)`, lazily wrapping and re-seating `d.fs`/`d.eval.FS`.

**TypeScript** (`src/typescript/`)
- NEW `mount.ts` — `MountSource`, `MountStat`, `MountingFilesystemDriver implements FilesystemDriver`
  with `_isDir` alias, `Mountable` + `isMountable` type guard.
- NEW `mount-sources.ts` — `LocalFolderSource` (`node:fs`).
- MODIFY `index.ts` — export new types.
- MODIFY `has-shell.ts` — lazy-upgrade `mount`/`unmount`/`mounts`.
- MODIFY `shell.ts` — widen `fs` field to a small `FilesystemLike` structural type (incl. `_isDir`).

**PHP** (`src/php/`)
- NEW `MountSource.php`, `MountStat.php`, `MountingFilesystemDriver.php` (implements
  `FilesystemDriver`), `LocalFolderSource.php`, `Mountable.php`.
- NEW `FilesystemLike.php` (or reuse `FilesystemDriver`) — narrow shape `VirtualFS` + wrapper share.
- MODIFY `Shell.php` — widen `public VirtualFS $fs` → `FilesystemLike` (the one unavoidable
  signature edit).
- MODIFY `HasShell.php` — `mount`/`unmount`/`mounts`.

**Docs / ADR** (once)
- NEW `docs/adr/0034-programmatic-mount-sources.md` (Status/Context/Decision/Consequences) —
  records the source/overlay split, copy-up + raise-on-source-delete, `Mountable` capability,
  builtin-only v1 scope, clone-by-reference, golden-fixture exclusion.
- MODIFY `docs/adr/README.md` — index row for 0034.
- MODIFY `docs/guides/virtual-fs.md` — new `## Mounts` section after "External drivers and sync".
- MODIFY `docs/guides/file-tools.md` — note Glob/Grep/Read transparently see mounted content.

## Testing

Three layers, **mirrored by name across all four suites** (parity is enforced by matching test
cases, not the golden JSON — host temp paths are non-deterministic, so mounts are deliberately
excluded from `src/go/shell/builtin/testdata/golden.json`; note this in the ADR).

1. **Wrapper unit** (in-memory inner + fake/local source): longest-prefix routing; `listdir`
   merge+dedup; copy-up shadowing (read→write→read returns edited, listed once); `remove` of
   copied-up vs source-only (raises); path-escape rejection; `clone` (writable diverges, source
   shared); empty-table passthrough byte-identical to inner.
2. **`LocalFolderSource` vs real temp dir**: nested files reflected via `stat`/`list`/`read`;
   `../`/symlink cannot escape root. Temp dirs: `tmp_path` (pytest) / `mkdtempSync` (vitest) /
   `sys_get_temp_dir` (PHPUnit) / `t.TempDir()` (Go).
3. **End-to-end acceptance** (one per language): mount a temp dir at `/work`; assert **file tools**
   (`Glob("/work/**/*.txt")`, `Grep`, `Read`) AND **shell** (`ls /work`, `cat`, `grep`,
   `find /work -name '*.md'`) both see content; then the key assertion: `echo edited >
   /work/hello.txt` → `cat` returns "edited" **and the host file is unchanged** (proves copy-up +
   read-only source at once). Go adds a `builtin/` E2E to exercise the shell seam.

## Verification

- Python: `uv run pytest tests/python -q` (esp. `test_virtual_fs.py`, file-tools, has-shell).
- Go: `cd src/go && go test ./shell/... -race` and `go test ./shell/builtin/ -run Golden`
  (golden must still pass, unchanged).
- TS: `npx vitest run` + `npx tsc --noEmit` (validates the widened `Shell.fs` type).
- PHP: `composer exec phpunit` (full suite).
- Cross-language: confirm the mirrored test-case names exist in all four suites; spot-check the
  "edit a mounted file via shell, host unchanged" E2E gives identical expected output everywhere.

## Out of scope (v1)

Whiteout/tombstone deletes; writing back to sources; GitHub/Slack/DB sources (contract admits them
later); remote bashkit/OpenShell mount sync; unifying Python/TS/PHP shells onto `FilesystemDriver`.
