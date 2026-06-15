package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

const validYAML = `
trade_sizes: ["1", "10"]
threshold_usdc: "1"
binance:
  taker_fee_bps: 10
  rate_limit_rps: 5
  rate_limit_burst: 2
  request_timeout: 10s
  breaker:
    min_requests: 20
    failure_ratio: 0.2
    cooldown: 30s
    interval: 1m
uniswap:
  taker_fee_bps: 0
  rate_limit_rps: 10
  rate_limit_burst: 10
  request_timeout: 10s
  breaker:
    min_requests: 20
    failure_ratio: 0.2
    cooldown: 30s
    interval: 1m
retry:
  max_retries: 4
  initial_wait: 500ms
  max_wait: 10s
dispatcher:
  call_timeout: 8s
subscriber:
  reconnect_initial: 1s
  reconnect_max: 30s
`

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	return path
}

func TestLoad_HappyPath(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := len(cfg.TradeSizes), 2; got != want {
		t.Errorf("trade sizes: got %d, want %d", got, want)
	}
	if !cfg.TradeSizes[1].Equal(decimal.NewFromInt(10)) {
		t.Errorf("trade_sizes[1]: got %s, want 10", cfg.TradeSizes[1])
	}
	if !cfg.ThresholdUSDC.Equal(decimal.NewFromInt(1)) {
		t.Errorf("threshold_usdc: got %s, want 1", cfg.ThresholdUSDC)
	}
	if cfg.Binance.TakerFeeBps != 10 {
		t.Errorf("binance.taker_fee_bps: got %d, want 10", cfg.Binance.TakerFeeBps)
	}
	if cfg.Binance.Breaker.Cooldown != 30*time.Second {
		t.Errorf("binance.breaker.cooldown: got %v, want 30s", cfg.Binance.Breaker.Cooldown)
	}
	if cfg.Retry.InitialWait != 500*time.Millisecond {
		t.Errorf("retry.initial_wait: got %v, want 500ms", cfg.Retry.InitialWait)
	}
	if cfg.Subscriber.ReconnectMax != 30*time.Second {
		t.Errorf("subscriber.reconnect_max: got %v, want 30s", cfg.Subscriber.ReconnectMax)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/no/such/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should mention read: %v", err)
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	// Type mismatch — `binance` declared as a scalar instead of a map.
	body := strings.Replace(
		validYAML,
		"binance:\n  taker_fee_bps: 10",
		"binance: not-a-struct",
		1,
	)
	_, err := Load(writeTempConfig(t, body))
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse: %v", err)
	}
}

func TestLoad_RejectsEmptyTradeSizes(t *testing.T) {
	body := strings.Replace(validYAML, `trade_sizes: ["1", "10"]`, `trade_sizes: []`, 1)
	_, err := Load(writeTempConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "trade_sizes") {
		t.Errorf("expected trade_sizes validation error, got %v", err)
	}
}

func TestLoad_RejectsNegativeTradeSize(t *testing.T) {
	body := strings.Replace(validYAML, `trade_sizes: ["1", "10"]`, `trade_sizes: ["1", "-5"]`, 1)
	_, err := Load(writeTempConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected positive-size validation error, got %v", err)
	}
}

func TestLoad_RejectsZeroDispatcherTimeout(t *testing.T) {
	body := strings.Replace(validYAML, "call_timeout: 8s", "call_timeout: 0s", 1)
	_, err := Load(writeTempConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "dispatcher.call_timeout") {
		t.Errorf("expected dispatcher.call_timeout validation error, got %v", err)
	}
}
