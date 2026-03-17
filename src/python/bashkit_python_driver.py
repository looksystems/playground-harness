"""BashkitPythonDriver: ShellDriver using the bashkit PyO3 package."""

from __future__ import annotations

from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult
from src.python._remote_sync import (
    DirtyTrackingFS,
    SYNC_BACK_SCRIPT,
    build_sync_preamble,
    parse_file_listing,
    apply_sync_back,
)

# Re-export for backwards compatibility (tests import this name)
_DirtyTrackingFS = DirtyTrackingFS


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
        self._fs_driver = DirtyTrackingFS(inner_fs)
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
        sync_commands = build_sync_preamble(self._fs_driver)
        if sync_commands:
            preamble = " && ".join(sync_commands)
            executor.execute_sync(preamble)

        result = executor.execute_sync(command)

        # Sync files back from bashkit
        sync_result = executor.execute_sync(SYNC_BACK_SCRIPT)
        if sync_result.exit_code == 0:
            remote_files = parse_file_listing(sync_result.stdout or "")
            apply_sync_back(self._fs_driver, remote_files)

        return ExecResult(
            stdout=result.stdout if result.stdout else "",
            stderr=result.stderr if result.stderr else "",
            exit_code=result.exit_code if result.exit_code else 0,
        )

    def capabilities(self) -> set[str]:
        return {"custom_commands", "stateful", "remote"}

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
