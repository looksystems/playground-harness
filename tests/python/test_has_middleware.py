import asyncio
import pytest
from src.python.has_middleware import HasMiddleware, BaseMiddleware


class MiddlewareUser(HasMiddleware):
    pass


class UppercaseMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        return [
            {**m, "content": m.get("content", "").upper()} if m.get("content") else m
            for m in messages
        ]

    async def post(self, message, context):
        if message.get("content"):
            message = {**message, "content": message["content"].upper()}
        return message


class PrefixMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        return [{"role": "system", "content": "PREFIX"}] + messages


class TestHasMiddleware:
    def test_use_adds_middleware(self):
        obj = MiddlewareUser()
        mw = UppercaseMiddleware()
        obj.use(mw)
        assert len(obj.middleware) == 1

    def test_run_pre_transforms_messages(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result[0]["content"] == "HELLO"

    def test_run_post_transforms_message(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        msg = {"role": "assistant", "content": "hello"}
        result = asyncio.run(obj._run_post(msg, None))
        assert result["content"] == "HELLO"

    def test_middleware_runs_in_order(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        obj.use(PrefixMiddleware())
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result[0]["content"] == "PREFIX"
        assert result[1]["content"] == "HELLO"

    def test_no_middleware(self):
        obj = MiddlewareUser()
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result == messages
