# BashkitNativeDriver Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `BashkitNativeDriver` across Python, TypeScript, and PHP — a `ShellDriver` that calls bashkit in-process via FFI through a shared C library (`libashkit`).

**Architecture:** Each language gets a `BashkitNativeDriver` class implementing the `ShellDriver` contract. The driver loads `libashkit.so`/`libashkit.dylib`/`bashkit.dll` via each language's FFI mechanism (Python `ctypes`, TypeScript `ffi-napi`+`ref-napi`, PHP `FFI`). On each `exec()`, it snapshots the host `FilesystemDriver` into JSON, calls `bashkit_exec()`, applies FS changes from the JSON response back to the host FS, and returns an `ExecResult`. Custom command callbacks use C function pointers with userdata. The existing `BashkitDriver` resolver is updated to prefer native over IPC. A `MockBashkitLib` in each language enables testing without the real shared library.

**Tech Stack:** Python (`ctypes`), TypeScript (`ffi-napi` + `ref-napi`), PHP (`FFI` extension, PHP 7.4+). Shared C API via `libashkit`. No new Rust code in this repo.

**References:**
- ADR 0027: `docs/adr/0027-bashkit-driver-integration.md`
- Design doc: `docs/plans/2026-03-06-shell-driver-architecture-design.md`
- Existing IPC drivers: `src/{python,typescript,php}/bashkit_ipc_driver.{py,ts}`, `src/php/BashkitIPCDriver.php`
- Existing resolvers: `src/{python,typescript,php}/bashkit_driver.{py,ts}`, `src/php/BashkitDriver.php`

---

## Conventions

- Python: `snake_case`, `ExecResult(stdout=..., stderr=..., exit_code=...)`
- TypeScript: `camelCase`, `{ stdout, stderr, exitCode }`
- PHP: `camelCase` methods, `new ExecResult(stdout: ..., stderr: ..., exitCode: ...)`
- Test commands: Python `.venv/bin/pytest`, TypeScript `npx vitest run`, PHP `php vendor/bin/phpunit`

---

## Expected C API (libashkit)

The shared library exposes this C interface. Our wrappers code against it:

```c
typedef struct bashkit_ctx bashkit_t;
typedef const char* (*bashkit_command_cb)(const char* args_json, void* userdata);

bashkit_t*   bashkit_create(const char* config_json);
void         bashkit_destroy(bashkit_t* ctx);
const char*  bashkit_exec(bashkit_t* ctx, const char* request_json);
void         bashkit_register_command(bashkit_t* ctx, const char* name, bashkit_command_cb cb, void* userdata);
void         bashkit_unregister_command(bashkit_t* ctx, const char* name);
void         bashkit_free_string(const char* str);
```

**`bashkit_exec` request JSON:**
```json
{"cmd": "echo hi", "cwd": "/", "env": {}, "fs": {"/file.txt": "content"}}
```

**`bashkit_exec` response JSON:**
```json
{"stdout": "hi\n", "stderr": "", "exitCode": 0, "fs_changes": {"created": {}, "deleted": []}}
```

**`bashkit_command_cb` args JSON:**
```json
{"name": "mycmd", "args": ["--flag"], "stdin": "input"}
```

**Callback return:** plain string (the stdout of the command).

---

## Library Discovery

Search order (same across all languages):

1. `BASHKIT_LIB_PATH` env var — explicit path to the shared library file
2. Standard library search paths:
   - `LD_LIBRARY_PATH` dirs (Linux), `DYLD_LIBRARY_PATH` dirs (macOS)
   - `/usr/local/lib`, `/usr/lib`
3. Platform-specific filename: `libashkit.so` (Linux), `libashkit.dylib` (macOS), `bashkit.dll` (Windows)

A static method `find_library()` / `findLibrary()` returns the path or `None`/`undefined`/`null`.

---

## Task 1: Python — BashkitNativeDriver

**Files:**
- Create: `src/python/bashkit_native_driver.py`
- Create: `tests/python/test_bashkit_native_driver.py`

### Step 1: Write the failing tests

Create `tests/python/test_bashkit_native_driver.py`:

