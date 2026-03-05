# Virtual Shell Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add VirtualFS, Shell, ShellRegistry, and HasShell mixin to all three language implementations.

**Architecture:** VirtualFS and Shell are standalone classes with no agent dependency. ShellRegistry is a global singleton storing named shell templates (clone-on-get). HasShell is a convenience mixin that wires Shell into the agent, auto-registering an `exec` tool when UsesTools is also composed. Existing `examples/shell_skill.py` contains the reference implementation for VirtualFS and Shell.

**Tech Stack:** Python (pytest, pytest-asyncio), TypeScript (vitest), PHP (PHPUnit)

**Reference:** The Python VirtualFS and Shell implementations exist in `examples/shell_skill.py`. Port the logic from there into the `src/` modules, adapting to match the patterns used by existing `src/` code.

---

## Phase 1: Python Implementation

### Task 1: VirtualFS — Tests

**Files:**
- Create: `tests/python/test_virtual_fs.py`
- Reference: `examples/shell_skill.py:42-134` (VirtualFS class)

**Step 1: Write VirtualFS tests**

```python
import pytest
from src.python.virtual_fs import VirtualFS


class TestVirtualFS:
    def test_write_and_read(self):
        fs = VirtualFS()
        fs.write("/hello.txt", "world")
        assert fs.read("/hello.txt") == "world"

    def test_read_text_decodes_bytes(self):
        fs = VirtualFS()
        fs.write("/bin.dat", b"\xff\xfe")
        assert isinstance(fs.read_text("/bin.dat"), str)

    def test_read_nonexistent_raises(self):
        fs = VirtualFS()
        with pytest.raises(FileNotFoundError):
            fs.read("/nope")

    def test_path_normalization(self):
        fs = VirtualFS()
        fs.write("//foo/../bar/baz.txt", "ok")
        assert fs.read("/bar/baz.txt") == "ok"

    def test_exists(self):
        fs = VirtualFS()
        fs.write("/a/b.txt", "x")
        assert fs.exists("/a/b.txt")
        assert fs.exists("/a")  # directory inferred
        assert not fs.exists("/nope")

    def test_remove(self):
        fs = VirtualFS()
        fs.write("/x.txt", "y")
        fs.remove("/x.txt")
        assert not fs.exists("/x.txt")

    def test_remove_nonexistent_raises(self):
        fs = VirtualFS()
        with pytest.raises(FileNotFoundError):
            fs.remove("/nope")

    def test_listdir(self):
        fs = VirtualFS()
        fs.write("/data/a.txt", "1")
        fs.write("/data/b.txt", "2")
        fs.write("/other/c.txt", "3")
        entries = fs.listdir("/data")
        assert sorted(entries) == ["a.txt", "b.txt"]

    def test_listdir_shows_subdirs(self):
        fs = VirtualFS()
        fs.write("/root/sub/file.txt", "x")
        entries = fs.listdir("/root")
        assert entries == ["sub"]

    def test_find(self):
        fs = VirtualFS()
        fs.write("/a/x.json", "{}")
        fs.write("/a/y.txt", "")
        fs.write("/b/z.json", "{}")
        results = fs.find("/", "*.json")
        assert sorted(results) == ["/a/x.json", "/b/z.json"]

    def test_stat_file(self):
        fs = VirtualFS()
        fs.write("/f.txt", "hello")
        s = fs.stat("/f.txt")
        assert s["type"] == "file"
        assert s["size"] == 5

    def test_stat_directory(self):
        fs = VirtualFS()
        fs.write("/d/f.txt", "x")
        s = fs.stat("/d")
        assert s["type"] == "directory"

    def test_write_lazy(self):
        called = False

        def provider():
            nonlocal called
            called = True
            return "lazy content"

        fs = VirtualFS()
        fs.write_lazy("/lazy.txt", provider)
        assert not called
        assert fs.read("/lazy.txt") == "lazy content"
        assert called
        # Second read uses cached value
        assert fs.read("/lazy.txt") == "lazy content"

    def test_init_with_files(self):
        fs = VirtualFS({"/a.txt": "1", "/b.txt": "2"})
        assert fs.read("/a.txt") == "1"
        assert fs.read("/b.txt") == "2"

    def test_clone(self):
        fs = VirtualFS({"/a.txt": "original"})
        fs.write_lazy("/b.txt", lambda: "lazy")
        clone = fs.clone()
        clone.write("/a.txt", "modified")
        assert fs.read("/a.txt") == "original"
        assert clone.read("/a.txt") == "modified"
        assert clone.read("/b.txt") == "lazy"
```

