<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\HasShell;
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
}
