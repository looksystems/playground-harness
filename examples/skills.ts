/**
 * skills.ts — Skill system for agent_harness.ts
 *
 * A Skill is a composable unit that bundles:
 *   - Tools (functions the LLM can call)
 *   - System prompt fragments (instructions injected into the prompt)
 *   - Middleware (pre/post message transforms)
 *   - Hooks (lifecycle event listeners)
 *   - Lifecycle methods (setup/teardown for resources)
 *
 * Skills can declare dependencies on other skills, and the manager resolves
 * the full dependency graph when mounting.
 *
 * Usage:
 *   import { Agent, defineTool, HookEvent } from "./agent_harness";
 *   import { Skill, SkillManager } from "./skills";
 *
 *   class WebBrowsingSkill extends Skill {
 *     name = "web_browsing";
 *     description = "Browse and extract content from web pages";
 *     instructions = "Use fetch_page to retrieve web content.";
 *
 *     async setup(ctx) {
 *       ctx.state.session = new SomeHttpClient();
 *     }
 *
 *     async teardown(ctx) {
 *       await ctx.state.session.close();
 *     }
 *
 *     tools() {
 *       return [defineTool({
 *         name: "fetch_page",
 *         description: "Fetch a web page",
 *         parameters: { ... },
 *         execute: async ({ url }) => { ... },
 *       })];
 *     }
 *   }
 *
 *   const agent = new Agent({ model: "gpt-4o" });
 *   const skills = new SkillManager(agent);
 *   await skills.mount(new WebBrowsingSkill());
 *   const result = await agent.run("Summarize https://example.com");
 *   await skills.shutdown();
 */

import type { ChatCompletionMessageParam } from "openai/resources/chat/completions";
import {
  Agent,
  defineTool,
  HookEvent,
  type ToolDef,
  type Middleware,
  type RunContext,
} from "./agent_harness";

// ---------------------------------------------------------------------------
// Skill context — per-skill state bag
// ---------------------------------------------------------------------------

export interface SkillContext {
  skill: Skill;
  agent: Agent;
  config: Record<string, any>;
  state: Record<string, any>;
}

// ---------------------------------------------------------------------------
// Skill base class
// ---------------------------------------------------------------------------

export abstract class Skill {
  /** Unique identifier. Auto-derived from class name if not set. */
  name: string = "";
  description: string = "";
  version: string = "0.1.0";

  /** Instructions injected into the system prompt. */
  instructions: string = "";

  /** Skill classes this skill depends on (resolved transitively). */
  dependencies: (new () => Skill)[] = [];

  /** Set by the SkillManager when mounted. */
  context: SkillContext | null = null;

  constructor() {
    // Auto-derive name from class name: WebBrowsingSkill -> web_browsing
    if (!this.name) {
      this.name = this.constructor.name
        .replace(/Skill$/, "")
        .replace(/([A-Z])/g, "_$1")
        .toLowerCase()
        .replace(/^_/, "");
    }
  }

  // -- Lifecycle -----------------------------------------------------------

  /** Called once when the skill is mounted. Acquire resources here. */
  async setup(_ctx: SkillContext): Promise<void> {}

  /** Called on shutdown. Release resources here. */
  async teardown(_ctx: SkillContext): Promise<void> {}

  // -- Composition ---------------------------------------------------------

  /** Return tools this skill provides. */
  tools(): ToolDef[] {
    return [];
  }

  /** Return middleware this skill provides. */
  middleware(): Middleware[] {
    return [];
  }

  /** Return hooks this skill provides. */
  hooks(): Partial<Record<HookEvent, ((...args: any[]) => void | Promise<void>)[]>> {
    return {};
  }
}

// ---------------------------------------------------------------------------
// Skill-aware prompt middleware
// ---------------------------------------------------------------------------

class SkillPromptMiddleware implements Middleware {
  constructor(private skills: Skill[]) {}

