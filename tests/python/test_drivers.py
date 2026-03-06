from src.python.drivers import (
    FilesystemDriver,
    BuiltinFilesystemDriver,
    ShellDriver,
    BuiltinShellDriver,
    ShellDriverFactory,
)
from src.python.shell import ExecResult


class TestFilesystemDriverContract:
    def test_builtin_fs_implements_contract(self):
        fs = BuiltinFilesystemDriver()
        assert isinstance(fs, FilesystemDriver)

    def test_write_and_read(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/test.txt", "hello")
        assert fs.read("/test.txt") == "hello"

    def test_write_lazy(self):
        fs = BuiltinFilesystemDriver()
        fs.write_lazy("/lazy.txt", lambda: "lazy content")
        assert fs.read("/lazy.txt") == "lazy content"

    def test_read_text(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/t.txt", "text")
        assert fs.read_text("/t.txt") == "text"

    def test_exists(self):
        fs = BuiltinFilesystemDriver()
        assert not fs.exists("/nope.txt")
        fs.write("/yes.txt", "y")
        assert fs.exists("/yes.txt")

    def test_remove(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/rm.txt", "bye")
        fs.remove("/rm.txt")
        assert not fs.exists("/rm.txt")

    def test_is_dir(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/dir/file.txt", "x")
        assert fs.is_dir("/dir")
        assert not fs.is_dir("/dir/file.txt")

    def test_listdir(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/d/a.txt", "a")
        fs.write("/d/b.txt", "b")
        assert fs.listdir("/d") == ["a.txt", "b.txt"]

    def test_find(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/src/main.py", "x")
        fs.write("/src/util.py", "y")
        assert fs.find("/src", "*.py") == ["/src/main.py", "/src/util.py"]

    def test_stat(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/s.txt", "hello")
        info = fs.stat("/s.txt")
        assert info["type"] == "file"
        assert info["size"] == 5

    def test_clone(self):
        fs = BuiltinFilesystemDriver()
        fs.write("/a.txt", "a")
        cloned = fs.clone()
        cloned.write("/b.txt", "b")
        assert not fs.exists("/b.txt")
        assert isinstance(cloned, FilesystemDriver)


class TestShellDriverContract:
    def test_builtin_shell_implements_contract(self):
        driver = BuiltinShellDriver()
        assert isinstance(driver, ShellDriver)

    def test_exec(self):
        driver = BuiltinShellDriver()
        driver.fs.write("/test.txt", "hello")
        result = driver.exec("cat /test.txt")
        assert isinstance(result, ExecResult)
        assert result.stdout == "hello"

    def test_register_and_exec_custom_command(self):
        driver = BuiltinShellDriver()
        driver.register_command("greet", lambda args, stdin: ExecResult(stdout="hi\n"))
        result = driver.exec("greet")
        assert result.stdout == "hi\n"

    def test_unregister_custom_command(self):
        driver = BuiltinShellDriver()
        driver.register_command("tmp", lambda args, stdin: ExecResult(stdout="x"))
        driver.unregister_command("tmp")
        result = driver.exec("tmp")
        assert result.exit_code == 127

    def test_clone(self):
        driver = BuiltinShellDriver()
        driver.fs.write("/a.txt", "a")
        cloned = driver.clone()
        cloned.fs.write("/b.txt", "b")
        assert not driver.fs.exists("/b.txt")
        assert isinstance(cloned, ShellDriver)

    def test_cwd_and_env(self):
        driver = BuiltinShellDriver(cwd="/tmp", env={"X": "42"})
        assert driver.cwd == "/tmp"
        assert driver.env["X"] == "42"

    def test_fs_is_filesystem_driver(self):
        driver = BuiltinShellDriver()
        assert isinstance(driver.fs, FilesystemDriver)

    def test_on_not_found_callback(self):
        driver = BuiltinShellDriver()
        not_found = []
        driver.on_not_found = lambda name: not_found.append(name)
        driver.exec("boguscmd")
        assert not_found == ["boguscmd"]


class TestShellDriverFactory:
    def setup_method(self):
        ShellDriverFactory.reset()

    def test_default_is_builtin(self):
        driver = ShellDriverFactory.create()
        assert isinstance(driver, BuiltinShellDriver)

    def test_create_by_name(self):
        driver = ShellDriverFactory.create("builtin")
        assert isinstance(driver, BuiltinShellDriver)

    def test_unknown_driver_raises(self):
        import pytest
        with pytest.raises(KeyError):
            ShellDriverFactory.create("nonexistent")

    def test_register_custom_driver(self):
        ShellDriverFactory.register("custom", lambda **kw: BuiltinShellDriver(**kw))
        driver = ShellDriverFactory.create("custom")
        assert isinstance(driver, BuiltinShellDriver)

    def test_set_default(self):
        ShellDriverFactory.register("alt", lambda **kw: BuiltinShellDriver(**kw))
        ShellDriverFactory.default = "alt"
        driver = ShellDriverFactory.create()
        assert isinstance(driver, BuiltinShellDriver)

    def test_create_passes_kwargs(self):
        driver = ShellDriverFactory.create("builtin", cwd="/opt", env={"A": "1"})
        assert driver.cwd == "/opt"
        assert driver.env["A"] == "1"
