/**
 * Shared remote sync utilities for drivers that execute commands in external processes.
 *
 * Provides:
 * - DirtyTrackingFS: filesystem wrapper that tracks written/removed paths
 * - Preamble/epilogue builders for syncing VFS state to/from a remote shell
 * - Parsing and applying file listings from remote execution
 */

import {
  type FilesystemDriver,
  BuiltinFilesystemDriver,
} from "./drivers.js";

export class DirtyTrackingFS implements FilesystemDriver {
  private _inner: BuiltinFilesystemDriver;
  private _dirty: Set<string> = new Set();

  constructor(inner: BuiltinFilesystemDriver) {
    this._inner = inner;
  }

  get inner(): BuiltinFilesystemDriver { return this._inner; }
  get dirty(): Set<string> { return this._dirty; }
  clearDirty(): void { this._dirty.clear(); }

  write(path: string, content: string): void {
    this._inner.write(path, content);
    this._dirty.add(path);
  }
  writeLazy(path: string, provider: () => string): void {
    this._inner.writeLazy(path, provider);
    this._dirty.add(path);
  }
  read(path: string): string { return this._inner.read(path); }
  readText(path: string): string { return this._inner.readText(path); }
  exists(path: string): boolean { return this._inner.exists(path); }
  remove(path: string): void {
    this._inner.remove(path);
    this._dirty.add(path);
  }
  isDir(path: string): boolean { return this._inner.isDir(path); }
  listdir(path?: string): string[] { return this._inner.listdir(path); }
  find(root?: string, pattern?: string): string[] { return this._inner.find(root, pattern); }
  stat(path: string): { path: string; type: string; size?: number } { return this._inner.stat(path); }
  clone(): DirtyTrackingFS {
    return new DirtyTrackingFS(this._inner.clone());
  }
}

/**
 * Build shell commands to sync dirty VFS files to a remote shell.
 * Clears the dirty set after building.
 */
export function buildSyncPreamble(fsDriver: DirtyTrackingFS): string {
  const commands: string[] = [];
  for (const path of fsDriver.dirty) {
    if (fsDriver.exists(path) && !fsDriver.isDir(path)) {
      const content = fsDriver.readText(path);
      const encoded = Buffer.from(content).toString("base64");
      commands.push(`mkdir -p $(dirname '${path}') && printf '%s' '${encoded}' | base64 -d > '${path}'`);
    } else if (!fsDriver.exists(path)) {
      commands.push(`rm -f '${path}'`);
    }
  }
  fsDriver.clearDirty();
  return commands.length > 0 ? commands.join(" && ") : "";
}

/**
 * Build an epilogue string that captures file state after a command.
 * Preserves the command's exit code.
 *
 * @param marker - Unique marker string to delimit sync output.
 * @param root - Root directory to scan for files. Defaults to "/" for virtual
 *               filesystems. Use a workspace path for real containers.
 */
export function buildSyncEpilogue(marker: string, root: string = "/"): string {
  return `; __exit=$?; printf '\\n${marker}\\n'; find ${root} -type f 2>/dev/null -exec sh -c 'for f; do printf "===FILE:%s===\\n" "$f"; base64 "$f"; done' _ {} +; exit $__exit`;
}

/**
 * Parse ===FILE:path=== delimited base64 output into a path->content map.
 */
export function parseFileListing(syncData: string): Map<string, string> {
  const files = new Map<string, string>();
  const fileMarker = "===FILE:";
  const endMarker = "===";
  let currentPath: string | null = null;
  const contentLines: string[] = [];

  for (const line of syncData.split("\n")) {
    if (line.startsWith(fileMarker) && line.endsWith(endMarker) && line.length > fileMarker.length + endMarker.length) {
      if (currentPath !== null) {
        try {
          files.set(currentPath, Buffer.from(contentLines.join(""), "base64").toString("utf-8"));
        } catch {
          files.set(currentPath, contentLines.join(""));
        }
      }
      currentPath = line.substring(fileMarker.length, line.length - endMarker.length);
      contentLines.length = 0;
    } else if (currentPath !== null) {
      contentLines.push(line);
    }
  }
  if (currentPath !== null) {
    try {
      files.set(currentPath, Buffer.from(contentLines.join(""), "base64").toString("utf-8"));
    } catch {
      files.set(currentPath, contentLines.join(""));
    }
  }

  return files;
}

/**
 * Split marker-delimited output into user stdout and file listing.
 */
export function parseSyncOutput(raw: string, marker: string): { stdout: string; files: Map<string, string> | null } {
  const markerIdx = raw.indexOf(`\n${marker}\n`);
  if (markerIdx === -1) {
    return { stdout: raw, files: null };
  }
  const stdout = raw.substring(0, markerIdx);
  const syncData = raw.substring(markerIdx + marker.length + 2);
  const files = parseFileListing(syncData);
  return { stdout, files };
}

/**
 * Diff and apply remote file state to local VFS.
 */
export function applySyncBack(fsDriver: DirtyTrackingFS, files: Map<string, string>): void {
  const vfsFiles = new Set<string>();
  for (const path of fsDriver.find("/", "*")) {
    if (!fsDriver.isDir(path)) {
      vfsFiles.add(path);
    }
  }

  for (const [path, content] of files) {
    if (!vfsFiles.has(path)) {
      fsDriver.inner.write(path, content);
    } else {
      const existing = fsDriver.readText(path);
      if (existing !== content) {
        fsDriver.inner.write(path, content);
      }
    }
  }

  for (const path of vfsFiles) {
    if (!files.has(path) && fsDriver.exists(path)) {
      fsDriver.inner.remove(path);
    }
  }
}
