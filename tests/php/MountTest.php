<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BuiltinFilesystemDriver;
use AgentHarness\HasShell;
use AgentHarness\LocalFolderSource;
use AgentHarness\Mountable;
use AgentHarness\MountingFilesystemDriver;
use AgentHarness\MountSource;
use AgentHarness\MountStat;
use AgentHarness\UsesTools;
use PHPUnit\Framework\TestCase;

/** In-memory read-only source. `files` maps relative subpaths -> content. */
class FakeSource implements MountSource
{
    /** @var array<string, string> */
    private array $files = [];

    /** @param array<string, string> $files */
    public function __construct(array $files)
    {
        foreach ($files as $k => $v) {
            $this->files[trim($k, '/')] = $v;
        }
    }

    /** @return array<string, bool> */
    private function dirs(): array
    {
        $dirs = ['' => true];
        foreach (array_keys($this->files) as $p) {
            $parts = explode('/', $p);
            for ($i = 1; $i < count($parts); $i++) {
                $dirs[implode('/', array_slice($parts, 0, $i))] = true;
            }
        }
        return $dirs;
    }

    public function stat(string $subpath): ?MountStat
    {
        $subpath = trim($subpath, '/');
        if (isset($this->files[$subpath])) {
            return new MountStat(isDir: false, size: strlen($this->files[$subpath]));
        }
        if (isset($this->dirs()[$subpath])) {
            return new MountStat(isDir: true);
        }
        return null;
    }

    /** @return list<string> */
    public function list(string $subpath): array
    {
        $subpath = trim($subpath, '/');
        $prefix = $subpath === '' ? '' : $subpath . '/';
        $names = [];
        foreach (array_keys($this->files) as $p) {
            if ($p !== $subpath && str_starts_with($p, $prefix)) {
                $rest = substr($p, strlen($prefix));
                $names[explode('/', $rest)[0]] = true;
            }
        }
        $out = array_keys($names);
        sort($out);
        return $out;
    }

    public function read(string $subpath): string
    {
        return $this->files[trim($subpath, '/')];
    }
}

class MountAgent
{
    use HasShell;
    use UsesTools;
}

class MountTest extends TestCase
{
    private function inner(): BuiltinFilesystemDriver
    {
        return new BuiltinFilesystemDriver();
    }

    private function mounting(): MountingFilesystemDriver
    {
        return new MountingFilesystemDriver($this->inner());
    }

    public function testEmptyTablePassthrough(): void
    {
        $inner = $this->inner();
        $inner->write('/a.txt', 'hi');
        $fs = new MountingFilesystemDriver($inner);
        $this->assertSame('hi', $fs->read('/a.txt'));
        $this->assertSame([], $fs->mounts());
    }

    public function testLongestPrefixRouting(): void
    {
        $fs = $this->mounting();
        $fs->mount('/a', new FakeSource(['x.txt' => 'outer']));
        $fs->mount('/a/b', new FakeSource(['y.txt' => 'inner']));
        $this->assertSame('inner', $fs->read('/a/b/y.txt'));
        $this->assertSame('outer', $fs->read('/a/x.txt'));
    }