**Step 2: Run tests to verify they fail**

Run: `python -m pytest tests/python/test_virtual_fs.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'src.python.virtual_fs'`

**Step 3: Commit**

```bash
git add tests/python/test_virtual_fs.py
git commit -m "test: add VirtualFS tests (red)"
```

---

### Task 2: VirtualFS — Implementation

**Files:**
- Create: `src/python/virtual_fs.py`
- Reference: `examples/shell_skill.py:42-134` (VirtualFS class)

**Step 1: Implement VirtualFS**

Port the `VirtualFS` class from `examples/shell_skill.py` into `src/python/virtual_fs.py`. Key changes from the example:
- Add a `clone()` method that deep copies `_files` and `_lazy` (lazy providers cannot be deep-copied, so copy the dict reference — providers are assumed stateless)
- Keep the same API: `write`, `read`, `read_text`, `write_lazy`, `exists`, `remove`, `listdir`, `find`, `stat`
- Use `os.path.normpath` for path normalization with the `//` edge case fix
- Use `fnmatch.fnmatch` for `find()` pattern matching

```python
from __future__ import annotations

import copy
import fnmatch
import os
from typing import Any, Callable


class VirtualFS:
    """In-memory filesystem. Paths are always absolute and normalized."""

    def __init__(self, files: dict[str, str | bytes] | None = None):
        self._files: dict[str, str | bytes] = {}
        self._lazy: dict[str, Callable[[], str | bytes]] = {}
        if files:
            for path, content in files.items():
                self.write(path, content)

    @staticmethod
    def _norm(path: str) -> str:
        result = os.path.normpath("/" + path)
        if result.startswith("//"):
            result = result[1:]
        return result

    def write(self, path: str, content: str | bytes) -> None:
        self._files[self._norm(path)] = content

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._lazy[self._norm(path)] = provider

    def read(self, path: str) -> str | bytes:
        path = self._norm(path)
        if path in self._lazy:
            self._files[path] = self._lazy.pop(path)()
        if path not in self._files:
            raise FileNotFoundError(f"{path}: No such file")
        return self._files[path]

    def read_text(self, path: str) -> str:
        content = self.read(path)
        return content if isinstance(content, str) else content.decode("utf-8", errors="replace")

    def exists(self, path: str) -> bool:
        path = self._norm(path)
        return path in self._files or path in self._lazy or self._is_dir(path)

    def remove(self, path: str) -> None:
        path = self._norm(path)
        if path in self._files:
            del self._files[path]
        elif path in self._lazy:
            del self._lazy[path]
        else:
            raise FileNotFoundError(f"{path}: No such file")

    def _all_paths(self) -> set[str]:
        return set(self._files.keys()) | set(self._lazy.keys())

    def _is_dir(self, path: str) -> bool:
        path = self._norm(path)
        prefix = path.rstrip("/") + "/"
        return any(p.startswith(prefix) for p in self._all_paths())

    def listdir(self, path: str = "/") -> list[str]:
        path = self._norm(path).rstrip("/") + "/"
        if path == "//":
            path = "/"
        entries: set[str] = set()
        for p in self._all_paths():
            if p.startswith(path) and p != path:
                rest = p[len(path):]
                entry = rest.split("/")[0]
                entries.add(entry)
        return sorted(entries)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        root = self._norm(root).rstrip("/")
        results = []
        for p in sorted(self._all_paths()):
            if not p.startswith(root):
                continue
            basename = p.rsplit("/", 1)[-1]
            if fnmatch.fnmatch(basename, pattern):
                results.append(p)
        return results

    def stat(self, path: str) -> dict[str, Any]:
        path = self._norm(path)
        if self._is_dir(path):
            return {"path": path, "type": "directory"}
        content = self.read(path)
        size = len(content.encode("utf-8") if isinstance(content, str) else content)
        return {"path": path, "type": "file", "size": size}

    def clone(self) -> VirtualFS:
        new = VirtualFS()
        new._files = copy.deepcopy(self._files)
        new._lazy = dict(self._lazy)
        return new
```

