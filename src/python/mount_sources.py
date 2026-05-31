"""Concrete ``MountSource`` implementations."""

from __future__ import annotations

import os

from src.python.mount import MountSource, MountStat


class LocalFolderSource(MountSource):
    """Mirrors a host directory as a read-only mount source.

    Path-escape-safe: the real absolute root is resolved once, and every
    subpath is lexically normalized, joined, resolved, and asserted to live at
    or under the root (defeating ``../`` and symlink escapes). Absolute
    subpaths are rejected; out-of-root paths are treated as not-found.
    """

    def __init__(self, root: str):
        self._root = os.path.realpath(root)

    def _resolve(self, subpath: str) -> str | None:
        if os.path.isabs(subpath):
            return None
        candidate = os.path.realpath(os.path.join(self._root, subpath))
        if candidate != self._root and not candidate.startswith(self._root + os.sep):
            return None
        return candidate

    def stat(self, subpath: str) -> MountStat | None:
        resolved = self._resolve(subpath)
        if resolved is None or not os.path.exists(resolved):
            return None
        info = os.stat(resolved)
        return MountStat(
            is_dir=os.path.isdir(resolved),
            size=info.st_size,
            mtime=int(info.st_mtime),
        )

    def list(self, subpath: str) -> list[str]:
        resolved = self._resolve(subpath)
        if resolved is None:
            raise FileNotFoundError(subpath)
        return sorted(os.listdir(resolved))

    def read(self, subpath: str) -> bytes:
        resolved = self._resolve(subpath)
        if resolved is None:
            raise FileNotFoundError(subpath)
        with open(resolved, "rb") as handle:
            return handle.read()
