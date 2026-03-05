from __future__ import annotations

import asyncio
import inspect
import logging
from collections import defaultdict
from enum import Enum
from typing import Any, Callable

logger = logging.getLogger(__name__)


class HookEvent(str, Enum):
    RUN_START = "run_start"
    RUN_END = "run_end"
    LLM_REQUEST = "llm_request"
    LLM_RESPONSE = "llm_response"
    TOOL_CALL = "tool_call"
    TOOL_RESULT = "tool_result"
    TOOL_ERROR = "tool_error"
    RETRY = "retry"
    TOKEN_STREAM = "token_stream"
    ERROR = "error"


async def _call_fn(fn: Callable, *args: Any) -> Any:
    result = fn(*args)
    if inspect.isawaitable(result):
        return await result
    return result


class HasHooks:
    def __init_has_hooks__(self) -> None:
        self._hooks: dict[HookEvent, list[Callable]] = defaultdict(list)

    def on(self, event: HookEvent, callback: Callable) -> None:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        self._hooks[event].append(callback)

    async def _emit(self, event: HookEvent, *args: Any) -> None:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        callbacks = self._hooks.get(event, [])
        if not callbacks:
            return
        results = await asyncio.gather(
            *[_call_fn(cb, *args) for cb in callbacks],
            return_exceptions=True,
        )
        for r in results:
            if isinstance(r, Exception):
                logger.warning("Hook %s error: %s", event.value, r)