**Step 2: Run tests to verify they pass**

Run: `python -m pytest tests/python/test_virtual_fs.py -v`
Expected: All PASS

**Step 3: Commit**

```bash
git add src/python/virtual_fs.py
git commit -m "feat: add VirtualFS in-memory filesystem (Python)"
```

---

### Task 3: Shell — Tests

**Files:**
- Create: `tests/python/test_shell.py`
- Reference: `examples/shell_skill.py:148-808` (Shell class)

**Step 1: Write Shell tests**

```python
import json
import pytest
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ExecResult, ShellRegistry


class TestShell:
    def setup_method(self):
        self.fs = VirtualFS()
        self.fs.write("/data/hello.txt", "hello world\n")
        self.fs.write("/data/nums.txt", "3\n1\n2\n1\n")
        self.fs.write("/data/users.json", json.dumps([
            {"name": "Alice", "role": "admin"},
            {"name": "Bob", "role": "user"},
        ], indent=2))
        self.shell = Shell(self.fs)

    def test_cat(self):
        r = self.shell.exec("cat /data/hello.txt")
        assert r.stdout == "hello world\n"
        assert r.exit_code == 0

    def test_cat_nonexistent(self):
        r = self.shell.exec("cat /nope")
        assert r.exit_code == 1

    def test_echo(self):
        r = self.shell.exec("echo hello world")
        assert r.stdout == "hello world\n"

    def test_ls(self):
        r = self.shell.exec("ls /data")
        assert "hello.txt" in r.stdout
        assert "users.json" in r.stdout

    def test_grep(self):
        r = self.shell.exec("grep admin /data/users.json")
        assert "admin" in r.stdout
        assert r.exit_code == 0

    def test_grep_no_match(self):
        r = self.shell.exec("grep zzzzz /data/hello.txt")
        assert r.exit_code == 1

    def test_grep_case_insensitive(self):
        r = self.shell.exec("grep -i HELLO /data/hello.txt")
        assert "hello" in r.stdout

    def test_pipe(self):
        r = self.shell.exec("cat /data/nums.txt | sort")
        assert r.stdout == "1\n1\n2\n3\n"

    def test_pipe_chain(self):
        r = self.shell.exec("cat /data/nums.txt | sort | uniq")
        assert r.stdout == "1\n2\n3\n"

    def test_redirect(self):
        self.shell.exec("echo test > /tmp/out.txt")
        assert self.fs.read_text("/tmp/out.txt") == "test\n"

    def test_redirect_append(self):
        self.shell.exec("echo line1 > /tmp/out.txt")
        self.shell.exec("echo line2 >> /tmp/out.txt")
        assert "line1" in self.fs.read_text("/tmp/out.txt")
        assert "line2" in self.fs.read_text("/tmp/out.txt")

    def test_find(self):
        r = self.shell.exec("find / -name '*.json'")
        assert "/data/users.json" in r.stdout

    def test_wc_lines(self):
        r = self.shell.exec("wc -l /data/nums.txt")
        assert "4" in r.stdout

    def test_head(self):
        r = self.shell.exec("head -2 /data/nums.txt")
        lines = r.stdout.strip().split("\n")
        assert len(lines) == 2

    def test_tail(self):
        r = self.shell.exec("tail -2 /data/nums.txt")
        lines = r.stdout.strip().split("\n")
        assert len(lines) == 2

    def test_sort(self):
        r = self.shell.exec("cat /data/nums.txt | sort -n")
        assert r.stdout == "1\n1\n2\n3\n"

    def test_jq_dot(self):
        r = self.shell.exec("cat /data/users.json | jq '.'")
        data = json.loads(r.stdout)
        assert len(data) == 2

    def test_jq_field(self):
        r = self.shell.exec("cat /data/users.json | jq '.[]'")
        assert r.exit_code == 0

    def test_tree(self):
        r = self.shell.exec("tree /data")
        assert "hello.txt" in r.stdout
        assert "users.json" in r.stdout

    def test_cd_and_pwd(self):
        self.shell.exec("cd /data")
        r = self.shell.exec("pwd")
        assert r.stdout.strip() == "/data"

    def test_sed(self):
        r = self.shell.exec("echo hello world | sed 's/world/earth/'")
        assert "earth" in r.stdout

    def test_cut(self):
        r = self.shell.exec("echo 'a:b:c' | cut -d ':' -f 2")
        assert r.stdout.strip() == "b"

    def test_env_expansion(self):
        shell = Shell(self.fs, env={"NAME": "test"})
        r = shell.exec("echo $NAME")
        assert r.stdout.strip() == "test"

    def test_command_not_found(self):
        r = self.shell.exec("nonexistent_cmd")
        assert r.exit_code == 127

    def test_allowed_commands(self):
        shell = Shell(self.fs, allowed_commands={"cat", "ls"})
        r = shell.exec("cat /data/hello.txt")
        assert r.exit_code == 0
        r = shell.exec("rm /data/hello.txt")
        assert r.exit_code == 127

    def test_output_truncation(self):
        self.fs.write("/big.txt", "x" * 20000)
        shell = Shell(self.fs, max_output=100)
        r = shell.exec("cat /big.txt")
        assert len(r.stdout) < 200
        assert "truncated" in r.stdout

    def test_command_chaining(self):
        r = self.shell.exec("echo hello; echo world")
        assert "hello" in r.stdout
        assert "world" in r.stdout

    def test_clone(self):
        original = Shell(self.fs, cwd="/data", env={"X": "1"})
        cloned = original.clone()
        cloned.fs.write("/new.txt", "new")
        cloned.env["Y"] = "2"
        assert not original.fs.exists("/new.txt")
        assert "Y" not in original.env
        assert cloned.cwd == "/data"


class TestShellRegistry:
    def setup_method(self):
        ShellRegistry.reset()

    def test_register_and_get(self):
        fs = VirtualFS({"/a.txt": "hello"})
        ShellRegistry.register("test", Shell(fs))
        shell = ShellRegistry.get("test")
        assert shell.exec("cat /a.txt").stdout == "hello"

    def test_get_returns_clone(self):
        fs = VirtualFS({"/a.txt": "hello"})
        ShellRegistry.register("test", Shell(fs))
        s1 = ShellRegistry.get("test")
        s2 = ShellRegistry.get("test")
        s1.fs.write("/b.txt", "new")
        assert not s2.fs.exists("/b.txt")

    def test_get_nonexistent_raises(self):
        with pytest.raises(KeyError):
            ShellRegistry.get("nope")

    def test_has(self):
        ShellRegistry.register("x", Shell(VirtualFS()))
        assert ShellRegistry.has("x")
        assert not ShellRegistry.has("y")

    def test_remove(self):
        ShellRegistry.register("x", Shell(VirtualFS()))
        ShellRegistry.remove("x")
        assert not ShellRegistry.has("x")

    def test_reset(self):
        ShellRegistry.register("a", Shell(VirtualFS()))
        ShellRegistry.register("b", Shell(VirtualFS()))
        ShellRegistry.reset()
        assert not ShellRegistry.has("a")
        assert not ShellRegistry.has("b")
```

