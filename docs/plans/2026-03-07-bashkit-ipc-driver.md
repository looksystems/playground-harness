# BashkitIPCDriver Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `BashkitIPCDriver` across Python, TypeScript, and PHP — a `ShellDriver` that communicates with `bashkit-cli` via JSON-RPC over stdin/stdout.

**Architecture:** Each language gets a `BashkitIPCDriver` class implementing the `ShellDriver` contract. The driver spawns `bashkit-cli --jsonrpc` as a long-lived subprocess. On each `exec()`, it snapshots the host `FilesystemDriver` into a JSON dict, sends an exec request, handles bidirectional JSON-RPC (dispatching `invoke_command` callbacks for custom commands), applies FS changes from the response back to the host FS, and returns an `ExecResult`. A `BashkitDriver` resolver auto-selects native (future Phase 3) or IPC based on availability. All three languages register `"bashkit"` with `ShellDriverFactory`.

**Tech Stack:** Python (`subprocess.Popen`), TypeScript (`child_process.spawn`), PHP (`proc_open`). JSON-RPC 2.0 over stdin/stdout. No external dependencies beyond `bashkit-cli` binary.

**References:**
- ADR 0027: `docs/adr/0027-bashkit-driver-integration.md`
- Design doc: `docs/plans/2026-03-06-shell-driver-architecture-design.md`
- Existing contracts: `src/{python,typescript,php}/drivers.{py,ts}` and PHP split files

---

## Conventions

- Python: `snake_case`, `ExecResult(stdout=..., stderr=..., exit_code=...)`
- TypeScript: `camelCase`, `{ stdout, stderr, exitCode }`
- PHP: `camelCase` methods, `new ExecResult(stdout: ..., stderr: ..., exitCode: ...)`
- Test commands: Python `.venv/bin/pytest`, TypeScript `npx vitest run`, PHP `php vendor/bin/phpunit`

---

## Task 1: Python — BashkitIPCDriver with VFS Sync

**Files:**
- Create: `src/python/bashkit_ipc_driver.py`
- Create: `tests/python/test_bashkit_ipc_driver.py`

### Step 1: Write the failing tests

Create `tests/python/test_bashkit_ipc_driver.py`:

```python
import json
import os
import subprocess
from unittest.mock import MagicMock, patch, PropertyMock

import pytest

from src.python.drivers import (
    FilesystemDriver,
    BuiltinFilesystemDriver,
    ShellDriver,
    ShellDriverFactory,
)
from src.python.shell import ExecResult
from src.python.bashkit_ipc_driver import BashkitIPCDriver


class FakeProcess:
    """Simulates a bashkit-cli subprocess for testing."""

    def __init__(self):
        self.stdin = MagicMock()
        self.stdout = MagicMock()
        self.returncode = None
        self._responses = []
        self._written = []
        self.stdin.write = self._capture_write
        self.stdin.flush = MagicMock()
        self.stdout.readline = self._read_response

    def _capture_write(self, data):
        self._written.append(data)

    def _read_response(self):
        if self._responses:
            return self._responses.pop(0)
        return b""

    def enqueue_response(self, obj):
        self._responses.append(json.dumps(obj).encode() + b"\n")

    def last_request(self):
        if not self._written:
            return None
        return json.loads(self._written[-1].decode())

    def poll(self):
        return self.returncode

    def terminate(self):
        self.returncode = -15

    def wait(self):
        pass


class TestBashkitIPCDriverContract:
    """BashkitIPCDriver implements ShellDriver."""

    def test_implements_shell_driver(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        assert isinstance(driver, ShellDriver)

    def test_has_fs_property(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        assert isinstance(driver.fs, FilesystemDriver)

    def test_cwd_default(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        assert driver.cwd == "/"

    def test_cwd_custom(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver(cwd="/tmp")
        assert driver.cwd == "/tmp"

    def test_env_default(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        assert driver.env == {}

    def test_env_custom(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver(env={"X": "1"})
        assert driver.env["X"] == "1"


class TestBashkitIPCDriverVfsSync:
    """VFS snapshot sent in exec request, changes applied from response."""

    def test_exec_sends_fs_snapshot(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "result": {"stdout": "", "stderr": "", "exitCode": 0, "fs_changes": {}},
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.fs.write("/hello.txt", "world")
        driver.exec("echo hi")
        req = proc.last_request()
        assert req["method"] == "exec"
        assert req["params"]["fs"]["/hello.txt"] == "world"

    def test_exec_returns_exec_result(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "result": {"stdout": "hi\n", "stderr": "", "exitCode": 0, "fs_changes": {}},
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        result = driver.exec("echo hi")
        assert isinstance(result, ExecResult)
        assert result.stdout == "hi\n"
        assert result.exit_code == 0

    def test_exec_applies_fs_creates(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {"/new.txt": "new content"}, "deleted": []},
            },
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.exec("touch /new.txt")
        assert driver.fs.read("/new.txt") == "new content"

    def test_exec_applies_fs_deletes(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": ["/gone.txt"]},
            },
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.fs.write("/gone.txt", "bye")
        driver.exec("rm /gone.txt")
        assert not driver.fs.exists("/gone.txt")

    def test_exec_resolves_lazy_files_in_snapshot(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "result": {"stdout": "", "stderr": "", "exitCode": 0, "fs_changes": {}},
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.fs.write_lazy("/lazy.txt", lambda: "resolved")
        driver.exec("cat /lazy.txt")
        req = proc.last_request()
        assert req["params"]["fs"]["/lazy.txt"] == "resolved"


class TestBashkitIPCDriverCallbacks:
    """Bidirectional JSON-RPC for custom command callbacks."""

    def test_register_and_invoke_command(self):
        proc = FakeProcess()
        # bashkit will first request callback, then return exec result
        proc.enqueue_response({
            "id": 100,
            "method": "invoke_command",
            "params": {"name": "greet", "args": [], "stdin": ""},
        })
        proc.enqueue_response({
            "id": 1,
            "result": {"stdout": "hello\n", "stderr": "", "exitCode": 0, "fs_changes": {}},
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.register_command("greet", lambda args, stdin: ExecResult(stdout="hello\n"))
        result = driver.exec("greet")
        assert result.stdout == "hello\n"
        # Verify callback response was sent
        callback_response = proc._written[-2]  # second-to-last write
        resp = json.loads(callback_response.decode())
        assert resp["id"] == 100
        assert resp["result"] == "hello\n"

    def test_unregister_command(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        driver.register_command("tmp", lambda args, stdin: ExecResult(stdout="x"))
        driver.unregister_command("tmp")
        assert "tmp" not in driver._commands

    def test_register_command_sends_to_process(self):
        proc = FakeProcess()
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        driver.register_command("mycmd", lambda args, stdin: ExecResult(stdout="ok"))
        req = proc.last_request()
        assert req["method"] == "register_command"
        assert req["params"]["name"] == "mycmd"


class TestBashkitIPCDriverLifecycle:
    """Process lifecycle management."""

    def test_clone_creates_independent_driver(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver(cwd="/home")
        driver.fs.write("/a.txt", "a")
        cloned = driver.clone()
        cloned.fs.write("/b.txt", "b")
        assert not driver.fs.exists("/b.txt")
        assert cloned.fs.exists("/a.txt")
        assert isinstance(cloned, ShellDriver)

    def test_on_not_found_property(self):
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
            driver = BashkitIPCDriver()
        assert driver.on_not_found is None
        cb = lambda name: None
        driver.on_not_found = cb
        assert driver.on_not_found is cb

    def test_exec_error_response(self):
        proc = FakeProcess()
        proc.enqueue_response({
            "id": 1,
            "error": {"code": -1, "message": "parse error"},
        })
        with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn", return_value=proc):
            driver = BashkitIPCDriver()
        result = driver.exec("bad syntax {{")
        assert result.exit_code != 0
        assert "parse error" in result.stderr
```

