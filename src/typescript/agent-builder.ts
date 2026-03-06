import { HookEvent } from "./has-hooks.js";
import type { Middleware } from "./has-middleware.js";
import type { ToolDef } from "./uses-tools.js";
import type { StructuredEvent } from "./event-stream-parser.js";
import type { Skill } from "./has-skills.js";
import type { CmdHandler } from "./shell.js";
import type { HasShellOptions } from "./has-shell.js";

export class AgentBuilder {
  private _model: string;
  private _system?: string;
  private _maxTurns: number = 20;
  private _maxRetries: number = 2;
  private _stream: boolean = true;
  private _tools: ToolDef[] = [];
  private _middleware: Middleware[] = [];
  private _hooks: Array<[HookEvent, (...args: any[]) => any]> = [];
  private _events: StructuredEvent[] = [];
  private _skills: Array<[Skill, Record<string, any>?]> = [];
  private _shellOpts?: HasShellOptions;
  private _driver?: string;
  private _commands: Array<[string, CmdHandler]> = [];

  constructor(model: string) {
    this._model = model;
  }

  system(prompt: string): this {
    this._system = prompt;
    return this;
  }

  maxTurns(n: number): this {
    this._maxTurns = n;
    return this;
  }

  maxRetries(n: number): this {
    this._maxRetries = n;
    return this;
  }

  stream(enabled: boolean = true): this {
    this._stream = enabled;
    return this;
  }

  tool(toolDef: ToolDef): this {
    this._tools.push(toolDef);
    return this;
  }

  tools(...toolDefs: ToolDef[]): this {
    this._tools.push(...toolDefs);
    return this;
  }

  middleware(...mws: Middleware[]): this {
    this._middleware.push(...mws);
    return this;
  }

  on(event: HookEvent, callback: (...args: any[]) => any): this {
    this._hooks.push([event, callback]);
    return this;
  }

  event(eventType: StructuredEvent): this {
    this._events.push(eventType);
    return this;
  }

  events(...eventTypes: StructuredEvent[]): this {
    this._events.push(...eventTypes);
    return this;
  }

  skill(sk: Skill, config?: Record<string, any>): this {
    this._skills.push([sk, config]);
    return this;
  }

  skills(...sks: Skill[]): this {
    for (const sk of sks) {
      this._skills.push([sk]);
    }
    return this;
  }

  shell(opts?: HasShellOptions): this {
    this._shellOpts = opts ?? {};
    return this;
  }

  driver(name: string): this {
    this._driver = name;
    return this;
  }

  command(name: string, handler: CmdHandler): this {
    this._commands.push([name, handler]);
    return this;
  }

  async create(): Promise<InstanceType<any>> {
    const { StandardAgent } = await import("./standard-agent.js");
    const agent = new StandardAgent({
      model: this._model,
      system: this._system,
      maxTurns: this._maxTurns,
      maxRetries: this._maxRetries,
      stream: this._stream,
    });

    for (const td of this._tools) {
      agent.registerTool(td);
    }

    for (const mw of this._middleware) {
      agent.use(mw);
    }

    for (const [event, cb] of this._hooks) {
      agent.on(event, cb);
    }

    for (const et of this._events) {
      agent.registerEvent(et);
    }

    if (this._shellOpts !== undefined) {
      agent.initHasShell({ ...this._shellOpts, driver: this._driver });
    } else if (this._driver) {
      agent.initHasShell({ driver: this._driver });
    }

    for (const [name, handler] of this._commands) {
      agent.registerCommand(name, handler);
    }

    // Skills last (async mount)
    for (const [sk, config] of this._skills) {
      await agent.mount(sk, config);
    }

    return agent;
  }
}
