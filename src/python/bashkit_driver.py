"""BashkitDriver: auto-resolves native (future) vs IPC driver."""

from __future__ import annotations

import shutil
from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory
from src.python.bashkit_ipc_driver import BashkitIPCDriver


class BashkitDriver:
    """Resolves the best available bashkit driver (native or IPC)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return a bashkit ShellDriver, preferring native (Phase 3) over IPC."""
        # Phase 3: check for native extension first (future)
        if shutil.which("bashkit-cli"):
            return BashkitIPCDriver(**kwargs)
        raise RuntimeError(
            "bashkit not found — install bashkit-cli or the native extension"
        )


def register_bashkit_driver() -> None:
    """Register the 'bashkit' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("bashkit", lambda **kw: BashkitDriver.resolve(**kw))