### Step 2: Run tests to verify they fail

Run: `.venv/bin/pytest tests/python/test_bashkit_ipc_driver.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'src.python.bashkit_ipc_driver'`

### Step 3: Implement BashkitIPCDriver

Create `src/python/bashkit_ipc_driver.py`:

```python
"""BashkitIPCDriver — ShellDriver backed by bashkit-cli over JSON-RPC."""

from __future__ import annotations

import json
import subprocess
from typing import Any, Callable

from src.python.drivers import (
    FilesystemDriver,
    BuiltinFilesystemDriver,
    ShellDriver,
)
from src.python.shell import ExecResult


class BashkitIPCDriver(ShellDriver):
    """Shell driver that communicates with bashkit-cli via JSON-RPC over stdin/stdout."""

    def __init__(
        self,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        **kwargs: Any,
    ):
        self._cwd = cwd
        self._env = env or {}
        self._fs_driver = BuiltinFilesystemDriver()
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None
        self._next_id = 0
        self._process = self._spawn()

    def _spawn(self) -> subprocess.Popen:
        return subprocess.Popen(
            ["bashkit-cli", "--jsonrpc"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def _next_request_id(self) -> int:
        self._next_id += 1
        return self._next_id

    def _send(self, msg: dict) -> None:
        data = json.dumps(msg).encode() + b"\n"
        self._process.stdin.write(data)
        self._process.stdin.flush()

    def _recv(self) -> dict:
        line = self._process.stdout.readline()
        if not line:
            return {}
        return json.loads(line.decode())

    def _snapshot_fs(self) -> dict[str, str]:
        """Serialize all files from the host FilesystemDriver."""
        snapshot: dict[str, str] = {}
        for path in self._fs_driver.find("/", "*"):
            if not self._fs_driver.is_dir(path):
                try:
                    snapshot[path] = self._fs_driver.read_text(path)
                except Exception:
                    pass
        return snapshot

    def _apply_fs_changes(self, changes: dict) -> None:
        """Apply creates/deletes from bashkit response to host FS."""
        for path, content in changes.get("created", {}).items():
            self._fs_driver.write(path, content)
        for path in changes.get("deleted", []):
            if self._fs_driver.exists(path):
                self._fs_driver.remove(path)

    @property
    def fs(self) -> FilesystemDriver:
        return self._fs_driver

    @property
    def cwd(self) -> str:
        return self._cwd

    @property
    def env(self) -> dict[str, str]:
        return self._env

    @property
    def on_not_found(self) -> Callable | None:
        return self._on_not_found

    @on_not_found.setter
    def on_not_found(self, callback: Callable | None) -> None:
        self._on_not_found = callback

    def exec(self, command: str) -> ExecResult:
        req_id = self._next_request_id()
        self._send({
            "id": req_id,
            "method": "exec",
            "params": {
                "cmd": command,
                "cwd": self._cwd,
                "env": self._env,
                "fs": self._snapshot_fs(),
            },
        })
        # Event loop: handle callback requests until we get the exec result
        while True:
            msg = self._recv()
            if not msg:
                return ExecResult(stderr="bashkit-cli: no response\n", exit_code=1)
            if "method" in msg and msg["method"] == "invoke_command":
                self._handle_callback(msg)
            elif "result" in msg:
                result = msg["result"]
                self._apply_fs_changes(result.get("fs_changes", {}))
                return ExecResult(
                    stdout=result.get("stdout", ""),
                    stderr=result.get("stderr", ""),
                    exit_code=result.get("exitCode", 0),
                )
            elif "error" in msg:
                err = msg["error"]
                return ExecResult(
                    stderr=err.get("message", "unknown error") + "\n",
                    exit_code=1,
                )

    def _handle_callback(self, msg: dict) -> None:
        params = msg.get("params", {})
        name = params.get("name", "")
        args = params.get("args", [])
        stdin = params.get("stdin", "")
        if name in self._commands:
            result = self._commands[name](args, stdin)
            self._send({"id": msg["id"], "result": result.stdout})
        else:
            self._send({"id": msg["id"], "error": {"code": -1, "message": f"unknown command: {name}"}})

    def register_command(self, name: str, handler: Callable) -> None:
        self._commands[name] = handler
        if self._process and self._process.poll() is None:
            self._send({
                "id": self._next_request_id(),
                "method": "register_command",
                "params": {"name": name},
            })

    def unregister_command(self, name: str) -> None:
        self._commands.pop(name, None)

    def clone(self) -> BashkitIPCDriver:
        cloned = BashkitIPCDriver.__new__(BashkitIPCDriver)
        cloned._cwd = self._cwd
        cloned._env = dict(self._env)
        cloned._fs_driver = self._fs_driver.clone()
        cloned._commands = dict(self._commands)
        cloned._on_not_found = self._on_not_found
        cloned._next_id = 0
        cloned._process = cloned._spawn()
        return cloned

    def __del__(self):
        if hasattr(self, "_process") and self._process and self._process.poll() is None:
            self._process.terminate()
            self._process.wait()
```

