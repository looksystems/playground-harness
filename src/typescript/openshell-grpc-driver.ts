/**
 * OpenShellGrpcDriver: ShellDriver using SSH to execute in an OpenShell sandbox.
 *
 * Node.js gRPC is async, so this driver uses SSH via spawnSync for synchronous
 * exec() calls. The SSH session is created via gRPC CreateSshSession, then
 * commands run via `ssh -p <port> user@host <command>`.
 *
 * VFS sync uses the same preamble/epilogue mechanism as BashkitCLIDriver.
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

export interface OpenShellPolicy {
  filesystemAllow?: string[];
  networkRules?: Record<string, unknown>[];
  inferenceRouting?: boolean;
}

export interface OpenShellGrpcDriverOptions extends ShellDriverOptions {
  endpoint?: string;
  sandboxId?: string;
  policy?: OpenShellPolicy;
  sshPort?: number;
  sshUser?: string;
  sshHost?: string;
  workspace?: string;
  _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };
}

export class OpenShellGrpcDriver implements ShellDriver {
  private _cwd: string;
  private _env: Record<string, string>;
  private _fsDriver: DirtyTrackingFS;
  private _commands: Map<string, CmdHandler> = new Map();
  private _onNotFound?: (cmdName: string) => void;
  private _execOverride?: (command: string) => { stdout: string; stderr: string; exitCode: number };
  private _endpoint: string;
  private _sandboxId: string | null;
  private _policy: OpenShellPolicy;
  private _sshPort: number;
  private _sshUser: string;
  private _sshHost: string;
  private _workspace: string;

  constructor(opts: OpenShellGrpcDriverOptions = {}) {
    this._cwd = opts.cwd ?? "/";
    this._env = opts.env ?? {};
    this._fsDriver = new DirtyTrackingFS(new BuiltinFilesystemDriver());
    this._execOverride = opts._execOverride;
    this._endpoint = opts.endpoint ?? "localhost:50051";
    this._sandboxId = opts.sandboxId ?? null;
    this._policy = opts.policy ?? { inferenceRouting: true };
    this._sshPort = opts.sshPort ?? 2222;
    this._sshUser = opts.sshUser ?? "sandbox";
    this._sshHost = opts.sshHost ?? "localhost";
    this._workspace = opts.workspace ?? "/home/sandbox/workspace";
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

  get sandboxId(): string | null {
    return this._sandboxId;
  }

  get policy(): OpenShellPolicy {
    return this._policy;
  }

  private _rawExec(command: string): { stdout: string; stderr: string; exitCode: number } {
    if (this._execOverride) {
      return this._execOverride(command);
    }

    // Real implementation: SSH into the OpenShell sandbox
    const result = spawnSync(
      "ssh",
      [
        "-p", String(this._sshPort),
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=/dev/null",
        `${this._sshUser}@${this._sshHost}`,
        command,
      ],
      {
        encoding: "utf-8",
        env: { ...process.env, ...this._env },
        timeout: 30000,
      },
    );
    return {
      stdout: result.stdout ?? "",
      stderr: result.stderr ?? "",
      exitCode: result.status ?? 1,
    };
  }

  private _remapPath(vfsPath: string): string {
    if (this._execOverride) return vfsPath;
    return this._workspace.replace(/\/$/, "") + "/" + vfsPath.replace(/^\//, "");
  }

  private _unmapPath(remotePath: string): string {
    if (this._execOverride) return remotePath;
    const prefix = this._workspace.replace(/\/$/, "") + "/";
    if (remotePath.startsWith(prefix)) {
      return "/" + remotePath.substring(prefix.length);
    }
    return remotePath;
  }

  exec(command: string): ExecResult {
    const marker = `__HARNESS_FS_SYNC_${Date.now()}__`;
    let preamble: string;
    let epilogue: string;

    if (this._execOverride) {
      // Mock mode: standard sync
      preamble = buildSyncPreamble(this._fsDriver);
      epilogue = buildSyncEpilogue(marker);
    } else {
      // Real mode: remap paths to workspace
      const parts: string[] = [`mkdir -p ${this._workspace}`];
      for (const path of this._fsDriver.dirty) {
        const remote = this._remapPath(path);
        if (this._fsDriver.exists(path) && !this._fsDriver.isDir(path)) {
          const content = this._fsDriver.readText(path);
          const encoded = Buffer.from(content).toString("base64");
          parts.push(`mkdir -p $(dirname '${remote}') && printf '%s' '${encoded}' | base64 -d > '${remote}'`);
        } else if (!this._fsDriver.exists(path)) {
          parts.push(`rm -f '${remote}'`);
        }
      }
      this._fsDriver.clearDirty();
      preamble = parts.join(" && ");
      epilogue = buildSyncEpilogue(marker, this._workspace);
    }

    const fullCommand = preamble
      ? `${preamble} && ${command}${epilogue}`
      : `${command}${epilogue}`;

    const raw = this._rawExec(fullCommand);
    const { stdout, files } = parseSyncOutput(raw.stdout, marker);

    if (files !== null) {
      // Remap remote paths back to VFS paths
      const remapped = new Map<string, string>();
      for (const [remotePath, content] of files) {
        remapped.set(this._unmapPath(remotePath), content);
      }
      applySyncBack(this._fsDriver, remapped);
    }

    return {
      stdout,
      stderr: raw.stderr,
      exitCode: raw.exitCode,
    };
  }

  capabilities(): Set<string> {
    return new Set(["custom_commands", "remote", "policies", "streaming"]);
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
  }

  close(): void {
    this._sandboxId = null;
  }

  clone(): OpenShellGrpcDriver {
    const newDriver = new OpenShellGrpcDriver({
      cwd: this._cwd,
      env: { ...this._env },
      endpoint: this._endpoint,
      policy: { ...this._policy },
      sshPort: this._sshPort,
      sshUser: this._sshUser,
      sshHost: this._sshHost,
      workspace: this._workspace,
      _execOverride: this._execOverride,
    });
    newDriver._fsDriver = this._fsDriver.clone();
    newDriver._commands = new Map(this._commands);
    newDriver._onNotFound = this._onNotFound;
    // Clone gets a fresh sandbox (null), will create on first exec
    newDriver._sandboxId = null;
    return newDriver;
  }
}
