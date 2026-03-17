import { VirtualFS } from "./virtual-fs.js";
import { Shell, type ExecResult, type CmdHandler, type ShellOptions } from "./shell.js";

export interface ShellSecurityPolicy {
  allowedCommands?: Set<string>;
  writablePaths?: string[];
  maxOutput?: number;
  maxIterations?: number;
  readOnly?: boolean;
}

export interface FilesystemDriver {
  write(path: string, content: string): void;
  writeLazy(path: string, provider: () => string): void;
  read(path: string): string;
  readText(path: string): string;
  exists(path: string): boolean;
  remove(path: string): void;
  isDir(path: string): boolean;
  listdir(path?: string): string[];
  find(root?: string, pattern?: string): string[];
  stat(path: string): { path: string; type: string; size?: number };
  clone(): FilesystemDriver;
}

export class BuiltinFilesystemDriver implements FilesystemDriver {
  private _vfs: VirtualFS;

  constructor(vfs?: VirtualFS) {
    this._vfs = vfs ?? new VirtualFS();
  }

  write(path: string, content: string): void { this._vfs.write(path, content); }
  writeLazy(path: string, provider: () => string): void { this._vfs.writeLazy(path, provider); }
  read(path: string): string { return this._vfs.read(path); }
  readText(path: string): string { return this._vfs.read(path); }
  exists(path: string): boolean { return this._vfs.exists(path); }
  remove(path: string): void { this._vfs.remove(path); }
  isDir(path: string): boolean { return this._vfs._isDir(path); }
  listdir(path: string = "/"): string[] { return this._vfs.listdir(path); }
  find(root: string = "/", pattern: string = "*"): string[] { return this._vfs.find(root, pattern); }
  stat(path: string): { path: string; type: string; size?: number } { return this._vfs.stat(path); }
  clone(): BuiltinFilesystemDriver { return new BuiltinFilesystemDriver(this._vfs.clone()); }

  get vfs(): VirtualFS { return this._vfs; }
}

export interface ShellDriver {
  readonly fs: FilesystemDriver;
  readonly cwd: string;
  readonly env: Record<string, string>;
  onNotFound?: (cmdName: string) => void;
  exec(command: string): ExecResult;
  registerCommand(name: string, handler: CmdHandler): void;
  unregisterCommand(name: string): void;
  clone(): ShellDriver;
  capabilities(): Set<string>;
}

export interface ShellDriverOptions {
  cwd?: string;
  env?: Record<string, string>;
  allowedCommands?: Set<string>;
  maxOutput?: number;
  maxIterations?: number;
}

export class BuiltinShellDriver implements ShellDriver {
  private _shell: Shell;
  private _fsDriver: BuiltinFilesystemDriver;
  private _opts: ShellDriverOptions;

  constructor(opts: ShellDriverOptions = {}) {
    this._opts = { ...opts };
    this._shell = new Shell({
      cwd: opts.cwd,
      env: opts.env,
      allowedCommands: opts.allowedCommands,
      maxOutput: opts.maxOutput,
      maxIterations: opts.maxIterations,
    });
    this._fsDriver = new BuiltinFilesystemDriver(this._shell.fs);
  }

  get fs(): FilesystemDriver { return this._fsDriver; }
  get cwd(): string { return this._shell.cwd; }
  get env(): Record<string, string> { return this._shell.env; }

  get onNotFound(): ((cmdName: string) => void) | undefined { return this._shell.onNotFound; }
  set onNotFound(cb: ((cmdName: string) => void) | undefined) { this._shell.onNotFound = cb; }

  exec(command: string): ExecResult { return this._shell.exec(command); }
  registerCommand(name: string, handler: CmdHandler): void { this._shell.registerCommand(name, handler); }
  unregisterCommand(name: string): void { this._shell.unregisterCommand(name); }

  clone(): BuiltinShellDriver {
    const clonedShell = this._shell.clone();
    const driver = Object.create(BuiltinShellDriver.prototype) as BuiltinShellDriver;
    driver._shell = clonedShell;
    driver._fsDriver = new BuiltinFilesystemDriver(clonedShell.fs);
    driver._opts = { ...this._opts };
    return driver;
  }

  capabilities(): Set<string> { return new Set(["custom_commands", "stateful"]); }

  get shell(): Shell { return this._shell; }

  static fromShell(shell: Shell): BuiltinShellDriver {
    const driver = Object.create(BuiltinShellDriver.prototype) as BuiltinShellDriver;
    driver._shell = shell;
    driver._fsDriver = new BuiltinFilesystemDriver(shell.fs);
    driver._opts = {};
    return driver;
  }
}

export type ShellDriverFactoryFn = (opts?: ShellDriverOptions) => ShellDriver;

export class ShellDriverFactory {
  static default: string = "builtin";
  private static _registry: Map<string, ShellDriverFactoryFn> = new Map();

  static register(name: string, factory: ShellDriverFactoryFn): void {
    ShellDriverFactory._registry.set(name, factory);
  }

  static create(name?: string, opts?: ShellDriverOptions): ShellDriver {
    const driverName = name ?? ShellDriverFactory.default;
    if (driverName === "builtin") {
      return new BuiltinShellDriver(opts);
    }
    const factory = ShellDriverFactory._registry.get(driverName);
    if (!factory) {
      throw new Error(`Shell driver '${driverName}' not registered`);
    }
    return factory(opts);
  }

  static reset(): void {
    ShellDriverFactory._registry.clear();
    ShellDriverFactory.default = "builtin";
  }
}
