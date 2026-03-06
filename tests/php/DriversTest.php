<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\FilesystemDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\BuiltinFilesystemDriver;
use AgentHarness\BuiltinShellDriver;
use AgentHarness\ShellDriverFactory;
use AgentHarness\ExecResult;
use PHPUnit\Framework\TestCase;

class DriversTest extends TestCase
{
    protected function setUp(): void
    {
        ShellDriverFactory::reset();
    }

    public function testBuiltinFsWriteAndRead(): void
    {
        $fs = new BuiltinFilesystemDriver();
        $fs->write('/test.txt', 'hello');
        $this->assertSame('hello', $fs->read('/test.txt'));
    }

    public function testBuiltinFsImplementsInterface(): void
    {
        $fs = new BuiltinFilesystemDriver();
        $this->assertInstanceOf(FilesystemDriver::class, $fs);
    }

    public function testBuiltinShellImplementsInterface(): void
    {
        $driver = new BuiltinShellDriver();
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testBuiltinShellExec(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->fs()->write('/test.txt', 'hello');
        $result = $driver->exec('cat /test.txt');
        $this->assertSame('hello', $result->stdout);
    }

    public function testBuiltinShellRegisterCommand(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->registerCommand('greet', function (array $args, string $stdin): ExecResult {
            return new ExecResult(stdout: "hi\n");
        });
        $result = $driver->exec('greet');
        $this->assertSame("hi\n", $result->stdout);
    }

    public function testBuiltinShellClone(): void
    {
        $driver = new BuiltinShellDriver();
        $driver->fs()->write('/a.txt', 'a');
        $cloned = $driver->cloneDriver();
        $cloned->fs()->write('/b.txt', 'b');
        $this->assertFalse($driver->fs()->exists('/b.txt'));
    }

    public function testFactoryDefaultIsBuiltin(): void
    {
        $driver = ShellDriverFactory::create();
        $this->assertInstanceOf(BuiltinShellDriver::class, $driver);
    }

    public function testFactoryUnknownThrows(): void
    {
        $this->expectException(\RuntimeException::class);
        ShellDriverFactory::create('nonexistent');
    }

    public function testFactoryRegisterCustom(): void
    {
        ShellDriverFactory::register('custom', function (array $opts): ShellDriverInterface {
            return new BuiltinShellDriver(...$opts);
        });
        $driver = ShellDriverFactory::create('custom');
        $this->assertInstanceOf(BuiltinShellDriver::class, $driver);
    }

    public function testOnNotFound(): void
    {
        $driver = new BuiltinShellDriver();
        $notFound = [];
        $driver->setOnNotFound(function (string $name) use (&$notFound) {
            $notFound[] = $name;
        });
        $driver->exec('boguscmd');
        $this->assertSame(['boguscmd'], $notFound);
    }
}