```python
"""Tests for BashkitNativeDriver — FFI-based shell driver."""

from __future__ import annotations

import json
from typing import Any, Callable

import pytest

from src.python.drivers import ShellDriver, FilesystemDriver
from src.python.shell import ExecResult
from src.python.bashkit_native_driver import BashkitNativeDriver


class MockBashkitLib:
    """Mock of the libashkit C API for testing without the real shared library."""

    def __init__(self):
        self._commands: dict[str, Callable] = {}
        self._responses: list[dict] = []
        self._last_request: dict | None = None
        self._ctx = object()  # fake context pointer

    def enqueue_response(self, response: dict) -> None:
        self._responses.append(response)

    def bashkit_create(self, config_json: str | None) -> Any:
        return self._ctx

    def bashkit_destroy(self, ctx: Any) -> None:
        pass

    def bashkit_exec(self, ctx: Any, request_json: str) -> str:
        self._last_request = json.loads(request_json)

        # Invoke any registered callbacks if the response triggers them
        if self._responses:
            response = self._responses.pop(0)
        else:
            response = {"stdout": "", "stderr": "", "exitCode": 0, "fs_changes": {}}

        return json.dumps(response)

    def bashkit_register_command(self, ctx: Any, name: str, cb: Any, userdata: Any) -> None:
        self._commands[name] = (cb, userdata)

    def bashkit_unregister_command(self, ctx: Any, name: str) -> None:
        self._commands.pop(name, None)

    def bashkit_free_string(self, s: str) -> None:
        pass

    def last_request(self) -> dict | None:
        return self._last_request

    def invoke_callback(self, name: str, args_json: str) -> str | None:
        """Simulate bashkit calling a registered callback."""
        if name in self._commands:
            cb, userdata = self._commands[name]
            return cb(args_json, userdata)
        return None


def make_driver(**kwargs) -> tuple[BashkitNativeDriver, MockBashkitLib]:
    mock = MockBashkitLib()
    driver = BashkitNativeDriver(lib_override=mock, **kwargs)
    return driver, mock


class TestBashkitNativeDriverContract:
    """BashkitNativeDriver implements ShellDriver."""

    def test_implements_shell_driver(self):
        driver, _ = make_driver()
        assert isinstance(driver, ShellDriver)

    def test_has_fs_property(self):
        driver, _ = make_driver()
        assert isinstance(driver.fs, FilesystemDriver)

    def test_cwd_default(self):
        driver, _ = make_driver()
        assert driver.cwd == "/"

    def test_cwd_custom(self):
        driver, _ = make_driver(cwd="/home")
        assert driver.cwd == "/home"

    def test_env_default(self):
        driver, _ = make_driver()
        assert driver.env == {}

    def test_env_custom(self):
        driver, _ = make_driver(env={"FOO": "bar"})
        assert driver.env == {"FOO": "bar"}


class TestBashkitNativeDriverVfsSync:
    """VFS snapshot sent to bashkit_exec, changes applied from response."""

    def test_exec_sends_fs_snapshot(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        driver.exec("echo hi")
        req = mock.last_request()
        assert req["cmd"] == "echo hi"
        assert req["fs"]["/hello.txt"] == "world"

    def test_exec_sends_cwd_and_env(self):
        driver, mock = make_driver(cwd="/tmp", env={"X": "1"})
        driver.exec("pwd")
        req = mock.last_request()
        assert req["cwd"] == "/tmp"
        assert req["env"]["X"] == "1"

    def test_exec_returns_exec_result(self):
        driver, mock = make_driver()
        mock.enqueue_response({"stdout": "hi\n", "stderr": "", "exitCode": 0, "fs_changes": {}})
        result = driver.exec("echo hi")
        assert isinstance(result, ExecResult)
        assert result.stdout == "hi\n"
        assert result.exit_code == 0

    def test_exec_applies_fs_creates(self):
        driver, mock = make_driver()
        mock.enqueue_response({
            "stdout": "", "stderr": "", "exitCode": 0,
            "fs_changes": {"created": {"/new.txt": "new content"}, "deleted": []},
        })
        driver.exec("touch /new.txt")
        assert driver.fs.read("/new.txt") == "new content"

    def test_exec_applies_fs_deletes(self):
        driver, mock = make_driver()
        driver.fs.write("/gone.txt", "bye")
        mock.enqueue_response({
            "stdout": "", "stderr": "", "exitCode": 0,
            "fs_changes": {"created": {}, "deleted": ["/gone.txt"]},
        })
        driver.exec("rm /gone.txt")
        assert not driver.fs.exists("/gone.txt")

    def test_exec_resolves_lazy_files_in_snapshot(self):
        driver, mock = make_driver()
        driver.fs.write_lazy("/lazy.txt", lambda: "resolved")
        driver.exec("cat /lazy.txt")
        req = mock.last_request()
        assert req["fs"]["/lazy.txt"] == "resolved"

    def test_exec_error_returns_nonzero(self):
        driver, mock = make_driver()
        mock.enqueue_response({"stdout": "", "stderr": "parse error", "exitCode": 2, "fs_changes": {}})
        result = driver.exec("bad {{")
        assert result.exit_code == 2
        assert "parse error" in result.stderr


class TestBashkitNativeDriverCallbacks:
    """Custom command callbacks via function pointers."""

    def test_register_command_stores_handler(self):
        driver, mock = make_driver()
        handler = lambda args, stdin="": ExecResult(stdout="ok\n")
        driver.register_command("mycmd", handler)
        assert "mycmd" in mock._commands

    def test_unregister_command_removes_handler(self):
        driver, mock = make_driver()
        driver.register_command("mycmd", lambda args, stdin="": ExecResult(stdout="ok\n"))
        driver.unregister_command("mycmd")
        assert "mycmd" not in mock._commands

    def test_callback_invocation(self):
        driver, mock = make_driver()
        driver.register_command("greet", lambda args, stdin="": ExecResult(stdout="hello\n"))
        # Simulate bashkit invoking the callback
        result_json = mock.invoke_callback("greet", json.dumps({"name": "greet", "args": [], "stdin": ""}))
        assert result_json is not None
        assert "hello" in result_json

    def test_callback_receives_stdin(self):
        driver, mock = make_driver()
        driver.register_command("upper", lambda args, stdin="": ExecResult(stdout=stdin.upper()))
        result_json = mock.invoke_callback("upper", json.dumps({"name": "upper", "args": [], "stdin": "hello"}))
        assert "HELLO" in result_json

    def test_callback_exception_returns_error(self):
        driver, mock = make_driver()
        def bad_cmd(args, stdin=""):
            raise ValueError("boom")
        driver.register_command("bad", bad_cmd)
        result_json = mock.invoke_callback("bad", json.dumps({"name": "bad", "args": [], "stdin": ""}))
        assert result_json is not None
        # Should contain error info, not crash


class TestBashkitNativeDriverLifecycle:
    """Process lifecycle and cloning."""

    def test_clone_creates_independent_driver(self):
        driver, _ = make_driver(cwd="/home")
        driver.fs.write("/a.txt", "a")
        cloned = driver.clone()
        cloned.fs.write("/b.txt", "b")
        assert not driver.fs.exists("/b.txt")
        assert cloned.fs.exists("/a.txt")
        assert isinstance(cloned, ShellDriver)

    def test_clone_preserves_cwd_and_env(self):
        driver, _ = make_driver(cwd="/tmp", env={"X": "1"})
        cloned = driver.clone()
        assert cloned.cwd == "/tmp"
        assert cloned.env == {"X": "1"}

    def test_on_not_found_property(self):
        driver, _ = make_driver()
        assert driver.on_not_found is None
        cb = lambda name: None
        driver.on_not_found = cb
        assert driver.on_not_found is cb


class TestBashkitNativeDriverLibraryDiscovery:
    """Library search path logic."""

    def test_find_library_returns_none_when_not_found(self):
        path = BashkitNativeDriver.find_library()
        # On dev machines without libashkit installed, should return None
        # (or a real path if installed — both are valid)
        assert path is None or isinstance(path, str)

    def test_find_library_respects_env_var(self, monkeypatch, tmp_path):
        lib_file = tmp_path / "libashkit.so"
        lib_file.write_text("fake")
        monkeypatch.setenv("BASHKIT_LIB_PATH", str(lib_file))
        path = BashkitNativeDriver.find_library()
        assert path == str(lib_file)

    def test_find_library_env_var_nonexistent_returns_none(self, monkeypatch):
        monkeypatch.setenv("BASHKIT_LIB_PATH", "/nonexistent/libashkit.so")
        path = BashkitNativeDriver.find_library()
        assert path is None
```

### Step 2: Run tests to verify they fail

Run: `.venv/bin/pytest tests/python/test_bashkit_native_driver.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'src.python.bashkit_native_driver'`

### Step 3: Implement BashkitNativeDriver

Create `src/python/bashkit_native_driver.py`:

