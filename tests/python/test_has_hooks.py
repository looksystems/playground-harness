import asyncio
import pytest
from src.python.has_hooks import HasHooks, HookEvent


class HookUser(HasHooks):
    pass


class TestHasHooks:
    def test_subscribe_and_emit(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.RUN_START, lambda: received.append("started"))
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["started"]

    def test_emit_with_args(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.TOOL_CALL, lambda name, args: received.append((name, args)))
        asyncio.run(obj._emit(HookEvent.TOOL_CALL, "add", {"a": 1}))
        assert received == [("add", {"a": 1})]

    def test_multiple_hooks_same_event(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.RUN_END, lambda: received.append("a"))
        obj.on(HookEvent.RUN_END, lambda: received.append("b"))
        asyncio.run(obj._emit(HookEvent.RUN_END))
        assert set(received) == {"a", "b"}

    def test_async_hook(self):
        obj = HookUser()
        received = []

        async def hook():
            received.append("async")

        obj.on(HookEvent.RUN_START, hook)
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["async"]

    def test_hook_error_does_not_propagate(self):
        obj = HookUser()
        received = []

        def bad_hook():
            raise ValueError("boom")

        obj.on(HookEvent.RUN_START, bad_hook)
        obj.on(HookEvent.RUN_START, lambda: received.append("ok"))
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["ok"]

    def test_no_hooks_registered(self):
        obj = HookUser()
        asyncio.run(obj._emit(HookEvent.RUN_START))  # should not raise
