<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitCLIDriver;
use AgentHarness\BashkitDriver;
use AgentHarness\ShellDriverFactory;
use AgentHarness\ShellDriverInterface;
use PHPUnit\Framework\TestCase;

class BashkitDriverTest extends TestCase
{
    private static bool $hasBashkit = false;

    public static function setUpBeforeClass(): void
    {
        exec('which bashkit 2>/dev/null', $output, $code);
        self::$hasBashkit = $code === 0;
    }

    protected function setUp(): void
    {
        ShellDriverFactory::reset();
    }

    protected function tearDown(): void
    {
        ShellDriverFactory::reset();
    }

    // -----------------------------------------------------------------------
    // resolve()
    // -----------------------------------------------------------------------

    public function testResolveWithExecOverrideReturnsCLIDriver(): void
    {
        $driver = BashkitDriver::resolve(
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $this->assertInstanceOf(BashkitCLIDriver::class, $driver);
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testResolveThrowsWhenBashkitNotAvailable(): void
    {
        if (self::$hasBashkit) {
            $this->markTestSkipped('bashkit is installed — cannot test "not found" path');
        }

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');

        BashkitDriver::resolve(cliPath: null);
    }

    public function testResolveReturnsCLIDriverWhenCliPathProvided(): void
    {
        $driver = BashkitDriver::resolve(cliPath: '/usr/local/bin/bashkit');
        $this->assertInstanceOf(BashkitCLIDriver::class, $driver);
    }

    public function testResolveReturnsCLIDriverWhenBashkitInstalled(): void
    {
        if (!self::$hasBashkit) {
            $this->markTestSkipped('bashkit not installed');
        }

        $driver = BashkitDriver::resolve();
        $this->assertInstanceOf(BashkitCLIDriver::class, $driver);
    }

    // -----------------------------------------------------------------------
    // register() + factory integration
    // -----------------------------------------------------------------------

    public function testRegisterAddsToFactory(): void
    {
        BashkitDriver::register();

        if (self::$hasBashkit) {
            $driver = ShellDriverFactory::create('bashkit');
            $this->assertInstanceOf(BashkitCLIDriver::class, $driver);
        } else {
            $this->expectException(\RuntimeException::class);
            $this->expectExceptionMessage('bashkit not found');
            ShellDriverFactory::create('bashkit');
        }
    }

    public function testFactoryCreateWithoutRegistrationThrowsNotRegistered(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('not registered');

        ShellDriverFactory::create('bashkit');
    }
}