  async pre(
    messages: ChatCompletionMessageParam[],
    _ctx: RunContext
  ): Promise<ChatCompletionMessageParam[]> {
    const fragments = this.skills
      .filter((s) => s.instructions)
      .map((s) => `## ${s.name}\n${s.instructions}`);

    if (fragments.length === 0) return messages;

    const block =
      "\n\n---\n**Available Skills:**\n\n" + fragments.join("\n\n");

    // Append to existing system message or create one
    const system = messages.find((m) => m.role === "system");
    if (system && "content" in system && typeof system.content === "string") {
      system.content += block;
    } else {
      messages.unshift({ role: "system", content: block.trim() });
    }

    return messages;
  }
}

// ---------------------------------------------------------------------------
// Skill manager
// ---------------------------------------------------------------------------

export class SkillManager {
  private skills = new Map<string, Skill>();
  private mountOrder: string[] = [];
  private promptMw: SkillPromptMiddleware | null = null;

  constructor(private agent: Agent) {}

  /** All currently mounted skills. */
  get mounted(): Map<string, Skill> {
    return new Map(this.skills);
  }

  /**
   * Mount a skill onto the agent.
   * Resolves dependencies, calls setup(), registers tools/middleware/hooks.
   */
  async mount(skill: Skill, config?: Record<string, any>): Promise<void> {
    const toMount = this.resolveDeps(skill);

    for (const s of toMount) {
      if (this.skills.has(s.name)) continue;

      // Create context
      const ctx: SkillContext = {
        skill: s,
        agent: this.agent,
        config: config ?? {},
        state: {},
      };
      s.context = ctx;

      // Lifecycle setup
      await s.setup(ctx);

      // Register tools
      for (const t of s.tools()) {
        this.agent.registerTool(t);
      }

      // Register middleware
      for (const mw of s.middleware()) {
        this.agent.use(mw);
      }

      // Register hooks
      const hookMap = s.hooks();
      for (const [event, callbacks] of Object.entries(hookMap)) {
        for (const cb of callbacks ?? []) {
          this.agent.on(event as HookEvent, cb);
        }
      }

      this.skills.set(s.name, s);
      this.mountOrder.push(s.name);
      console.debug(`Mounted skill: ${s.name} (v${s.version})`);
    }

    this.rebuildPromptMiddleware();
  }

  /** Teardown and remove a skill. */
  async unmount(skillName: string): Promise<void> {
    const skill = this.skills.get(skillName);
    if (!skill) throw new Error(`Skill not mounted: ${skillName}`);

    if (skill.context) {
      await skill.teardown(skill.context);
    }

    this.skills.delete(skillName);
    this.mountOrder = this.mountOrder.filter((n) => n !== skillName);
    this.rebuildPromptMiddleware();
    console.debug(`Unmounted skill: ${skillName}`);
  }

  /** Teardown all skills in reverse mount order. */
  async shutdown(): Promise<void> {
    for (const name of [...this.mountOrder].reverse()) {
      const skill = this.skills.get(name);
      if (skill?.context) {
        try {
          await skill.teardown(skill.context);
          console.debug(`Torn down skill: ${name}`);
        } catch (e) {
          console.warn(`Teardown error for ${name}:`, e);
        }
      }
    }
    this.skills.clear();
    this.mountOrder = [];
  }

  // -- Dependency resolution -----------------------------------------------

  private resolveDeps(skill: Skill): Skill[] {
    const visited = new Set<string>();
    const order: Skill[] = [];

    const visit = (s: Skill) => {
      if (visited.has(s.name)) return;
      visited.add(s.name);
      for (const DepClass of s.dependencies) {
        visit(new DepClass());
      }
      order.push(s);
    };

    visit(skill);
    return order;
  }

  private rebuildPromptMiddleware(): void {
    const withInstructions = Array.from(this.skills.values()).filter(
      (s) => s.instructions
    );
    if (withInstructions.length === 0) return;

    // Access internal middleware array (acceptable — same package)
    const mwArray = (this.agent as any).middlewares as Middleware[];

    // Remove old
    if (this.promptMw) {
      const idx = mwArray.indexOf(this.promptMw);
      if (idx !== -1) mwArray.splice(idx, 1);
    }

    this.promptMw = new SkillPromptMiddleware(withInstructions);
    mwArray.unshift(this.promptMw);
  }
}

// ===========================================================================
// Example skills
// ===========================================================================

