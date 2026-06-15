package resilience

import (
	"context"
	"errors"
	"log/slog"
	"time"

	// Classic 3-state circuit breaker from Sony; minimal API, zero
	// transitive deps.
	"github.com/sony/gobreaker/v2"
)

// CircuitBreaker is a 3-state breaker (closed / open / half-open) that
// short-circuits calls to a failing dependency.
//
// Closed → Open: when the underlying gobreaker readyToTrip predicate
// fires (we use "N consecutive failures").
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
	Name             string
	ConsecutiveFails uint32        // open after this many consecutive failures
	Cooldown         time.Duration // how long to stay open before half-open
	OnStateChange    func(name string, from, to string)
}

// NewCircuitBreaker returns a breaker that opens after `cfg.ConsecutiveFails`
// consecutive failures and cools down for `cfg.Cooldown` before
// half-open. OnStateChange, if set, is invoked on every transition.
//
// context.Canceled is treated as not-a-failure (the caller decided to
// stop, e.g., SIGTERM mid-call — nothing about the dependency went
// wrong). context.DeadlineExceeded IS counted as a failure: a provider
// that doesn't respond within our configured timeout is unhealthy from
// our point of view, which is exactly what the breaker exists to track.
func NewCircuitBreaker(cfg BreakerConfig) *CircuitBreaker {
	settings := gobreaker.Settings{
		Name:    cfg.Name,
		Timeout: cfg.Cooldown,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= cfg.ConsecutiveFails
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

// ErrOpen is returned when a call is rejected because the breaker is
// open (or half-open and the probe slot is taken). Callers can check
// for it explicitly to distinguish "the dependency is failing" from
// "your call to the dependency failed."
var ErrOpen = gobreaker.ErrOpenState

// Execute runs op through the breaker. If the breaker is open the
// call is rejected immediately with ErrOpen and op is never invoked;
// the rejection is logged at DEBUG so an operator can see how many
// calls were short-circuited during an outage (state transitions
// themselves go through OnStateChange in the config). Otherwise op
// runs and its outcome is reported to the breaker.
func (b *CircuitBreaker) Execute(ctx context.Context, op func() error) error {
	_, err := b.inner.Execute(func() (any, error) {
		return nil, op()
	})
	// Normalise the half-open over-probe rejection to ErrOpen so
	// callers only need to check one sentinel.
	if errors.Is(err, gobreaker.ErrTooManyRequests) {
		err = ErrOpen
	}
	if errors.Is(err, ErrOpen) {
		slog.DebugContext(ctx, "circuit breaker rejected call", "venue", b.inner.Name())
	}
	return err
}
