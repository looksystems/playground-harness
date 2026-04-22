# Plan: Native gRPC Transport for OpenShellGrpcDriver

## Context

The OpenShellGrpcDriver across all 3 languages (Python, TypeScript, PHP) currently uses SSH subprocess calls to execute commands in OpenShell sandboxes. Despite the name "GrpcDriver", no gRPC is involved — ADR 0029 documents the SSH decision for zero-dependency simplicity.

NVIDIA's OpenShell project now publishes proto definitions at `github.com/NVIDIA/OpenShell/tree/main/proto` with a full gRPC service including `ExecSandbox` (server-streaming), `CreateSandbox`, `DeleteSandbox`, `GetSandbox`, and `Health` RPCs. We want to add native gRPC transport to unlock streaming exec, proper sandbox lifecycle management, and alignment with the official API.

**Goal**: Replace SSH with native gRPC in the existing `OpenShellGrpcDriver` class, keep SSH as a fallback, add async `execStream()`, and verify with Docker-based local testing.

---

## Phase 1: Proto Vendoring & Codegen Infrastructure

### Files to create
- `proto/openshell/openshell.proto` — vendored from NVIDIA/OpenShell
- `proto/openshell/sandbox.proto` — vendored
- `proto/openshell/datamodel.proto` — vendored
- `src/python/generated/openshell/` — Python generated stubs
- `src/typescript/generated/openshell/` — TypeScript generated stubs
- `src/php/Generated/OpenShell/` — PHP generated stubs

### Steps
1. Vendor the 3 proto files from `NVIDIA/OpenShell/proto/` into `proto/openshell/`
2. Add dependencies:
   - Python: `grpcio`, `grpcio-tools` in `pyproject.toml` optional deps
   - TypeScript: `@grpc/grpc-js`, `@grpc/proto-loader` in `package.json`
   - PHP: `grpc/grpc`, `google/protobuf` in `composer.json`
3. Add codegen scripts (Makefile target or per-language scripts)
4. Run codegen, verify generated files compile/type-check
5. Commit generated code (small, stable API surface)

### Codegen commands
```bash
# Python
python -m grpc_tools.protoc --python_out=src/python/generated \
  --grpc_python_out=src/python/generated --pyi_out=src/python/generated \
  -Iproto proto/openshell/*.proto

# TypeScript — use proto-loader for dynamic loading (simpler, fewer build deps)
# Proto loaded at runtime via @grpc/proto-loader

# PHP
protoc --php_out=src/php/Generated --grpc_out=src/php/Generated \
  --plugin=protoc-gen-grpc=$(which grpc_php_plugin) \
  -Iproto proto/openshell/*.proto
```

---

## Phase 2: Transport Abstraction in Driver

### Files to modify
- `src/python/openshell_grpc_driver.py`
- `src/typescript/openshell-grpc-driver.ts`
- `src/php/OpenShellGrpcDriver.php`

### Changes (same pattern in all 3 languages)

**2a. Add `transport` constructor parameter**
- Type: `"grpc" | "ssh"`, default `"ssh"`
- Stored as `_transport` field

**2b. Add gRPC client field**
- `_grpcClient` — lazily created from `_endpoint` on first use when `transport == "grpc"`
- Python: `OpenShellStub(grpc.insecure_channel(endpoint))`
- TypeScript: loaded via `@grpc/proto-loader` + `grpc.loadPackageDefinition`
- PHP: `new OpenShellClient(endpoint, ['credentials' => ChannelCredentials::createInsecure()])`

**2c. Split `_rawExec()` into transport paths**
```python
def _raw_exec(self, command: str) -> dict:
    if self._transport == "grpc":
        return self._raw_exec_grpc(command)
    return self._raw_exec_ssh(command)
```

