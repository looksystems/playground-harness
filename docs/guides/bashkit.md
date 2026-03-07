# Bashkit Integration Guide

## Overview

[bashkit](https://github.com/everruns/bashkit) is a Rust-based sandboxed POSIX shell (IEEE 1003.1-2024) with 100+ builtins, a virtual filesystem, and resource limits. The agent harness integrates with bashkit as an optional shell driver, giving agents a fully POSIX-compliant shell without changing any agent code — just set `driver("bashkit")`.

The integration uses an IPC transport: the harness spawns `bashkit-cli` as a long-lived subprocess and communicates via JSON-RPC over stdin/stdout. A future native transport (in-process FFI via PyO3/napi-rs/ext-php-rs) will be added transparently behind the same `"bashkit"` driver name.

### Why bashkit?

| | Builtin Shell | bashkit |
|---|---|---|
| **Builtins** | ~30 commands | 100+ POSIX commands |
| **POSIX compliance** | Subset (pipes, redirects, control flow) | Full IEEE 1003.1-2024 |
| **Dependencies** | None (pure language) | `bashkit-cli` binary |
| **Sandboxing** | Basic (allowed commands list) | Resource limits (max commands, loop iterations, function depth) |
| **Performance** | Interpreted | Native Rust |

The builtin shell remains the default — zero dependencies, good enough for most agent workloads. Switch to bashkit when you need POSIX compliance, more builtins, or sandboxing controls.

---

## Prerequisites

Install the `bashkit-cli` binary and ensure it's on your `PATH`:

```bash
# From source (requires Rust toolchain)
cargo install bashkit-cli

# Verify installation
bashkit-cli --version
```

> **Note:** `bashkit-cli` must support a `--jsonrpc` mode for the IPC driver. This mode may require an upstream contribution or a fork — check the [bashkit repository](https://github.com/everruns/bashkit) for current status.

No language-specific packages are needed for the IPC driver. It uses only standard library subprocess/process APIs.

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

### Auto-Resolution

When you say `driver("bashkit")`, the `BashkitDriver` resolver picks the best available transport:

```
BashkitDriver.resolve():
    1. Native extension available?  →  BashkitNativeDriver  (Phase 3, not yet built)
    2. bashkit-cli on PATH?         →  BashkitIPCDriver     (current)
    3. Neither?                     →  RuntimeError
```

You never choose between native and IPC explicitly. As native extensions become available, existing code automatically upgrades.

### JSON-RPC Protocol

The IPC driver spawns `bashkit-cli --jsonrpc` and communicates via newline-delimited JSON-RPC over stdin/stdout:

```
Host → bashkit-cli:  {"method":"exec","params":{"cmd":"ls -la","cwd":"/","env":{},"fs":{...}},"id":1}
bashkit-cli → Host:  {"id":1,"result":{"stdout":"...","exitCode":0,"fs_changes":{...}}}
```

Each `exec()` call:
1. **Snapshots** the host virtual filesystem into a JSON dict
2. **Sends** the snapshot + command to bashkit-cli
3. **Handles** any callback requests (for custom commands — see below)
4. **Applies** filesystem changes (created/deleted files) back to the host VFS
5. **Returns** an `ExecResult` with stdout, stderr, and exit code

### VFS Sync (Host-Owns Model)

The host's `FilesystemDriver` is always the source of truth. Before each command, the driver serializes all files (including lazy providers) into the request. After execution, any files bashkit created or deleted are applied back. This means:

- `agent.fs.write()` and `agent.exec()` stay in sync automatically
- Lazy file providers (`write_lazy()`) resolve naturally during snapshot
- No shared mutable state between host and bashkit

---

## Custom Commands

Custom commands registered via `registerCommand()` work transparently over IPC. bashkit invokes them mid-evaluation via bidirectional JSON-RPC, so they compose with pipes, redirects, and control flow — just like built-in commands.

### Python

```python
from src.python.shell import ExecResult

agent.register_command("summarize", lambda args, stdin: ExecResult(
    stdout=f"Summary of {len(stdin.splitlines())} lines\n"
))

result = agent.exec("cat /data.txt | summarize")
```

### TypeScript

```typescript
agent.registerCommand("summarize", (args, stdin) => ({
  stdout: `Summary of ${stdin.split("\n").length} lines\n`,
  stderr: "",
  exitCode: 0,
}));

const result = agent.exec("cat /data.txt | summarize");
```

### PHP

```php
$agent->registerCommand('summarize', function (array $args, string $stdin): \AgentHarness\ExecResult {
    $lines = count(explode("\n", $stdin));
    return new \AgentHarness\ExecResult(stdout: "Summary of {$lines} lines\n");
});

$result = $agent->execCommand('cat /data.txt | summarize');
```

### How Callbacks Work Over IPC

When bashkit encounters a custom command during evaluation, it pauses and sends an `invoke_command` request back to the host:

```
Host → bashkit:  {"id":1,"method":"exec","params":{"cmd":"echo hi | summarize"}}
bashkit → Host:  {"id":100,"method":"invoke_command","params":{"name":"summarize","args":[],"stdin":"hi\n"}}
Host → bashkit:  {"id":100,"result":"Summary of 1 lines\n"}
bashkit → Host:  {"id":1,"result":{"stdout":"Summary of 1 lines\n","exitCode":0}}
```

The host runs a simple event loop that dispatches callback requests to locally registered handlers and resolves the pending exec call when the final result arrives.

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

## Limitations & Future

### Current Limitations

- **`bashkit-cli --jsonrpc` mode** may not exist upstream yet. Check the [bashkit repository](https://github.com/everruns/bashkit) or use a fork that supports it.
- **Full VFS snapshot** is sent with every `exec()` call. For large virtual filesystems, this adds serialization overhead. Dirty-tracking optimization is planned but not yet implemented.
- **Synchronous only.** The IPC driver blocks on subprocess I/O. This matches the synchronous `ShellDriver` contract but means exec calls are blocking.

### Phase 3: Native Drivers

Future native extensions will call bashkit in-process via FFI, eliminating subprocess overhead:

| Language | Binding | Status |
|----------|---------|--------|
| Python | PyO3 (`bashkit-python`) | Exists upstream |
| TypeScript | napi-rs (`bashkit-node`) | To be built (~300 lines) |
| PHP | ext-php-rs (`bashkit-php`) | To be built (~300 lines) |

Native drivers will be selected automatically by the `BashkitDriver` resolver when available — no code changes needed. Install the binding package, and `driver("bashkit")` upgrades from IPC to native automatically.

### Related Documents

- [ADR 0026: Shell Driver Contracts](../adr/0026-shell-driver-contracts.md)
- [ADR 0027: Bashkit Driver Integration](../adr/0027-bashkit-driver-integration.md)
- [Shell Driver Architecture Design](../plans/2026-03-06-shell-driver-architecture-design.md)
