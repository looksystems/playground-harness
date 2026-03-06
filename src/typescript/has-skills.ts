import { HookEvent } from "./has-hooks.js";
import { tryEmit } from "./utils.js";
import type { ToolDef } from "./uses-tools.js";
import type { Middleware } from "./has-middleware.js";

type Constructor<T = {}> = new (...args: any[]) => T;

export interface SkillContext {
  skill: Skill;
  agent: any;
  config: Record<string, any>;
  state: Record<string, any>;
}

export abstract class Skill {
  readonly name: string;
  readonly description: string = "";
  readonly version: string = "0.1.0";
  readonly instructions: string = "";
  readonly dependencies: Constructor<Skill>[] = [];
  context: SkillContext | null = null;

  constructor() {
    const className = this.constructor.name;
    const stripped = className.endsWith("Skill")
      ? className.slice(0, -"Skill".length)
      : className;
    this.name = stripped
      .replace(/([A-Z])/g, "_$1")
      .replace(/^_/, "")
      .toLowerCase();
  }

  async setup(_ctx: SkillContext): Promise<void> {}
  async teardown(_ctx: SkillContext): Promise<void> {}
  tools(): ToolDef[] { return []; }
  middleware(): Middleware[] { return []; }
  hooks(): Partial<Record<HookEvent, Array<(...args: any[]) => any>>> { return {}; }
  commands(): Record<string, (...args: any[]) => any> { return {}; }
}

export class SkillPromptMiddleware implements Middleware {
  private skills: Skill[];

  constructor(skills: Skill[]) {
    this.skills = skills;
  }

  pre(
    messages: Record<string, any>[],
    _context: any
  ): Record<string, any>[] {
    const skillsWithInstructions = this.skills.filter(
      (s) => s.instructions && s.instructions.length > 0
    );
    if (skillsWithInstructions.length === 0) {
      return messages;
    }

    let block = "\n\n---\n**Available Skills:**";
    for (const skill of skillsWithInstructions) {
      block += `\n\n## ${skill.name}\n${skill.instructions}`;
    }

    const systemIdx = messages.findIndex((m) => m.role === "system");
    if (systemIdx !== -1) {
      const updated = { ...messages[systemIdx] };
      updated.content = (updated.content ?? "") + block;
      const result = [...messages];
      result[systemIdx] = updated;
      return result;
    }

    return [{ role: "system", content: block.trimStart() }, ...messages];
  }
}

export class SkillManager {
  private skills: Map<string, Skill> = new Map();
  private mountOrder: string[] = [];
  private promptMw: SkillPromptMiddleware | null = null;
  private agent: any;
  private skillTools: Map<string, string[]> = new Map();
  private skillMiddleware: Map<string, Middleware[]> = new Map();
  private skillHooks: Map<string, Array<[HookEvent, (...args: any[]) => any]>> = new Map();
  private skillCommands: Map<string, string[]> = new Map();

  constructor(agent: any) {
    this.agent = agent;
  }

  async mount(skill: Skill, config?: Record<string, any>): Promise<void> {
    const resolved = this.resolveDeps(skill);

    for (const dep of resolved) {
      if (this.skills.has(dep.name)) {
        continue;
      }
      await this.mountSingle(dep, config);
    }
  }

  private async mountSingle(
    skill: Skill,
    config?: Record<string, any>
  ): Promise<void> {
    const ctx: SkillContext = {
      skill,
      agent: this.agent,
      config: config ?? {},
      state: {},
    };
    skill.context = ctx;

    await skill.setup(ctx);

    const toolNames: string[] = [];
    for (const toolDef of skill.tools()) {
      if (typeof this.agent.registerTool === "function") {
        this.agent.registerTool(toolDef);
      }
      toolNames.push(toolDef.name);
    }
    this.skillTools.set(skill.name, toolNames);

    const mwList = skill.middleware();
    for (const mw of mwList) {
      if (typeof this.agent.use === "function") {
        this.agent.use(mw);
      }
    }
    this.skillMiddleware.set(skill.name, [...mwList]);

    const hookPairs: Array<[HookEvent, (...args: any[]) => any]> = [];
    const hookDefs = skill.hooks();
    for (const [event, callbacks] of Object.entries(hookDefs)) {
      if (callbacks) {
        for (const cb of callbacks) {
          if (typeof this.agent.on === "function") {
            this.agent.on(event as HookEvent, cb);
          }
          hookPairs.push([event as HookEvent, cb]);
        }
      }
    }
    this.skillHooks.set(skill.name, hookPairs);

    const cmdNames: string[] = [];
    const cmds = skill.commands();
    for (const [cmdName, handler] of Object.entries(cmds)) {
      if (typeof this.agent.registerCommand === "function") {
        this.agent.registerCommand(cmdName, handler);
        cmdNames.push(cmdName);
      }
    }
    this.skillCommands.set(skill.name, cmdNames);

    this.skills.set(skill.name, skill);
    this.mountOrder.push(skill.name);
    this.rebuildPromptMiddleware();
  }

