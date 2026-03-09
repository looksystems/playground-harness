# Bashkit Integration Guide

## Overview

[bashkit](https://github.com/everruns/bashkit) is a Rust-based sandboxed POSIX shell (IEEE 1003.1-2024) with 100+ builtins, a virtual filesystem, and resource limits. The agent harness integrates with bashkit as an optional shell driver, giving agents a fully POSIX-compliant shell without changing any agent code — just set `driver("bashkit")`.

The integration differs by language:

- **Python** — in-process via `bashkit` PyO3 package. Stateful, with custom command support via `ScriptedTool`.
- **TypeScript / PHP** — subprocess via `bashkit -c`. Stateless one-shot execution.

### Why bashkit?

| | Builtin Shell | bashkit |
|---|---|---|
| **Builtins** | ~30 commands | 100+ POSIX commands |
| **POSIX compliance** | Subset (pipes, redirects, control flow) | Full IEEE 1003.1-2024 |
| **Dependencies** | None (pure language) | `bashkit` Python package or `bashkit` CLI binary |
| **Sandboxing** | Basic (allowed commands list) | Resource limits (max commands, loop iterations, function depth) |
| **Performance** | Interpreted | Native Rust |

The builtin shell remains the default — zero dependencies, good enough for most agent workloads. Switch to bashkit when you need POSIX compliance, more builtins, or sandboxing controls.

---

## Prerequisites

### Python

Install the `bashkit` Python package (PyO3-compiled native extension):

```bash
pip install bashkit
```

This provides in-process execution with full feature support.

### TypeScript / PHP

Install the `bashkit` CLI binary:

```bash
# From source (requires Rust toolchain)
cargo install bashkit-cli

# Verify installation
bashkit -c 'echo hello'
```

> **Note:** The CLI path provides stateless one-shot execution. Custom command callbacks registered via `registerCommand()` are stored locally but not available in the subprocess. For full feature support, use the Python integration.

---

## Quick Start

Select bashkit as your shell driver — everything else stays the same:

### Python

```python
from src.python.standard_agent import StandardAgent
from src.python.bashkit_driver import register_bashkit_driver

# Register the "bashkit" driver (once, at startup)
register_bashkit_driver()

# Use it via the builder
agent = await (
    StandardAgent.build("gpt-4")
    .shell(cwd="/workspace")
    .driver("bashkit")
    .create()
)

result = agent.exec("echo hello | tr a-z A-Z")
print(result.stdout)  # "HELLO\n"
```

### TypeScript

```typescript
import { StandardAgent } from "./src/typescript/standard-agent.js";
import { registerBashkitDriver } from "./src/typescript/bashkit-driver.js";

// Register the "bashkit" driver (once, at startup)
registerBashkitDriver();

// Use it via the builder
const agent = await StandardAgent.build("gpt-4")
  .shell({ cwd: "/workspace" })
  .driver("bashkit")
  .create();

const result = agent.exec("echo hello | tr a-z A-Z");
console.log(result.stdout); // "HELLO\n"
```

### PHP

```php
use AgentHarness\StandardAgent;
use AgentHarness\BashkitDriver;

// Register the "bashkit" driver (once, at startup)
BashkitDriver::register();

// Use it via the builder
$agent = StandardAgent::build('gpt-4')
    ->shell(['cwd' => '/workspace'])
    ->driver('bashkit')
    ->create();

$result = $agent->execCommand('echo hello | tr a-z A-Z');
echo $result->stdout; // "HELLO\n"
```

---

## How It Works

### Resolution

When you say `driver("bashkit")`, the resolver checks for the appropriate runtime:

| Language | Resolver checks | Driver returned |
|----------|----------------|-----------------|
| Python | `import bashkit` succeeds | `BashkitPythonDriver` |
| TypeScript | `which bashkit` succeeds | `BashkitCLIDriver` |
| PHP | `which bashkit` succeeds | `BashkitCLIDriver` |

### Python: In-Process (PyO3)

The `BashkitPythonDriver` wraps the `bashkit` Python package, which is a Rust native extension compiled via PyO3. Execution happens in-process — no subprocess overhead.

**Key features:**
- **Stateful** — shell state (variables, functions) persists between `exec()` calls within the same driver instance
- **Custom commands** — `register_command()` uses bashkit's `ScriptedTool` to register Python callbacks as bash builtins
- **VFS sync** — hybrid lazy sync tracks dirty files and syncs only changes before/after each `exec()`

**VFS synchronization:**

The host's `FilesystemDriver` remains the source of truth. A `_DirtyTrackingFS` wrapper intercepts writes to track which files have changed since the last exec:

1. Before exec: only dirty files are written into bashkit's VFS via base64-encoded commands
2. The command executes in bashkit's interpreter
3. After exec: a batched `find + base64` command reads all bashkit files back; changes are diffed and applied to the host FS

Content is base64-encoded during sync to safely handle special characters (quotes, backslashes, percent signs, newlines, binary content).

### TypeScript / PHP: CLI Subprocess

The `BashkitCLIDriver` spawns `bashkit -c 'command'` for each `exec()` call. Each invocation creates a fresh bashkit instance.

**VFS synchronization (preamble/epilogue):**

Despite being stateless at the process level, the CLI driver maintains VFS consistency across exec() calls using the same `DirtyTrackingFS` pattern as Python:

1. Before exec (preamble): dirty files are injected as base64-encoded shell commands prepended to the user's command
2. The combined script runs in a single bashkit subprocess
3. After exec (epilogue): a `find + base64` command appended with a unique marker dumps all file state
4. The driver parses stdout to split user output from sync data, and applies changes back to the host VFS

The exit code from the user's command is preserved by capturing `$?` before the epilogue runs.

**Limitations:**
- **No persistent shell state** — variables, functions, and aliases don't persist between `exec()` calls (new process each time)
- **No custom commands** — `registerCommand()` stores handlers locally, but they are not available inside the subprocess
- **VFS files do persist** — the preamble/epilogue pattern ensures files written in one exec() are available in the next

---

## Custom Commands (Python Only)

Custom commands registered via `register_command()` become bash builtins inside bashkit. They compose with pipes, redirects, and control flow — just like built-in commands.

Under the hood, `BashkitPythonDriver` uses bashkit's `ScriptedTool` API. When you register a command, the driver switches from `Bash` to `ScriptedTool` for execution, which supports callback invocation during script evaluation.

```python
from src.python.shell import ExecResult

agent.register_command("summarize", lambda args, stdin: ExecResult(
    stdout=f"Summary of {len(stdin.splitlines())} lines\n"
))

result = agent.exec("cat /data.txt | summarize")
```

> **Note:** Custom commands are not supported in the TypeScript and PHP `BashkitCLIDriver` because the CLI subprocess cannot call back into the host process.

---

## Configuration

### Per-Agent (Builder)

```python
# Python
agent = await StandardAgent.build("gpt-4").driver("bashkit").create()
```

```typescript
// TypeScript
const agent = await StandardAgent.build("gpt-4").driver("bashkit").create();
```

```php
// PHP
$agent = StandardAgent::build('gpt-4')->driver('bashkit')->create();
```

### Global Default

Set bashkit as the default driver for all agents:

```python
# Python
from src.python.drivers import ShellDriverFactory
ShellDriverFactory.default = "bashkit"
```

```typescript
// TypeScript
import { ShellDriverFactory } from "./src/typescript/drivers.js";
ShellDriverFactory.default = "bashkit";
```

```php
// PHP
use AgentHarness\ShellDriverFactory;
ShellDriverFactory::$default = 'bashkit';
```

After setting the global default, all agents use bashkit unless they explicitly override with `.driver("builtin")`.

### Combining with Shell Options

The `driver()` and `shell()` builder methods compose freely:

```python
agent = await (
    StandardAgent.build("gpt-4")
    .shell(cwd="/workspace", env={"PATH": "/usr/bin"})
    .driver("bashkit")
    .create()
)
```

---

## Capabilities by Language

| Capability | Python | TypeScript | PHP |
|-----------|--------|------------|-----|
| In-process execution | Yes (PyO3) | No (subprocess) | No (subprocess) |
| Shell state persistence between exec() | Yes | No | No |
| Custom command callbacks | Yes (ScriptedTool) | No | No |
| VFS sync | Hybrid lazy (in-process) | Preamble/epilogue (per subprocess) | Preamble/epilogue (per subprocess) |
| Install | `pip install bashkit` | `cargo install bashkit-cli` | `cargo install bashkit-cli` |

---

## Limitations

- **TypeScript/PHP shell state is stateless.** Each `exec()` starts a fresh bashkit instance. Shell variables and functions don't persist between calls. VFS files do persist via preamble/epilogue sync.
- **Custom commands only work in Python.** The CLI subprocess can't call back into the host process.
- **Synchronous only.** All drivers block on execution, matching the synchronous `ShellDriver` contract.

### Related Documents

- [ADR 0026: Shell Driver Contracts](../adr/0026-shell-driver-contracts.md)
- [ADR 0027: Bashkit Driver Integration](../adr/0027-bashkit-driver-integration.md)
- [Shell Driver Architecture Design](../plans/2026-03-06-shell-driver-architecture-design.md)
- [Bashkit Integration Revision Plan](../plans/2026-03-09-bashkit-integration-revision.md)
