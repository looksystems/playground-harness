<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\ExecResult;
use AgentHarness\HasHooks;
use AgentHarness\HasShell;
use AgentHarness\HookEvent;
use AgentHarness\Shell;
use AgentHarness\ShellRegistry;
use AgentHarness\StandardAgent;
use AgentHarness\ToolDef;
use AgentHarness\UsesTools;
use AgentHarness\VirtualFS;
use PHPUnit\Framework\TestCase;

/**
 * Minimal class using only HasShell (no UsesTools).
 */
class ShellOnly
{
    use HasShell;
}

/**
 * Class using both HasShell and UsesTools for auto-registration test.
 */
class ShellWithTools
{
    use HasShell;
    use UsesTools;
}

/**
 * Class using HasHooks, HasShell, and UsesTools for hook tests.
 */
class HookShellAgent
{
    use HasHooks;
    use HasShell;
    use UsesTools;
}

class HasShellTest extends TestCase
{
    protected function tearDown(): void
    {
        ShellRegistry::reset();
    }

    public function testLazyInitialization(): void
    {
        $obj = new ShellOnly();
        // shell() should auto-init
        $shell = $obj->shell();
        $this->assertInstanceOf(Shell::class, $shell);
    }

    public function testExecCommand(): void
    {
        $obj = new ShellOnly();
        $obj->initHasShell();
        $obj->fs()->write('/home/user/test.txt', "hello\n");
        $result = $obj->execCommand('cat test.txt');
        $this->assertSame("hello\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
    }

    public function testFsAccess(): void
    {
        $obj = new ShellOnly();
        $obj->initHasShell();
        $obj->fs()->write('/home/user/file.txt', 'content');
        $this->assertSame('content', $obj->fs()->read('/home/user/file.txt'));
    }

    public function testInitWithShellInstance(): void
    {
        $fs = new VirtualFS(['/data/file.txt' => 'data']);
        $shell = new Shell(fs: $fs, cwd: '/data');
        $obj = new ShellOnly();
        $obj->initHasShell(shell: $shell);
        $result = $obj->execCommand('cat file.txt');
        $this->assertSame('data', $result->stdout);
    }

    public function testInitWithRegistryName(): void
    {
        $fs = new VirtualFS(['/tmp/test.txt' => 'registered']);
        $shell = new Shell(fs: $fs, cwd: '/tmp');
        ShellRegistry::register('myshell', $shell);

        $obj = new ShellOnly();
        $obj->initHasShell(shell: 'myshell');
        $result = $obj->execCommand('cat test.txt');
        $this->assertSame('registered', $result->stdout);
    }

    public function testInitWithCustomCwd(): void
    {
        $obj = new ShellOnly();
        $obj->initHasShell(cwd: '/tmp');
        $result = $obj->execCommand('pwd');
        $this->assertSame("/tmp\n", $result->stdout);
    }

    public function testInitWithEnv(): void
    {
        $obj = new ShellOnly();
        $obj->initHasShell(env: ['NAME' => 'World']);
        $result = $obj->execCommand('echo Hello $NAME');
        $this->assertSame("Hello World\n", $result->stdout);
    }

    public function testAutoRegistersExecTool(): void
    {
        $obj = new ShellWithTools();
        $obj->initHasShell();

        $schema = $obj->toolsSchema();
        $this->assertCount(1, $schema);
        $this->assertSame('exec', $schema[0]['function']['name']);
        $this->assertStringContainsString('bash command', $schema[0]['function']['description']);
    }

    public function testExecToolExecution(): void
    {
        $obj = new ShellWithTools();
        $obj->initHasShell();
        $obj->fs()->write('/home/user/test.txt', 'hello world');

        $result = $obj->executeTool('exec', ['command' => 'cat test.txt']);
        $decoded = json_decode($result, true);
        $this->assertStringContainsString('hello world', $decoded);
    }

    public function testNoAutoRegisterWithoutUsesTools(): void
    {
        // ShellOnly does not use UsesTools, so no tool should be registered
        $obj = new ShellOnly();
        $obj->initHasShell();
        $this->assertFalse(method_exists($obj, 'toolsSchema'));
    }

    public function testStandardAgentHasShell(): void
    {
        $agent = new StandardAgent(model: 'test-model');
        $agent->initHasShell();
        $result = $agent->execCommand('echo "from agent"');
        $this->assertSame("from agent\n", $result->stdout);

        // Should have auto-registered exec tool
        $schema = $agent->toolsSchema();
        $execTools = array_filter($schema, fn($s) => $s['function']['name'] === 'exec');
        $this->assertCount(1, $execTools);
    }

    public function testRegistryReturnsClone(): void
    {
        $fs = new VirtualFS(['/home/user/file.txt' => 'original']);
        $shell = new Shell(fs: $fs, cwd: '/home/user');
        ShellRegistry::register('test', $shell);

        $obj = new ShellOnly();
        $obj->initHasShell(shell: 'test');
        $obj->execCommand('touch new.txt');

        // Original shell should be unmodified
        $this->assertFalse($fs->exists('/home/user/new.txt'));
    }

    public function testExecToolErrorOutput(): void
    {
        $obj = new ShellWithTools();
        $obj->initHasShell();

        $result = $obj->executeTool('exec', ['command' => 'cat nonexistent.txt']);
        $decoded = json_decode($result, true);
        $this->assertStringContainsString('stderr', $decoded);
    }

    public function testExecToolNoOutput(): void
    {
        $obj = new ShellWithTools();
        $obj->initHasShell();

        $result = $obj->executeTool('exec', ['command' => 'touch newfile.txt']);
        $decoded = json_decode($result, true);
        $this->assertSame('(no output)', $decoded);
    }

    public function testShellCallHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $calls = [];
        $obj->on(HookEvent::ShellCall, function (string $cmd) use (&$calls) {
            $calls[] = $cmd;
        });
        $obj->execCommand('echo hello');
        $this->assertSame(['echo hello'], $calls);
    }

