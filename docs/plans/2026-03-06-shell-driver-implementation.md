# Shell Driver Architecture Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Introduce `FilesystemDriver` and `ShellDriver` contracts across all three languages, wrap existing VirtualFS and Shell as builtin drivers, wire into HasShell mixin and AgentBuilder with global default + per-agent override.

**Architecture:** Driver/adapter pattern with two open contracts (`FilesystemDriver`, `ShellDriver`). Existing implementations become the `builtin` driver. A `ShellDriverFactory` resolves driver names to instances. HasShell delegates to the driver instead of directly to Shell/VirtualFS.

**Tech Stack:** Python (ABC), TypeScript (interfaces + function mixin), PHP (interfaces + traits)

**Design doc:** `docs/plans/2026-03-06-shell-driver-architecture-design.md`

---

### Task 1: ADR 0026 — Shell Driver Contracts

**Files:**
- Create: `docs/adr/0026-shell-driver-contracts.md`
- Modify: `docs/adr/README.md`

**Step 1: Write ADR**

```markdown
# ADR 0026: Shell and Filesystem Driver Contracts

## Status

Accepted

## Context

The virtual shell and filesystem are implemented independently in Python, TypeScript, and PHP (~2500 lines each). This creates maintenance burden, correctness risk from divergence, and a performance ceiling from interpreted-language implementations.

We want to enable swappable shell backends (e.g., bashkit, a Rust-based POSIX shell) while retaining the existing pure-language implementations as the zero-dependency default.

## Decision

Introduce two open contracts:

**FilesystemDriver** — abstracts the virtual filesystem. Methods: `write`, `write_lazy`, `read`, `read_text`, `exists`, `remove`, `is_dir`, `listdir`, `find`, `stat`, `clone`.

**ShellDriver** — abstracts the shell interpreter. Receives a `FilesystemDriver`. Methods: `exec`, `register_command`, `unregister_command`, `clone`. Properties: `fs`, `cwd`, `env`.

**ShellDriverFactory** — resolves driver names to instances. Supports `register(name, factory)` for user-defined drivers. Global default with per-agent override via `HasShell` init and `AgentBuilder.driver()`.

Existing `VirtualFS` and `Shell` classes become the "builtin" driver implementations, satisfying these contracts without code changes beyond adding the interface declaration.

The contracts are open — users can implement custom drivers (e.g., Docker-backed, WASM-based, or IPC-to-native).

## Consequences

- HasShell mixin delegates to ShellDriver/FilesystemDriver instead of directly to Shell/VirtualFS
- `agent.fs` returns a FilesystemDriver, `agent.shell` returns a ShellDriver
- Existing tests continue to pass — builtin driver has identical behavior
- New drivers can be registered at runtime via ShellDriverFactory
- AgentBuilder gains a `driver(name)` method
- VFS ownership model: host owns, sync before/after exec for external drivers
```

**Step 2: Add to ADR README index**

Add under the Mixins section:

```markdown
| [0026](0026-shell-driver-contracts.md) | Shell and Filesystem Driver Contracts |
```

**Step 3: Commit**

```bash
git add docs/adr/0026-shell-driver-contracts.md docs/adr/README.md
git commit -m "Add ADR 0026: Shell and filesystem driver contracts"
```

---

### Task 2: Python — FilesystemDriver contract

**Files:**
- Create: `src/python/drivers.py`
- Create: `tests/python/test_drivers.py`

**Step 1: Write the failing test**

```python
# tests/python/test_drivers.py
from src.python.drivers import FilesystemDriver, BuiltinFilesystemDriver


class TestFilesystemDriverContract:
    def test_builtin_fs_implements_contract(self):
        fs = BuiltinFilesystemDriver()
        assert isinstance(fs, FilesystemDriver)

    def test_write_and_read(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/test.txt", "hello")
        assert fs.read("/test.txt") == "hello"

    def test_write_lazy(self):
        fs = BuiltinFilesystemDriver()
        fs.write_lazy("/lazy.txt", lambda: "lazy content")
        assert fs.read("/lazy.txt") == "lazy content"

    def test_read_text(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/t.txt", "text")
        assert fs.read_text("/t.txt") == "text"

    def test_exists(self):
        fs = BuiltinFilesystemDriver()
        assert not fs.exists("/nope.txt")
        fs.write("/yes.txt", "y")
        assert fs.exists("/yes.txt")

    def test_remove(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/rm.txt", "bye")
        fs.remove("/rm.txt")
        assert not fs.exists("/rm.txt")

    def test_is_dir(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/dir/file.txt", "x")
        assert fs.is_dir("/dir")
        assert not fs.is_dir("/dir/file.txt")

    def test_listdir(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/d/a.txt", "a")
        fs.write("/d/b.txt", "b")
        assert fs.listdir("/d") == ["a.txt", "b.txt"]

    def test_find(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/src/main.py", "x")
        fs.write("/src/util.py", "y")
        assert fs.find("/src", "*.py") == ["/src/main.py", "/src/util.py"]

    def test_stat(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/s.txt", "hello")
        info = fs.stat("/s.txt")
        assert info["type"] == "file"
        assert info["size"] == 5

    def test_clone(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/a.txt", "a")
        cloned = fs.clone()
        cloned.write("/b.txt", "b")
        assert not fs.exists("/b.txt")
        assert isinstance(cloned, FilesystemDriver)
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_drivers.py::TestFilesystemDriverContract -v`
Expected: FAIL with "cannot import name 'FilesystemDriver'"