**Step 2: Run tests to verify they fail**

Run: `python -m pytest tests/python/test_shell.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'src.python.shell'`

**Step 3: Commit**

```bash
git add tests/python/test_shell.py
git commit -m "test: add Shell and ShellRegistry tests (red)"
```

---

### Task 4: Shell — Implementation

**Files:**
- Create: `src/python/shell.py`
- Reference: `examples/shell_skill.py:140-808` (Shell class, ExecResult)

**Step 1: Implement Shell, ExecResult, and ShellRegistry**

Port the `Shell` and `ExecResult` classes from `examples/shell_skill.py`. Add `ShellRegistry` and `clone()` method.

The Shell class should:
- Import `VirtualFS` from `src.python.virtual_fs`
- Include all 23 built-in commands from the example
- Add `clone()` that deep copies the fs, cwd, env, and allowed_commands
- Accept `max_output` as constructor param (default 16,000)

The ShellRegistry should:
- Be a module-level singleton (class with only static/class methods)
- Store shells in a class-level dict
- `get()` returns `shell.clone()`

```python
# ShellRegistry at the end of shell.py

class ShellRegistry:
    _shells: dict[str, Shell] = {}

    @classmethod
    def register(cls, name: str, shell: Shell) -> None:
        cls._shells[name] = shell

    @classmethod
    def get(cls, name: str) -> Shell:
        if name not in cls._shells:
            raise KeyError(f"No shell registered with name: {name}")
        return cls._shells[name].clone()

    @classmethod
    def has(cls, name: str) -> bool:
        return name in cls._shells

    @classmethod
    def remove(cls, name: str) -> None:
        del cls._shells[name]

    @classmethod
    def reset(cls) -> None:
        cls._shells.clear()
```

