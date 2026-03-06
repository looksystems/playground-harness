import asyncio
import pytest
from src.python.has_commands import HasCommands, CommandDef, command
from src.python.has_hooks import HasHooks, HookEvent
from src.python.uses_tools import UsesTools, ToolDef


# --- Minimal test classes ---

class CommandsOnly(HasCommands):
    pass


class CommandsWithTools(UsesTools, HasCommands):
    pass


class HookCommandAgent(HasHooks, UsesTools, HasCommands):
    pass


# --- Phase 1: Standalone HasCommands ---

class TestStandaloneCommands:
    def test_register_command_def(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="help",
            description="Show help",
            handler=lambda args: "help text",
            parameters={"type": "object", "properties": {}},
        )
        obj.register_slash_command(cdef)
        assert "help" in obj.commands

    def test_register_decorated_function(self):
        obj = CommandsOnly()

        @command(description="Greet user")
        def greet(name: str = "World") -> str:
            return f"Hello, {name}!"

        obj.register_slash_command(greet)
        assert "greet" in obj.commands

    def test_unregister_command(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="temp",
            description="Temporary",
            handler=lambda args: "temp",
            parameters={"type": "object", "properties": {}},
        )
        obj.register_slash_command(cdef)
        assert "temp" in obj.commands
        obj.unregister_slash_command("temp")
        assert "temp" not in obj.commands

    def test_execute_command(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="ping",
            description="Ping",
            handler=lambda args: "pong",
            parameters={"type": "object", "properties": {}},
        )
        obj.register_slash_command(cdef)
        result = obj.execute_slash_command("ping", {})
        assert result == "pong"

    def test_execute_unknown_command(self):
        obj = CommandsOnly()
        result = obj.execute_slash_command("nope", {})
        assert "error" in result.lower() or "unknown" in result.lower()

    def test_intercept_slash_command(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="help",
            description="Help",
            handler=lambda args: "help text",
            parameters={"type": "object", "properties": {}},
        )
        obj.register_slash_command(cdef)
        result = obj.intercept_slash_command("/help some args")
        assert result is not None
        name, args = result
        assert name == "help"
        assert args == {"input": "some args"}

    def test_intercept_non_slash(self):
        obj = CommandsOnly()
        result = obj.intercept_slash_command("hello")
        assert result is None

    def test_intercept_with_schema(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="greet",
            description="Greet",
            handler=lambda args: f"Hello, {args.get('name', 'World')}!",
            parameters={
                "type": "object",
                "properties": {"name": {"type": "string"}},
            },
        )
        obj.register_slash_command(cdef)
        result = obj.intercept_slash_command("/greet name=World")
        assert result is not None
        name, args = result
        assert name == "greet"
        assert args == {"name": "World"}

    def test_commands_property(self):
        obj = CommandsOnly()
        cdef = CommandDef(
            name="foo",
            description="Foo",
            handler=lambda args: "bar",
            parameters={"type": "object", "properties": {}},
        )
        obj.register_slash_command(cdef)
        cmds = obj.commands
        assert isinstance(cmds, dict)
        assert "foo" in cmds

    def test_lazy_init(self):
        obj = CommandsOnly()
        assert not hasattr(obj, "_commands")
        # Accessing commands triggers lazy init
        _ = obj.commands
        assert hasattr(obj, "_commands")


# --- Phase 2: With UsesTools ---

class TestCommandsWithTools:
    def test_auto_registers_tool(self):
        obj = CommandsWithTools()
        cdef = CommandDef(
            name="help",
            description="Show help",
            handler=lambda args: "help text",
            parameters={"type": "object", "properties": {}},
            llm_visible=True,
        )
        obj.register_slash_command(cdef)
        assert "slash_help" in obj._tools

    def test_no_tool_when_not_visible(self):
        obj = CommandsWithTools()
        cdef = CommandDef(
            name="secret",
            description="Secret cmd",
            handler=lambda args: "secret",
            parameters={"type": "object", "properties": {}},
            llm_visible=False,
        )
        obj.register_slash_command(cdef)
        if hasattr(obj, "_tools"):
            assert "slash_secret" not in obj._tools

    def test_agent_level_disable(self):
        obj = CommandsWithTools()
        obj.__init_has_commands__(llm_commands_enabled=False)
        cdef = CommandDef(
            name="help",
            description="Show help",
            handler=lambda args: "help text",
            parameters={"type": "object", "properties": {}},
            llm_visible=True,
        )
        obj.register_slash_command(cdef)
        if hasattr(obj, "_tools"):
            assert "slash_help" not in obj._tools

    def test_unregister_removes_tool(self):
        obj = CommandsWithTools()
        cdef = CommandDef(
            name="temp",
            description="Temporary",
            handler=lambda args: "temp",
            parameters={"type": "object", "properties": {}},
            llm_visible=True,
        )
        obj.register_slash_command(cdef)
        assert "slash_temp" in obj._tools
        obj.unregister_slash_command("temp")
        assert "slash_temp" not in obj._tools

    def test_tool_executes_command(self):
        obj = CommandsWithTools()
        cdef = CommandDef(
            name="ping",
            description="Ping",
            handler=lambda args: "pong",
            parameters={"type": "object", "properties": {}},
            llm_visible=True,
        )
        obj.register_slash_command(cdef)
        tool_def = obj._tools["slash_ping"]
        # The tool function should execute the command
        result = asyncio.run(tool_def.function({}))
        assert "pong" in result


