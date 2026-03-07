<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitIPCDriver;
use AgentHarness\FilesystemDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\ExecResult;
use PHPUnit\Framework\TestCase;

/**
 * Fake process for testing BashkitIPCDriver without a real bashkit-cli.
 */
class FakeProcess
{
    /** @var list<string> */
    private array $writes = [];

    /** @var list<string> */
    private array $responseLines = [];

    private int $readIndex = 0;

    public function write(string $data): void
    {
        $this->writes[] = $data;
    }

    public function readline(): ?string
    {
        if ($this->readIndex < count($this->responseLines)) {
            return $this->responseLines[$this->readIndex++];
        }
        return null;
    }

    public function enqueueResponse(array $obj): void
    {
        $this->responseLines[] = json_encode($obj) . "\n";
    }

    public function lastRequest(): ?array
    {
        for ($i = count($this->writes) - 1; $i >= 0; $i--) {
            $line = trim($this->writes[$i]);
            if ($line !== '') {
                return json_decode($line, true);
            }
        }
        return null;
    }

    /** @return list<array> */
    public function allRequests(): array
    {
        $results = [];
        foreach ($this->writes as $raw) {
            $line = trim($raw);
            if ($line !== '') {
                $results[] = json_decode($line, true);
            }
        }
        return $results;
    }
}

class BashkitIPCDriverTest extends TestCase
{
    private function makeDriver(string $cwd = '/', array $env = []): array
    {
        $fake = new FakeProcess();
        $driver = new BashkitIPCDriver(cwd: $cwd, env: $env, processOverride: $fake);
        return [$driver, $fake];
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
        [$driver, $fake] = $this->makeDriver();
        $driver->fs()->write('/hello.txt', 'world');

        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => 'ok',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $result = $driver->exec('echo hi');
        $this->assertInstanceOf(ExecResult::class, $result);
        $this->assertSame('ok', $result->stdout);
        $this->assertSame(0, $result->exitCode);

        $req = $fake->lastRequest();
        $this->assertSame('exec', $req['method']);
        $this->assertSame('echo hi', $req['params']['cmd']);
        $this->assertSame('world', $req['params']['fs']['/hello.txt']);
    }

    public function testExecReturnsExecResult(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => "line1\nline2",
                'stderr' => 'warn',
                'exitCode' => 2,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $result = $driver->exec('cmd');
        $this->assertSame("line1\nline2", $result->stdout);
        $this->assertSame('warn', $result->stderr);
        $this->assertSame(2, $result->exitCode);
    }

