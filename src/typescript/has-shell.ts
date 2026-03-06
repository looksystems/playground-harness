/**
 * HasShell mixin — adds virtual shell capabilities to an agent.
 */

import { VirtualFS } from "./virtual-fs.js";
import { Shell, ShellRegistry, ExecResult } from "./shell.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface HasShellOptions {
  shell?: string | Shell;
  cwd?: string;
  env?: Record<string, string>;
  allowedCommands?: Set<string>;
}

export function HasShell<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _shell?: Shell;

    _initHasShell(opts?: HasShellOptions): void {
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

      // Auto-register exec tool if UsesTools is composed
      if ("register_tool" in this && typeof (this as any).register_tool === "function") {
        this._registerShellTool();
      }
    }

    _ensureHasShell(): void {
      if (!this._shell) {
        this._initHasShell();
      }
    }

    private _registerShellTool(): void {
      const self = this as any;
      self.register_tool({
        name: "exec",
        description:
          "Execute a bash command in the virtual filesystem. " +
          "Commands: ls, cat, grep, find, head, tail, wc, sort, uniq, " +
          "cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, echo, stat, test, printf. " +
          "Operators: pipes (|), redirects (>, >>), && (and), || (or), ; (sequence). " +
          "Flow control: if/then/elif/else/fi, for/in/do/done, while/do/done, case/in/esac. " +
          "Features: VAR=value assignment, $(cmd) substitution, $((expr)) arithmetic, ${var:-default} expansion.",
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
      this._ensureHasShell();
      return this._shell!;
    }

    get fs(): VirtualFS {
      return this.shell.fs;
    }

    exec(command: string): ExecResult {
      return this.shell.exec(command);
    }
  };
}
