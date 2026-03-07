<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitDriver;
use AgentHarness\BashkitIPCDriver;
use AgentHarness\ShellDriverFactory;
use AgentHarness\ShellDriverInterface;
use PHPUnit\Framework\TestCase;

/**
 * Minimal fake process so BashkitIPCDriver doesn't spawn a real process.
 */
class BashkitDriverFakeProcess
{
    public function write(string $data): void {}
    public function readline(): ?string { return null; }
}

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

    public function testResolveReturnsIPCDriverWhenCliAvailable(): void
    {
        $fake = new BashkitDriverFakeProcess();
        $driver = BashkitDriver::resolve(cliPath: '/usr/local/bin/bashkit-cli', processOverride: $fake);

        $this->assertInstanceOf(BashkitIPCDriver::class, $driver);
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testResolveThrowsWhenCliNotAvailable(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');

        BashkitDriver::resolve(cliPath: null);
    }

    // -----------------------------------------------------------------------
    // register() + factory integration
    // -----------------------------------------------------------------------

    public function testRegisterAddsToFactory(): void
    {
        BashkitDriver::register();

        // After registration, create should throw RuntimeError (not "not registered")
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
