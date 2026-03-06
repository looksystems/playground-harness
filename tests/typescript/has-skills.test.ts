import { describe, it, expect, beforeEach } from "vitest";
import {
  HasSkills,
  Skill,
  SkillContext,
  SkillManager,
  SkillPromptMiddleware,
} from "../../src/typescript/has-skills.js";
import { UsesTools } from "../../src/typescript/uses-tools.js";
import { HasHooks, HookEvent } from "../../src/typescript/has-hooks.js";
import { HasMiddleware, Middleware } from "../../src/typescript/has-middleware.js";
import type { ToolDef } from "../../src/typescript/uses-tools.js";

// ---------------------------------------------------------------------------
// Base class & mixin compositions
// ---------------------------------------------------------------------------

class Base {
  name = "test";
}

const SkillsAgent = HasSkills(Base);
const SkillsToolsAgent = HasSkills(UsesTools(Base));
const HookSkillsAgent = HasSkills(HasHooks(UsesTools(Base)));
const FullSkillAgent = HasSkills(HasHooks(UsesTools(HasMiddleware(Base))));

// ---------------------------------------------------------------------------
// Test skills
// ---------------------------------------------------------------------------

class WebBrowsingSkill extends Skill {
  description = "Browse the web";
}

class MathSkill extends Skill {
  description = "Math operations";
  instructions = "Use math tools";

  tools(): ToolDef[] {
    return [
      {
        name: "add",
        description: "Add two numbers",
        execute: (args: any) => args.a + args.b,
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
        },
      },
      {
        name: "multiply",
        description: "Multiply two numbers",
        execute: (args: any) => args.a * args.b,
        parameters: {
          type: "object",
          properties: {
            a: { type: "number" },
            b: { type: "number" },
          },
        },
      },
    ];
  }
}

class EmptySkill extends Skill {
  description = "Does nothing";
}

class TrackingSkill extends Skill {
  static setupCalls: string[] = [];
  static teardownCalls: string[] = [];
  description = "Tracks lifecycle";

  async setup(ctx: SkillContext): Promise<void> {
    TrackingSkill.setupCalls.push(this.name);
  }

  async teardown(ctx: SkillContext): Promise<void> {
    TrackingSkill.teardownCalls.push(this.name);
  }
}

class AlphaTrackingSkill extends Skill {
  static setupCalls: string[] = [];
  static teardownCalls: string[] = [];
  description = "Alpha tracker";

  async setup(_ctx: SkillContext): Promise<void> {
    AlphaTrackingSkill.setupCalls.push(this.name);
  }

  async teardown(_ctx: SkillContext): Promise<void> {
    AlphaTrackingSkill.teardownCalls.push(this.name);
  }
}

class BetaTrackingSkill extends Skill {
  static setupCalls: string[] = [];
  static teardownCalls: string[] = [];
  description = "Beta tracker";

  async setup(_ctx: SkillContext): Promise<void> {
    BetaTrackingSkill.setupCalls.push(this.name);
  }

  async teardown(_ctx: SkillContext): Promise<void> {
    BetaTrackingSkill.teardownCalls.push(this.name);
  }
}

class InstructionSkill extends Skill {
  description = "Has instructions";
  instructions = "Follow these instructions for the skill";
}

class NoInstructionSkill extends Skill {
  description = "No instructions";
}

class MiddlewareSkill extends Skill {
  description = "Provides middleware";
  static preCalled = false;

  middleware(): Middleware[] {
    return [
      {
        pre(messages: Record<string, any>[], _context: any) {
          MiddlewareSkill.preCalled = true;
          return messages;
        },
      },
    ];
  }
}

class HookProvidingSkill extends Skill {
  description = "Provides hooks";
  static hookFired = false;

  hooks(): Partial<Record<HookEvent, Array<(...args: any[]) => any>>> {
    return {
      [HookEvent.RUN_START]: [
        () => {
          HookProvidingSkill.hookFired = true;
        },
      ],
    };
  }
}

