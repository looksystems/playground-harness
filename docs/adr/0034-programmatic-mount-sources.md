# ADR 0034: Programmatic Mount Sources

## Status

Accepted

## Context

The virtual filesystem could hold only materialised content (`write`) or one-shot
per-path lazy providers (`write_lazy`). Neither can represent a *live directory tree of
unknown shape* sourced from somewhere else — a host folder, a GitHub repo, Slack
messages, a DB table — where `listdir`/`find`/`stat` must enumerate lazily and reads come
from the backing source.

We want host code to **mount** programmatically-derived content as browsable files so the
agent explores it with the same file tools and shell commands it already knows (`ls`,
`cat`, `grep`, `find`, `Read`, `Glob`, `Grep`) — no bespoke tool per data source. Folder
mirroring is the first concrete source and the acceptance test; GitHub/Slack/DB sources
are future implementations of the same contract.

## Decision

Two new contracts + one wrapper + one capability interface. The core `FilesystemDriver`
contract is **not** widened.

**1. `MountSource` — read-only provider, relative subpaths.** Three methods scoped to a
path relative to the mount point (`""` = mount root): `stat(subpath) -> MountStat | null`
(null/None/`(_, false)` means "not found"; existence is checked constantly so it never
throws for control flow), `list(subpath) -> string[]` (immediate child names), and
`read(subpath) -> content`. `list`/`read` may throw on missing (only called after `stat`
confirms existence). Go uses the comma-ok idiom `Stat(subpath) (MountStat, bool)`.

**2. `MountingFilesystemDriver` — implements `FilesystemDriver`.** Wraps an inner writable
driver plus an ordered mount table, modeled on the existing dirty-tracking wrapper. Routing
picks the **longest** matching mount prefix and strips it to a relative subpath. Per-method
behaviour is overlay-first / copy-up:

- `write`/`write_lazy` always go to `inner` (copy-up; inner shadows source).
- `read` returns inner content if present, else the source's, else inner's not-found error
  (edited content wins).
- `exists`/`is_dir` are true for inner hits, mount points and their ancestors, and source
  hits.
- `listdir` merges and dedups (sorted): `inner.listdir` ∪ source children ∪ mount-point
  first-segments rooted at this dir (so `ls /` shows `repo` when `/repo` is mounted).
- `find` walks the merged tree (files only, basename match), so the shell's `find` and
  `grep -r` — which call `fs.find` directly — see merged results.
- `remove` un-shadows a copied-up path via `inner.remove`; removing a **source-only** path
  **raises** (read-only — no whiteout/tombstone in v1).
- `clone` clones the inner writable layer and copies the mount table **by reference**
  (sources are read-only and shareable, consistent with lazy-provider clone semantics).
- An empty mount table makes every method a transparent passthrough to inner (zero
  behavioural drift). Go guards the mount slice with a `sync.RWMutex`; the single-threaded
  ports need no lock.

**3. `Mountable` capability interface — NOT in the core contract.** `mount`/`unmount`/
`mounts`, implemented only by `MountingFilesystemDriver`. Host code check-and-calls
(Python `Protocol`/`isinstance`, TS `isMountable` guard, PHP `instanceof`, Go
`FS().(vfs.Mountable)`). Honors ADR 0026 (minimal core) and ADR 0031 (Go narrow capability
interfaces).

**4. `LocalFolderSource` — first concrete source.** Mirrors a host directory, read-only and
path-escape-safe: the real absolute root is resolved once; every subpath is normalized,
joined, resolved, and asserted to be the root or under `root + sep` (defeating `../` and
symlink escapes). Absolute subpaths are rejected; out-of-root paths are treated as
not-found.

**5. Lazy upgrade on first mount.** Until `mount` is called the fs is the plain builtin
driver/VFS exactly as before. The first mount wraps the current writable fs in a
`MountingFilesystemDriver` and re-seats it as **both** the shell's `fs` (so the builtins
see mounts) and the tools' driver instance (so the file tools see mounts); later mounts
append to the table. Exposed on the agent's shell mixin as `mount_source`/`unmount_source`/
`mount_sources` (Python/TS/PHP) and `Mount`/`Unmount`/`Mounts` (Go). The verbose names in
the dynamic ports avoid colliding with `HasSkills`'s existing `mount`/`unmount`; Go's skills
subsystem already uses `MountSkill`, leaving the plain names free.

## Consequences

- **Scope is builtin-driver-only in v1.** Remote drivers (bashkit/OpenShell) do not sync
  mounts; `mount_source` raises (or `Mount` returns an error) on a non-builtin driver. This
  is documented as a limitation in the Virtual Filesystem guide.
- **Deletes of source-only paths raise.** There is no whiteout/tombstone state; you can
  shadow a source file by writing over it, then un-shadow by removing the copy-up, but you
  cannot make a source file disappear.
- **Per-port wiring seam.** Python/TS/PHP shells hold a concrete `VirtualFS` and reach into
  a private `_isDir`/`_is_dir`; the wrapper exposes that alias and a small `FilesystemLike`
  shape lets the `fs` field widen. Go's shell evaluator and `FS()` already share one
  `FilesystemDriver`, so the re-seat is a single swap. Unifying the dynamic ports onto
  `FilesystemDriver` (as Go already is) is a cleaner long-term refactor, left out of scope.
- **Golden-fixture exclusion.** Mounts use host temp paths that are non-deterministic, so
  they are deliberately excluded from `src/go/shell/builtin/testdata/golden.json`.
  Cross-language parity is enforced by **matching test-case names** across the four suites
  (wrapper unit, `LocalFolderSource` vs a real temp dir, and an end-to-end "edit a mounted
  file via the shell, host file unchanged" acceptance test), not by shared golden JSON.

## Out of scope (v1)

Whiteout/tombstone deletes; writing back to sources; GitHub/Slack/DB sources (the contract
admits them later); remote bashkit/OpenShell mount sync; unifying the Python/TS/PHP shells
onto `FilesystemDriver`.