### Step 4: Run tests to verify they pass

Run: `.venv/bin/pytest tests/python/test_bashkit_ipc_driver.py -q`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/python/bashkit_ipc_driver.py tests/python/test_bashkit_ipc_driver.py
git commit -m "feat(python): add BashkitIPCDriver with VFS sync and callbacks"
```

---

## Task 2: TypeScript — BashkitIPCDriver with VFS Sync

**Files:**
- Create: `src/typescript/bashkit-ipc-driver.ts`
- Create: `tests/typescript/bashkit-ipc-driver.test.ts`

### Step 1: Write the failing tests

Create `tests/typescript/bashkit-ipc-driver.test.ts`:

```typescript
import { describe, it, expect, vi, beforeEach } from "vitest";
import { BashkitIPCDriver } from "../src/typescript/bashkit-ipc-driver.js";
import type { ShellDriver, FilesystemDriver } from "../src/typescript/drivers.js";
import type { ExecResult } from "../src/typescript/shell.js";

class FakeProcess {
  stdin = { write: vi.fn(), end: vi.fn() };
  stdout: { once: ReturnType<typeof vi.fn>; on: ReturnType<typeof vi.fn> };
  stderr = { on: vi.fn() };
  killed = false;
  private _responses: string[] = [];
  private _lineHandler?: (line: string) => void;

  constructor() {
    this.stdout = {
      once: vi.fn(),
      on: vi.fn((event: string, handler: (data: Buffer) => void) => {
        if (event === "data") {
          // not used in line-based protocol
        }
      }),
    };
  }

  enqueueResponse(obj: Record<string, unknown>): void {
    this._responses.push(JSON.stringify(obj));
  }

  simulateResponses(): void {
    for (const resp of this._responses) {
      if (this._lineHandler) {
        this._lineHandler(resp);
      }
    }
    this._responses = [];
  }

  setLineHandler(handler: (line: string) => void): void {
    this._lineHandler = handler;
  }

  lastRequest(): Record<string, unknown> | null {
    const calls = this.stdin.write.mock.calls;
    if (calls.length === 0) return null;
    const last = calls[calls.length - 1][0] as string;
    return JSON.parse(last.trim());
  }

  kill(): void {
    this.killed = true;
  }
}

function createDriverWithFake(opts: { cwd?: string; env?: Record<string, string> } = {}): {
  driver: BashkitIPCDriver;
  proc: FakeProcess;
} {
  const proc = new FakeProcess();
  const driver = new BashkitIPCDriver({
    ...opts,
    _spawnOverride: () => proc as any,
  });
  return { driver, proc };
}

describe("BashkitIPCDriver contract", () => {
  it("implements ShellDriver", () => {
    const { driver } = createDriverWithFake();
    expect(driver.exec).toBeTypeOf("function");
    expect(driver.registerCommand).toBeTypeOf("function");
    expect(driver.unregisterCommand).toBeTypeOf("function");
    expect(driver.clone).toBeTypeOf("function");
  });

  it("has fs property", () => {
    const { driver } = createDriverWithFake();
    expect(driver.fs).toBeDefined();
    expect(driver.fs.write).toBeTypeOf("function");
  });

  it("cwd defaults to /", () => {
    const { driver } = createDriverWithFake();
    expect(driver.cwd).toBe("/");
  });

  it("cwd can be customized", () => {
    const { driver } = createDriverWithFake({ cwd: "/tmp" });
    expect(driver.cwd).toBe("/tmp");
  });

  it("env defaults to empty", () => {
    const { driver } = createDriverWithFake();
    expect(driver.env).toEqual({});
  });

  it("env can be customized", () => {
    const { driver } = createDriverWithFake({ env: { X: "1" } });
    expect(driver.env.X).toBe("1");
  });
});

describe("BashkitIPCDriver VFS sync", () => {
  it("sends fs snapshot in exec request", () => {
    const { driver, proc } = createDriverWithFake();
    driver.fs.write("/hello.txt", "world");
    proc.enqueueResponse({
      id: 1,
      result: { stdout: "", stderr: "", exitCode: 0, fs_changes: {} },
    });
    const result = driver.exec("echo hi");
    const req = proc.lastRequest();
    expect(req!.method).toBe("exec");
    expect((req!.params as any).fs["/hello.txt"]).toBe("world");
  });

  it("returns ExecResult", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      result: { stdout: "hi\n", stderr: "", exitCode: 0, fs_changes: {} },
    });
    const result = driver.exec("echo hi");
    expect(result.stdout).toBe("hi\n");
    expect(result.exitCode).toBe(0);
  });

  it("applies fs creates from response", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: { "/new.txt": "content" }, deleted: [] },
      },
    });
    driver.exec("touch /new.txt");
    expect(driver.fs.read("/new.txt")).toBe("content");
  });

  it("applies fs deletes from response", () => {
    const { driver, proc } = createDriverWithFake();
    driver.fs.write("/gone.txt", "bye");
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: ["/gone.txt"] },
      },
    });
    driver.exec("rm /gone.txt");
    expect(driver.fs.exists("/gone.txt")).toBe(false);
  });
});

