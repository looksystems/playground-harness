import { describe, it, expect, vi } from "vitest";
import { StandardAgent } from "../../src/typescript/standard-agent.js";
import { AgentBuilder } from "../../src/typescript/agent-builder.js";
import { HookEvent } from "../../src/typescript/has-hooks.js";
import { StructuredEvent } from "../../src/typescript/event-stream-parser.js";
import { Skill } from "../../src/typescript/has-skills.js";
import { defineTool } from "../../src/typescript/uses-tools.js";

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

function testTool(name = "test_tool") {
  return defineTool({
    name,
    description: `A ${name}`,
    execute: () => "ok",
    parameters: { type: "object", properties: {} },
  });
}

class TestSkill extends Skill {
  description = "A test skill";
}

// ---------------------------------------------------------------------------
// AgentBuilder
// ---------------------------------------------------------------------------

describe("AgentBuilder", () => {
  it("build() returns AgentBuilder", () => {
    expect((StandardAgent as any).build("gpt-4")).toBeInstanceOf(AgentBuilder);
  });

  it("create() returns agent with correct model", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    expect(agent.model).toBe("gpt-4");
  });

  it("system()", async () => {
    const agent = await new AgentBuilder("gpt-4").system("Be helpful").create();
    expect(agent.system).toBe("Be helpful");
  });

  it("maxTurns()", async () => {
    const agent = await new AgentBuilder("gpt-4").maxTurns(5).create();
    expect(agent.maxTurns).toBe(5);
  });

  it("tools()", async () => {
    const agent = await new AgentBuilder("gpt-4")
      .tools(testTool("test_tool"))
      .create();
    expect(agent.tools.has("test_tool")).toBe(true);
  });

  it("tool() singular", async () => {
    const agent = await new AgentBuilder("gpt-4")
      .tool(testTool("test_tool"))
      .create();
    expect(agent.tools.has("test_tool")).toBe(true);
  });

  it("middleware()", async () => {
    const mw = { pre: (msgs: any[]) => msgs };
    const agent = await new AgentBuilder("gpt-4").middleware(mw).create();
    expect(agent.middlewareStack.length).toBeGreaterThanOrEqual(1);
  });

  it("on()", async () => {
    const cb = vi.fn();
    const agent = await new AgentBuilder("gpt-4")
      .on(HookEvent.RUN_START, cb)
      .create();
    expect(agent.hooks.has(HookEvent.RUN_START)).toBe(true);
  });

  it("event()", async () => {
    const evt: StructuredEvent = {
      name: "test",
      description: "A test event",
      schema: { value: "string" },
    };
    const agent = await new AgentBuilder("gpt-4").event(evt).create();
    expect(agent.eventRegistry.has("test")).toBe(true);
  });

  it("skill()", async () => {
    const agent = await new AgentBuilder("gpt-4")
      .skill(new TestSkill())
      .create();
    expect(agent.skills.has("test")).toBe(true);
  });

  it("shell()", async () => {
    const agent = await new AgentBuilder("gpt-4")
      .shell({ registerTool: false })
      .create();
    expect(agent.shell).toBeDefined();
  });

  it("command()", async () => {
    const agent = await new AgentBuilder("gpt-4")
      .shell({ registerTool: false })
      .command("greet", () => ({ stdout: "hi\n", stderr: "", exitCode: 0 }))
      .create();
    const result = agent.exec("greet");
    expect(result.stdout).toBe("hi\n");
  });

  it("fluent chaining — all methods return builder instance", () => {
    const builder = new AgentBuilder("gpt-4");
    const evt: StructuredEvent = {
      name: "test",
      description: "test",
      schema: {},
    };
    const result = builder
      .system("sys")
      .maxTurns(3)
      .maxRetries(1)
      .stream(false)
      .tool(testTool("a"))
      .tools(testTool("b"))
      .middleware({ pre: (m: any[]) => m })
      .on(HookEvent.RUN_START, () => {})
      .event(evt)
      .skill(new TestSkill())
      .shell()
      .command("x", () => ({ stdout: "", stderr: "", exitCode: 0 }));
    expect(result).toBe(builder);
  });
});

// ---------------------------------------------------------------------------
// off()
// ---------------------------------------------------------------------------

describe("off()", () => {
  it("removes callback so it no longer fires", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    const calls: string[] = [];
    const cb = () => calls.push("fired");

    agent.on(HookEvent.RUN_START, cb);
    await agent.emit(HookEvent.RUN_START);
    expect(calls).toEqual(["fired"]);

    agent.off(HookEvent.RUN_START, cb);
    await agent.emit(HookEvent.RUN_START);
    expect(calls).toEqual(["fired"]); // not called again
  });

  it("returns this for chaining", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    const cb = () => {};
    agent.on(HookEvent.RUN_START, cb);
    const result = agent.off(HookEvent.RUN_START, cb);
    expect(result).toBe(agent);
  });

  it("nonexistent callback removal is a noop", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    // Should not throw
    expect(() => agent.off(HookEvent.RUN_START, () => {})).not.toThrow();
  });
});

// ---------------------------------------------------------------------------
// unregisterEvent()
// ---------------------------------------------------------------------------

describe("unregisterEvent()", () => {
  it("removes a registered event", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    const evt: StructuredEvent = {
      name: "test",
      description: "test",
      schema: {},
    };
    agent.registerEvent(evt);
    expect(agent.eventRegistry.has("test")).toBe(true);
    agent.unregisterEvent("test");
    expect(agent.eventRegistry.has("test")).toBe(false);
  });

  it("returns this for chaining", async () => {
    const agent = await new AgentBuilder("gpt-4").create();
    const result = agent.unregisterEvent("nonexistent");
    expect(result).toBe(agent);
  });
});
