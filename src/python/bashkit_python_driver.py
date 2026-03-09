"""BashkitPythonDriver: ShellDriver using the bashkit PyO3 package."""

from __future__ import annotations

import base64
from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult


class _DirtyTrackingFS(FilesystemDriver):
    """Wraps a FilesystemDriver to track which paths have been written to."""

    def __init__(self, inner: FilesystemDriver):
        self._inner = inner
        self._dirty: set[str] = set()

    @property
    def dirty(self) -> set[str]:
        return self._dirty

    def clear_dirty(self) -> None:
        self._dirty.clear()

    def write(self, path: str, content: str | bytes) -> None:
        self._inner.write(path, content)
        self._dirty.add(path)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._inner.write_lazy(path, provider)
        self._dirty.add(path)

    def read(self, path: str) -> str | bytes:
        return self._inner.read(path)

    def read_text(self, path: str) -> str:
        return self._inner.read_text(path)

    def exists(self, path: str) -> bool:
        return self._inner.exists(path)

    def remove(self, path: str) -> None:
        self._inner.remove(path)
        self._dirty.add(path)

    def is_dir(self, path: str) -> bool:
        return self._inner.is_dir(path)

    def listdir(self, path: str = "/") -> list[str]:
        return self._inner.listdir(path)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        return self._inner.find(root, pattern)

    def stat(self, path: str) -> dict[str, Any]:
        return self._inner.stat(path)

    def clone(self) -> _DirtyTrackingFS:
        return _DirtyTrackingFS(self._inner.clone())


