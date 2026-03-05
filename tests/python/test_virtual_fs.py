import pytest
from src.python.virtual_fs import VirtualFS


class TestVirtualFS:
    def test_write_and_read(self):
        fs = VirtualFS()
        fs.write("/hello.txt", "world")
        assert fs.read("/hello.txt") == "world"

    def test_read_text_decodes_bytes(self):
        fs = VirtualFS()
        fs.write("/bin.dat", b"\xff\xfe")
        assert isinstance(fs.read_text("/bin.dat"), str)

    def test_read_nonexistent_raises(self):
        fs = VirtualFS()
        with pytest.raises(FileNotFoundError):
            fs.read("/nope")

    def test_path_normalization(self):
        fs = VirtualFS()
        fs.write("//foo/../bar/baz.txt", "ok")
        assert fs.read("/bar/baz.txt") == "ok"

    def test_exists(self):
        fs = VirtualFS()
        fs.write("/a/b.txt", "x")
        assert fs.exists("/a/b.txt")
        assert fs.exists("/a")  # directory inferred
        assert not fs.exists("/nope")

    def test_remove(self):
        fs = VirtualFS()
        fs.write("/x.txt", "y")
        fs.remove("/x.txt")
        assert not fs.exists("/x.txt")

    def test_remove_nonexistent_raises(self):
        fs = VirtualFS()
        with pytest.raises(FileNotFoundError):
            fs.remove("/nope")

    def test_listdir(self):
        fs = VirtualFS()
        fs.write("/data/a.txt", "1")
        fs.write("/data/b.txt", "2")
        fs.write("/other/c.txt", "3")
        entries = fs.listdir("/data")
        assert sorted(entries) == ["a.txt", "b.txt"]

    def test_listdir_shows_subdirs(self):
        fs = VirtualFS()
        fs.write("/root/sub/file.txt", "x")
        entries = fs.listdir("/root")
        assert entries == ["sub"]

    def test_find(self):
        fs = VirtualFS()
        fs.write("/a/x.json", "{}")
        fs.write("/a/y.txt", "")
        fs.write("/b/z.json", "{}")
        results = fs.find("/", "*.json")
        assert sorted(results) == ["/a/x.json", "/b/z.json"]

    def test_stat_file(self):
        fs = VirtualFS()
        fs.write("/f.txt", "hello")
        s = fs.stat("/f.txt")
        assert s["type"] == "file"
        assert s["size"] == 5

    def test_stat_directory(self):
        fs = VirtualFS()
        fs.write("/d/f.txt", "x")
        s = fs.stat("/d")
        assert s["type"] == "directory"

    def test_write_lazy(self):
        called = False

        def provider():
            nonlocal called
            called = True
            return "lazy content"

        fs = VirtualFS()
        fs.write_lazy("/lazy.txt", provider)
        assert not called
        assert fs.read("/lazy.txt") == "lazy content"
        assert called
        assert fs.read("/lazy.txt") == "lazy content"

    def test_init_with_files(self):
        fs = VirtualFS({"/a.txt": "1", "/b.txt": "2"})
        assert fs.read("/a.txt") == "1"
        assert fs.read("/b.txt") == "2"

    def test_clone(self):
        fs = VirtualFS({"/a.txt": "original"})
        fs.write_lazy("/b.txt", lambda: "lazy")
        clone = fs.clone()
        clone.write("/a.txt", "modified")
        assert fs.read("/a.txt") == "original"
        assert clone.read("/a.txt") == "modified"
        assert clone.read("/b.txt") == "lazy"
