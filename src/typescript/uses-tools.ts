import { HookEvent } from "./has-hooks.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface ToolDef {
  name: string;
  description: string;
  execute: (args: Record<string, any>) => any | Promise<any>;
  parameters: Record<string, any>;
}

export function defineTool(def: ToolDef): ToolDef {
  return def;
}

export function UsesTools<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _tools: Map<string, ToolDef> = new Map();

    register_tool(toolDef: ToolDef): void {
      this._tools.set(toolDef.name, toolDef);
      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.TOOL_REGISTER, toolDef);
      }
    }

    unregister_tool(name: string): void {
      this._tools.delete(name);
      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.TOOL_UNREGISTER, name);
      }
    }

    _tools_schema(): Record<string, any>[] {
      const schema: Record<string, any>[] = [];
      for (const t of this._tools.values()) {
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

    async _execute_tool(name: string, args: Record<string, any>): Promise<string> {
      const td = this._tools.get(name);
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