class BashkitPythonDriver(ShellDriver):
    """ShellDriver that uses the bashkit PyO3 Python package."""

    def __init__(
        self,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        bash_override: Any = None,
        **kwargs: Any,
    ):
        self._cwd = cwd
        self._env = env if env is not None else {}
        inner_fs = BuiltinFilesystemDriver()
        self._fs_driver = _DirtyTrackingFS(inner_fs)
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None
        self._bash_override = bash_override

        # When no commands are registered, use Bash; otherwise ScriptedTool
        self._bash = bash_override or self._create_bash()
        self._tool: Any = None  # lazily created ScriptedTool

    def _create_bash(self) -> Any:
        """Create a bashkit.Bash instance."""
        from bashkit import Bash
        return Bash()

    def _create_tool(self) -> Any:
        """Create a bashkit.ScriptedTool and register all current commands."""
        from bashkit import ScriptedTool
        tool = ScriptedTool(name="harness")
        for name, handler in self._commands.items():
            tool.add_tool(name, name, self._wrap_handler(handler))
        # Set environment variables
        for key, value in self._env.items():
            tool.env(key, value)
        return tool

    def _wrap_handler(self, handler: Callable) -> Callable:
        """Adapt our handler(args, stdin='') to ScriptedTool callback(params, stdin).

        ScriptedTool parses --flag value pairs into a dict. Positional args
        after -- land under the "" key. We flatten all values into a list
        for handlers that expect (args_list, stdin).
        """

        def wrapper(params: dict, stdin: str | None) -> str:
            args: list[str] = []
            for key, value in params.items():
                if key == "":
                    # Positional args after --
                    if isinstance(value, list):
                        args.extend(str(v) for v in value)
                    else:
                        args.append(str(value))
                else:
                    args.extend([f"--{key}", str(value)])
            result = handler(args, stdin=stdin or "")
            if isinstance(result, ExecResult):
                return result.stdout
            return str(result)

        return wrapper

    def _get_executor(self) -> Any:
        """Return the appropriate executor (ScriptedTool if commands registered, else Bash)."""
        if self._commands and self._tool is not None:
            return self._tool
        return self._bash

    def _sync_dirty_to_bashkit(self) -> list[str]:
        """Write dirty files into bashkit via base64-encoded commands. Returns commands to prepend."""
        commands: list[str] = []
        for path in list(self._fs_driver.dirty):
            if self._fs_driver.exists(path) and not self._fs_driver.is_dir(path):
                content = self._fs_driver.read_text(path)
                encoded = base64.b64encode(content.encode()).decode()
                commands.append(f"mkdir -p $(dirname '{path}') && printf '%s' '{encoded}' | base64 -d > '{path}'")
            elif not self._fs_driver.exists(path):
                commands.append(f"rm -f '{path}'")
        self._fs_driver.clear_dirty()
        return commands

    def _sync_bashkit_to_vfs(self, executor: Any) -> None:
        """After exec, list files in bashkit and diff/apply changes to our VFS."""
        marker = "===FILE:"
        end_marker = "==="
        script = (
            "find / -type f 2>/dev/null -exec sh -c "
            "'for f; do printf \"===FILE:%s===\\n\" \"$f\"; base64 \"$f\"; done' _ {} +"
        )
        result = executor.execute_sync(script)
        if result.exit_code != 0:
            return

        bashkit_files: dict[str, str] = {}
        current_path: str | None = None
        content_lines: list[str] = []

        for line in result.stdout.split("\n"):
            if line.startswith(marker) and line.endswith(end_marker):
                if current_path is not None:
                    encoded = "".join(content_lines)
                    try:
                        bashkit_files[current_path] = base64.b64decode(encoded).decode()
                    except Exception:
                        bashkit_files[current_path] = encoded
                current_path = line[len(marker):-len(end_marker)]
                content_lines = []
            elif current_path is not None:
                content_lines.append(line)

        if current_path is not None:
            encoded = "".join(content_lines)
            try:
                bashkit_files[current_path] = base64.b64decode(encoded).decode()
            except Exception:
                bashkit_files[current_path] = encoded

        vfs_files: set[str] = set()
        for path in self._fs_driver.find("/", "*"):
            if not self._fs_driver.is_dir(path):
                vfs_files.add(path)

        for path, new_content in bashkit_files.items():
            if path not in vfs_files:
                self._fs_driver._inner.write(path, new_content)
            else:
                existing = self._fs_driver.read_text(path)
                if existing != new_content:
                    self._fs_driver._inner.write(path, new_content)

        for path in vfs_files - set(bashkit_files.keys()):
            if self._fs_driver.exists(path):
                self._fs_driver._inner.remove(path)

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
        """Execute a command via bashkit."""
        executor = self._get_executor()

        # Sync dirty files to bashkit
        sync_commands = self._sync_dirty_to_bashkit()
        if sync_commands:
            preamble = " && ".join(sync_commands)
            executor.execute_sync(preamble)

        result = executor.execute_sync(command)

        self._sync_bashkit_to_vfs(executor)

        return ExecResult(
            stdout=result.stdout if result.stdout else "",
            stderr=result.stderr if result.stderr else "",
            exit_code=result.exit_code if result.exit_code else 0,
        )

    def register_command(self, name: str, handler: Callable) -> None:
        """Register a custom command. Switches to ScriptedTool."""
        self._commands[name] = handler
        if self._bash_override is not None:
            # In testing mode, the mock handles everything
            return
        self._tool = self._create_tool()

    def unregister_command(self, name: str) -> None:
        """Remove a custom command."""
        self._commands.pop(name, None)
        if self._bash_override is not None:
            return
        if self._commands:
            self._tool = self._create_tool()
        else:
            self._tool = None

    def clone(self) -> BashkitPythonDriver:
        """Create a new independent instance with cloned FS and same config."""
        new_driver = BashkitPythonDriver.__new__(BashkitPythonDriver)
        new_driver._cwd = self._cwd
        new_driver._env = dict(self._env)
        new_driver._fs_driver = self._fs_driver.clone()
        new_driver._commands = dict(self._commands)
        new_driver._on_not_found = self._on_not_found
        new_driver._bash_override = self._bash_override

        if self._bash_override is not None:
            new_driver._bash = self._bash_override
            new_driver._tool = None
        else:
            new_driver._bash = new_driver._create_bash()
            new_driver._tool = new_driver._create_tool() if new_driver._commands else None

        return new_driver
