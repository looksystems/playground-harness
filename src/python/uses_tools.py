from __future__ import annotations

import inspect
import json
import logging
from dataclasses import dataclass
from typing import Any, Callable, Self, TypeVar, get_type_hints

from src.python._utils import call_fn_kwargs, emit_fire_and_forget
from src.python.has_hooks import HookEvent

logger = logging.getLogger(__name__)

PYTHON_TYPE_TO_JSON = {
    str: "string",
    int: "integer",
    float: "number",
    bool: "boolean",
    list: "array",
    dict: "object",
}

F = TypeVar("F", bound=Callable)


@dataclass
class ToolDef:
    name: str
    description: str
    function: Callable
    parameters: dict[str, Any]


def _build_param_schema(fn: Callable) -> dict[str, Any]:
    hints = get_type_hints(fn)
    props: dict[str, Any] = {}
    required: list[str] = []
    sig = inspect.signature(fn)
    for pname, param in sig.parameters.items():
        if pname in ("self", "cls"):
            continue
        ptype = hints.get(pname, str)
        props[pname] = {"type": PYTHON_TYPE_TO_JSON.get(ptype, "string")}
        if param.default is inspect.Parameter.empty:
            required.append(pname)
    schema: dict[str, Any] = {"type": "object", "properties": props}
    if required:
        schema["required"] = required
    return schema


def tool(
    description: str,
    name: str | None = None,
    schema_override: dict | None = None,
) -> Callable[[F], F]:
    def decorator(fn: F) -> F:
        fn._tool_meta = {
            "name": name or fn.__name__,
            "description": description,
            "schema": schema_override,
        }
        return fn
    return decorator


class UsesTools:
    def __init_uses_tools__(self) -> None:
        self._tools_registry: dict[str, ToolDef] = {}

    def register_tool(self, fn_or_def: Callable | ToolDef) -> Self:
        if not hasattr(self, "_tools_registry"):
            self.__init_uses_tools__()
        if isinstance(fn_or_def, ToolDef):
            self._tools_registry[fn_or_def.name] = fn_or_def
            emit_fire_and_forget(self, HookEvent.TOOL_REGISTER, fn_or_def)
            return self
        meta = getattr(fn_or_def, "_tool_meta", None)
        if meta is None:
            raise ValueError(f"{fn_or_def} is not decorated with @tool")
        td = ToolDef(
            name=meta["name"],
            description=meta["description"],
            function=fn_or_def,
            parameters=meta["schema"] or _build_param_schema(fn_or_def),
        )
        self._tools_registry[td.name] = td
        emit_fire_and_forget(self, HookEvent.TOOL_REGISTER, td)
        return self

    def unregister_tool(self, name: str) -> Self:
        if not hasattr(self, "_tools_registry"):
            self.__init_uses_tools__()
        self._tools_registry.pop(name, None)
        emit_fire_and_forget(self, HookEvent.TOOL_UNREGISTER, name)
        return self

    @property
    def tools(self) -> dict[str, ToolDef]:
        if not hasattr(self, "_tools_registry"):
            self.__init_uses_tools__()
        return dict(self._tools_registry)

    def _tools_schema(self) -> list[dict[str, Any]]:
        if not hasattr(self, "_tools_registry"):
            self.__init_uses_tools__()
        return [
            {
                "type": "function",
                "function": {
                    "name": t.name,
                    "description": t.description,
                    "parameters": t.parameters,
                },
            }
            for t in self._tools_registry.values()
        ]

    async def _execute_tool(self, name: str, arguments: dict[str, Any]) -> str:
        if not hasattr(self, "_tools_registry"):
            self.__init_uses_tools__()
        td = self._tools_registry.get(name)
        if td is None:
            return json.dumps({"error": f"Unknown tool: {name}"})
        try:
            result = await call_fn_kwargs(td.function, **arguments)
            return json.dumps(result, default=str)
        except Exception as e:
            logger.warning("Tool %s error: %s", name, e)
            return json.dumps({"error": str(e)})
