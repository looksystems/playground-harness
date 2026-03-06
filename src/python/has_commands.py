from __future__ import annotations

import asyncio
import inspect
import logging
from dataclasses import dataclass, field
from typing import Any, Callable, TypeVar

from src.python.has_hooks import HookEvent

logger = logging.getLogger(__name__)

F = TypeVar("F", bound=Callable)


@dataclass
class CommandDef:
    name: str
    description: str
    handler: Callable
    parameters: dict[str, Any]
    llm_visible: bool = True


def command(
    description: str,
    name: str | None = None,
    schema_override: dict | None = None,
    llm_visible: bool = True,
) -> Callable[[F], F]:
    def decorator(fn: F) -> F:
        fn._command_meta = {
            "name": name or fn.__name__,
            "description": description,
            "schema": schema_override,
            "llm_visible": llm_visible,
        }
        return fn
    return decorator


def _build_param_schema(fn: Callable) -> dict[str, Any]:
    from src.python.uses_tools import _build_param_schema as _build
    return _build(fn)


class HasCommands:
    def __init_has_commands__(self, llm_commands_enabled: bool = True) -> None:
        self._commands: dict[str, CommandDef] = {}
        self._llm_commands_enabled: bool = llm_commands_enabled

    def _ensure_has_commands(self) -> None:
        if not hasattr(self, "_commands"):
            self.__init_has_commands__()

    @property
    def commands(self) -> dict[str, CommandDef]:
        self._ensure_has_commands()
        return dict(self._commands)

    def _emit_fire_and_forget(self, event: HookEvent, *args: Any) -> None:
        if not hasattr(self, "_emit"):
            return
        try:
            loop = asyncio.get_running_loop()
            loop.create_task(self._emit(event, *args))
        except RuntimeError:
            pass

    def register_slash_command(self, fn_or_def: Callable | CommandDef) -> None:
        self._ensure_has_commands()

        if isinstance(fn_or_def, CommandDef):
            cdef = fn_or_def
        else:
            meta = getattr(fn_or_def, "_command_meta", None)
            if meta is None:
                raise ValueError(f"{fn_or_def} is not decorated with @command")
            cdef = CommandDef(
                name=meta["name"],
                description=meta["description"],
                handler=fn_or_def,
                parameters=meta["schema"] or _build_param_schema(fn_or_def),
                llm_visible=meta["llm_visible"],
            )

        self._commands[cdef.name] = cdef
        self._emit_fire_and_forget(HookEvent.SLASH_COMMAND_REGISTER, cdef.name)

        # Auto-register tool if UsesTools is composed and command is visible
        if (
            cdef.llm_visible
            and getattr(self, "_llm_commands_enabled", True)
            and hasattr(self, "register_tool")
        ):
            from src.python.uses_tools import ToolDef

            cmd_name = cdef.name

            async def _tool_fn(_args: dict[str, Any] = {}, _cmd=cmd_name, **kwargs: Any) -> str:
                # Merge positional dict arg and kwargs
                merged = {**_args, **kwargs} if _args else kwargs
                return self.execute_slash_command(_cmd, merged)

            tool_def = ToolDef(
                name=f"slash_{cdef.name}",
                description=cdef.description,
                function=_tool_fn,
                parameters=cdef.parameters,
            )
            self.register_tool(tool_def)

    def unregister_slash_command(self, name: str) -> None:
        self._ensure_has_commands()
        self._commands.pop(name, None)
        self._emit_fire_and_forget(HookEvent.SLASH_COMMAND_UNREGISTER, name)

        # Remove auto-registered tool
        if hasattr(self, "unregister_tool"):
            self.unregister_tool(f"slash_{name}")

    def execute_slash_command(self, name: str, args: dict[str, Any]) -> str:
        self._ensure_has_commands()
        cdef = self._commands.get(name)
        if cdef is None:
            return f"Error: unknown slash command '/{name}'"

        self._emit_fire_and_forget(HookEvent.SLASH_COMMAND_CALL, name, args)
        result = cdef.handler(args)
        if not isinstance(result, str):
            result = str(result)
        self._emit_fire_and_forget(HookEvent.SLASH_COMMAND_RESULT, name, result)
        return result

    def intercept_slash_command(self, text: str) -> tuple[str, dict[str, Any]] | None:
        if not text.startswith("/"):
            return None

        self._ensure_has_commands()

        parts = text[1:].split(None, 1)
        cmd_name = parts[0]
        rest = parts[1] if len(parts) > 1 else ""

        cdef = self._commands.get(cmd_name)
        if cdef is None:
            return None

        # If command has properties in its schema, parse key=value pairs
        props = cdef.parameters.get("properties", {})
        if props and rest:
            args: dict[str, Any] = {}
            for token in rest.split():
                if "=" in token:
                    k, v = token.split("=", 1)
                    args[k] = v
                else:
                    args.setdefault("input", "")
                    if args["input"]:
                        args["input"] += " "
                    args["input"] += token
            return (cmd_name, args)

        return (cmd_name, {"input": rest} if rest else {})