# --- Phase 3: With HasHooks ---

class TestCommandHooks:
    @pytest.mark.asyncio
    async def test_register_hook(self):
        agent = HookCommandAgent()
        agent.__init_has_hooks__()
        registered = []
        agent.on(HookEvent.SLASH_COMMAND_REGISTER, lambda name: registered.append(name))
        cdef = CommandDef(
            name="help",
            description="Help",
            handler=lambda args: "help",
            parameters={"type": "object", "properties": {}},
        )
        agent.register_slash_command(cdef)
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert registered == ["help"]

    @pytest.mark.asyncio
    async def test_unregister_hook(self):
        agent = HookCommandAgent()
        agent.__init_has_hooks__()
        cdef = CommandDef(
            name="temp",
            description="Temp",
            handler=lambda args: "temp",
            parameters={"type": "object", "properties": {}},
        )
        agent.register_slash_command(cdef)
        unregistered = []
        agent.on(HookEvent.SLASH_COMMAND_UNREGISTER, lambda name: unregistered.append(name))
        agent.unregister_slash_command("temp")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert unregistered == ["temp"]

    @pytest.mark.asyncio
    async def test_call_hook(self):
        agent = HookCommandAgent()
        agent.__init_has_hooks__()
        calls = []
        agent.on(HookEvent.SLASH_COMMAND_CALL, lambda name, args: calls.append((name, args)))
        cdef = CommandDef(
            name="ping",
            description="Ping",
            handler=lambda args: "pong",
            parameters={"type": "object", "properties": {}},
        )
        agent.register_slash_command(cdef)
        agent.execute_slash_command("ping", {"input": "test"})
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert calls == [("ping", {"input": "test"})]

    @pytest.mark.asyncio
    async def test_result_hook(self):
        agent = HookCommandAgent()
        agent.__init_has_hooks__()
        results = []
        agent.on(HookEvent.SLASH_COMMAND_RESULT, lambda name, result: results.append((name, result)))
        cdef = CommandDef(
            name="ping",
            description="Ping",
            handler=lambda args: "pong",
            parameters={"type": "object", "properties": {}},
        )
        agent.register_slash_command(cdef)
        agent.execute_slash_command("ping", {})
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert results == [("ping", "pong")]


# --- Phase 4: SlashCommandMiddleware ---

class TestSlashCommandMiddleware:
    @pytest.mark.asyncio
    async def test_intercepts_slash_message(self):
        from src.python.slash_command_middleware import SlashCommandMiddleware

        agent = CommandsWithTools()
        cdef = CommandDef(
            name="help",
            description="Help",
            handler=lambda args: "help output",
            parameters={"type": "object", "properties": {}},
        )
        agent.register_slash_command(cdef)

        mw = SlashCommandMiddleware()

        class Context:
            pass

        ctx = Context()
        ctx.agent = agent

        messages = [{"role": "user", "content": "/help"}]
        result = await mw.pre(messages, ctx)
        assert result[0]["content"] == "[Slash command /help result]: help output"

    @pytest.mark.asyncio
    async def test_passes_through_regular(self):
        from src.python.slash_command_middleware import SlashCommandMiddleware

        agent = CommandsWithTools()
        mw = SlashCommandMiddleware()

        class Context:
            pass

        ctx = Context()
        ctx.agent = agent

        messages = [{"role": "user", "content": "hello world"}]
        result = await mw.pre(messages, ctx)
        assert result[0]["content"] == "hello world"