    public function testReadFromSource(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'world']));
        $this->assertSame('world', $fs->read('/work/hello.txt'));
        $this->assertSame('world', $fs->readText('/work/hello.txt'));
    }

    public function testExistsMountPointAndAncestor(): void
    {
        $fs = $this->mounting();
        $fs->mount('/deep/mnt', new FakeSource(['f.txt' => 'x']));
        $this->assertTrue($fs->exists('/deep/mnt'));
        $this->assertTrue($fs->exists('/deep'));
        $this->assertTrue($fs->exists('/deep/mnt/f.txt'));
        $this->assertFalse($fs->exists('/deep/mnt/nope.txt'));
        $this->assertTrue($fs->isDir('/deep'));
        $this->assertTrue($fs->isDir('/deep/mnt'));
        $this->assertFalse($fs->isDir('/deep/mnt/f.txt'));
    }

    public function testListdirMergeDedupSorted(): void
    {
        $inner = $this->inner();
        $inner->write('/work/local.txt', 'L');
        $inner->write('/work/shared.txt', 'edited');
        $fs = new MountingFilesystemDriver($inner);
        $fs->mount('/work', new FakeSource(['shared.txt' => 'orig', 'sub/deep.txt' => 'd']));
        $this->assertSame(['local.txt', 'shared.txt', 'sub'], $fs->listdir('/work'));
    }

    public function testListdirRootShowsMountPoint(): void
    {
        $fs = $this->mounting();
        $fs->mount('/repo', new FakeSource(['a.txt' => 'x']));
        $this->assertContains('repo', $fs->listdir('/'));
    }

    public function testCopyUpShadowing(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'original']));
        $this->assertSame('original', $fs->read('/work/hello.txt'));
        $fs->write('/work/hello.txt', 'edited');
        $this->assertSame('edited', $fs->read('/work/hello.txt'));
        $this->assertSame(1, count(array_filter($fs->listdir('/work'), fn($n) => $n === 'hello.txt')));
    }

    public function testRemoveCopiedUpUnshadows(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'original']));
        $fs->write('/work/hello.txt', 'edited');
        $fs->remove('/work/hello.txt');
        $this->assertSame('original', $fs->read('/work/hello.txt'));
    }

    public function testRemoveSourceOnlyRaises(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'original']));
        $this->expectException(\RuntimeException::class);
        $fs->remove('/work/hello.txt');
    }

    public function testFindMerged(): void
    {
        $inner = $this->inner();
        $inner->write('/work/local.md', 'L');
        $fs = new MountingFilesystemDriver($inner);
        $fs->mount('/work', new FakeSource(['a.md' => 'x', 'sub/b.md' => 'y', 'c.txt' => 'z']));
        $found = $fs->find('/work', '*.md');
        sort($found);
        $this->assertSame(['/work/a.md', '/work/local.md', '/work/sub/b.md'], $found);
    }

    public function testStatSourceFile(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'world']));
        $s = $fs->stat('/work/hello.txt');
        $this->assertSame('file', $s['type']);
        $this->assertSame(5, $s['size']);
        $this->assertSame('directory', $fs->stat('/work')['type']);
    }

    public function testCloneWritableDivergesSourceShared(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['hello.txt' => 'original']));
        $fs->write('/work/local.txt', 'L');
        $clone = $fs->cloneFs();
        $this->assertInstanceOf(MountingFilesystemDriver::class, $clone);
        $clone->write('/work/local.txt', 'changed');
        $this->assertSame('L', $fs->read('/work/local.txt'));
        $this->assertSame('changed', $clone->read('/work/local.txt'));
        $this->assertSame($fs->mounts(), $clone->mounts());
        $this->assertSame('original', $clone->read('/work/hello.txt'));
    }

    public function testUnmount(): void
    {
        $fs = $this->mounting();
        $fs->mount('/work', new FakeSource(['a.txt' => 'x']));
        $this->assertSame(['/work'], $fs->mounts());
        $fs->unmount('/work');
        $this->assertSame([], $fs->mounts());
        $this->assertFalse($fs->exists('/work/a.txt'));
    }

    public function testIsMountable(): void
    {
        $this->assertInstanceOf(Mountable::class, $this->mounting());
    }

    // --- LocalFolderSource ---------------------------------------------------

    private function mkTemp(): string
    {
        $dir = sys_get_temp_dir() . '/mount-' . bin2hex(random_bytes(8));
        mkdir($dir, 0o755, true);
        return $dir;
    }

    public function testLocalFolderSourceNestedFiles(): void
    {
        $root = $this->mkTemp();
        file_put_contents($root . '/a.txt', 'alpha');
        mkdir($root . '/sub');
        file_put_contents($root . '/sub/b.txt', 'beta');

        $src = new LocalFolderSource($root);
        $this->assertTrue($src->stat('')->isDir);
        $this->assertFalse($src->stat('a.txt')->isDir);
        $this->assertSame(5, $src->stat('a.txt')->size);
        $list = $src->list('');
        sort($list);
        $this->assertSame(['a.txt', 'sub'], $list);
        $this->assertSame(['b.txt'], $src->list('sub'));
        $this->assertSame('alpha', $src->read('a.txt'));
        $this->assertSame('beta', $src->read('sub/b.txt'));
        $this->assertNull($src->stat('nope.txt'));
    }

    public function testLocalFolderSourceParentTraversalCannotEscape(): void
    {
        $base = $this->mkTemp();
        $root = $base . '/root';
        mkdir($root);
        file_put_contents($root . '/in.txt', 'inside');
        file_put_contents($base . '/secret.txt', 'secret');

        $src = new LocalFolderSource($root);
        $this->assertNull($src->stat('../secret.txt'));
    }

    public function testLocalFolderSourceSymlinkCannotEscape(): void
    {
        $base = $this->mkTemp();
        $root = $base . '/root';
        mkdir($root);
        $outside = $base . '/outside.txt';
        file_put_contents($outside, 'secret');
        if (!@symlink($outside, $root . '/link.txt')) {
            $this->markTestSkipped('symlinks unsupported');
        }
        $src = new LocalFolderSource($root);
        $this->assertNull($src->stat('link.txt'));
    }

    // --- E2E -----------------------------------------------------------------

    public function testMountVisibleToToolsAndShell(): void
    {
        $root = $this->mkTemp();
        file_put_contents($root . '/hello.txt', 'from-host');
        file_put_contents($root . '/notes.md', '# notes');
        mkdir($root . '/sub');
        file_put_contents($root . '/sub/deep.txt', 'deep-content');

        $agent = new MountAgent();
        $agent->mountSource('/work', new LocalFolderSource($root));

        $this->assertStringContainsString('hello.txt', $agent->execCommand('ls /work')->stdout);
        $this->assertStringContainsString('from-host', $agent->execCommand('cat /work/hello.txt')->stdout);
        $this->assertStringContainsString('notes.md', $agent->execCommand("find /work -name '*.md'")->stdout);
        $this->assertStringContainsString('deep-content', $agent->execCommand('grep -r deep /work')->stdout);

        $fs = $agent->shell()->fs();
        $this->assertSame('deep-content', $fs->readText('/work/sub/deep.txt'));
        $this->assertTrue($fs->exists('/work/notes.md'));
    }

    public function testEditMountedFileCopyUpHostUnchanged(): void
    {
        $root = $this->mkTemp();
        $hostFile = $root . '/hello.txt';
        file_put_contents($hostFile, 'from-host');

        $agent = new MountAgent();
        $agent->mountSource('/work', new LocalFolderSource($root));

        $agent->execCommand('echo edited > /work/hello.txt');
        $this->assertSame('edited', trim($agent->execCommand('cat /work/hello.txt')->stdout));
        $this->assertSame('from-host', file_get_contents($hostFile));
    }

    public function testMountsListing(): void
    {
        $root = $this->mkTemp();
        $agent = new MountAgent();
        $agent->mountSource('/work', new LocalFolderSource($root));
        $this->assertSame(['/work'], $agent->mountSources());
        $agent->unmountSource('/work');
        $this->assertSame([], $agent->mountSources());
    }
}
