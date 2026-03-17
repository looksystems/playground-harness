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
import {
  DirtyTrackingFS,
  buildSyncPreamble,
  buildSyncEpilogue,
  parseSyncOutput,
  applySyncBack,
} from "./remote-sync.js";

// Re-export DirtyTrackingFS for backwards compatibility
export { DirtyTrackingFS };

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

  private _tryCustomCommand(command: string): ExecResult | null {
    const parts = command.trim().split(/\s+/);
    if (parts.length === 0) return null;
    const handler = this._commands.get(parts[0]);
    if (!handler) return null;
    return handler(parts.slice(1), "");
  }

  exec(command: string): ExecResult {
    const customResult = this._tryCustomCommand(command);
    if (customResult !== null) return customResult;

    const preamble = buildSyncPreamble(this._fsDriver);
    const marker = `__HARNESS_FS_SYNC_${Date.now()}__`;
    const epilogue = buildSyncEpilogue(marker);

    const fullCommand = preamble
      ? `${preamble} && ${command}${epilogue}`
      : `${command}${epilogue}`;

    const raw = this._rawExec(fullCommand);
    const { stdout, files } = parseSyncOutput(raw.stdout, marker);

    if (files !== null) {
      applySyncBack(this._fsDriver, files);
    }

    return {
      stdout,
      stderr: raw.stderr,
      exitCode: raw.exitCode,
    };
  }

  capabilities(): Set<string> {
    return new Set(["custom_commands", "remote"]);
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