**Step 3: Write minimal implementation**

```python
# src/python/drivers.py
"""Shell and filesystem driver contracts."""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any, Callable

from src.python.virtual_fs import VirtualFS


class FilesystemDriver(ABC):
    """Contract for virtual filesystem implementations."""

    @abstractmethod
    def write(self, path: str, content: str | bytes) -> None: ...

    @abstractmethod
    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None: ...

    @abstractmethod
    def read(self, path: str) -> str | bytes: ...

    @abstractmethod
    def read_text(self, path: str) -> str: ...

    @abstractmethod
    def exists(self, path: str) -> bool: ...

    @abstractmethod
    def remove(self, path: str) -> None: ...

    @abstractmethod
    def is_dir(self, path: str) -> bool: ...

    @abstractmethod
    def listdir(self, path: str = "/") -> list[str]: ...

    @abstractmethod
    def find(self, root: str = "/", pattern: str = "*") -> list[str]: ...

    @abstractmethod
    def stat(self, path: str) -> dict[str, Any]: ...

    @abstractmethod
    def clone(self) -> FilesystemDriver: ...


class BuiltinFilesystemDriver(FilesystemDriver):
    """Wraps the existing VirtualFS as a FilesystemDriver."""

    def __init__(self, vfs: VirtualFS | None = None):
        self._vfs = vfs if vfs is not None else VirtualFS()

    def write(self, path: str, content: str | bytes) -> None:
        self._vfs.write(path, content)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._vfs.write_lazy(path, provider)

    def read(self, path: str) -> str | bytes:
        return self._vfs.read(path)

    def read_text(self, path: str) -> str:
        return self._vfs.read_text(path)

    def exists(self, path: str) -> bool:
        return self._vfs.exists(path)

    def remove(self, path: str) -> None:
        self._vfs.remove(path)

    def is_dir(self, path: str) -> bool:
        return self._vfs._is_dir(path)

    def listdir(self, path: str = "/") -> list[str]:
        return self._vfs.listdir(path)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        return self._vfs.find(root, pattern)

    def stat(self, path: str) -> dict[str, Any]:
        return self._vfs.stat(path)

    def clone(self) -> BuiltinFilesystemDriver:
        return BuiltinFilesystemDriver(self._vfs.clone())
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_drivers.py::TestFilesystemDriverContract -v`
Expected: PASS

**Step 5: Commit**

```bash
git add src/python/drivers.py tests/python/test_drivers.py
git commit -m "Add Python FilesystemDriver contract and BuiltinFilesystemDriver"
```

---

### Task 3: Python — ShellDriver contract and factory

**Files:**
- Modify: `src/python/drivers.py`
- Modify: `tests/python/test_drivers.py`

**Step 1: Write the failing test**

