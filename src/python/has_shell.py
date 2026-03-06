from __future__ import annotations

import logging
from typing import Any, Callable, Self

from src.python._utils import emit_fire_and_forget
from src.python.has_hooks import HookEvent
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ExecResult, ShellRegistry

logger = logging.getLogger(__name__)


class HasShell:
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

        if hasattr(self, "_emit"):
            self._shell.on_not_found = lambda cmd_name: (
                emit_fire_and_forget(self, HookEvent.SHELL_NOT_FOUND, cmd_name)
            )

        # Auto-register exec tool if UsesTools is composed
        if hasattr(self, "register_tool"):
            self._register_shell_tool()

    def _ensure_has_shell(self) -> None:
        if not hasattr(self, "_shell"):
            self.__init_has_shell__()

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
                "Commands: ls, cat, grep, find, head, tail, wc, sort, uniq, "
                "cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, "
                "echo, stat, test, printf, true, false. "
                "Operators: pipes (|), redirects (>, >>), && (and), || (or), ; (sequence). "
                "Flow control: if/then/elif/else/fi, for/in/do/done, while/do/done, case/in/esac. "
                "Features: VAR=assignment, $(cmd) substitution, $((expr)) arithmetic, "
                "${var:-default} expansion, ${var:=default}, ${#var}, "
                "${var:offset:length}, ${var//pat/repl}, ${var%suffix}, ${var##prefix}. "
                "Custom commands registered via register_command() are also available."
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
        emit_fire_and_forget(self, HookEvent.SHELL_CALL, command)
        old_cwd = self.shell.cwd
        result = self.shell.exec(command)
        emit_fire_and_forget(self, HookEvent.SHELL_RESULT, command, result)
        if self.shell.cwd != old_cwd:
            emit_fire_and_forget(self, HookEvent.SHELL_CWD, old_cwd, self.shell.cwd)
        return result

    def register_command(self, name: str, handler: Callable) -> Self:
        self.shell.register_command(name, handler)
        emit_fire_and_forget(self, HookEvent.COMMAND_REGISTER, name)
        return self

    def unregister_command(self, name: str) -> Self:
        self.shell.unregister_command(name)
        emit_fire_and_forget(self, HookEvent.COMMAND_UNREGISTER, name)
        return self