**2d. `_raw_exec_grpc()` implementation**
- Build `ExecSandboxRequest(sandbox_id=self._sandbox_id, command=["bash", "-c", command])`
- Iterate the response stream, accumulate stdout/stderr bytes, capture exit code
- Return `{stdout, stderr, exitCode}` — same shape as SSH path

**2e. Update `_ensureSandbox()` for gRPC**
- When `transport == "grpc"`: call `CreateSandbox` RPC with spec built from `self._policy`
- Add `_build_sandbox_spec()` helper to map `OpenShellPolicy` → proto `SandboxSpec`:
  - `filesystem_allow` → `SandboxPolicy.filesystem.read_write`
  - `network_rules` → `SandboxPolicy.network_policies`
  - `inference_routing` → provider config

**2f. Update `close()` for gRPC**
- When `transport == "grpc"` and sandbox exists: call `DeleteSandbox(name=sandbox_name)` RPC

**2g. Update `capabilities()`**
- Return `"streaming"` only when `transport == "grpc"` (honest capability reporting)

**2h. SSH path unchanged**
- Existing SSH code extracted verbatim into `_raw_exec_ssh()`, `_ensure_sandbox_ssh()`, etc.
- Mock path (`grpc_override`/`_execOverride`) still works regardless of transport setting

---

## Phase 3: TypeScript Sync Bridge (Worker Thread)

### Problem
`@grpc/grpc-js` is fully async. The `ShellDriver` interface requires sync `exec()`.

### Solution: Worker thread with SharedArrayBuffer + Atomics.wait

### Files to create
- `src/typescript/grpc-sync-bridge.ts` — the worker thread script

### Design
1. Main thread creates a `SharedArrayBuffer` and a `Worker` running `grpc-sync-bridge.ts`
2. On `exec()`: main thread posts the gRPC request to the worker, then calls `Atomics.wait()` to block
3. Worker thread makes the async gRPC call, collects the stream, writes result to shared buffer, calls `Atomics.notify()`
4. Main thread wakes up, reads result from shared buffer, returns `ExecResult`

This gives zero-subprocess-overhead sync gRPC in TypeScript. The worker is created once and reused across exec() calls.

---

## Phase 4: Async `execStream()` Method

### Files to modify (same 3 driver files)

**4a. Define `ExecStreamEvent` type**
```typescript
// TypeScript
type ExecStreamEvent =
  | { type: "stdout"; data: string }
  | { type: "stderr"; data: string }
  | { type: "exit"; exitCode: number };
```
```python
# Python
@dataclass
class ExecStreamEvent:
    type: Literal["stdout", "stderr", "exit"]
    data: str = ""
    exit_code: int = 0
```

**4b. Implement `execStream(command: str)`**
- Runs VFS preamble (same logic as `exec()`)
- Sends `ExecSandboxRequest` via gRPC
- Yields `ExecStreamEvent` for each chunk from the stream
- Accumulates raw stdout internally for VFS sync-back parsing
- After stream completes: runs `parseSyncOutput` + `applySyncBack` on accumulated buffer
- Throws `UnsupportedError` if `transport != "grpc"`

