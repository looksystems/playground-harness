# Virtual Filesystem (VFS)

The harness gives every agent an in-memory filesystem. You "mount" context into it — schemas, data dumps, source files — and the agent explores it with the [file tools](file-tools.md) (Read/Write/Edit/Glob/Grep) and the [shell](virtual-bash-reference.md) (`cat`, `grep`, `find`, `jq`). This is the concrete realisation of the project's core principle: *don't build a tool per query pattern — mount context as files and let the model use Unix commands it already knows* ([principles.md](../principles.md)).

This guide covers what the VFS is, how you put files into it, and how it works underneath. For the contract it satisfies, see [ADR 0026 — Shell and Filesystem Driver Contracts](../adr/0026-shell-driver-contracts.md).

## Where it sits

```
 file tools (Read/Write/Edit/Glob/Grep)        shell (exec / Bash)
                    \                            /
                     v                          v
              ┌───────────────────────────────────────┐
              │           FilesystemDriver             │   ← the contract (ADR 0026)
              └───────────────────────────────────────┘
                     |                |               |
                builtin VFS       bashkit         OpenShell
              (this document)   (subprocess)       (remote)
```

There is **one** filesystem per agent and **two** surfaces onto it. The file tools and the shell both call the same `FilesystemDriver`, so anything you write is visible to both — there is no second filesystem to keep in sync. Swapping the shell backend (builtin / bashkit / OpenShell) swaps the driver, and the tools follow automatically.