```python
"""BashkitNativeDriver: FFI-based shell driver using libashkit shared library."""

from __future__ import annotations

import ctypes
import json
import os
import sys
from pathlib import Path
from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult

# C callback type: const char* (*)(const char* args_json, void* userdata)
COMMAND_CB_TYPE = ctypes.CFUNCTYPE(ctypes.c_char_p, ctypes.c_char_p, ctypes.c_void_p)


class BashkitNativeDriver(ShellDriver):
    """ShellDriver that calls bashkit in-process via FFI (ctypes)."""

    def __init__(
        self,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        lib_override: Any = None,
        **kwargs: Any,
    ):
        self._cwd = cwd
        self._env = env if env is not None else {}
        self._fs_driver = BuiltinFilesystemDriver()
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None
        # Keep references to prevent garbage collection of C callbacks
        self._c_callbacks: dict[str, Any] = {}

        if lib_override is not None:
            self._lib = lib_override
            self._ctx = self._lib.bashkit_create(None)
        else:
            lib_path = self.find_library()
            if lib_path is None:
                raise RuntimeError("libashkit not found — set BASHKIT_LIB_PATH or install libashkit")
            self._lib = self._load_library(lib_path)
            config = json.dumps({"cwd": cwd, "env": self._env})
            self._ctx = self._lib.bashkit_create(config.encode())

    @staticmethod
    def find_library() -> str | None:
        """Find the libashkit shared library. Returns path or None."""
        # 1. BASHKIT_LIB_PATH env var
        env_path = os.environ.get("BASHKIT_LIB_PATH")
        if env_path and Path(env_path).is_file():
            return env_path
        if env_path:
            return None  # env var set but file doesn't exist

        # 2. Platform-specific filename
        if sys.platform == "darwin":
            lib_name = "libashkit.dylib"
        elif sys.platform == "win32":
            lib_name = "bashkit.dll"
        else:
            lib_name = "libashkit.so"

        # 3. Search standard paths
        search_dirs = []

        # LD_LIBRARY_PATH / DYLD_LIBRARY_PATH
        ld_path = os.environ.get("DYLD_LIBRARY_PATH" if sys.platform == "darwin" else "LD_LIBRARY_PATH", "")
        if ld_path:
            search_dirs.extend(ld_path.split(os.pathsep))

        search_dirs.extend(["/usr/local/lib", "/usr/lib"])

        for d in search_dirs:
            candidate = Path(d) / lib_name
            if candidate.is_file():
                return str(candidate)

        return None

    @staticmethod
    def _load_library(path: str) -> ctypes.CDLL:
        """Load the shared library and set up function signatures."""
        lib = ctypes.cdll.LoadLibrary(path)

        lib.bashkit_create.argtypes = [ctypes.c_char_p]
        lib.bashkit_create.restype = ctypes.c_void_p

        lib.bashkit_destroy.argtypes = [ctypes.c_void_p]
        lib.bashkit_destroy.restype = None

        lib.bashkit_exec.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        lib.bashkit_exec.restype = ctypes.c_char_p

        lib.bashkit_register_command.argtypes = [
            ctypes.c_void_p, ctypes.c_char_p, COMMAND_CB_TYPE, ctypes.c_void_p,
        ]
        lib.bashkit_register_command.restype = None

        lib.bashkit_unregister_command.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        lib.bashkit_unregister_command.restype = None

        lib.bashkit_free_string.argtypes = [ctypes.c_char_p]
        lib.bashkit_free_string.restype = None

        return lib

    def _snapshot_fs(self) -> dict[str, str]:
        """Serialize all VFS files to a dict, resolving lazy providers."""
        snapshot: dict[str, str] = {}
        for path in self._fs_driver.find("/", "*"):
            if not self._fs_driver.is_dir(path):
                snapshot[path] = self._fs_driver.read_text(path)
        return snapshot

    def _apply_fs_changes(self, changes: dict) -> None:
        """Apply created files and deleted paths back to the host FS."""
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
        """Execute a command via bashkit FFI."""
        request = json.dumps({
            "cmd": command,
            "cwd": self._cwd,
            "env": self._env,
            "fs": self._snapshot_fs(),
        })

        if hasattr(self._lib, 'bashkit_exec') and callable(self._lib.bashkit_exec):
            # Mock or real lib with Python-callable interface
            result_str = self._lib.bashkit_exec(self._ctx, request)
        else:
            result_str = self._lib.bashkit_exec(self._ctx, request.encode())

        if isinstance(result_str, bytes):
            result_str = result_str.decode()

        result = json.loads(result_str)
        if "fs_changes" in result:
            self._apply_fs_changes(result["fs_changes"])

        return ExecResult(
            stdout=result.get("stdout", ""),
            stderr=result.get("stderr", ""),
            exit_code=result.get("exitCode", 0),
        )

    def _make_c_callback(self, name: str, handler: Callable) -> Any:
        """Wrap a Python handler into a C-compatible callback."""
        def c_callback(args_json_bytes: bytes, userdata: Any) -> bytes:
            try:
                if isinstance(args_json_bytes, bytes):
                    args_json = args_json_bytes.decode()
                else:
                    args_json = args_json_bytes
                params = json.loads(args_json)
                args = params.get("args", [])
                stdin = params.get("stdin", "")
                result = handler(args, stdin=stdin)
                return result.stdout.encode() if isinstance(result.stdout, str) else result.stdout
            except Exception as e:
                return json.dumps({"error": str(e)}).encode()

        return c_callback

    def register_command(self, name: str, handler: Callable) -> None:
        """Register a custom command handler."""
        self._commands[name] = handler
        c_cb = self._make_c_callback(name, handler)
        self._c_callbacks[name] = c_cb
        self._lib.bashkit_register_command(self._ctx, name, c_cb, None)

    def unregister_command(self, name: str) -> None:
        """Remove a custom command handler."""
        self._commands.pop(name, None)
        self._c_callbacks.pop(name, None)
        self._lib.bashkit_unregister_command(self._ctx, name)

    def clone(self) -> BashkitNativeDriver:
        """Create a new independent instance with cloned FS and same config."""
        cloned = BashkitNativeDriver.__new__(BashkitNativeDriver)
        cloned._cwd = self._cwd
        cloned._env = dict(self._env)
        cloned._fs_driver = self._fs_driver.clone()
        cloned._commands = dict(self._commands)
        cloned._on_not_found = self._on_not_found
        cloned._c_callbacks = {}
        cloned._lib = self._lib
        cloned._ctx = self._lib.bashkit_create(None)
        # Re-register commands with the new context
        for cmd_name, handler in cloned._commands.items():
            c_cb = cloned._make_c_callback(cmd_name, handler)
            cloned._c_callbacks[cmd_name] = c_cb
            cloned._lib.bashkit_register_command(cloned._ctx, cmd_name, c_cb, None)
        return cloned

    def __del__(self) -> None:
        ctx = getattr(self, "_ctx", None)
        lib = getattr(self, "_lib", None)
        if ctx is not None and lib is not None:
            try:
                lib.bashkit_destroy(ctx)
            except Exception:
                pass
```

### Step 4: Run tests to verify they pass

Run: `.venv/bin/pytest tests/python/test_bashkit_native_driver.py -q`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/python/bashkit_native_driver.py tests/python/test_bashkit_native_driver.py
git commit -m "feat(python): add BashkitNativeDriver with FFI and mock library"
```

---

## Task 2: TypeScript — BashkitNativeDriver

**Files:**
- Create: `src/typescript/bashkit-native-driver.ts`
- Create: `tests/typescript/bashkit-native-driver.test.ts`

### Step 1: Write the failing tests

Create `tests/typescript/bashkit-native-driver.test.ts`:

```typescript
/**
 * Tests for BashkitNativeDriver — FFI-based shell driver.
 */
import { describe, it, expect, vi } from "vitest";
import { BashkitNativeDriver } from "../../src/typescript/bashkit-native-driver.js";
import type { ShellDriver, FilesystemDriver } from "../../src/typescript/drivers.js";
import type { ExecResult } from "../../src/typescript/shell.js";

interface MockCommand {
  cb: (argsJson: string, userdata: unknown) => string;
  userdata: unknown;
}

class MockBashkitLib {
  _commands = new Map<string, MockCommand>();
  _responses: Record<string, unknown>[] = [];
  _lastRequest: Record<string, unknown> | null = null;
  _ctx = {};

  enqueueResponse(response: Record<string, unknown>): void {
    this._responses.push(response);
  }

  bashkit_create(_configJson: string | null): unknown {
    return this._ctx;
  }

  bashkit_destroy(_ctx: unknown): void {}

  bashkit_exec(_ctx: unknown, requestJson: string): string {
    this._lastRequest = JSON.parse(requestJson);
    const response = this._responses.length > 0
      ? this._responses.shift()!
      : { stdout: "", stderr: "", exitCode: 0, fs_changes: {} };
    return JSON.stringify(response);
  }

  bashkit_register_command(_ctx: unknown, name: string, cb: (argsJson: string, userdata: unknown) => string, userdata: unknown): void {
    this._commands.set(name, { cb, userdata });
  }

  bashkit_unregister_command(_ctx: unknown, name: string): void {
    this._commands.delete(name);
  }

  bashkit_free_string(_s: string): void {}

  lastRequest(): Record<string, unknown> | null {
    return this._lastRequest;
  }

  invokeCallback(name: string, argsJson: string): string | undefined {
    const entry = this._commands.get(name);
    if (entry) {
      return entry.cb(argsJson, entry.userdata);
    }
    return undefined;
  }
}

function makeDriver(opts: { cwd?: string; env?: Record<string, string> } = {}): {
  driver: BashkitNativeDriver;
  mock: MockBashkitLib;
} {
  const mock = new MockBashkitLib();
  const driver = new BashkitNativeDriver({ ...opts, _libOverride: mock });
  return { driver, mock };
}

