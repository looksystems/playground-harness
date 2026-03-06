import { describe, it, expect } from "vitest";
import { UsesTools, ToolDef, defineTool } from "../../src/typescript/uses-tools.js";

class ToolUser extends UsesTools(class {}) {}

const addTool = defineTool({
  name: "add",
  description: "Add two numbers",
  execute: async (args) => args.a + args.b,
  parameters: {
    type: "object",
    properties: {
      a: { type: "integer" },
      b: { type: "integer" },
    },
    required: ["a", "b"],
  },
});

const multiplyTool = defineTool({
  name: "multiply",
  description: "Multiply two numbers",
  execute: (args) => args.a * args.b,
  parameters: {
    type: "object",
    properties: {
      a: { type: "integer" },
      b: { type: "integer" },
    },
    required: ["a", "b"],
  },
});

describe("UsesTools", () => {
  it("register tool via defineTool", () => {
    const obj = new ToolUser();
    obj.register_tool(addTool);
    expect(obj._tools_schema().some((s) => s.function.name === "add")).toBe(true);
  });

  it("register tooldef directly", () => {
    const obj = new ToolUser();
    const td: ToolDef = {
      name: "custom",
      description: "A custom tool",
      execute: (args) => args.x * 2,
      parameters: { type: "object", properties: { x: { type: "integer" } } },
    };
    obj.register_tool(td);
    expect(obj._tools_schema().some((s) => s.function.name === "custom")).toBe(true);
  });

  it("tools_schema", () => {
    const obj = new ToolUser();
    obj.register_tool(addTool);
    const schema = obj._tools_schema();
    expect(schema.length).toBe(1);
    expect(schema[0].type).toBe("function");
    expect(schema[0].function.name).toBe("add");
    expect(schema[0].function.parameters.properties.a).toBeDefined();
  });

  it("execute tool async", async () => {
    const obj = new ToolUser();
    obj.register_tool(addTool);
    const result = await obj._execute_tool("add", { a: 3, b: 4 });
    expect(result).toContain("7");
  });

  it("execute tool sync", async () => {
    const obj = new ToolUser();
    obj.register_tool(multiplyTool);
    const result = await obj._execute_tool("multiply", { a: 3, b: 4 });
    expect(result).toContain("12");
  });

  it("execute unknown tool", async () => {
    const obj = new ToolUser();
    const result = await obj._execute_tool("nonexistent", {});
    expect(result.toLowerCase()).toMatch(/error|unknown/);
  });

  it("auto schema from definition", () => {
    const obj = new ToolUser();
    obj.register_tool(addTool);
    const schema = obj._tools_schema();
    const props = schema[0].function.parameters.properties;
    expect(props.a.type).toBe("integer");
    expect(props.b.type).toBe("integer");
  });
});
