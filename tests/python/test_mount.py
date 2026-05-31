"""Tests for programmatic mount sources (VFS overlay mounting).

Three layers, mirrored by name across all four language suites:
1. Wrapper unit (in-memory inner + fake source)
2. LocalFolderSource vs a real temp dir
3. End-to-end acceptance via the agent's shell + file tools
"""

import os

import pytest

from src.python.drivers import BuiltinFilesystemDriver
from src.python.has_shell import HasShell
from src.python.uses_tools import UsesTools
from src.python.mount import (
    MountSource,
    MountStat,
    MountingFilesystemDriver,
    Mountable,
)
from src.python.mount_sources import LocalFolderSource


class FakeSource(MountSource):
    """In-memory read-only source. `files` maps relative subpaths -> content."""

    def __init__(self, files: dict[str, str | bytes]):
        self._files = {k.strip("/"): v for k, v in files.items()}

    def _dirs(self) -> set[str]:
        dirs = {""}
        for p in self._files:
            parts = p.split("/")
            for i in range(len(parts) - 1):
                dirs.add("/".join(parts[: i + 1]))
        return dirs

    def stat(self, subpath: str) -> MountStat | None:
        subpath = subpath.strip("/")
        if subpath in self._files:
            c = self._files[subpath]
            size = len(c.encode() if isinstance(c, str) else c)
            return MountStat(is_dir=False, size=size, mtime=0)
        if subpath in self._dirs():
            return MountStat(is_dir=True)
        return None

    def list(self, subpath: str) -> list[str]:
        subpath = subpath.strip("/")
        prefix = subpath + "/" if subpath else ""
        names: set[str] = set()
        for p in self._files:
            if p.startswith(prefix) and p != subpath:
                names.add(p[len(prefix):].split("/")[0])
        return sorted(names)

    def read(self, subpath: str) -> str | bytes:
        return self._files[subpath.strip("/")]


def _inner() -> BuiltinFilesystemDriver:
    return BuiltinFilesystemDriver()


class TestMountingFilesystemDriver:
    def test_empty_table_passthrough(self):
        inner = _inner()
        inner.write("/a.txt", "hi")
        fs = MountingFilesystemDriver(inner)
        assert fs.read("/a.txt") == "hi"
        assert fs.listdir("/") == inner.listdir("/")
        assert fs.exists("/a.txt")
        assert fs.mounts() == []

    def test_longest_prefix_routing(self):
        fs = MountingFilesystemDriver(_inner())
        outer = FakeSource({"x.txt": "outer"})
        inner_src = FakeSource({"y.txt": "inner"})
        fs.mount("/a", outer)
        fs.mount("/a/b", inner_src)
        # /a/b/y.txt routes to the longer (/a/b) mount
        assert fs.read("/a/b/y.txt") == "inner"
        # /a/x.txt routes to /a
        assert fs.read("/a/x.txt") == "outer"

    def test_read_from_source(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"hello.txt": "world"}))
        assert fs.read("/work/hello.txt") == "world"
        assert fs.read_text("/work/hello.txt") == "world"

    def test_exists_mount_point_and_ancestor(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/deep/mnt", FakeSource({"f.txt": "x"}))
        assert fs.exists("/deep/mnt")  # mount point
        assert fs.exists("/deep")  # ancestor
        assert fs.exists("/deep/mnt/f.txt")  # source file
        assert not fs.exists("/deep/mnt/nope.txt")
        assert fs.is_dir("/deep")
        assert fs.is_dir("/deep/mnt")
        assert not fs.is_dir("/deep/mnt/f.txt")

    def test_listdir_merge_dedup_sorted(self):
        inner = _inner()
        inner.write("/work/local.txt", "L")
        inner.write("/work/shared.txt", "edited")  # also in source -> dedup
        fs = MountingFilesystemDriver(inner)
        fs.mount("/work", FakeSource({"shared.txt": "orig", "sub/deep.txt": "d"}))
        assert fs.listdir("/work") == ["local.txt", "shared.txt", "sub"]

    def test_listdir_root_shows_mount_point(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/repo", FakeSource({"a.txt": "x"}))
        assert "repo" in fs.listdir("/")

    def test_copy_up_shadowing(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"hello.txt": "original"}))
        assert fs.read("/work/hello.txt") == "original"
        fs.write("/work/hello.txt", "edited")
        assert fs.read("/work/hello.txt") == "edited"
        # listed exactly once despite existing in both layers
        assert fs.listdir("/work").count("hello.txt") == 1

    def test_remove_copied_up_unshadows(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"hello.txt": "original"}))
        fs.write("/work/hello.txt", "edited")
        fs.remove("/work/hello.txt")
        # source still visible after un-shadowing
        assert fs.read("/work/hello.txt") == "original"

    def test_remove_source_only_raises(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"hello.txt": "original"}))
        with pytest.raises((PermissionError, FileNotFoundError, OSError)):
            fs.remove("/work/hello.txt")

    def test_find_merged(self):
        inner = _inner()
        inner.write("/work/local.md", "L")
        fs = MountingFilesystemDriver(inner)
        fs.mount("/work", FakeSource({"a.md": "x", "sub/b.md": "y", "c.txt": "z"}))
        found = fs.find("/work", "*.md")
        assert sorted(found) == ["/work/a.md", "/work/local.md", "/work/sub/b.md"]

    def test_stat_source_file(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"hello.txt": "world"}))
        s = fs.stat("/work/hello.txt")
        assert s["type"] == "file"
        assert s["size"] == 5
        d = fs.stat("/work")
        assert d["type"] == "directory"

    def test_clone_writable_diverges_source_shared(self):
        fs = MountingFilesystemDriver(_inner())
        src = FakeSource({"hello.txt": "original"})
        fs.mount("/work", src)
        fs.write("/work/local.txt", "L")
        clone = fs.clone()
        # writable layer is independent
        clone.write("/work/local.txt", "changed")
        assert fs.read("/work/local.txt") == "L"
        assert clone.read("/work/local.txt") == "changed"
        # source shared (same mount table content)
        assert clone.mounts() == fs.mounts()
        assert clone.read("/work/hello.txt") == "original"

    def test_unmount(self):
        fs = MountingFilesystemDriver(_inner())
        fs.mount("/work", FakeSource({"a.txt": "x"}))
        assert fs.mounts() == ["/work"]
        fs.unmount("/work")
        assert fs.mounts() == []
        assert not fs.exists("/work/a.txt")

    def test_is_mountable(self):
        fs = MountingFilesystemDriver(_inner())
        assert isinstance(fs, Mountable)


