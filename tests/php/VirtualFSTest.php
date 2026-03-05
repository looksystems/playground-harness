<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\VirtualFS;
use PHPUnit\Framework\TestCase;

class VirtualFSTest extends TestCase
{
    public function testWriteAndRead(): void
    {
        $fs = new VirtualFS();
        $fs->write('/hello.txt', 'world');
        $this->assertSame('world', $fs->read('/hello.txt'));
    }

    public function testReadTextAlias(): void
    {
        $fs = new VirtualFS();
        $fs->write('/file.txt', 'content');
        $this->assertSame('content', $fs->readText('/file.txt'));
    }

    public function testPathNormalization(): void
    {
        $fs = new VirtualFS();
        $fs->write('foo/bar.txt', 'data');
        $this->assertSame('data', $fs->read('/foo/bar.txt'));
    }

    public function testPathNormalizationDotDot(): void
    {
        $fs = new VirtualFS();
        $fs->write('/a/b/../c.txt', 'data');
        $this->assertSame('data', $fs->read('/a/c.txt'));
    }

    public function testReadNonexistentThrows(): void
    {
        $fs = new VirtualFS();
        $this->expectException(\RuntimeException::class);
        $fs->read('/nope.txt');
    }

    public function testExists(): void
    {
        $fs = new VirtualFS();
        $this->assertFalse($fs->exists('/a.txt'));
        $fs->write('/a.txt', '');
        $this->assertTrue($fs->exists('/a.txt'));
    }

    public function testExistsDirectory(): void
    {
        $fs = new VirtualFS();
        $fs->write('/dir/file.txt', 'content');
        $this->assertTrue($fs->exists('/dir'));
    }

    public function testRemove(): void
    {
        $fs = new VirtualFS();
        $fs->write('/tmp.txt', 'data');
        $fs->remove('/tmp.txt');
        $this->assertFalse($fs->exists('/tmp.txt'));
    }

    public function testRemoveNonexistentThrows(): void
    {
        $fs = new VirtualFS();
        $this->expectException(\RuntimeException::class);
        $fs->remove('/nope.txt');
    }

    public function testListdir(): void
    {
        $fs = new VirtualFS();
        $fs->write('/src/a.php', '');
        $fs->write('/src/b.php', '');
        $fs->write('/src/lib/c.php', '');
        $entries = $fs->listdir('/src');
        $this->assertSame(['a.php', 'b.php', 'lib'], $entries);
    }

    public function testListdirRoot(): void
    {
        $fs = new VirtualFS();
        $fs->write('/a.txt', '');
        $fs->write('/b/c.txt', '');
        $entries = $fs->listdir('/');
        $this->assertSame(['a.txt', 'b'], $entries);
    }

    public function testFind(): void
    {
        $fs = new VirtualFS();
        $fs->write('/src/main.php', '');
        $fs->write('/src/lib/util.php', '');
        $fs->write('/readme.md', '');
        $results = $fs->find('/', '*.php');
        $this->assertSame(['/src/lib/util.php', '/src/main.php'], $results);
    }

    public function testFindWithRoot(): void
    {
        $fs = new VirtualFS();
        $fs->write('/a/file.txt', '');
        $fs->write('/b/file.txt', '');
        $results = $fs->find('/a');
        $this->assertSame(['/a/file.txt'], $results);
    }

    public function testStat(): void
    {
        $fs = new VirtualFS();
        $fs->write('/file.txt', 'hello');
        $stat = $fs->stat('/file.txt');
        $this->assertSame('file', $stat['type']);
        $this->assertSame(5, $stat['size']);
    }

    public function testStatDirectory(): void
    {
        $fs = new VirtualFS();
        $fs->write('/dir/file.txt', '');
        $stat = $fs->stat('/dir');
        $this->assertSame('directory', $stat['type']);
    }

    public function testWriteLazy(): void
    {
        $called = false;
        $fs = new VirtualFS();
        $fs->writeLazy('/lazy.txt', function () use (&$called) {
            $called = true;
            return 'lazy content';
        });

        $this->assertFalse($called);
        $this->assertTrue($fs->exists('/lazy.txt'));
        $this->assertSame('lazy content', $fs->read('/lazy.txt'));
        $this->assertTrue($called);
    }

    public function testWriteLazyCachesResult(): void
    {
        $callCount = 0;
        $fs = new VirtualFS();
        $fs->writeLazy('/lazy.txt', function () use (&$callCount) {
            $callCount++;
            return 'content';
        });

        $fs->read('/lazy.txt');
        $fs->read('/lazy.txt');
        $this->assertSame(1, $callCount);
    }

    public function testCloneFs(): void
    {
        $fs = new VirtualFS();
        $fs->write('/a.txt', 'original');

        $clone = $fs->cloneFs();
        $clone->write('/a.txt', 'modified');
        $clone->write('/b.txt', 'new');

        $this->assertSame('original', $fs->read('/a.txt'));
        $this->assertFalse($fs->exists('/b.txt'));
        $this->assertSame('modified', $clone->read('/a.txt'));
    }

    public function testConstructorWithFiles(): void
    {
        $fs = new VirtualFS(['/a.txt' => 'aaa', '/b.txt' => 'bbb']);
        $this->assertSame('aaa', $fs->read('/a.txt'));
        $this->assertSame('bbb', $fs->read('/b.txt'));
    }

    public function testIsDir(): void
    {
        $fs = new VirtualFS();
        $fs->write('/dir/sub/file.txt', '');
        $this->assertTrue($fs->isDir('/dir'));
        $this->assertTrue($fs->isDir('/dir/sub'));
        $this->assertFalse($fs->isDir('/dir/sub/file.txt'));
        $this->assertFalse($fs->isDir('/nonexistent'));
    }

    public function testRemoveLazyFile(): void
    {
        $fs = new VirtualFS();
        $fs->writeLazy('/lazy.txt', fn() => 'content');
        $this->assertTrue($fs->exists('/lazy.txt'));
        $fs->remove('/lazy.txt');
        $this->assertFalse($fs->exists('/lazy.txt'));
    }
}