For `Shell.clone()`:

```python
def clone(self) -> Shell:
    return Shell(
        fs=self.fs.clone(),
        cwd=self.cwd,
        env=dict(self.env),
        allowed_commands=set(self._builtins.keys()) if self._original_allowed is not None else None,
        max_output=self.max_output,
        max_iterations=self.max_iterations,
    )
```

Note: Track `_original_allowed` (the `allowed_commands` passed to constructor) so `clone()` can reconstruct the allowlist.

**Step 2: Run tests to verify they pass**

Run: `python -m pytest tests/python/test_shell.py -v`
Expected: All PASS

**Step 3: Commit**

```bash
git add src/python/shell.py
git commit -m "feat: add Shell interpreter and ShellRegistry (Python)"
```

---

### Task 5: HasShell Mixin — Tests

**Files:**
- Create: `tests/python/test_has_shell.py`
- Reference: `src/python/uses_tools.py` (for mixin pattern), `tests/python/test_uses_tools.py` (for test pattern)

**Step 1: Write HasShell tests**

```python
import pytest
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ShellRegistry
from src.python.has_shell import HasShell
from src.python.uses_tools import UsesTools


class ShellOnly(HasShell):
    pass


class ShellWithTools(UsesTools, HasShell):
    pass


class TestHasShell:
    def test_default_shell(self):
        obj = ShellOnly()
        assert obj.shell is not None
        assert obj.fs is not None

    def test_shell_instance(self):
        fs = VirtualFS({"/a.txt": "hello"})
        shell = Shell(fs)
        obj = ShellOnly(shell=shell)
        assert obj.fs.read("/a.txt") == "hello"

    def test_shell_from_registry(self):
        ShellRegistry.reset()
        fs = VirtualFS({"/data.txt": "registry"})
        ShellRegistry.register("test-shell", Shell(fs))
        obj = ShellOnly(shell="test-shell")
        assert obj.fs.read("/data.txt") == "registry"
        # Verify it's a clone
        obj.fs.write("/new.txt", "local")
        s2 = ShellRegistry.get("test-shell")
        assert not s2.fs.exists("/new.txt")
        ShellRegistry.reset()

    def test_exec(self):
        obj = ShellOnly()
        obj.fs.write("/test.txt", "hello\n")
        result = obj.exec("cat /test.txt")
        assert result.stdout == "hello\n"

    def test_constructor_params(self):
        obj = ShellOnly(
            shell=None,
            cwd="/home",
            env={"X": "1"},
            allowed_commands={"cat", "ls"},
        )
        r = obj.exec("pwd")
        assert r.stdout.strip() == "/home"
        r = obj.exec("echo $X")
        assert r.stdout.strip() == "1"
        r = obj.exec("grep x y")
        assert r.exit_code == 127


class TestHasShellWithTools:
    def test_auto_registers_exec_tool(self):
        obj = ShellWithTools()
        assert "exec" in obj._tools

    def test_exec_tool_works(self):
        obj = ShellWithTools()
        obj.fs.write("/test.txt", "hello\n")
        # Execute the registered tool function directly
        tool_def = obj._tools["exec"]
        import asyncio
        result = asyncio.get_event_loop().run_until_complete(
            tool_def.function({"command": "cat /test.txt"})
        )
        assert "hello" in result
```

