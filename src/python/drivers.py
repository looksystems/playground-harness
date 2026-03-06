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


from src.python.shell import Shell, ExecResult


class ShellDriver(ABC):
    """Contract for shell interpreter implementations."""

    @property
    @abstractmethod
    def fs(self) -> FilesystemDriver: ...

    @property
    @abstractmethod
    def cwd(self) -> str: ...

    @property
    @abstractmethod
    def env(self) -> dict[str, str]: ...

    @abstractmethod
    def exec(self, command: str) -> ExecResult: ...

    @abstractmethod
    def register_command(self, name: str, handler: Callable) -> None: ...

    @abstractmethod
    def unregister_command(self, name: str) -> None: ...

    @abstractmethod
    def clone(self) -> ShellDriver: ...

    @property
    @abstractmethod
    def on_not_found(self) -> Callable | None: ...

    @on_not_found.setter
    @abstractmethod
    def on_not_found(self, callback: Callable | None) -> None: ...


class BuiltinShellDriver(ShellDriver):
    """Wraps the existing Shell as a ShellDriver."""

    def __init__(
        self,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
        max_output: int = 16_000,
        max_iterations: int = 10_000,
    ):
        self._shell = Shell(
            cwd=cwd,
            env=env or {},
            allowed_commands=allowed_commands,
            max_output=max_output,
            max_iterations=max_iterations,
        )
        self._fs_driver = BuiltinFilesystemDriver(self._shell.fs)

    @property
    def fs(self) -> FilesystemDriver:
        return self._fs_driver

    @property
    def cwd(self) -> str:
        return self._shell.cwd

    @property
    def env(self) -> dict[str, str]:
        return self._shell.env

    def exec(self, command: str) -> ExecResult:
        return self._shell.exec(command)

    def register_command(self, name: str, handler: Callable) -> None:
        self._shell.register_command(name, handler)

    def unregister_command(self, name: str) -> None:
        self._shell.unregister_command(name)

    def clone(self) -> BuiltinShellDriver:
        cloned_shell = self._shell.clone()
        driver = BuiltinShellDriver.__new__(BuiltinShellDriver)
        driver._shell = cloned_shell
        driver._fs_driver = BuiltinFilesystemDriver(cloned_shell.fs)
        return driver

    @property
    def on_not_found(self) -> Callable | None:
        return self._shell.on_not_found

    @on_not_found.setter
    def on_not_found(self, callback: Callable | None) -> None:
        self._shell.on_not_found = callback

    @property
    def _custom_commands(self) -> dict:
        """Expose Shell internals for backward compatibility."""
        return self._shell._custom_commands

    @staticmethod
    def from_shell(shell: Shell) -> BuiltinShellDriver:
        """Create from an existing Shell instance."""
        driver = BuiltinShellDriver.__new__(BuiltinShellDriver)
        driver._shell = shell
        driver._fs_driver = BuiltinFilesystemDriver(shell.fs)
        return driver


class ShellDriverFactory:
    """Resolves driver names to ShellDriver instances."""

    default: str = "builtin"
    _registry: dict[str, Callable[..., ShellDriver]] = {}

    @classmethod
    def register(cls, name: str, factory: Callable[..., ShellDriver]) -> None:
        cls._registry[name] = factory

    @classmethod
    def create(cls, name: str | None = None, **kwargs: Any) -> ShellDriver:
        name = name or cls.default
        if name == "builtin":
            return BuiltinShellDriver(**kwargs)
        if name not in cls._registry:
            raise KeyError(f"Shell driver '{name}' not registered")
        return cls._registry[name](**kwargs)

    @classmethod
    def reset(cls) -> None:
        cls._registry.clear()
        cls.default = "builtin"
