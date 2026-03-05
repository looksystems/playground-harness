import asyncio
import pytest
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ShellRegistry
from src.python.has_shell import HasShell
from src.python.uses_tools import UsesTools


class ShellOnly(HasShell):
    pass


class ShellWithTools(UsesTools, HasShell):
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
