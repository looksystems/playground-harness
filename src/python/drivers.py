"""Shell and filesystem driver contracts."""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any, Callable

from src.python.virtual_fs import VirtualFS


class FilesystemDriver(ABC):
    """Contract for virtual filesystem implementations."""

    @abstractmethod
    def write(self, path: str, content: str | bytes) -> None: ...

    @abstractmethod
    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None: ...

    @abstractmethod
    def read(self, path: str) -> str | bytes: ...

    @abstractmethod
    def read_text(self, path: str) -> str: ...

    @abstractmethod
    def exists(self, path: str) -> bool: ...

    @abstractmethod
    def remove(self, path: str) -> None: ...

    @abstractmethod
    def is_dir(self, path: str) -> bool: ...

    @abstractmethod
    def listdir(self, path: str = "/") -> list[str]: ...

    @abstractmethod
    def find(self, root: str = "/", pattern: str = "*") -> list[str]: ...

    @abstractmethod
    def stat(self, path: str) -> dict[str, Any]: ...

    @abstractmethod
    def clone(self) -> FilesystemDriver: ...


class BuiltinFilesystemDriver(FilesystemDriver):
    """Wraps the existing VirtualFS as a FilesystemDriver."""

    def __init__(self, vfs: VirtualFS | None = None):
        self._vfs = vfs if vfs is not None else VirtualFS()

    def write(self, path: str, content: str | bytes) -> None:
        self._vfs.write(path, content)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._vfs.write_lazy(path, provider)

    def read(self, path: str) -> str | bytes:
        return self._vfs.read(path)

    def read_text(self, path: str) -> str:
        return self._vfs.read_text(path)

    def exists(self, path: str) -> bool:
        return self._vfs.exists(path)

    def remove(self, path: str) -> None:
        self._vfs.remove(path)

    def is_dir(self, path: str) -> bool:
        return self._vfs._is_dir(path)

    def listdir(self, path: str = "/") -> list[str]:
        return self._vfs.listdir(path)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        return self._vfs.find(root, pattern)

    def stat(self, path: str) -> dict[str, Any]:
        return self._vfs.stat(path)

    def clone(self) -> BuiltinFilesystemDriver:
        return BuiltinFilesystemDriver(self._vfs.clone())
