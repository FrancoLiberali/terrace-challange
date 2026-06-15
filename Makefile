.DEFAULT_GOAL := help
.PHONY: help test lint run build tidy probe-binance probe-uniswap probe-chain

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

test: ## Run the full test suite with the race detector.
	go test -race ./...

lint: ## Run golangci-lint across the module.
	golangci-lint run ./...

build: ## Build all binaries into ./bin/.
	@mkdir -p bin
	go build -o bin/ ./cmd/...

run: ## Run arbd against the configured venues.
	go run ./cmd/arbd

probe-binance: ## Fetch slippage-aware Binance prices for the configured sizes.
	go run ./cmd/probe-binance

probe-uniswap: ## Fetch slippage-aware Uniswap V3 prices for the configured sizes.
	go run ./cmd/probe-uniswap

probe-chain: ## Subscribe to Ethereum newHeads and print incoming blocks.
	go run ./cmd/probe-chain

tidy: ## Run go mod tidy.
	go mod tidy
