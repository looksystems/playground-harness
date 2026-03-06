from __future__ import annotations

import asyncio
import inspect
from typing import TYPE_CHECKING, Any, Callable

if TYPE_CHECKING:
    from src.python.has_hooks import HookEvent


async def call_fn(fn: Callable, *args: Any) -> Any:
    result = fn(*args)
    if inspect.isawaitable(result):
        return await result
    return result


async def call_fn_kwargs(fn: Callable, **kwargs: Any) -> Any:
    result = fn(**kwargs)
    if inspect.isawaitable(result):
        return await result
    return result


def emit_fire_and_forget(obj: Any, event: HookEvent, *args: Any) -> None:
    if not hasattr(obj, "_emit"):
        return
    try:
        loop = asyncio.get_running_loop()
        loop.create_task(obj._emit(event, *args))
    except RuntimeError:
        pass