```python
# append to tests/python/test_drivers.py
from src.python.drivers import (
    ShellDriver,
    BuiltinShellDriver,
    ShellDriverFactory,
)
from src.python.shell import ExecResult


class TestShellDriverContract:
    def test_builtin_shell_implements_contract(self):
        driver = BuiltinShellDriver()
        assert isinstance(driver, ShellDriver)

    def test_exec(self):
        driver = BuiltinShellDriver()
        driver.fs.write("/test.txt", "hello")
        result = driver.exec("cat /test.txt")
        assert isinstance(result, ExecResult)
        assert result.stdout == "hello"

    def test_register_and_exec_custom_command(self):
        driver = BuiltinShellDriver()
        driver.register_command("greet", lambda args, stdin: ExecResult(stdout="hi\n"))
        result = driver.exec("greet")
        assert result.stdout == "hi\n"

    def test_unregister_custom_command(self):
        driver = BuiltinShellDriver()
        driver.register_command("tmp", lambda args, stdin: ExecResult(stdout="x"))
        driver.unregister_command("tmp")
        result = driver.exec("tmp")
        assert result.exit_code == 127

    def test_clone(self):
        driver = BuiltinShellDriver()
        driver.fs.write("/a.txt", "a")
        cloned = driver.clone()
        cloned.fs.write("/b.txt", "b")
        assert not driver.fs.exists("/b.txt")
        assert isinstance(cloned, ShellDriver)

    def test_cwd_and_env(self):
        driver = BuiltinShellDriver(cwd="/tmp", env={"X": "42"})
        assert driver.cwd == "/tmp"
        assert driver.env["X"] == "42"

    def test_fs_is_filesystem_driver(self):
        driver = BuiltinShellDriver()
        assert isinstance(driver.fs, FilesystemDriver)

    def test_on_not_found_callback(self):
        driver = BuiltinShellDriver()
        not_found = []
        driver.on_not_found = lambda name: not_found.append(name)
        driver.exec("boguscmd")
        assert not_found == ["boguscmd"]


class TestShellDriverFactory:
    def setup_method(self):
        ShellDriverFactory.reset()

    def test_default_is_builtin(self):
        driver = ShellDriverFactory.create()
        assert isinstance(driver, BuiltinShellDriver)

    def test_create_by_name(self):
        driver = ShellDriverFactory.create("builtin")
        assert isinstance(driver, BuiltinShellDriver)

    def test_unknown_driver_raises(self):
        import pytest
        with pytest.raises(KeyError):
            ShellDriverFactory.create("nonexistent")

    def test_register_custom_driver(self):
        class CustomDriver(ShellDriver):
            def __init__(self, **kwargs):
                self._fs = BuiltinFilesystemDriver()
            @property
            def fs(self): return self._fs
            @property
            def cwd(self): return "/"
            @property
            def env(self): return {}
            def exec(self, command): return ExecResult(stdout="custom")
            def register_command(self, name, handler): pass
            def unregister_command(self, name): pass
            def clone(self): return CustomDriver()
            @property
            def on_not_found(self): return None
            @on_not_found.setter
            def on_not_found(self, cb): pass

        ShellDriverFactory.register("custom", lambda **kw: CustomDriver(**kw))
        driver = ShellDriverFactory.create("custom")
        assert isinstance(driver, CustomDriver)
        assert driver.exec("anything").stdout == "custom"

    def test_set_default(self):
        ShellDriverFactory.register("custom", lambda **kw: BuiltinShellDriver(**kw))
        ShellDriverFactory.default = "custom"
        driver = ShellDriverFactory.create()
        assert isinstance(driver, BuiltinShellDriver)

    def test_create_passes_kwargs(self):
        driver = ShellDriverFactory.create("builtin", cwd="/opt", env={"A": "1"})
        assert driver.cwd == "/opt"
        assert driver.env["A"] == "1"
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_drivers.py::TestShellDriverContract -v`
Expected: FAIL with "cannot import name 'ShellDriver'"

**Step 3: Write minimal implementation**

Append to `src/python/drivers.py`:

```python
from src.python.shell import Shell, ExecResult


class ShellDriver(ABC):
    """Contract for shell interpreter implementations."""

    @property
    @abstractmethod
    def fs(self) -> FilesystemDriver: ...

    @property
    @abstractmethod
    def cwd(self) -> str: ...

    @property
    @abstractmethod
    def env(self) -> dict[str, str]: ...

    @abstractmethod
    def exec(self, command: str) -> ExecResult: ...

    @abstractmethod
    def register_command(self, name: str, handler: Callable) -> None: ...

    @abstractmethod
    def unregister_command(self, name: str) -> None: ...

    @abstractmethod
    def clone(self) -> ShellDriver: ...

    @property
    @abstractmethod
    def on_not_found(self) -> Callable | None: ...

    @on_not_found.setter
    @abstractmethod
    def on_not_found(self, callback: Callable | None) -> None: ...


class BuiltinShellDriver(ShellDriver):
    """Wraps the existing Shell as a ShellDriver."""

    def __init__(
        self,
        fs: FilesystemDriver | None = None,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
        max_output: int = 16_000,
        max_iterations: int = 10_000,
    ):
        if fs is not None and isinstance(fs, BuiltinFilesystemDriver):
            vfs = fs._vfs
        else:
            vfs = VirtualFS()
            if fs is not None:
                self._sync_from_driver(fs, vfs)
        self._shell = Shell(
            fs=vfs,
            cwd=cwd,
            env=env or {},
            allowed_commands=allowed_commands,
            max_output=max_output,
            max_iterations=max_iterations,
        )
        self._fs_driver = BuiltinFilesystemDriver(self._shell.fs)
        self._allowed_commands = allowed_commands
        self._max_output = max_output
        self._max_iterations = max_iterations

    @staticmethod
    def _sync_from_driver(src: FilesystemDriver, dest: VirtualFS) -> None:
        for path in src.find("/", "*"):
            dest.write(path, src.read(path))

    @property
    def fs(self) -> FilesystemDriver:
        return self._fs_driver

    @property
    def cwd(self) -> str:
        return self._shell.cwd

    @property
    def env(self) -> dict[str, str]:
        return self._shell.env

    def exec(self, command: str) -> ExecResult:
        return self._shell.exec(command)

    def register_command(self, name: str, handler: Callable) -> None:
        self._shell.register_command(name, handler)

    def unregister_command(self, name: str) -> None:
        self._shell.unregister_command(name)

    def clone(self) -> BuiltinShellDriver:
        cloned_shell = self._shell.clone()
        driver = BuiltinShellDriver.__new__(BuiltinShellDriver)
        driver._shell = cloned_shell
        driver._fs_driver = BuiltinFilesystemDriver(cloned_shell.fs)
        driver._allowed_commands = (
            set(self._allowed_commands) if self._allowed_commands else None
        )
        driver._max_output = self._max_output
        driver._max_iterations = self._max_iterations
        return driver

    @property
    def on_not_found(self) -> Callable | None:
        return self._shell.on_not_found

    @on_not_found.setter
    def on_not_found(self, callback: Callable | None) -> None:
        self._shell.on_not_found = callback


class ShellDriverFactory:
    """Resolves driver names to ShellDriver instances."""

    default: str = "builtin"
    _registry: dict[str, Callable[..., ShellDriver]] = {}

    @classmethod
    def register(cls, name: str, factory: Callable[..., ShellDriver]) -> None:
        cls._registry[name] = factory

    @classmethod
    def create(cls, name: str | None = None, **kwargs: Any) -> ShellDriver:
        name = name or cls.default
        if name == "builtin":
            return BuiltinShellDriver(**kwargs)
        if name not in cls._registry:
            raise KeyError(f"Shell driver '{name}' not registered")
        return cls._registry[name](**kwargs)

    @classmethod
    def reset(cls) -> None:
        cls._registry.clear()
        cls.default = "builtin"
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_drivers.py -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add src/python/drivers.py tests/python/test_drivers.py
git commit -m "Add Python ShellDriver contract, BuiltinShellDriver, and ShellDriverFactory"
```

