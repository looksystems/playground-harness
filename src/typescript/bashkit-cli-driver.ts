/**
 * BashkitCLIDriver: subprocess-based ShellDriver using `bashkit -c 'command'`.
 *
 * Each exec() call spawns a new bashkit process (stateless).
 * VFS sync is achieved via preamble/epilogue: dirty files are injected
 * as base64-encoded commands before exec, and file state is read back after.
 * Shell state (variables, functions) does NOT persist between exec() calls.
 */

import { spawnSync } from "child_process";
import {
  type ShellDriver,
  type FilesystemDriver,
  type ShellDriverOptions,
  BuiltinFilesystemDriver,
} from "./drivers.js";
import type { ExecResult, CmdHandler } from "./shell.js";

class DirtyTrackingFS implements FilesystemDriver {
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
    const cloned = new DirtyTrackingFS(this._inner.clone());
    return cloned;
  }
}

export interface BashkitCLIDriverOptions extends ShellDriverOptions {
  _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };
}

export class BashkitCLIDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: DirtyTrackingFS;
  private _commands: Map<string, CmdHandler> = new Map();
  private _onNotFound?: (cmdName: string) => void;
  private _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };

  constructor(opts: BashkitCLIDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new DirtyTrackingFS(new BuiltinFilesystemDriver());
    this._execOverride = opts._execOverride;
  }

  get fs(): FilesystemDriver {
    return this._fsDriver;
  }

  get cwd(): string {
    return this._cwd;
  }

  get env(): Record<string, string> {
    return this._env;
  }

  get onNotFound(): ((cmdName: string) => void) | undefined {
    return this._onNotFound;
  }

  set onNotFound(cb: ((cmdName: string) => void) | undefined) {
    this._onNotFound = cb;
  }

  private _buildSyncPreamble(): string {
    const commands: string[] = [];
    for (const path of this._fsDriver.dirty) {
      if (this._fsDriver.exists(path) && !this._fsDriver.isDir(path)) {
        const content = this._fsDriver.readText(path);
        const encoded = Buffer.from(content).toString("base64");
        commands.push(`mkdir -p $(dirname '${path}') && printf '%s' '${encoded}' | base64 -d > '${path}'`);
      } else if (!this._fsDriver.exists(path)) {
        commands.push(`rm -f '${path}'`);
      }
    }
    this._fsDriver.clearDirty();
    return commands.length > 0 ? commands.join(" && ") : "";
  }

  private _buildSyncEpilogue(marker: string): string {
    return `; __exit=$?; printf '\\n${marker}\\n'; find / -type f 2>/dev/null -exec sh -c 'for f; do printf "===FILE:%s===\\n" "$f"; base64 "$f"; done' _ {} +; exit $__exit`;
  }

  private _parseSyncOutput(raw: string, marker: string): { stdout: string; files: Map<string, string> | null } {
    const markerIdx = raw.indexOf(`\n${marker}\n`);
    if (markerIdx === -1) {
      return { stdout: raw, files: null };
    }
    const files = new Map<string, string>();
    const stdout = raw.substring(0, markerIdx);
    const syncData = raw.substring(markerIdx + marker.length + 2);

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

    return { stdout, files };
  }

  private _applySyncBack(files: Map<string, string>): void {
    const vfsFiles = new Set<string>();
    for (const path of this._fsDriver.find("/", "*")) {
      if (!this._fsDriver.isDir(path)) {
        vfsFiles.add(path);
      }
    }

    for (const [path, content] of files) {
      if (!vfsFiles.has(path)) {
        this._fsDriver.inner.write(path, content);
      } else {
        const existing = this._fsDriver.readText(path);
        if (existing !== content) {
          this._fsDriver.inner.write(path, content);
        }
      }
    }

    for (const path of vfsFiles) {
      if (!files.has(path) && this._fsDriver.exists(path)) {
        this._fsDriver.inner.remove(path);
      }
    }
  }

  private _rawExec(command: string): { stdout: string; stderr: string; exitCode: number } {
    if (this._execOverride) {
      return this._execOverride(command);
    }

    const result = spawnSync("bashkit", ["-c", command], {
      encoding: "utf-8",
      env: { ...process.env, ...this._env },
      timeout: 30000,
    });
    return {
      stdout: result.stdout ?? "",
      stderr: result.stderr ?? "",
      exitCode: result.status ?? 1,
    };
  }

  exec(command: string): ExecResult {
    const preamble = this._buildSyncPreamble();
    const marker = `__HARNESS_FS_SYNC_${Date.now()}__`;
    const epilogue = this._buildSyncEpilogue(marker);

    const fullCommand = preamble
      ? `${preamble} && ${command}${epilogue}`
      : `${command}${epilogue}`;

    const raw = this._rawExec(fullCommand);
    const { stdout, files } = this._parseSyncOutput(raw.stdout, marker);

    if (files !== null) {
      this._applySyncBack(files);
    }

    return {
      stdout,
      stderr: raw.stderr,
      exitCode: raw.exitCode,
    };
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
  }

  clone(): BashkitCLIDriver {
    const newDriver = new BashkitCLIDriver({
      cwd: this._cwd,
      env: { ...this._env },
      _execOverride: this._execOverride,
    });
    newDriver._fsDriver = this._fsDriver.clone();
    newDriver._commands = new Map(this._commands);
    newDriver._onNotFound = this._onNotFound;
    return newDriver;
  }
}
