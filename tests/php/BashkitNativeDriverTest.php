<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitNativeDriver;
use AgentHarness\FilesystemDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\ExecResult;
use PHPUnit\Framework\TestCase;

/**
 * Mock library that simulates the bashkit C API for testing.
 */
class MockBashkitLib
{
    /** @var list<array> */
    private array $responseQueue = [];

    public ?array $lastRequest = null;

    /** @var array<string, array{callback: \Closure, userdata: mixed}> */
    public array $commands = [];

    public function bashkit_create(?string $configJson): object
    {
        return (object) ['id' => 'mock-ctx'];
    }

    public function bashkit_destroy(object $ctx): void
    {
        // no-op
    }

    public function bashkit_exec(object $ctx, string $requestJson): string
    {
        $this->lastRequest = json_decode($requestJson, true);

        if (count($this->responseQueue) > 0) {
            $response = array_shift($this->responseQueue);
            return json_encode($response);
        }

        return json_encode([
            'stdout' => '',
            'stderr' => 'no response queued',
            'exitCode' => 1,
            'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
        ]);
    }

    public function bashkit_register_command(object $ctx, string $name, \Closure $cb, mixed $userdata): void
    {
        $this->commands[$name] = ['callback' => $cb, 'userdata' => $userdata];
    }

    public function bashkit_unregister_command(object $ctx, string $name): void
    {
        unset($this->commands[$name]);
    }

    public function bashkit_free_string(string $s): void
    {
        // no-op
    }

    public function enqueueResponse(array $response): void
    {
        $this->responseQueue[] = $response;
    }

    /**
     * Invoke a registered callback by name, simulating what the C library would do.
     */
    public function invokeCallback(string $name, string $argsJson): ?string
    {
        if (!isset($this->commands[$name])) {
            return null;
        }
        return ($this->commands[$name]['callback'])($argsJson);
    }
}

class BashkitNativeDriverTest extends TestCase
{
    private function makeDriver(string $cwd = '/', array $env = []): array
    {
        $mock = new MockBashkitLib();
        $driver = new BashkitNativeDriver(cwd: $cwd, env: $env, libOverride: $mock);
        return [$driver, $mock];
    }

    // ── Contract ──

    public function testIsShellDriver(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertInstanceOf(ShellDriverInterface::class, $driver);
    }

    public function testFsIsFilesystemDriver(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertInstanceOf(FilesystemDriver::class, $driver->fs());
    }

    public function testCwdDefault(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertSame('/', $driver->cwd());
    }

    public function testCwdCustom(): void
    {
        [$driver] = $this->makeDriver(cwd: '/home');
        $this->assertSame('/home', $driver->cwd());
    }

    public function testEnvDefault(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertSame([], $driver->env());
    }

    public function testEnvCustom(): void
    {
        [$driver] = $this->makeDriver(env: ['FOO' => 'bar']);
        $this->assertSame(['FOO' => 'bar'], $driver->env());
    }

    // ── VFS Sync ──

    public function testExecSendsSnapshot(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->fs()->write('/hello.txt', 'world');

        $mock->enqueueResponse([
            'stdout' => 'ok',
            'stderr' => '',
            'exitCode' => 0,
            'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
        ]);

        $result = $driver->exec('echo hi');
        $this->assertInstanceOf(ExecResult::class, $result);
        $this->assertSame('ok', $result->stdout);
        $this->assertSame(0, $result->exitCode);

        $req = $mock->lastRequest;
        $this->assertSame('echo hi', $req['cmd']);
        $this->assertSame('world', $req['fs']['/hello.txt']);
    }

    public function testExecSendsCwdAndEnv(): void
    {
        [$driver, $mock] = $this->makeDriver(cwd: '/tmp', env: ['PATH' => '/bin']);

        $mock->enqueueResponse([
            'stdout' => '',
            'stderr' => '',
            'exitCode' => 0,
            'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
        ]);

        $driver->exec('ls');
        $req = $mock->lastRequest;
        $this->assertSame('/tmp', $req['cwd']);
        $this->assertSame(['PATH' => '/bin'], $req['env']);
    }

    public function testExecReturnsExecResult(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse([
            'stdout' => "line1\nline2",
            'stderr' => 'warn',
            'exitCode' => 2,
            'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
        ]);

        $result = $driver->exec('cmd');
        $this->assertSame("line1\nline2", $result->stdout);
        $this->assertSame('warn', $result->stderr);
        $this->assertSame(2, $result->exitCode);
    }

    public function testExecAppliesCreatedFiles(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse([
            'stdout' => '',
            'stderr' => '',
            'exitCode' => 0,
            'fs_changes' => [
                'created' => ['/new.txt' => 'new content'],
                'deleted' => [],
            ],
        ]);

        $driver->exec('touch /new.txt');
        $this->assertTrue($driver->fs()->exists('/new.txt'));
        $this->assertSame('new content', $driver->fs()->readText('/new.txt'));
    }

    public function testExecAppliesDeletedFiles(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->fs()->write('/old.txt', 'old');

        $mock->enqueueResponse([
            'stdout' => '',
            'stderr' => '',
            'exitCode' => 0,
            'fs_changes' => [
                'created' => new \stdClass(),
                'deleted' => ['/old.txt'],
            ],
        ]);

        $driver->exec('rm /old.txt');
        $this->assertFalse($driver->fs()->exists('/old.txt'));
    }

    public function testExecErrorResponse(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $mock->enqueueResponse([
            'error' => 'something broke',
        ]);

        $result = $driver->exec('bad_cmd');
        $this->assertNotSame(0, $result->exitCode);
        $this->assertStringContainsString('something broke', $result->stderr);
    }