**Return types by language:**
- Python: `Generator[ExecStreamEvent, None, None]` (sync generator — grpcio iterators are blocking)
- TypeScript: `AsyncGenerator<ExecStreamEvent>` (natural for @grpc/grpc-js)
- PHP: `Generator` (PHP generators are sync, matching grpc extension's blocking stream)

**VFS sync-back with streaming**: The caller sees stdout chunks in real-time, but VFS updates only happen after the stream completes. This is correct — VFS state isn't consistent mid-command anyway.

---

## Phase 5: Test Updates

### Files to modify
- `tests/python/test_openshell_grpc_driver.py`
- `tests/typescript/openshell-grpc-driver.test.ts`
- `tests/php/OpenShellGrpcDriverTest.php`

### Strategy
1. **Existing tests remain unchanged** — they test SSH/mock path and pass as-is
2. **New test block for gRPC transport** — uses a mock gRPC client stub injected via `_grpcClient` option
3. **Mock gRPC stub per language**: implements the generated stub interface
   - `ExecSandbox` → returns iterable of `ExecSandboxEvent` messages
   - `CreateSandbox` / `DeleteSandbox` → tracks calls, returns canned responses

### New test cases
- Transport selection: `transport: "grpc"` uses gRPC path, `"ssh"` uses SSH
- gRPC exec: returns correct stdout/stderr/exitCode from stream
- gRPC lifecycle: CreateSandbox called on first exec, DeleteSandbox on close
- gRPC VFS sync: preamble/epilogue works through gRPC exec
- execStream: yields events in correct order, stdout+stderr interleaved
- execStream VFS sync-back: happens after stream completes
- Policy mapping: OpenShellPolicy correctly mapped to proto SandboxSpec
- Custom commands still bypass gRPC (local dispatch)
- Clone copies transport setting

---

## Phase 6: Docker Compose & Integration Tests

### Files to create
- `docker-compose.yml` — orchestrates mock gRPC server + sandbox container
- `tests/openshell-grpc-mock/server.py` — minimal Python gRPC server
- `tests/openshell-grpc-mock/Dockerfile`
- `tests/openshell-grpc-mock/requirements.txt`
- `tests/integration/test_openshell_grpc_live.py`

### Mock gRPC server design
- Python `grpc.server` implementing `OpenShell` service
- `Health` → returns HEALTHY
- `CreateSandbox` → starts or reuses existing sandbox container, returns sandbox ID
- `ExecSandbox` → runs command via `docker exec` in sandbox container, streams stdout/stderr/exit
- `DeleteSandbox` → stops container
- Exposes port 50051

### docker-compose.yml
```yaml
services:
  openshell-mock:
    build: tests/openshell-grpc-mock
    ports: ["50051:50051"]
    volumes: ["/var/run/docker.sock:/var/run/docker.sock"]
  sandbox:
    image: ubuntu:22.04
    command: ["sleep", "infinity"]
```

### Integration test
- Set `OPENSHELL_ENDPOINT=localhost:50051`
- Create driver with `transport="grpc"`
- Test: create sandbox, exec commands, VFS sync, execStream, delete sandbox
- Skipped in CI unless `OPENSHELL_ENDPOINT` is set

---

## Phase 7: Documentation Updates

### Files to modify
- `docs/adr/0029-openshell-driver-integration.md` — add "gRPC transport" section, update status
- `docs/guides/openshell.md` — add gRPC transport setup, dependency instructions, docker-compose usage, execStream examples

---

## Verification

1. **Unit tests pass (all 3 languages)**:
   ```bash
   pytest tests/python/test_openshell_grpc_driver.py -v
   npx vitest run tests/typescript/openshell-grpc-driver.test.ts
   php vendor/bin/phpunit tests/php/OpenShellGrpcDriverTest.php
   ```

2. **Integration tests with Docker**:
   ```bash
   docker compose up -d
   OPENSHELL_ENDPOINT=localhost:50051 pytest tests/integration/test_openshell_grpc_live.py -v
   docker compose down
   ```

3. **Proto codegen reproduces cleanly**:
   ```bash
   make proto  # or equivalent codegen command
   git diff --exit-code src/*/generated/  # no untracked changes
   ```

4. **SSH fallback still works**: All existing tests pass without gRPC deps installed

---

## Execution Order

| Phase | Description | Dependencies |
|-------|-------------|--------------|
| 1 | Proto vendoring & codegen | None |
| 2 | Transport abstraction | Phase 1 |
| 3 | TS sync bridge | Phase 2 |
| 4 | execStream() | Phase 2 |
| 5 | Test updates | Phases 2-4 |
| 6 | Docker & integration | Phase 5 |
| 7 | Docs | Phase 6 |

Phases 3 and 4 can run in parallel after Phase 2.