---

### Task 4: Python — Wire HasShell to use drivers

**Files:**
- Modify: `src/python/has_shell.py`
- Modify: `src/python/agent_builder.py`
- Modify: `tests/python/test_has_shell.py`

**Step 1: Write the failing test**

Add to `tests/python/test_has_shell.py`:

```python
from src.python.drivers import ShellDriver, ShellDriverFactory, BuiltinShellDriver, FilesystemDriver


class TestHasShellDriver:
    def test_default_driver_is_builtin(self):
        obj = ShellOnly()
        assert isinstance(obj.shell, ShellDriver)

    def test_driver_by_name(self):
        obj = ShellOnly()
        obj.__init_has_shell__(driver="builtin")
        assert isinstance(obj.shell, BuiltinShellDriver)

    def test_fs_returns_filesystem_driver(self):
        obj = ShellOnly()
        assert isinstance(obj.fs, FilesystemDriver)

    def test_custom_driver(self):
        ShellDriverFactory.reset()
        ShellDriverFactory.register("custom", lambda **kw: BuiltinShellDriver(**kw))
        obj = ShellOnly()
        obj.__init_has_shell__(driver="custom")
        assert isinstance(obj.shell, BuiltinShellDriver)
        ShellDriverFactory.reset()
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_has_shell.py::TestHasShellDriver -v`
Expected: FAIL — `__init_has_shell__` doesn't accept `driver` parameter

**Step 3: Update HasShell to accept driver parameter**

Modify `src/python/has_shell.py` — add `driver` parameter to `__init_has_shell__`, use `ShellDriverFactory` when no shell instance is provided:

```python
# Updated __init_has_shell__ signature and body:
def __init_has_shell__(
    self,
    shell: str | Shell | None = None,
    driver: str | None = None,
    cwd: str = "/home/user",
    env: dict[str, str] | None = None,
    allowed_commands: set[str] | None = None,
) -> None:
    from src.python.drivers import ShellDriverFactory, BuiltinShellDriver

    if isinstance(shell, str):
        # Registry name — existing behavior, wrap in driver
        from src.python.shell import ShellRegistry
        raw_shell = ShellRegistry.get(shell)
        self._shell = BuiltinShellDriver(
            fs=None,  # will use shell's own fs
            cwd=raw_shell.cwd,
            env=raw_shell.env,
        )
        # Directly assign the raw shell's internals
        self._shell._shell = raw_shell
        self._shell._fs_driver = BuiltinFilesystemDriver(raw_shell.fs)
    elif isinstance(shell, Shell):
        from src.python.drivers import BuiltinFilesystemDriver
        self._shell = BuiltinShellDriver.__new__(BuiltinShellDriver)
        self._shell._shell = shell
        self._shell._fs_driver = BuiltinFilesystemDriver(shell.fs)
    else:
        self._shell = ShellDriverFactory.create(
            driver,
            cwd=cwd,
            env=env or {},
            allowed_commands=allowed_commands,
        )

    if hasattr(self, "_emit"):
        self._shell.on_not_found = lambda cmd_name: (
            emit_fire_and_forget(self, HookEvent.SHELL_NOT_FOUND, cmd_name)
        )

    if hasattr(self, "register_tool"):
        self._register_shell_tool()
```

Update property types and method signatures:

```python
@property
def shell(self) -> ShellDriver:
    self._ensure_has_shell()
    return self._shell

@property
def fs(self) -> FilesystemDriver:
    return self.shell.fs
```

