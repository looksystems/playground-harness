/**
 * HasShell mixin — adds virtual shell capabilities to an agent.
 */

import { VirtualFS } from "./virtual-fs.js";
import { Shell, ShellRegistry, ExecResult, CmdHandler } from "./shell.js";
import { HookEvent } from "./has-hooks.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface HasShellOptions {
  shell?: string | Shell;
  cwd?: string;
  env?: Record<string, string>;
  allowedCommands?: Set<string>;
  registerTool?: boolean;
}

export function HasShell<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _shell?: Shell;

    private tryEmit(event: HookEvent, ...args: any[]): void {
      if (typeof (this as any).emit === "function") {
        void (this as any).emit(event, ...args);
      }
    }

    initHasShell(opts?: HasShellOptions): void {
      const options = opts ?? {};
      if (typeof options.shell === "string") {
        this._shell = ShellRegistry.get(options.shell);
      } else if (options.shell instanceof Shell) {
        this._shell = options.shell;
      } else {
        this._shell = new Shell({
          fs: new VirtualFS(),
          cwd: options.cwd ?? "/home/user",
          env: options.env ?? {},
          allowedCommands: options.allowedCommands,
        });
      }

      // Wire onNotFound hook if HasHooks is composed
      if (typeof (this as any).emit === "function") {
        const self = this as any;
        this._shell!.onNotFound = (cmdName: string) => {
          void self.emit(HookEvent.SHELL_NOT_FOUND, cmdName);
        };
      }

      // Auto-register exec tool if UsesTools is composed (opt-out via registerTool: false)
      if ((options.registerTool ?? true) && "registerTool" in this && typeof (this as any).registerTool === "function") {
        this._registerShellTool();
      }
    }

    ensureHasShell(): void {
      if (!this._shell) {
        this.initHasShell();
      }
    }

    private _registerShellTool(): void {
      const self = this as any;
      self.registerTool({
        name: "exec",
        description:
          "Execute a bash command in the virtual filesystem. " +
          "Commands: ls, cat, grep, find, head, tail, wc, sort, uniq, " +
          "cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, echo, stat, test, printf. " +
          "Operators: pipes (|), redirects (>, >>), && (and), || (or), ; (sequence). " +
          "Flow control: if/then/elif/else/fi, for/in/do/done, while/do/done, case/in/esac. " +
          "Features: VAR=value assignment, $(cmd) substitution, $((expr)) arithmetic, ${var:-default} expansion. " +
          "Custom commands registered via registerCommand() are also available.",
        execute: (args: Record<string, any>): string => {
          const result = this.exec(args["command"]);
          const parts: string[] = [];
          if (result.stdout) parts.push(result.stdout);
          if (result.stderr) parts.push(`[stderr] ${result.stderr}`);
          if (result.exitCode !== 0) parts.push(`[exit code: ${result.exitCode}]`);
          return parts.join("") || "(no output)";
        },
        parameters: {
          type: "object",
          properties: {
            command: {
              type: "string",
              description: "The shell command to execute",
            },
          },
          required: ["command"],
        },
      });
    }

    get shell(): Shell {
      this.ensureHasShell();
      return this._shell!;
    }

    get fs(): VirtualFS {
      return this.shell.fs;
    }

    exec(command: string): ExecResult {
      this.tryEmit(HookEvent.SHELL_CALL, command);
      const oldCwd = this.shell.cwd;
      const result = this.shell.exec(command);
      this.tryEmit(HookEvent.SHELL_RESULT, command, result);
      if (this.shell.cwd !== oldCwd) {
        this.tryEmit(HookEvent.SHELL_CWD, oldCwd, this.shell.cwd);
      }
      return result;
    }

    registerCommand(name: string, handler: CmdHandler): void {
      this.shell.registerCommand(name, handler);
      this.tryEmit(HookEvent.COMMAND_REGISTER, name);
    }

    unregisterCommand(name: string): void {
      this.shell.unregisterCommand(name);
      this.tryEmit(HookEvent.COMMAND_UNREGISTER, name);
    }
  };
}
