// Package config loads arbd's tunables from a YAML file. Per-venue
// rate-limit, breaker and retry knobs, trade sizes, the profitability
// threshold, and the various timeout / cooldown durations all live
// here. Credentials (RPC URLs) and runtime mode (LOG_LEVEL,
// PRETTY_ALERTS) stay in .env — config.yaml is for behavior, .env is
// for environment bindings.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// Config is the root of arbd's configuration file. See
// config.example.yaml for the canonical shape and defaults.
type Config struct {
	TradeSizes    []decimal.Decimal `yaml:"trade_sizes"`
	ThresholdUSDC decimal.Decimal   `yaml:"threshold_usdc"`

	Binance VenueConfig `yaml:"binance"`
	Uniswap VenueConfig `yaml:"uniswap"`

	Retry      RetryConfig      `yaml:"retry"`
	Dispatcher DispatcherConfig `yaml:"dispatcher"`
	Subscriber SubscriberConfig `yaml:"subscriber"`
}

// VenueConfig holds the per-venue knobs that show up on both the CEX
// and DEX sides. TakerFeeBps is only meaningful for the CEX side;
// Uniswap's pool fee is encoded on-chain.
type VenueConfig struct {
	RateLimitRPS   float64       `yaml:"rate_limit_rps"`
	RateLimitBurst int           `yaml:"rate_limit_burst"`
	Breaker        BreakerConfig `yaml:"breaker"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	TakerFeeBps    uint32        `yaml:"taker_fee_bps"`
}

// BreakerConfig parameterises the circuit breaker. See
// internal/resilience.BreakerConfig for the runtime semantics.
type BreakerConfig struct {
	MinRequests  uint32        `yaml:"min_requests"`
	FailureRatio float64       `yaml:"failure_ratio"`
	Cooldown     time.Duration `yaml:"cooldown"`
	Interval     time.Duration `yaml:"interval"`
}

// RetryConfig parameterises the HTTP transport retry layer. See
// internal/resilience.RetryConfig.
type RetryConfig struct {
	MaxRetries  int           `yaml:"max_retries"`
	InitialWait time.Duration `yaml:"initial_wait"`
	MaxWait     time.Duration `yaml:"max_wait"`
}

// DispatcherConfig parameterises the per-block fan-out.
type DispatcherConfig struct {
	CallTimeout time.Duration `yaml:"call_timeout"`
}

// SubscriberConfig parameterises the WS reconnect backoff.
type SubscriberConfig struct {
	ReconnectInitial time.Duration `yaml:"reconnect_initial"`
	ReconnectMax     time.Duration `yaml:"reconnect_max"`
}

// Load reads the YAML config file at path and decodes it into a Config.
// Validation rejects empty trade-size lists and non-positive durations;
// other fields are left to fail at use-site if obviously wrong.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if len(c.TradeSizes) == 0 {
		return errors.New("trade_sizes must be non-empty")
	}
	for i, s := range c.TradeSizes {
		if !s.IsPositive() {
			return fmt.Errorf("trade_sizes[%d] must be positive, got %s", i, s)
		}
	}
	if c.Binance.RateLimitRPS <= 0 {
		return errors.New("binance.rate_limit_rps must be positive")
	}
	if c.Uniswap.RateLimitRPS <= 0 {
		return errors.New("uniswap.rate_limit_rps must be positive")
	}
	if c.Dispatcher.CallTimeout <= 0 {
		return errors.New("dispatcher.call_timeout must be positive")
	}
	if c.Subscriber.ReconnectInitial <= 0 || c.Subscriber.ReconnectMax <= 0 {
		return errors.New("subscriber.reconnect_initial and reconnect_max must be positive")
	}
	return nil
}
