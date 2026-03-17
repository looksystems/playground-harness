# OpenShell Integration: Multi-Language Shell Driver + Architectural Lessons

## Context

**NVIDIA OpenShell** is a secure sandboxed runtime for AI agents — Rust-based, using K3s/Docker with gRPC APIs, multi-layer policy enforcement (filesystem, network, process, inference), and SSH-based command execution inside isolated containers.

**Agent Harness** already has a swappable driver architecture (`ShellDriver` / `FilesystemDriver` contracts) with a `ShellDriverFactory` registry. Two drivers exist: `builtin` (in-memory VirtualFS) and `bashkit` (real bash via PyO3 or subprocess). The OpenShell driver follows the same pattern — a third registered driver that routes commands to a remote sandbox.

The goal is twofold: (1) a concrete OpenShell driver for all three languages, and (2) extracting architectural patterns from OpenShell that improve Agent Harness independently.

---

## Phase 1: Extract Shared Remote Sync Logic

The preamble/epilogue/parse/apply sync logic is duplicated across `BashkitPythonDriver`, `BashkitCLIDriver` (TS), and `BashkitCLIDriver` (PHP). OpenShell would be a third consumer. Extract into shared utilities first.

### Files to create
- `src/python/_remote_sync.py` — `build_sync_preamble()`, `build_sync_epilogue()`, `parse_sync_output()`, `apply_sync_back()`
- `src/typescript/remote-sync.ts` — same functions
- `src/php/RemoteSyncTrait.php` — same as PHP trait

### Files to modify
- `src/python/bashkit_python_driver.py` — use extracted functions
- `src/typescript/bashkit-cli-driver.ts` — use extracted functions
- `src/php/BashkitCLIDriver.php` — use extracted trait

### Verification
All existing bashkit tests must continue passing after extraction.

---

## Phase 2: Python OpenShell Driver

### Key design decisions

**Communication**: gRPC `ExecSandbox` (primary). Stateless per-call, maps cleanly to `exec() -> ExecResult`. SSH available as opt-in alternative (`openshell-ssh` driver name) for stateful sessions.

**Filesystem sync**: Reuse `DirtyTrackingFS` + extracted sync logic from Phase 1. The only difference from bashkit is `_raw_exec()` calls gRPC instead of subprocess.

**Sandbox lifecycle**: Lazy creation on first `exec()`. Explicit `close()` + context manager (`__enter__`/`__exit__`). `clone()` creates a new sandbox.

