"""In-memory virtual filesystem with lazy file support."""

from __future__ import annotations

import copy
import fnmatch
import os
from typing import Any, Callable


class VirtualFS:
    """Simple in-memory filesystem. Paths are always absolute and normalized."""

    def __init__(self, files: dict[str, str | bytes] | None = None):
        self._files: dict[str, str | bytes] = {}
        # Lazy file providers — called on first read, result cached
        self._lazy: dict[str, Callable[[], str | bytes]] = {}
        if files:
            for path, content in files.items():
                self.write(self._norm(path), content)

    @staticmethod
    def _norm(path: str) -> str:
        result = os.path.normpath("/" + path)
        # os.path.normpath preserves leading // on POSIX — collapse it
        if result.startswith("//"):
            result = result[1:]
        return result

    def write(self, path: str, content: str | bytes) -> None:
        path = self._norm(path)
        self._files[path] = content

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        """Register a lazy file — provider called on first read, then cached."""
        path = self._norm(path)
        self._lazy[path] = provider

    def read(self, path: str) -> str | bytes:
        path = self._norm(path)
        # Resolve lazy providers
        if path in self._lazy:
            self._files[path] = self._lazy.pop(path)()
        if path not in self._files:
            raise FileNotFoundError(f"{path}: No such file")
        return self._files[path]

    def read_text(self, path: str) -> str:
        content = self.read(path)
        return content if isinstance(content, str) else content.decode("utf-8", errors="replace")

    def exists(self, path: str) -> bool:
        path = self._norm(path)
        return path in self._files or path in self._lazy or self._is_dir(path)

    def remove(self, path: str) -> None:
        path = self._norm(path)
        if path in self._files:
            del self._files[path]
        elif path in self._lazy:
            del self._lazy[path]
        else:
            raise FileNotFoundError(f"{path}: No such file")

    def _all_paths(self) -> set[str]:
        return set(self._files.keys()) | set(self._lazy.keys())

    def _is_dir(self, path: str) -> bool:
        path = self._norm(path)
        prefix = path.rstrip("/") + "/"
        return any(p.startswith(prefix) for p in self._all_paths())

    def listdir(self, path: str = "/") -> list[str]:
        path = self._norm(path).rstrip("/") + "/"
        if path == "//":
            path = "/"
        entries: set[str] = set()
        for p in self._all_paths():
            if p.startswith(path) and p != path:
                rest = p[len(path):]
                entry = rest.split("/")[0]
                entries.add(entry)
        return sorted(entries)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        root = self._norm(root).rstrip("/")
        results = []
        for p in sorted(self._all_paths()):
            if not p.startswith(root):
                continue
            basename = p.rsplit("/", 1)[-1]
            if fnmatch.fnmatch(basename, pattern):
                results.append(p)
        return results

    def stat(self, path: str) -> dict[str, Any]:
        path = self._norm(path)
        if self._is_dir(path):
            return {"path": path, "type": "directory"}
        content = self.read(path)
        size = len(content.encode("utf-8") if isinstance(content, str) else content)
        return {"path": path, "type": "file", "size": size}

    def clone(self) -> VirtualFS:
        """Create an independent copy of this filesystem."""
        new = VirtualFS()
        new._files = copy.deepcopy(self._files)
        new._lazy = dict(self._lazy)
        return new