class TestLocalFolderSource:
    def test_nested_files_reflected(self, tmp_path):
        (tmp_path / "a.txt").write_text("alpha")
        sub = tmp_path / "sub"
        sub.mkdir()
        (sub / "b.txt").write_text("beta")

        src = LocalFolderSource(str(tmp_path))
        assert src.stat("").is_dir
        assert src.stat("a.txt").is_dir is False
        assert src.stat("a.txt").size == 5
        assert sorted(src.list("")) == ["a.txt", "sub"]
        assert src.list("sub") == ["b.txt"]
        assert _as_text(src.read("a.txt")) == "alpha"
        assert _as_text(src.read("sub/b.txt")) == "beta"
        assert src.stat("nope.txt") is None

    def test_parent_traversal_cannot_escape(self, tmp_path):
        root = tmp_path / "root"
        root.mkdir()
        (root / "in.txt").write_text("inside")
        (tmp_path / "secret.txt").write_text("secret")

        src = LocalFolderSource(str(root))
        assert src.stat("../secret.txt") is None

    def test_symlink_cannot_escape(self, tmp_path):
        root = tmp_path / "root"
        root.mkdir()
        outside = tmp_path / "outside.txt"
        outside.write_text("secret")
        link = root / "link.txt"
        try:
            os.symlink(str(outside), str(link))
        except (OSError, NotImplementedError):
            pytest.skip("symlinks unsupported")

        src = LocalFolderSource(str(root))
        assert src.stat("link.txt") is None


def _as_text(content: str | bytes) -> str:
    return content if isinstance(content, str) else content.decode()


class _MountAgent(UsesTools, HasShell):
    pass


class TestMountE2E:
    def _agent(self):
        return _MountAgent()

    def test_mount_visible_to_tools_and_shell(self, tmp_path):
        (tmp_path / "hello.txt").write_text("from-host")
        (tmp_path / "notes.md").write_text("# notes")
        sub = tmp_path / "sub"
        sub.mkdir()
        (sub / "deep.txt").write_text("deep-content")

        agent = self._agent()
        agent.mount_source("/work", LocalFolderSource(str(tmp_path)))

        # shell sees content
        assert "hello.txt" in agent.exec("ls /work").stdout
        assert "from-host" in agent.exec("cat /work/hello.txt").stdout
        assert "notes.md" in agent.exec("find /work -name '*.md'").stdout
        assert "deep-content" in agent.exec("grep -r deep /work").stdout

        # file tools see content (same merged fs)
        fs = agent.shell.fs
        assert fs.read_text("/work/sub/deep.txt") == "deep-content"
        assert fs.exists("/work/notes.md")

    def test_edit_mounted_file_copy_up_host_unchanged(self, tmp_path):
        host_file = tmp_path / "hello.txt"
        host_file.write_text("from-host")

        agent = self._agent()
        agent.mount_source("/work", LocalFolderSource(str(tmp_path)))

        agent.exec("echo edited > /work/hello.txt")
        assert agent.exec("cat /work/hello.txt").stdout.strip() == "edited"
        # host file untouched (read-only source + copy-up)
        assert host_file.read_text() == "from-host"

    def test_mounts_listing(self, tmp_path):
        agent = self._agent()
        agent.mount_source("/work", LocalFolderSource(str(tmp_path)))
        assert agent.mount_sources() == ["/work"]
        agent.unmount_source("/work")
        assert agent.mount_sources() == []