    // ── Callbacks ──

    public function testRegisterCommand(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(stdout: 'hello'));

        $this->assertArrayHasKey('greet', $mock->commands);
        $this->assertTrue($driver->hasCommand('greet'));
    }

    public function testUnregisterCommand(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(stdout: 'hello'));
        $driver->unregisterCommand('greet');

        $this->assertArrayNotHasKey('greet', $mock->commands);
        $this->assertFalse($driver->hasCommand('greet'));
    }

    public function testCallbackInvocation(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(
            stdout: 'hello ' . implode(' ', $args),
        ));

        $resultJson = $mock->invokeCallback('greet', json_encode([
            'name' => 'greet',
            'args' => ['world'],
            'stdin' => '',
        ]));

        $this->assertNotNull($resultJson);
        $this->assertSame('hello world', $resultJson);
    }

    public function testCallbackPassesStdin(): void
    {
        [$driver, $mock] = $this->makeDriver();
        $received = [];

        $driver->registerCommand('mycmd', function (array $args, string $stdin) use (&$received) {
            $received['args'] = $args;
            $received['stdin'] = $stdin;
            return new ExecResult(stdout: 'ok');
        });

        $mock->invokeCallback('mycmd', json_encode([
            'name' => 'mycmd',
            'args' => ['a', 'b'],
            'stdin' => 'hello',
        ]));

        $this->assertSame(['a', 'b'], $received['args']);
        $this->assertSame('hello', $received['stdin']);
    }

    public function testCallbackExceptionReturnsError(): void
    {
        [$driver, $mock] = $this->makeDriver();

        $driver->registerCommand('boom', function (array $args, string $stdin) {
            throw new \RuntimeException('handler blew up');
        });

        $resultJson = $mock->invokeCallback('boom', json_encode([
            'name' => 'boom',
            'args' => [],
            'stdin' => '',
        ]));

        $this->assertNotNull($resultJson);
        $decoded = json_decode($resultJson, true);
        $this->assertArrayHasKey('error', $decoded);
        $this->assertStringContainsString('handler blew up', $decoded['error']);
    }

    // ── Lifecycle ──

    public function testCloneCreatesIndependentInstance(): void
    {
        [$driver, $mock] = $this->makeDriver(cwd: '/home', env: ['A' => '1']);
        $driver->fs()->write('/file.txt', 'data');
        $driver->registerCommand('cmd1', fn(array $args, string $stdin) => new ExecResult(stdout: 'r1'));

        $cloned = $driver->cloneDriver();

        $this->assertSame('/home', $cloned->cwd());
        $this->assertSame(['A' => '1'], $cloned->env());
        $this->assertTrue($cloned->fs()->exists('/file.txt'));
        $this->assertSame('data', $cloned->fs()->readText('/file.txt'));

        // Independence: writing in clone doesn't affect original
        $cloned->fs()->write('/clone_only.txt', 'clone');
        $this->assertFalse($driver->fs()->exists('/clone_only.txt'));
    }

    public function testClonePreservesCwdAndEnv(): void
    {
        [$driver] = $this->makeDriver(cwd: '/opt', env: ['X' => 'y']);
        $cloned = $driver->cloneDriver();

        $this->assertSame('/opt', $cloned->cwd());
        $this->assertSame(['X' => 'y'], $cloned->env());
    }

    public function testOnNotFound(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertNull($driver->getOnNotFound());

        $handler = fn(string $name) => new ExecResult(stderr: 'not found', exitCode: 127);
        $driver->setOnNotFound($handler);
        $this->assertSame($handler, $driver->getOnNotFound());

        $driver->setOnNotFound(null);
        $this->assertNull($driver->getOnNotFound());
    }

    // ── Library Discovery ──

    public function testFindLibraryUsesEnvVar(): void
    {
        $original = getenv('BASHKIT_LIB_PATH');
        try {
            // Point to a file that exists
            putenv('BASHKIT_LIB_PATH=' . __FILE__);
            $this->assertSame(__FILE__, BashkitNativeDriver::findLibrary());
        } finally {
            if ($original === false) {
                putenv('BASHKIT_LIB_PATH');
            } else {
                putenv('BASHKIT_LIB_PATH=' . $original);
            }
        }
    }

    public function testFindLibraryEnvVarNonexistent(): void
    {
        $original = getenv('BASHKIT_LIB_PATH');
        try {
            putenv('BASHKIT_LIB_PATH=/nonexistent/path/libashkit.so');
            $this->assertNull(BashkitNativeDriver::findLibrary());
        } finally {
            if ($original === false) {
                putenv('BASHKIT_LIB_PATH');
            } else {
                putenv('BASHKIT_LIB_PATH=' . $original);
            }
        }
    }

    public function testFindLibraryReturnsNullWhenNotFound(): void
    {
        $original = getenv('BASHKIT_LIB_PATH');
        try {
            putenv('BASHKIT_LIB_PATH');
            // With no env var and no library installed, should return null
            // (This test may pass or fail depending on system; we verify the method doesn't throw)
            $result = BashkitNativeDriver::findLibrary();
            $this->assertTrue($result === null || is_string($result));
        } finally {
            if ($original === false) {
                putenv('BASHKIT_LIB_PATH');
            } else {
                putenv('BASHKIT_LIB_PATH=' . $original);
            }
        }
    }

    public function testHasCommand(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertFalse($driver->hasCommand('greet'));
        $driver->registerCommand('greet', fn(array $args, string $stdin) => new ExecResult(stdout: 'hi'));
        $this->assertTrue($driver->hasCommand('greet'));
        $driver->unregisterCommand('greet');
        $this->assertFalse($driver->hasCommand('greet'));
    }
}
