<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\OpenShellGrpcDriver;
use AgentHarness\ShellDriverInterface;
use AgentHarness\FilesystemDriver;
use PHPUnit\Framework\TestCase;

class OpenShellGrpcDriverTest extends TestCase
{
    /** Create a driver where sync-back is a no-op (no marker in output). */
    private function createDriver(array $responses = []): OpenShellGrpcDriver
    {
        $default = ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
        return new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use ($responses, $default): array {
                $baseCmd = preg_replace("/; printf '\\\\n__HARNESS_FS_SYNC_\\d+__\\\\n'.*/s", '', $cmd);
                $baseCmd = preg_replace("/^.*? && (?=echo |cat |bad_cmd|ls)/", '', $baseCmd);
                foreach ($responses as $key => $resp) {
                    if (str_starts_with($baseCmd, $key)) {
                        return ['stdout' => $resp['stdout'] ?? '', 'stderr' => $resp['stderr'] ?? '', 'exitCode' => $resp['exitCode'] ?? 0];
                    }
                }
                return $default;
            },
        );
    }

    private function createDriverWithSyncBack(array $syncFiles): OpenShellGrpcDriver
    {
        return new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use ($syncFiles): array {
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    $marker = $matches[0];
                    $lines = [];
                    foreach ($syncFiles as $path => $content) {
                        $lines[] = "===FILE:{$path}===";
                        $lines[] = base64_encode($content);
                    }
                    $syncData = implode("\n", $lines);
                    return ['stdout' => "output\n{$marker}\n{$syncData}", 'stderr' => '', 'exitCode' => 0];
                }
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
    }

    // --- Contract tests ---

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
        $driver = new OpenShellGrpcDriver(
            cwd: '/tmp',
            env: ['FOO' => 'bar'],
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $this->assertSame('/tmp', $driver->cwd());
        $this->assertSame(['FOO' => 'bar'], $driver->env());
    }

    // --- Exec tests ---

    public function testExecReturnsResult(): void
    {
        $driver = $this->createDriver([
            'echo hello' => ['stdout' => "hello\n", 'stderr' => '', 'exitCode' => 0],
        ]);
        $result = $driver->exec('echo hello');
        $this->assertSame("hello\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
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

    // --- VFS sync tests ---

    public function testDirtyFileSyncedAsPreamble(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    return ['stdout' => "\n{$matches[0]}\n", 'stderr' => '', 'exitCode' => 0];
                }
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->fs()->write('/hello.txt', 'world');
        $driver->exec('cat /hello.txt');
        $this->assertCount(1, $calls);
        $this->assertStringContainsString('base64', $calls[0]);
        $this->assertStringContainsString('/hello.txt', $calls[0]);
        $this->assertStringContainsString(base64_encode('world'), $calls[0]);
    }

    public function testNoPreambleWhenNoDirty(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    return ['stdout' => "\n{$matches[0]}\n", 'stderr' => '', 'exitCode' => 0];
                }
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->exec('echo hi');
        $this->assertCount(1, $calls);
        $this->assertStringStartsWith('echo hi', $calls[0]);
    }

    public function testNewFileSyncedBackToVfs(): void
    {
        $driver = $this->createDriverWithSyncBack(['/created.txt' => 'from sandbox']);
        $driver->exec('echo from sandbox > /created.txt');
        $this->assertTrue($driver->fs()->exists('/created.txt'));
        $this->assertSame('from sandbox', $driver->fs()->readText('/created.txt'));
    }

    public function testModifiedFileSyncedBackToVfs(): void
    {
        $driver = $this->createDriverWithSyncBack(['/existing.txt' => 'modified']);
        $driver->fs()->write('/existing.txt', 'original');
        $driver->exec('echo modified > /existing.txt');
        $this->assertSame('modified', $driver->fs()->readText('/existing.txt'));
    }

    public function testDeletedFileRemovedFromVfs(): void
    {
        $driver = $this->createDriverWithSyncBack([]);
        $driver->fs()->write('/to_delete.txt', 'data');
        $driver->exec('rm /to_delete.txt');
        $this->assertFalse($driver->fs()->exists('/to_delete.txt'));
    }

    public function testStdoutSeparatedFromSyncData(): void
    {
        $driver = $this->createDriverWithSyncBack(['/f.txt' => 'data']);
        $result = $driver->exec('echo hello');
        $this->assertSame('output', $result->stdout);
    }

    public function testSpecialCharsSurviveSync(): void
    {
        $content = "quotes'here\nback\\slash\n%percent";
        $driver = $this->createDriverWithSyncBack(['/special.txt' => $content]);
        $driver->exec('echo special');
        $this->assertSame($content, $driver->fs()->readText('/special.txt'));
    }

    // --- Command registration ---

    public function testRegisterAndUnregisterCommand(): void
    {
        $driver = $this->createDriver();
        $handler = fn($args, $stdin) => 'ok';
        $driver->registerCommand('mycmd', $handler);
        $this->assertContains('custom_commands', $driver->capabilities());
        $driver->unregisterCommand('mycmd');
        $this->assertContains('custom_commands', $driver->capabilities());
    }

    // --- Custom command dispatch ---

    public function testCustomCommandExecutesLocally(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->registerCommand('greet', fn($args, $stdin) => new \AgentHarness\ExecResult(
            stdout: "hello\n",
        ));
        $result = $driver->exec('greet');
        $this->assertSame("hello\n", $result->stdout);
        $this->assertSame(0, $result->exitCode);
        $this->assertCount(0, $calls);
    }

    public function testCustomCommandWithArgs(): void
    {
        $receivedArgs = [];
        $driver = $this->createDriver();
        $driver->registerCommand('mycmd', function ($args, $stdin) use (&$receivedArgs) {
            $receivedArgs = $args;
            return new \AgentHarness\ExecResult(stdout: "ok\n");
        });
        $result = $driver->exec('mycmd foo bar');
        $this->assertSame(['foo', 'bar'], $receivedArgs);
        $this->assertSame("ok\n", $result->stdout);
    }

    public function testUnregisteredCommandFallsThroughToRemote(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->exec('echo hello');
        $this->assertCount(1, $calls);
    }

    public function testUnregisterStopsInterception(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->registerCommand('mycmd', fn($args, $stdin) => new \AgentHarness\ExecResult(
            stdout: "local\n",
        ));
        $driver->unregisterCommand('mycmd');
        $driver->exec('mycmd');
        $this->assertCount(1, $calls);
    }

    public function testVfsSyncSkippedForCustomCommands(): void
    {
        $calls = [];
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd) use (&$calls): array {
                $calls[] = $cmd;
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $driver->fs()->write('/dirty.txt', 'data');
        $driver->registerCommand('mycmd', fn($args, $stdin) => new \AgentHarness\ExecResult(
            stdout: "ok\n",
        ));
        $driver->exec('mycmd');
        $this->assertCount(0, $calls);
    }

    // --- Clone ---

    public function testCloneCreatesIndependentCopy(): void
    {
        $driver = $this->createDriver();
        $driver->fs()->write('/file.txt', 'content');
        $driver->exec('ls');
        $cloned = $driver->cloneDriver();
        $this->assertTrue($cloned->fs()->exists('/file.txt'));
        $cloned->fs()->write('/other.txt', 'data');
        $this->assertFalse($driver->fs()->exists('/other.txt'));
    }

    public function testCloneHasNullSandboxId(): void
    {
        $driver = new OpenShellGrpcDriver(
            sandboxId: 'test-sandbox',
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $cloned = $driver->cloneDriver();
        $this->assertNull($cloned->sandboxId());
    }

    // --- Lifecycle ---

    public function testCloseResetsSandboxId(): void
    {
        $driver = new OpenShellGrpcDriver(
            sandboxId: 'test-sandbox',
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $this->assertSame('test-sandbox', $driver->sandboxId());
        $driver->close();
        $this->assertNull($driver->sandboxId());
    }

    public function testOnNotFound(): void
    {
        $driver = $this->createDriver();
        $this->assertNull($driver->getOnNotFound());
        $cb = fn() => null;
        $driver->setOnNotFound($cb);
        $this->assertSame($cb, $driver->getOnNotFound());
    }

    // --- Policy ---

    public function testDefaultPolicy(): void
    {
        $driver = $this->createDriver();
        $policy = $driver->policy();
        $this->assertTrue($policy['inferenceRouting']);
    }

    public function testCustomPolicy(): void
    {
        $driver = new OpenShellGrpcDriver(
            policy: ['filesystemAllow' => ['/data'], 'inferenceRouting' => false],
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $policy = $driver->policy();
        $this->assertSame(['/data'], $policy['filesystemAllow']);
        $this->assertFalse($policy['inferenceRouting']);
    }

    // --- Transport selection ---

    public function testDefaultTransportIsSsh(): void
    {
        $driver = $this->createDriver();
        $ref = new \ReflectionProperty($driver, 'transport');
        $this->assertSame('ssh', $ref->getValue($driver));
    }

    public function testGrpcTransportOption(): void
    {
        $driver = new OpenShellGrpcDriver(
            transport: 'grpc',
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $ref = new \ReflectionProperty($driver, 'transport');
        $this->assertSame('grpc', $ref->getValue($driver));
    }

    public function testGrpcCapabilitiesIncludeStreaming(): void
    {
        $driver = new OpenShellGrpcDriver(
            transport: 'grpc',
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $this->assertContains('streaming', $driver->capabilities());
    }

    public function testSshCapabilitiesExcludeStreaming(): void
    {
        $driver = $this->createDriver();
        $this->assertNotContains('streaming', $driver->capabilities());
    }

    public function testClonePreservesTransport(): void
    {
        $driver = new OpenShellGrpcDriver(
            transport: 'grpc',
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $cloned = $driver->cloneDriver();
        $ref = new \ReflectionProperty($cloned, 'transport');
        $this->assertSame('grpc', $ref->getValue($cloned));
    }

    // --- execStream tests ---

    public function testExecStreamYieldsEvents(): void
    {
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd): array {
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    return ['stdout' => "hello\n{$matches[0]}\n", 'stderr' => '', 'exitCode' => 0];
                }
                return ['stdout' => 'hello', 'stderr' => '', 'exitCode' => 0];
            },
        );
        $events = iterator_to_array($driver->execStream('echo hello'), false);
        $types = array_column($events, 'type');
        $this->assertContains('stdout', $types);
        $this->assertContains('exit', $types);
    }

    public function testExecStreamExitCode(): void
    {
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd): array {
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    return ['stdout' => "\n{$matches[0]}\n", 'stderr' => '', 'exitCode' => 42];
                }
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 42];
            },
        );
        $events = iterator_to_array($driver->execStream('fail'), false);
        $exitEvents = array_filter($events, fn($e) => $e['type'] === 'exit');
        $exitEvent = array_values($exitEvents)[0];
        $this->assertSame(42, $exitEvent['exitCode']);
    }

    public function testExecStreamVfsSyncAfterCompletion(): void
    {
        $driver = new OpenShellGrpcDriver(
            execOverride: function (string $cmd): array {
                if (preg_match('/__HARNESS_FS_SYNC_\d+__/', $cmd, $matches)) {
                    $marker = $matches[0];
                    $syncData = "===FILE:/result.txt===\n" . base64_encode('streamed');
                    return ['stdout' => "output\n{$marker}\n{$syncData}", 'stderr' => '', 'exitCode' => 0];
                }
                return ['stdout' => '', 'stderr' => '', 'exitCode' => 0];
            },
        );
        iterator_to_array($driver->execStream('echo output'), false);
        $this->assertTrue($driver->fs()->exists('/result.txt'));
        $this->assertSame('streamed', $driver->fs()->readText('/result.txt'));
    }

    public function testExecStreamCustomCommand(): void
    {
        $driver = new OpenShellGrpcDriver(
            execOverride: fn($cmd) => ['stdout' => '', 'stderr' => '', 'exitCode' => 0],
        );
        $driver->registerCommand('greet', fn($args, $stdin) => new \AgentHarness\ExecResult(
            stdout: "hi\n",
        ));
        $events = iterator_to_array($driver->execStream('greet'), false);
        $stdoutEvents = array_filter($events, fn($e) => $e['type'] === 'stdout');
        $this->assertCount(1, $stdoutEvents);
        $this->assertSame("hi\n", array_values($stdoutEvents)[0]['data']);
    }

    public function testExecStreamRaisesWithoutGrpc(): void
    {
        $driver = new OpenShellGrpcDriver(transport: 'ssh');
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage("execStream requires");
        iterator_to_array($driver->execStream('echo hi'), false);
    }
}