**Step 2: Run tests to verify they fail**

Run: `python -m pytest tests/python/test_has_shell.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'src.python.has_shell'`

**Step 3: Commit**

```bash
git add tests/python/test_has_shell.py
git commit -m "test: add HasShell mixin tests (red)"
```

---

### Task 6: HasShell Mixin — Implementation

**Files:**
- Create: `src/python/has_shell.py`
- Modify: `src/python/standard_agent.py`
- Modify: `src/python/__init__.py` (if it has exports)
- Reference: `src/python/uses_tools.py` (for mixin pattern), `src/python/emits_events.py` (for lazy init pattern)

**Step 1: Implement HasShell mixin**

Follow the lazy-init pattern used by other mixins (check `hasattr`, call `__init_has_shell__()`).

```python
from __future__ import annotations

import logging
from typing import Any

from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ExecResult, ShellRegistry

logger = logging.getLogger(__name__)


class HasShell:
    """
    Mixin that provides a virtual shell.
    Works independently; auto-registers exec tool if UsesTools is also composed.
    """

    def __init_has_shell__(
        self,
        shell: str | Shell | None = None,
        cwd: str = "/home/user",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
    ) -> None:
        if isinstance(shell, str):
            self._shell = ShellRegistry.get(shell)
        elif isinstance(shell, Shell):
            self._shell = shell
        else:
            self._shell = Shell(
                fs=VirtualFS(),
                cwd=cwd,
                env=env or {},
                allowed_commands=allowed_commands,
            )

        # Auto-register exec tool if UsesTools is composed
        if hasattr(self, "register_tool"):
            self._register_shell_tool()

    def _ensure_has_shell(self, **kwargs: Any) -> None:
        if not hasattr(self, "_shell"):
            self.__init_has_shell__(**kwargs)

    def _register_shell_tool(self) -> None:
        from src.python.uses_tools import ToolDef

        async def exec_command(args: dict[str, Any]) -> str:
            result = self.exec(args["command"])
            parts = []
            if result.stdout:
                parts.append(result.stdout)
            if result.stderr:
                parts.append(f"[stderr] {result.stderr}")
            if result.exit_code != 0:
                parts.append(f"[exit code: {result.exit_code}]")
            return "".join(parts) or "(no output)"

        tool = ToolDef(
            name="exec",
            description=(
                "Execute a bash command in the virtual filesystem. "
                "Supports: ls, cat, grep, find, head, tail, wc, sort, uniq, "
                "cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, echo, stat. "
                "Pipes (|) and redirects (>, >>) are supported."
            ),
            function=exec_command,
            parameters={
                "type": "object",
                "properties": {
                    "command": {
                        "type": "string",
                        "description": "The shell command to execute",
                    },
                },
                "required": ["command"],
            },
        )
        self.register_tool(tool)

    @property
    def shell(self) -> Shell:
        self._ensure_has_shell()
        return self._shell

    @property
    def fs(self) -> VirtualFS:
        return self.shell.fs

    def exec(self, command: str) -> ExecResult:
        return self.shell.exec(command)

    def _build_shell_prompt(self) -> str:
        return (
            "You have access to a virtual filesystem via the `exec` tool. "
            "Use standard Unix commands to explore and process files: "
            "ls, cat, grep, find, head, tail, wc, sort, uniq, cut, sed, jq, tree. "
            "Pipes (|) and redirects (>, >>) are supported. "
            "Use `tree /` to see the full file layout."
        )
```

Note: The `__init__` integration depends on how the existing mixins handle constructor kwargs. Check `emits_events.py` for the pattern — HasShell should accept `shell`, `cwd`, `env`, `allowed_commands` kwargs and pass them to `__init_has_shell__`.

**Step 2: Update StandardAgent**

```python
# src/python/standard_agent.py
from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.has_middleware import HasMiddleware
from src.python.uses_tools import UsesTools
from src.python.emits_events import EmitsEvents
from src.python.has_shell import HasShell


class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents, HasShell):
    pass
```

