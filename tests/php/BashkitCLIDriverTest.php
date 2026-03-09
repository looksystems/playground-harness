<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitCLIDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\FilesystemDriver;
use PHPUnit\Framework\TestCase;

class BashkitCLIDriverTest extends TestCase
{
    private function createDriver(array $responses = []): BashkitCLIDriver
    {
        $default = ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
        return new BashkitCLIDriver(
            execOverride: function (string $cmd) use ($responses, $default): array {
                return $responses[$cmd] ?? $default;
            },
        );
    }

    public function testExecReturnsResult(): void
    {
        $driver = $this->createDriver([
            'echo hello' => ['stdout' => "hello\n", 'stderr' => '', 'exitCode' => 0],
        ]);
        $result = $driver->exec('echo hello');
        $this->assertSame("hello\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
    }

    public function testImplementsInterface(): void
    {
        $driver = $this->createDriver();
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testFsReturnsFilesystemDriver(): void
    {
        $driver = $this->createDriver();
        $this->assertInstanceOf(FilesystemDriver::class, $driver->fs());
    }

    public function testCwdDefaultsToRoot(): void
    {
        $driver = $this->createDriver();
        $this->assertSame('/', $driver->cwd());
    }

    public function testEnvDefaultsToEmpty(): void
    {
        $driver = $this->createDriver();
        $this->assertSame([], $driver->env());
    }

    public function testCustomCwdAndEnv(): void
    {
        $driver = new BashkitCLIDriver(
            cwd: '/tmp',
            env: ['FOO' => 'bar'],
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $this->assertSame('/tmp', $driver->cwd());
        $this->assertSame(['FOO' => 'bar'], $driver->env());
    }

    public function testRegisterAndUnregisterCommand(): void
    {
        $driver = $this->createDriver();
        $handler = fn($args, $stdin) => 'ok';
        $driver->registerCommand('mycmd', $handler);
        $this->assertTrue($driver->hasCommand('mycmd'));
        $driver->unregisterCommand('mycmd');
        $this->assertFalse($driver->hasCommand('mycmd'));
    }

    public function testCloneCreatesIndependentCopy(): void
    {
        $driver = $this->createDriver();
        $driver->fs()->write('/file.txt', 'content');
        $cloned = $driver->cloneDriver();
        $this->assertTrue($cloned->fs()->exists('/file.txt'));
        $cloned->fs()->write('/other.txt', 'data');
        $this->assertFalse($driver->fs()->exists('/other.txt'));
    }

    public function testOnNotFound(): void
    {
        $driver = $this->createDriver();
        $this->assertNull($driver->getOnNotFound());
        $cb = fn() => null;
        $driver->setOnNotFound($cb);
        $this->assertSame($cb, $driver->getOnNotFound());
    }

    public function testExecWithStderr(): void
    {
        $driver = $this->createDriver([
            'bad_cmd' => ['stdout' => '', 'stderr' => 'not found', 'exitCode' => 127],
        ]);
        $result = $driver->exec('bad_cmd');
        $this->assertSame('not found', $result->stderr);
        $this->assertSame(127, $result->exitCode);
    }
}
