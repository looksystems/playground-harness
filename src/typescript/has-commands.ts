import { HookEvent } from "./has-hooks.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface CommandDef {
  name: string;
  description: string;
  execute: (args: Record<string, any>) => any | Promise<any>;
  parameters: Record<string, any>;
  llmVisible?: boolean;
}

export function HasCommands<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _commands?: Map<string, CommandDef>;
    _llmCommandsEnabled?: boolean;

    _initHasCommands(opts?: { llmCommandsEnabled?: boolean }): void {
      this._commands = new Map();
      this._llmCommandsEnabled = opts?.llmCommandsEnabled ?? true;
    }

    _ensureHasCommands(): void {
      if (!this._commands) {
        this._initHasCommands();
      }
    }

    registerSlashCommand(def: CommandDef): void {
      this._ensureHasCommands();
      this._commands!.set(def.name, def);

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SLASH_COMMAND_REGISTER, def);
      }

      const llmVisible = def.llmVisible !== false;
      if (
        llmVisible &&
        this._llmCommandsEnabled !== false &&
        "register_tool" in this &&
        typeof (this as any).register_tool === "function"
      ) {
        const self = this;
        (this as any).register_tool({
          name: `slash_${def.name}`,
          description: `Slash command /${def.name}: ${def.description}`,
          execute: (args: Record<string, any>) => {
            return self.executeSlashCommand(def.name, args);
          },
          parameters: def.parameters,
        });
      }
    }

    unregisterSlashCommand(name: string): void {
      this._ensureHasCommands();
      this._commands!.delete(name);

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SLASH_COMMAND_UNREGISTER, name);
      }

      if (
        "unregister_tool" in this &&
        typeof (this as any).unregister_tool === "function"
      ) {
        (this as any).unregister_tool(`slash_${name}`);
      }
    }

    executeSlashCommand(name: string, args: Record<string, any>): string {
      this._ensureHasCommands();
      const def = this._commands!.get(name);
      if (!def) {
        return `Error: unknown slash command "/${name}"`;
      }

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SLASH_COMMAND_CALL, name, args);
      }

      const result = String(def.execute(args));

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SLASH_COMMAND_RESULT, name, result);
      }

      return result;
    }

    interceptSlashCommand(
      text: string
    ): { name: string; args: Record<string, any> } | null {
      if (!text.startsWith("/")) {
        return null;
      }

      this._ensureHasCommands();

      const parts = text.slice(1).split(/\s+/);
      const commandName = parts[0];
      const rest = text.slice(1 + commandName.length).trim();

      const def = this._commands!.get(commandName);
      if (!def) {
        return null;
      }

      // If the command has parameters.properties, parse key=value pairs
      if (
        def.parameters &&
        def.parameters.properties &&
        Object.keys(def.parameters.properties).length > 0
      ) {
        const args: Record<string, any> = {};
        const kvPairs = rest.split(/\s+/).filter((s: string) => s.length > 0);
        for (const pair of kvPairs) {
          const eqIdx = pair.indexOf("=");
          if (eqIdx !== -1) {
            const key = pair.slice(0, eqIdx);
            const value = pair.slice(eqIdx + 1);
            args[key] = value;
          }
        }
        return { name: commandName, args };
      }

      return { name: commandName, args: { input: rest } };
    }

    get commands(): Map<string, CommandDef> {
      this._ensureHasCommands();
      return this._commands!;
    }
  };
}
