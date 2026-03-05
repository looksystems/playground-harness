import asyncio
import pytest
from src.python.standard_agent import StandardAgent
from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import tool


@tool(description="Add two numbers")
async def add(a: int, b: int) -> int:
    return a + b


class TestStandardAgent:
    def test_has_all_capabilities(self):
        agent = StandardAgent(model="gpt-4")
        assert hasattr(agent, "on")
        assert hasattr(agent, "use")
        assert hasattr(agent, "register_tool")
        assert hasattr(agent, "_emit")
        assert hasattr(agent, "_run_pre")
        assert hasattr(agent, "_tools_schema")

    def test_register_tool_and_hook(self):
        agent = StandardAgent(model="gpt-4")
        agent.register_tool(add)
        received = []
        agent.on(HookEvent.RUN_START, lambda: received.append("start"))
        assert "add" in agent._tools
        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert received == ["start"]

    def test_middleware_integration(self):
        agent = StandardAgent(model="gpt-4")

        class TestMW(BaseMiddleware):
            async def pre(self, messages, context):
                return messages + [{"role": "system", "content": "injected"}]

        agent.use(TestMW())
        messages = [{"role": "user", "content": "hi"}]
        result = asyncio.run(agent._run_pre(messages, None))
        assert len(result) == 2
        assert result[-1]["content"] == "injected"
