<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BashkitCLIDriver;
use PHPUnit\Framework\TestCase;

/**
 * Live integration tests for BashkitCLIDriver against real bashkit CLI.
 */
class BashkitLiveTest extends TestCase
{
    private static bool $hasBashkit = false;

    public static function setUpBeforeClass(): void
    {
        exec('which bashkit 2>/dev/null', $output, $code);
        self::$hasBashkit = $code === 0;
    }

    protected function setUp(): void
    {
        if (!self::$hasBashkit) {
            $this->markTestSkipped('bashkit CLI not installed');
        }
    }

    private function driver(): BashkitCLIDriver
    {
        return new BashkitCLIDriver();
    }

    // --- Basic execution ---

    public function testEcho(): void
    {
        $r = $this->driver()->exec('echo hello world');
        $this->assertSame('hello world', trim($r->stdout));
        $this->assertSame(0, $r->exitCode);
    }

    public function testPipes(): void
    {
        $r = $this->driver()->exec('echo hello | tr a-z A-Z');
        $this->assertSame('HELLO', trim($r->stdout));
    }

    public function testExitCodeOnFailure(): void
    {
        $r = $this->driver()->exec('false');
        $this->assertNotSame(0, $r->exitCode);
    }

    public function testVariableExpansion(): void
    {
        $r = $this->driver()->exec('X=42; echo $X');
        $this->assertSame('42', trim($r->stdout));
    }

    public function testRedirects(): void
    {
        $r = $this->driver()->exec('echo written > /out.txt && cat /out.txt');
        $this->assertSame('written', trim($r->stdout));
    }

    public function testForLoop(): void
    {
        $r = $this->driver()->exec('for i in 1 2 3; do echo $i; done');
        $this->assertSame("1\n2\n3", trim($r->stdout));
    }

    public function testStderrCapture(): void
    {
        $r = $this->driver()->exec('echo err >&2');
        $this->assertStringContainsString('err', $r->stderr);
    }

    // --- POSIX builtins ---

    public function testSeqWc(): void
    {
        $r = $this->driver()->exec('seq 1 5 | wc -l');
        $this->assertSame('5', trim($r->stdout));
    }

    public function testHead(): void
    {
        $r = $this->driver()->exec('seq 1 10 | head -3');
        $this->assertSame("1\n2\n3", trim($r->stdout));
    }

    public function testSort(): void
    {
        $r = $this->driver()->exec("printf '3\n1\n2\n' | sort");
        $this->assertSame("1\n2\n3", trim($r->stdout));
    }

    public function testGrep(): void
    {
        $r = $this->driver()->exec("printf 'foo\nbar\nbaz\n' | grep ba");
        $this->assertSame("bar\nbaz", trim($r->stdout));
    }

    public function testCut(): void
    {
        $r = $this->driver()->exec("echo 'a:b:c' | cut -d: -f2");
        $this->assertSame('b', trim($r->stdout));
    }

    public function testBasename(): void
    {
        $r = $this->driver()->exec('basename /foo/bar/baz.txt');
        $this->assertSame('baz.txt', trim($r->stdout));
    }

    public function testDirname(): void
    {
        $r = $this->driver()->exec('dirname /foo/bar/baz.txt');
        $this->assertSame('/foo/bar', trim($r->stdout));
    }

    // --- VFS sync ---

    public function testWriteInVfsReadInBashkit(): void
    {
        $d = $this->driver();
        $d->fs()->write('/test.txt', 'hello from vfs');
        $r = $d->exec('cat /test.txt');
        $this->assertSame('hello from vfs', trim($r->stdout));
    }

    public function testWriteInBashkitReadBackViaVfs(): void
    {
        $d = $this->driver();
        $d->exec("echo 'from bashkit' > /created.txt");
        $this->assertTrue($d->fs()->exists('/created.txt'));
        $this->assertStringContainsString('from bashkit', $d->fs()->readText('/created.txt'));
    }

    public function testRoundTripVfsToBashkitToVfs(): void
    {
        $d = $this->driver();
        $d->fs()->write('/round.txt', 'original');
        $d->exec('cat /round.txt | tr a-z A-Z > /upper.txt');
        $this->assertTrue($d->fs()->exists('/upper.txt'));
        $this->assertSame('ORIGINAL', trim($d->fs()->readText('/upper.txt')));
    }

    public function testSpecialCharsSurviveSync(): void
    {
        $d = $this->driver();
        $content = "quotes'here\nback\\slash\n%percent";
        $d->fs()->write('/special.txt', $content);
        $r = $d->exec('cat /special.txt');
        $this->assertSame($content, $r->stdout);
    }

    // --- Stateless behavior ---

    public function testVariablesDoNotPersist(): void
    {
        $d = $this->driver();
        $d->exec('MY_VAR=hello');
        $r = $d->exec('echo $MY_VAR');
        $this->assertSame('', trim($r->stdout));
    }
}
