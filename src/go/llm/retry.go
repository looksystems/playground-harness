package llm

import (
	"context"
	"errors"
	"time"
)

// retryConfig holds the options for the retry decorator.
type retryConfig struct {
	maxRetries int
	maxDelay   time.Duration
	sleep      func(ctx context.Context, d time.Duration) error // injectable for tests
}

// RetryOption is a functional option for WithRetry.
type RetryOption func(*retryConfig)

// MaxRetries sets the maximum number of retry attempts after the first
// failure, i.e. the total number of attempts is MaxRetries+1.
// Default: 2 (3 total attempts), matching Python's base_agent.max_retries default.
func MaxRetries(n int) RetryOption {
	return func(c *retryConfig) { c.maxRetries = n }
}

// MaxDelay caps the exponential back-off delay.
// Default: 10s, matching Python's min(2**attempt, 10).
func MaxDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.maxDelay = d }
}

// withSleepFn is an unexported option used in tests to replace time.After with
// a fast fake clock.
func withSleepFn(fn func(ctx context.Context, d time.Duration) error) RetryOption {
	return func(c *retryConfig) { c.sleep = fn }
}

// defaultSleep sleeps for d, honouring ctx cancellation.
func defaultSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isContextError returns true for errors that should not be retried.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// backoffDelay returns 2^attempt seconds, capped at max.
// attempt=0 → 1s, attempt=1 → 2s, attempt=2 → 4s, …
func backoffDelay(attempt int, max time.Duration) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > max {
		return max
	}
	return d
}

// retryClient wraps an inner Client with automatic retry on transient errors.
type retryClient struct {
	inner Client
	cfg   retryConfig
}

// WithRetry wraps inner so that both Stream and Complete are retried with
// exponential back-off on transient errors.
//
// Retry semantics mirror Python's _call_llm loop:
//   - delay = min(2^attempt, MaxDelay)  (attempt 0 → 1s, capped at MaxDelay)
//   - Does NOT retry context.Canceled or context.DeadlineExceeded
//   - Stream retries apply to the initial Stream() call only; in-stream errors
//     (terminal Chunk.Err) are NOT retried
func WithRetry(inner Client, opts ...RetryOption) Client {
	cfg := retryConfig{
		maxRetries: 2,
		maxDelay:   10 * time.Second,
		sleep:      defaultSleep,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &retryClient{inner: inner, cfg: cfg}
}

// Complete implements Client with retry.
func (r *retryClient) Complete(ctx context.Context, req Request) (Response, error) {
	var lastErr error
	for attempt := 0; attempt <= r.cfg.maxRetries; attempt++ {
		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Never retry context errors.
		if isContextError(err) {
			return Response{}, err
		}

		// If this was the last attempt, stop.
		if attempt == r.cfg.maxRetries {
			break
		}

		// Sleep with back-off before the next attempt.
		delay := backoffDelay(attempt, r.cfg.maxDelay)
		if sleepErr := r.cfg.sleep(ctx, delay); sleepErr != nil {
			return Response{}, sleepErr
		}
	}
	return Response{}, lastErr
}

// Stream implements Client with retry on the initial Stream() call.
// In-stream errors delivered as terminal Chunks are NOT retried.
func (r *retryClient) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	var lastErr error
	for attempt := 0; attempt <= r.cfg.maxRetries; attempt++ {
		ch, err := r.inner.Stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		lastErr = err

		// Never retry context errors.
		if isContextError(err) {
			return nil, err
		}

		// If this was the last attempt, stop.
		if attempt == r.cfg.maxRetries {
			break
		}

		// Sleep with back-off before the next attempt.
		delay := backoffDelay(attempt, r.cfg.maxDelay)
		if sleepErr := r.cfg.sleep(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
	return nil, lastErr
}