describe("BashkitIPCDriver callbacks", () => {
  it("registers and invokes custom command", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("greet", (_args, _stdin) => ({
      stdout: "hello\n",
      stderr: "",
      exitCode: 0,
    }));
    proc.enqueueResponse({
      id: 100,
      method: "invoke_command",
      params: { name: "greet", args: [], stdin: "" },
    });
    proc.enqueueResponse({
      id: 1,
      result: { stdout: "hello\n", stderr: "", exitCode: 0, fs_changes: {} },
    });
    const result = driver.exec("greet");
    expect(result.stdout).toBe("hello\n");
  });

  it("unregisters command", () => {
    const { driver } = createDriverWithFake();
    driver.registerCommand("tmp", () => ({ stdout: "x", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("tmp");
    expect((driver as any)._commands.has("tmp")).toBe(false);
  });

  it("sends register_command to process", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("mycmd", () => ({ stdout: "", stderr: "", exitCode: 0 }));
    const req = proc.lastRequest();
    expect(req!.method).toBe("register_command");
    expect((req!.params as any).name).toBe("mycmd");
  });
});

describe("BashkitIPCDriver lifecycle", () => {
  it("clone creates independent driver", () => {
    const { driver } = createDriverWithFake();
    driver.fs.write("/a.txt", "a");
    const cloned = driver.clone();
    cloned.fs.write("/b.txt", "b");
    expect(driver.fs.exists("/b.txt")).toBe(false);
    expect(cloned.fs.exists("/a.txt")).toBe(true);
  });

  it("onNotFound property", () => {
    const { driver } = createDriverWithFake();
    expect(driver.onNotFound).toBeUndefined();
    const cb = (_name: string) => {};
    driver.onNotFound = cb;
    expect(driver.onNotFound).toBe(cb);
  });

  it("error response returns non-zero exit code", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      error: { code: -1, message: "parse error" },
    });
    const result = driver.exec("bad {{");
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr).toContain("parse error");
  });
});
```

### Step 2: Run tests to verify they fail

Run: `npx vitest run tests/typescript/bashkit-ipc-driver.test.ts`
Expected: FAIL — cannot find module `bashkit-ipc-driver`

### Step 3: Implement BashkitIPCDriver

Create `src/typescript/bashkit-ipc-driver.ts`:

```typescript
import { spawn, type ChildProcess } from "child_process";
import {
  type FilesystemDriver,
  BuiltinFilesystemDriver,
  type ShellDriver,
  type ShellDriverOptions,
} from "./drivers.js";
import { type ExecResult, type CmdHandler, makeResult } from "./shell.js";

export interface BashkitIPCDriverOptions extends ShellDriverOptions {
  _spawnOverride?: () => ChildProcess;
}

export class BashkitIPCDriver implements ShellDriver {
  private _fsDriver: BuiltinFilesystemDriver;
  private _cwd: string;
  private _env: Record<string, string>;
  private _commands = new Map<string, CmdHandler>();
  private _process: ChildProcess | any;
  private _nextId = 0;
  private _buffer = "";
  private _pendingLines: string[] = [];
  onNotFound?: (cmdName: string) => void;

  constructor(opts: BashkitIPCDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ? { ...opts.env } : {};
    this._fsDriver = new BuiltinFilesystemDriver();
    if (opts._spawnOverride) {
      this._process = opts._spawnOverride();
    } else {
      this._process = this._spawn();
    }
  }

  private _spawn(): ChildProcess {
    return spawn("bashkit-cli", ["--jsonrpc"], {
      stdio: ["pipe", "pipe", "pipe"],
    });
  }

  private _nextRequestId(): number {
    return ++this._nextId;
  }

  private _send(msg: Record<string, unknown>): void {
    const data = JSON.stringify(msg) + "\n";
    this._process.stdin.write(data);
  }

  private _recv(): Record<string, unknown> {
    if (this._pendingLines.length > 0) {
      return JSON.parse(this._pendingLines.shift()!);
    }
    // In synchronous mode, read from enqueued responses (for testing via fake)
    // In real usage, this would be async — but the ShellDriver contract is sync
    const line = this._process.stdout?.readline?.();
    if (!line) return {};
    return JSON.parse(typeof line === "string" ? line : line.toString());
  }

  private _snapshotFs(): Record<string, string> {
    const snapshot: Record<string, string> = {};
    for (const path of this._fsDriver.find("/", "*")) {
      if (!this._fsDriver.isDir(path)) {
        try {
          snapshot[path] = this._fsDriver.readText(path);
        } catch {
          // skip unreadable files
        }
      }
    }
    return snapshot;
  }

  private _applyFsChanges(changes: Record<string, unknown>): void {
    const created = (changes.created ?? {}) as Record<string, string>;
    const deleted = (changes.deleted ?? []) as string[];
    for (const [path, content] of Object.entries(created)) {
      this._fsDriver.write(path, content);
    }
    for (const path of deleted) {
      if (this._fsDriver.exists(path)) {
        this._fsDriver.remove(path);
      }
    }
  }

  get fs(): FilesystemDriver {
    return this._fsDriver;
  }

  get cwd(): string {
    return this._cwd;
  }

  get env(): Record<string, string> {
    return this._env;
  }

  exec(command: string): ExecResult {
    const reqId = this._nextRequestId();
    this._send({
      id: reqId,
      method: "exec",
      params: {
        cmd: command,
        cwd: this._cwd,
        env: this._env,
        fs: this._snapshotFs(),
      },
    });

    // Drain enqueued responses (fake process support)
    if (this._process.simulateResponses) {
      this._process.simulateResponses();
    }

    while (true) {
      const msg = this._recv();
      if (!msg || Object.keys(msg).length === 0) {
        return makeResult("", "bashkit-cli: no response\n", 1);
      }
      if ("method" in msg && msg.method === "invoke_command") {
        this._handleCallback(msg);
      } else if ("result" in msg) {
        const result = msg.result as Record<string, unknown>;
        this._applyFsChanges((result.fs_changes ?? {}) as Record<string, unknown>);
        return makeResult(
          (result.stdout as string) ?? "",
          (result.stderr as string) ?? "",
          (result.exitCode as number) ?? 0,
        );
      } else if ("error" in msg) {
        const err = msg.error as Record<string, unknown>;
        return makeResult("", ((err.message as string) ?? "unknown error") + "\n", 1);
      }
    }
  }

