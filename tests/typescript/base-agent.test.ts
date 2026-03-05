import { describe, it, expect } from "vitest";
import { BaseAgent, RunContext } from "../../src/typescript/base-agent.js";

describe("BaseAgent", () => {
  it("init defaults", () => {
    const agent = new BaseAgent({ model: "gpt-4" });
    expect(agent.model).toBe("gpt-4");
    expect(agent.maxTurns).toBe(20);
    expect(agent.maxRetries).toBe(2);
    expect(agent.stream).toBe(true);
  });

  it("init custom", () => {
    const agent = new BaseAgent({
      model: "claude-3-opus",
      system: "You are helpful.",
      maxTurns: 5,
      maxRetries: 0,
      stream: false,
    });
    expect(agent.model).toBe("claude-3-opus");
    expect(agent.system).toBe("You are helpful.");
    expect(agent.maxTurns).toBe(5);
  });

  it("build system prompt", async () => {
    const agent = new BaseAgent({ model: "gpt-4", system: "Be helpful." });
    const result = await agent._build_system_prompt("Be helpful.", null);
    expect(result).toBe("Be helpful.");
  });

  it("run context creation", () => {
    const agent = new BaseAgent({ model: "gpt-4" });
    const ctx: RunContext = { agent, turn: 0, metadata: {} };
    expect(ctx.agent).toBe(agent);
    expect(ctx.turn).toBe(0);
  });
});
