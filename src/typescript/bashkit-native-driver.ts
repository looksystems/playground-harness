/**
 * BashkitNativeDriver: FFI-based ShellDriver using bashkit shared library.
 */

import {
  type ShellDriver,
  type FilesystemDriver,
  type ShellDriverOptions,
  BuiltinFilesystemDriver,
} from "./drivers.js";
import type { ExecResult, CmdHandler } from "./shell.js";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";

export interface BashkitNativeDriverOptions extends ShellDriverOptions {
  _libOverride?: any;
}

export class BashkitNativeDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: BuiltinFilesystemDriver;
  private _commands: Map<string, CmdHandler> = new Map();
  private _onNotFound?: (cmdName: string) => void;
  private _lib: any;
  private _ctx: any;

  constructor(opts: BashkitNativeDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new BuiltinFilesystemDriver();
    this._lib = opts._libOverride ?? this._loadLibrary();

    const config = JSON.stringify({ cwd: this._cwd, env: this._env });
    this._ctx = this._lib.bashkit_create(config);
  }

  private _loadLibrary(): any {
    const libPath = BashkitNativeDriver.findLibrary();
    if (!libPath) {
      throw new Error(
        "bashkit shared library not found. Set BASHKIT_LIB_PATH or " +
          "install libashkit to a standard library path. " +
          "Use _libOverride for testing."
      );
    }
    // Actual ffi-napi loading is deferred to a future phase.
    throw new Error(
      "Native FFI loading not yet implemented. Use _libOverride for testing."
    );
  }

  static findLibrary(): string | undefined {
    // 1. Explicit env var
    const envPath = process.env.BASHKIT_LIB_PATH;
    if (envPath !== undefined) {
      try {
        if (fs.statSync(envPath).isFile()) return envPath;
      } catch {
        // File doesn't exist
      }
      return undefined;
    }

    // 2. Platform-specific library name and search path variable
    let libName: string;
    let pathVar: string;
    const platform = process.platform;

    if (platform === "darwin") {
      libName = "libashkit.dylib";
      pathVar = "DYLD_LIBRARY_PATH";
    } else if (platform === "win32") {
      libName = "bashkit.dll";
      pathVar = "PATH";
    } else {
      libName = "libashkit.so";
      pathVar = "LD_LIBRARY_PATH";
    }

    // 3. Search dirs from env var + standard paths
    const searchDirs: string[] = [];
    const envDirs = process.env[pathVar] ?? "";
    if (envDirs) {
      searchDirs.push(...envDirs.split(path.delimiter));
    }
    searchDirs.push("/usr/local/lib", "/usr/lib");

    for (const dir of searchDirs) {
      const candidate = path.join(dir, libName);
      try {
        if (fs.statSync(candidate).isFile()) return candidate;
      } catch {
        // Not found, continue
      }
    }

    return undefined;
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

  private _snapshotFs(): Record<string, string> {
    const snapshot: Record<string, string> = {};
    for (const filePath of this._fsDriver.find("/", "*")) {
      if (!this._fsDriver.isDir(filePath)) {
        snapshot[filePath] = this._fsDriver.readText(filePath);
      }
    }
    return snapshot;
  }

  private _applyFsChanges(changes: Record<string, unknown>): void {
    const created = (changes.created ?? {}) as Record<string, string>;
    const deleted = (changes.deleted ?? []) as string[];
    for (const [filePath, content] of Object.entries(created)) {
      this._fsDriver.write(filePath, content);
    }
    for (const filePath of deleted) {
      if (this._fsDriver.exists(filePath)) {
        this._fsDriver.remove(filePath);
      }
    }
  }

  private _wrapHandler(handler: CmdHandler): (argsJson: string) => string {
    return (argsJson: string): string => {
      try {
        const request = JSON.parse(argsJson);
        const args: string[] = request.args ?? [];
        const stdin: string = request.stdin ?? "";
        const result = handler(args, stdin);
        return result.stdout;
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        return JSON.stringify({ error: msg });
      }
    };
  }

  exec(command: string): ExecResult {
    const snapshot = this._snapshotFs();
    const requestJson = JSON.stringify({
      cmd: command,
      cwd: this._cwd,
      env: this._env,
      fs: snapshot,
    });

    const responseJson = this._lib.bashkit_exec(this._ctx, requestJson);
    const response = JSON.parse(responseJson);

    if (response.fs_changes) {
      this._applyFsChanges(response.fs_changes);
    }

    return {
      stdout: response.stdout ?? "",
      stderr: response.stderr ?? "",
      exitCode: response.exitCode ?? 0,
    };
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
    const wrappedCb = this._wrapHandler(handler);
    this._lib.bashkit_register_command(this._ctx, name, wrappedCb, null);
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
    this._lib.bashkit_unregister_command(this._ctx, name);
  }

  clone(): BashkitNativeDriver {
    const newDriver = new BashkitNativeDriver({
      cwd: this._cwd,
      env: { ...this._env },
      _libOverride: this._lib,
    });

    // Clone the filesystem
    newDriver._fsDriver = this._fsDriver.clone();

    // Copy registered commands and re-register with new context
    newDriver._commands = new Map(this._commands);
    newDriver._onNotFound = this._onNotFound;

    for (const [name, handler] of this._commands.entries()) {
      const wrappedCb = newDriver._wrapHandler(handler);
      newDriver._lib.bashkit_register_command(newDriver._ctx, name, wrappedCb, null);
    }

    return newDriver;
  }

  destroy(): void {
    if (this._lib && this._ctx !== undefined) {
      try {
        this._lib.bashkit_destroy(this._ctx);
      } catch {
        // Ignore errors during cleanup
      }
      this._ctx = undefined;
    }
  }
}