  private _handleCallback(msg: Record<string, unknown>): void {
    const params = (msg.params ?? {}) as Record<string, unknown>;
    const name = (params.name as string) ?? "";
    const args = (params.args as string[]) ?? [];
    const stdin = (params.stdin as string) ?? "";
    if (this._commands.has(name)) {
      const result = this._commands.get(name)!(args, stdin);
      this._send({ id: msg.id, result: result.stdout });
    } else {
      this._send({ id: msg.id, error: { code: -1, message: `unknown command: ${name}` } });
    }
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
    this._send({
      id: this._nextRequestId(),
      method: "register_command",
      params: { name },
    });
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
  }

  clone(): BashkitIPCDriver {
    const cloned = Object.create(BashkitIPCDriver.prototype) as BashkitIPCDriver;
    cloned._cwd = this._cwd;
    cloned._env = { ...this._env };
    cloned._fsDriver = this._fsDriver.clone() as BuiltinFilesystemDriver;
    cloned._commands = new Map(this._commands);
    cloned.onNotFound = this.onNotFound;
    cloned._nextId = 0;
    cloned._buffer = "";
    cloned._pendingLines = [];
    cloned._process = cloned._spawn();
    return cloned;
  }

  destroy(): void {
    if (this._process && !this._process.killed) {
      this._process.kill();
    }
  }
}
```

### Step 4: Run tests to verify they pass

Run: `npx vitest run tests/typescript/bashkit-ipc-driver.test.ts`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/typescript/bashkit-ipc-driver.ts tests/typescript/bashkit-ipc-driver.test.ts
git commit -m "feat(typescript): add BashkitIPCDriver with VFS sync and callbacks"
```

---

## Task 3: PHP — BashkitIPCDriver with VFS Sync

**Files:**
- Create: `src/php/BashkitIPCDriver.php`
- Create: `tests/php/BashkitIPCDriverTest.php`

### Step 1: Write the failing tests

Create `tests/php/BashkitIPCDriverTest.php`:

```php
<?php

declare(strict_types=1);

use PHPUnit\Framework\TestCase;
use AgentHarness\BashkitIPCDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\FilesystemDriver;
use AgentHarness\ExecResult;

class FakeProcess
{
    /** @var string[] */
    public array $written = [];
    /** @var string[] */
    private array $responses = [];

    public function enqueueResponse(array $obj): void
    {
        $this->responses[] = json_encode($obj);
    }

    public function write(string $data): void
    {
        $this->written[] = $data;
    }

    public function readline(): ?string
    {
        return array_shift($this->responses);
    }

    public function lastRequest(): ?array
    {
        if (empty($this->written)) {
            return null;
        }
        return json_decode($this->written[count($this->written) - 1], true);
    }

    public function isRunning(): bool
    {
        return true;
    }

    public function terminate(): void {}
}

class BashkitIPCDriverTest extends TestCase
{
    private function createDriverWithFake(array $opts = []): array
    {
        $proc = new FakeProcess();
        $driver = new BashkitIPCDriver(processOverride: $proc, ...$opts);
        return [$driver, $proc];
    }

    public function testImplementsShellDriverInterface(): void
    {
        [$driver] = $this->createDriverWithFake();
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testHasFsProperty(): void
    {
        [$driver] = $this->createDriverWithFake();
        $this->assertInstanceOf(FilesystemDriver::class, $driver->fs());
    }

    public function testCwdDefault(): void
    {
        [$driver] = $this->createDriverWithFake();
        $this->assertSame('/', $driver->cwd());
    }

    public function testCwdCustom(): void
    {
        [$driver] = $this->createDriverWithFake(['cwd' => '/tmp']);
        $this->assertSame('/tmp', $driver->cwd());
    }

    public function testEnvDefault(): void
    {
        [$driver] = $this->createDriverWithFake();
        $this->assertSame([], $driver->env());
    }

    public function testEnvCustom(): void
    {
        [$driver] = $this->createDriverWithFake(['env' => ['X' => '1']]);
        $this->assertSame('1', $driver->env()['X']);
    }

    public function testExecSendsFsSnapshot(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $driver->fs()->write('/hello.txt', 'world');
        $proc->enqueueResponse([
            'id' => 1,
            'result' => ['stdout' => '', 'stderr' => '', 'exitCode' => 0, 'fs_changes' => new \stdClass()],
        ]);
        $driver->exec('echo hi');
        $req = $proc->lastRequest();
        $this->assertSame('exec', $req['method']);
        $this->assertSame('world', $req['params']['fs']['/hello.txt']);
    }

    public function testExecReturnsExecResult(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $proc->enqueueResponse([
            'id' => 1,
            'result' => ['stdout' => "hi\n", 'stderr' => '', 'exitCode' => 0, 'fs_changes' => new \stdClass()],
        ]);
        $result = $driver->exec('echo hi');
        $this->assertInstanceOf(ExecResult::class, $result);
        $this->assertSame("hi\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
    }

    public function testExecAppliesFsCreates(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $proc->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => ['/new.txt' => 'content'], 'deleted' => []],
            ],
        ]);
        $driver->exec('touch /new.txt');
        $this->assertSame('content', $driver->fs()->read('/new.txt'));
    }

    public function testExecAppliesFsDeletes(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $driver->fs()->write('/gone.txt', 'bye');
        $proc->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => ['/gone.txt']],
            ],
        ]);
        $driver->exec('rm /gone.txt');
        $this->assertFalse($driver->fs()->exists('/gone.txt'));
    }

    public function testRegisterAndInvokeCommand(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(stdout: "hello\n"));
        $proc->enqueueResponse([
            'id' => 100,
            'method' => 'invoke_command',
            'params' => ['name' => 'greet', 'args' => [], 'stdin' => ''],
        ]);
        $proc->enqueueResponse([
            'id' => 1,
            'result' => ['stdout' => "hello\n", 'stderr' => '', 'exitCode' => 0, 'fs_changes' => new \stdClass()],
        ]);
        $result = $driver->exec('greet');
        $this->assertSame("hello\n", $result->stdout);
    }

    public function testUnregisterCommand(): void
    {
        [$driver] = $this->createDriverWithFake();
        $driver->registerCommand('tmp', fn(array $args, string $stdin) => new ExecResult(stdout: 'x'));
        $driver->unregisterCommand('tmp');
        $this->assertFalse($driver->hasCommand('tmp'));
    }

    public function testRegisterCommandSendsToProcess(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $driver->registerCommand('mycmd', fn(array $args, string $stdin) => new ExecResult(stdout: 'ok'));
        $req = $proc->lastRequest();
        $this->assertSame('register_command', $req['method']);
        $this->assertSame('mycmd', $req['params']['name']);
    }

    public function testCloneCreatesIndependentDriver(): void
    {
        [$driver] = $this->createDriverWithFake();
        $driver->fs()->write('/a.txt', 'a');
        $cloned = $driver->cloneDriver();
        $cloned->fs()->write('/b.txt', 'b');
        $this->assertFalse($driver->fs()->exists('/b.txt'));
        $this->assertTrue($cloned->fs()->exists('/a.txt'));
        $this->assertInstanceOf(ShellDriverInterface::class, $cloned);
    }

    public function testOnNotFoundProperty(): void
    {
        [$driver] = $this->createDriverWithFake();
        $this->assertNull($driver->getOnNotFound());
        $cb = fn(string $name) => null;
        $driver->setOnNotFound($cb);
        $this->assertSame($cb, $driver->getOnNotFound());
    }

    public function testErrorResponse(): void
    {
        [$driver, $proc] = $this->createDriverWithFake();
        $proc->enqueueResponse([
            'id' => 1,
            'error' => ['code' => -1, 'message' => 'parse error'],
        ]);
        $result = $driver->exec('bad {{');
        $this->assertNotSame(0, $result->exitCode);
        $this->assertStringContainsString('parse error', $result->stderr);
    }
}
```