`agent.fs` returns the `FilesystemDriver`, not a concrete `VirtualFS`. For the default ("builtin") driver that driver wraps a `VirtualFS`; for external drivers it wraps a dirty-tracking shim over one (see [External drivers](#external-drivers-and-sync)).

## Mounting files

There is no separate "mount" API — you seed the filesystem by writing to it. Three entry points:

### 1. Seed at construction

The `VirtualFS` constructor takes a `path → content` map:

```python
fs = VirtualFS({"/schema/users.yaml": schema, "/data/users.json": dump})
```
```typescript
const fs = new VirtualFS({ "/schema/users.yaml": schema });
```
```php
$fs = new VirtualFS(['/schema/users.yaml' => $schema]);
```
```go
fs := vfs.New(map[string][]byte{"/schema/users.yaml": []byte(schema)})
```

### 2. Write onto a live agent

After the agent exists, reach its filesystem through `agent.fs` and write:

```python
agent.fs.write("/data/schema.yaml", schema_content)
await agent.run("What tables reference user_id?")
```
```typescript
agent.fs.write("/data/users.json", JSON.stringify(users));
```
```php
$agent->fs()->write('/data/users.json', json_encode($users));
```
```go
agent.Host.Driver.FS().WriteString("/data/users.json", string(dump))
```

### 3. Lazy mount (deferred / expensive content)

Register a provider instead of content; it runs **once** on first read, then the result is cached as an ordinary file. Use it when the content is expensive to compute or may never be read.

```python
agent.fs.write_lazy("/report.json", lambda: expensive_dump())
```
```typescript
agent.fs.writeLazy("/report.json", () => expensiveDump());
```
```php
$agent->fs()->writeLazy('/report.json', fn () => expensiveDump());
```
```go
agent.Host.Driver.FS().WriteLazy("/report.json", func() ([]byte, error) {
    return expensiveDump(), nil
})
```

A lazy path reports as existing (`exists`, `listdir`, `find`, `stat`) before it is resolved — `stat` resolves it to compute size.

## Shared baseline vs. per-agent

To mount common context once and still isolate per-agent writes, register a pre-seeded shell in the registry. Each agent that names it gets its **own clone**, so writes never cross between agents:

```python
ShellRegistry.register("data-explorer", Shell(
    fs=VirtualFS({"/schema/users.yaml": schema}),     # shared baseline
    allowed_commands={"cat", "grep", "find", "ls", "jq", "head", "tail", "wc"},
))

agent = MyAgent(model="...", shell="data-explorer")
agent.fs.write("/data/results.json", results)          # only THIS agent sees it
```

Everything in the registered template is the shared baseline; everything an agent writes afterward lands in its private clone. The isolation comes from `clone()` being called per agent — see [clone semantics](#clone-semantics) for exactly what is and isn't deep-copied.

## How it works

The builtin VFS is a dict-backed store. All four language ports share the same model; the Go port is the reference for concurrency.

### Paths

Every path is normalised to **absolute, cleaned, forward-slash** form on the way in — `write`, `read`, `exists`, etc. all normalise their argument first. Relative paths are made absolute by prefixing `/`; `.` and `..` segments are resolved; duplicate slashes collapse. So `foo/../bar`, `/bar`, and `//bar` all address `/bar`. There is no per-agent "current directory" inside the VFS — `cwd` lives on the *shell* and on the file tools' `cwdFn`, not on the filesystem.

### Directories are implicit

The VFS stores **files only**. There is no directory table and no such thing as an empty directory. A path is a directory iff some stored path has it as a `/`-terminated prefix:

```
write("/a/b/c.txt")  ⇒  "/a" is a dir, "/a/b" is a dir, "/a/b/c.txt" is a file
remove("/a/b/c.txt") ⇒  "/a" and "/a/b" silently cease to exist
```

`listdir` derives immediate children by scanning all paths under a prefix and taking the first segment; `is_dir` is a prefix scan. This is why a shell `mkdir` of an empty directory does not persist as a VFS entry on its own — the directory only "exists" once it contains a file.

### Internal state

Each VFS instance holds four pieces of state:

| Field | Purpose |
|-------|---------|
| `files` | `path → content` for materialised files |
| `lazy` | `path → provider` for unresolved lazy files |
| `mtimes` | `path → monotonic counter value` |
| `mtimeCounter` | per-instance counter, incremented on every `write` / `writeLazy` |

**mtime is a logical clock, not a wall clock.** Every write bumps the per-instance counter and stamps the path with the new value. `stat` exposes it as `mtime`. Its one consumer is the **Glob tool's "newest first" ordering** — Glob sorts results by this counter descending. Backends that don't track mtime return `0`, and Glob falls back to a stable sort. Because it's a counter, it is deterministic across runs (no `Date.now()`), which keeps Glob ordering reproducible in tests.

### Lazy resolution

`read` checks `lazy` first: if the path is an unresolved provider, it calls the provider, stores the result in `files`, drops the `lazy` entry, and returns the content. Every subsequent read hits the materialised file. The provider runs at most once per VFS instance.

### clone semantics

`clone()` produces an **independent** filesystem — the mechanism behind per-agent isolation:

- **Real file contents are deep-copied** (Python `deepcopy`; Go copies each byte slice; TS copies map entries — strings are immutable; PHP relies on copy-on-write). Writes in one clone never affect the other.
- **Lazy providers are shared by reference**, but each clone gets its own `lazy` dict. If two clones both read the same still-unresolved lazy path, the provider runs **once per clone** — so a provider with side effects, or one whose result varies, can produce different content in each clone. Mount via `write` (eager) if you need both clones to see identical bytes.
- `mtimes` and the counter value are copied, so Glob ordering is preserved across the clone.

### Concurrency

Only the **Go** VFS is mutex-guarded and safe for concurrent goroutines (it uses a `sync.RWMutex` and re-checks lazy state under the write lock). The Python, TypeScript, and PHP ports are **not** thread-safe — they assume the single-threaded / async-cooperative execution model of their runtimes. Don't share one Python/TS/PHP VFS across OS threads.

## External drivers and sync

When the shell backend runs commands in a *different process* (bashkit subprocess) or on a *different host* (OpenShell), the in-memory VFS is still the source of truth. ADR 0026 calls this **"host owns, sync before/after exec."**

The driver wraps the VFS in a **dirty-tracking shim** (`DirtyTrackingFS`): writes and removes pass through to the inner VFS *and* record the path in a dirty set; reads are pass-through and don't dirty anything. Around each `exec` the driver:

1. **Preamble** — for each dirty path, emits shell commands that recreate it in the remote environment (`mkdir -p` + base64-decode the content into place), or `rm -f` for deletions, then clears the dirty set.
2. Runs the user's command.
3. **Epilogue** — appends a script that preserves the command's exit code, then lists every file under the sync root as base64 (`===FILE:path===` delimited).
4. **Sync-back** — parses that listing and diffs it against the VFS: new files are added, changed files updated, files that vanished remotely are removed.

The practical consequence: with an external driver, **mount through `agent.fs`** (the host side) so the change is dirty-tracked and pushed on the next `exec`. Writing files inside the sandbox out-of-band risks being overwritten by the next preamble sync. With the builtin driver there is only one filesystem, so this is moot.

## Mounts (programmatic sources)

The seeding APIs above hold *materialised* content. To expose a **live directory tree of unknown shape** — a host folder, a GitHub repo, Slack messages, a DB table — mount a **`MountSource`** instead. The agent then browses it with the same file tools and shell commands (`ls`, `cat`, `grep`, `find`, `Read`, `Glob`, `Grep`); there is no bespoke tool per data source. See [ADR 0034](../adr/0034-programmatic-mount-sources.md).

A `MountSource` is a read-only provider with three methods, scoped to a path *relative* to the mount point (`""` is the mount root):

- `stat(subpath)` → a small stat (`isDir`, optional `size`/`mtime`) or **not-found** (`null`/`None`/`(_, false)`). Existence is checked constantly, so this never throws for control flow.
- `list(subpath)` → immediate child names of a directory subpath.
- `read(subpath)` → file content.

Mount one through the agent's shell mixin. The first mount lazily wraps the writable fs in a `MountingFilesystemDriver` and re-seats it as **both** the shell's filesystem and the file tools' driver, so both see the mount; there is zero overhead until you mount:

```python
from src.python.mount_sources import LocalFolderSource

agent.mount_source("/work", LocalFolderSource("/path/on/host"))
agent.exec("cat /work/hello.txt")        # reads from the host folder
agent.mount_sources()                    # ["/work"]
agent.unmount_source("/work")
```

`LocalFolderSource` is the first concrete source — it mirrors a host directory, read-only and path-escape-safe (`../` and symlink escapes are rejected; out-of-root paths read as not-found).

**Copy-up overlay (mutability).** Writes under a mount go to the in-memory layer and **shadow** the source — the host source is never touched. So `echo edited > /work/hello.txt` makes `cat` return `edited` while the host file stays unchanged; `rm /work/hello.txt` then un-shadows it and the original source content reappears. Deleting a **source-only** path raises (the source is read-only; there is no whiteout/tombstone in v1).

**Scope (v1).** Mounts are supported on the **builtin driver only**. Remote drivers (bashkit/OpenShell) do not sync mounts — `mount_source` raises (Go `Mount` returns an error) on a non-builtin driver.

| Method | Python / TS / PHP | Go |
|--------|-------------------|----|
| Mount a source | `mount_source` / `mountSource` / `mountSource` | `Mount` |
| Unmount | `unmount_source` / `unmountSource` / `unmountSource` | `Unmount` |
| List mount points | `mount_sources` / `mountSources` / `mountSources` | `Mounts` |
| First concrete source | `LocalFolderSource` | `vfs.NewLocalFolderSource` |

The verbose names in the dynamic ports avoid colliding with `HasSkills`'s `mount`/`unmount`; Go's skills subsystem already uses `MountSkill`, leaving the plain names free.

## Behaviour reference

All methods normalise their path argument first. Names below use the Python spelling; see the [cross-language table](#cross-language-surface) for the others.

| Method | Behaviour |
|--------|-----------|
| `write(path, content)` | Store/overwrite a file. Stamps mtime. |
| `write_lazy(path, provider)` | Register a provider resolved on first read. Stamps mtime. |
| `read(path)` | Return content; resolves a lazy provider on first access. Raises if the path is not a file. |
| `read_text(path)` | `read` decoded to text (Python decodes bytes; PHP is an alias; TS/Go have `ReadString`). |
| `exists(path)` | True for a real file, an unresolved lazy file, or an implicit directory. |
| `remove(path)` | Delete a file or lazy entry. Raises on a missing path (`FileNotFoundError` / `Error` / `RuntimeException` / `fs.ErrNotExist`); the shell's `rm` swallows this for `rm -f` semantics. |
| `is_dir(path)` | True iff some stored path has `path + "/"` as a prefix. |
| `listdir(path="/")` | Immediate child names (no trailing slash), sorted. |
| `find(root="/", pattern="*")` | Paths under `root` whose **basename** matches the glob (single-level `*`/`?`). Sorted. |
| `stat(path)` | `{path, type}` for a dir; `{path, type, size, mtime}` for a file (resolves lazy to size it). |
| `clone()` | Independent copy — see [clone semantics](#clone-semantics). |

**`find` vs. the Glob tool.** This driver `find` matches a glob against the *basename* only — it is a single-level primitive. The recursive `**` walk the Glob *tool* performs is implemented in the tool layer (via `listdir` + `is_dir`), not here — see [file-tools.md](file-tools.md). Don't reach for `find` expecting `**` semantics.

## Cross-language surface

| Aspect | Python | TypeScript | PHP | Go |
|--------|--------|------------|-----|----|
| Module | `src/python/virtual_fs.py` | `src/typescript/virtual-fs.ts` | `src/php/VirtualFS.php` | `src/go/shell/vfs/vfs.go` |
| Content type | `str \| bytes` | `string` only | `string` only | `[]byte` |
| Write | `write` | `write` | `write` | `Write` / `WriteString` |
| Read-as-text | `read_text` | `read` returns string | `readText` (alias) | `ReadString` |
| Clone | `clone` | `clone` | `cloneFs` | `Clone` |
| Seed constructor | `VirtualFS(files)` | `new VirtualFS(files)` | `new VirtualFS($files)` | `vfs.New(files)` |
| Missing-read error | `FileNotFoundError` | `Error` | `RuntimeException` | `fs.ErrNotExist` |
| Thread-safe | No | No | No | **Yes** (`sync.RWMutex`) |
| Driver wrapper | `BuiltinFilesystemDriver` | (driver layer) | (driver layer) | `BuiltinFilesystemDriver` |

## Known limitations and divergences

- **No empty directories.** Directories are inferred from file paths; an empty directory cannot exist. A shell `mkdir` of an empty dir doesn't leave a standalone VFS entry.
- **`remove` on a missing path raises** in all four ports. Use the shell's `rm` (or guard with `exists`) when you want `rm -f`-style tolerance.
- **`write` supersedes a pending lazy provider** in all four ports — writing over a path you previously `write_lazy`'d clears the provider, so the next `read` returns the written bytes.
- **mtime is a logical counter, not wall-clock time.** Only meaningful for relative ordering within one VFS instance (Glob's "newest first"). It does not survive serialisation as a timestamp and is not comparable across instances.
- **Content is held in memory.** The VFS is not backed by host disk. Large corpora live entirely in RAM; there is no streaming/paging.
- **Not a POSIX filesystem.** No permissions, symlinks, hard links, devices, or inodes. Path normalisation resolves `..` lexically, with no symlink awareness.
- **Mounts are builtin-driver-only and source-read-only.** Remote drivers don't sync mounts; deleting a source-only path raises (copy-up shadowing only, no whiteout). See [Mounts](#mounts-programmatic-sources).

## See also

- [File Tools](file-tools.md) — Read/Write/Edit/Glob/Grep, the agent-facing surface over this filesystem
- [Virtual Bash Reference](virtual-bash-reference.md) — the shell surface over the same filesystem
- [Why Virtual Bash](why-virtual-bash.md) — why a single exec tool over a semantic filesystem beats bespoke tools
- [Bashkit](bashkit.md) · [OpenShell](openshell.md) — external drivers that sync against this VFS
- [ADR 0026 — Shell and Filesystem Driver Contracts](../adr/0026-shell-driver-contracts.md)
- [Python](python.md#virtual-shell) · [TypeScript](typescript.md) · [PHP](php.md) · [Go](go.md#shell) guides
