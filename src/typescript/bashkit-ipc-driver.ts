/**
 * BashkitIPCDriver: JSON-RPC over stdin/stdout with bashkit-cli.
 */

import {
  type ShellDriver,
  type FilesystemDriver,
  type ShellDriverOptions,
  BuiltinFilesystemDriver,
} from "./drivers.js";
import type { ExecResult, CmdHandler } from "./shell.js";

export interface BashkitIPCDriverOptions extends ShellDriverOptions {
  _spawnOverride?: () => any;
}

interface JsonRpcMessage {
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: Record<string, unknown>;
  error?: { code: number; message: string } | string;
}

export class BashkitIPCDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: BuiltinFilesystemDriver;
  private _commands: Map<string, CmdHandler> = new Map();
  private _onNotFound?: (cmdName: string) => void;
  private _requestId = 0;
  private _process: any;

  constructor(opts: BashkitIPCDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new BuiltinFilesystemDriver();
    this._process = opts._spawnOverride
      ? opts._spawnOverride()
      : this._spawn();
  }

  private _spawn(): any {
    // For real usage, spawn a persistent bashkit-cli process.
    // Currently a placeholder — real IPC transport is a future concern.
    throw new Error(
      "bashkit-cli not available. Use _spawnOverride for testing."
    );
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

  private _nextId(): number {
    this._requestId += 1;
    return this._requestId;
  }

  private _send(msg: Record<string, unknown>): void {
    this._process.stdin.write(JSON.stringify(msg) + "\n");
  }

  private _recv(): JsonRpcMessage {
    const line: string = this._process.stdout.readline();
    if (!line) return {};
    return JSON.parse(line.trim());
  }

  private _snapshotFs(): Record<string, string> {
    const snapshot: Record<string, string> = {};
    for (const path of this._fsDriver.find("/", "*")) {
      if (!this._fsDriver.isDir(path)) {
        snapshot[path] = this._fsDriver.readText(path);
      }
    }
    return snapshot;
  }

  private _applyFsChanges(changes: Record<string, unknown>): void {
    const created = (changes.created ?? {}) as Record<string, string>;
    const deleted = (changes.deleted ?? []) as string[];
    for (const [path, content] of Object.entries(created)) {
      this._fsDriver.write(path, content);
    }
    for (const path of deleted) {
      if (this._fsDriver.exists(path)) {
        this._fsDriver.remove(path);
      }
    }
  }

  exec(command: string): ExecResult {
    const reqId = this._nextId();
    const snapshot = this._snapshotFs();

    this._send({
      id: reqId,
      method: "exec",
      params: {
        cmd: command,
        cwd: this._cwd,
        env: this._env,
        fs: snapshot,
      },
    });

    // Event loop: handle callbacks until we get the final result
    while (true) {
      const response = this._recv();
      if (!response || Object.keys(response).length === 0) {
        return { stdout: "", stderr: "No response from bashkit-cli", exitCode: 1 };
      }

      // Error response
      if (response.error !== undefined) {
        const error = response.error;
        const msg =
          typeof error === "object" && error !== null
            ? (error as any).message ?? "Unknown error"
            : String(error);
        return { stdout: "", stderr: msg, exitCode: 1 };
      }

      // Callback: invoke_command from bashkit
      if (response.method === "invoke_command") {
        const cbId = response.id;
        const params = response.params ?? {};
        const name = (params.name as string) ?? "";
        const args = (params.args as string[]) ?? [];
        const stdin = (params.stdin as string) ?? "";

        if (this._commands.has(name)) {
          try {
            const result = this._commands.get(name)!(args, stdin);
            this._send({ id: cbId, result });
          } catch (e: unknown) {
            const errMsg = e instanceof Error ? e.message : String(e);
            this._send({
              id: cbId,
              error: { code: -1, message: errMsg },
            });
          }
        } else {
          this._send({
            id: cbId,
            error: { code: -1, message: `Unknown command: ${name}` },
          });
        }
        continue;
      }

      // Final result
      if (response.result !== undefined) {
        const r = response.result as Record<string, unknown>;
        if (r.fs_changes) {
          this._applyFsChanges(r.fs_changes as Record<string, unknown>);
        }
        return {
          stdout: (r.stdout as string) ?? "",
          stderr: (r.stderr as string) ?? "",
          exitCode: (r.exitCode as number) ?? 0,
        };
      }
    }
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
    this._send({
      method: "register_command",
      params: { name },
    });
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
    this._send({
      method: "unregister_command",
      params: { name },
    });
  }

  clone(opts?: { _spawnOverride?: () => any }): BashkitIPCDriver {
    const spawnOverride = opts?._spawnOverride;
    const newDriver = new BashkitIPCDriver({
      cwd: this._cwd,
      env: { ...this._env },
      _spawnOverride: spawnOverride,
    });

    // Clone the filesystem
    newDriver._fsDriver = this._fsDriver.clone();

    // Copy registered commands and re-register with new process
    newDriver._commands = new Map(this._commands);
    newDriver._onNotFound = this._onNotFound;

    for (const name of this._commands.keys()) {
      newDriver._send({
        method: "register_command",
        params: { name },
      });
    }

    return newDriver;
  }

  destroy(): void {
    if (this._process) {
      try {
        this._process.kill();
      } catch {
        // Ignore errors during cleanup
      }
    }
  }
}
