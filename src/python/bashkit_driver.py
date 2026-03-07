"""BashkitDriver: auto-resolves native (future) vs IPC driver."""

from __future__ import annotations

import shutil
from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory
from src.python.bashkit_ipc_driver import BashkitIPCDriver
from src.python.bashkit_native_driver import BashkitNativeDriver


class BashkitDriver:
    """Resolves the best available bashkit driver (native or IPC)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return a bashkit ShellDriver, preferring native over IPC."""
        # 1. Try native FFI
        lib_override = kwargs.pop("lib_override", None)
        if lib_override is not None:
            return BashkitNativeDriver(lib_override=lib_override, **kwargs)
        if BashkitNativeDriver.find_library() is not None:
            return BashkitNativeDriver(**kwargs)
        # 2. Fall back to IPC
        if shutil.which("bashkit-cli"):
            return BashkitIPCDriver(**kwargs)
        raise RuntimeError(
            "bashkit not found — install libashkit, bashkit-cli, or the native extension"
        )


def register_bashkit_driver() -> None:
    """Register the 'bashkit' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("bashkit", lambda **kw: BashkitDriver.resolve(**kw))
