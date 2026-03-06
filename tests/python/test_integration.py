import asyncio
import pytest
from src.python.standard_agent import StandardAgent
from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import tool
from src.python.emits_events import EmitsEvents
from src.python.event_stream_parser import EventType, StreamConfig
from src.python.message_bus import ParsedEvent


class TestIntegration:
    def test_standard_agent_with_events(self):
        agent = StandardAgent(model="gpt-4")

        agent.register_event(EventType(
            name="user_response",
            description="Respond to user",
            schema={"data": {"message": "string"}},
        ))
        agent.default_events = ["user_response"]

        hook_log = []
        agent.on(HookEvent.RUN_START, lambda: hook_log.append("start"))

        @tool(description="Add numbers")
        async def add(a: int, b: int) -> int:
            return a + b

        agent.register_tool(add)

        assert len(agent.tools) == 1

        bus_events = []
        agent.bus.subscribe("user_response", lambda e, b: bus_events.append(e))

        active = agent._resolve_active_events()
        assert len(active) == 1

        prompt = agent._build_event_prompt(active)
        assert "user_response" in prompt

        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert hook_log == ["start"]

    def test_standard_agent_with_shell(self):
        agent = StandardAgent(model="gpt-4")

        # Shell is available via lazy init
        assert agent.shell is not None
        assert agent.fs is not None

        # Can write files and exec commands
        agent.fs.write("/data/test.txt", "hello world\n")
        result = agent.exec("cat /data/test.txt")
        assert result.stdout == "hello world\n"

        # exec tool is auto-registered (StandardAgent includes both UsesTools and HasShell)
        assert "exec" in agent.tools

        # Pipes work
        agent.fs.write("/data/nums.txt", "3\n1\n2\n")
        result = agent.exec("cat /data/nums.txt | sort")
        assert result.stdout == "1\n2\n3\n"
