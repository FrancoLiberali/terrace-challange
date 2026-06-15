package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
)

// newTestBreaker: 2-request minimum, >50% failure ratio. Tight enough
// to trip on a small number of failures so the suite stays fast.
func newTestBreaker(cooldown time.Duration) *CircuitBreaker {
	return NewCircuitBreaker(BreakerConfig{
		Name:         "test",
		MinRequests:  2,
		FailureRatio: 0.5,
		Cooldown:     cooldown,
	})
}

func TestCircuitBreaker_TripsOnFailureRatio(t *testing.T) {
	b := newTestBreaker(time.Hour) // long cooldown so we don't accidentally half-open
	boom := errors.New("boom")

	for i := range 2 {
		if err := b.Execute(t.Context(), func() error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("call %d: expected boom, got %v", i, err)
		}
	}
	// 2/2 failures = 100% > 50% → tripped. Next call short-circuits without invoking op.
	called := false
	err := b.Execute(t.Context(), func() error {
		called = true
		return nil
	})
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Errorf("expected ErrOpenState after threshold, got %v", err)
	}
	if called {
		t.Error("op was invoked while breaker was open")
	}
}

func TestCircuitBreaker_StaysClosedBelowMinRequests(t *testing.T) {
	// MinRequests=5, but we only send 4 failures — ratio is 100% but
	// sample is below the floor, so the breaker must stay closed.
	b := NewCircuitBreaker(BreakerConfig{
		Name:         "test",
		MinRequests:  5,
		FailureRatio: 0.2,
		Cooldown:     time.Hour,
	})
	boom := errors.New("boom")

	for range 4 {
		_ = b.Execute(t.Context(), func() error { return boom })
	}
	// Next call must reach op — breaker still closed.
	called := false
	_ = b.Execute(t.Context(), func() error {
		called = true
		return nil
	})
	if !called {
		t.Error("op was not invoked; breaker must stay closed below MinRequests")
	}
}

func TestCircuitBreaker_HalfOpenAllowsProbeAfterCooldown(t *testing.T) {
	b := newTestBreaker(50 * time.Millisecond)
	boom := errors.New("boom")

	// Trip the breaker (2/2 failures, 100% > 50%).
	_ = b.Execute(t.Context(), func() error { return boom })
	_ = b.Execute(t.Context(), func() error { return boom })

	// Wait out the cooldown — breaker transitions to half-open.
	time.Sleep(100 * time.Millisecond)

	// A successful probe closes the breaker.
	if err := b.Execute(t.Context(), func() error { return nil }); err != nil {
		t.Fatalf("half-open probe should succeed: %v", err)
	}
	// Closed again — normal calls work.
	if err := b.Execute(t.Context(), func() error { return nil }); err != nil {
		t.Errorf("expected closed-state success, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenReopensOnFailure(t *testing.T) {
	b := newTestBreaker(50 * time.Millisecond)
	boom := errors.New("boom")

	_ = b.Execute(t.Context(), func() error { return boom })
	_ = b.Execute(t.Context(), func() error { return boom })
	time.Sleep(100 * time.Millisecond)

	// The half-open probe fails — breaker re-opens.
	if err := b.Execute(t.Context(), func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("probe should surface inner err, got %v", err)
	}
	// Next call before cooldown sees ErrOpenState.
	err := b.Execute(t.Context(), func() error { return nil })
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Errorf("expected ErrOpenState after re-open, got %v", err)
	}
}

func TestCircuitBreaker_OnStateChangeFires(t *testing.T) {
	var got []string
	b := NewCircuitBreaker(BreakerConfig{
		Name:         "test",
		MinRequests:  1,
		FailureRatio: 0.5,
		Cooldown:     50 * time.Millisecond,
		OnStateChange: func(_ string, from, to string) {
			got = append(got, from+"->"+to)
		},
	})
	boom := errors.New("boom")

	_ = b.Execute(context.Background(), func() error { return boom })
	if len(got) == 0 || got[0] != "closed->open" {
		t.Errorf("expected closed->open transition recorded, got %v", got)
	}
}