**Custom commands**: Local interception (same limitation as bashkit — doesn't work inside pipelines).

### Files to create

**`src/python/openshell_driver.py`** — Resolver (mirrors `bashkit_driver.py`)
```python
class OpenShellDriver:
    @staticmethod
    def resolve(**kwargs): ...  # checks for grpcio

def register_openshell_driver():
    ShellDriverFactory.register("openshell", lambda **kw: OpenShellDriver.resolve(**kw))
```

**`src/python/openshell_grpc_driver.py`** — Implementation
```python
@dataclass
class OpenShellPolicy:
    filesystem_allow: list[str] | None = None
    network_rules: list[dict] | None = None
    inference_routing: bool = True

class OpenShellGrpcDriver(ShellDriver):
    def __init__(self, endpoint="localhost:50051", sandbox_id=None,
                 policy=None, cwd="/", env=None, grpc_override=None): ...
    def exec(self, command: str) -> ExecResult: ...  # preamble → gRPC ExecSandbox → sync back
    def close(self) -> None: ...  # DeleteSandbox
    def __enter__/exit__: ...  # context manager
```

**`tests/python/test_openshell_grpc_driver.py`** — Mock-based tests following `test_bashkit_python_driver.py` pattern:
- Contract compliance (isinstance, properties)
- Exec: stdout/stderr/exit_code propagation
- VFS sync: dirty tracking, preamble, sync-back
- Lifecycle: lazy creation, close, context manager
- Policy: passed to CreateSandbox correctly
- Clone: independent sandbox + VFS

### Critical files to reference
- `src/python/bashkit_python_driver.py` — primary pattern to follow
- `src/python/bashkit_driver.py` — resolver pattern
- `src/python/drivers.py` — contracts to implement

---

## Phase 3: TypeScript + PHP OpenShell Drivers

### TypeScript

**Async question**: Node.js gRPC is async. Two options:
1. Use SSH via `child_process.spawnSync` for sync `exec()` (pragmatic, no contract change)
2. Return `Promise<ExecResult>` from `exec()` (requires contract evolution)

**Recommendation**: Option 1 for v1 (SSH via spawnSync). Keeps the contract stable. The SSH session is created via gRPC `CreateSshSession`, then commands run via `ssh -p <port> user@host <command>`. File sync uses the same extracted `remote-sync.ts` utilities.

### Files to create
- `src/typescript/openshell-driver.ts` — resolver
- `src/typescript/openshell-grpc-driver.ts` — implementation
- `tests/typescript/openshell-grpc-driver.test.ts`

### PHP

PHP gRPC is synchronous natively — maps cleanly. Follows Python structure exactly.

### Files to create
- `src/php/OpenShellDriver.php` — resolver
- `src/php/OpenShellGrpcDriver.php` — implementation
- `tests/php/OpenShellGrpcDriverTest.php`

---

## Phase 4: Architectural Lessons (Independent of OpenShell Driver)

### 4a. Security Policy as First-Class Object

OpenShell formalizes security (filesystem allow-lists, network rules, process isolation) into a composable policy object. Agent Harness currently scatters security across constructor params (`allowed_commands`, `max_output`, `max_iterations`).

**Proposal**: Introduce `ShellSecurityPolicy`:
```python
@dataclass
class ShellSecurityPolicy:
    allowed_commands: set[str] | None = None
    writable_paths: list[str] | None = None
    max_output: int = 16_000
    max_iterations: int = 10_000
    read_only: bool = False
```

All three languages. Accepted by `ShellDriver` constructors and `AgentBuilder.shell()`.

### 4b. Credential Isolation Pattern

OpenShell routes credentials through `inference.local` — keys never touch the sandbox filesystem. For Agent Harness, add a `secrets`/`providers` config on the driver that maps service names to credential injection, kept separate from VFS writes.

### 4c. Streaming Execution Hooks

OpenShell's streaming `ExecSandbox` provides real-time output. Add `HookEvent.SHELL_STDOUT_CHUNK` and `HookEvent.SHELL_STDERR_CHUNK` for drivers that support it. The existing `SHELL_RESULT` only fires after completion.

### 4d. Driver Capability Query

Different drivers have different capabilities. Add:
```python
class ShellDriver(ABC):
    def capabilities(self) -> set[str]:
        return set()  # "custom_commands", "stateful", "streaming", "policies", "remote"
```

### Files to create/modify
- `docs/adr/0029-openshell-driver-integration.md`
- `docs/adr/0030-security-policy-objects.md`
- `docs/guides/openshell.md`

---

## Phase 5: Documentation + README

- ADR 0029: OpenShell driver integration decision
- ADR 0030: Security policy objects
- `docs/guides/openshell.md` — setup guide (install OpenShell, configure driver, policy examples)
- Update README with OpenShell as third driver option

---

## Dependencies

All optional (graceful failure at resolve time):
- **Python**: `grpcio` — `pip install agent-harness[openshell]`
- **TypeScript**: `@grpc/grpc-js`, `@grpc/proto-loader` — optional peer deps
- **PHP**: `grpc` PECL extension, `google/protobuf` — suggested in composer.json

---

## Verification

1. All existing tests pass after Phase 1 refactor
2. New OpenShell driver tests pass with mock gRPC stub (all 3 languages)
3. Live integration test (skipped by default, requires running OpenShell instance):
   - Create sandbox, exec commands, verify output
   - VFS sync round-trip
   - Policy enforcement
   - Cleanup on close
4. Existing bashkit driver tests still pass (regression)
