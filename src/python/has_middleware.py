from __future__ import annotations

from typing import Any, Protocol, Self, runtime_checkable

from src.python._utils import call_fn_kwargs


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

    def use(self, middleware: BaseMiddleware) -> Self:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        self._middleware.append(middleware)
        return self

    def remove_middleware(self, middleware: BaseMiddleware) -> Self:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        try:
            self._middleware.remove(middleware)
        except ValueError:
            pass
        return self

    @property
    def middleware(self) -> list[BaseMiddleware]:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        return list(self._middleware)

    def _prepend_middleware(self, middleware: BaseMiddleware) -> None:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        self._middleware.insert(0, middleware)

    async def _run_pre(self, messages: list[dict], context: Any) -> list[dict]:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            messages = await call_fn_kwargs(mw.pre, messages=messages, context=context)
        return messages

    async def _run_post(self, message: dict, context: Any) -> dict:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            message = await call_fn_kwargs(mw.post, message=message, context=context)
        return message
