<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitDriver;
use AgentHarness\BashkitIPCDriver;
use AgentHarness\BashkitNativeDriver;
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

        BashkitDriver::resolve(nativeLib: null, cliPath: null);
    }

    public function testResolvePrefersNativeOverIpc(): void
    {
        $mockLib = new class {
            public function bashkit_create(?string $c): object { return new \stdClass(); }
            public function bashkit_destroy(object $c): void {}
            public function bashkit_exec(object $c, string $r): string { return '{}'; }
            public function bashkit_register_command(object $c, string $n, \Closure $cb, mixed $u): void {}
            public function bashkit_unregister_command(object $c, string $n): void {}
            public function bashkit_free_string(string $s): void {}
        };
        $driver = BashkitDriver::resolve(nativeLib: $mockLib);
        $this->assertInstanceOf(BashkitNativeDriver::class, $driver);
    }

    public function testResolveFallsBackToIpc(): void
    {
        $fake = new BashkitDriverFakeProcess();
        $driver = BashkitDriver::resolve(nativeLib: null, cliPath: '/usr/bin/bashkit-cli', processOverride: $fake);
        $this->assertInstanceOf(BashkitIPCDriver::class, $driver);
    }

    public function testResolveThrowsWhenNothingAvailable(): void
    {
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('bashkit not found');
        BashkitDriver::resolve(nativeLib: null, cliPath: null);
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
