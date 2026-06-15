package resilience

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	// Classic 3-state circuit breaker from Sony; minimal API, zero
	// transitive deps.
	"github.com/sony/gobreaker/v2"
)

// CircuitBreaker is a 3-state breaker (closed / open / half-open) that
// short-circuits calls to a failing dependency.
//
// Closed → Open: when the failure ratio exceeds FailureRatio after at
// least MinRequests calls have been observed. The MinRequests floor
// avoids tripping on a tiny startup sample (5/5 = 100% means little).
// Open → Half-Open: after the configured cooldown elapses.
// Half-Open → Closed: the next call succeeds.
// Half-Open → Open: the next call fails — cooldown resets.
type CircuitBreaker struct {
	inner *gobreaker.CircuitBreaker[any]
}

// BreakerConfig parameterises a CircuitBreaker. Name is included in
// the underlying gobreaker settings; it surfaces in state-change
// callback hooks and in test failure output.
type BreakerConfig struct {
	Name          string
	MinRequests   uint32        // minimum sample size before the ratio is evaluated
	FailureRatio  float64       // trip when failures/requests strictly exceeds this
	Cooldown      time.Duration // how long to stay open before half-open
	Interval      time.Duration // closed-state counter reset cadence; 0 = never reset
	OnStateChange func(name string, from, to string)
}

// NewCircuitBreaker returns a breaker that opens when the failure
// ratio over the current closed-state window exceeds cfg.FailureRatio,
// provided at least cfg.MinRequests calls have been observed in that
// window. It cools down for cfg.Cooldown before transitioning to
// half-open. cfg.Interval controls how often the closed-state counter
// is cleared — 0 means it accumulates over the breaker's lifetime,
// which is rarely what you want for long-running processes.
//
// context.Canceled is treated as not-a-failure (the caller decided to
// stop, e.g., SIGTERM mid-call — nothing about the dependency went
// wrong). context.DeadlineExceeded IS counted as a failure: a provider
// that doesn't respond within our configured timeout is unhealthy from
// our point of view, which is exactly what the breaker exists to track.
func NewCircuitBreaker(cfg BreakerConfig) *CircuitBreaker {
	settings := gobreaker.Settings{
		Name:     cfg.Name,
		Timeout:  cfg.Cooldown,
		Interval: cfg.Interval,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			if c.Requests < cfg.MinRequests {
				return false
			}
			return float64(c.TotalFailures)/float64(c.Requests) > cfg.FailureRatio
		},
		IsSuccessful: func(err error) bool {
			return err == nil || errors.Is(err, context.Canceled)
		},
	}
	if cfg.OnStateChange != nil {
		settings.OnStateChange = func(name string, from, to gobreaker.State) {
			cfg.OnStateChange(name, from.String(), to.String())
		}
	}
	return &CircuitBreaker{inner: gobreaker.NewCircuitBreaker[any](settings)}
}

// Execute runs op through the breaker. If the breaker is open the
// call is rejected immediately with gobreaker.ErrOpenState (or
// gobreaker.ErrTooManyRequests during a half-open over-probe) and op
// is never invoked; the rejection is logged at DEBUG so an operator
// can see how many calls were short-circuited during an outage (state
// transitions themselves go through OnStateChange in the config).
// Otherwise op runs and its outcome is reported to the breaker.
func (b *CircuitBreaker) Execute(ctx context.Context, op func() error) error {
	_, err := b.inner.Execute(func() (any, error) {
		return nil, op()
	})
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		slog.DebugContext(ctx, "circuit breaker rejected call", "venue", b.inner.Name())
	}
	return err
}

// circuitBreakerTransport gates each outbound HTTP request through the
// breaker. When the breaker is open the request short-circuits with
// gobreaker.ErrOpenState (which surfaces unchanged to the retry layer
// so it can be classified as permanent).
//
// 5xx responses are reported to the breaker as failures even though
// Go's HTTP semantics surface them as (resp, nil). Without this, a
// server consistently returning 500 would never trip the breaker. The
// 5xx marker error is swallowed on the way out so the response itself
// still flows upstream — retryablehttp's CheckRetry will see the 5xx
// status and decide whether to retry.
type circuitBreakerTransport struct {
	inner   http.RoundTripper
	breaker *CircuitBreaker
}

var errServerError = errors.New("server returned 5xx")

func (t *circuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := t.breaker.Execute(req.Context(), func() error {
		r, e := t.inner.RoundTrip(req) //nolint:bodyclose // transport passes the body upstream; caller owns Close
		resp = r
		if e != nil {
			return e
		}
		if r != nil && r.StatusCode >= http.StatusInternalServerError {
			return errServerError
		}
		return nil
	})
	if errors.Is(err, errServerError) {
		return resp, nil
	}
	return resp, err
}