### Step 2: Run tests to verify they fail

Run: `php vendor/bin/phpunit tests/php/BashkitIPCDriverTest.php --no-coverage`
Expected: FAIL — `Class "AgentHarness\BashkitIPCDriver" not found`

### Step 3: Implement BashkitIPCDriver

Create `src/php/BashkitIPCDriver.php`:

```php
<?php

declare(strict_types=1);

namespace AgentHarness;

class BashkitIPCDriver implements ShellDriverInterface
{
    private BuiltinFilesystemDriver $fsDriver;
    private string $cwd;
    /** @var array<string, string> */
    private array $env;
    /** @var array<string, \Closure> */
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    private int $nextId = 0;
    /** @var mixed Process handle or FakeProcess for testing */
    private mixed $process;
    /** @var resource|null */
    private mixed $stdin = null;
    /** @var resource|null */
    private mixed $stdout = null;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        mixed $processOverride = null,
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new BuiltinFilesystemDriver();
        $this->process = $processOverride ?? $this->spawn();
    }

    private function spawn(): mixed
    {
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'r'],
            2 => ['pipe', 'r'],
        ];
        $proc = proc_open(['bashkit-cli', '--jsonrpc'], $descriptors, $pipes);
        $this->stdin = $pipes[0];
        $this->stdout = $pipes[1];
        return $proc;
    }

    private function nextRequestId(): int
    {
        return ++$this->nextId;
    }

    private function send(array $msg): void
    {
        $data = json_encode($msg) . "\n";
        if ($this->process instanceof \AgentHarness\BashkitIPCDriver) {
            // won't happen, just for type safety
        } elseif (is_object($this->process) && method_exists($this->process, 'write')) {
            $this->process->write($data);
        } elseif ($this->stdin !== null) {
            fwrite($this->stdin, $data);
            fflush($this->stdin);
        }
    }

    private function recv(): ?array
    {
        if (is_object($this->process) && method_exists($this->process, 'readline')) {
            $line = $this->process->readline();
            if ($line === null) {
                return null;
            }
            return json_decode($line, true);
        }
        if ($this->stdout !== null) {
            $line = fgets($this->stdout);
            if ($line === false) {
                return null;
            }
            return json_decode($line, true);
        }
        return null;
    }

    /** @return array<string, string> */
    private function snapshotFs(): array
    {
        $snapshot = [];
        foreach ($this->fsDriver->find('/', '*') as $path) {
            if (!$this->fsDriver->isDir($path)) {
                try {
                    $snapshot[$path] = $this->fsDriver->readText($path);
                } catch (\Throwable) {
                    // skip unreadable
                }
            }
        }
        return $snapshot;
    }

    private function applyFsChanges(array $changes): void
    {
        $created = $changes['created'] ?? [];
        $deleted = $changes['deleted'] ?? [];
        if (is_object($created)) {
            $created = (array) $created;
        }
        foreach ($created as $path => $content) {
            $this->fsDriver->write($path, $content);
        }
        foreach ($deleted as $path) {
            if ($this->fsDriver->exists($path)) {
                $this->fsDriver->remove($path);
            }
        }
    }

    public function fs(): FilesystemDriver
    {
        return $this->fsDriver;
    }

    public function cwd(): string
    {
        return $this->cwd;
    }

    public function env(): array
    {
        return $this->env;
    }

    public function exec(string $command): ExecResult
    {
        $reqId = $this->nextRequestId();
        $this->send([
            'id' => $reqId,
            'method' => 'exec',
            'params' => [
                'cmd' => $command,
                'cwd' => $this->cwd,
                'env' => $this->env ?: new \stdClass(),
                'fs' => $this->snapshotFs() ?: new \stdClass(),
            ],
        ]);

        while (true) {
            $msg = $this->recv();
            if ($msg === null) {
                return new ExecResult(stderr: "bashkit-cli: no response\n", exitCode: 1);
            }
            if (isset($msg['method']) && $msg['method'] === 'invoke_command') {
                $this->handleCallback($msg);
            } elseif (isset($msg['result'])) {
                $result = $msg['result'];
                $this->applyFsChanges($result['fs_changes'] ?? []);
                return new ExecResult(
                    stdout: $result['stdout'] ?? '',
                    stderr: $result['stderr'] ?? '',
                    exitCode: $result['exitCode'] ?? 0,
                );
            } elseif (isset($msg['error'])) {
                return new ExecResult(
                    stderr: ($msg['error']['message'] ?? 'unknown error') . "\n",
                    exitCode: 1,
                );
            }
        }
    }

    private function handleCallback(array $msg): void
    {
        $params = $msg['params'] ?? [];
        $name = $params['name'] ?? '';
        $args = $params['args'] ?? [];
        $stdin = $params['stdin'] ?? '';

        if (isset($this->commands[$name])) {
            $result = ($this->commands[$name])($args, $stdin);
            $this->send(['id' => $msg['id'], 'result' => $result->stdout]);
        } else {
            $this->send(['id' => $msg['id'], 'error' => ['code' => -1, 'message' => "unknown command: {$name}"]]);
        }
    }

    public function registerCommand(string $name, \Closure $handler): void
    {
        $this->commands[$name] = $handler;
        $this->send([
            'id' => $this->nextRequestId(),
            'method' => 'register_command',
            'params' => ['name' => $name],
        ]);
    }

    public function unregisterCommand(string $name): void
    {
        unset($this->commands[$name]);
    }

    public function hasCommand(string $name): bool
    {
        return isset($this->commands[$name]);
    }

    public function cloneDriver(): ShellDriverInterface
    {
        $cloned = new self(processOverride: new class {
            public function write(string $data): void {}
            public function readline(): ?string { return null; }
            public function isRunning(): bool { return false; }
            public function terminate(): void {}
        });
        $cloned->cwd = $this->cwd;
        $cloned->env = $this->env;
        $cloned->fsDriver = $this->fsDriver->cloneFs();
        $cloned->commands = $this->commands;
        $cloned->onNotFound = $this->onNotFound;
        return $cloned;
    }

    public function setOnNotFound(?\Closure $callback): void
    {
        $this->onNotFound = $callback;
    }

    public function getOnNotFound(): ?\Closure
    {
        return $this->onNotFound;
    }

    public function __destruct()
    {
        if (is_resource($this->stdin)) {
            fclose($this->stdin);
        }
        if (is_resource($this->stdout)) {
            fclose($this->stdout);
        }
        if (is_resource($this->process)) {
            proc_terminate($this->process);
            proc_close($this->process);
        }
    }
}
```

