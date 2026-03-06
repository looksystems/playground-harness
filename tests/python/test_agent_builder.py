"""Comprehensive tests for the AgentBuilder fluent API and new public accessors."""

import asyncio
import pytest

from src.python.standard_agent import StandardAgent
from src.python.agent_builder import AgentBuilder
from src.python.has_hooks import HasHooks, HookEvent
from src.python.has_middleware import BaseMiddleware, HasMiddleware
from src.python.uses_tools import UsesTools, ToolDef, tool
from src.python.has_skills import HasSkills, Skill
from src.python.emits_events import EmitsEvents
from src.python.event_stream_parser import EventType


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

class _DummyMiddleware(BaseMiddleware):
    pass


class _DummySkill(Skill):
    @property
    def name(self):
        return "dummy"

    @property
    def description(self):
        return "A dummy skill"


@tool(description="test tool")
def dummy_tool(x: str) -> str:
    return x


def _noop_handler(args, stdin=""):
    from src.python.shell import ExecResult
    return ExecResult(stdout="ok\n")


# ---------------------------------------------------------------------------
# TestAgentBuilder
# ---------------------------------------------------------------------------

class TestAgentBuilder:
    def test_build_returns_builder(self):
        builder = StandardAgent.build("gpt-4")
        assert isinstance(builder, AgentBuilder)

    @pytest.mark.asyncio
    async def test_create_returns_agent(self):
        agent = await StandardAgent.build("gpt-4").create()
        assert isinstance(agent, StandardAgent)

    @pytest.mark.asyncio
    async def test_system_prompt(self):
        agent = await StandardAgent.build("gpt-4").system("You are helpful.").create()
        assert agent.system == "You are helpful."

    @pytest.mark.asyncio
    async def test_max_turns(self):
        agent = await StandardAgent.build("gpt-4").max_turns(5).create()
        assert agent.max_turns == 5

    @pytest.mark.asyncio
    async def test_tools_registered(self):
        agent = await StandardAgent.build("gpt-4").tools(dummy_tool).create()
        assert "dummy_tool" in agent.tools

    @pytest.mark.asyncio
    async def test_tool_singular(self):
        td = ToolDef(
            name="custom_tool",
            description="custom",
            function=lambda **kw: "ok",
            parameters={"type": "object", "properties": {}},
        )
        agent = await StandardAgent.build("gpt-4").tool(td).create()
        assert "custom_tool" in agent.tools

    @pytest.mark.asyncio
    async def test_middleware_registered(self):
        mw = _DummyMiddleware()
        agent = await StandardAgent.build("gpt-4").middleware(mw).create()
        assert len(agent.middleware) == 1

    @pytest.mark.asyncio
    async def test_hooks_registered(self):
        cb = lambda: None
        agent = await StandardAgent.build("gpt-4").on(HookEvent.RUN_START, cb).create()
        assert HookEvent.RUN_START in agent.hooks

    @pytest.mark.asyncio
    async def test_events_registered(self):
        et = EventType(name="my_event", description="test", schema={})
        agent = await StandardAgent.build("gpt-4").event(et).create()
        assert "my_event" in agent.events

    @pytest.mark.asyncio
    async def test_skills_mounted(self):
        agent = await StandardAgent.build("gpt-4").skill(_DummySkill()).create()
        assert "dummy" in agent.skills

    @pytest.mark.asyncio
    async def test_shell_configured(self):
        agent = await StandardAgent.build("gpt-4").shell(cwd="/tmp").create()
        assert agent.shell.cwd == "/tmp"

    @pytest.mark.asyncio
    async def test_command_registered(self):
        agent = (
            await StandardAgent.build("gpt-4")
            .shell()
            .command("greet", _noop_handler)
            .create()
        )
        result = agent.exec("greet")
        assert result.stdout == "ok\n"

    @pytest.mark.asyncio
    async def test_fluent_chaining(self):
        """All builder methods return self for chaining."""
        builder = StandardAgent.build("gpt-4")
        result = (
            builder
            .system("prompt")
            .max_turns(3)
            .max_retries(1)
            .stream(False)
            .litellm(temperature=0.5)
            .tool(dummy_tool)
            .middleware(_DummyMiddleware())
            .on(HookEvent.RUN_START, lambda: None)
            .event(EventType(name="e", description="", schema={}))
            .skill(_DummySkill())
            .shell()
            .command("x", _noop_handler)
        )
        assert result is builder


