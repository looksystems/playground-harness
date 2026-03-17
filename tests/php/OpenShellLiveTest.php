<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\OpenShellGrpcDriver;
use AgentHarness\OpenShellDriver;
use PHPUnit\Framework\TestCase;

/**
 * Live integration tests for OpenShellGrpcDriver against a running OpenShell instance.
 *
 * Skipped by default. To run, start an OpenShell instance and set
 * OPENSHELL_ENDPOINT (default: localhost:50051).
 */
class OpenShellLiveTest extends TestCase
{
    private static bool $hasOpenShell = false;

    public static function setUpBeforeClass(): void
    {
        $host = getenv('OPENSHELL_SSH_HOST') ?: 'localhost';
        $port = (int) (getenv('OPENSHELL_SSH_PORT') ?: '2222');
        $sock = @fsockopen($host, $port, $errno, $errstr, 1);
        if ($sock) {
            fclose($sock);
            self::$hasOpenShell = true;
        }
    }

    protected function setUp(): void
    {
        if (!self::$hasOpenShell) {
            $this->markTestSkipped('OpenShell not running (set OPENSHELL_ENDPOINT or start on localhost:50051)');
        }
    }

    private const WORKSPACE = '/tmp/harness';

    private function driver(): OpenShellGrpcDriver
    {
        return new OpenShellGrpcDriver(
            sshHost: getenv('OPENSHELL_SSH_HOST') ?: 'localhost',
            sshPort: (int) (getenv('OPENSHELL_SSH_PORT') ?: '2222'),
            sshUser: getenv('OPENSHELL_SSH_USER') ?: 'sandbox',
            workspace: self::WORKSPACE,
        );
    }

    // --- Basic execution ---

    public function testEcho(): void
    {
        $d = $this->driver();
        $r = $d->exec('echo hello world');
        $this->assertSame('hello world', trim($r->stdout));
        $this->assertSame(0, $r->exitCode);
        $d->close();
    }

    public function testPipes(): void
    {
        $d = $this->driver();
        $r = $d->exec('echo hello | tr a-z A-Z');
        $this->assertSame('HELLO', trim($r->stdout));
        $d->close();
    }

    public function testExitCodeOnFailure(): void
    {
        $d = $this->driver();
        $r = $d->exec('false');
        $this->assertNotSame(0, $r->exitCode);
        $d->close();
    }

    public function testVariableExpansion(): void
    {
        $d = $this->driver();
        $r = $d->exec('X=42; echo $X');
        $this->assertSame('42', trim($r->stdout));
        $d->close();
    }

    public function testRedirects(): void
    {
        $d = $this->driver();
        $r = $d->exec('echo written > /tmp/out.txt && cat /tmp/out.txt');
        $this->assertSame('written', trim($r->stdout));
        $d->close();
    }

    public function testForLoop(): void
    {
        $d = $this->driver();
        $r = $d->exec('for i in 1 2 3; do echo $i; done');
        $this->assertSame("1\n2\n3", trim($r->stdout));
        $d->close();
    }

    public function testStderrCapture(): void
    {
        $d = $this->driver();
        $r = $d->exec('echo err >&2');
        $this->assertStringContainsString('err', $r->stderr);
        $d->close();
    }

    // --- VFS sync ---

    public function testWriteInVfsReadInSandbox(): void
    {
        $d = $this->driver();
        $d->fs()->write('/test.txt', 'hello from vfs');
        $w = self::WORKSPACE;
        $r = $d->exec("cat {$w}/test.txt");
        $this->assertSame('hello from vfs', trim($r->stdout));
        $d->close();
    }

    public function testWriteInSandboxReadBackViaVfs(): void
    {
        $d = $this->driver();
        $w = self::WORKSPACE;
        $d->exec("mkdir -p {$w} && echo 'from sandbox' > {$w}/created.txt");
        $this->assertTrue($d->fs()->exists('/created.txt'));
        $this->assertStringContainsString('from sandbox', $d->fs()->readText('/created.txt'));
        $d->close();
    }

    public function testRoundTripVfsToSandboxToVfs(): void
    {
        $d = $this->driver();
        $w = self::WORKSPACE;
        $d->fs()->write('/round.txt', 'original');
        $d->exec("cat {$w}/round.txt | tr a-z A-Z > {$w}/upper.txt");
        $this->assertTrue($d->fs()->exists('/upper.txt'));
        $this->assertSame('ORIGINAL', trim($d->fs()->readText('/upper.txt')));
        $d->close();
    }

    public function testSpecialCharsSurviveSync(): void
    {
        $d = $this->driver();
        $w = self::WORKSPACE;
        $content = "quotes'here\nback\\slash\n%percent";
        $d->fs()->write('/special.txt', $content);
        $r = $d->exec("cat {$w}/special.txt");
        $this->assertSame($content, $r->stdout);
        $d->close();
    }

    // --- Policy enforcement ---

    public function testDefaultPolicyAllowsExec(): void
    {
        $d = $this->driver();
        $r = $d->exec('echo allowed');
        $this->assertSame(0, $r->exitCode);
        $d->close();
    }

    public function testPolicyAccessible(): void
    {
        $d = $this->driver();
        $policy = $d->policy();
        $this->assertTrue($policy['inferenceRouting']);
        $d->close();
    }

    // --- Lifecycle ---

    public function testCloseResetsSandboxId(): void
    {
        $d = $this->driver();
        $d->exec('echo hello');
        $d->close();
        $this->assertNull($d->sandboxId());
    }

    public function testCloneCreatesIndependentCopy(): void
    {
        $d = $this->driver();
        $d->fs()->write('/orig.txt', 'original');
        $d->exec('echo hello');  // syncs orig.txt to remote
        $cloned = $d->cloneDriver();
        $this->assertTrue($cloned->fs()->exists('/orig.txt'));
        $cloned->fs()->write('/clone_only.txt', 'clone');
        $this->assertFalse($d->fs()->exists('/clone_only.txt'));
        $cloned->close();
        $d->close();
    }

    // --- Capabilities ---

    public function testCapabilities(): void
    {
        $d = $this->driver();
        $caps = $d->capabilities();
        $this->assertContains('remote', $caps);
        $this->assertContains('policies', $caps);
        $this->assertContains('streaming', $caps);
        $d->close();
    }
}
