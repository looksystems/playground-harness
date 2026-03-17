# OpenShell Driver Guide

The OpenShell driver connects Agent Harness to [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell), a secure sandboxed runtime for AI agents. Commands execute inside isolated containers accessed via SSH, with multi-layer policy enforcement.

## Installation

### SSH transport (default)

No additional packages needed for any language. Ensure `ssh` is on your PATH.

### gRPC transport (optional)

For native gRPC with streaming exec support, install the gRPC dependencies:

```bash
# Python
pip install agent-harness[grpc]

# TypeScript
npm install @grpc/grpc-js @grpc/proto-loader

# PHP
pecl install grpc
composer require grpc/grpc google/protobuf
```

## Setup

### 1. Start an OpenShell sandbox

**Option A: Using the OpenShell CLI** (requires [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell))

```bash
# Install OpenShell CLI
curl -LsSf https://raw.githubusercontent.com/NVIDIA/OpenShell/main/install.sh | sh

# Create a sandbox
openshell sandbox create

# Note the SSH port from `openshell sandbox list`
```

**Option B: Using Docker** (for development/testing)

If you don't have the OpenShell CLI, you can run a standalone sandbox container:

```bash
# Build the test sandbox image
docker build -t openshell-sandbox tests/openshell-sandbox/

# Start on port 2222
docker run -d --name openshell-sandbox -p 2222:22 openshell-sandbox

# Verify SSH access
ssh -p 2222 -o StrictHostKeyChecking=no sandbox@localhost 'echo hello'
```

The test sandbox provides a minimal Linux environment with SSH access, which mirrors how real OpenShell sandboxes operate.

### 2. Register the driver

```python
# Python
from src.python.openshell_driver import register_openshell_driver
register_openshell_driver()
```

```typescript
// TypeScript
import { registerOpenShellDriver } from "./openshell-driver.js";
registerOpenShellDriver();
```

```php
// PHP
AgentHarness\OpenShellDriver::register();
```

### 3. Direct usage

```python
from src.python.openshell_grpc_driver import OpenShellGrpcDriver, OpenShellPolicy

policy = OpenShellPolicy(
    filesystem_allow=["/data", "/tmp"],
    network_rules=[{"allow": "10.0.0.0/8"}],
    inference_routing=True,
)

with OpenShellGrpcDriver(
    ssh_host="localhost",
    ssh_port=2222,
    ssh_user="sandbox",
    workspace="/tmp/harness",
    policy=policy,
) as driver:
    driver.fs.write("/input.txt", "hello world")
    result = driver.exec("wc -w /tmp/harness/input.txt")
    print(result.stdout)  # "2 /tmp/harness/input.txt"
```

## Workspace

The driver uses a **workspace directory** on the remote host to scope VFS file sync. This avoids scanning the entire container filesystem (which would include thousands of system files).

- **Default**: `/home/sandbox/workspace`
- VFS path `/hello.txt` maps to `{workspace}/hello.txt` on the remote host
- The epilogue `find` only scans the workspace directory
- Shell commands that reference synced files must use the workspace path

```python
driver = OpenShellGrpcDriver(workspace="/tmp/harness")
driver.fs.write("/data.txt", "content")       # VFS path
driver.exec("cat /tmp/harness/data.txt")       # Remote path
```

In mock mode (via `grpc_override` or `_execOverride`), no remapping occurs — paths pass through as-is.

## SSH connection

The driver connects to the sandbox via SSH. All three languages use the same connection model:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `ssh_host` | `localhost` | SSH hostname |
| `ssh_port` | `2222` | SSH port |
| `ssh_user` | `sandbox` | SSH username |
| `workspace` | `/home/sandbox/workspace` | Remote directory for VFS file sync |

Environment variables for live tests:
- `OPENSHELL_SSH_HOST` — override SSH host
- `OPENSHELL_SSH_PORT` — override SSH port
- `OPENSHELL_SSH_USER` — override SSH user

## Policy examples

### Read-only sandbox

```python
policy = OpenShellPolicy(
    filesystem_allow=["/data"],  # read-only access to /data
)
```

### Network-restricted sandbox

```python
policy = OpenShellPolicy(
    network_rules=[
        {"allow": "10.0.0.0/8"},      # internal network only
        {"deny": "0.0.0.0/0"},         # block everything else
    ],
)
```

### Inference routing

When `inference_routing=True` (default), the sandbox routes API calls through OpenShell's `inference.local` endpoint. Credentials never touch the sandbox filesystem.

## VFS sync

The OpenShell driver uses the same preamble/epilogue sync mechanism as the bashkit driver:

1. **Before exec**: Dirty VFS files are base64-encoded and written to the workspace via SSH
2. **After exec**: Files in the workspace are read back and diffed against the local VFS
3. New/modified files are updated; deleted files are removed

VFS paths are transparently remapped to workspace paths on the remote host.

## Custom Commands

Registered commands are intercepted locally before SSH dispatch. The driver extracts the first whitespace-delimited token from the command string and checks it against the registered commands map. If it matches, the handler runs in the host process and the result is returned directly — no SSH call, no VFS sync.

- Only simple first-word matching (no pipe or compound expression support)
- Custom command results don't trigger VFS sync (they're local)
- Use case: domain-specific operations that don't need sandbox execution

```python
driver.register_command("lookup", lambda args, stdin="": ExecResult(
    stdout=f"Found: {args[0]}\n"
))
result = driver.exec("lookup item-42")  # Runs locally, no SSH
```

## gRPC Transport

The driver supports native gRPC transport for streaming exec, proper sandbox lifecycle management, and alignment with the official OpenShell API.

### Setup

```python
# Python — gRPC transport
from src.python.openshell_grpc_driver import OpenShellGrpcDriver, OpenShellPolicy

driver = OpenShellGrpcDriver(
    endpoint="localhost:50051",
    transport="grpc",
    policy=OpenShellPolicy(
        filesystem_allow=["/data"],
        inference_routing=True,
    ),
)
result = driver.exec("echo hello")
driver.close()
```

```typescript
// TypeScript — gRPC transport
const driver = new OpenShellGrpcDriver({
  endpoint: "localhost:50051",
  transport: "grpc",
  policy: { filesystemAllow: ["/data"] },
});
const result = driver.exec("echo hello");
driver.close();
```

```php
// PHP — gRPC transport
$driver = new OpenShellGrpcDriver(
    endpoint: 'localhost:50051',
    transport: 'grpc',
    policy: ['filesystemAllow' => ['/data']],
);
$result = $driver->exec('echo hello');
$driver->close();
```

### Streaming exec

When using gRPC transport, `execStream()` yields real-time events:

```python
# Python
from src.python.openshell_grpc_driver import ExecStreamEvent

for event in driver.exec_stream("make build"):
    if event.type == "stdout":
        print(event.data, end="")
    elif event.type == "stderr":
        print(event.data, end="", file=sys.stderr)
    elif event.type == "exit":
        print(f"\nExit code: {event.exit_code}")
```

```typescript
// TypeScript
for await (const event of driver.execStream("make build")) {
  if (event.type === "stdout") process.stdout.write(event.data);
  else if (event.type === "stderr") process.stderr.write(event.data);
  else if (event.type === "exit") console.log(`Exit: ${event.exitCode}`);
}
```

```php
// PHP
foreach ($driver->execStream('make build') as $event) {
    match ($event['type']) {
        'stdout' => print($event['data']),
        'stderr' => fwrite(STDERR, $event['data']),
        'exit' => printf("\nExit code: %d\n", $event['exitCode']),
    };
}
```

VFS sync-back happens after the stream completes — the caller sees stdout chunks in real-time, but VFS updates are applied once the command finishes.

### Docker-based testing

A mock gRPC server is provided for local development:

```bash
# Start the mock server
docker compose up -d

# Run integration tests
OPENSHELL_ENDPOINT=localhost:50051 python3 -m pytest tests/integration/test_openshell_grpc_live.py -v

# Clean up
docker compose down
```

## Lifecycle

- **Lazy creation**: The sandbox connection is established on first `exec()` call
- **Context manager**: Use `with` statement for automatic cleanup (Python)
- **Explicit close**: Call `driver.close()` to release resources
- **Clone**: `driver.clone()` creates a new driver instance with cloned VFS

## Running live tests

The live integration tests require a running sandbox accessible via SSH:

```bash
# Start the test sandbox
docker run -d --name openshell-sandbox -p 2222:22 openshell-sandbox

# Run live tests
python3 -m pytest tests/python/test_openshell_live.py -v
npx vitest run tests/typescript/openshell-live.test.ts
php vendor/bin/phpunit tests/php/OpenShellLiveTest.php

# Clean up
docker rm -f openshell-sandbox
```

Tests skip automatically when no sandbox is running on `localhost:2222`.

## Driver capabilities

```python
driver.capabilities()
# {'custom_commands', 'remote', 'policies', 'streaming'}
```

| Capability | Description |
|-----------|-------------|
| `custom_commands` | Supports `register_command()` / `unregister_command()`. Custom commands execute locally in the host process via first-word interception (not in the sandbox). |
| `remote` | Executes commands in a remote process |
| `policies` | Accepts security policy objects |
| `streaming` | Supports `execStream()` with real-time stdout/stderr events (gRPC transport only) |
