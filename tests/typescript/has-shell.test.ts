import { describe, it, expect, beforeEach } from "vitest";
import { HasShell } from "../../src/typescript/has-shell.js";
import { Shell, ShellRegistry } from "../../src/typescript/shell.js";
import { VirtualFS } from "../../src/typescript/virtual-fs.js";
import { UsesTools, ToolDef } from "../../src/typescript/uses-tools.js";

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
    agent._initHasShell({ shell: sh });
    expect(agent.exec("pwd").stdout).toBe("/tmp\n");
    expect(agent.exec("cat /f.txt").stdout).toBe("content");
  });

  it("init with shell name from registry", () => {
    const fs = new VirtualFS({ "/data.txt": "registry-data" });
    const sh = new Shell({ fs });
    ShellRegistry.register("my-shell", sh);

    const agent = new ShellAgent();
    agent._initHasShell({ shell: "my-shell" });
    expect(agent.exec("cat /data.txt").stdout).toBe("registry-data");
  });

  it("init with custom cwd and env", () => {
    const agent = new ShellAgent();
    agent._initHasShell({
      cwd: "/custom",
      env: { NAME: "test" },
    });
    // cwd doesn't need to exist as a real dir for pwd
    expect(agent.exec("echo $NAME").stdout).toBe("test\n");
  });

  it("init with allowed commands", () => {
    const agent = new ShellAgent();
    agent._initHasShell({
      allowedCommands: new Set(["echo"]),
    });
    expect(agent.exec("echo hi").exitCode).toBe(0);
    expect(agent.exec("ls /").exitCode).toBe(127);
  });

  it("auto-registers exec tool when UsesTools is present", () => {
    const agent = new ToolShellAgent();
    agent._initHasShell();

    // Check that exec tool was registered
    const tool = agent._tools.get("exec");
    expect(tool).toBeDefined();
    expect(tool!.name).toBe("exec");
    expect(tool!.description).toContain("Execute a bash command");
  });

  it("exec tool produces output", () => {
    const agent = new ToolShellAgent();
    agent._initHasShell();
    agent.fs.write("/greet.txt", "hello");

    const tool = agent._tools.get("exec")!;
    const result = tool.execute({ command: "cat /greet.txt" });
    expect(result).toBe("hello");
  });

  it("exec tool shows stderr and exit code", () => {
    const agent = new ToolShellAgent();
    agent._initHasShell();

    const tool = agent._tools.get("exec")!;
    const result = tool.execute({ command: "cat /nonexistent" });
    expect(result).toContain("[stderr]");
    expect(result).toContain("[exit code: 1]");
  });

  it("exec tool returns (no output) for empty result", () => {
    const agent = new ToolShellAgent();
    agent._initHasShell();

    const tool = agent._tools.get("exec")!;
    const result = tool.execute({ command: "touch /x.txt" });
    expect(result).toBe("(no output)");
  });

  it("does not auto-register when UsesTools is absent", () => {
    const agent = new ShellAgent();
    agent._initHasShell();
    // No _tools property on plain ShellAgent
    expect("_tools" in agent).toBe(false);
  });

  it("_ensureHasShell is idempotent", () => {
    const agent = new ShellAgent();
    agent._ensureHasShell();
    const shell1 = agent._shell;
    agent._ensureHasShell();
    const shell2 = agent._shell;
    expect(shell1).toBe(shell2);
  });
});