    public function testExecAppliesCreatedFiles(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => [
                    'created' => ['/new.txt' => 'new content'],
                    'deleted' => [],
                ],
            ],
        ]);

        $driver->exec('touch /new.txt');
        $this->assertTrue($driver->fs()->exists('/new.txt'));
        $this->assertSame('new content', $driver->fs()->readText('/new.txt'));
    }

    public function testExecAppliesDeletedFiles(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->fs()->write('/old.txt', 'old');

        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => [
                    'created' => new \stdClass(),
                    'deleted' => ['/old.txt'],
                ],
            ],
        ]);

        $driver->exec('rm /old.txt');
        $this->assertFalse($driver->fs()->exists('/old.txt'));
    }

    public function testExecResolvesLazyFilesInSnapshot(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->fs()->writeLazy('/lazy.txt', fn() => 'lazy content');

        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $driver->exec('cat /lazy.txt');
        $req = $fake->lastRequest();
        $this->assertSame('lazy content', $req['params']['fs']['/lazy.txt']);
    }

    public function testExecSendsCwdAndEnv(): void
    {
        [$driver, $fake] = $this->makeDriver(cwd: '/tmp', env: ['PATH' => '/bin']);

        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $driver->exec('ls');
        $req = $fake->lastRequest();
        $this->assertSame('/tmp', $req['params']['cwd']);
        $this->assertSame(['PATH' => '/bin'], $req['params']['env']);
    }

    // ── Callbacks ──

    public function testRegisterCommandSendsNotification(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => 'hello');

        $reqs = $fake->allRequests();
        $regReqs = array_values(array_filter($reqs, fn($r) => ($r['method'] ?? '') === 'register_command'));
        $this->assertCount(1, $regReqs);
        $this->assertSame('greet', $regReqs[0]['params']['name']);
        $this->assertArrayNotHasKey('id', $regReqs[0]);
    }

    public function testInvokeCommandCallbackDuringExec(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => 'hello ' . implode(' ', $args));

        // Bashkit sends invoke_command callback, then final result
        $fake->enqueueResponse([
            'method' => 'invoke_command',
            'params' => ['name' => 'greet', 'args' => ['world']],
            'id' => 100,
        ]);
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => 'done',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $result = $driver->exec('greet world');
        $this->assertSame('done', $result->stdout);

        $reqs = $fake->allRequests();
        $callbackResponses = array_values(array_filter(
            $reqs,
            fn($r) => isset($r['result']) && ($r['id'] ?? null) === 100
        ));
        $this->assertCount(1, $callbackResponses);
        $this->assertSame('hello world', $callbackResponses[0]['result']);
    }

    public function testUnregisterCommandSendsNotification(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => 'hello');
        $driver->unregisterCommand('greet');

        $reqs = $fake->allRequests();
        $unregReqs = array_values(array_filter($reqs, fn($r) => ($r['method'] ?? '') === 'unregister_command'));
        $this->assertCount(1, $unregReqs);
        $this->assertSame('greet', $unregReqs[0]['params']['name']);
        $this->assertArrayNotHasKey('id', $unregReqs[0]);
    }

    public function testUnregisterCommandRemovesHandler(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $driver->registerCommand('greet', fn(array $args, string $stdin) => 'hello');
        $driver->unregisterCommand('greet');

        $fake->enqueueResponse([
            'method' => 'invoke_command',
            'params' => ['name' => 'greet', 'args' => []],
            'id' => 101,
        ]);
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $driver->exec('greet');
        $reqs = $fake->allRequests();
        $errorResponses = array_values(array_filter(
            $reqs,
            fn($r) => isset($r['error']) && ($r['id'] ?? null) === 101
        ));
        $this->assertCount(1, $errorResponses);
    }

    public function testCallbackPassesStdinToHandler(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $received = [];

        $driver->registerCommand('mycmd', function (array $args, string $stdin) use (&$received) {
            $received['args'] = $args;
            $received['stdin'] = $stdin;
            return 'ok';
        });

        $fake->enqueueResponse([
            'method' => 'invoke_command',
            'params' => ['name' => 'mycmd', 'args' => ['a', 'b'], 'stdin' => 'hello'],
            'id' => 200,
        ]);
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => 'done',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $driver->exec('mycmd a b');
        $this->assertSame(['a', 'b'], $received['args']);
        $this->assertSame('hello', $received['stdin']);
    }

    public function testCallbackHandlerExceptionReturnsError(): void
    {
        [$driver, $fake] = $this->makeDriver();

        $driver->registerCommand('boom', function (array $args, string $stdin) {
            throw new \RuntimeException('handler blew up');
        });

        $fake->enqueueResponse([
            'method' => 'invoke_command',
            'params' => ['name' => 'boom', 'args' => []],
            'id' => 300,
        ]);
        $fake->enqueueResponse([
            'id' => 1,
            'result' => [
                'stdout' => '',
                'stderr' => '',
                'exitCode' => 0,
                'fs_changes' => ['created' => new \stdClass(), 'deleted' => []],
            ],
        ]);

        $result = $driver->exec('boom');
        $this->assertSame(0, $result->exitCode);

        $reqs = $fake->allRequests();
        $errorResponses = array_values(array_filter(
            $reqs,
            fn($r) => isset($r['error']) && ($r['id'] ?? null) === 300
        ));
        $this->assertCount(1, $errorResponses);
        $this->assertStringContainsString('handler blew up', $errorResponses[0]['error']['message']);
    }

    // ── Lifecycle ──

    public function testCloneCreatesIndependentInstance(): void
    {
        [$driver, $fake] = $this->makeDriver(cwd: '/home', env: ['A' => '1']);
        $driver->fs()->write('/file.txt', 'data');
        $driver->registerCommand('cmd1', fn(array $args, string $stdin) => 'r1');

        $fake2 = new FakeProcess();
        $cloned = $driver->cloneDriver($fake2);

        $this->assertSame('/home', $cloned->cwd());
        $this->assertSame(['A' => '1'], $cloned->env());
        $this->assertTrue($cloned->fs()->exists('/file.txt'));
        $this->assertSame('data', $cloned->fs()->readText('/file.txt'));

        // Independence
        $cloned->fs()->write('/clone_only.txt', 'clone');
        $this->assertFalse($driver->fs()->exists('/clone_only.txt'));

        // Clone should re-register commands
        $reqs = $fake2->allRequests();
        $regReqs = array_values(array_filter($reqs, fn($r) => ($r['method'] ?? '') === 'register_command'));
        $this->assertCount(1, $regReqs);
        $this->assertSame('cmd1', $regReqs[0]['params']['name']);
        $this->assertArrayNotHasKey('id', $regReqs[0]);
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

    public function testErrorResponseReturnsExecResultWithStderr(): void
    {
        [$driver, $fake] = $this->makeDriver();
        $fake->enqueueResponse([
            'id' => 1,
            'error' => ['code' => -1, 'message' => 'something broke'],
        ]);

        $result = $driver->exec('bad_cmd');
        $this->assertNotSame(0, $result->exitCode);
        $this->assertStringContainsString('something broke', $result->stderr);
    }

    public function testHasCommand(): void
    {
        [$driver] = $this->makeDriver();
        $this->assertFalse($driver->hasCommand('greet'));
        $driver->registerCommand('greet', fn(array $args, string $stdin) => 'hi');
        $this->assertTrue($driver->hasCommand('greet'));
        $driver->unregisterCommand('greet');
        $this->assertFalse($driver->hasCommand('greet'));
    }
}