### Step 4: Run tests to verify they pass

Run: `php vendor/bin/phpunit tests/php/BashkitIPCDriverTest.php --no-coverage`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/php/BashkitIPCDriver.php tests/php/BashkitIPCDriverTest.php
git commit -m "feat(php): add BashkitIPCDriver with VFS sync and callbacks"
```

---

## Task 4: Factory Registration — All Languages

Register `"bashkit"` driver with `ShellDriverFactory` in all three languages, with auto-resolution (native check → IPC fallback → error).

**Files:**
- Create: `src/python/bashkit_driver.py`
- Create: `src/typescript/bashkit-driver.ts`
- Create: `src/php/BashkitDriver.php`
- Create: `tests/python/test_bashkit_driver.py`
- Create: `tests/typescript/bashkit-driver.test.ts`
- Create: `tests/php/BashkitDriverTest.php`

### Step 1: Write the failing tests

**Python** — `tests/python/test_bashkit_driver.py`:

```python
import shutil
from unittest.mock import patch

import pytest

from src.python.drivers import ShellDriverFactory, ShellDriver
from src.python.bashkit_driver import BashkitDriver, register_bashkit_driver
from src.python.bashkit_ipc_driver import BashkitIPCDriver


class TestBashkitDriverResolver:
    def setup_method(self):
        ShellDriverFactory.reset()

    def test_resolve_returns_ipc_when_cli_available(self):
        with patch("shutil.which", return_value="/usr/bin/bashkit-cli"):
            with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
                driver = BashkitDriver.resolve()
        assert isinstance(driver, BashkitIPCDriver)

    def test_resolve_raises_when_nothing_available(self):
        with patch("shutil.which", return_value=None):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                BashkitDriver.resolve()

    def test_register_adds_to_factory(self):
        register_bashkit_driver()
        assert "bashkit" in ShellDriverFactory._registry

    def test_factory_create_bashkit(self):
        register_bashkit_driver()
        with patch("shutil.which", return_value="/usr/bin/bashkit-cli"):
            with patch("src.python.bashkit_ipc_driver.BashkitIPCDriver._spawn"):
                driver = ShellDriverFactory.create("bashkit")
        assert isinstance(driver, BashkitIPCDriver)
```

**TypeScript** — `tests/typescript/bashkit-driver.test.ts`:

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ShellDriverFactory } from "../src/typescript/drivers.js";
import { BashkitDriver, registerBashkitDriver } from "../src/typescript/bashkit-driver.js";
import { BashkitIPCDriver } from "../src/typescript/bashkit-ipc-driver.js";

describe("BashkitDriver resolver", () => {
  beforeEach(() => ShellDriverFactory.reset());
  afterEach(() => ShellDriverFactory.reset());

  it("resolve returns IPC when cli available", () => {
    const driver = BashkitDriver.resolve({
      _cliAvailable: true,
      _spawnOverride: () => ({
        stdin: { write: vi.fn(), end: vi.fn() },
        stdout: { on: vi.fn(), once: vi.fn() },
        stderr: { on: vi.fn() },
        killed: false,
        kill: vi.fn(),
      }) as any,
    });
    expect(driver).toBeInstanceOf(BashkitIPCDriver);
  });

  it("resolve throws when nothing available", () => {
    expect(() => BashkitDriver.resolve({ _cliAvailable: false })).toThrow(
      "bashkit not found",
    );
  });

  it("register adds to factory", () => {
    registerBashkitDriver();
    // factory should accept "bashkit" without throwing "not registered"
    expect(() => ShellDriverFactory.create("bashkit", {
      _cliAvailable: false,
    } as any)).toThrow("bashkit not found");
  });
});
```

**PHP** — `tests/php/BashkitDriverTest.php`:

