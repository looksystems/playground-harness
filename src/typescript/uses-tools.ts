import { HookEvent } from "./has-hooks.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface ToolDef {
  name: string;
  description: string;
  execute: (args: Record<string, any>) => any | Promise<any>;
  parameters: Record<string, any>;
}

export function defineTool(def: ToolDef): ToolDef {
  if (!def.name) throw new Error("ToolDef requires a name");
  if (!def.execute) throw new Error("ToolDef requires an execute function");
  if (!def.parameters) throw new Error("ToolDef requires parameters");
  return def;
}

export function UsesTools<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    tools: Map<string, ToolDef> = new Map();

    private tryEmit(event: HookEvent, ...args: any[]): void {
      if (typeof (this as any).emit === "function") {
        void (this as any).emit(event, ...args);
      }
    }

    registerTool(toolDef: ToolDef): this {
      this.tools.set(toolDef.name, toolDef);
      this.tryEmit(HookEvent.TOOL_REGISTER, toolDef);
      return this;
    }

    unregisterTool(name: string): this {
      this.tools.delete(name);
      this.tryEmit(HookEvent.TOOL_UNREGISTER, name);
      return this;
    }

    toolsSchema(): Record<string, any>[] {
      const schema: Record<string, any>[] = [];
      for (const t of this.tools.values()) {
        schema.push({
          type: "function",
          function: {
            name: t.name,
            description: t.description,
            parameters: t.parameters,
          },
        });
      }
      return schema;
    }

    async executeTool(name: string, args: Record<string, any>): Promise<string> {
      const td = this.tools.get(name);
      if (!td) {
        return JSON.stringify({ error: `Unknown tool: ${name}` });
      }
      try {
        const result = await Promise.resolve(td.execute(args));
        return JSON.stringify(result);
      } catch (e: any) {
        console.warn(`Tool ${name} error: ${e}`);
        return JSON.stringify({ error: String(e) });
      }
    }
  };
}
