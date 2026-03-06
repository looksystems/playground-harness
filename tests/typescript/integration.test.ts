import { describe, it, expect } from "vitest";
import { StandardAgent } from "../../src/typescript/standard-agent.js";
import { HookEvent } from "../../src/typescript/has-hooks.js";
import { EventType } from "../../src/typescript/event-stream-parser.js";
import { defineTool } from "../../src/typescript/uses-tools.js";

describe("Integration", () => {
  it("standard agent with events", async () => {
    const agent = new StandardAgent({ model: "gpt-4" });

    agent.register_event({
      name: "user_response",
      description: "Respond to user",
      schema: { data: { message: "string" } },
    });
    agent.default_events = ["user_response"];

    const hookLog: string[] = [];
    agent.on(HookEvent.RUN_START, () => hookLog.push("start"));

    const addTool = defineTool({
      name: "add",
      description: "Add numbers",
      execute: async (args: any) => args.a + args.b,
      parameters: {
        type: "object",
        properties: {
          a: { type: "integer" },
          b: { type: "integer" },
        },
        required: ["a", "b"],
      },
    });
    agent.register_tool(addTool);

    expect(agent._tools_schema().length).toBe(1);

    const busEvents: any[] = [];
    agent.bus.subscribe("user_response", async (e) => busEvents.push(e));

    const active = agent._resolve_active_events();
    expect(active.length).toBe(1);

    const prompt = agent._build_event_prompt(active);
    expect(prompt).toContain("user_response");

    await agent._emit(HookEvent.RUN_START);
    expect(hookLog).toEqual(["start"]);
  });

  it("standard agent with shell", () => {
    const agent = new StandardAgent({ model: "gpt-4" });

    // Shell is available via lazy init
    expect(agent.shell).toBeDefined();
    expect(agent.fs).toBeDefined();

    // Can write files and exec commands
    agent.fs.write("/data/test.txt", "hello world\n");
    const result = agent.exec("cat /data/test.txt");
    expect(result.stdout).toBe("hello world\n");

    // exec tool is auto-registered
    expect(agent._tools_schema().some((s: any) => s.function.name === "exec")).toBe(true);

    // Pipes work
    agent.fs.write("/data/nums.txt", "3\n1\n2\n");
    const result2 = agent.exec("cat /data/nums.txt | sort");
    expect(result2.stdout).toBe("1\n2\n3\n");
  });
});
