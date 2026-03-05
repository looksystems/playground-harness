from __future__ import annotations

import inspect
from typing import Any, Protocol, runtime_checkable


async def _call_fn(fn, *args):
    result = fn(*args)
    if inspect.isawaitable(result):
        return await result
    return result


@runtime_checkable
class Middleware(Protocol):
    async def pre(self, messages: list[dict], context: Any) -> list[dict]: ...
    async def post(self, message: dict, context: Any) -> dict: ...


class BaseMiddleware:
    async def pre(self, messages: list[dict], context: Any) -> list[dict]:
        return messages

    async def post(self, message: dict, context: Any) -> dict:
        return message


class HasMiddleware:
    def __init_has_middleware__(self) -> None:
        self._middleware: list[BaseMiddleware] = []

    def use(self, middleware: BaseMiddleware) -> None:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        self._middleware.append(middleware)

    async def _run_pre(self, messages: list[dict], context: Any) -> list[dict]:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            messages = await _call_fn(mw.pre, messages, context)
        return messages

    async def _run_post(self, message: dict, context: Any) -> dict:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            message = await _call_fn(mw.post, message, context)
        return message
