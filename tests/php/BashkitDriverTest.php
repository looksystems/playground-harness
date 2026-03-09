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
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');

        BashkitDriver::resolve(cliPath: null);
    }

    public function testResolveReturnsCLIDriverWhenCliPathProvided(): void
    {
        $driver = BashkitDriver::resolve(cliPath: '/usr/local/bin/bashkit');
        $this->assertInstanceOf(BashkitCLIDriver::class, $driver);
    }

    // -----------------------------------------------------------------------
    // register() + factory integration
    // -----------------------------------------------------------------------

    public function testRegisterAddsToFactory(): void
    {
        BashkitDriver::register();

        // After registration, create should throw "bashkit not found" (not "not registered")
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');

        ShellDriverFactory::create('bashkit');
    }

    public function testFactoryCreateWithoutRegistrationThrowsNotRegistered(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('not registered');

        ShellDriverFactory::create('bashkit');
    }

    public function testFactoryCreateAfterRegisterThrowsBashkitNotFound(): void
    {
        BashkitDriver::register();

        // Should throw "bashkit not found", not "not registered"
        try {
            ShellDriverFactory::create('bashkit');
            $this->fail('Expected RuntimeException');
        } catch (\RuntimeException $e) {
            $this->assertStringContainsString('bashkit not found', $e->getMessage());
            $this->assertStringNotContainsString('not registered', $e->getMessage());
        }
    }
}