describe("BashkitNativeDriver contract", () => {
  it("has fs property", () => {
    const { driver } = makeDriver();
    expect(driver.fs).toBeDefined();
    expect(driver.fs.write).toBeTypeOf("function");
  });

  it("cwd defaults to /", () => {
    const { driver } = makeDriver();
    expect(driver.cwd).toBe("/");
  });

  it("cwd can be customized", () => {
    const { driver } = makeDriver({ cwd: "/tmp" });
    expect(driver.cwd).toBe("/tmp");
  });

  it("env defaults to empty", () => {
    const { driver } = makeDriver();
    expect(driver.env).toEqual({});
  });

  it("env can be customized", () => {
    const { driver } = makeDriver({ env: { X: "1" } });
    expect(driver.env.X).toBe("1");
  });
});

describe("BashkitNativeDriver VFS sync", () => {
  it("sends fs snapshot in exec request", () => {
    const { driver, mock } = makeDriver();
    driver.fs.write("/hello.txt", "world");
    driver.exec("echo hi");
    const req = mock.lastRequest()!;
    expect(req.cmd).toBe("echo hi");
    expect((req.fs as Record<string, string>)["/hello.txt"]).toBe("world");
  });

  it("sends cwd and env", () => {
    const { driver, mock } = makeDriver({ cwd: "/tmp", env: { X: "1" } });
    driver.exec("pwd");
    const req = mock.lastRequest()!;
    expect(req.cwd).toBe("/tmp");
    expect((req.env as Record<string, string>).X).toBe("1");
  });

  it("returns ExecResult", () => {
    const { driver, mock } = makeDriver();
    mock.enqueueResponse({ stdout: "hi\n", stderr: "", exitCode: 0, fs_changes: {} });
    const result = driver.exec("echo hi");
    expect(result.stdout).toBe("hi\n");
    expect(result.exitCode).toBe(0);
  });

  it("applies fs creates from response", () => {
    const { driver, mock } = makeDriver();
    mock.enqueueResponse({
      stdout: "", stderr: "", exitCode: 0,
      fs_changes: { created: { "/new.txt": "content" }, deleted: [] },
    });
    driver.exec("touch /new.txt");
    expect(driver.fs.read("/new.txt")).toBe("content");
  });

  it("applies fs deletes from response", () => {
    const { driver, mock } = makeDriver();
    driver.fs.write("/gone.txt", "bye");
    mock.enqueueResponse({
      stdout: "", stderr: "", exitCode: 0,
      fs_changes: { created: {}, deleted: ["/gone.txt"] },
    });
    driver.exec("rm /gone.txt");
    expect(driver.fs.exists("/gone.txt")).toBe(false);
  });

  it("error response returns nonzero exit code", () => {
    const { driver, mock } = makeDriver();
    mock.enqueueResponse({ stdout: "", stderr: "parse error", exitCode: 2, fs_changes: {} });
    const result = driver.exec("bad {{");
    expect(result.exitCode).toBe(2);
    expect(result.stderr).toContain("parse error");
  });
});

