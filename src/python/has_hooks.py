from __future__ import annotations

import asyncio
import logging
from collections import defaultdict
from enum import Enum
from typing import Any, Callable, Self

from src.python._utils import call_fn

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
    SHELL_CALL = "shell_call"
    SHELL_RESULT = "shell_result"
    SHELL_NOT_FOUND = "shell_not_found"
    SHELL_CWD = "shell_cwd"
    TOOL_REGISTER = "tool_register"
    TOOL_UNREGISTER = "tool_unregister"
    COMMAND_REGISTER = "command_register"
    COMMAND_UNREGISTER = "command_unregister"
    SKILL_MOUNT = "skill_mount"
    SKILL_UNMOUNT = "skill_unmount"
    SKILL_SETUP = "skill_setup"
    SKILL_TEARDOWN = "skill_teardown"
    SHELL_STDOUT_CHUNK = "shell_stdout_chunk"
    SHELL_STDERR_CHUNK = "shell_stderr_chunk"


class HasHooks:
    def __init_has_hooks__(self) -> None:
        self._hooks: dict[HookEvent, list[Callable]] = defaultdict(list)

    def on(self, event: HookEvent, callback: Callable) -> Self:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        self._hooks[event].append(callback)
        return self

    def remove_hook(self, event: HookEvent, callback: Callable) -> Self:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        cbs = self._hooks.get(event, [])
        try:
            cbs.remove(callback)
        except ValueError:
            pass
        return self

    @property
    def hooks(self) -> dict[HookEvent, list[Callable]]:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        return {k: list(v) for k, v in self._hooks.items()}

    async def _emit(self, event: HookEvent, *args: Any) -> None:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        callbacks = self._hooks.get(event, [])
        if not callbacks:
            return
        results = await asyncio.gather(
            *[call_fn(cb, *args) for cb in callbacks],
            return_exceptions=True,
        )
        for r in results:
            if isinstance(r, Exception):
                logger.warning("Hook %s error: %s", event.value, r)