```php
<?php

declare(strict_types=1);

use PHPUnit\Framework\TestCase;
use AgentHarness\BashkitDriver;
use AgentHarness\BashkitIPCDriver;
use AgentHarness\ShellDriverFactory;
use AgentHarness\ShellDriverInterface;

class BashkitDriverTest extends TestCase
{
    protected function setUp(): void
    {
        ShellDriverFactory::reset();
    }

    public function testResolveReturnsIpcWhenCliAvailable(): void
    {
        $fakeProc = new class {
            public function write(string $data): void {}
            public function readline(): ?string { return null; }
            public function isRunning(): bool { return true; }
            public function terminate(): void {}
        };
        $driver = BashkitDriver::resolve(cliPath: '/usr/bin/bashkit-cli', processOverride: $fakeProc);
        $this->assertInstanceOf(BashkitIPCDriver::class, $driver);
    }

    public function testResolveThrowsWhenNothingAvailable(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');
        BashkitDriver::resolve(cliPath: null);
    }

    public function testRegisterAddsToFactory(): void
    {
        BashkitDriver::register();
        // Should throw "bashkit not found" (not "not registered")
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');
        ShellDriverFactory::create('bashkit');
    }
}
```

### Step 2: Run tests to verify they fail

Run all three:
- `.venv/bin/pytest tests/python/test_bashkit_driver.py -q`
- `npx vitest run tests/typescript/bashkit-driver.test.ts`
- `php vendor/bin/phpunit tests/php/BashkitDriverTest.php --no-coverage`

Expected: All FAIL — modules not found

### Step 3: Implement BashkitDriver resolvers

**Python** — `src/python/bashkit_driver.py`:

```python
"""Bashkit driver resolver — auto-selects native (future) or IPC."""

from __future__ import annotations

import shutil
from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory
from src.python.bashkit_ipc_driver import BashkitIPCDriver


class BashkitDriver:
    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        # Phase 3 will add: if native extension available, return BashkitNativeDriver
        if shutil.which("bashkit-cli"):
            return BashkitIPCDriver(**kwargs)
        raise RuntimeError(
            "bashkit not found — install bashkit-cli or the native extension"
        )


def register_bashkit_driver() -> None:
    ShellDriverFactory.register("bashkit", lambda **kw: BashkitDriver.resolve(**kw))
```

**TypeScript** — `src/typescript/bashkit-driver.ts`:

```typescript
import { type ShellDriver, ShellDriverFactory, type ShellDriverOptions } from "./drivers.js";
import { BashkitIPCDriver, type BashkitIPCDriverOptions } from "./bashkit-ipc-driver.js";

export interface BashkitResolveOptions extends BashkitIPCDriverOptions {
  _cliAvailable?: boolean;
}

export class BashkitDriver {
  static resolve(opts: BashkitResolveOptions = {}): ShellDriver {
    const cliAvailable = opts._cliAvailable ?? BashkitDriver._checkCli();
    // Phase 3 will add: if native extension available, return BashkitNativeDriver
    if (cliAvailable) {
      return new BashkitIPCDriver(opts);
    }
    throw new Error("bashkit not found — install bashkit-cli or the native extension");
  }

  private static _checkCli(): boolean {
    try {
      const { execSync } = require("child_process");
      execSync("which bashkit-cli", { stdio: "ignore" });
      return true;
    } catch {
      return false;
    }
  }
}

export function registerBashkitDriver(): void {
  ShellDriverFactory.register("bashkit", (opts?: ShellDriverOptions) =>
    BashkitDriver.resolve(opts as BashkitResolveOptions),
  );
}
```

**PHP** — `src/php/BashkitDriver.php`:

```php
<?php

declare(strict_types=1);

namespace AgentHarness;

class BashkitDriver
{
    public static function resolve(?string $cliPath = 'auto', mixed $processOverride = null): ShellDriverInterface
    {
        $cliAvailable = $cliPath === 'auto'
            ? self::checkCli()
            : ($cliPath !== null);

        // Phase 3 will add: if native extension available, return BashkitNativeDriver
        if ($cliAvailable) {
            return new BashkitIPCDriver(processOverride: $processOverride);
        }

        throw new \RuntimeException('bashkit not found — install bashkit-cli or the native extension');
    }

    public static function register(): void
    {
        ShellDriverFactory::register('bashkit', function (array $opts = []): ShellDriverInterface {
            return self::resolve();
        });
    }

    private static function checkCli(): bool
    {
        $path = trim((string) shell_exec('which bashkit-cli 2>/dev/null'));
        return $path !== '';
    }
}
```

### Step 4: Run tests to verify they pass

Run all three:
- `.venv/bin/pytest tests/python/test_bashkit_driver.py -q`
- `npx vitest run tests/typescript/bashkit-driver.test.ts`
- `php vendor/bin/phpunit tests/php/BashkitDriverTest.php --no-coverage`

Expected: All tests PASS

### Step 5: Commit

```bash
git add src/python/bashkit_driver.py src/typescript/bashkit-driver.ts src/php/BashkitDriver.php \
        tests/python/test_bashkit_driver.py tests/typescript/bashkit-driver.test.ts tests/php/BashkitDriverTest.php
git commit -m "feat: add BashkitDriver resolver with factory registration (all languages)"
```

---

## Task 5: Full Test Suite — Verify No Regressions

### Step 1: Run all existing tests

```bash
.venv/bin/pytest tests/python/ -q
npx vitest run
php vendor/bin/phpunit tests/php/ --no-coverage
```

Expected: All existing tests PASS (313 Python, 344 TypeScript, 312 PHP) plus new bashkit tests.

### Step 2: Commit (if any adjustments needed)

```bash
git commit -am "fix: address test regressions if any"
```

---

## Task 6: Update ADR and Docs

**Files:**
- Modify: `docs/adr/0027-bashkit-driver-integration.md` (status → "Phase 2 Implemented")
- Modify: `docs/comparison.md` (add bashkit IPC driver to feature matrix)

### Step 1: Update ADR status

Change status from `Proposed` to `Phase 2 Implemented` in ADR 0027.

### Step 2: Update comparison doc

Add a row for BashkitIPCDriver to the driver comparison table in `docs/comparison.md`.

### Step 3: Commit

```bash
git add docs/
git commit -m "docs: update ADR 0027 status and comparison for Phase 2"
```
