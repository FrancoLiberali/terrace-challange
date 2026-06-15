// Package resilience holds the generic resilience primitives the rest
// of the codebase composes around external dependencies: rate limiting,
// circuit breaking, and retry with exponential backoff and jitter. Each
// primitive is a thin wrapper around a battle-tested third-party
// library so the rest of the codebase can depend on a small, internal
// surface and swap the underlying implementation later without rippling.
package resilience

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	// Token-bucket rate limiter from the golang.org/x sub-repo.
	"golang.org/x/time/rate"
)

// RateLimiter is a token-bucket limiter and the seam where cross-cutting
// observability concerns (debug logs today, metrics / tracing later)
// live. Callers Wait before every rate-limited operation; Wait blocks
// until a token is available or the context is cancelled.
type RateLimiter struct {
	name  string
	inner *rate.Limiter
}

// NewRateLimiter returns a limiter that emits `perSecond` tokens per
// second with a maximum burst of `burst`. `name` tags the limiter for
// observability — it appears as the venue field in debug logs and
// (later) in metric labels. A burst of 1 disables bursting.
func NewRateLimiter(name string, perSecond float64, burst int) *RateLimiter {
	return &RateLimiter{name: name, inner: rate.NewLimiter(rate.Limit(perSecond), burst)}
}

// rateLimitLogThreshold filters Wait calls that returned immediately
// (token already available) from the debug log — only actual blocking
// is worth surfacing.
const rateLimitLogThreshold = time.Millisecond

// Wait blocks until a token is available or ctx is cancelled. The
// caller should check the returned error and abort the operation
// rather than proceed when Wait returns non-nil. A wait that actually
// blocked (> 1ms) is logged at DEBUG so an operator can diagnose
// "why is arbd missing blocks?" by spotting rate-budget exhaustion.
func (r *RateLimiter) Wait(ctx context.Context) error {
	start := time.Now()
	err := r.inner.Wait(ctx)
	if elapsed := time.Since(start); err == nil && elapsed >= rateLimitLogThreshold {
		slog.DebugContext(ctx, "rate-limit wait", "venue", r.name, "wait_ms", elapsed.Milliseconds())
	}
	return err
}

// rateLimitTransport gates each outbound HTTP request through the
// limiter before delegating to the inner transport.
type rateLimitTransport struct {
	inner   http.RoundTripper
	limiter *RateLimiter
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.inner.RoundTrip(req)
}
