import { describe, it, expect } from "vitest";
import { HasCommands, CommandDef } from "../../src/typescript/has-commands.js";
import { UsesTools } from "../../src/typescript/uses-tools.js";
import { HasHooks, HookEvent } from "../../src/typescript/has-hooks.js";
import { SlashCommandMiddleware } from "../../src/typescript/slash-command-middleware.js";

class Base {
  name: string;
  constructor() {
    this.name = "test";
  }
}

const CommandsAgent = HasCommands(Base);
const CommandsToolsAgent = HasCommands(UsesTools(Base));
const HookCommandsAgent = HasCommands(HasHooks(UsesTools(Base)));

describe("HasCommands", () => {
  describe("Standalone", () => {
    it("registers a command in _commands map", () => {
      const agent = new CommandsAgent();
      const def: CommandDef = {
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      };
      agent.registerSlashCommand(def);
      expect(agent.commands.has("help")).toBe(true);
      expect(agent.commands.get("help")).toBe(def);
    });

    it("unregisters a command from _commands map", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      expect(agent.commands.has("help")).toBe(true);
      agent.unregisterSlashCommand("help");
      expect(agent.commands.has("help")).toBe(false);
    });

    it("executes a command and returns string result", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "greet",
        description: "Greet",
        execute: (args) => `Hello, ${args.input}!`,
        parameters: {},
      });
      const result = agent.executeSlashCommand("greet", { input: "World" });
      expect(result).toBe("Hello, World!");
    });

    it("returns error string for unknown command", () => {
      const agent = new CommandsAgent();
      const result = agent.executeSlashCommand("unknown", {});
      expect(result).toContain("unknown");
      expect(result.toLowerCase()).toContain("error");
    });

    it("intercepts /help some args", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: (args) => `Help: ${args.input}`,
        parameters: {},
      });
      const result = agent.interceptSlashCommand("/help some args");
      expect(result).toEqual({ name: "help", args: { input: "some args" } });
    });

    it("intercepts plain text as null", () => {
      const agent = new CommandsAgent();
      const result = agent.interceptSlashCommand("hello");
      expect(result).toBeNull();
    });

    it("intercepts /greet name=World when command has parameter schema", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "greet",
        description: "Greet someone",
        execute: (args) => `Hello, ${args.name}!`,
        parameters: {
          type: "object",
          properties: {
            name: { type: "string" },
          },
        },
      });
      const result = agent.interceptSlashCommand("/greet name=World");
      expect(result).toEqual({ name: "greet", args: { name: "World" } });
    });

    it("commands getter returns the map", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "test",
        description: "Test",
        execute: () => "ok",
        parameters: {},
      });
      const cmds = agent.commands;
      expect(cmds).toBeInstanceOf(Map);
      expect(cmds.size).toBe(1);
    });

    it("lazy init via _ensureHasCommands", () => {
      const agent = new CommandsAgent();
      expect(agent._commands).toBeUndefined();
      // Accessing commands triggers lazy init
      const cmds = agent.commands;
      expect(cmds).toBeInstanceOf(Map);
      expect(agent._commands).toBeDefined();
    });

    it("returns null for unregistered slash command", () => {
      const agent = new CommandsAgent();
      const result = agent.interceptSlashCommand("/nonexistent foo");
      expect(result).toBeNull();
    });
  });

  describe("With UsesTools", () => {
    it("auto-registers slash_{name} tool when llmVisible=true (default)", () => {
      const agent = new CommandsToolsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      expect(agent._tools.has("slash_help")).toBe(true);
      const tool = agent._tools.get("slash_help")!;
      expect(tool.description).toContain("help");
    });

    it("does not register tool when llmVisible=false", () => {
      const agent = new CommandsToolsAgent();
      agent.registerSlashCommand({
        name: "secret",
        description: "Secret command",
        execute: () => "secret",
        parameters: {},
        llmVisible: false,
      });
      expect(agent._tools.has("slash_secret")).toBe(false);
    });

    it("agent-level disable with llmCommandsEnabled=false", () => {
      const agent = new CommandsToolsAgent();
      agent._initHasCommands({ llmCommandsEnabled: false });
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      expect(agent._tools.has("slash_help")).toBe(false);
    });

    it("unregister removes tool", () => {
      const agent = new CommandsToolsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      expect(agent._tools.has("slash_help")).toBe(true);
      agent.unregisterSlashCommand("help");
      expect(agent._tools.has("slash_help")).toBe(false);
    });

    it("tool executes the command", () => {
      const agent = new CommandsToolsAgent();
      agent.registerSlashCommand({
        name: "greet",
        description: "Greet",
        execute: (args) => `Hello, ${args.input}!`,
        parameters: {},
      });
      const tool = agent._tools.get("slash_greet")!;
      const result = tool.execute({ input: "World" });
      expect(result).toBe("Hello, World!");
    });
  });

  describe("With HasHooks", () => {
    it("fires SLASH_COMMAND_REGISTER on register", async () => {
      const agent = new HookCommandsAgent();
      const registered: string[] = [];
      agent.on(HookEvent.SLASH_COMMAND_REGISTER, (def: CommandDef) => {
        registered.push(def.name);
      });
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      await new Promise((r) => setTimeout(r, 0));
      expect(registered).toEqual(["help"]);
    });

    it("fires SLASH_COMMAND_UNREGISTER on unregister", async () => {
      const agent = new HookCommandsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help text",
        parameters: {},
      });
      const unregistered: string[] = [];
      agent.on(HookEvent.SLASH_COMMAND_UNREGISTER, (name: string) => {
        unregistered.push(name);
      });
      agent.unregisterSlashCommand("help");
      await new Promise((r) => setTimeout(r, 0));
      expect(unregistered).toEqual(["help"]);
    });

    it("fires SLASH_COMMAND_CALL on execute", async () => {
      const agent = new HookCommandsAgent();
      agent.registerSlashCommand({
        name: "greet",
        description: "Greet",
        execute: (args) => `Hello, ${args.input}!`,
        parameters: {},
      });
      const calls: any[] = [];
      agent.on(HookEvent.SLASH_COMMAND_CALL, (name: string, args: any) => {
        calls.push({ name, args });
      });
      agent.executeSlashCommand("greet", { input: "World" });
      await new Promise((r) => setTimeout(r, 0));
      expect(calls).toEqual([{ name: "greet", args: { input: "World" } }]);
    });

    it("fires SLASH_COMMAND_RESULT on execute", async () => {
      const agent = new HookCommandsAgent();
      agent.registerSlashCommand({
        name: "greet",
        description: "Greet",
        execute: (args) => `Hello, ${args.input}!`,
        parameters: {},
      });
      const results: any[] = [];
      agent.on(HookEvent.SLASH_COMMAND_RESULT, (name: string, result: string) => {
        results.push({ name, result });
      });
      agent.executeSlashCommand("greet", { input: "World" });
      await new Promise((r) => setTimeout(r, 0));
      expect(results).toEqual([{ name: "greet", result: "Hello, World!" }]);
    });
  });

  describe("SlashCommandMiddleware", () => {
    it("intercepts /command messages and replaces with result", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help output",
        parameters: {},
      });

      const mw = new SlashCommandMiddleware();
      const messages = [
        { role: "user", content: "/help" },
      ];
      const result = mw.pre!(messages, { agent });
      expect(result).toHaveLength(1);
      expect((result as any[])[0].content).toContain("help output");
      expect((result as any[])[0].content).toContain("/help");
    });

    it("passes through regular messages", () => {
      const agent = new CommandsAgent();
      const mw = new SlashCommandMiddleware();
      const messages = [
        { role: "user", content: "hello world" },
      ];
      const result = mw.pre!(messages, { agent });
      expect(result).toEqual(messages);
    });

    it("passes through when no agent in context", () => {
      const mw = new SlashCommandMiddleware();
      const messages = [
        { role: "user", content: "/help" },
      ];
      const result = mw.pre!(messages, {});
      expect(result).toEqual(messages);
    });

    it("passes through when last message is not user role", () => {
      const agent = new CommandsAgent();
      agent.registerSlashCommand({
        name: "help",
        description: "Show help",
        execute: () => "help output",
        parameters: {},
      });
      const mw = new SlashCommandMiddleware();
      const messages = [
        { role: "assistant", content: "/help" },
      ];
      const result = mw.pre!(messages, { agent });
      expect(result).toEqual(messages);
    });
  });
});
