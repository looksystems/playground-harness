package util_test

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/internal/util"
)

// syncBuffer is a goroutine-safe bytes.Buffer for capturing log output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// redirectLog redirects the default logger to a thread-safe buffer for the
// duration of the test. The returned buffer can be read at any time.
func redirectLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	orig := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	log.SetOutput(buf)
	t.Cleanup(func() {
		log.SetOutput(orig)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	})
	return buf
}

// waitFor polls cond until it returns true or the deadline elapses.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestGoSafeRunsFn(t *testing.T) {
	done := make(chan struct{})
	var ran atomic.Bool

	util.GoSafe(func() {
		ran.Store(true)
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GoSafe did not invoke fn in time")
	}
	assert.True(t, ran.Load())
}

func TestGoSafeRecoversPanic(t *testing.T) {
	buf := redirectLog(t)

	util.GoSafe(func() {
		panic("boom-value-42")
	})

	// Wait for the log entry to appear; GoSafe's recover/log runs after fn
	// returns via panic.
	ok := waitFor(t, 2*time.Second, func() bool {
		out := buf.String()
		return strings.Contains(strings.ToLower(out), "panic") &&
			strings.Contains(out, "boom-value-42")
	})

	out := buf.String()
	require.True(t, ok, "expected log to mention panic and recovered value; got %q", out)
	assert.Contains(t, strings.ToLower(out), "panic")
	assert.Contains(t, out, "boom-value-42")
}

func TestGoSafePanicDoesNotPropagate(t *testing.T) {
	_ = redirectLog(t)

	// If GoSafe failed to recover, the test process would crash here.
	util.GoSafe(func() {
		panic("should-not-escape")
	})

	// Give the goroutine time to panic-and-recover, then confirm this
	// goroutine is still running normally.
	time.Sleep(50 * time.Millisecond)
	assert.True(t, true, "main test goroutine still running after panic in GoSafe")
}

func TestGoSafeConcurrentIndependent(t *testing.T) {
	const n = 20
	buf := redirectLog(t)

	var wg sync.WaitGroup
	wg.Add(n)

	var successCount atomic.Int64

	for i := 0; i < n; i++ {
		i := i
		util.GoSafe(func() {
			defer wg.Done()
			if i%2 == 0 {
				panic("goroutine-panic")
			}
			successCount.Add(1)
		})
	}

	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("not all GoSafe goroutines completed in time; log=%q", buf.String())
	}

	assert.Equal(t, int64(n/2), successCount.Load(),
		"non-panicking goroutines should complete independently of panicking ones")

	// And the panicking halves should each have been logged. GoSafe's
	// recover/log runs after the fn's deferred wg.Done, so poll briefly.
	ok := waitFor(t, 2*time.Second, func() bool {
		return strings.Count(buf.String(), "goroutine-panic") >= n/2
	})
	assert.True(t, ok,
		"each panicking goroutine should log its panic value; got %q", buf.String())
}
