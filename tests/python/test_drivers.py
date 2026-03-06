from src.python.drivers import FilesystemDriver, BuiltinFilesystemDriver


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