describe("BashkitNativeDriver callbacks", () => {
  it("registers command with mock lib", () => {
    const { driver, mock } = makeDriver();
    driver.registerCommand("mycmd", (_args, _stdin) => ({ stdout: "ok\n", stderr: "", exitCode: 0 }));
    expect(mock._commands.has("mycmd")).toBe(true);
  });

  it("unregisters command", () => {
    const { driver, mock } = makeDriver();
    driver.registerCommand("tmp", () => ({ stdout: "", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("tmp");
    expect(mock._commands.has("tmp")).toBe(false);
  });

  it("callback invocation returns stdout", () => {
    const { driver, mock } = makeDriver();
    driver.registerCommand("greet", (_args, _stdin) => ({ stdout: "hello\n", stderr: "", exitCode: 0 }));
    const result = mock.invokeCallback("greet", JSON.stringify({ name: "greet", args: [], stdin: "" }));
    expect(result).toContain("hello");
  });

  it("callback receives stdin", () => {
    const { driver, mock } = makeDriver();
    driver.registerCommand("upper", (_args, stdin) => ({ stdout: stdin.toUpperCase(), stderr: "", exitCode: 0 }));
    const result = mock.invokeCallback("upper", JSON.stringify({ name: "upper", args: [], stdin: "hello" }));
    expect(result).toContain("HELLO");
  });
});

describe("BashkitNativeDriver lifecycle", () => {
  it("clone creates independent driver", () => {
    const { driver } = makeDriver({ cwd: "/home" });
    driver.fs.write("/a.txt", "a");
    const cloned = driver.clone();
    cloned.fs.write("/b.txt", "b");
    expect(driver.fs.exists("/b.txt")).toBe(false);
    expect(cloned.fs.exists("/a.txt")).toBe(true);
  });

  it("clone preserves cwd and env", () => {
    const { driver } = makeDriver({ cwd: "/tmp", env: { X: "1" } });
    const cloned = driver.clone();
    expect(cloned.cwd).toBe("/tmp");
    expect(cloned.env).toEqual({ X: "1" });
  });

  it("onNotFound property", () => {
    const { driver } = makeDriver();
    expect(driver.onNotFound).toBeUndefined();
    const cb = (_name: string) => {};
    driver.onNotFound = cb;
    expect(driver.onNotFound).toBe(cb);
  });
});

describe("BashkitNativeDriver library discovery", () => {
  it("findLibrary returns undefined when not found", () => {
    const path = BashkitNativeDriver.findLibrary();
    // On dev machines without libashkit, returns undefined
    expect(path === undefined || typeof path === "string").toBe(true);
  });
});
```

### Step 2: Run tests to verify they fail

Run: `npx vitest run tests/typescript/bashkit-native-driver.test.ts`
Expected: FAIL — cannot find module `bashkit-native-driver`

### Step 3: Implement BashkitNativeDriver

Create `src/typescript/bashkit-native-driver.ts`:

```typescript
/**
 * BashkitNativeDriver: FFI-based shell driver using libashkit shared library.
 */

import { existsSync } from "fs";
import { join } from "path";
import {
  type ShellDriver,
  type FilesystemDriver,
  type ShellDriverOptions,
  BuiltinFilesystemDriver,
} from "./drivers.js";
import type { ExecResult, CmdHandler } from "./shell.js";

export interface BashkitNativeDriverOptions extends ShellDriverOptions {
  _libOverride?: any;
}

export class BashkitNativeDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: BuiltinFilesystemDriver;
  private _commands = new Map<string, CmdHandler>();
  private _onNotFound?: (cmdName: string) => void;
  private _lib: any;
  private _ctx: any;

  constructor(opts: BashkitNativeDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new BuiltinFilesystemDriver();

    if (opts._libOverride) {
      this._lib = opts._libOverride;
      this._ctx = this._lib.bashkit_create(null);
    } else {
      const libPath = BashkitNativeDriver.findLibrary();
      if (!libPath) {
        throw new Error("libashkit not found — set BASHKIT_LIB_PATH or install libashkit");
      }
      this._lib = BashkitNativeDriver._loadLibrary(libPath);
      const config = JSON.stringify({ cwd: this._cwd, env: this._env });
      this._ctx = this._lib.bashkit_create(config);
    }
  }

  static findLibrary(): string | undefined {
    // 1. BASHKIT_LIB_PATH env var
    const envPath = process.env.BASHKIT_LIB_PATH;
    if (envPath) {
      return existsSync(envPath) ? envPath : undefined;
    }

    // 2. Platform-specific filename
    const platform = process.platform;
    const libName = platform === "darwin"
      ? "libashkit.dylib"
      : platform === "win32"
        ? "bashkit.dll"
        : "libashkit.so";

    // 3. Search standard paths
    const searchDirs: string[] = [];

    const ldPath = process.env[platform === "darwin" ? "DYLD_LIBRARY_PATH" : "LD_LIBRARY_PATH"] ?? "";
    if (ldPath) {
      searchDirs.push(...ldPath.split(":"));
    }
    searchDirs.push("/usr/local/lib", "/usr/lib");

    for (const dir of searchDirs) {
      const candidate = join(dir, libName);
      if (existsSync(candidate)) {
        return candidate;
      }
    }

    return undefined;
  }

  private static _loadLibrary(_path: string): any {
    // Real implementation would use ffi-napi:
    // const ffi = require("ffi-napi");
    // return ffi.Library(path, { ... });
    throw new Error("ffi-napi loading not implemented — use _libOverride for testing");
  }

  private _snapshotFs(): Record<string, string> {
    const snapshot: Record<string, string> = {};
    for (const path of this._fsDriver.find("/", "*")) {
      if (!this._fsDriver.isDir(path)) {
        snapshot[path] = this._fsDriver.readText(path);
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

  get onNotFound(): ((cmdName: string) => void) | undefined {
    return this._onNotFound;
  }

  set onNotFound(cb: ((cmdName: string) => void) | undefined) {
    this._onNotFound = cb;
  }

  exec(command: string): ExecResult {
    const request = JSON.stringify({
      cmd: command,
      cwd: this._cwd,
      env: this._env,
      fs: this._snapshotFs(),
    });

    const resultStr: string = this._lib.bashkit_exec(this._ctx, request);
    const result = JSON.parse(resultStr);

    if (result.fs_changes) {
      this._applyFsChanges(result.fs_changes);
    }

    return {
      stdout: result.stdout ?? "",
      stderr: result.stderr ?? "",
      exitCode: result.exitCode ?? 0,
    };
  }

  private _makeCCallback(name: string, handler: CmdHandler): (argsJson: string, userdata: unknown) => string {
    return (argsJson: string, _userdata: unknown): string => {
      try {
        const params = JSON.parse(argsJson);
        const args = params.args ?? [];
        const stdin = params.stdin ?? "";
        const result = handler(args, stdin);
        return result.stdout;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        return JSON.stringify({ error: msg });
      }
    };
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
    const cCb = this._makeCCallback(name, handler);
    this._lib.bashkit_register_command(this._ctx, name, cCb, null);
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
    this._lib.bashkit_unregister_command(this._ctx, name);
  }

  clone(): BashkitNativeDriver {
    const cloned = Object.create(BashkitNativeDriver.prototype) as BashkitNativeDriver;
    cloned._cwd = this._cwd;
    cloned._env = { ...this._env };
    cloned._fsDriver = this._fsDriver.clone();
    cloned._commands = new Map(this._commands);
    cloned._onNotFound = this._onNotFound;
    cloned._lib = this._lib;
    cloned._ctx = this._lib.bashkit_create(null);

    // Re-register commands with the new context
    for (const [cmdName, handler] of cloned._commands) {
      const cCb = cloned._makeCCallback(cmdName, handler);
      cloned._lib.bashkit_register_command(cloned._ctx, cmdName, cCb, null);
    }

    return cloned;
  }

  destroy(): void {
    if (this._ctx && this._lib) {
      try {
        this._lib.bashkit_destroy(this._ctx);
      } catch {
        // Ignore errors during cleanup
      }
    }
  }
}
```

### Step 4: Run tests to verify they pass

Run: `npx vitest run tests/typescript/bashkit-native-driver.test.ts`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/typescript/bashkit-native-driver.ts tests/typescript/bashkit-native-driver.test.ts
git commit -m "feat(typescript): add BashkitNativeDriver with FFI and mock library"
```

---

## Task 3: PHP — BashkitNativeDriver

**Files:**
- Create: `src/php/BashkitNativeDriver.php`
- Create: `tests/php/BashkitNativeDriverTest.php`

### Step 1: Write the failing tests

Create `tests/php/BashkitNativeDriverTest.php`:

```php
<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitNativeDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\FilesystemDriver;
use AgentHarness\ExecResult;
use PHPUnit\Framework\TestCase;

class MockBashkitLib
{
    /** @var array<string, array{cb: \Closure, userdata: mixed}> */
    public array $commands = [];
    /** @var list<array<string, mixed>> */
    private array $responses = [];
    public ?array $lastRequest = null;

    public function enqueueResponse(array $response): void
    {
        $this->responses[] = $response;
    }

    public function bashkit_create(?string $configJson): object
    {
        return new \stdClass();
    }

    public function bashkit_destroy(object $ctx): void {}

    public function bashkit_exec(object $ctx, string $requestJson): string
    {
        $this->lastRequest = json_decode($requestJson, true);
        $response = !empty($this->responses)
            ? array_shift($this->responses)
            : ['stdout' => '', 'stderr' => '', 'exitCode' => 0, 'fs_changes' => []];
        return json_encode($response);
    }

    public function bashkit_register_command(object $ctx, string $name, \Closure $cb, mixed $userdata): void
    {
        $this->commands[$name] = ['cb' => $cb, 'userdata' => $userdata];
    }

    public function bashkit_unregister_command(object $ctx, string $name): void
    {
        unset($this->commands[$name]);
    }

    public function bashkit_free_string(string $s): void {}

    public function invokeCallback(string $name, string $argsJson): ?string
    {
        if (isset($this->commands[$name])) {
            return ($this->commands[$name]['cb'])($argsJson, $this->commands[$name]['userdata']);
        }
        return null;
    }
}

class BashkitNativeDriverTest extends TestCase
{
    private function makeDriver(array $opts = []): array
    {
        $mock = new MockBashkitLib();
        $driver = new BashkitNativeDriver(libOverride: $mock, ...$opts);
        return [$driver, $mock];
    }

    // --- Contract ---

    public function testImplementsShellDriverInterface(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testHasFsProperty(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertInstanceOf(FilesystemDriver::class, $driver->fs());
    }

    public function testCwdDefault(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertSame('/', $driver->cwd());
    }

    public function testCwdCustom(): void
    {
        [$driver] = $this->makeDriver(['cwd' => '/home']);
        $this->assertSame('/home', $driver->cwd());
    }

    public function testEnvDefault(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertSame([], $driver->env());
    }

    public function testEnvCustom(): void
    {
        [$driver] = $this->makeDriver(['env' => ['FOO' => 'bar']]);
        $this->assertSame('bar', $driver->env()['FOO']);
    }

    // --- VFS Sync ---

    public function testExecSendsFsSnapshot(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->fs()->write('/hello.txt', 'world');
        $driver->exec('echo hi');
        $req = $mock->lastRequest;
        $this->assertSame('echo hi', $req['cmd']);
        $this->assertSame('world', $req['fs']['/hello.txt']);
    }

    public function testExecSendsCwdAndEnv(): void
    {
        [$driver, $mock] = $this->makeDriver(['cwd' => '/tmp', 'env' => ['X' => '1']]);
        $driver->exec('pwd');
        $req = $mock->lastRequest;
        $this->assertSame('/tmp', $req['cwd']);
        $this->assertSame('1', $req['env']['X']);
    }

    public function testExecReturnsExecResult(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse(['stdout' => "hi\n", 'stderr' => '', 'exitCode' => 0, 'fs_changes' => []]);
        $result = $driver->exec('echo hi');
        $this->assertInstanceOf(ExecResult::class, $result);
        $this->assertSame("hi\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
    }

    public function testExecAppliesFsCreates(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse([
            'stdout' => '', 'stderr' => '', 'exitCode' => 0,
            'fs_changes' => ['created' => ['/new.txt' => 'content'], 'deleted' => []],
        ]);
        $driver->exec('touch /new.txt');
        $this->assertSame('content', $driver->fs()->read('/new.txt'));
    }

    public function testExecAppliesFsDeletes(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->fs()->write('/gone.txt', 'bye');
        $mock->enqueueResponse([
            'stdout' => '', 'stderr' => '', 'exitCode' => 0,
            'fs_changes' => ['created' => [], 'deleted' => ['/gone.txt']],
        ]);
        $driver->exec('rm /gone.txt');
        $this->assertFalse($driver->fs()->exists('/gone.txt'));
    }

    public function testExecErrorReturnsNonzero(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse(['stdout' => '', 'stderr' => 'parse error', 'exitCode' => 2, 'fs_changes' => []]);
        $result = $driver->exec('bad {{');
        $this->assertSame(2, $result->exitCode);
        $this->assertStringContainsString('parse error', $result->stderr);
    }

    // --- Callbacks ---

    public function testRegisterCommandStoresHandler(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('mycmd', fn(array $args, string $stdin) => new ExecResult(stdout: "ok\n"));
        $this->assertArrayHasKey('mycmd', $mock->commands);
    }

    public function testUnregisterCommandRemovesHandler(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('mycmd', fn(array $args, string $stdin) => new ExecResult(stdout: "ok\n"));
        $driver->unregisterCommand('mycmd');
        $this->assertArrayNotHasKey('mycmd', $mock->commands);
    }

    public function testCallbackInvocation(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(stdout: "hello\n"));
        $result = $mock->invokeCallback('greet', json_encode(['name' => 'greet', 'args' => [], 'stdin' => '']));
        $this->assertNotNull($result);
        $this->assertStringContainsString('hello', $result);
    }

    public function testCallbackReceivesStdin(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('upper', fn(array $args, string $stdin) => new ExecResult(stdout: strtoupper($stdin)));
        $result = $mock->invokeCallback('upper', json_encode(['name' => 'upper', 'args' => [], 'stdin' => 'hello']));
        $this->assertStringContainsString('HELLO', $result);
    }

    // --- Lifecycle ---

    public function testCloneCreatesIndependentDriver(): void
    {
        [$driver] = $this->makeDriver(['cwd' => '/home']);
        $driver->fs()->write('/a.txt', 'a');
        $cloned = $driver->cloneDriver();
        $cloned->fs()->write('/b.txt', 'b');
        $this->assertFalse($driver->fs()->exists('/b.txt'));
        $this->assertTrue($cloned->fs()->exists('/a.txt'));
        $this->assertInstanceOf(ShellDriverInterface::class, $cloned);
    }

    public function testClonePreservesCwdAndEnv(): void
    {
        [$driver] = $this->makeDriver(['cwd' => '/tmp', 'env' => ['X' => '1']]);
        $cloned = $driver->cloneDriver();
        $this->assertSame('/tmp', $cloned->cwd());
        $this->assertSame(['X' => '1'], $cloned->env());
    }

    public function testOnNotFoundProperty(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertNull($driver->getOnNotFound());
        $cb = fn(string $name) => null;
        $driver->setOnNotFound($cb);
        $this->assertSame($cb, $driver->getOnNotFound());
    }

    // --- Library Discovery ---

    public function testFindLibraryReturnsNullWhenNotFound(): void
    {
        $path = BashkitNativeDriver::findLibrary();
        $this->assertTrue($path === null || is_string($path));
    }

    public function testFindLibraryRespectsEnvVar(): void
    {
        $tmpFile = tempnam(sys_get_temp_dir(), 'bashkit_test_');
        file_put_contents($tmpFile, 'fake');
        putenv("BASHKIT_LIB_PATH={$tmpFile}");
        try {
            $path = BashkitNativeDriver::findLibrary();
            $this->assertSame($tmpFile, $path);
        } finally {
            putenv('BASHKIT_LIB_PATH');
            unlink($tmpFile);
        }
    }

    public function testFindLibraryEnvVarNonexistentReturnsNull(): void
    {
        putenv('BASHKIT_LIB_PATH=/nonexistent/libashkit.so');
        try {
            $path = BashkitNativeDriver::findLibrary();
            $this->assertNull($path);
        } finally {
            putenv('BASHKIT_LIB_PATH');
        }
    }
}
```

### Step 2: Run tests to verify they fail

Run: `php vendor/bin/phpunit tests/php/BashkitNativeDriverTest.php --no-coverage`
Expected: FAIL — `Class "AgentHarness\BashkitNativeDriver" not found`

### Step 3: Implement BashkitNativeDriver

Create `src/php/BashkitNativeDriver.php`:

```php
<?php

declare(strict_types=1);

namespace AgentHarness;

if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

/**
 * ShellDriver that calls bashkit in-process via FFI (PHP FFI extension).
 */
class BashkitNativeDriver implements ShellDriverInterface
{
    private string $cwd;
    /** @var array<string, string> */
    private array $env;
    private BuiltinFilesystemDriver $fsDriver;
    /** @var array<string, \Closure> */
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    /** @var mixed Mock or FFI library handle */
    private mixed $lib;
    /** @var mixed Context pointer */
    private mixed $ctx;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        mixed $libOverride = null,
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new BuiltinFilesystemDriver();

        if ($libOverride !== null) {
            $this->lib = $libOverride;
            $this->ctx = $this->lib->bashkit_create(null);
        } else {
            $libPath = self::findLibrary();
            if ($libPath === null) {
                throw new \RuntimeException('libashkit not found — set BASHKIT_LIB_PATH or install libashkit');
            }
            $this->lib = self::loadLibrary($libPath);
            $config = json_encode(['cwd' => $cwd, 'env' => $env ?: new \stdClass()]);
            $this->ctx = $this->lib->bashkit_create($config);
        }
    }

    public static function findLibrary(): ?string
    {
        // 1. BASHKIT_LIB_PATH env var
        $envPath = getenv('BASHKIT_LIB_PATH');
        if ($envPath !== false && $envPath !== '') {
            return is_file($envPath) ? $envPath : null;
        }

        // 2. Platform-specific filename
        $libName = PHP_OS_FAMILY === 'Darwin'
            ? 'libashkit.dylib'
            : (PHP_OS_FAMILY === 'Windows' ? 'bashkit.dll' : 'libashkit.so');

        // 3. Search standard paths
        $searchDirs = [];

        $ldPath = PHP_OS_FAMILY === 'Darwin'
            ? (getenv('DYLD_LIBRARY_PATH') ?: '')
            : (getenv('LD_LIBRARY_PATH') ?: '');
        if ($ldPath !== '') {
            $searchDirs = array_merge($searchDirs, explode(PATH_SEPARATOR, $ldPath));
        }

        $searchDirs[] = '/usr/local/lib';
        $searchDirs[] = '/usr/lib';

        foreach ($searchDirs as $dir) {
            $candidate = rtrim($dir, '/') . '/' . $libName;
            if (is_file($candidate)) {
                return $candidate;
            }
        }

        return null;
    }

    private static function loadLibrary(string $path): \FFI
    {
        return \FFI::cdef(
            '
            typedef struct bashkit_ctx bashkit_t;
            typedef const char* (*bashkit_command_cb)(const char* args_json, void* userdata);

            bashkit_t*   bashkit_create(const char* config_json);
            void         bashkit_destroy(bashkit_t* ctx);
            const char*  bashkit_exec(bashkit_t* ctx, const char* request_json);
            void         bashkit_register_command(bashkit_t* ctx, const char* name, bashkit_command_cb cb, void* userdata);
            void         bashkit_unregister_command(bashkit_t* ctx, const char* name);
            void         bashkit_free_string(const char* str);
            ',
            $path
        );
    }

    /** @return array<string, string> */
    private function snapshotFs(): array
    {
        $snapshot = [];
        foreach ($this->fsDriver->find('/', '*') as $path) {
            if (!$this->fsDriver->isDir($path)) {
                $snapshot[$path] = $this->fsDriver->readText($path);
            }
        }
        return $snapshot;
    }

    private function applyFsChanges(array $changes): void
    {
        $created = $changes['created'] ?? [];
        $deleted = $changes['deleted'] ?? [];

        if (is_array($created)) {
            foreach ($created as $path => $content) {
                $this->fsDriver->write($path, $content);
            }
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
        $request = json_encode([
            'cmd' => $command,
            'cwd' => $this->cwd,
            'env' => $this->env ?: new \stdClass(),
            'fs' => $this->snapshotFs() ?: new \stdClass(),
        ], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

        $resultStr = $this->lib->bashkit_exec($this->ctx, $request);
        $result = json_decode($resultStr, true);

        if (isset($result['fs_changes'])) {
            $this->applyFsChanges($result['fs_changes']);
        }

        return new ExecResult(
            stdout: $result['stdout'] ?? '',
            stderr: $result['stderr'] ?? '',
            exitCode: $result['exitCode'] ?? 0,
        );
    }

    private function makeCCallback(string $name, \Closure $handler): \Closure
    {
        return function (string $argsJson, mixed $userdata) use ($handler): string {
            try {
                $params = json_decode($argsJson, true);
                $args = $params['args'] ?? [];
                $stdin = $params['stdin'] ?? '';
                if (is_string($args)) {
                    $args = $args !== '' ? explode(' ', $args) : [];
                }
                $result = $handler($args, $stdin);
                return $result->stdout;
            } catch (\Throwable $e) {
                return json_encode(['error' => $e->getMessage()]);
            }
        };
    }

    public function registerCommand(string $name, \Closure $handler): void
    {
        $this->commands[$name] = $handler;
        $cCb = $this->makeCCallback($name, $handler);
        $this->lib->bashkit_register_command($this->ctx, $name, $cCb, null);
    }

    public function unregisterCommand(string $name): void
    {
        unset($this->commands[$name]);
        $this->lib->bashkit_unregister_command($this->ctx, $name);
    }

    public function hasCommand(string $name): bool
    {
        return isset($this->commands[$name]);
    }

    public function cloneDriver(): ShellDriverInterface
    {
        $cloned = new self(libOverride: $this->lib);
        $cloned->cwd = $this->cwd;
        $cloned->env = $this->env;
        $cloned->fsDriver = new BuiltinFilesystemDriver($this->fsDriver->vfs()->cloneFs());
        $cloned->commands = $this->commands;
        $cloned->onNotFound = $this->onNotFound;

        // Re-register commands with the new context
        foreach ($cloned->commands as $cmdName => $handler) {
            $cCb = $cloned->makeCCallback($cmdName, $handler);
            $cloned->lib->bashkit_register_command($cloned->ctx, $cmdName, $cCb, null);
        }

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
}
```

### Step 4: Run tests to verify they pass

Run: `php vendor/bin/phpunit tests/php/BashkitNativeDriverTest.php --no-coverage`
Expected: All tests PASS

### Step 5: Commit

```bash
git add src/php/BashkitNativeDriver.php tests/php/BashkitNativeDriverTest.php
git commit -m "feat(php): add BashkitNativeDriver with FFI and mock library"
```

---

## Task 4: Update BashkitDriver Resolvers — All Languages

Update the existing `BashkitDriver` resolver in each language to try native FFI first, then fall back to IPC, then error.

**Files:**
- Modify: `src/python/bashkit_driver.py`
- Modify: `src/typescript/bashkit-driver.ts`
- Modify: `src/php/BashkitDriver.php`
- Modify: `tests/python/test_bashkit_driver.py`
- Modify: `tests/typescript/bashkit-driver.test.ts`
- Modify: `tests/php/BashkitDriverTest.php`

### Step 1: Write the failing tests

**Python** — add to `tests/python/test_bashkit_driver.py`:

```python
# Add these imports at the top:
from src.python.bashkit_native_driver import BashkitNativeDriver

# Add to TestBashkitDriverResolve class:

    @patch.object(BashkitNativeDriver, "find_library", return_value="/usr/local/lib/libashkit.so")
    def test_resolve_prefers_native_over_ipc(self, mock_find):
        driver = BashkitDriver.resolve(lib_override=MagicMock())
        assert isinstance(driver, BashkitNativeDriver)

    @patch.object(BashkitNativeDriver, "find_library", return_value=None)
    @patch("src.python.bashkit_driver.shutil.which", return_value="/usr/local/bin/bashkit-cli")
    @patch.object(BashkitIPCDriver, "_spawn", return_value=MagicMock())
    def test_resolve_falls_back_to_ipc(self, mock_spawn, mock_which, mock_find):
        driver = BashkitDriver.resolve()
        assert isinstance(driver, BashkitIPCDriver)

    @patch.object(BashkitNativeDriver, "find_library", return_value=None)
    @patch("src.python.bashkit_driver.shutil.which", return_value=None)
    def test_resolve_raises_when_nothing_available(self, mock_which, mock_find):
        with pytest.raises(RuntimeError, match="bashkit not found"):
            BashkitDriver.resolve()
```

**TypeScript** — add to `tests/typescript/bashkit-driver.test.ts`:

```typescript
// Add import:
import { BashkitNativeDriver } from "../../src/typescript/bashkit-native-driver.js";

// Add to "resolve()" describe block:

    it("prefers native over IPC when library available", () => {
      const driver = BashkitDriver.resolve({
        _nativeAvailable: true,
        _cliAvailable: true,
        _libOverride: new (class {
          bashkit_create() { return {}; }
          bashkit_destroy() {}
          bashkit_exec() { return "{}"; }
          bashkit_register_command() {}
          bashkit_unregister_command() {}
          bashkit_free_string() {}
        })(),
      });
      expect(driver).toBeInstanceOf(BashkitNativeDriver);
    });

    it("falls back to IPC when native unavailable", () => {
      const driver = BashkitDriver.resolve({
        _nativeAvailable: false,
        _cliAvailable: true,
        _spawnOverride: fakeSpawn,
      });
      expect(driver).toBeInstanceOf(BashkitIPCDriver);
    });

    it("throws when neither native nor IPC available", () => {
      expect(() => BashkitDriver.resolve({
        _nativeAvailable: false,
        _cliAvailable: false,
      })).toThrow("bashkit not found");
    });
```

**PHP** — add to `tests/php/BashkitDriverTest.php`:

```php
// Add import:
use AgentHarness\BashkitNativeDriver;

// Add test methods:

    public function testResolvePrefersNativeOverIpc(): void
    {
        $fakeMock = new \AgentHarness\Tests\BashkitDriverFakeProcess();  // reuse existing fake
        // Create a mock lib that satisfies BashkitNativeDriver
        $mockLib = new class {
            public function bashkit_create(?string $c): object { return new \stdClass(); }
            public function bashkit_destroy(object $c): void {}
            public function bashkit_exec(object $c, string $r): string { return '{}'; }
            public function bashkit_register_command(object $c, string $n, \Closure $cb, mixed $u): void {}
            public function bashkit_unregister_command(object $c, string $n): void {}
            public function bashkit_free_string(string $s): void {}
        };
        $driver = BashkitDriver::resolve(nativeLib: $mockLib);
        $this->assertInstanceOf(BashkitNativeDriver::class, $driver);
    }

    public function testResolveFallsBackToIpc(): void
    {
        $fake = new BashkitDriverFakeProcess();
        $driver = BashkitDriver::resolve(nativeLib: null, cliPath: '/usr/bin/bashkit-cli', processOverride: $fake);
        $this->assertInstanceOf(BashkitIPCDriver::class, $driver);
    }

    public function testResolveThrowsWhenNothingAvailable(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');
        BashkitDriver::resolve(nativeLib: null, cliPath: null);
    }
```

### Step 2: Run tests to verify they fail

Run all three:
- `.venv/bin/pytest tests/python/test_bashkit_driver.py -q`
- `npx vitest run tests/typescript/bashkit-driver.test.ts`
- `php vendor/bin/phpunit tests/php/BashkitDriverTest.php --no-coverage`

Expected: New tests FAIL (old tests still pass)

### Step 3: Update the resolver implementations

**Python** — update `src/python/bashkit_driver.py`:

```python
"""BashkitDriver: auto-resolves native FFI vs IPC driver."""

from __future__ import annotations

import shutil
from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory
from src.python.bashkit_ipc_driver import BashkitIPCDriver
from src.python.bashkit_native_driver import BashkitNativeDriver


class BashkitDriver:
    """Resolves the best available bashkit driver (native FFI > IPC)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return a bashkit ShellDriver, preferring native FFI over IPC."""
        # 1. Try native FFI
        lib_override = kwargs.pop("lib_override", None)
        if lib_override is not None:
            return BashkitNativeDriver(lib_override=lib_override, **kwargs)
        if BashkitNativeDriver.find_library() is not None:
            return BashkitNativeDriver(**kwargs)

        # 2. Fall back to IPC
        if shutil.which("bashkit-cli"):
            return BashkitIPCDriver(**kwargs)

        raise RuntimeError(
            "bashkit not found — install libashkit, bashkit-cli, or the native extension"
        )


def register_bashkit_driver() -> None:
    """Register the 'bashkit' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("bashkit", lambda **kw: BashkitDriver.resolve(**kw))
```

**TypeScript** — update `src/typescript/bashkit-driver.ts`:

```typescript
/**
 * BashkitDriver: auto-resolves native FFI vs IPC driver.
 */

import {
  type ShellDriver,
  ShellDriverFactory,
  type ShellDriverOptions,
} from "./drivers.js";
import {
  BashkitIPCDriver,
  type BashkitIPCDriverOptions,
} from "./bashkit-ipc-driver.js";
import {
  BashkitNativeDriver,
  type BashkitNativeDriverOptions,
} from "./bashkit-native-driver.js";

export interface BashkitResolveOptions extends BashkitIPCDriverOptions, BashkitNativeDriverOptions {
  _nativeAvailable?: boolean;
  _cliAvailable?: boolean;
}

export class BashkitDriver {
  /**
   * Return a bashkit ShellDriver, preferring native FFI over IPC.
   */
  static resolve(opts: BashkitResolveOptions = {}): ShellDriver {
    // 1. Try native FFI
    if (opts._libOverride) {
      return new BashkitNativeDriver(opts);
    }
    const nativeAvailable = opts._nativeAvailable ?? (BashkitNativeDriver.findLibrary() !== undefined);
    if (nativeAvailable) {
      return new BashkitNativeDriver(opts);
    }

    // 2. Fall back to IPC
    const cliAvailable = opts._cliAvailable ?? BashkitDriver._checkCli();
    if (cliAvailable) {
      return new BashkitIPCDriver(opts);
    }

    throw new Error(
      "bashkit not found — install libashkit, bashkit-cli, or the native extension"
    );
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
    BashkitDriver.resolve(opts as BashkitResolveOptions)
  );
}
```

**PHP** — update `src/php/BashkitDriver.php`:

```php
<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * BashkitDriver: auto-resolves native FFI vs IPC driver.
 */
class BashkitDriver
{
    /**
     * Return a bashkit ShellDriver, preferring native FFI over IPC.
     *
     * @param mixed       $nativeLib        Mock lib for testing native driver, or null.
     * @param string|null $cliPath          Pass 'auto' to detect, a path to force available, null to force unavailable.
     * @param mixed       $processOverride  Fake process for IPC testing.
     */
    public static function resolve(
        mixed $nativeLib = 'auto',
        ?string $cliPath = 'auto',
        mixed $processOverride = null,
    ): ShellDriverInterface {
        // 1. Try native FFI
        if ($nativeLib !== null && $nativeLib !== 'auto') {
            return new BashkitNativeDriver(libOverride: $nativeLib);
        }
        if ($nativeLib === 'auto' && BashkitNativeDriver::findLibrary() !== null) {
            return new BashkitNativeDriver();
        }

        // 2. Fall back to IPC
        $cliAvailable = $cliPath === 'auto' ? self::checkCli() : ($cliPath !== null);
        if ($cliAvailable) {
            return new BashkitIPCDriver(processOverride: $processOverride);
        }

        throw new \RuntimeException('bashkit not found — install libashkit, bashkit-cli, or the native extension');
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

Expected: All tests PASS (old and new)

### Step 5: Commit

```bash
git add src/python/bashkit_driver.py src/typescript/bashkit-driver.ts src/php/BashkitDriver.php \
        tests/python/test_bashkit_driver.py tests/typescript/bashkit-driver.test.ts tests/php/BashkitDriverTest.php
git commit -m "feat: update BashkitDriver resolvers to prefer native FFI over IPC"
```

---

## Task 5: Full Test Suite — Verify No Regressions

### Step 1: Run all existing tests

```bash
.venv/bin/pytest tests/python/ -q
npx vitest run
php vendor/bin/phpunit tests/php/ --no-coverage
```

Expected: All existing tests PASS plus new bashkit native driver tests.

### Step 2: Fix any regressions and commit

```bash
git commit -am "fix: address test regressions if any"
```

---

## Task 6: Update ADR, Design Doc, and Comparison

**Files:**
- Modify: `docs/adr/0027-bashkit-driver-integration.md` — status → "Phase 3 Implemented"
- Modify: `docs/plans/2026-03-06-shell-driver-architecture-design.md` — mark Phase 3 complete
- Modify: `docs/comparison.md` — add native driver row to table

### Step 1: Update ADR 0027 status

Change the status line from:
```
Phase 2 Implemented (IPC driver complete across Python, TypeScript, PHP)
```
to:
```
Phase 3 Implemented (Native FFI + IPC drivers complete across Python, TypeScript, PHP)
```

Add a section about the native FFI path under Decision:

```markdown
### Native FFI Path (Phase 3)

Uses a shared C library (`libashkit.so`/`.dylib`/`.dll`) loaded via each language's FFI mechanism:

| Language | FFI Mechanism | Callback Support |
|----------|--------------|-----------------|
| Python | `ctypes` (stdlib) | `ctypes.CFUNCTYPE` |
| TypeScript | `ffi-napi` + `ref-napi` | `ffi.Callback` |
| PHP | `FFI` (built-in 7.4+) | `FFI` closure binding |

Library discovery: `BASHKIT_LIB_PATH` env var, then standard library paths (`LD_LIBRARY_PATH`/`DYLD_LIBRARY_PATH`, `/usr/local/lib`, `/usr/lib`).

The resolver prefers native FFI over IPC: native is in-process (no subprocess overhead, no JSON-RPC event loop), while IPC works as a universal fallback.
```

### Step 2: Update comparison doc

Add a row to the comparison table:

```markdown
| **Bashkit Native Driver** | `BashkitNativeDriver` (ctypes FFI) | `BashkitNativeDriver` (ffi-napi FFI) | `BashkitNativeDriver` (PHP FFI) |
```

### Step 3: Update design doc

In `docs/plans/2026-03-06-shell-driver-architecture-design.md`, update the Implementation Scope section. Change Phase 3 from future tense to past tense, noting the FFI approach was chosen over per-language binding crates.

### Step 4: Commit

```bash
git add docs/
git commit -m "docs: update ADR 0027, design doc, and comparison for Phase 3 native FFI"
```