export class MathSkill extends Skill {
  name = "math";
  description = "Perform arithmetic operations";
  instructions =
    "You have access to math tools for precise calculations. " +
    "Always use these tools instead of doing math in your head.";

  tools(): ToolDef[] {
    return [
      defineTool({
        name: "add",
        description: "Add two numbers",
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
          required: ["a", "b"],
        },
        execute: async ({ a, b }: { a: number; b: number }) => a + b,
      }),
      defineTool({
        name: "subtract",
        description: "Subtract b from a",
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
          required: ["a", "b"],
        },
        execute: async ({ a, b }: { a: number; b: number }) => a - b,
      }),
      defineTool({
        name: "multiply",
        description: "Multiply two numbers",
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
          required: ["a", "b"],
        },
        execute: async ({ a, b }: { a: number; b: number }) => a * b,
      }),
      defineTool({
        name: "divide",
        description: "Divide a by b",
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
          required: ["a", "b"],
        },
        execute: async ({ a, b }: { a: number; b: number }) => {
          if (b === 0) throw new Error("Division by zero");
          return a / b;
        },
      }),
    ];
  }
}

export class MemorySkill extends Skill {
  name = "memory";
  description = "Remember and recall information across turns";
  instructions =
    "You can store and retrieve facts using the memory tools. " +
    "Use remember() to save important information and recall() to retrieve it.";

  async setup(ctx: SkillContext): Promise<void> {
    ctx.state.store = new Map<string, string>();
  }

  tools(): ToolDef[] {
    const getStore = (): Map<string, string> => this.context!.state.store;

    return [
      defineTool({
        name: "remember",
        description: "Store a value with a key",
        parameters: {
          type: "object",
          properties: {
            key: { type: "string" },
            value: { type: "string" },
          },
          required: ["key", "value"],
        },
        execute: async ({ key, value }: { key: string; value: string }) => {
          getStore().set(key, value);
          return `Stored '${key}'`;
        },
      }),
      defineTool({
        name: "recall",
        description: "Retrieve a value by key",
        parameters: {
          type: "object",
          properties: { key: { type: "string" } },
          required: ["key"],
        },
        execute: async ({ key }: { key: string }) =>
          getStore().get(key) ?? `No memory found for '${key}'`,
      }),
      defineTool({
        name: "list_memories",
        description: "List all stored memory keys",
        parameters: { type: "object", properties: {} },
        execute: async () => Array.from(getStore().keys()),
      }),
    ];
  }
}

export class GuardrailSkill extends Skill {
  name = "guardrails";
  description = "Content safety guardrails";

  constructor(private blockedPatterns: string[] = []) {
    super();
  }

  middleware(): Middleware[] {
    const blocked = this.blockedPatterns;
    return [
      {
        async post(
          message: Record<string, any>,
          _ctx: RunContext
        ): Promise<Record<string, any>> {
          const content = ((message.content as string) ?? "").toLowerCase();
          for (const pattern of blocked) {
            if (content.includes(pattern.toLowerCase())) {
              console.warn(`Guardrail triggered: blocked pattern '${pattern}'`);
              message.content =
                "I'm unable to provide that information. Please rephrase your request.";
              break;
            }
          }
          return message;
        },
      },
    ];
  }

  hooks() {
    return {
      [HookEvent.RunStart]: [
        (msgs: any[]) =>
          console.debug(`Guardrail active for ${msgs.length} messages`),
      ],
    };
  }
}

// ===========================================================================
// Demo
// ===========================================================================

async function main() {
  const agent = new Agent({
    model: process.env.AGENT_MODEL ?? "gpt-4o",
    system: "You are a helpful assistant.",
    maxTurns: 10,
    completionParams: { temperature: 0.3 },
  });

  const skills = new SkillManager(agent);

  await skills.mount(new MathSkill());
  await skills.mount(new MemorySkill());
  await skills.mount(new GuardrailSkill(["social security"]));

  console.log(
    "Mounted skills:",
    Array.from(skills.mounted.keys())
  );

  const result = await agent.run(
    "Remember that my favorite number is 42, then compute 42 * 17."
  );
  console.log("\n---\n", result);

  await skills.shutdown();
}

main().catch(console.error);