    public function testShellResultHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $results = [];
        $obj->on(HookEvent::ShellResult, function (string $cmd, ExecResult $result) use (&$results) {
            $results[] = ['cmd' => $cmd, 'exitCode' => $result->exitCode];
        });
        $obj->execCommand('echo hello');
        $this->assertSame([['cmd' => 'echo hello', 'exitCode' => 0]], $results);
    }

    public function testShellNotFoundHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $notFound = [];
        $obj->on(HookEvent::ShellNotFound, function (string $name) use (&$notFound) {
            $notFound[] = $name;
        });
        $obj->execCommand('nonexistent arg1');
        $this->assertSame(['nonexistent'], $notFound);
    }

    public function testShellNotFoundInPipeline(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $notFound = [];
        $obj->on(HookEvent::ShellNotFound, function (string $name) use (&$notFound) {
            $notFound[] = $name;
        });
        $obj->execCommand('echo hi | bogus');
        $this->assertSame(['bogus'], $notFound);
    }

    public function testShellCwdHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $obj->fs()->write('/tmp/.keep', '');
        $cwdChanges = [];
        $obj->on(HookEvent::ShellCwd, function (string $old, string $new) use (&$cwdChanges) {
            $cwdChanges[] = ['old' => $old, 'new' => $new];
        });
        $obj->execCommand('cd /tmp');
        $this->assertCount(1, $cwdChanges);
        $this->assertSame('/tmp', $cwdChanges[0]['new']);
    }

    public function testNoCwdHookWhenUnchanged(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $cwdChanges = [];
        $obj->on(HookEvent::ShellCwd, function (string $old, string $new) use (&$cwdChanges) {
            $cwdChanges[] = ['old' => $old, 'new' => $new];
        });
        $obj->execCommand('echo hello');
        $this->assertEmpty($cwdChanges);
    }

    public function testHooksDontFireWithoutHasHooks(): void
    {
        $obj = new ShellOnly();
        $obj->initHasShell();
        // Should not throw
        $obj->execCommand('echo hello');
        $this->assertTrue(true);
    }

    public function testCommandRegisterHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $registered = [];
        $obj->on(HookEvent::CommandRegister, function (string $name) use (&$registered) {
            $registered[] = $name;
        });
        $obj->registerCommand('mycmd', function (array $args, string $stdin): ExecResult {
            return new ExecResult(stdout: "ok\n");
        });
        $this->assertSame(['mycmd'], $registered);
    }

    public function testCommandUnregisterHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $obj->registerCommand('mycmd', function (array $args, string $stdin): ExecResult {
            return new ExecResult(stdout: "ok\n");
        });
        $unregistered = [];
        $obj->on(HookEvent::CommandUnregister, function (string $name) use (&$unregistered) {
            $unregistered[] = $name;
        });
        $obj->unregisterCommand('mycmd');
        $this->assertSame(['mycmd'], $unregistered);
    }

    public function testToolRegisterHook(): void
    {
        $obj = new HookShellAgent();
        $obj->initHasShell();
        $registered = [];
        $obj->on(HookEvent::ToolRegister, function (ToolDef $tool) use (&$registered) {
            $registered[] = $tool->name;
        });
        $obj->registerTool(ToolDef::make(
            name: 'test_tool',
            description: 'test',
            parameters: ['type' => 'object', 'properties' => new \stdClass()],
            execute: fn(array $args) => 'ok',
        ));
        $this->assertContains('test_tool', $registered);
    }
}
