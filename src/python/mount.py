"""Programmatic mount sources for the virtual filesystem.

A ``MountSource`` exposes a live, read-only directory tree of unknown shape
(a host folder, a GitHub repo, Slack messages, a DB table, ...). The
``MountingFilesystemDriver`` overlays one or more sources onto an inner
writable ``FilesystemDriver`` using copy-up semantics: writes shadow the
source in the inner layer (the source is never mutated), and deleting a
source-only path raises (read-only, no whiteout/tombstone in v1).

See docs/adr/0034-programmatic-mount-sources.md.
"""

from __future__ import annotations

import fnmatch
import os
from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any, Callable, Protocol, runtime_checkable

from src.python.drivers import FilesystemDriver


@dataclass
class MountStat:
    """Lightweight stat for a path inside a mount source."""

    is_dir: bool
    size: int = 0
    mtime: int = 0


class MountSource(ABC):
    """Read-only provider of a directory tree, addressed by relative subpaths.

    ``""`` is the mount root. ``stat`` is called constantly to check existence
    and must not raise for missing paths (return ``None``). ``list``/``read``
    are only called after ``stat`` confirms existence and may raise on real
    I/O faults.
    """

    @abstractmethod
    def stat(self, subpath: str) -> MountStat | None: ...

    @abstractmethod
    def list(self, subpath: str) -> list[str]: ...

    @abstractmethod
    def read(self, subpath: str) -> str | bytes: ...


@runtime_checkable
class Mountable(Protocol):
    """Capability interface for filesystems that support mounting sources."""

    def mount(self, mount_point: str, source: MountSource) -> None: ...

    def unmount(self, mount_point: str) -> None: ...

    def mounts(self) -> list[str]: ...


def _norm(path: str) -> str:
    result = os.path.normpath("/" + path)
    if result.startswith("//"):
        result = result[1:]
    return result


class MountingFilesystemDriver(FilesystemDriver):
    """Overlays read-only ``MountSource``s on an inner writable driver.

    Empty mount table => every method is a transparent passthrough to inner.
    """

    def __init__(
        self,
        inner: FilesystemDriver,
        mounts: list[tuple[str, MountSource]] | None = None,
    ):
        self._inner = inner
        # Ordered mount table: (mount_point, source).
        self._mounts: list[tuple[str, MountSource]] = list(mounts) if mounts else []

    # -- Mountable capability ------------------------------------------------

    def mount(self, mount_point: str, source: MountSource) -> None:
        mount_point = _norm(mount_point)
        self.unmount(mount_point)
        self._mounts.append((mount_point, source))

    def unmount(self, mount_point: str) -> None:
        mount_point = _norm(mount_point)
        self._mounts = [(m, s) for m, s in self._mounts if m != mount_point]

    def mounts(self) -> list[str]:
        return sorted(m for m, _ in self._mounts)

    # -- Routing helpers -----------------------------------------------------

    def _resolve_mount(self, path: str) -> tuple[str, MountSource, str] | None:
        """Longest matching mount prefix -> (mount_point, source, subpath)."""
        path = _norm(path)
        best: tuple[str, MountSource, str] | None = None
        for mount_point, source in self._mounts:
            if path == mount_point:
                subpath = ""
            elif mount_point == "/":
                subpath = path[1:]
            elif path.startswith(mount_point + "/"):
                subpath = path[len(mount_point) + 1:]
            else:
                continue
            if best is None or len(mount_point) > len(best[0]):
                best = (mount_point, source, subpath)
        return best

    def _is_mount_point_or_ancestor(self, path: str) -> bool:
        path = _norm(path)
        prefix = "/" if path == "/" else path + "/"
        for mount_point, _ in self._mounts:
            if mount_point == path or mount_point.startswith(prefix):
                return True
        return False

    # -- FilesystemDriver ----------------------------------------------------

    def write(self, path: str, content: str | bytes) -> None:
        self._inner.write(path, content)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._inner.write_lazy(path, provider)

    def read(self, path: str) -> str | bytes:
        if self._inner.exists(path) and not self._inner.is_dir(path):
            return self._inner.read(path)
        resolved = self._resolve_mount(path)
        if resolved is not None:
            _, source, subpath = resolved
            st = source.stat(subpath)
            if st is not None and not st.is_dir:
                return source.read(subpath)
        return self._inner.read(path)  # raises FileNotFoundError

    def read_text(self, path: str) -> str:
        content = self.read(path)
        return content if isinstance(content, str) else content.decode(
            "utf-8", errors="replace"
        )

    def exists(self, path: str) -> bool:
        if self._inner.exists(path):
            return True
        if self._is_mount_point_or_ancestor(path):
            return True
        resolved = self._resolve_mount(path)
        if resolved is not None:
            _, source, subpath = resolved
            return source.stat(subpath) is not None
        return False

    def remove(self, path: str) -> None:
        if self._inner.exists(path):
            self._inner.remove(path)
            return
        resolved = self._resolve_mount(path)
        if resolved is not None:
            _, source, subpath = resolved
            if source.stat(subpath) is not None:
                raise PermissionError(
                    f"{_norm(path)}: read-only mount source (cannot delete)"
                )
        raise FileNotFoundError(f"{_norm(path)}: No such file")

    def is_dir(self, path: str) -> bool:
        if self._inner.is_dir(path):
            return True
        if self._is_mount_point_or_ancestor(path):
            return True
        resolved = self._resolve_mount(path)
        if resolved is not None:
            _, source, subpath = resolved
            st = source.stat(subpath)
            return st is not None and st.is_dir
        return False

    # private alias used by the shell builtins (parity with VirtualFS)
    _is_dir = is_dir

    def listdir(self, path: str = "/") -> list[str]:
        path = _norm(path)
        entries: set[str] = set(self._inner.listdir(path))
        # Source children when this path is a directory inside a mount.
        resolved = self._resolve_mount(path)
        if resolved is not None:
            _, source, subpath = resolved
            st = source.stat(subpath)
            if st is not None and st.is_dir:
                entries.update(source.list(subpath))
        # Mount points whose first segment is rooted at this directory.
        prefix = "/" if path == "/" else path + "/"
        for mount_point, _ in self._mounts:
            if mount_point.startswith(prefix):
                entries.add(mount_point[len(prefix):].split("/")[0])
        return sorted(entries)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        root = _norm(root)
        results: list[str] = []

        def walk(directory: str) -> None:
            for name in self.listdir(directory):
                full = (directory.rstrip("/") + "/" + name) if directory != "/" else "/" + name
                is_dir = self.is_dir(full)
                if not is_dir and fnmatch.fnmatch(name, pattern):
                    results.append(full)
                if is_dir:
                    walk(full)

        if self.is_dir(root):
            walk(root)
        return sorted(results)

    def stat(self, path: str) -> dict[str, Any]:
        if self._inner.exists(path):
            return self._inner.stat(path)
        npath = _norm(path)
        if self._is_mount_point_or_ancestor(npath):
            return {"path": npath, "type": "directory"}
        resolved = self._resolve_mount(npath)
        if resolved is not None:
            _, source, subpath = resolved
            st = source.stat(subpath)
            if st is not None:
                if st.is_dir:
                    return {"path": npath, "type": "directory"}
                return {
                    "path": npath,
                    "type": "file",
                    "size": st.size,
                    "mtime": st.mtime,
                }
        return self._inner.stat(path)  # raises FileNotFoundError

    def clone(self) -> MountingFilesystemDriver:
        # Sources are read-only and shareable; copy the table by reference.
        return MountingFilesystemDriver(self._inner.clone(), list(self._mounts))
