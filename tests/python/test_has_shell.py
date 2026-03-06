import asyncio
import pytest
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ShellRegistry, ExecResult
from src.python.has_shell import HasShell
from src.python.has_hooks import HasHooks, HookEvent
from src.python.uses_tools import UsesTools, ToolDef


class ShellOnly(HasShell):
    pass


class ShellWithTools(UsesTools, HasShell):
    pass


class HookShellAgent(HasHooks, UsesTools, HasShell):
    pass


class TestHasShell:
    def test_default_shell(self):
        obj = ShellOnly()
        assert obj.shell is not None
        assert obj.fs is not None

    def test_shell_instance(self):
        fs = VirtualFS({"/a.txt": "hello"})
        shell = Shell(fs)
        obj = ShellOnly()
        obj.__init_has_shell__(shell=shell)
        assert obj.fs.read("/a.txt") == "hello"

    def test_shell_from_registry(self):
        ShellRegistry.reset()
        fs = VirtualFS({"/data.txt": "registry"})
        ShellRegistry.register("test-shell", Shell(fs))
        obj = ShellOnly()
        obj.__init_has_shell__(shell="test-shell")
        assert obj.fs.read("/data.txt") == "registry"
        # Verify it's a clone
        obj.fs.write("/new.txt", "local")
        s2 = ShellRegistry.get("test-shell")
        assert not s2.fs.exists("/new.txt")
        ShellRegistry.reset()

    def test_exec(self):
        obj = ShellOnly()
        obj.fs.write("/test.txt", "hello\n")
        result = obj.exec("cat /test.txt")
        assert result.stdout == "hello\n"

    def test_constructor_params(self):
        obj = ShellOnly()
        obj.__init_has_shell__(
            cwd="/home",
            env={"X": "1"},
            allowed_commands={"cat", "ls", "pwd", "echo"},
        )
        r = obj.exec("pwd")
        assert r.stdout.strip() == "/home"
        r = obj.exec("echo $X")
        assert r.stdout.strip() == "1"
        r = obj.exec("grep x y")
        assert r.exit_code == 127


class TestHasShellWithTools:
    def test_auto_registers_exec_tool(self):
        obj = ShellWithTools()
        # Trigger shell init which should auto-register the tool
        _ = obj.shell
        assert "exec" in obj._tools

    def test_exec_tool_works(self):
        obj = ShellWithTools()
        obj.fs.write("/test.txt", "hello\n")
        tool_def = obj._tools["exec"]
        result = asyncio.run(tool_def.function({"command": "cat /test.txt"}))
        assert "hello" in result


class TestShellHooks:
    @pytest.mark.asyncio
    async def test_shell_call_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        calls = []
        agent.on(HookEvent.SHELL_CALL, lambda cmd: calls.append(cmd))
        agent.exec("echo hello")
        await asyncio.sleep(0)
        await asyncio.sleep(0)  # flush tasks
        assert calls == ["echo hello"]

    @pytest.mark.asyncio
    async def test_shell_result_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        results = []
        agent.on(HookEvent.SHELL_RESULT, lambda cmd, result: results.append((cmd, result.exit_code)))
        agent.exec("echo hello")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert results == [("echo hello", 0)]

    @pytest.mark.asyncio
    async def test_shell_not_found_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        not_found = []
        agent.on(HookEvent.SHELL_NOT_FOUND, lambda name: not_found.append(name))
        agent.exec("nonexistent arg1")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert not_found == ["nonexistent"]

    @pytest.mark.asyncio
    async def test_shell_not_found_in_pipeline(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        not_found = []
        agent.on(HookEvent.SHELL_NOT_FOUND, lambda name: not_found.append(name))
        agent.exec("echo hi | bogus")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert not_found == ["bogus"]

    @pytest.mark.asyncio
    async def test_shell_cwd_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        agent.fs.write("/tmp/.keep", "")
        cwd_changes = []
        agent.on(HookEvent.SHELL_CWD, lambda old, new: cwd_changes.append((old, new)))
        agent.exec("cd /tmp")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert len(cwd_changes) == 1
        assert cwd_changes[0][1] == "/tmp"

    @pytest.mark.asyncio
    async def test_no_shell_cwd_when_unchanged(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        cwd_changes = []
        agent.on(HookEvent.SHELL_CWD, lambda old, new: cwd_changes.append((old, new)))
        agent.exec("echo hello")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert cwd_changes == []

    def test_hooks_dont_fire_without_has_hooks(self):
        agent = ShellOnly()
        # Should not throw
        agent.exec("echo hello")

    @pytest.mark.asyncio
    async def test_command_register_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        registered = []
        agent.on(HookEvent.COMMAND_REGISTER, lambda name: registered.append(name))
        agent.register_command("mycmd", lambda args, stdin: ExecResult(stdout="ok\n"))
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert registered == ["mycmd"]

    @pytest.mark.asyncio
    async def test_command_unregister_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        agent.register_command("mycmd", lambda args, stdin: ExecResult(stdout="ok\n"))
        unregistered = []
        agent.on(HookEvent.COMMAND_UNREGISTER, lambda name: unregistered.append(name))
        agent.unregister_command("mycmd")
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert unregistered == ["mycmd"]

    @pytest.mark.asyncio
    async def test_tool_register_hook(self):
        agent = HookShellAgent()
        agent.__init_has_hooks__()
        agent.__init_has_shell__()
        registered = []
        agent.on(HookEvent.TOOL_REGISTER, lambda td: registered.append(td.name))
        agent.register_tool(ToolDef(
            name="test_tool",
            description="test",
            function=lambda **kw: "ok",
            parameters={"type": "object", "properties": {}},
        ))
        await asyncio.sleep(0)
        await asyncio.sleep(0)
        assert "test_tool" in registered
