/**
 * BashkitCLIDriver: subprocess-based ShellDriver using `bashkit -c 'command'`.
 *
 * Each exec() call spawns a new bashkit process (stateless).
 * Custom commands (registerCommand) are stored locally but not available
 * inside the bashkit subprocess.
 */

import { execSync } from "child_process";
import {
  type ShellDriver,
  type FilesystemDriver,
  type ShellDriverOptions,
  BuiltinFilesystemDriver,
} from "./drivers.js";
import type { ExecResult, CmdHandler } from "./shell.js";

export interface BashkitCLIDriverOptions extends ShellDriverOptions {
  /** Override for testing -- function that takes command string and returns ExecResult-like object. */
  _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };
}

export class BashkitCLIDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: BuiltinFilesystemDriver;
  private _commands: Map<string, CmdHandler> = new Map();
  private _onNotFound?: (cmdName: string) => void;
  private _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };

  constructor(opts: BashkitCLIDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new BuiltinFilesystemDriver();
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

  exec(command: string): ExecResult {
    if (this._execOverride) {
      return this._execOverride(command);
    }

    try {
      const result = execSync(`bashkit -c ${JSON.stringify(command)}`, {
        encoding: "utf-8",
        env: { ...process.env, ...this._env },
        cwd: undefined,
        timeout: 30000,
      });
      return { stdout: result, stderr: "", exitCode: 0 };
    } catch (e: any) {
      return {
        stdout: e.stdout ?? "",
        stderr: e.stderr ?? "",
        exitCode: e.status ?? 1,
      };
    }
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
