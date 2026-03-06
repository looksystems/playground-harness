import { HookEvent } from "./has-hooks.js";
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
      if (typeof this.agent.register_tool === "function") {
        this.agent.register_tool(toolDef);
      }
      toolNames.push(toolDef.name);
    }
    this.skillTools.set(skill.name, toolNames);

    for (const mw of skill.middleware()) {
      if (typeof this.agent.use === "function") {
        this.agent.use(mw);
      }
    }

    const hookDefs = skill.hooks();
    for (const [event, callbacks] of Object.entries(hookDefs)) {
      if (callbacks) {
        for (const cb of callbacks) {
          if (typeof this.agent.on === "function") {
            this.agent.on(event as HookEvent, cb);
          }
        }
      }
    }

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
      if (typeof this.agent.unregister_tool === "function") {
        this.agent.unregister_tool(toolName);
      }
    }
    this.skillTools.delete(name);

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
    if (this.agent._middleware && this.promptMw) {
      const idx = this.agent._middleware.indexOf(this.promptMw);
      if (idx !== -1) {
        this.agent._middleware.splice(idx, 1);
      }
    }

    if (this.skills.size > 0) {
      this.promptMw = new SkillPromptMiddleware(
        Array.from(this.skills.values())
      );
      if (this.agent._middleware) {
        this.agent._middleware.push(this.promptMw);
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
    _skillManager?: SkillManager;

    _ensureHasSkills(): void {
      if (!this._skillManager) {
        this._skillManager = new SkillManager(this);
      }
    }

    async mount(skill: Skill, config?: Record<string, any>): Promise<void> {
      this._ensureHasSkills();
      await this._skillManager!.mount(skill, config);

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SKILL_SETUP, skill.name, skill);
        void (this as any)._emit(HookEvent.SKILL_MOUNT, skill.name, skill);
      }
    }

    async unmount(name: string): Promise<void> {
      this._ensureHasSkills();
      const skill = this._skillManager!.mounted.get(name);

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SKILL_TEARDOWN, name, skill);
      }

      await this._skillManager!.unmount(name);

      if (typeof (this as any)._emit === "function") {
        void (this as any)._emit(HookEvent.SKILL_UNMOUNT, name, skill);
      }
    }

    async shutdown(): Promise<void> {
      if (this._skillManager) {
        await this._skillManager.shutdown();
      }
    }

    get skills(): Map<string, Skill> {
      this._ensureHasSkills();
      return this._skillManager!.mounted;
    }
  };
}
