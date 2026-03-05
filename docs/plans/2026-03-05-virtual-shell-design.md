# Virtual Shell Support

## Overview

Add a virtual filesystem, shell interpreter, and agent mixin to the harness as first-class components across all three languages (Python, TypeScript, PHP).

Inspired by [vercel-labs/just-bash](https://github.com/vercel-labs/just-bash): instead of building many specialized tools, mount context as files and give the agent a single `exec` tool over an in-memory filesystem. The model explores with `grep`, `cat`, `find`, `jq` — commands it already understands deeply.

## Components

### VirtualFS (standalone)

In-memory filesystem. Flat key-value store — key is absolute normalized path, value is content. Directories are inferred by prefix scanning.

**API:**
- `write(path, content)` / `read(path)` / `read_text(path)`
- `write_lazy(path, provider)` — provider called on first read, then cached
- `exists(path)` / `remove(path)` / `stat(path)`
- `listdir(path)` / `find(root, pattern)`
- `clone()` — deep copy of all files and pending lazy providers

Python supports `str | bytes` content. TypeScript and PHP are string-only.

### Shell (standalone)

Lightweight command interpreter over a VirtualFS. Pure emulation — no real shell invocation. Every command is a function in the host language operating on the in-memory VirtualFS.

**API:**
- `exec(command) -> ExecResult` — parse and execute, supports pipes/redirects/chaining
- `cwd` — current working directory
- `env` — environment variables for `$VAR` expansion
- `fs` — the underlying VirtualFS
- `clone()` — deep copy (clones VirtualFS, cwd, env, allowed_commands)

**23 commands:** `cat`, `cd`, `cp`, `cut`, `echo`, `find`, `grep`, `head`, `jq`, `ls`, `mkdir`, `pwd`, `rm`, `sed`, `sort`, `stat`, `tail`, `tee`, `touch`, `tr`, `tree`, `uniq`, `wc`

**Configuration:**
- `allowed_commands: set[str] | None` — command allowlist (None = all)
- `max_output: int` — truncation limit (default 16,000 chars)
- `max_iterations: int` — loop safety (default 10,000)

### ShellRegistry (global singleton)

Named shell configurations that serve as templates. Agents receive clones, not references.

**API:**
- `ShellRegistry.register(name, shell)` — register a named shell
- `ShellRegistry.get(name) -> Shell` — returns a **clone** of the named shell
- `ShellRegistry.has(name) -> bool`
- `ShellRegistry.remove(name)`
- `ShellRegistry.reset()` — clear all (for testing)

### HasShell (mixin)

Convenience layer that wires Shell into the agent. Works independently of `UsesTools` but auto-registers the `exec` tool when both are composed.

**Provides:**
- `agent.fs` — shortcut to `agent.shell.fs`
- `agent.shell` — the Shell instance
- `agent.exec(command)` — programmatic shell execution

**Configuration (constructor params):**
- `shell: str | Shell | None` — a registry name, a Shell instance, or None (creates default)
- `cwd`, `env`, `allowed_commands` — passed through if creating a new Shell

**Behavior:**
- If `shell` is a string, clones the named shell from the registry
- If `shell` is a Shell instance, uses it directly
- If `None`, creates a default Shell
- If `UsesTools` is composed, auto-registers the `exec` tool
- Appends shell usage instructions to system prompt via `_build_system_prompt`

## Security Model

All implementations are pure emulation. No real shell is ever invoked.

- **No process spawning** — no `subprocess`, `child_process`, `exec()`, `proc_open`
- **No real filesystem access** — all reads/writes go to an in-memory dict/Map
- **No network access** — no HTTP calls, sockets, or fetch
- **No code execution** — no eval, awk, or function definition support
- **Output truncation** — configurable `max_output` prevents context flooding
- **Command allowlisting** — restrict available commands per Shell instance

## Usage Examples

### Standalone (no agent)

```python
fs = VirtualFS()
fs.write("/data/users.json", json.dumps(users))
shell = Shell(fs)
result = shell.exec("cat /data/users.json | jq '.[].name' | sort")
print(result.stdout)
```

### With agent via mixin

```python
class MyAgent(BaseAgent, UsesTools, HasShell):
    pass

agent = MyAgent(model="anthropic/claude-sonnet-4-20250514", shell="data-explorer")
agent.fs.write("/data/results.json", results)
response = await agent.run("How many active users are there?")
```

### Registry for shared configurations

```python
ShellRegistry.register("data-explorer", Shell(
    fs=VirtualFS({"/schema/users.yaml": schema}),
    allowed_commands={"cat", "grep", "find", "ls", "jq", "head", "tail", "wc"},
    cwd="/schema",
))

# Each agent gets its own clone
shell = ShellRegistry.get("data-explorer")
shell.fs.write("/data/query.json", my_data)  # only this clone sees this
```

## File Layout

```
src/python/
  virtual_fs.py          # VirtualFS
  shell.py               # Shell, ExecResult, ShellRegistry
  has_shell.py           # HasShell mixin
  standard_agent.py      # updated to include HasShell

src/typescript/
  virtual-fs.ts          # VirtualFS
  shell.ts               # Shell, ExecResult, ShellRegistry
  has-shell.ts           # HasShell mixin
  standard-agent.ts      # updated to include HasShell

src/php/
  VirtualFS.php          # VirtualFS
  Shell.php              # Shell, ExecResult, ShellRegistry
  HasShell.php           # HasShell trait
  StandardAgent.php      # updated to include HasShell
```

## Future Hardening (deferred)

The following are documented as requirements for future iterations:

- **Readonly mode** — `readonly: bool` flag preventing writes to the VFS
- **Size limits** — cap total VFS storage (sum of all values) to prevent memory exhaustion
- **Path jailing** — restrict writes to specific prefixes (e.g., `/tmp`, `/workspace`) while keeping everything else read-only, similar to just-bash's OverlayFs pattern
- **Per-command timeouts** — prevent expensive regex in grep/sed from blocking
- **Audit logging** — record all commands executed for observability and debugging