# ---------------------------------------------------------------------------
# TestOff
# ---------------------------------------------------------------------------

class TestRemoveHook:
    def test_remove_hook_removes_callback(self):
        agent = StandardAgent(model="gpt-4")
        received = []
        cb = lambda: received.append("fired")
        agent.on(HookEvent.RUN_START, cb)

        # Fires before remove_hook()
        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert received == ["fired"]

        received.clear()
        agent.remove_hook(HookEvent.RUN_START, cb)

        # Does not fire after remove_hook()
        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert received == []

    def test_remove_hook_returns_self(self):
        agent = StandardAgent(model="gpt-4")
        result = agent.remove_hook(HookEvent.RUN_START, lambda: None)
        assert result is agent

    def test_remove_hook_nonexistent_is_noop(self):
        agent = StandardAgent(model="gpt-4")
        # Should not raise
        agent.remove_hook(HookEvent.RUN_START, lambda: None)


# ---------------------------------------------------------------------------
# TestRemoveMiddleware
# ---------------------------------------------------------------------------

class TestRemoveMiddleware:
    def test_remove_middleware(self):
        agent = StandardAgent(model="gpt-4")
        mw = _DummyMiddleware()
        agent.use(mw)
        assert len(agent.middleware) == 1
        agent.remove_middleware(mw)
        assert len(agent.middleware) == 0

    def test_remove_middleware_returns_self(self):
        agent = StandardAgent(model="gpt-4")
        mw = _DummyMiddleware()
        agent.use(mw)
        result = agent.remove_middleware(mw)
        assert result is agent

    def test_remove_nonexistent_middleware_is_noop(self):
        agent = StandardAgent(model="gpt-4")
        # Should not raise
        agent.remove_middleware(_DummyMiddleware())


# ---------------------------------------------------------------------------
# TestUnregisterEvent
# ---------------------------------------------------------------------------

class TestUnregisterEvent:
    def test_unregister_event(self):
        agent = StandardAgent(model="gpt-4")
        et = EventType(name="my_event", description="test", schema={})
        agent.register_event(et)
        assert "my_event" in agent.events
        agent.unregister_event("my_event")
        assert "my_event" not in agent.events

    def test_unregister_event_returns_self(self):
        agent = StandardAgent(model="gpt-4")
        et = EventType(name="my_event", description="test", schema={})
        agent.register_event(et)
        result = agent.unregister_event("my_event")
        assert result is agent

    def test_unregister_nonexistent_event_is_noop(self):
        agent = StandardAgent(model="gpt-4")
        # Should not raise
        agent.unregister_event("nonexistent")


# ---------------------------------------------------------------------------
# TestReadOnlyAccessors
# ---------------------------------------------------------------------------

class TestReadOnlyAccessors:
    def test_tools_returns_copy(self):
        agent = StandardAgent(model="gpt-4")
        agent.register_tool(dummy_tool)
        tools = agent.tools
        tools.pop("dummy_tool")
        # Original should be unaffected
        assert "dummy_tool" in agent.tools

    def test_hooks_returns_copy(self):
        agent = StandardAgent(model="gpt-4")
        cb = lambda: None
        agent.on(HookEvent.RUN_START, cb)
        hooks = agent.hooks
        hooks.pop(HookEvent.RUN_START)
        # Original should be unaffected
        assert HookEvent.RUN_START in agent.hooks

    def test_middleware_returns_copy(self):
        agent = StandardAgent(model="gpt-4")
        mw = _DummyMiddleware()
        agent.use(mw)
        middleware = agent.middleware
        middleware.clear()
        # Original should be unaffected
        assert len(agent.middleware) == 1

    def test_events_returns_copy(self):
        agent = StandardAgent(model="gpt-4")
        et = EventType(name="test_event", description="test", schema={})
        agent.register_event(et)
        events = agent.events
        events.pop("test_event")
        # Original should be unaffected
        assert "test_event" in agent.events
