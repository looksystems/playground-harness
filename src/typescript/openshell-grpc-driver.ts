/**
 * OpenShellGrpcDriver: ShellDriver using SSH or gRPC to execute in an OpenShell sandbox.
 *
 * Supports two transport modes:
 * - "ssh" (default): Commands run via spawnSync SSH subprocess.
 * - "grpc": Commands run via native gRPC using @grpc/grpc-js and @grpc/proto-loader,
 *   with a Worker thread sync bridge for blocking exec() calls.
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

export type ExecStreamEvent =
  | { type: "stdout"; data: string }
  | { type: "stderr"; data: string }
  | { type: "exit"; exitCode: number };

export interface OpenShellGrpcDriverOptions extends ShellDriverOptions {
  endpoint?: string;
  sandboxId?: string;
  policy?: OpenShellPolicy;
  sshPort?: number;
  sshUser?: string;
  sshHost?: string;
  workspace?: string;
  transport?: "ssh" | "grpc";
  grpcClient?: unknown;
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
  private _transport: "ssh" | "grpc";
  private _grpcClient: unknown;

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
    this._transport = opts.transport ?? "ssh";
    this._grpcClient = opts.grpcClient ?? null;
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

  private _getGrpcClient(): unknown {
    if (this._grpcClient !== null) return this._grpcClient;
    // Dynamic import of @grpc/grpc-js and @grpc/proto-loader
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const grpc = require("@grpc/grpc-js");
    const protoLoader = require("@grpc/proto-loader");
    const path = require("path");
    const protoPath = path.resolve(__dirname, "../../proto/openshell/openshell.proto");
    const packageDef = protoLoader.loadSync(protoPath, {
      keepCase: true,
      longs: String,
      enums: String,
      defaults: true,
      oneofs: true,
      includeDirs: [path.resolve(__dirname, "../../proto")],
    });
    const proto = grpc.loadPackageDefinition(packageDef) as any;
    this._grpcClient = new proto.openshell.OpenShell(
      this._endpoint,
      grpc.credentials.createInsecure(),
    );
    return this._grpcClient;
  }

  private _ensureSandbox(): void {
    if (this._sandboxId !== null) return;

    if (this._execOverride) {
      const override = this._execOverride as unknown as {
        createSandbox?: (policy: OpenShellPolicy) => { sandboxId: string };
      };
      if (override.createSandbox) {
        const result = override.createSandbox(this._policy);
        this._sandboxId = result.sandboxId;
        return;
      }
    }

    if (this._transport === "grpc") {
      this._ensureSandboxGrpc();
      return;
    }

    this._sandboxId = `${this._sshUser}@${this._sshHost}:${this._sshPort}`;
  }

  private _ensureSandboxGrpc(): void {
    const client = this._getGrpcClient() as any;
    const spec = this._buildSandboxSpec();
    const name = `harness-${Date.now().toString(36)}`;
    // Use sync bridge worker for blocking call
    const { execSync } = require("child_process");
    const request = JSON.stringify({ name, spec });
    // For CreateSandbox, use the grpc-sync-bridge
    const bridgePath = require("path").resolve(__dirname, "grpc-sync-bridge.js");
    const result = execSync(
      `node ${bridgePath} create-sandbox ${JSON.stringify(this._endpoint)} ${Buffer.from(request).toString("base64")}`,
      { encoding: "utf-8", timeout: 30000 },
    );
    const response = JSON.parse(result.trim());
    this._sandboxId = response.sandbox?.sandbox_id ?? response.sandbox?.name ?? name;
  }

  private _buildSandboxSpec(): Record<string, unknown> {
    const fsPolicy: Record<string, unknown> = {};
    if (this._policy.filesystemAllow) {
      fsPolicy.read_write = [...this._policy.filesystemAllow];
    }
    const netPolicies = (this._policy.networkRules ?? []).map((rule) => {
      const allow = rule.allow as string | undefined;
      const deny = rule.deny as string | undefined;
      return {
        cidr: allow ?? deny ?? "",
        action: deny ? 1 : 0,
      };
    });
    return {
      policy: {
        filesystem: fsPolicy,
        network_policies: netPolicies,
        inference: {
          routing_enabled: this._policy.inferenceRouting ?? true,
        },
      },
      workspace: this._workspace,
    };
  }

  private _rawExec(command: string): { stdout: string; stderr: string; exitCode: number } {
    this._ensureSandbox();

    if (this._execOverride) {
      return this._execOverride(command);
    }

    if (this._transport === "grpc") {
      return this._rawExecGrpc(command);
    }
    return this._rawExecSsh(command);
  }

  private _rawExecSsh(command: string): { stdout: string; stderr: string; exitCode: number } {
    const result = spawnSync(
      "ssh",
      [
        "-p", String(this._sshPort),
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=/dev/null",
        "-o", "LogLevel=ERROR",
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

  private _rawExecGrpc(command: string): { stdout: string; stderr: string; exitCode: number } {
    const { execSync } = require("child_process");
    const path = require("path");
    const bridgePath = path.resolve(__dirname, "grpc-sync-bridge.js");
    const request = JSON.stringify({
      sandbox_id: this._sandboxId,
      command: ["bash", "-c", command],
      env: this._env,
    });
    const result = execSync(
      `node ${bridgePath} exec-sandbox ${JSON.stringify(this._endpoint)} ${Buffer.from(request).toString("base64")}`,
      { encoding: "utf-8", timeout: 30000 },
    );
    return JSON.parse(result.trim());
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
    const caps = new Set(["custom_commands", "remote", "policies"]);
    if (this._transport === "grpc") {
      caps.add("streaming");
    }
    return caps;
  }

  async *execStream(command: string): AsyncGenerator<ExecStreamEvent> {
    if (this._transport !== "grpc" && !this._execOverride) {
      throw new Error("execStream requires transport='grpc'");
    }

    const customResult = this._tryCustomCommand(command);
    if (customResult !== null) {
      if (customResult.stdout) yield { type: "stdout", data: customResult.stdout };
      if (customResult.stderr) yield { type: "stderr", data: customResult.stderr };
      yield { type: "exit", exitCode: customResult.exitCode };
      return;
    }

    const marker = `__HARNESS_FS_SYNC_${Date.now()}__`;
    let preamble: string;
    let epilogue: string;

    if (this._execOverride) {
      preamble = buildSyncPreamble(this._fsDriver);
      epilogue = buildSyncEpilogue(marker);
    } else {
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

    this._ensureSandbox();

    if (this._execOverride) {
      const raw = this._execOverride(fullCommand);
      const { stdout, files } = parseSyncOutput(raw.stdout, marker);
      if (stdout) yield { type: "stdout", data: stdout };
      if (raw.stderr) yield { type: "stderr", data: raw.stderr };
      yield { type: "exit", exitCode: raw.exitCode };

      if (files !== null) {
        const remapped = new Map<string, string>();
        for (const [remotePath, content] of files) {
          remapped.set(this._unmapPath(remotePath), content);
        }
        applySyncBack(this._fsDriver, remapped);
      }
      return;
    }

    // Real gRPC streaming via async call
    const client = this._getGrpcClient() as any;
    const request = {
      sandbox_id: this._sandboxId,
      command: ["bash", "-c", fullCommand],
      env: this._env,
    };

    const stdoutAccum: string[] = [];
    const stream = client.ExecSandbox(request);

    for await (const event of stream) {
      if (event.stdout?.data) {
        const chunk = Buffer.from(event.stdout.data).toString("utf-8");
        stdoutAccum.push(chunk);
        yield { type: "stdout", data: chunk };
      } else if (event.stderr?.data) {
        const chunk = Buffer.from(event.stderr.data).toString("utf-8");
        yield { type: "stderr", data: chunk };
      } else if (event.exit) {
        yield { type: "exit", exitCode: event.exit.code ?? 0 };
      }
    }

    const rawStdout = stdoutAccum.join("");
    const { files } = parseSyncOutput(rawStdout, marker);
    if (files !== null) {
      const remapped = new Map<string, string>();
      for (const [remotePath, content] of files) {
        remapped.set(this._unmapPath(remotePath), content);
      }
      applySyncBack(this._fsDriver, remapped);
    }
  }

  registerCommand(name: string, handler: CmdHandler): void {
    this._commands.set(name, handler);
  }

  unregisterCommand(name: string): void {
    this._commands.delete(name);
  }

  close(): void {
    if (this._sandboxId !== null) {
      if (this._execOverride) {
        const override = this._execOverride as unknown as {
          deleteSandbox?: (sandboxId: string) => void;
        };
        if (override.deleteSandbox) {
          override.deleteSandbox(this._sandboxId);
        }
      } else if (this._transport === "grpc") {
        const { execSync } = require("child_process");
        const path = require("path");
        const bridgePath = path.resolve(__dirname, "grpc-sync-bridge.js");
        const request = JSON.stringify({ name: this._sandboxId });
        try {
          execSync(
            `node ${bridgePath} delete-sandbox ${JSON.stringify(this._endpoint)} ${Buffer.from(request).toString("base64")}`,
            { encoding: "utf-8", timeout: 10000 },
          );
        } catch {
          // Best-effort cleanup
        }
      }
      this._sandboxId = null;
    }
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
      transport: this._transport,
      grpcClient: this._grpcClient,
      _execOverride: this._execOverride,
    });
    newDriver._fsDriver = this._fsDriver.clone();
    newDriver._commands = new Map(this._commands);
    newDriver._onNotFound = this._onNotFound;
    newDriver._sandboxId = null;
    return newDriver;
  }
}