**Step 4: Run ALL has_shell tests to verify no regressions**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/test_has_shell.py -v`
Expected: ALL PASS (including new driver tests and all existing tests)

**Step 5: Update AgentBuilder with driver method**

Add to `src/python/agent_builder.py`:

```python
# New field in __init__:
self._driver: str | None = None

# New method:
def driver(self, name: str) -> Self:
    self._driver = name
    return self

# Update create() — pass driver to shell init:
if self._shell_opts is not None:
    agent.__init_has_shell__(driver=self._driver, **self._shell_opts)
elif self._driver is not None:
    agent.__init_has_shell__(driver=self._driver)
```

**Step 6: Run full test suite**

Run: `cd /Users/adrian/Work/noodlehq/harness && python -m pytest tests/python/ -v`
Expected: ALL PASS

**Step 7: Commit**

```bash
git add src/python/has_shell.py src/python/agent_builder.py tests/python/test_has_shell.py
git commit -m "Wire Python HasShell mixin to use ShellDriver contracts"
```

---

### Task 5: TypeScript — FilesystemDriver and ShellDriver contracts

**Files:**
- Create: `src/typescript/drivers.ts`
- Create: `tests/typescript/drivers.test.ts`

**Step 1: Write the failing test**

```typescript
// tests/typescript/drivers.test.ts
import { describe, it, expect } from "vitest";
import {
  FilesystemDriver,
  BuiltinFilesystemDriver,
  ShellDriver,
  BuiltinShellDriver,
  ShellDriverFactory,
} from "../../src/typescript/drivers.js";

describe("BuiltinFilesystemDriver", () => {
  it("write and read", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/test.txt", "hello");
    expect(fs.read("/test.txt")).toBe("hello");
  });

  it("writeLazy", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.writeLazy("/lazy.txt", () => "lazy");
    expect(fs.read("/lazy.txt")).toBe("lazy");
  });

  it("exists", () => {
    const fs = new BuiltinFilesystemDriver();
    expect(fs.exists("/nope")).toBe(false);
    fs.write("/yes.txt", "y");
    expect(fs.exists("/yes.txt")).toBe(true);
  });

  it("remove", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/rm.txt", "x");
    fs.remove("/rm.txt");
    expect(fs.exists("/rm.txt")).toBe(false);
  });

  it("isDir", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/d/f.txt", "x");
    expect(fs.isDir("/d")).toBe(true);
  });

  it("listdir", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/d/a.txt", "a");
    fs.write("/d/b.txt", "b");
    expect(fs.listdir("/d")).toEqual(["a.txt", "b.txt"]);
  });

  it("find", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/src/a.ts", "a");
    fs.write("/src/b.ts", "b");
    expect(fs.find("/src", "*.ts")).toEqual(["/src/a.ts", "/src/b.ts"]);
  });

  it("stat", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/s.txt", "hello");
    const info = fs.stat("/s.txt");
    expect(info.type).toBe("file");
    expect(info.size).toBe(5);
  });

  it("clone is independent", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/a.txt", "a");
    const cloned = fs.clone();
    cloned.write("/b.txt", "b");
    expect(fs.exists("/b.txt")).toBe(false);
  });
});

