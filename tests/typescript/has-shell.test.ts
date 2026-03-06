import { describe, it, expect, beforeEach } from "vitest";
import { HasShell } from "../../src/typescript/has-shell.js";
import { Shell, ShellRegistry } from "../../src/typescript/shell.js";
import { VirtualFS } from "../../src/typescript/virtual-fs.js";
import { UsesTools, ToolDef } from "../../src/typescript/uses-tools.js";
import { HasHooks, HookEvent } from "../../src/typescript/has-hooks.js";

// Simple base class for testing
class Base {
  name: string;
  constructor() {
    this.name = "test";
  }
}

const ShellAgent = HasShell(Base);
const ToolShellAgent = HasShell(UsesTools(Base));

describe("HasShell", () => {
  beforeEach(() => {
    ShellRegistry.reset();
  });

  it("lazy initialization", () => {
    const agent = new ShellAgent();
    // _shell should not be set yet
    expect(agent._shell).toBeUndefined();
    // Accessing shell property triggers init
    const sh = agent.shell;
    expect(sh).toBeInstanceOf(Shell);
    expect(agent._shell).toBeDefined();
  });

  it("exec runs commands", () => {
    const agent = new ShellAgent();
    agent.fs.write("/hello.txt", "hello world");
    const r = agent.exec("cat /hello.txt");
    expect(r.stdout).toBe("hello world");
    expect(r.exitCode).toBe(0);
  });

  it("fs property accesses filesystem", () => {
    const agent = new ShellAgent();
    agent.fs.write("/test.txt", "data");
    expect(agent.fs.read("/test.txt")).toBe("data");
  });

  it("init with shell instance", () => {
    const fs = new VirtualFS({ "/f.txt": "content" });
    const sh = new Shell({ fs, cwd: "/tmp" });
    const agent = new ShellAgent();
    agent.initHasShell({ shell: sh });
    expect(agent.exec("pwd").stdout).toBe("/tmp\n");
    expect(agent.exec("cat /f.txt").stdout).toBe("content");
  });

  it("init with shell name from registry", () => {
    const fs = new VirtualFS({ "/data.txt": "registry-data" });
    const sh = new Shell({ fs });
    ShellRegistry.register("my-shell", sh);

    const agent = new ShellAgent();
    agent.initHasShell({ shell: "my-shell" });
    expect(agent.exec("cat /data.txt").stdout).toBe("registry-data");
  });

  it("init with custom cwd and env", () => {
    const agent = new ShellAgent();
    agent.initHasShell({
      cwd: "/custom",
      env: { NAME: "test" },
    });
    // cwd doesn't need to exist as a real dir for pwd
    expect(agent.exec("echo $NAME").stdout).toBe("test\n");
  });

  it("init with allowed commands", () => {
    const agent = new ShellAgent();
    agent.initHasShell({
      allowedCommands: new Set(["echo"]),
    });
    expect(agent.exec("echo hi").exitCode).toBe(0);
    expect(agent.exec("ls /").exitCode).toBe(127);
  });

  it("auto-registers exec tool when UsesTools is present", () => {
    const agent = new ToolShellAgent();
    agent.initHasShell();

    // Check that exec tool was registered
    const tool = agent.tools.get("exec");
    expect(tool).toBeDefined();
    expect(tool!.name).toBe("exec");
    expect(tool!.description).toContain("Execute a bash command");
  });

  it("exec tool produces output", () => {
    const agent = new ToolShellAgent();
    agent.initHasShell();
    agent.fs.write("/greet.txt", "hello");

    const tool = agent.tools.get("exec")!;
    const result = tool.execute({ command: "cat /greet.txt" });
    expect(result).toBe("hello");
  });

  it("exec tool shows stderr and exit code", () => {
    const agent = new ToolShellAgent();
    agent.initHasShell();

    const tool = agent.tools.get("exec")!;
    const result = tool.execute({ command: "cat /nonexistent" });
    expect(result).toContain("[stderr]");
    expect(result).toContain("[exit code: 1]");
  });

  it("exec tool returns (no output) for empty result", () => {
    const agent = new ToolShellAgent();
    agent.initHasShell();

    const tool = agent.tools.get("exec")!;
    const result = tool.execute({ command: "touch /x.txt" });
    expect(result).toBe("(no output)");
  });

  it("registerTool: false skips exec tool registration", () => {
    const agent = new ToolShellAgent();
    agent.initHasShell({ registerTool: false });
    expect(agent.tools.has("exec")).toBe(false);
  });

  it("does not auto-register when UsesTools is absent", () => {
    const agent = new ShellAgent();
    agent.initHasShell();
    // No tools property on plain ShellAgent
    expect("tools" in agent && agent.tools instanceof Map).toBe(false);
  });

  it("ensureHasShell is idempotent", () => {
    const agent = new ShellAgent();
    agent.ensureHasShell();
    const shell1 = agent._shell;
    agent.ensureHasShell();
    const shell2 = agent._shell;
    expect(shell1).toBe(shell2);
  });

  // Create a hook-capable agent class
  const HookShellAgent = HasShell(HasHooks(UsesTools(Base)));

  describe("Shell hooks", () => {
    it("emits SHELL_CALL before exec", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const calls: string[] = [];
      agent.on(HookEvent.SHELL_CALL, (cmd: string) => { calls.push(cmd); });
      agent.exec("echo hello");
      await new Promise(r => setTimeout(r, 0));
      expect(calls).toEqual(["echo hello"]);
    });

    it("emits SHELL_RESULT after exec", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const results: any[] = [];
      agent.on(HookEvent.SHELL_RESULT, (cmd: string, result: any) => {
        results.push({ cmd, exitCode: result.exitCode });
      });
      agent.exec("echo hello");
      await new Promise(r => setTimeout(r, 0));
      expect(results).toEqual([{ cmd: "echo hello", exitCode: 0 }]);
    });

    it("emits SHELL_NOT_FOUND for unknown commands", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const notFound: string[] = [];
      agent.on(HookEvent.SHELL_NOT_FOUND, (name: string) => { notFound.push(name); });
      agent.exec("nonexistent arg1");
      await new Promise(r => setTimeout(r, 0));
      expect(notFound).toEqual(["nonexistent"]);
    });

    it("emits SHELL_NOT_FOUND inside pipeline", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const notFound: string[] = [];
      agent.on(HookEvent.SHELL_NOT_FOUND, (name: string) => { notFound.push(name); });
      agent.exec("echo hi | bogus");
      await new Promise(r => setTimeout(r, 0));
      expect(notFound).toEqual(["bogus"]);
    });

    it("emits SHELL_CWD when cwd changes", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      agent.fs.write("/tmp/.keep", "");
      const cwdChanges: any[] = [];
      agent.on(HookEvent.SHELL_CWD, (oldCwd: string, newCwd: string) => {
        cwdChanges.push({ oldCwd, newCwd });
      });
      agent.exec("cd /tmp");
      await new Promise(r => setTimeout(r, 0));
      expect(cwdChanges.length).toBe(1);
      expect(cwdChanges[0].newCwd).toBe("/tmp");
    });

    it("does not emit SHELL_CWD when cwd stays the same", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const cwdChanges: any[] = [];
      agent.on(HookEvent.SHELL_CWD, (oldCwd: string, newCwd: string) => {
        cwdChanges.push({ oldCwd, newCwd });
      });
      agent.exec("echo hello");
      await new Promise(r => setTimeout(r, 0));
      expect(cwdChanges).toEqual([]);
    });

    it("hooks don't fire without HasHooks", () => {
      const agent = new ShellAgent();
      // Should not throw
      agent.exec("echo hello");
    });

    it("throwing hook doesn't break exec", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      agent.on(HookEvent.SHELL_CALL, () => { throw new Error("boom"); });
      const result = agent.exec("echo hello");
      expect(result.stdout).toBe("hello\n");
      expect(result.exitCode).toBe(0);
    });

    it("emits COMMAND_REGISTER on registerCommand", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const registered: string[] = [];
      agent.on(HookEvent.COMMAND_REGISTER, (name: string) => { registered.push(name); });
      agent.registerCommand("mycmd", (args, stdin) => ({ stdout: "ok\n", stderr: "", exitCode: 0 }));
      await new Promise(r => setTimeout(r, 0));
      expect(registered).toEqual(["mycmd"]);
    });

    it("emits COMMAND_UNREGISTER on unregisterCommand", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      agent.registerCommand("mycmd", (args, stdin) => ({ stdout: "ok\n", stderr: "", exitCode: 0 }));
      const unregistered: string[] = [];
      agent.on(HookEvent.COMMAND_UNREGISTER, (name: string) => { unregistered.push(name); });
      agent.unregisterCommand("mycmd");
      await new Promise(r => setTimeout(r, 0));
      expect(unregistered).toEqual(["mycmd"]);
    });

    it("emits TOOL_REGISTER on registerTool", async () => {
      const agent = new HookShellAgent();
      agent.initHasShell();
      const registered: any[] = [];
      agent.on(HookEvent.TOOL_REGISTER, (toolDef: any) => { registered.push(toolDef.name); });
      agent.registerTool({
        name: "test_tool",
        description: "test",
        execute: () => "ok",
        parameters: { type: "object", properties: {} },
      });
      await new Promise(r => setTimeout(r, 0));
      // "exec" was auto-registered, plus our "test_tool"
      expect(registered).toContain("test_tool");
    });
  });
});
