// Package alert is where the bot emits arbitrage opportunities — the
// product of the detection pipeline. Sink is the injectable seam for
// future destinations (Slack, metrics, a queue). The default
// implementation, TextSink, writes a structured slog event for log
// aggregation plus an optional human-readable block to stdout.
package alert

import "github.com/FrancoLiberali/terrace-challenge/internal/arbitrage"

// Sink receives detected opportunities. Implementations must be safe
// for concurrent use if the caller emits from multiple goroutines;
// arbd today emits from a single consumer goroutine, so the contract
// is single-writer by default.
type Sink interface {
	Emit(op arbitrage.Opportunity)
}