describe("BuiltinShellDriver", () => {
  it("exec runs commands", () => {
    const driver = new BuiltinShellDriver();
    driver.fs.write("/test.txt", "hello");
    const result = driver.exec("cat /test.txt");
    expect(result.stdout).toBe("hello");
  });

  it("register and exec custom command", () => {
    const driver = new BuiltinShellDriver();
    driver.registerCommand("greet", () => ({ stdout: "hi\n", stderr: "", exitCode: 0 }));
    expect(driver.exec("greet").stdout).toBe("hi\n");
  });

  it("unregister custom command", () => {
    const driver = new BuiltinShellDriver();
    driver.registerCommand("tmp", () => ({ stdout: "x", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("tmp");
    expect(driver.exec("tmp").exitCode).toBe(127);
  });

  it("clone is independent", () => {
    const driver = new BuiltinShellDriver();
    driver.fs.write("/a.txt", "a");
    const cloned = driver.clone();
    cloned.fs.write("/b.txt", "b");
    expect(driver.fs.exists("/b.txt")).toBe(false);
  });

  it("cwd and env", () => {
    const driver = new BuiltinShellDriver({ cwd: "/tmp", env: { X: "1" } });
    expect(driver.cwd).toBe("/tmp");
    expect(driver.env["X"]).toBe("1");
  });

  it("onNotFound callback", () => {
    const driver = new BuiltinShellDriver();
    const notFound: string[] = [];
    driver.onNotFound = (name) => { notFound.push(name); };
    driver.exec("boguscmd");
    expect(notFound).toEqual(["boguscmd"]);
  });
});

describe("ShellDriverFactory", () => {
  it("default creates builtin", () => {
    ShellDriverFactory.reset();
    const driver = ShellDriverFactory.create();
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
  });

  it("create by name", () => {
    ShellDriverFactory.reset();
    const driver = ShellDriverFactory.create("builtin");
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
  });

  it("unknown driver throws", () => {
    ShellDriverFactory.reset();
    expect(() => ShellDriverFactory.create("nope")).toThrow();
  });

  it("register custom driver", () => {
    ShellDriverFactory.reset();
    ShellDriverFactory.register("custom", (opts) => new BuiltinShellDriver(opts));
    const driver = ShellDriverFactory.create("custom");
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
    ShellDriverFactory.reset();
  });

  it("set default", () => {
    ShellDriverFactory.reset();
    ShellDriverFactory.register("alt", (opts) => new BuiltinShellDriver(opts));
    ShellDriverFactory.default = "alt";
    const driver = ShellDriverFactory.create();
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
    ShellDriverFactory.reset();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/adrian/Work/noodlehq/harness && npx vitest run tests/typescript/drivers.test.ts`
Expected: FAIL — module not found

**Step 3: Write implementation**

```typescript
// src/typescript/drivers.ts
import { VirtualFS } from "./virtual-fs.js";
import { Shell, ExecResult, CmdHandler, ShellOptions } from "./shell.js";

export interface FilesystemDriver {
  write(path: string, content: string): void;
  writeLazy(path: string, provider: () => string): void;
  read(path: string): string;
  readText(path: string): string;
  exists(path: string): boolean;
  remove(path: string): void;
  isDir(path: string): boolean;
  listdir(path: string): string[];
  find(root: string, pattern: string): string[];
  stat(path: string): { path: string; type: string; size?: number };
  clone(): FilesystemDriver;
}

export class BuiltinFilesystemDriver implements FilesystemDriver {
  private _vfs: VirtualFS;

  constructor(vfs?: VirtualFS) {
    this._vfs = vfs ?? new VirtualFS();
  }

  write(path: string, content: string): void { this._vfs.write(path, content); }
  writeLazy(path: string, provider: () => string): void { this._vfs.writeLazy(path, provider); }
  read(path: string): string { return this._vfs.read(path); }
  readText(path: string): string { return this._vfs.read(path); }
  exists(path: string): boolean { return this._vfs.exists(path); }
  remove(path: string): void { this._vfs.remove(path); }
  isDir(path: string): boolean { return this._vfs._isDir(path); }
  listdir(path: string = "/"): string[] { return this._vfs.listdir(path); }
  find(root: string = "/", pattern: string = "*"): string[] { return this._vfs.find(root, pattern); }
  stat(path: string): { path: string; type: string; size?: number } { return this._vfs.stat(path); }
  clone(): BuiltinFilesystemDriver { return new BuiltinFilesystemDriver(this._vfs.clone()); }

  /** Access underlying VirtualFS for Shell interop. */
  get vfs(): VirtualFS { return this._vfs; }
}

export interface ShellDriver {
  readonly fs: FilesystemDriver;
  readonly cwd: string;
  readonly env: Record<string, string>;
  onNotFound?: (cmdName: string) => void;
  exec(command: string): ExecResult;
  registerCommand(name: string, handler: CmdHandler): void;
  unregisterCommand(name: string): void;
  clone(): ShellDriver;
}

export interface ShellDriverOptions {
  cwd?: string;
  env?: Record<string, string>;
  allowedCommands?: Set<string>;
  maxOutput?: number;
  maxIterations?: number;
}

export class BuiltinShellDriver implements ShellDriver {
  private _shell: Shell;
  private _fsDriver: BuiltinFilesystemDriver;
  private _opts: ShellDriverOptions;

  constructor(opts: ShellDriverOptions = {}) {
    this._opts = opts;
    this._shell = new Shell({
      cwd: opts.cwd,
      env: opts.env,
      allowedCommands: opts.allowedCommands,
      maxOutput: opts.maxOutput,
      maxIterations: opts.maxIterations,
    });
    this._fsDriver = new BuiltinFilesystemDriver(this._shell.fs);
  }

  get fs(): FilesystemDriver { return this._fsDriver; }
  get cwd(): string { return this._shell.cwd; }
  get env(): Record<string, string> { return this._shell.env; }

  get onNotFound(): ((cmdName: string) => void) | undefined { return this._shell.onNotFound; }
  set onNotFound(cb: ((cmdName: string) => void) | undefined) { this._shell.onNotFound = cb; }

  exec(command: string): ExecResult { return this._shell.exec(command); }
  registerCommand(name: string, handler: CmdHandler): void { this._shell.registerCommand(name, handler); }
  unregisterCommand(name: string): void { this._shell.unregisterCommand(name); }

  clone(): BuiltinShellDriver {
    const cloned = new BuiltinShellDriver(this._opts);
    const clonedShell = this._shell.clone();
    cloned._shell = clonedShell;
    cloned._fsDriver = new BuiltinFilesystemDriver(clonedShell.fs);
    return cloned;
  }

  /** Access underlying Shell for HasShell interop. */
  get shell(): Shell { return this._shell; }

  /** Create from an existing Shell instance. */
  static fromShell(shell: Shell): BuiltinShellDriver {
    const driver = Object.create(BuiltinShellDriver.prototype) as BuiltinShellDriver;
    driver._shell = shell;
    driver._fsDriver = new BuiltinFilesystemDriver(shell.fs);
    driver._opts = {};
    return driver;
  }
}

export type ShellDriverFactoryFn = (opts?: ShellDriverOptions) => ShellDriver;

export class ShellDriverFactory {
  static default: string = "builtin";
  private static _registry: Map<string, ShellDriverFactoryFn> = new Map();

  static register(name: string, factory: ShellDriverFactoryFn): void {
    ShellDriverFactory._registry.set(name, factory);
  }

  static create(name?: string, opts?: ShellDriverOptions): ShellDriver {
    const driverName = name ?? ShellDriverFactory.default;
    if (driverName === "builtin") {
      return new BuiltinShellDriver(opts);
    }
    const factory = ShellDriverFactory._registry.get(driverName);
    if (!factory) {
      throw new Error(`Shell driver '${driverName}' not registered`);
    }
    return factory(opts);
  }

  static reset(): void {
    ShellDriverFactory._registry.clear();
    ShellDriverFactory.default = "builtin";
  }
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/adrian/Work/noodlehq/harness && npx vitest run tests/typescript/drivers.test.ts`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add src/typescript/drivers.ts tests/typescript/drivers.test.ts
git commit -m "Add TypeScript FilesystemDriver, ShellDriver contracts and factory"
```

---

### Task 6: TypeScript — Wire HasShell to use drivers

**Files:**
- Modify: `src/typescript/has-shell.ts`
- Modify: `src/typescript/agent-builder.ts`
- Modify: `src/typescript/index.ts`

**Step 1: Update HasShell to accept driver option**

Add `driver?: string` to `HasShellOptions`. Update `initHasShell` to create driver via factory when no shell instance is given. Change `_shell` from `Shell` to `ShellDriver`. Update `fs` getter to return `FilesystemDriver`.

Key changes:
- `HasShellOptions.driver?: string`
- `_shell?: ShellDriver` (was `Shell`)
- `initHasShell` uses `ShellDriverFactory.create(opts.driver, ...)` when no shell instance
- When shell instance or registry name given, wrap with `BuiltinShellDriver.fromShell()`
- `exec()` calls `this._shell!.exec()` (unchanged semantics)
- `registerCommand`/`unregisterCommand` delegate to `this._shell!`

**Step 2: Update AgentBuilder with driver method**

Add `_driver?: string` field and `driver(name: string): this` method. Pass driver to shell init.

**Step 3: Update index.ts exports**

Add exports for `FilesystemDriver`, `ShellDriver`, `BuiltinFilesystemDriver`, `BuiltinShellDriver`, `ShellDriverFactory` from `drivers.ts`.

**Step 4: Run ALL TypeScript tests**

Run: `cd /Users/adrian/Work/noodlehq/harness && npx vitest run`
Expected: ALL PASS (no regressions)

**Step 5: Commit**

```bash
git add src/typescript/has-shell.ts src/typescript/agent-builder.ts src/typescript/index.ts
git commit -m "Wire TypeScript HasShell mixin to use ShellDriver contracts"
```

---

### Task 7: PHP — FilesystemDriver and ShellDriver contracts

**Files:**
- Create: `src/php/FilesystemDriver.php`
- Create: `src/php/ShellDriver.php`
- Create: `src/php/BuiltinFilesystemDriver.php`
- Create: `src/php/BuiltinShellDriver.php`
- Create: `src/php/ShellDriverFactory.php`
- Create: `tests/php/DriversTest.php`

**Step 1: Write the failing test**

```php
<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\FilesystemDriver;
use AgentHarness\ShellDriver;
use AgentHarness\BuiltinFilesystemDriver;
use AgentHarness\BuiltinShellDriver;
use AgentHarness\ShellDriverFactory;
use AgentHarness\ExecResult;
use PHPUnit\Framework\TestCase;

class DriversTest extends TestCase
{
    protected function setUp(): void
    {
        ShellDriverFactory::reset();
    }

    public function testBuiltinFsWriteAndRead(): void
    {
        $fs = new BuiltinFilesystemDriver();
        $fs->write('/test.txt', 'hello');
        $this->assertSame('hello', $fs->read('/test.txt'));
    }

    public function testBuiltinFsImplementsInterface(): void
    {
        $fs = new BuiltinFilesystemDriver();
        $this->assertInstanceOf(FilesystemDriver::class, $fs);
    }

    public function testBuiltinShellImplementsInterface(): void
    {
        $driver = new BuiltinShellDriver();
        $this->assertInstanceOf(ShellDriver::class, $driver);
    }

    public function testBuiltinShellExec(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->fs()->write('/test.txt', 'hello');
        $result = $driver->exec('cat /test.txt');
        $this->assertSame('hello', $result->stdout);
    }

    public function testBuiltinShellRegisterCommand(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->registerCommand('greet', function (array $args, string $stdin): ExecResult {
            return new ExecResult(stdout: "hi\n");
        });
        $result = $driver->exec('greet');
        $this->assertSame("hi\n", $result->stdout);
    }

    public function testBuiltinShellClone(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->fs()->write('/a.txt', 'a');
        $cloned = $driver->clone();
        $cloned->fs()->write('/b.txt', 'b');
        $this->assertFalse($driver->fs()->exists('/b.txt'));
    }

    public function testFactoryDefaultIsBuiltin(): void
    {
        $driver = ShellDriverFactory::create();
        $this->assertInstanceOf(BuiltinShellDriver::class, $driver);
    }

    public function testFactoryUnknownThrows(): void
    {
        $this->expectException(\RuntimeException::class);
        ShellDriverFactory::create('nonexistent');
    }

    public function testFactoryRegisterCustom(): void
    {
        ShellDriverFactory::register('custom', function (array $opts): ShellDriver {
            return new BuiltinShellDriver(...$opts);
        });
        $driver = ShellDriverFactory::create('custom');
        $this->assertInstanceOf(BuiltinShellDriver::class, $driver);
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/adrian/Work/noodlehq/harness && vendor/bin/phpunit tests/php/DriversTest.php`
Expected: FAIL — classes not found

**Step 3: Write implementations**

Create `FilesystemDriver.php` (interface), `ShellDriver.php` (interface), `BuiltinFilesystemDriver.php`, `BuiltinShellDriver.php`, `ShellDriverFactory.php`.

PHP interfaces follow the same contract as Python/TypeScript. `BuiltinFilesystemDriver` wraps `VirtualFS`, `BuiltinShellDriver` wraps `Shell`.

**Step 4: Run test to verify it passes**

Run: `cd /Users/adrian/Work/noodlehq/harness && vendor/bin/phpunit tests/php/DriversTest.php`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add src/php/FilesystemDriver.php src/php/ShellDriver.php src/php/BuiltinFilesystemDriver.php src/php/BuiltinShellDriver.php src/php/ShellDriverFactory.php tests/php/DriversTest.php
git commit -m "Add PHP FilesystemDriver, ShellDriver contracts and factory"
```

---

### Task 8: PHP — Wire HasShell to use drivers

**Files:**
- Modify: `src/php/HasShell.php`
- Modify: `src/php/AgentBuilder.php`

**Step 1: Update HasShell trait**

Add `driver` parameter to `initHasShell`. Change `$this->shell` type from `Shell` to `ShellDriver`. Use `ShellDriverFactory::create()` when no shell instance given. Wrap existing Shell instances with `BuiltinShellDriver::fromShell()`.

**Step 2: Update AgentBuilder**

Add `private ?string $driver = null` and `public function driver(string $name): static`. Pass driver to shell init in `create()`.

**Step 3: Run ALL PHP tests**

Run: `cd /Users/adrian/Work/noodlehq/harness && vendor/bin/phpunit`
Expected: ALL PASS

**Step 4: Commit**

```bash
git add src/php/HasShell.php src/php/AgentBuilder.php
git commit -m "Wire PHP HasShell mixin to use ShellDriver contracts"
```

---

### Task 9: Update documentation

**Files:**
- Modify: `docs/architecture.md`
- Modify: `docs/overview.md`
- Modify: `docs/guides/python.md`
- Modify: `docs/guides/typescript.md`
- Modify: `docs/guides/php.md`

**Step 1: Update architecture.md**

Add a "Shell Driver Architecture" section after the existing "Virtual Shell" section:

- Explain the two contracts (FilesystemDriver, ShellDriver)
- Show the driver resolution flow
- Document ShellDriverFactory API
- Add mermaid diagram showing driver hierarchy

**Step 2: Update overview.md**

Add "Swappable shell backends" to the Key Capabilities list.

**Step 3: Update language guides**

Add a "Shell Drivers" section to each guide showing:
- How to use the default builtin driver (no change needed)
- How to select a driver via builder: `.driver("bashkit")`
- How to register a custom driver

**Step 4: Commit**

```bash
git add docs/architecture.md docs/overview.md docs/guides/python.md docs/guides/typescript.md docs/guides/php.md
git commit -m "Update docs for shell driver architecture"
```

---

### Task 10: Final verification and comparison table

**Step 1: Run all test suites**

```bash
cd /Users/adrian/Work/noodlehq/harness
python -m pytest tests/python/ -v
npx vitest run
vendor/bin/phpunit
```

Expected: ALL PASS across all languages

**Step 2: Update comparison.md**

Add driver contract comparison showing the API surface is identical across Python, TypeScript, and PHP.

**Step 3: Commit**

```bash
git add docs/comparison.md
git commit -m "Update comparison doc with driver contract parity"
```