**Step 3: Run tests to verify they pass**

Run: `python -m pytest tests/python/test_has_shell.py -v`
Expected: All PASS

**Step 4: Run all existing tests to verify no regressions**

Run: `python -m pytest tests/python/ -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add src/python/has_shell.py src/python/standard_agent.py
git commit -m "feat: add HasShell mixin with auto tool registration (Python)"
```

---

## Phase 2: TypeScript Implementation

### Task 7: VirtualFS — Tests + Implementation

**Files:**
- Create: `tests/typescript/virtual-fs.test.ts`
- Create: `src/typescript/virtual-fs.ts`
- Reference: `examples/shell_skill.py:42-134` (logic), `tests/typescript/message-bus.test.ts` (test pattern)

**Step 1: Write VirtualFS tests**

Mirror the Python VirtualFS tests in vitest. Key differences:
- No `bytes` support — string only
- `write_lazy` providers can be sync (return `string`)
- Use `describe`/`it` pattern from existing TS tests

**Step 2: Implement VirtualFS**

Port VirtualFS as a TypeScript class. Use `Map<string, string>` for `_files` and `Map<string, () => string>` for `_lazy`. Path normalization via manual split/filter/join (no `os.path`).

**Step 3: Run tests**

Run: `cd src/typescript && npx vitest run ../../tests/typescript/virtual-fs.test.ts`
Expected: All PASS

**Step 4: Commit**

```bash
git add tests/typescript/virtual-fs.test.ts src/typescript/virtual-fs.ts
git commit -m "feat: add VirtualFS in-memory filesystem (TypeScript)"
```

---

### Task 8: Shell + ShellRegistry — Tests + Implementation

**Files:**
- Create: `tests/typescript/shell.test.ts`
- Create: `src/typescript/shell.ts`
- Reference: `examples/shell_skill.py:148-808` (logic)

**Step 1: Write Shell and ShellRegistry tests**

Mirror the Python Shell tests. Same commands, same behavior. ShellRegistry is a module-level singleton object.

**Step 2: Implement Shell, ExecResult, ShellRegistry**

Port all 23 commands. Use the same parsing logic (shlex equivalent: split on spaces respecting quotes). ShellRegistry stores `Map<string, Shell>`, `get()` returns `shell.clone()`.

**Step 3: Run tests**

Run: `cd src/typescript && npx vitest run ../../tests/typescript/shell.test.ts`
Expected: All PASS

**Step 4: Commit**

```bash
git add tests/typescript/shell.test.ts src/typescript/shell.ts
git commit -m "feat: add Shell interpreter and ShellRegistry (TypeScript)"
```

---

### Task 9: HasShell Mixin — Tests + Implementation

**Files:**
- Create: `tests/typescript/has-shell.test.ts`
- Create: `src/typescript/has-shell.ts`
- Modify: `src/typescript/standard-agent.ts`
- Modify: `src/typescript/index.ts`

**Step 1: Write HasShell tests**

Test standalone usage, registry lookup, auto tool registration when composed with UsesTools.

**Step 2: Implement HasShell**

Follow the function-based mixin pattern: `export function HasShell<TBase extends Constructor>(Base: TBase)`. Detect UsesTools via `hasattr`-equivalent (`'registerTool' in this`).

**Step 3: Update StandardAgent and exports**

```typescript
// standard-agent.ts
export const StandardAgent = HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent)))));

// index.ts — add exports
export { HasShell } from "./has-shell.js";
export { VirtualFS } from "./virtual-fs.js";
export { Shell, ShellRegistry, ExecResult } from "./shell.js";
```

**Step 4: Run all tests**

Run: `cd src/typescript && npx vitest run`
Expected: All PASS

**Step 5: Commit**

```bash
git add tests/typescript/has-shell.test.ts src/typescript/has-shell.ts src/typescript/standard-agent.ts src/typescript/index.ts
git commit -m "feat: add HasShell mixin with auto tool registration (TypeScript)"
```

---

## Phase 3: PHP Implementation

