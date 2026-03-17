"""Shared remote sync utilities for drivers that execute commands in external processes.

Provides:
- DirtyTrackingFS: filesystem wrapper that tracks written/removed paths
- Preamble/epilogue builders for syncing VFS state to/from a remote shell
- Parsing and applying file listings from remote execution
"""

from __future__ import annotations

import base64
from typing import Any, Callable

from src.python.drivers import FilesystemDriver, BuiltinFilesystemDriver


class DirtyTrackingFS(FilesystemDriver):
    """Wraps a FilesystemDriver to track which paths have been written to."""

    def __init__(self, inner: FilesystemDriver):
        self._inner = inner
        self._dirty: set[str] = set()

    @property
    def inner(self) -> FilesystemDriver:
        return self._inner

    @property
    def dirty(self) -> set[str]:
        return self._dirty

    def clear_dirty(self) -> None:
        self._dirty.clear()

    def write(self, path: str, content: str | bytes) -> None:
        self._inner.write(path, content)
        self._dirty.add(path)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        self._inner.write_lazy(path, provider)
        self._dirty.add(path)

    def read(self, path: str) -> str | bytes:
        return self._inner.read(path)

    def read_text(self, path: str) -> str:
        return self._inner.read_text(path)

    def exists(self, path: str) -> bool:
        return self._inner.exists(path)

    def remove(self, path: str) -> None:
        self._inner.remove(path)
        self._dirty.add(path)

    def is_dir(self, path: str) -> bool:
        return self._inner.is_dir(path)

    def listdir(self, path: str = "/") -> list[str]:
        return self._inner.listdir(path)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        return self._inner.find(root, pattern)

    def stat(self, path: str) -> dict[str, Any]:
        return self._inner.stat(path)

    def clone(self) -> DirtyTrackingFS:
        return DirtyTrackingFS(self._inner.clone())


# The shell script that lists all files with base64-encoded content.
SYNC_BACK_SCRIPT = (
    "find / -type f 2>/dev/null -exec sh -c "
    "'for f; do printf \"===FILE:%s===\\n\" \"$f\"; base64 \"$f\"; done' _ {} +"
)


def build_sync_preamble(fs_driver: DirtyTrackingFS) -> list[str]:
    """Build shell commands to sync dirty VFS files to a remote shell.

    Clears the dirty set after building.

    Returns:
        List of shell commands (each writes or removes one file).
    """
    commands: list[str] = []
    for path in list(fs_driver.dirty):
        if fs_driver.exists(path) and not fs_driver.is_dir(path):
            content = fs_driver.read_text(path)
            encoded = base64.b64encode(content.encode()).decode()
            commands.append(
                f"mkdir -p $(dirname '{path}') && printf '%s' '{encoded}' | base64 -d > '{path}'"
            )
        elif not fs_driver.exists(path):
            commands.append(f"rm -f '{path}'")
    fs_driver.clear_dirty()
    return commands


def build_sync_epilogue(marker: str, root: str = "/") -> str:
    """Build an epilogue string that captures file state after a command.

    The epilogue preserves the command's exit code, prints a marker, then
    lists all files with base64-encoded content.

    Args:
        marker: Unique marker string to delimit sync output.
        root: Root directory to scan for files. Defaults to "/" for virtual
              filesystems. Use a workspace path (e.g. "/home/sandbox/workspace")
              for real containers to avoid scanning system files.
    """
    return (
        f"; __exit=$?; printf '\\n{marker}\\n'; "
        f"find {root} -type f 2>/dev/null -exec sh -c "
        "'for f; do printf \"===FILE:%s===\\n\" \"$f\"; base64 \"$f\"; done' _ {} +; "
        "exit $__exit"
    )


def parse_file_listing(stdout: str) -> dict[str, str]:
    """Parse ===FILE:path=== delimited base64 output into a path->content dict.

    Used by drivers that run the sync-back script as a separate command.
    """
    marker = "===FILE:"
    end_marker = "==="
    files: dict[str, str] = {}
    current_path: str | None = None
    content_lines: list[str] = []

    for line in stdout.split("\n"):
        if line.startswith(marker) and line.endswith(end_marker) and len(line) > len(marker) + len(end_marker):
            if current_path is not None:
                encoded = "".join(content_lines)
                try:
                    files[current_path] = base64.b64decode(encoded).decode()
                except Exception:
                    files[current_path] = encoded
            current_path = line[len(marker):-len(end_marker)]
            content_lines = []
        elif current_path is not None:
            content_lines.append(line)

    if current_path is not None:
        encoded = "".join(content_lines)
        try:
            files[current_path] = base64.b64decode(encoded).decode()
        except Exception:
            files[current_path] = encoded

    return files


def parse_sync_output(raw: str, marker: str) -> tuple[str, dict[str, str] | None]:
    """Split marker-delimited output into (user_stdout, files_dict).

    Used by CLI-based drivers that concatenate preamble + command + epilogue
    into a single shell invocation.

    Returns:
        Tuple of (user stdout, parsed files dict or None if marker not found).
    """
    marker_idx = raw.find(f"\n{marker}\n")
    if marker_idx == -1:
        return raw, None
    stdout = raw[:marker_idx]
    sync_data = raw[marker_idx + len(marker) + 2:]
    files = parse_file_listing(sync_data)
    return stdout, files


def apply_sync_back(fs_driver: DirtyTrackingFS, files: dict[str, str]) -> None:
    """Diff and apply remote file state to local VFS.

    - New files in remote are added to VFS
    - Modified files are updated
    - Files in VFS but not in remote are removed
    """
    vfs_files: set[str] = set()
    for path in fs_driver.find("/", "*"):
        if not fs_driver.is_dir(path):
            vfs_files.add(path)

    for path, new_content in files.items():
        if path not in vfs_files:
            fs_driver.inner.write(path, new_content)
        else:
            existing = fs_driver.read_text(path)
            if existing != new_content:
                fs_driver.inner.write(path, new_content)

    for path in vfs_files - set(files.keys()):
        if fs_driver.exists(path):
            fs_driver.inner.remove(path)