// Dependency skills
class DepCSkill extends Skill {
  description = "Dependency C";
}

class DepBSkill extends Skill {
  description = "Dependency B";
  dependencies = [DepCSkill as any];
}

class DepASkill extends Skill {
  description = "Dependency A";
  dependencies = [DepBSkill as any];
}

class StandaloneDepSkill extends Skill {
  description = "Standalone dep";
  dependencies = [MathSkill as any];
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("HasSkills", () => {
  // -----------------------------------------------------------------------
  // 1. Skill base
  // -----------------------------------------------------------------------
  describe("Skill base", () => {
    it("derives name from WebBrowsingSkill -> web_browsing", () => {
      const skill = new WebBrowsingSkill();
      expect(skill.name).toBe("web_browsing");
    });

    it("derives name from MathSkill -> math", () => {
      const skill = new MathSkill();
      expect(skill.name).toBe("math");
    });

    it("derives name from EmptySkill -> empty", () => {
      const skill = new EmptySkill();
      expect(skill.name).toBe("empty");
    });

    it("default version is 0.1.0", () => {
      const skill = new WebBrowsingSkill();
      expect(skill.version).toBe("0.1.0");
    });

    it("default tools() returns empty array", () => {
      const skill = new EmptySkill();
      expect(skill.tools()).toEqual([]);
    });

    it("default middleware() returns empty array", () => {
      const skill = new EmptySkill();
      expect(skill.middleware()).toEqual([]);
    });

    it("default hooks() returns empty object", () => {
      const skill = new EmptySkill();
      expect(skill.hooks()).toEqual({});
    });

    it("context is null before mounting", () => {
      const skill = new MathSkill();
      expect(skill.context).toBeNull();
    });
  });

  // -----------------------------------------------------------------------
  // 2. SkillManager standalone
  // -----------------------------------------------------------------------
  describe("SkillManager standalone", () => {
    it("mounts a skill into skills map", async () => {
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      const skill = new MathSkill();
      await mgr.mount(skill);
      expect(mgr.mounted.has("math")).toBe(true);
      expect(mgr.mounted.get("math")).toBe(skill);
    });

    it("unmounts a skill from skills map", async () => {
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      const skill = new MathSkill();
      await mgr.mount(skill);
      expect(mgr.mounted.has("math")).toBe(true);
      await mgr.unmount("math");
      expect(mgr.mounted.has("math")).toBe(false);
    });

    it("shutdown tears down in reverse order", async () => {
      AlphaTrackingSkill.teardownCalls = [];
      BetaTrackingSkill.teardownCalls = [];
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      await mgr.mount(new AlphaTrackingSkill());
      await mgr.mount(new BetaTrackingSkill());

      const teardownOrder: string[] = [];
      const origAlpha = AlphaTrackingSkill.prototype.teardown;
      const origBeta = BetaTrackingSkill.prototype.teardown;
      AlphaTrackingSkill.prototype.teardown = async function (ctx) {
        teardownOrder.push(this.name);
        return origAlpha.call(this, ctx);
      };
      BetaTrackingSkill.prototype.teardown = async function (ctx) {
        teardownOrder.push(this.name);
        return origBeta.call(this, ctx);
      };

      await mgr.shutdown();

      // Restore originals
      AlphaTrackingSkill.prototype.teardown = origAlpha;
      BetaTrackingSkill.prototype.teardown = origBeta;

      // beta mounted second, so torn down first
      expect(teardownOrder[0]).toBe("beta_tracking");
      expect(teardownOrder[1]).toBe("alpha_tracking");
      expect(mgr.mounted.size).toBe(0);
    });

    it("duplicate mount is skipped", async () => {
      TrackingSkill.setupCalls = [];
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      const skill = new TrackingSkill();
      await mgr.mount(skill);
      await mgr.mount(skill);
      // setup called only once
      expect(TrackingSkill.setupCalls).toEqual(["tracking"]);
    });

    it("unmount of non-existent skill is a no-op", async () => {
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      await mgr.unmount("nonexistent");
      expect(mgr.mounted.size).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // 3. With UsesTools
  // -----------------------------------------------------------------------
  describe("With UsesTools", () => {
    it("skill tools are registered on agent", async () => {
      const agent = new SkillsToolsAgent();
      const skill = new MathSkill();
      await agent.mount(skill);
      expect(agent._tools.has("add")).toBe(true);
      expect(agent._tools.has("multiply")).toBe(true);
    });

    it("unmount removes tools from agent", async () => {
      const agent = new SkillsToolsAgent();
      const skill = new MathSkill();
      await agent.mount(skill);
      expect(agent._tools.has("add")).toBe(true);
      await agent.unmount("math");
      expect(agent._tools.has("add")).toBe(false);
      expect(agent._tools.has("multiply")).toBe(false);
    });

    it("registered tools are functional", async () => {
      const agent = new SkillsToolsAgent();
      await agent.mount(new MathSkill());
      const addTool = agent._tools.get("add")!;
      expect(addTool.execute({ a: 3, b: 4 })).toBe(7);
      const mulTool = agent._tools.get("multiply")!;
      expect(mulTool.execute({ a: 3, b: 4 })).toBe(12);
    });

    it("skill with no tools does not add to _tools", async () => {
      const agent = new SkillsToolsAgent();
      await agent.mount(new EmptySkill());
      // Only the prompt middleware might be present, but no tool names from EmptySkill
      expect(agent._tools.size).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // 4. With HasHooks
  // -----------------------------------------------------------------------
  describe("With HasHooks", () => {
    it("fires SKILL_MOUNT on mount", async () => {
      const agent = new HookSkillsAgent();
      const mounted: string[] = [];
      agent.on(HookEvent.SKILL_MOUNT, (name: string) => {
        mounted.push(name);
      });
      await agent.mount(new MathSkill());
      await new Promise((r) => setTimeout(r, 0));
      expect(mounted).toEqual(["math"]);
    });

    it("fires SKILL_UNMOUNT on unmount", async () => {
      const agent = new HookSkillsAgent();
      await agent.mount(new MathSkill());
      const unmounted: string[] = [];
      agent.on(HookEvent.SKILL_UNMOUNT, (name: string) => {
        unmounted.push(name);
      });
      await agent.unmount("math");
      await new Promise((r) => setTimeout(r, 0));
      expect(unmounted).toEqual(["math"]);
    });

    it("fires SKILL_SETUP on mount", async () => {
      const agent = new HookSkillsAgent();
      const setups: string[] = [];
      agent.on(HookEvent.SKILL_SETUP, (name: string) => {
        setups.push(name);
      });
      await agent.mount(new WebBrowsingSkill());
      await new Promise((r) => setTimeout(r, 0));
      expect(setups).toEqual(["web_browsing"]);
    });

    it("fires SKILL_TEARDOWN on unmount", async () => {
      const agent = new HookSkillsAgent();
      await agent.mount(new WebBrowsingSkill());
      const teardowns: string[] = [];
      agent.on(HookEvent.SKILL_TEARDOWN, (name: string) => {
        teardowns.push(name);
      });
      await agent.unmount("web_browsing");
      await new Promise((r) => setTimeout(r, 0));
      expect(teardowns).toEqual(["web_browsing"]);
    });

    it("hook receives skill object as second argument", async () => {
      const agent = new HookSkillsAgent();
      let receivedSkill: Skill | null = null;
      agent.on(HookEvent.SKILL_MOUNT, (_name: string, skill: Skill) => {
        receivedSkill = skill;
      });
      const math = new MathSkill();
      await agent.mount(math);
      await new Promise((r) => setTimeout(r, 0));
      expect(receivedSkill).toBe(math);
    });
  });

  // -----------------------------------------------------------------------
  // 5. Lifecycle
  // -----------------------------------------------------------------------
  describe("Lifecycle", () => {
    beforeEach(() => {
      TrackingSkill.setupCalls = [];
      TrackingSkill.teardownCalls = [];
    });

    it("setup() is called with SkillContext on mount", async () => {
      const agent = new SkillsAgent();
      const skill = new TrackingSkill();
      await agent.mount(skill);
      expect(TrackingSkill.setupCalls).toEqual(["tracking"]);
      expect(skill.context).not.toBeNull();
      expect(skill.context!.agent).toBe(agent);
      expect(skill.context!.skill).toBe(skill);
    });

    it("teardown() is called on unmount", async () => {
      const agent = new SkillsAgent();
      await agent.mount(new TrackingSkill());
      await agent.unmount("tracking");
      expect(TrackingSkill.teardownCalls).toEqual(["tracking"]);
    });

    it("shutdown() calls teardown in reverse mount order", async () => {
      const order: string[] = [];

      class FirstSkill extends Skill {
        description = "first";
        async teardown(_ctx: SkillContext) {
          order.push("first");
        }
      }

      class SecondSkill extends Skill {
        description = "second";
        async teardown(_ctx: SkillContext) {
          order.push("second");
        }
      }

      class ThirdSkill extends Skill {
        description = "third";
        async teardown(_ctx: SkillContext) {
          order.push("third");
        }
      }

      const agent = new SkillsAgent();
      await agent.mount(new FirstSkill());
      await agent.mount(new SecondSkill());
      await agent.mount(new ThirdSkill());
      await agent.shutdown();
      expect(order).toEqual(["third", "second", "first"]);
    });

    it("context is set to null after unmount", async () => {
      const agent = new SkillsAgent();
      const skill = new TrackingSkill();
      await agent.mount(skill);
      expect(skill.context).not.toBeNull();
      await agent.unmount("tracking");
      expect(skill.context).toBeNull();
    });

    it("config is passed through to SkillContext", async () => {
      let receivedConfig: Record<string, any> | null = null;

      class ConfigSkill extends Skill {
        description = "config test";
        async setup(ctx: SkillContext) {
          receivedConfig = ctx.config;
        }
      }

      const agent = new SkillsAgent();
      await agent.mount(new ConfigSkill(), { key: "value" });
      expect(receivedConfig).toEqual({ key: "value" });
    });
  });

  // -----------------------------------------------------------------------
  // 6. Instructions (SkillPromptMiddleware)
  // -----------------------------------------------------------------------
  describe("Instructions", () => {
    it("skill with instructions injects system message", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new InstructionSkill());

      const messages = [
        { role: "system", content: "You are helpful." },
        { role: "user", content: "Hello" },
      ];
      const result = await agent._run_pre(messages, {});
      const system = result.find((m: any) => m.role === "system");
      expect(system!.content).toContain("instruction");
      expect(system!.content).toContain("Follow these instructions");
    });

    it("multiple skills with instructions all appear", async () => {
      class SkillAlpha extends Skill {
        description = "Alpha";
        instructions = "Alpha instructions here";
      }
      class SkillBeta extends Skill {
        description = "Beta";
        instructions = "Beta instructions here";
      }

      const agent = new FullSkillAgent();
      await agent.mount(new SkillAlpha());
      await agent.mount(new SkillBeta());

      const messages = [
        { role: "system", content: "Base system." },
        { role: "user", content: "Hello" },
      ];
      const result = await agent._run_pre(messages, {});
      const system = result.find((m: any) => m.role === "system");
      expect(system!.content).toContain("Alpha instructions here");
      expect(system!.content).toContain("Beta instructions here");
    });

    it("no instructions means no injection", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new NoInstructionSkill());

      const messages = [
        { role: "system", content: "Base system." },
        { role: "user", content: "Hello" },
      ];
      const result = await agent._run_pre(messages, {});
      const system = result.find((m: any) => m.role === "system");
      expect(system!.content).toBe("Base system.");
    });

    it("creates system message if none exists", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new InstructionSkill());

      const messages = [{ role: "user", content: "Hello" }];
      const result = await agent._run_pre(messages, {});
      const system = result.find((m: any) => m.role === "system");
      expect(system).toBeDefined();
      expect(system!.content).toContain("Follow these instructions");
    });

    it("SkillPromptMiddleware.pre returns messages unchanged when no skills have instructions", () => {
      const mw = new SkillPromptMiddleware([new NoInstructionSkill()]);
      const messages = [
        { role: "system", content: "Hello" },
        { role: "user", content: "Hi" },
      ];
      const result = mw.pre(messages, {});
      expect(result).toEqual(messages);
    });
  });

  // -----------------------------------------------------------------------
  // 7. Middleware and hooks from skill
  // -----------------------------------------------------------------------
  describe("Middleware and hooks from skill", () => {
    beforeEach(() => {
      MiddlewareSkill.preCalled = false;
      HookProvidingSkill.hookFired = false;
    });

    it("skill returning middleware() registers on agent", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new MiddlewareSkill());
      // The skill's middleware should be in agent._middleware
      // Run pre to verify it is called
      await agent._run_pre([], {});
      expect(MiddlewareSkill.preCalled).toBe(true);
    });

    it("skill returning hooks() registers on agent", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new HookProvidingSkill());
      // Fire the event that the skill registered a hook for
      await agent._emit(HookEvent.RUN_START);
      expect(HookProvidingSkill.hookFired).toBe(true);
    });

    it("skill hooks do not fire for unrelated events", async () => {
      const agent = new FullSkillAgent();
      await agent.mount(new HookProvidingSkill());
      await agent._emit(HookEvent.RUN_END);
      expect(HookProvidingSkill.hookFired).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // 8. Dependencies
  // -----------------------------------------------------------------------
  describe("Dependencies", () => {
    it("mounting A with dep B also mounts B", async () => {
      const agent = new SkillsAgent();
      await agent.mount(new StandaloneDepSkill());
      expect(agent.skills.has("standalone_dep")).toBe(true);
      expect(agent.skills.has("math")).toBe(true);
    });

    it("already-mounted deps are skipped", async () => {
      TrackingSkill.setupCalls = [];
      const agent = new SkillsAgent();
      // Pre-mount math
      await agent.mount(new MathSkill());
      // Now mount something that depends on math
      await agent.mount(new StandaloneDepSkill());
      // math setup should only be called once
      expect(
        [...agent.skills.keys()].filter((k) => k === "math").length
      ).toBe(1);
    });

    it("A -> B -> C transitive dependencies resolved", async () => {
      const agent = new SkillsAgent();
      await agent.mount(new DepASkill());
      expect(agent.skills.has("dep_a")).toBe(true);
      expect(agent.skills.has("dep_b")).toBe(true);
      expect(agent.skills.has("dep_c")).toBe(true);
    });

    it("transitive deps mounted in correct order (C before B before A)", async () => {
      const order: string[] = [];

      // Use SkillManager directly to track mount order
      const agent = { _middleware: [] as any[] };
      const mgr = new SkillManager(agent);
      const resolved = mgr.resolveDeps(new DepASkill());
      for (const s of resolved) {
        order.push(s.name);
      }
      // C has no deps, then B depends on C, then A depends on B
      expect(order).toEqual(["dep_c", "dep_b", "dep_a"]);
    });
  });

  // -----------------------------------------------------------------------
  // Mixin API surface
  // -----------------------------------------------------------------------
  describe("Mixin API", () => {
    it("skills getter returns a Map", () => {
      const agent = new SkillsAgent();
      expect(agent.skills).toBeInstanceOf(Map);
    });

    it("lazy initialization via _ensureHasSkills", () => {
      const agent = new SkillsAgent();
      expect(agent._skillManager).toBeUndefined();
      const _ = agent.skills;
      expect(agent._skillManager).toBeDefined();
    });

    it("shutdown on agent with no skills is a no-op", async () => {
      const agent = new SkillsAgent();
      // Should not throw
      await agent.shutdown();
    });
  });
});