### Task 10: VirtualFS — Tests + Implementation

**Files:**
- Create: `tests/php/VirtualFSTest.php`
- Create: `src/php/VirtualFS.php`
- Reference: `examples/shell_skill.py:42-134` (logic), `tests/php/MessageBusTest.php` (test pattern)

**Step 1: Write VirtualFS tests**

PHPUnit test class mirroring the Python tests. String-only content. Lazy providers are closures.

**Step 2: Implement VirtualFS**

PHP class in `AgentHarness` namespace. `array<string, string>` for `$files`, `array<string, Closure>` for `$lazy`. Manual path normalization.

**Step 3: Run tests**

Run: `cd src/php && ./vendor/bin/phpunit --filter VirtualFSTest ../../tests/php/VirtualFSTest.php`
Expected: All PASS

**Step 4: Commit**

```bash
git add tests/php/VirtualFSTest.php src/php/VirtualFS.php
git commit -m "feat: add VirtualFS in-memory filesystem (PHP)"
```

---

### Task 11: Shell + ShellRegistry — Tests + Implementation

**Files:**
- Create: `tests/php/ShellTest.php`
- Create: `src/php/Shell.php`

**Step 1: Write Shell and ShellRegistry tests**

Mirror Python tests. ShellRegistry is a static class.

**Step 2: Implement Shell, ExecResult, ShellRegistry**

All 23 commands as PHP methods. `ExecResult` is a simple value object. ShellRegistry uses static properties.

**Step 3: Run tests**

Run: `cd src/php && ./vendor/bin/phpunit --filter ShellTest ../../tests/php/ShellTest.php`
Expected: All PASS

**Step 4: Commit**

```bash
git add tests/php/ShellTest.php src/php/Shell.php
git commit -m "feat: add Shell interpreter and ShellRegistry (PHP)"
```

---

### Task 12: HasShell Trait — Tests + Implementation

**Files:**
- Create: `tests/php/HasShellTest.php`
- Create: `src/php/HasShell.php`
- Modify: `src/php/StandardAgent.php`

**Step 1: Write HasShell tests**

Test standalone, registry lookup, auto tool registration.

**Step 2: Implement HasShell**

PHP trait. Uses the same lazy-init pattern as other traits. Detects UsesTools via `method_exists($this, 'registerTool')`.

**Step 3: Update StandardAgent**

```php
class StandardAgent extends BaseAgent
{
    use HasHooks;
    use HasMiddleware;
    use UsesTools;
    use EmitsEvents;
    use HasShell;
}
```

**Step 4: Run all tests**

Run: `cd src/php && ./vendor/bin/phpunit`
Expected: All PASS

**Step 5: Commit**

```bash
git add tests/php/HasShellTest.php src/php/HasShell.php src/php/StandardAgent.php
git commit -m "feat: add HasShell trait with auto tool registration (PHP)"
```

---

## Phase 4: Final Integration

### Task 13: Integration Tests

**Files:**
- Modify: `tests/python/test_integration.py` (add shell integration test)
- Modify: `tests/typescript/integration.test.ts` (add shell integration test)
- Modify: `tests/php/IntegrationTest.php` (add shell integration test)

**Step 1: Add integration test to each language**

Test that StandardAgent has shell capabilities:
- `agent.fs.write(...)` works
- `agent.exec(...)` returns expected results
- The `exec` tool is registered in the tools list

**Step 2: Run full test suites**

Run all three:
```bash
python -m pytest tests/python/ -v
cd src/typescript && npx vitest run
cd src/php && ./vendor/bin/phpunit
```
Expected: All PASS across all languages

**Step 3: Commit**

```bash
git add tests/python/test_integration.py tests/typescript/integration.test.ts tests/php/IntegrationTest.php
git commit -m "test: add virtual shell integration tests across all languages"
```

---

### Task 14: Final Commit — Docs

**Step 1: Verify all docs are committed**

The design doc, ADRs, conversation record, and guide updates were created during brainstorming. Verify they're staged.

**Step 2: Commit docs**

```bash
git add docs/
git commit -m "docs: add virtual shell design, ADRs, and guide updates"
```