  async unmount(name: string): Promise<void> {
    const skill = this.skills.get(name);
    if (!skill || !skill.context) {
      return;
    }

    await skill.teardown(skill.context);

    const toolNames = this.skillTools.get(name) ?? [];
    for (const toolName of toolNames) {
      if (typeof this.agent.unregisterTool === "function") {
        this.agent.unregisterTool(toolName);
      }
    }
    this.skillTools.delete(name);

    const mws = this.skillMiddleware.get(name) ?? [];
    for (const mw of mws) {
      if (typeof this.agent.removeMiddleware === "function") {
        this.agent.removeMiddleware(mw);
      }
    }
    this.skillMiddleware.delete(name);

    const hookPairs = this.skillHooks.get(name) ?? [];
    for (const [event, cb] of hookPairs) {
      if (typeof this.agent.removeHook === "function") {
        this.agent.removeHook(event, cb);
      }
    }
    this.skillHooks.delete(name);

    const cmdNames = this.skillCommands.get(name) ?? [];
    for (const cmdName of cmdNames) {
      if (typeof this.agent.unregisterCommand === "function") {
        this.agent.unregisterCommand(cmdName);
      }
    }
    this.skillCommands.delete(name);

    skill.context = null;
    this.skills.delete(name);
    this.mountOrder = this.mountOrder.filter((n) => n !== name);
    this.rebuildPromptMiddleware();
  }

  async shutdown(): Promise<void> {
    const reversed = [...this.mountOrder].reverse();
    for (const name of reversed) {
      const skill = this.skills.get(name);
      if (skill && skill.context) {
        await skill.teardown(skill.context);
        skill.context = null;
      }
    }
    this.skills.clear();
    this.mountOrder = [];
    this.skillTools.clear();
    this.skillMiddleware.clear();
    this.skillHooks.clear();
    this.skillCommands.clear();
    this.rebuildPromptMiddleware();
  }

  resolveDeps(skill: Skill): Skill[] {
    const visited = new Set<string>();
    const result: Skill[] = [];

    const visit = (s: Skill) => {
      if (visited.has(s.name)) {
        return;
      }
      visited.add(s.name);
      for (const DepCtor of s.dependencies) {
        const dep = new DepCtor();
        visit(dep);
      }
      result.push(s);
    };

    visit(skill);
    return result;
  }

  private rebuildPromptMiddleware(): void {
    if (this.promptMw && typeof this.agent.removeMiddleware === "function") {
      this.agent.removeMiddleware(this.promptMw);
    }

    if (this.skills.size > 0) {
      this.promptMw = new SkillPromptMiddleware(
        Array.from(this.skills.values())
      );
      if (typeof this.agent.use === "function") {
        this.agent.use(this.promptMw);
      }
    } else {
      this.promptMw = null;
    }
  }

  get mounted(): Map<string, Skill> {
    return new Map(this.skills);
  }
}

export function HasSkills<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    skillManager?: SkillManager;

    ensureHasSkills(): void {
      if (!this.skillManager) {
        this.skillManager = new SkillManager(this);
      }
    }

    async mount(skill: Skill, config?: Record<string, any>): Promise<this> {
      this.ensureHasSkills();
      await this.skillManager!.mount(skill, config);
      tryEmit(this, HookEvent.SKILL_SETUP, skill.name, skill);
      tryEmit(this, HookEvent.SKILL_MOUNT, skill.name, skill);
      return this;
    }

    async unmount(name: string): Promise<this> {
      this.ensureHasSkills();
      const skill = this.skillManager!.mounted.get(name);
      tryEmit(this, HookEvent.SKILL_TEARDOWN, name, skill);
      await this.skillManager!.unmount(name);
      tryEmit(this, HookEvent.SKILL_UNMOUNT, name, skill);
      return this;
    }

    async shutdown(): Promise<void> {
      if (this.skillManager) {
        await this.skillManager.shutdown();
      }
    }

    get skills(): Map<string, Skill> {
      this.ensureHasSkills();
      return this.skillManager!.mounted;
    }
  };
}
